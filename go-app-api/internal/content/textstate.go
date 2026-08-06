package content

import (
	"p4wn/internal/font"
	"p4wn/internal/graphics"
	"p4wn/internal/pdf"
	"p4wn/internal/render"
)

// textState is the PDF text-state subset of the graphics state (persists
// across BT/ET; tm/tlm are only live inside a text object).
type textState struct {
	charSpace float64 // Tc
	wordSpace float64 // Tw
	scale     float64 // Tz / 100
	leading   float64 // TL
	font      *font.Font
	size      float64 // Tf
	render    int     // Tr
	rise      float64 // Ts

	tm, tlm graphics.Matrix
}

func newTextState() textState {
	return textState{scale: 1, tm: graphics.Identity, tlm: graphics.Identity}
}

// beginText (BT) resets the text matrices.
func (in *interp) beginText() {
	in.gs.text.tm = graphics.Identity
	in.gs.text.tlm = graphics.Identity
}

// textNewline (T*, TD, ', ") moves to the next line via the leading.
func (in *interp) textNewline(tx, ty float64) {
	ts := &in.gs.text
	ts.tlm = graphics.Concat(graphics.Translate(tx, ty), ts.tlm)
	ts.tm = ts.tlm
}

// setTextFont resolves /Name size Tf against the resource stack. Indirect
// font dicts (the normal case) are cached by Ref; direct dicts load fresh.
func (in *interp) setTextFont(name pdf.Name, size float64) {
	ts := &in.gs.text
	ts.size = size
	obj := in.lookupResource("Font", name)
	if obj == nil {
		ts.font = font.Load(in.doc, nil) // hail-mary substitute
		return
	}
	if ref, ok := obj.(pdf.Ref); ok {
		if f, ok := in.fontCache[ref]; ok {
			ts.font = f
			return
		}
		f := font.Load(in.doc, in.doc.GetDict(obj))
		if in.fontCache == nil {
			in.fontCache = map[pdf.Ref]*font.Font{}
		}
		in.fontCache[ref] = f
		ts.font = f
		return
	}
	ts.font = font.Load(in.doc, in.doc.GetDict(obj))
}

// showText lays out one show-string and emits it to the device.
func (in *interp) showText(s []byte) {
	gs := &in.gs
	ts := &gs.text
	if ts.font == nil {
		in.setTextFont("", ts.size)
	}
	if ts.size == 0 {
		// zero-size text advances nothing visible; skip layout entirely
		return
	}
	var t render.Text
	t.FontFace = ts.font.CSSFontFamily()
	t.FontSize = ts.size
	if ts.font.HasEmbeddedFont() {
		t.FontData = ts.font.FileData
		t.FontMIME = ts.font.FontMIME()
	}
	for _, g := range ts.font.Decode(s) {
		// glyph space (em) → text space → user space
		if ts.render != 3 { // 3 = invisible
			gtm := graphics.Matrix{
				A: ts.size * ts.scale, D: ts.size,
				F: ts.rise,
			}
			gtm = graphics.Concat(gtm, ts.tm)
			glyph := render.Glyph{Trm: gtm, Unicode: g.Unicode}
			if path := ts.font.Outline(g.GID); path != nil {
				glyph.Path = path
			}
			// Always keep the glyph so advances stay visible to HTML/PNG
			// consumers even when Unicode and outlines are both missing.
			t.Glyphs = append(t.Glyphs, glyph)
		}
		// advance
		tx := (g.WidthEm*ts.size + ts.charSpace) * ts.scale
		if g.IsSpace {
			tx += ts.wordSpace * ts.scale
		}
		ts.tm = graphics.Concat(graphics.Translate(tx, 0), ts.tm)
	}
	if len(t.Glyphs) == 0 {
		return
	}
	switch ts.render {
	case 1, 5: // stroke
		in.dev.StrokeText(&t, &gs.stroke, gs.ctm, gs.strokeCS, gs.strokeColor, gs.strokeAlpha)
	case 2, 6: // fill + stroke
		in.dev.FillText(&t, gs.ctm, gs.fillCS, gs.fillColor, gs.fillAlpha)
		in.dev.StrokeText(&t, &gs.stroke, gs.ctm, gs.strokeCS, gs.strokeColor, gs.strokeAlpha)
	case 3: // invisible
	default: // 0, 4, 7 → fill (clip part of 4-7 degrades, M5)
		in.dev.FillText(&t, gs.ctm, gs.fillCS, gs.fillColor, gs.fillAlpha)
	}
}

// showTextAdjusted handles TJ arrays: strings interleaved with position
// adjustments in thousandths of text space.
func (in *interp) showTextAdjusted(arr pdf.Array) {
	ts := &in.gs.text
	for _, o := range arr {
		switch v := in.doc.Resolve(o).(type) {
		case pdf.String:
			in.showText([]byte(v))
		case pdf.Integer:
			tx := -float64(v) / 1000 * ts.size * ts.scale
			ts.tm = graphics.Concat(graphics.Translate(tx, 0), ts.tm)
		case pdf.Real:
			tx := -float64(v) / 1000 * ts.size * ts.scale
			ts.tm = graphics.Concat(graphics.Translate(tx, 0), ts.tm)
		}
	}
}
