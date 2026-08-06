package render

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"

	"p4wn/internal/graphics"
)

// HTMLDevice emits an SVG page fragment (paths, images, semantic text with
// outline fallback) driven by the content interpreter.
type HTMLDevice struct {
	transform graphics.Matrix
	width     float64
	height    float64

	b         strings.Builder // body content inside <svg>
	defs      strings.Builder
	clipStack []string // active clip-path url ids; empty = none
	clipID    int
	imgID     int
	fonts     map[string]fontFace // face id → embedded data
	fontOrder []string
}

type fontFace struct {
	mime string
	data []byte
}

// NewHTMLDevice creates a device targeting a page of the given CSS-pixel size.
// transform maps PDF user space to page coordinates (origin top-left).
func NewHTMLDevice(transform graphics.Matrix, width, height float64) *HTMLDevice {
	return &HTMLDevice{
		transform: transform,
		width:     width,
		height:    height,
		fonts:     map[string]fontFace{},
	}
}

func (d *HTMLDevice) ctm(opCTM graphics.Matrix) graphics.Matrix {
	return graphics.Concat(opCTM, d.transform)
}

func (d *HTMLDevice) FillPath(path *graphics.Path, evenOdd bool, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	if path == nil || path.IsEmpty() {
		return
	}
	ctm := d.ctm(opCTM)
	d.writePath(path, ctm, evenOdd, cssColor(cs, color), alpha, "", 0, 0, nil)
}

func (d *HTMLDevice) StrokePath(path *graphics.Path, ss *graphics.StrokeState, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	if path == nil || path.IsEmpty() || ss == nil {
		return
	}
	ctm := d.ctm(opCTM)
	sw := ss.LineWidth * ctm.Expansion()
	if sw < 0.25 {
		sw = 0.25
	}
	d.writePath(path, ctm, false, "none", alpha, cssColor(cs, color), sw, ss.LineJoin, ss)
}

func (d *HTMLDevice) ClipPath(path *graphics.Path, evenOdd bool, opCTM graphics.Matrix) {
	d.pushClip(path, evenOdd, d.ctm(opCTM), nil)
}

func (d *HTMLDevice) ClipStrokePath(path *graphics.Path, ss *graphics.StrokeState, opCTM graphics.Matrix) {
	d.pushClip(path, false, d.ctm(opCTM), ss)
}

func (d *HTMLDevice) pushClip(path *graphics.Path, evenOdd bool, ctm graphics.Matrix, ss *graphics.StrokeState) {
	if path == nil || path.IsEmpty() {
		d.clipStack = append(d.clipStack, "")
		return
	}
	d.clipID++
	id := fmt.Sprintf("c%d", d.clipID)
	rule := ""
	if evenOdd {
		rule = ` clip-rule="evenodd"`
	}
	fmt.Fprintf(&d.defs, `<clipPath id="%s"><path d="%s"%s`, id, pathToSVG(path, ctm), rule)
	if ss != nil {
		sw := ss.LineWidth * ctm.Expansion()
		if sw < 0.25 {
			sw = 0.25
		}
		fmt.Fprintf(&d.defs, ` fill="none" stroke="black" stroke-width="%s"`, fmtFloat(sw))
	} else {
		d.defs.WriteString(` fill="black"`)
	}
	d.defs.WriteString(`/></clipPath>`)
	d.clipStack = append(d.clipStack, id)
	fmt.Fprintf(&d.b, `<g clip-path="url(#%s)">`, id)
}

func (d *HTMLDevice) PopClip() {
	if len(d.clipStack) == 0 {
		return
	}
	id := d.clipStack[len(d.clipStack)-1]
	d.clipStack = d.clipStack[:len(d.clipStack)-1]
	if id != "" {
		d.b.WriteString(`</g>`)
	}
}

func (d *HTMLDevice) FillImage(img *Image, opCTM graphics.Matrix, alpha float64) {
	if img == nil || img.W <= 0 || img.H <= 0 {
		return
	}
	ctm := d.ctm(opCTM)
	href, ok := imageDataURI(img)
	if !ok {
		return
	}
	svgCTM := svgImageMatrix(ctm)
	// PDF image space is a unit square; include both href and xlink:href for
	// broader SVG/HTML engine support.
	fmt.Fprintf(&d.b, `<image width="1" height="1" preserveAspectRatio="none" href="%s" xlink:href="%s" transform="%s" opacity="%s"/>`,
		href, href, matrixAttr(svgCTM), fmtFloat(clamp01(alpha)))
}

