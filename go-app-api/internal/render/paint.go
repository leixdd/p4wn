package render

import (
	"p4wn/internal/graphics"
)

// solidColor is a fill color resolved to the pixmap's component model,
// non-premultiplied 0..255 plus alpha 0..255.
type solidColor struct {
	comps [3]uint8 // gray in [0]; rgb in [0..2]
	alpha uint8
}

// resolveColor converts colorspace components + alpha into the pixmap model.
func resolveColor(cs graphics.Colorspace, color []float64, alpha float64, pixColors int) solidColor {
	r, g, b := 0.0, 0.0, 0.0
	if cs != nil {
		r, g, b = cs.ToRGB(color)
	}
	var sc solidColor
	if pixColors == 1 {
		sc.comps[0] = to255(graphics.RGBToGray(r, g, b))
	} else {
		sc.comps[0] = to255(r)
		sc.comps[1] = to255(g)
		sc.comps[2] = to255(b)
	}
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	sc.alpha = uint8(alpha*255 + 0.5)
	return sc
}

func to255(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// mul255 approximates a*b/255 with exact rounding.
func mul255(a, b int) int { t := a*b + 128; return (t + t>>8) >> 8 }

// paintSpanSolid returns a SpanPainter compositing the solid color into pix
// (premultiplied source-over).
func paintSpanSolid(pix *graphics.Pixmap, sc solidColor) SpanPainter {
	n := pix.N()
	nc := pix.NColor
	hasAlpha := pix.Alpha
	return func(y, x0 int, cover []uint8) {
		if y < pix.Y || y >= pix.Y+pix.H {
			return
		}
		row := pix.Samples[(y-pix.Y)*pix.Stride:]
		for i, cov := range cover {
			if cov == 0 {
				continue
			}
			x := x0 + i - pix.X
			if x < 0 || x >= pix.W {
				continue
			}
			// effective alpha = color alpha × coverage
			ea := mul255(int(sc.alpha), int(cov))
			if ea == 0 {
				continue
			}
			o := x * n
			inv := 255 - ea
			if ea == 255 {
				for c := 0; c < nc; c++ {
					row[o+c] = sc.comps[c]
				}
				if hasAlpha {
					row[o+nc] = 255
				}
				continue
			}
			for c := 0; c < nc; c++ {
				// premultiplied: src comp is comps[c]*ea/255
				row[o+c] = uint8(mul255(int(sc.comps[c]), ea) + mul255(int(row[o+c]), inv))
			}
			if hasAlpha {
				row[o+nc] = uint8(ea + mul255(int(row[o+nc]), inv))
			}
		}
	}
}
