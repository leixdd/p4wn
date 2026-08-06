package render

import (
	"p4wn/internal/graphics"
)

// DrawDevice rasterizes device calls into a pixmap.
//
// Clipping: axis-aligned rectangular clips intersect a scissor rect (cheap);
// arbitrary clip paths rasterize into a 1-channel alpha mask — drawing
// continues into a temporary dest copied from the parent, composited back
// through the mask on PopClip.
type DrawDevice struct {
	transform graphics.Matrix // baked page CTM (user → device)
	stack     []drawState
	ras       *Rasterizer
}

type drawState struct {
	scissor graphics.IRect
	dest    *graphics.Pixmap
	mask    *graphics.Pixmap // nil for scissor-only states
	parent  *graphics.Pixmap // composite target on PopClip
}

// NewDrawDevice creates a draw device rendering into pix. transform is
// concatenated after every operation's ctm.
func NewDrawDevice(transform graphics.Matrix, pix *graphics.Pixmap) *DrawDevice {
	return &DrawDevice{
		transform: transform,
		stack:     []drawState{{scissor: pix.Bounds(), dest: pix}},
		ras:       NewRasterizer(),
	}
}

func (d *DrawDevice) top() *drawState { return &d.stack[len(d.stack)-1] }

func (d *DrawDevice) ctm(opCTM graphics.Matrix) graphics.Matrix {
	return graphics.Concat(opCTM, d.transform)
}

func (d *DrawDevice) FillPath(path *graphics.Path, evenOdd bool, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	st := d.top()
	if st.scissor.IsEmpty() {
		return
	}
	ctm := d.ctm(opCTM)
	d.ras.Reset(st.scissor)
	FillPathIntoRasterizer(d.ras, path, ctm)
	sc := resolveColor(cs, color, alpha, st.dest.NColor)
	d.ras.Convert(evenOdd, paintSpanSolid(st.dest, sc))
}

func (d *DrawDevice) StrokePath(path *graphics.Path, ss *graphics.StrokeState, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	st := d.top()
	if st.scissor.IsEmpty() {
		return
	}
	ctm := d.ctm(opCTM)
	d.ras.Reset(st.scissor)
	StrokePathIntoRasterizer(d.ras, path, ss, ctm)
	sc := resolveColor(cs, color, alpha, st.dest.NColor)
	// stroke outlines union under nonzero
	d.ras.Convert(false, paintSpanSolid(st.dest, sc))
}

func (d *DrawDevice) ClipPath(path *graphics.Path, evenOdd bool, opCTM graphics.Matrix) {
	ctm := d.ctm(opCTM)
	st := d.top()
	if r, ok := path.IsAxisAlignedRect(); ok && ctm.IsRectilinear() {
		scissor := st.scissor.Intersect(graphics.RoundRect(ctm.TransformRect(r)))
		d.stack = append(d.stack, drawState{scissor: scissor, dest: st.dest})
		return
	}
	d.pushMaskClip(st, graphics.OuterIRect(path.Bounds(ctm)), func(mask *graphics.Pixmap) {
		d.ras.Reset(mask.Bounds())
		FillPathIntoRasterizer(d.ras, path, ctm)
		d.ras.Convert(evenOdd, paintSpanMask(mask))
	})
}

func (d *DrawDevice) ClipStrokePath(path *graphics.Path, ss *graphics.StrokeState, opCTM graphics.Matrix) {
	ctm := d.ctm(opCTM)
	st := d.top()
	bounds := path.Bounds(ctm)
	hw := ss.LineWidth*ctm.Expansion()/2 + 1
	bounds.X0 -= hw
	bounds.Y0 -= hw
	bounds.X1 += hw
	bounds.Y1 += hw
	d.pushMaskClip(st, graphics.OuterIRect(bounds), func(mask *graphics.Pixmap) {
		d.ras.Reset(mask.Bounds())
		StrokePathIntoRasterizer(d.ras, path, ss, ctm)
		d.ras.Convert(false, paintSpanMask(mask))
	})
}

// pushMaskClip allocates the mask + temp dest pair and runs rasterize to
// fill the mask.
func (d *DrawDevice) pushMaskClip(st *drawState, area graphics.IRect, rasterize func(mask *graphics.Pixmap)) {
	bbox := st.scissor.Intersect(area)
	if bbox.IsEmpty() {
		d.stack = append(d.stack, drawState{scissor: bbox, dest: st.dest})
		return
	}
	mask, err := graphics.NewPixmap(bbox.X0, bbox.Y0, bbox.Width(), bbox.Height(), 0, true)
	if err != nil {
		// mask too large: degrade to bbox scissor
		d.stack = append(d.stack, drawState{scissor: bbox, dest: st.dest})
		return
	}
	temp, err := graphics.NewPixmap(bbox.X0, bbox.Y0, bbox.Width(), bbox.Height(),
		st.dest.NColor, st.dest.Alpha)
	if err != nil {
		d.stack = append(d.stack, drawState{scissor: bbox, dest: st.dest})
		return
	}
	copyRegion(temp, st.dest)
	rasterize(mask)
	d.stack = append(d.stack, drawState{scissor: bbox, dest: temp, mask: mask, parent: st.dest})
}