// svgImageMatrix adapts a PDF image CTM for SVG <image>. PDF maps the unit
// square with image row 0 at v=1 (top); SVG <image> places row 0 at local y=0.
// Negating the Y basis and shifting the origin keeps the same device quad
// with upright bitmap orientation (and avoids negative scales that some
// clipPath implementations mishandle).
func svgImageMatrix(m graphics.Matrix) graphics.Matrix {
	return graphics.Matrix{
		A: m.A, B: m.B,
		C: -m.C, D: -m.D,
		E: m.E + m.C, F: m.F + m.D,
	}
}

func (d *HTMLDevice) FillImageMask(img *Image, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	if img == nil {
		return
	}
	// Approximate stencil: tinted opaque image via feColorMatrix would be
	// heavy; paint as a solid-colored rectangle clipped by the mask path of
	// coverage — fall back to a filled image with the solid color baked in.
	tinted := tintMaskImage(img, cs, color)
	d.FillImage(tinted, opCTM, alpha)
}

func (d *HTMLDevice) FillText(t *Text, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	d.emitText(t, opCTM, cs, color, alpha, false, nil)
}

func (d *HTMLDevice) StrokeText(t *Text, ss *graphics.StrokeState, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64) {
	d.emitText(t, opCTM, cs, color, alpha, true, ss)
}

func (d *HTMLDevice) emitText(t *Text, opCTM graphics.Matrix,
	cs graphics.Colorspace, color []float64, alpha float64, stroke bool, ss *graphics.StrokeState) {
	if t == nil || len(t.Glyphs) == 0 {
		return
	}
	d.noteFont(t)
	fill := cssColor(cs, color)
	// Line width is in user space (same as DrawDevice StrokeText): scale by the
	// page/op CTM only — never by the text matrix / font size.
	pageExp := d.ctm(opCTM).Expansion()
	for _, g := range t.Glyphs {
		// Prefer glyph outlines when available: PDF path CTMs (often D<0 after
		// the page Y-flip) paint correctly, while raw SVG <text> with the same
		// matrix appears upside-down. Semantic text is the no-outline fallback.
		useOutline := stroke || g.Path != nil || g.Unicode == "" || !isTextSafe(g.Trm)
		if !useOutline && g.Unicode != "" {
			m := svgTextMatrix(d.ctm(graphics.Concat(g.Trm, opCTM)))
			face := cssFontStack(t.FontFace)
			fmt.Fprintf(&d.b,
				`<text transform="%s" font-family="%s" font-size="1" fill="%s" opacity="%s" text-rendering="geometricPrecision">%s</text>`,
				matrixAttr(m), xmlEscapeAttr(face), fill, fmtFloat(clamp01(alpha)), xmlEscapeText(g.Unicode))
			continue
		}
		if g.Path == nil {
			continue
		}
		ctm := d.ctm(graphics.Concat(g.Trm, opCTM))
		if stroke {
			sw := 1.0
			if ss != nil {
				sw = ss.LineWidth * pageExp
				if sw < 0.25 {
					sw = 0.25
				}
			}
			d.writePath(g.Path, ctm, false, "none", alpha, fill, sw, 0, ss)
		} else {
			d.writePath(g.Path, ctm, false, fill, alpha, "", 0, 0, nil)
		}
	}
}

// svgTextMatrix adapts a PDF device text matrix for SVG <text>. PDF glyph
// paths are Y-up; after the page flip the path CTM often has D<0 and fills
// upright. SVG text is Y-down, so the same matrix draws upside-down — negate
// the Y basis (C,D) to keep glyphs upright at the same origin.
func svgTextMatrix(m graphics.Matrix) graphics.Matrix {
	m.C, m.D = -m.C, -m.D
	return m
}

func (d *HTMLDevice) noteFont(t *Text) {
	if t.FontFace == "" || len(t.FontData) == 0 {
		return
	}
	if _, ok := d.fonts[t.FontFace]; ok {
		return
	}
	d.fonts[t.FontFace] = fontFace{mime: t.FontMIME, data: t.FontData}
	d.fontOrder = append(d.fontOrder, t.FontFace)
}

func (d *HTMLDevice) Close() {
	for len(d.clipStack) > 0 {
		d.PopClip()
	}
}

