package graphics

// path commands
const (
	cmdMoveTo byte = iota
	cmdLineTo
	cmdCurveTo // cubic bezier: 3 coordinate pairs
	cmdClose
)

// Path stores drawing commands compactly as parallel cmd/coord arrays,
// in user space.
type Path struct {
	cmds   []byte
	coords []float64
	// current & subpath-start points for building
	curX, curY     float64
	startX, startY float64
	hasCurrent     bool
}

func (p *Path) MoveTo(x, y float64) {
	p.cmds = append(p.cmds, cmdMoveTo)
	p.coords = append(p.coords, x, y)
	p.curX, p.curY = x, y
	p.startX, p.startY = x, y
	p.hasCurrent = true
}

func (p *Path) LineTo(x, y float64) {
	if !p.hasCurrent {
		p.MoveTo(x, y)
		return
	}
	p.cmds = append(p.cmds, cmdLineTo)
	p.coords = append(p.coords, x, y)
	p.curX, p.curY = x, y
}

func (p *Path) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	if !p.hasCurrent {
		p.MoveTo(x1, y1)
	}
	p.cmds = append(p.cmds, cmdCurveTo)
	p.coords = append(p.coords, x1, y1, x2, y2, x3, y3)
	p.curX, p.curY = x3, y3
}

// CurveToV: first control point coincides with the current point (PDF "v").
func (p *Path) CurveToV(x2, y2, x3, y3 float64) {
	p.CurveTo(p.curX, p.curY, x2, y2, x3, y3)
}

// CurveToY: second control point coincides with the endpoint (PDF "y").
func (p *Path) CurveToY(x1, y1, x3, y3 float64) {
	p.CurveTo(x1, y1, x3, y3, x3, y3)
}

func (p *Path) Close() {
	if !p.hasCurrent {
		return
	}
	p.cmds = append(p.cmds, cmdClose)
	p.curX, p.curY = p.startX, p.startY
}

// Rect appends an axis-aligned rectangle subpath (PDF "re").
func (p *Path) Rect(x, y, w, h float64) {
	p.MoveTo(x, y)
	p.LineTo(x+w, y)
	p.LineTo(x+w, y+h)
	p.LineTo(x, y+h)
	p.Close()
}

// Current returns the current point.
func (p *Path) Current() (float64, float64) { return p.curX, p.curY }

func (p *Path) IsEmpty() bool { return len(p.cmds) == 0 }

// Transformed returns a copy of the path with all coordinates mapped
// through m.
func (p *Path) Transformed(m Matrix) *Path {
	out := &Path{
		cmds:   append([]byte(nil), p.cmds...),
		coords: make([]float64, len(p.coords)),
	}
	for i := 0; i+1 < len(p.coords); i += 2 {
		out.coords[i], out.coords[i+1] = m.TransformPoint(p.coords[i], p.coords[i+1])
	}
	out.curX, out.curY = m.TransformPoint(p.curX, p.curY)
	out.startX, out.startY = m.TransformPoint(p.startX, p.startY)
	out.hasCurrent = p.hasCurrent
	return out
}

// Walker receives decoded path commands in order.
type Walker interface {
	MoveTo(x, y float64)
	LineTo(x, y float64)
	CurveTo(x1, y1, x2, y2, x3, y3 float64)
	Close()
}

// Walk replays the path into w — the single traversal choke point used by
// flattening, stroking and bounds.
func (p *Path) Walk(w Walker) {
	i := 0
	for _, c := range p.cmds {
		switch c {
		case cmdMoveTo:
			w.MoveTo(p.coords[i], p.coords[i+1])
			i += 2
		case cmdLineTo:
			w.LineTo(p.coords[i], p.coords[i+1])
			i += 2
		case cmdCurveTo:
			w.CurveTo(p.coords[i], p.coords[i+1], p.coords[i+2],
				p.coords[i+3], p.coords[i+4], p.coords[i+5])
			i += 6
		case cmdClose:
			w.Close()
		}
	}
}

// Bounds returns the control-point bounding box transformed by m (a
// conservative bound: bezier control points bound the curve).
func (p *Path) Bounds(m Matrix) Rect {
	first := true
	var r Rect
	for i := 0; i+1 < len(p.coords); i += 2 {
		x, y := m.TransformPoint(p.coords[i], p.coords[i+1])
		if first {
			r = Rect{X0: x, Y0: y, X1: x, Y1: y}
			first = false
			continue
		}
		if x < r.X0 {
			r.X0 = x
		}
		if x > r.X1 {
			r.X1 = x
		}
		if y < r.Y0 {
			r.Y0 = y
		}
		if y > r.Y1 {
			r.Y1 = y
		}
	}
	return r
}

// IsAxisAlignedRect reports whether the path is exactly one axis-aligned
// rectangular subpath (used for the cheap scissor-clip fast path). The
// returned rect is in path (user) space.
func (p *Path) IsAxisAlignedRect() (Rect, bool) {
	// accept: M L L L (optional L back to start) Close, or M L L L L
	if len(p.cmds) < 4 || p.cmds[0] != cmdMoveTo {
		return Rect{}, false
	}
	var pts [][2]float64
	i := 0
	for ci, c := range p.cmds {
		switch c {
		case cmdMoveTo:
			if ci != 0 {
				return Rect{}, false // multiple subpaths
			}
			pts = append(pts, [2]float64{p.coords[i], p.coords[i+1]})
			i += 2
		case cmdLineTo:
			pts = append(pts, [2]float64{p.coords[i], p.coords[i+1]})
			i += 2
		case cmdClose:
			if ci != len(p.cmds)-1 {
				return Rect{}, false
			}
		default:
			return Rect{}, false
		}
	}
	// drop an explicit closing point equal to the start
	if len(pts) == 5 && pts[4] == pts[0] {
		pts = pts[:4]
	}
	if len(pts) != 4 {
		return Rect{}, false
	}
	// consecutive segments must alternate horizontal/vertical
	for k := 0; k < 4; k++ {
		a, b := pts[k], pts[(k+1)%4]
		if a[0] != b[0] && a[1] != b[1] {
			return Rect{}, false
		}
	}
	r := Rect{X0: pts[0][0], Y0: pts[0][1], X1: pts[0][0], Y1: pts[0][1]}
	for _, pt := range pts[1:] {
		if pt[0] < r.X0 {
			r.X0 = pt[0]
		}
		if pt[0] > r.X1 {
			r.X1 = pt[0]
		}
		if pt[1] < r.Y0 {
			r.Y0 = pt[1]
		}
		if pt[1] > r.Y1 {
			r.Y1 = pt[1]
		}
	}
	return r, true
}