func (d *DrawDevice) PopClip() {
	if len(d.stack) <= 1 {
		return
	}
	st := d.top()
	if st.mask != nil {
		compositeThroughMask(st.parent, st.dest, st.mask)
	}
	d.stack = d.stack[:len(d.stack)-1]
}

func (d *DrawDevice) FillImage(img *Image, opCTM graphics.Matrix, alpha float64) {
	st := d.top()
	drawImage(st.dest, st.scissor, img, d.ctm(opCTM), alpha, nil)
}

func (d *DrawDevice) FillImageMask(img *Image, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	st := d.top()
	sc := resolveColor(cs, color, 1, 3) // keep rgb; drawImage does gray luma
	sc.alpha = 255
	drawImage(st.dest, st.scissor, img, d.ctm(opCTM), alpha, &sc)
}

func (d *DrawDevice) FillText(t *Text, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	st := d.top()
	if st.scissor.IsEmpty() {
		return
	}
	sc := resolveColor(cs, color, alpha, st.dest.NColor)
	for _, g := range t.Glyphs {
		if g.Path == nil {
			continue
		}
		ctm := d.ctm(graphics.Concat(g.Trm, opCTM))
		d.ras.Reset(st.scissor)
		FillPathIntoRasterizer(d.ras, g.Path, ctm)
		d.ras.Convert(false, paintSpanSolid(st.dest, sc))
	}
}

func (d *DrawDevice) StrokeText(t *Text, ss *graphics.StrokeState, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	st := d.top()
	if st.scissor.IsEmpty() {
		return
	}
	sc := resolveColor(cs, color, alpha, st.dest.NColor)
	for _, g := range t.Glyphs {
		if g.Path == nil {
			continue
		}
		// the line width lives in USER space: pre-transform the glyph into
		// user space instead of folding Trm into the stroking ctm
		userPath := g.Path.Transformed(g.Trm)
		d.ras.Reset(st.scissor)
		StrokePathIntoRasterizer(d.ras, userPath, ss, d.ctm(opCTM))
		d.ras.Convert(false, paintSpanSolid(st.dest, sc))
	}
}

func (d *DrawDevice) Close() {
	// composite any unbalanced clip states back down
	for len(d.stack) > 1 {
		d.PopClip()
	}
}

// --- mask plumbing -----------------------------------------------------------

// paintSpanMask writes rasterizer coverage into an alpha-only pixmap
// (max-combining so overlapping subpaths keep full coverage).
func paintSpanMask(mask *graphics.Pixmap) SpanPainter {
	return func(y, x0 int, cover []uint8) {
		if y < mask.Y || y >= mask.Y+mask.H {
			return
		}
		row := mask.Samples[(y-mask.Y)*mask.Stride:]
		for i, c := range cover {
			x := x0 + i - mask.X
			if x < 0 || x >= mask.W {
				continue
			}
			if c > row[x] {
				row[x] = c
			}
		}
	}
}

// copyRegion copies the overlapping area of src into dst (same component
// layout assumed).
func copyRegion(dst, src *graphics.Pixmap) {
	n := dst.N()
	b := dst.Bounds().Intersect(src.Bounds())
	for y := b.Y0; y < b.Y1; y++ {
		srow := src.Samples[(y-src.Y)*src.Stride:]
		drow := dst.Samples[(y-dst.Y)*dst.Stride:]
		copy(drow[(b.X0-dst.X)*n:(b.X1-dst.X)*n], srow[(b.X0-src.X)*n:(b.X1-src.X)*n])
	}
}

// compositeThroughMask blends temp over parent using mask alpha:
// parent = temp·a + parent·(1−a).
func compositeThroughMask(parent, temp, mask *graphics.Pixmap) {
	n := parent.N()
	b := parent.Bounds().Intersect(temp.Bounds()).Intersect(mask.Bounds())
	for y := b.Y0; y < b.Y1; y++ {
		prow := parent.Samples[(y-parent.Y)*parent.Stride:]
		trow := temp.Samples[(y-temp.Y)*temp.Stride:]
		mrow := mask.Samples[(y-mask.Y)*mask.Stride:]
		for x := b.X0; x < b.X1; x++ {
			a := int(mrow[x-mask.X])
			if a == 0 {
				continue
			}
			po := (x - parent.X) * n
			to := (x - temp.X) * n
			if a == 255 {
				copy(prow[po:po+n], trow[to:to+n])
				continue
			}
			inv := 255 - a
			for c := 0; c < n; c++ {
				prow[po+c] = uint8(mul255(int(trow[to+c]), a) + mul255(int(prow[po+c]), inv))
			}
		}
	}
}