// PageSVG returns the inner SVG markup for this page (no HTML chrome).
func (d *HTMLDevice) PageSVG() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%s" height="%s" viewBox="0 0 %s %s">`,
		fmtFloat(d.width), fmtFloat(d.height), fmtFloat(d.width), fmtFloat(d.height))
	if d.defs.Len() > 0 {
		b.WriteString(`<defs>`)
		b.WriteString(d.defs.String())
		b.WriteString(`</defs>`)
	}
	b.WriteString(d.b.String())
	b.WriteString(`</svg>`)
	return b.String()
}

// FontFaces returns discovered embedded fonts in encounter order.
func (d *HTMLDevice) FontFaces() []struct {
	Family string
	MIME   string
	Data   []byte
} {
	out := make([]struct {
		Family string
		MIME   string
		Data   []byte
	}, 0, len(d.fontOrder))
	for _, id := range d.fontOrder {
		f := d.fonts[id]
		out = append(out, struct {
			Family string
			MIME   string
			Data   []byte
		}{Family: id, MIME: f.mime, Data: f.data})
	}
	return out
}

func (d *HTMLDevice) writePath(path *graphics.Path, ctm graphics.Matrix, evenOdd bool,
	fill string, alpha float64, stroke string, strokeWidth float64, _ int, ss *graphics.StrokeState) {
	attrs := fmt.Sprintf(`d="%s"`, pathToSVG(path, ctm))
	if fill == "" {
		fill = "none"
	}
	attrs += fmt.Sprintf(` fill="%s"`, fill)
	if evenOdd && fill != "none" {
		attrs += ` fill-rule="evenodd"`
	}
	if stroke != "" && stroke != "none" {
		attrs += fmt.Sprintf(` stroke="%s" stroke-width="%s"`, stroke, fmtFloat(strokeWidth))
		if ss != nil {
			switch ss.LineCap {
			case graphics.CapRound:
				attrs += ` stroke-linecap="round"`
			case graphics.CapSquare:
				attrs += ` stroke-linecap="square"`
			}
			switch ss.LineJoin {
			case graphics.JoinRound:
				attrs += ` stroke-linejoin="round"`
			case graphics.JoinBevel:
				attrs += ` stroke-linejoin="bevel"`
			}
			if ss.MiterLimit > 0 && ss.LineJoin == graphics.JoinMiter {
				attrs += fmt.Sprintf(` stroke-miterlimit="%s"`, fmtFloat(ss.MiterLimit))
			}
			if len(ss.Dashes) > 0 {
				parts := make([]string, len(ss.Dashes))
				for i, v := range ss.Dashes {
					parts[i] = fmtFloat(v * ctm.Expansion())
				}
				attrs += fmt.Sprintf(` stroke-dasharray="%s"`, strings.Join(parts, " "))
				if ss.DashPhase != 0 {
					attrs += fmt.Sprintf(` stroke-dashoffset="%s"`, fmtFloat(ss.DashPhase*ctm.Expansion()))
				}
			}
		}
	} else {
		attrs += ` stroke="none"`
	}
	if alpha < 0.999 {
		attrs += fmt.Sprintf(` opacity="%s"`, fmtFloat(clamp01(alpha)))
	}
	fmt.Fprintf(&d.b, `<path %s/>`, attrs)
}

// cssFontStack builds a browser font-family list with CJK system fallbacks so
// Japanese text remains readable when the embedded subset is incomplete.
func cssFontStack(face string) string {
	const cjk = `"Hiragino Sans", "Yu Gothic", "Noto Sans CJK JP", "Meiryo", sans-serif`
	if face == "" {
		return cjk
	}
	return `"` + face + `", ` + cjk
}

// isTextSafe reports whether the glyph text matrix is roughly upright /
// horizontal so browser text placement is reliable.
func isTextSafe(trm graphics.Matrix) bool {
	const eps = 1e-3
	// allow uniform scale + translation; reject large shear/rotation
	return math.Abs(trm.B) < eps && math.Abs(trm.C) < eps && trm.A > eps && math.Abs(trm.D) > eps
}

func pathToSVG(path *graphics.Path, ctm graphics.Matrix) string {
	var b strings.Builder
	path.Walk(svgPathWriter{&b, ctm})
	return b.String()
}

type svgPathWriter struct {
	b   *strings.Builder
	ctm graphics.Matrix
}

