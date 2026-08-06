// Package render rasterizes device-space geometry into pixmaps:
// flatten → edge list → scanline coverage → composite.
package render

import (
	"math"
	"sort"

	"p4wn/internal/graphics"
)

// Anti-aliasing supersampling factors. 17×15 = 255 subpixel samples per
// pixel, so accumulated coverage is exactly an 8-bit alpha (linear area
// coverage, no gamma).
const (
	hscale = 17
	vscale = 15
)

// subpixel coordinate clamp (avoids int overflow on wild coordinates)
const coordLimit = 1 << 24

// edge is one y-monotonic line in subpixel space, stepped in 32.32 fixed
// point per sub-scanline.
type edge struct {
	x    int64 // current x, 32.32 fixed point, in subpixel units
	dxdy int64 // x increment per sub-scanline, 32.32
	y0   int   // first sub-scanline (inclusive)
	h    int   // remaining sub-scanlines
	dir  int8  // +1 down, -1 up (winding)
}

// Rasterizer accumulates edges for one fill and converts them to coverage
// spans. Reusable via Reset.
type Rasterizer struct {
	edges []edge
	clip  graphics.IRect // device pixels
	ymin  int            // sub-scanline bounds of inserted edges
	ymax  int
	xmin  int // subpixel bounds
	xmax  int

	// scratch buffers reused across Convert calls
	active []int
	deltas []int
	cover  []uint8
}

func NewRasterizer() *Rasterizer { return &Rasterizer{} }

// Reset prepares for a new shape clipped to the given pixel rect.
func (r *Rasterizer) Reset(clip graphics.IRect) {
	r.edges = r.edges[:0]
	r.clip = clip
	r.ymin = math.MaxInt
	r.ymax = math.MinInt
	r.xmin = math.MaxInt
	r.xmax = math.MinInt
}

// InsertLine adds one line segment in device pixel coordinates.
func (r *Rasterizer) InsertLine(fx0, fy0, fx1, fy1 float64) {
	// scale to subpixels
	x0 := clampF(fx0*hscale, -coordLimit, coordLimit)
	y0 := clampF(fy0*vscale, -coordLimit, coordLimit)
	x1 := clampF(fx1*hscale, -coordLimit, coordLimit)
	y1 := clampF(fy1*vscale, -coordLimit, coordLimit)

	dir := int8(1)
	if y0 > y1 {
		x0, y0, x1, y1 = x1, y1, x0, y0
		dir = -1
	}
	// clip vertically to the pixel clip box (in sub-scanlines)
	clipY0 := float64(r.clip.Y0 * vscale)
	clipY1 := float64(r.clip.Y1 * vscale)
	if y1 <= clipY0 || y0 >= clipY1 {
		return
	}
	if y0 < clipY0 {
		x0 += (x1 - x0) * (clipY0 - y0) / (y1 - y0)
		y0 = clipY0
	}
	if y1 > clipY1 {
		x1 += (x0 - x1) * (y1 - clipY1) / (y1 - y0)
		y1 = clipY1
	}

	iy0 := int(math.Floor(y0))
	iy1 := int(math.Floor(y1))
	h := iy1 - iy0
	if h <= 0 {
		return // horizontal (within one sub-scanline): contributes nothing
	}
	dxdy := (x1 - x0) / (y1 - y0)
	// sample x at the vertical center of each sub-scanline
	xAtIy0 := x0 + dxdy*(float64(iy0)+0.5-y0)

	e := edge{
		x:    int64(xAtIy0 * (1 << 32)),
		dxdy: int64(dxdy * (1 << 32)),
		y0:   iy0,
		h:    h,
		dir:  dir,
	}
	r.edges = append(r.edges, e)
	if iy0 < r.ymin {
		r.ymin = iy0
	}
	if iy1 > r.ymax {
		r.ymax = iy1
	}
	lo, hi := int(math.Floor(math.Min(x0, x1))), int(math.Ceil(math.Max(x0, x1)))
	if lo < r.xmin {
		r.xmin = lo
	}
	if hi > r.xmax {
		r.xmax = hi
	}
}

