package render

import (
	"math"

	"p4wn/internal/graphics"
)

// StrokePathIntoRasterizer converts a stroked path into outline polygons and
// inserts them into ras. Strategy: each segment becomes a quad, each join a
// wedge, each cap a fan; every polygon is oriented consistently so the
// nonzero fill rule yields their union.
func StrokePathIntoRasterizer(ras *Rasterizer, path *graphics.Path, ss *graphics.StrokeState, ctm graphics.Matrix) {
	pl := flattenToPolyline(path, ctm)

	// line width in device space
	hw := ss.LineWidth * ctm.Expansion() / 2
	// hairline minimum: keep strokes visible (~1px total width)
	if hw < 0.5 {
		hw = 0.5
	}

	if len(ss.Dashes) > 0 {
		pl = applyDashes(pl, ss, ctm)
	}

	st := &stroker{ras: ras, hw: hw, ss: ss}
	for i, pts := range pl.subpaths {
		st.strokeSubpath(pts, pl.closed[i])
	}
}

type stroker struct {
	ras *Rasterizer
	hw  float64
	ss  *graphics.StrokeState
}

// strokeSubpath emits geometry for one polyline.
func (s *stroker) strokeSubpath(pts []point, closed bool) {
	pts = dropRepeats(pts)
	if len(pts) == 0 {
		return
	}
	if len(pts) == 1 || (closed && len(pts) == 2 && pts[0] == pts[1]) {
		// degenerate subpath: round/square caps draw a dot
		s.dot(pts[0])
		return
	}
	if closed && pts[len(pts)-1] == pts[0] {
		pts = pts[:len(pts)-1]
		if len(pts) < 2 {
			s.dot(pts[0])
			return
		}
	}

	n := len(pts)
	segEnd := n - 1
	if closed {
		segEnd = n // wrap-around segment n-1 → 0
	}
	// segment quads
	for i := 0; i < segEnd; i++ {
		a, b := pts[i], pts[(i+1)%n]
		s.segmentQuad(a, b)
	}
	// joins at interior vertices
	jEnd := n - 1
	jStart := 1
	if closed {
		jStart, jEnd = 0, n // every vertex joins
	}
	for i := jStart; i < jEnd; i++ {
		prev := pts[(i-1+n)%n]
		cur := pts[i]
		next := pts[(i+1)%n]
		s.join(prev, cur, next)
	}
	if !closed {
		// caps at both ends
		s.cap(pts[1], pts[0])     // start cap faces backwards
		s.cap(pts[n-2], pts[n-1]) // end cap faces forwards
	}
}

func dropRepeats(pts []point) []point {
	out := pts[:0:0]
	for _, p := range pts {
		if len(out) == 0 || out[len(out)-1] != p {
			out = append(out, p)
		}
	}
	return out
}

// segmentQuad emits the rectangle covering a stroked segment.
func (s *stroker) segmentQuad(a, b point) {
	nx, ny, ok := normal(a, b, s.hw)
	if !ok {
		return
	}
	s.polygon([]point{
		{a.x + nx, a.y + ny},
		{b.x + nx, b.y + ny},
		{b.x - nx, b.y - ny},
		{a.x - nx, a.y - ny},
	})
}

// join fills the outer wedge between segments (prev→cur) and (cur→next).
func (s *stroker) join(prev, cur, next point) {
	n1x, n1y, ok1 := normal(prev, cur, s.hw)
	n2x, n2y, ok2 := normal(cur, next, s.hw)
	if !ok1 || !ok2 {
		return
	}
	d1x, d1y := cur.x-prev.x, cur.y-prev.y
	d2x, d2y := next.x-cur.x, next.y-cur.y
	cross := d1x*d2y - d1y*d2x
	if cross == 0 {
		return // collinear: quads already overlap
	}
	// pick the outer side of the turn. normal() returns the left normal
	// (up for rightward travel in y-down space); cross > 0 turns away from
	// it, so the left side is outer. cross < 0 → outer is the right side.
	if cross < 0 {
		n1x, n1y = -n1x, -n1y
		n2x, n2y = -n2x, -n2y
	}
	p1 := point{cur.x + n1x, cur.y + n1y}
	p2 := point{cur.x + n2x, cur.y + n2y}

	switch s.ss.LineJoin {
	case graphics.JoinRound:
		s.arcFan(cur, p1, p2)
	case graphics.JoinMiter:
		// miter point: intersection of the two outer edges. Using the
		// half-angle formula: m = (n1+n2) scaled by hw²/|((n1+n2)/2)|².
		mx, my := (n1x+n2x)/2, (n1y+n2y)/2
		len2 := mx*mx + my*my
		if len2 < 1e-12 {
			s.polygon([]point{cur, p1, p2})
			return
		}
		scale := (s.hw * s.hw) / len2
		miter := point{cur.x + mx*scale, cur.y + my*scale}
		// miter length test: |miter-cur| / hw <= miterlimit
		ml := s.ss.MiterLimit
		if ml <= 0 {
			ml = 10
		}
		mdx, mdy := miter.x-cur.x, miter.y-cur.y
		if mdx*mdx+mdy*mdy > ml*ml*s.hw*s.hw {
			s.polygon([]point{cur, p1, p2}) // bevel fallback
			return
		}
		s.polygon([]point{cur, p1, miter, p2})
	default: // bevel
		s.polygon([]point{cur, p1, p2})
	}
}