func (w svgPathWriter) MoveTo(x, y float64) {
	x, y = w.ctm.TransformPoint(x, y)
	fmt.Fprintf(w.b, "M%s %s", fmtFloat(x), fmtFloat(y))
}
func (w svgPathWriter) LineTo(x, y float64) {
	x, y = w.ctm.TransformPoint(x, y)
	fmt.Fprintf(w.b, "L%s %s", fmtFloat(x), fmtFloat(y))
}
func (w svgPathWriter) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	x1, y1 = w.ctm.TransformPoint(x1, y1)
	x2, y2 = w.ctm.TransformPoint(x2, y2)
	x3, y3 = w.ctm.TransformPoint(x3, y3)
	fmt.Fprintf(w.b, "C%s %s %s %s %s %s",
		fmtFloat(x1), fmtFloat(y1), fmtFloat(x2), fmtFloat(y2), fmtFloat(x3), fmtFloat(y3))
}
func (w svgPathWriter) Close() { w.b.WriteByte('Z') }

func matrixAttr(m graphics.Matrix) string {
	return fmt.Sprintf("matrix(%s %s %s %s %s %s)",
		fmtFloat(m.A), fmtFloat(m.B), fmtFloat(m.C), fmtFloat(m.D), fmtFloat(m.E), fmtFloat(m.F))
}

func cssColor(cs graphics.Colorspace, comps []float64) string {
	r, g, b := 0.0, 0.0, 0.0
	if cs != nil {
		r, g, b = cs.ToRGB(comps)
	}
	return fmt.Sprintf("#%02x%02x%02x", to255(r), to255(g), to255(b))
}

func fmtFloat(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	s := strconvFormatFloat(v)
	return s
}

func strconvFormatFloat(v float64) string {
	// trim trailing zeros without pulling in strconv in hot path names — use fmt
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "-0" {
		return "0"
	}
	if s == "" {
		return "0"
	}
	return s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func xmlEscapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func xmlEscapeAttr(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '<':
			b.WriteString("&lt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func imageDataURI(img *Image) (string, bool) {
	nrgba := imageToNRGBA(img)
	if nrgba == nil {
		return "", false
	}
	var b strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &b)
	if err := png.Encode(enc, nrgba); err != nil {
		return "", false
	}
	_ = enc.Close()
	return "data:image/png;base64," + b.String(), true
}

func imageToNRGBA(img *Image) *image.NRGBA {
	if img == nil || img.W <= 0 || img.H <= 0 {
		return nil
	}
	out := image.NewNRGBA(image.Rect(0, 0, img.W, img.H))
	n := img.NColor
	if img.Alpha {
		n++
	}
	if n <= 0 {
		return nil
	}
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			o := (y*img.W + x) * n
			if o+img.NColor > len(img.Samples) {
				return out
			}
			var r, g, b, a uint8 = 0, 0, 0, 255
			switch img.NColor {
			case 1:
				r = img.Samples[o]
				g, b = r, r
			case 3:
				r, g, b = img.Samples[o], img.Samples[o+1], img.Samples[o+2]
			default:
				r = img.Samples[o]
				g, b = r, r
			}
			if img.Alpha && o+img.NColor < len(img.Samples) {
				a = img.Samples[o+img.NColor]
			}
			out.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}
	return out
}

func tintMaskImage(img *Image, cs graphics.Colorspace, comps []float64) *Image {
	r, g, b := cssColorRGB(cs, comps)
	out := &Image{W: img.W, H: img.H, NColor: 3, Alpha: true, Samples: make([]byte, img.W*img.H*4)}
	srcN := img.NColor
	if img.Alpha {
		srcN++
	}
	for i := 0; i < img.W*img.H; i++ {
		o := i * srcN
		cov := byte(255)
		if len(img.Samples) > o {
			cov = img.Samples[o] // mask samples are typically 1-channel
		}
		if img.Alpha && len(img.Samples) > o+img.NColor {
			cov = img.Samples[o+img.NColor]
		}
		dst := i * 4
		out.Samples[dst] = r
		out.Samples[dst+1] = g
		out.Samples[dst+2] = b
		out.Samples[dst+3] = cov
	}
	return out
}

func cssColorRGB(cs graphics.Colorspace, comps []float64) (r, g, b byte) {
	rr, gg, bb := 0.0, 0.0, 0.0
	if cs != nil {
		rr, gg, bb = cs.ToRGB(comps)
	}
	return to255(rr), to255(gg), to255(bb)
}
