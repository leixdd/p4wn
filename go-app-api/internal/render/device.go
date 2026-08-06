package render

import (
	"p4wn/internal/graphics"
)

// Device is the seam between the content interpreter and any renderer.
// The interpreter emits these calls; implementations rasterize (DrawDevice),
// trace (test devices), or measure.
type Device interface {
	FillPath(path *graphics.Path, evenOdd bool, ctm graphics.Matrix,
		cs graphics.Colorspace, color []float64, alpha float64)
	StrokePath(path *graphics.Path, ss *graphics.StrokeState, ctm graphics.Matrix,
		cs graphics.Colorspace, color []float64, alpha float64)
	ClipPath(path *graphics.Path, evenOdd bool, ctm graphics.Matrix)
	ClipStrokePath(path *graphics.Path, ss *graphics.StrokeState, ctm graphics.Matrix)
	PopClip()

	FillImage(img *Image, ctm graphics.Matrix, alpha float64)
	FillImageMask(img *Image, ctm graphics.Matrix,
		cs graphics.Colorspace, color []float64, alpha float64)

	FillText(t *Text, ctm graphics.Matrix,
		cs graphics.Colorspace, color []float64, alpha float64)
	StrokeText(t *Text, ss *graphics.StrokeState, ctm graphics.Matrix,
		cs graphics.Colorspace, color []float64, alpha float64)

	Close()
}

// BaseDevice is a no-op Device for embedding: partial devices override what
// they need.
type BaseDevice struct{}

func (BaseDevice) FillPath(*graphics.Path, bool, graphics.Matrix, graphics.Colorspace, []float64, float64) {
}
func (BaseDevice) StrokePath(*graphics.Path, *graphics.StrokeState, graphics.Matrix, graphics.Colorspace, []float64, float64) {
}
func (BaseDevice) ClipPath(*graphics.Path, bool, graphics.Matrix)                        {}
func (BaseDevice) ClipStrokePath(*graphics.Path, *graphics.StrokeState, graphics.Matrix) {}
func (BaseDevice) PopClip()                                                              {}
func (BaseDevice) FillImage(*Image, graphics.Matrix, float64)                            {}
func (BaseDevice) FillImageMask(*Image, graphics.Matrix, graphics.Colorspace, []float64, float64) {
}
func (BaseDevice) FillText(*Text, graphics.Matrix, graphics.Colorspace, []float64, float64) {}
func (BaseDevice) StrokeText(*Text, *graphics.StrokeState, graphics.Matrix, graphics.Colorspace, []float64, float64) {
}
func (BaseDevice) Close() {}

// Image is a decoded renderer-side image: W×H straight-alpha interleaved
// samples.
type Image struct {
	W, H        int
	NColor      int  // 1 = gray, 3 = rgb
	Alpha       bool // has alpha channel appended per pixel
	Interpolate bool // /Interpolate: smooth when scaling up
	Samples     []byte
}

// Text is a positioned glyph run with optional semantic Unicode.
type Text struct {
	Glyphs   []Glyph
	FontFace string  // CSS font-family id; empty when unknown
	FontSize float64 // PDF text size in user units (approximate)
	FontData []byte  // embedded font bytes for @font-face, optional
	FontMIME string  // MIME for FontData
}

// Glyph is one positioned glyph: its outline path is in em units; Trm maps
// em space to user space. Unicode is set when a reliable mapping exists.
type Glyph struct {
	Path    *graphics.Path
	Trm     graphics.Matrix
	Unicode string // empty when unmapped
}
