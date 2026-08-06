package render

import (
	"math"

	"p4wn/internal/graphics"
)

const maxBezierDepth = 8

// flattener walks a path, transforms points into device space, subdivides
// beziers, and hands line segments to emit. Close() re-emits the segment
// back to the subpath start so filled shapes are always closed.
type flattener struct {
	ctm      graphics.Matrix
	flatness float64 // device-space chord tolerance
	emit     func(x0, y0, x1, y1 float64)

	curX, curY     float64
	startX, startY float64
	open           bool
}

// deviceFlatness is the device-space chord tolerance (0.3 px).
// The flattener transforms first, so we use device-space flatness directly.
const flatnessDevice = 0.3

func newFlattener(ctm graphics.Matrix, emit func(x0, y0, x1, y1 float64)) *flattener {
	return &flattener{ctm: ctm, flatness: flatnessDevice, emit: emit}
}

func (f *flattener) MoveTo(x, y float64) {
	f.closeIfOpen()
	f.curX, f.curY = f.ctm.TransformPoint(x, y)
	f.startX, f.startY = f.curX, f.curY
	f.open = true
}

func (f *flattener) LineTo(x, y float64) {
	dx, dy := f.ctm.TransformPoint(x, y)
	f.emit(f.curX, f.curY, dx, dy)
	f.curX, f.curY = dx, dy
}

func (f *flattener) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	c1x, c1y := f.ctm.TransformPoint(x1, y1)
	c2x, c2y := f.ctm.TransformPoint(x2, y2)
	ex, ey := f.ctm.TransformPoint(x3, y3)
	f.flattenCubic(f.curX, f.curY, c1x, c1y, c2x, c2y, ex, ey, 0)
	f.curX, f.curY = ex, ey
}

func (f *flattener) Close() {
	f.closeIfOpen()
}

func (f *flattener) closeIfOpen() {
	if f.open && (f.curX != f.startX || f.curY != f.startY) {
		f.emit(f.curX, f.curY, f.startX, f.startY)
		f.curX, f.curY = f.startX, f.startY
	}
	f.open = false
}

// finish closes any dangling subpath (fills treat all subpaths as closed).
func (f *flattener) finish() { f.closeIfOpen() }

// flattenCubic subdivides by de Casteljau midpoint split until the control
// points deviate from the endpoints by less than flatness.
func (f *flattener) flattenCubic(x0, y0, x1, y1, x2, y2, x3, y3 float64, depth int) {
	// termination: max control-point offset from the chord endpoints
	dmax := math.Max(
		math.Max(math.Abs(x1-x0), math.Abs(y1-y0)),
		math.Max(math.Abs(x3-x2), math.Abs(y3-y2)),
	)
	if dmax < f.flatness || depth >= maxBezierDepth {
		f.emit(x0, y0, x3, y3)
		return
	}
	// midpoint subdivision
	x01, y01 := (x0+x1)/2, (y0+y1)/2
	x12, y12 := (x1+x2)/2, (y1+y2)/2
	x23, y23 := (x2+x3)/2, (y2+y3)/2
	x012, y012 := (x01+x12)/2, (y01+y12)/2
	x123, y123 := (x12+x23)/2, (y12+y23)/2
	xm, ym := (x012+x123)/2, (y012+y123)/2
	f.flattenCubic(x0, y0, x01, y01, x012, y012, xm, ym, depth+1)
	f.flattenCubic(xm, ym, x123, y123, x23, y23, x3, y3, depth+1)
}

// FillPathIntoRasterizer flattens path (user space) through ctm into ras.
func FillPathIntoRasterizer(ras *Rasterizer, path *graphics.Path, ctm graphics.Matrix) {
	fl := newFlattener(ctm, ras.InsertLine)
	path.Walk(fl)
	fl.finish()
}

// polyline collects flattened subpaths (device space) for stroking/dashing.
type polyline struct {
	subpaths [][]point
	closed   []bool
	cur      []point
	curClose bool
}

type point struct{ x, y float64 }

func (p *polyline) MoveTo(x, y float64)  { p.flush(); p.cur = append(p.cur, point{x, y}) }
func (p *polyline) LineTo(x, y float64)  { p.cur = append(p.cur, point{x, y}) }
func (p *polyline) closeSub()            { p.curClose = true; p.flush() }
func (p *polyline) flush() {
	if len(p.cur) > 0 {
		p.subpaths = append(p.subpaths, p.cur)
		p.closed = append(p.closed, p.curClose)
		p.cur = nil
	}
	p.curClose = false
}

// flattenToPolyline flattens a path into device-space polylines, keeping
// subpath open/closed status (strokes treat them differently).
func flattenToPolyline(path *graphics.Path, ctm graphics.Matrix) *polyline {
	pl := &polyline{}
	f := &polylineFlattener{
		inner: newFlattener(ctm, func(x0, y0, x1, y1 float64) {
			pl.LineTo(x1, y1)
		}),
		pl: pl,
	}
	path.Walk(f)
	pl.flush()
	return pl
}

// polylineFlattener adapts flattener's segment output into polyline form,
// tracking moveto/close boundaries.
type polylineFlattener struct {
	inner *flattener
	pl    *polyline
}

func (p *polylineFlattener) MoveTo(x, y float64) {
	dx, dy := p.inner.ctm.TransformPoint(x, y)
	p.pl.flush()
	p.pl.MoveTo(dx, dy)
	p.inner.curX, p.inner.curY = dx, dy
	p.inner.startX, p.inner.startY = dx, dy
	p.inner.open = true
}

func (p *polylineFlattener) LineTo(x, y float64) { p.inner.LineTo(x, y) }

func (p *polylineFlattener) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	p.inner.CurveTo(x1, y1, x2, y2, x3, y3)
}

func (p *polylineFlattener) Close() {
	// snap back to subpath start, mark closed
	p.inner.curX, p.inner.curY = p.inner.startX, p.inner.startY
	p.inner.open = false
	p.pl.closeSub()
}
