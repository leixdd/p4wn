// Package content interprets PDF page content streams and drives a render
// device. RenderPage is the top-level page → pixmap entry point.
package content

import (
	"context"
	"fmt"
	"math"

	"p4wn/internal/graphics"
	"p4wn/internal/pdf"
	"p4wn/internal/render"
)

// DefaultMaxPagePixels caps the page pixmap area when RenderOptions.MaxPixels
// is 0: 80 MP is ~240 MB as RGB, plus ~320 MB more while converting to an
// image for PNG encoding (A1 at ~320 dpi, A0 at ~227 dpi).
const DefaultMaxPagePixels = 80_000_000

// RenderOptions controls one page render.
type RenderOptions struct {
	DPI   float64 // output resolution; 72 = 1 PDF unit per pixel
	Gray  bool    // grayscale output instead of RGB
	Alpha bool    // transparent background instead of white

	// MaxPixels fails the page, before allocating, when its pixmap would
	// exceed this many pixels; 0 = DefaultMaxPagePixels.
	MaxPixels int

	// Warn, when non-nil, is called (on the rendering goroutine) for each
	// image that could not be decoded and was left out of the page.
	Warn func(error)
}

// PageCTM builds the matrix mapping PDF user space to device pixels for the
// given page: scale by dpi/72, apply /Rotate, then flip y (PDF origin is
// bottom-left, images are top-down) and translate the crop box to (0,0).
func PageCTM(page *pdf.Page, dpi float64) (graphics.Matrix, graphics.IRect) {
	zoom := dpi / 72.0
	box := graphics.Rect{
		X0: page.CropBox[0], Y0: page.CropBox[1],
		X1: page.CropBox[2], Y1: page.CropBox[3],
	}
	// rotate about origin, then scale, then y-flip
	m := graphics.Rotate(float64(page.Rotate))
	m = graphics.Concat(m, graphics.Scale(zoom, -zoom))
	r := m.TransformRect(box)
	// shift so the transformed box's top-left corner is (0,0)
	m = graphics.Concat(m, graphics.Translate(-r.X0, -r.Y0))
	bbox := graphics.RoundRect(graphics.Rect{X0: 0, Y0: 0, X1: r.Width(), Y1: r.Height()})
	return m, bbox
}

// RenderPage renders one page to a fresh pixmap: build the page CTM, clear
// the canvas, and run the content stream through a DrawDevice.
func RenderPage(ctx context.Context, doc *pdf.Document, pageNum int, opts RenderOptions) (*graphics.Pixmap, error) {
	if opts.DPI <= 0 {
		opts.DPI = 150
	}
	page, err := doc.GetPage(pageNum)
	if err != nil {
		return nil, err
	}
	ctm, bbox := PageCTM(page, opts.DPI)
	if bbox.IsEmpty() {
		return nil, fmt.Errorf("content: page %d has an empty box", pageNum+1)
	}
	maxPx := opts.MaxPixels
	if maxPx <= 0 {
		maxPx = DefaultMaxPagePixels
	}
	if px := int64(bbox.Width()) * int64(bbox.Height()); px > int64(maxPx) {
		fit := opts.DPI * math.Sqrt(float64(maxPx)/float64(px))
		return nil, fmt.Errorf("content: page %d would be %dx%d px (%.1f MP) at %g dpi, over the %.1f MP page limit; use dpi <= %d",
			pageNum+1, bbox.Width(), bbox.Height(), float64(px)/1e6, opts.DPI, float64(maxPx)/1e6, int(fit))
	}
	nColor := 3
	if opts.Gray {
		nColor = 1
	}
	pix, err := graphics.NewPixmap(0, 0, bbox.Width(), bbox.Height(), nColor, opts.Alpha)
	if err != nil {
		return nil, err
	}
	pix.Clear()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	data, err := page.ContentStreams()
	if err != nil {
		return nil, err
	}
	dev := render.NewDrawDevice(ctm, pix)
	defer dev.Close()
	if err := Run(ctx, doc, page.Resources, data, dev, opts.Warn); err != nil {
		return nil, err
	}
	return pix, nil
}