func clampF(v, lo, hi float64) float64 {
	if v != v { // NaN
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Bounds returns the pixel bbox the inserted edges may touch, intersected
// with the clip.
func (r *Rasterizer) Bounds() graphics.IRect {
	if len(r.edges) == 0 {
		return graphics.IRect{}
	}
	b := graphics.IRect{
		X0: floorDiv(r.xmin, hscale),
		Y0: floorDiv(r.ymin, vscale),
		X1: floorDiv(r.xmax, hscale) + 1,
		Y1: floorDiv(r.ymax, vscale) + 1,
	}
	return b.Intersect(r.clip)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// SpanPainter receives one output row's coverage: cover[i] is the 0..255
// alpha of pixel (x0+i, y).
type SpanPainter func(y, x0 int, cover []uint8)

// Convert sweeps the edge list and emits per-row coverage to paint.
// evenOdd selects the fill rule.
func (r *Rasterizer) Convert(evenOdd bool, paint SpanPainter) {
	if len(r.edges) == 0 {
		return
	}
	bounds := r.Bounds()
	if bounds.IsEmpty() {
		return
	}
	// sort by starting sub-scanline
	sort.Slice(r.edges, func(i, j int) bool { return r.edges[i].y0 < r.edges[j].y0 })

	w := bounds.Width()
	if cap(r.deltas) < w+2 {
		r.deltas = make([]int, w+2)
		r.cover = make([]uint8, w)
	}
	deltas := r.deltas[:w+2]
	cover := r.cover[:w]

	r.active = r.active[:0]
	nextEdge := 0
	clipX0sub := bounds.X0 * hscale
	clipX1sub := bounds.X1 * hscale

	for y := bounds.Y0; y < bounds.Y1; y++ {
		for i := range deltas {
			deltas[i] = 0
		}
		rowHasInk := false
		for sub := 0; sub < vscale; sub++ {
			sy := y*vscale + sub
			// activate edges starting at or before sy
			for nextEdge < len(r.edges) && r.edges[nextEdge].y0 <= sy {
				e := &r.edges[nextEdge]
				if e.y0 < sy {
					// starts above current row (can happen after bounds
					// clamp): fast-forward
					skip := sy - e.y0
					if skip >= e.h {
						nextEdge++
						continue
					}
					e.x += e.dxdy * int64(skip)
					e.h -= skip
					e.y0 = sy
				}
				r.active = append(r.active, nextEdge)
				nextEdge++
			}
			if len(r.active) == 0 {
				continue
			}
			// drop dead edges
			na := r.active[:0]
			for _, idx := range r.active {
				if r.edges[idx].h > 0 {
					na = append(na, idx)
				}
			}
			r.active = na
			if len(r.active) == 0 {
				continue
			}
			// sort active by current x (insertion sort: nearly sorted)
			for i := 1; i < len(r.active); i++ {
				for j := i; j > 0 && r.edges[r.active[j]].x < r.edges[r.active[j-1]].x; j-- {
					r.active[j], r.active[j-1] = r.active[j-1], r.active[j]
				}
			}
			// walk winding transitions → spans
			winding := 0
			spanStart := 0
			for _, idx := range r.active {
				e := &r.edges[idx]
				x := int(e.x >> 32)
				inside := winding != 0
				if evenOdd {
					inside = winding&1 != 0
				}
				if !inside {
					spanStart = x
				}
				winding += int(e.dir)
				insideAfter := winding != 0
				if evenOdd {
					insideAfter = winding&1 != 0
				}
				if inside && !insideAfter {
					x0, x1 := spanStart, x
					if x0 < clipX0sub {
						x0 = clipX0sub
					}
					if x1 > clipX1sub {
						x1 = clipX1sub
					}
					if x1 > x0 {
						addSpan(deltas, x0-clipX0sub, x1-clipX0sub)
						rowHasInk = true
					}
				}
			}
			// step all active edges to the next sub-scanline
			for _, idx := range r.active {
				e := &r.edges[idx]
				e.x += e.dxdy
				e.h--
			}
		}
		if !rowHasInk {
			continue
		}
		// prefix-sum deltas → subpixel coverage → 8-bit alpha.
		// hscale*vscale = 255, so coverage IS the alpha value.
		acc := 0
		x0 := -1
		x1 := 0
		for i := 0; i < w; i++ {
			acc += deltas[i]
			c := acc
			if c < 0 {
				c = 0
			} else if c > 255 {
				c = 255
			}
			cover[i] = uint8(c)
			if c > 0 {
				if x0 < 0 {
					x0 = i
				}
				x1 = i + 1
			}
		}
		if x0 >= 0 {
			paint(y, bounds.X0+x0, cover[x0:x1])
		}
	}
}

// addSpan accumulates the coverage derivative for subpixel span [x0, x1)
// within one sub-scanline. Prefix-summing yields per-pixel coverage:
// full interior pixels gain hscale per sub-scanline.
func addSpan(deltas []int, x0, x1 int) {
	p0, r0 := x0/hscale, x0%hscale
	p1, r1 := x1/hscale, x1%hscale
	// works for p0 == p1 too (contributions combine to x1-x0)
	deltas[p0] += hscale - r0
	deltas[p0+1] += r0
	deltas[p1] += r1 - hscale
	deltas[p1+1] -= r1
}