// cap emits the line cap at end point b of a segment a→b.
func (s *stroker) cap(a, b point) {
	nx, ny, ok := normal(a, b, s.hw)
	if !ok {
		return
	}
	switch s.ss.LineCap {
	case graphics.CapRound:
		s.arcFan(b, point{b.x + nx, b.y + ny}, point{b.x - nx, b.y - ny})
	case graphics.CapSquare:
		// extend by half-width along the direction
		dx, dy := b.x-a.x, b.y-a.y
		l := math.Hypot(dx, dy)
		ex, ey := dx/l*s.hw, dy/l*s.hw
		s.polygon([]point{
			{b.x + nx, b.y + ny},
			{b.x + nx + ex, b.y + ny + ey},
			{b.x - nx + ex, b.y - ny + ey},
			{b.x - nx, b.y - ny},
		})
	}
	// butt: nothing
}

// dot draws a filled circle (round/square caps on zero-length subpaths).
func (s *stroker) dot(c point) {
	switch s.ss.LineCap {
	case graphics.CapRound:
		s.circle(c, s.hw)
	case graphics.CapSquare:
		s.polygon([]point{
			{c.x - s.hw, c.y - s.hw},
			{c.x + s.hw, c.y - s.hw},
			{c.x + s.hw, c.y + s.hw},
			{c.x - s.hw, c.y + s.hw},
		})
	}
}

// arcFan fills the pie wedge centered at c from rim point p1 to rim point
// p2, going the short way around.
func (s *stroker) arcFan(c, p1, p2 point) {
	a1 := math.Atan2(p1.y-c.y, p1.x-c.x)
	a2 := math.Atan2(p2.y-c.y, p2.x-c.x)
	da := a2 - a1
	for da > math.Pi {
		da -= 2 * math.Pi
	}
	for da < -math.Pi {
		da += 2 * math.Pi
	}
	r := s.hw
	// arc step from flatness: theta = 2·√2·√(flatness/r)
	step := 2 * math.Sqrt2 * math.Sqrt(flatnessDevice/math.Max(r, 1e-6))
	steps := int(math.Ceil(math.Abs(da) / step))
	if steps < 1 {
		steps = 1
	}
	if steps > 64 {
		steps = 64
	}
	pts := make([]point, 0, steps+2)
	pts = append(pts, c)
	for i := 0; i <= steps; i++ {
		a := a1 + da*float64(i)/float64(steps)
		pts = append(pts, point{c.x + r*math.Cos(a), c.y + r*math.Sin(a)})
	}
	s.polygon(pts)
}

func (s *stroker) circle(c point, r float64) {
	step := 2 * math.Sqrt2 * math.Sqrt(flatnessDevice/math.Max(r, 1e-6))
	steps := int(math.Ceil(2 * math.Pi / step))
	if steps < 8 {
		steps = 8
	}
	if steps > 128 {
		steps = 128
	}
	pts := make([]point, 0, steps)
	for i := 0; i < steps; i++ {
		a := 2 * math.Pi * float64(i) / float64(steps)
		pts = append(pts, point{c.x + r*math.Cos(a), c.y + r*math.Sin(a)})
	}
	s.polygon(pts)
}

// polygon inserts a closed polygon with consistent (positive-area)
// orientation so overlapping stroke pieces union under the nonzero rule.
func (s *stroker) polygon(pts []point) {
	if len(pts) < 3 {
		return
	}
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].x*pts[j].y - pts[j].x*pts[i].y
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	for i := range pts {
		j := (i + 1) % len(pts)
		s.ras.InsertLine(pts[i].x, pts[i].y, pts[j].x, pts[j].y)
	}
}

// normal returns the half-width normal of segment a→b; ok is false for
// zero-length segments.
func normal(a, b point, hw float64) (nx, ny float64, ok bool) {
	dx, dy := b.x-a.x, b.y-a.y
	l := math.Hypot(dx, dy)
	if l < 1e-9 {
		return 0, 0, false
	}
	return dy / l * hw, -dx / l * hw, true
}
