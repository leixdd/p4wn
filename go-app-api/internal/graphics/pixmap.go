package graphics

import (
	"errors"
	"image"
)

// Pixmap is the render target: interleaved 8-bit samples, color components
// first, then one alpha byte when Alpha is true.
//
// Invariant: samples are PREMULTIPLIED alpha while compositing;
// conversion to image.Image un-premultiplies as needed.
type Pixmap struct {
	X, Y    int // device-space origin of this pixmap
	W, H    int
	NColor  int // color components (1 = gray, 3 = rgb); 0 = pure alpha mask
	Alpha   bool
	Stride  int
	Samples []byte
}

// N returns total components per pixel.
func (p *Pixmap) N() int {
	n := p.NColor
	if p.Alpha {
		n++
	}
	return n
}

// MaxPixmapDim guards against pixel bombs (huge MediaBox × huge DPI).
const MaxPixmapDim = 20000

// NewPixmap allocates a zeroed pixmap.
func NewPixmap(x, y, w, h, nColor int, alpha bool) (*Pixmap, error) {
	if w <= 0 || h <= 0 || w > MaxPixmapDim || h > MaxPixmapDim {
		return nil, errors.New("graphics: pixmap dimensions out of range")
	}
	if nColor < 0 || nColor > 4 {
		return nil, errors.New("graphics: bad component count")
	}
	n := nColor
	if alpha {
		n++
	}
	if n == 0 {
		return nil, errors.New("graphics: pixmap needs color or alpha")
	}
	stride := w * n
	return &Pixmap{
		X: x, Y: y, W: w, H: h,
		NColor: nColor, Alpha: alpha,
		Stride:  stride,
		Samples: make([]byte, h*stride),
	}, nil
}

// Clear resets the pixmap to its background: transparent when it has alpha,
// white otherwise.
func (p *Pixmap) Clear() {
	if p.Alpha {
		clear(p.Samples)
		return
	}
	for i := range p.Samples {
		p.Samples[i] = 0xFF
	}
}

// Bounds returns the device-space rectangle covered.
func (p *Pixmap) Bounds() IRect {
	return IRect{X0: p.X, Y0: p.Y, X1: p.X + p.W, Y1: p.Y + p.H}
}

// ToImage converts to a stdlib image for PNG encoding. Premultiplied samples
// map directly onto image.RGBA (which is premultiplied); opaque and gray
// variants use the tighter stdlib types.
func (p *Pixmap) ToImage() image.Image {
	switch {
	case p.NColor == 3 && p.Alpha:
		img := image.NewRGBA(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			src := p.Samples[y*p.Stride : y*p.Stride+p.W*4]
			dst := img.Pix[y*img.Stride : y*img.Stride+p.W*4]
			copy(dst, src)
		}
		return img
	case p.NColor == 3:
		img := image.NewRGBA(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			src := p.Samples[y*p.Stride:]
			dst := img.Pix[y*img.Stride:]
			for x := 0; x < p.W; x++ {
				dst[x*4+0] = src[x*3+0]
				dst[x*4+1] = src[x*3+1]
				dst[x*4+2] = src[x*3+2]
				dst[x*4+3] = 0xFF
			}
		}
		return img
	case p.NColor == 1 && p.Alpha:
		img := image.NewNRGBA(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			src := p.Samples[y*p.Stride:]
			dst := img.Pix[y*img.Stride:]
			for x := 0; x < p.W; x++ {
				g, a := src[x*2], src[x*2+1]
				if a != 0 && a != 0xFF {
					g = uint8(int(g) * 0xFF / int(a)) // un-premultiply
				}
				dst[x*4+0] = g
				dst[x*4+1] = g
				dst[x*4+2] = g
				dst[x*4+3] = a
			}
		}
		return img
	case p.NColor == 1:
		img := image.NewGray(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			copy(img.Pix[y*img.Stride:y*img.Stride+p.W], p.Samples[y*p.Stride:])
		}
		return img
	default:
		// alpha-only mask: render as grayscale for debugging
		img := image.NewGray(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			copy(img.Pix[y*img.Stride:y*img.Stride+p.W], p.Samples[y*p.Stride:])
		}
		return img
	}
}
