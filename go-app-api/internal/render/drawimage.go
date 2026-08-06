package render

import (
	"math"

	"p4wn/internal/graphics"
)

// drawImage maps the image's unit square through ctm onto the pixmap using
// inverse-mapped bilinear sampling. When maskColor is non-nil the image is a
// stencil/alpha source painted in that solid color (FillImageMask).
func drawImage(pix *graphics.Pixmap, scissor graphics.IRect, img *Image,
	ctm graphics.Matrix, alpha float64, maskColor *solidColor) {
	if img == nil || img.W <= 0 || img.H <= 0 {
		return
	}
	inv, ok := ctm.Invert()
	if !ok {
		return
	}
	// device bbox of the unit square
	area := ctm.TransformRect(graphics.Rect{X0: 0, Y0: 0, X1: 1, Y1: 1})
	bbox := graphics.OuterIRect(area).Intersect(scissor).Intersect(pix.Bounds())
	if bbox.IsEmpty() {
		return
	}

	// heavy downscale: box-prescale so sampling doesn't alias
	src := img
	devW := math.Hypot(ctm.A, ctm.B)
	devH := math.Hypot(ctm.C, ctm.D)
	fx := float64(img.W) / math.Max(devW, 1)
	fy := float64(img.H) / math.Max(devH, 1)
	downscaling := fx > 1.2 || fy > 1.2
	if fx > 2 || fy > 2 {
		src = boxReduce(img, int(fx/2)+1, int(fy/2)+1)
	}
	// spec: smooth upscaling only with /Interpolate; downscales always
	// filter (the prescale + bilinear tail keeps them alias-free)
	smooth := img.Interpolate || downscaling

	ga := int(clampF(alpha, 0, 1)*255 + 0.5)
	n := pix.N()
	nc := pix.NColor
	sn := src.N()

	for y := bbox.Y0; y < bbox.Y1; y++ {
		row := pix.Samples[(y-pix.Y)*pix.Stride:]
		for x := bbox.X0; x < bbox.X1; x++ {
			// device pixel center → unit square
			u, v := inv.TransformPoint(float64(x)+0.5, float64(y)+0.5)
			if u < 0 || u >= 1 || v < 0 || v >= 1 {
				continue
			}
			// image row 0 is the TOP of the unit square (v = 1)
			sx := u*float64(src.W) - 0.5
			sy := (1-v)*float64(src.H) - 0.5
			var comps [4]int
			if smooth {
				bilinear(src, sx, sy, sn, &comps)
			} else {
				nearest(src, u, 1-v, sn, &comps)
			}

			// resolve source color + alpha in pixmap terms
			var cr, cg, cb, ca int
			if maskColor != nil {
				// stencil: sample IS the coverage
				ca = comps[0]
				cr, cg, cb = int(maskColor.comps[0]), int(maskColor.comps[1]), int(maskColor.comps[2])
				ca = mul255(ca, int(maskColor.alpha))
			} else {
				switch src.NColor {
				case 1:
					cr, cg, cb = comps[0], comps[0], comps[0]
				default:
					cr, cg, cb = comps[0], comps[1], comps[2]
				}
				ca = 255
				if src.Alpha {
					ca = comps[src.NColor]
				}
			}
			ea := mul255(ca, ga)
			if ea == 0 {
				continue
			}
			o := (x - pix.X) * n
			inv255 := 255 - ea
			if nc == 1 {
				g := (cr*77 + cg*151 + cb*28) >> 8 // luma
				row[o] = uint8(mul255(g, ea) + mul255(int(row[o]), inv255))
			} else {
				row[o+0] = uint8(mul255(cr, ea) + mul255(int(row[o+0]), inv255))
				row[o+1] = uint8(mul255(cg, ea) + mul255(int(row[o+1]), inv255))
				row[o+2] = uint8(mul255(cb, ea) + mul255(int(row[o+2]), inv255))
			}
			if pix.Alpha {
				row[o+nc] = uint8(ea + mul255(int(row[o+nc]), inv255))
			}
		}
	}
}

// N returns components per pixel of an Image.
func (im *Image) N() int {
	n := im.NColor
	if im.Alpha {
		n++
	}
	return n
}

// nearest samples src at unit-square coords (u, vTop) without filtering.
func nearest(src *Image, u, vTop float64, n int, out *[4]int) {
	x := int(u * float64(src.W))
	y := int(vTop * float64(src.H))
	if x < 0 {
		x = 0
	}
	if x >= src.W {
		x = src.W - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= src.H {
		y = src.H - 1
	}
	p := src.Samples[(y*src.W+x)*n:]
	for c := 0; c < n && c < 4; c++ {
		out[c] = int(p[c])
	}
}

// bilinear samples all components of src at fractional (sx, sy), clamping
// at the borders.
func bilinear(src *Image, sx, sy float64, n int, out *[4]int) {
	x0 := int(math.Floor(sx))
	y0 := int(math.Floor(sy))
	fx := sx - float64(x0)
	fy := sy - float64(y0)
	x1 := x0 + 1
	y1 := y0 + 1
	cl := func(v, hi int) int {
		if v < 0 {
			return 0
		}
		if v >= hi {
			return hi - 1
		}
		return v
	}
	x0, x1 = cl(x0, src.W), cl(x1, src.W)
	y0, y1 = cl(y0, src.H), cl(y1, src.H)
	stride := src.W * n
	p00 := src.Samples[y0*stride+x0*n:]
	p10 := src.Samples[y0*stride+x1*n:]
	p01 := src.Samples[y1*stride+x0*n:]
	p11 := src.Samples[y1*stride+x1*n:]
	w00 := (1 - fx) * (1 - fy)
	w10 := fx * (1 - fy)
	w01 := (1 - fx) * fy
	w11 := fx * fy
	for c := 0; c < n && c < 4; c++ {
		v := w00*float64(p00[c]) + w10*float64(p10[c]) + w01*float64(p01[c]) + w11*float64(p11[c])
		out[c] = int(v + 0.5)
	}
}

// boxReduce shrinks an image by integer factors (simple area average).
func boxReduce(img *Image, fx, fy int) *Image {
	if fx < 1 {
		fx = 1
	}
	if fy < 1 {
		fy = 1
	}
	nw := (img.W + fx - 1) / fx
	nh := (img.H + fy - 1) / fy
	if nw < 1 || nh < 1 || (fx == 1 && fy == 1) {
		return img
	}
	n := img.N()
	out := &Image{W: nw, H: nh, NColor: img.NColor, Alpha: img.Alpha,
		Samples: make([]byte, nw*nh*n)}
	srcStride := img.W * n
	for oy := 0; oy < nh; oy++ {
		for ox := 0; ox < nw; ox++ {
			var acc [4]int
			cnt := 0
			for dy := 0; dy < fy; dy++ {
				sy := oy*fy + dy
				if sy >= img.H {
					break
				}
				for dx := 0; dx < fx; dx++ {
					sx := ox*fx + dx
					if sx >= img.W {
						break
					}
					p := img.Samples[sy*srcStride+sx*n:]
					for c := 0; c < n && c < 4; c++ {
						acc[c] += int(p[c])
					}
					cnt++
				}
			}
			if cnt == 0 {
				continue
			}
			o := (oy*nw + ox) * n
			for c := 0; c < n && c < 4; c++ {
				out.Samples[o+c] = uint8(acc[c] / cnt)
			}
		}
	}
	return out
}
