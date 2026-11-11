package font

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"p4wn/internal/graphics"
)

// cjkFallback is the optional runtime CJK face (SetCJKFallback). The Go
// fonts used for non-embedded fonts have no CJK glyphs; without this face,
// such text renders blank in PNG output.
var cjkFallback atomic.Pointer[sfntSource]

// SetCJKFallback loads a font file whose glyphs are used, by Unicode, when a
// font is not embedded (or its CIDs cannot be mapped) and the Go substitute
// has no glyph. Accepts TrueType (.ttf), CFF OpenType (.otf, CID-keyed
// included) and collections (.ttc/.otc; the first face with Japanese kana
// and kanji is used). Not supported by x/image/font/sfnt: CFF2/variable
// fonts, WOFF/WOFF2, Type1. The whole file stays in memory. An empty path
// removes the fallback.
func SetCJKFallback(path string) error {
	if path == "" {
		cjkFallback.Store(nil)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c, err := sfnt.ParseCollection(data)
	if err != nil {
		return fmt.Errorf("cjk font %s: %w", path, err)
	}
	var buf sfnt.Buffer
	for i := 0; i < c.NumFonts(); i++ {
		f, err := c.Font(i)
		if err != nil {
			continue
		}
		a, _ := f.GlyphIndex(&buf, 'あ')
		k, _ := f.GlyphIndex(&buf, '日')
		if a == 0 || k == 0 {
			continue
		}
		upem := float64(f.UnitsPerEm())
		if upem <= 0 {
			upem = 1000
		}
		cjkFallback.Store(&sfntSource{f: f, upem: upem, ppem: fixed.I(int(upem))})
		return nil
	}
	return errors.New("cjk font " + path + ": no face with Japanese glyphs (あ, 日)")
}

// cjkSource returns a per-Font handle on the fallback face (sfnt.Buffer is
// not shareable), or nil when none is configured.
func cjkSource() *sfntSource {
	s := cjkFallback.Load()
	if s == nil {
		return nil
	}
	return &sfntSource{f: s.f, upem: s.upem, ppem: s.ppem}
}

// verticalForms maps punctuation to its Unicode vertical presentation form,
// standing in for the OpenType 'vert' feature the fallback path lacks.
var verticalForms = map[rune]rune{
	'、': '︑', '。': '︒', '，': '︐', '：': '︓', '；': '︔', '！': '︕', '？': '︖',
	'…': '︙', '‥': '︰', '—': '︱', '–': '︲', '＿': '︳',
	'（': '︵', '）': '︶', '｛': '︷', '｝': '︸', '〔': '︹', '〕': '︺',
	'【': '︻', '】': '︼', '《': '︽', '》': '︾', '〈': '︿', '〉': '﹀',
	'「': '﹁', '」': '﹂', '『': '﹃', '』': '﹄', '［': '﹇', '］': '﹈',
}

// rotatedInVertical lists glyphs that are drawn turned 90° clockwise in
// vertical text but have no presentation form.
func rotatedInVertical(r rune) bool {
	switch r {
	case 'ー', '～', '〜', '＝', '－', '→', '←':
		return true
	}
	return false
}

// rotateCW turns an em-space outline 90° clockwise about the centre of the
// usual CJK em box (x 0..1, y -0.12..0.88).
var rotateCW = graphics.Matrix{A: 0, B: -1, C: 1, D: 0, E: 0.5 - 0.38, F: 0.5 + 0.38}

// useCJK points g at the fallback face's glyph for r, if any.
func (f *Font) useCJK(g *GlyphRun, r rune) {
	if f.cjk == nil || r == 0 {
		return
	}
	if f.Vertical() {
		if v, ok := verticalForms[r]; ok {
			if gid := f.cjk.GIDForRune(v); gid != 0 {
				g.GID, g.fromCJK = gid, true
				return
			}
		}
		g.rotate = rotatedInVertical(r)
	}
	if gid := f.cjk.GIDForRune(r); gid != 0 {
		g.GID, g.fromCJK = gid, true
	}
}
