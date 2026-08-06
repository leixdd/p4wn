// Package font resolves PDF font dictionaries into glyph outlines: embedded
// programs when possible, base-14/gofont substitutes otherwise, with PDF
// /Widths always overriding program metrics.
package font

import (
	"errors"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"p4wn/internal/graphics"
)

// glyphSource provides outlines and metrics by glyph index. Implementations
// are NOT safe for concurrent use (they share an sfnt.Buffer); Fonts are
// cached per rendered page.
type glyphSource interface {
	// OutlineEm returns the glyph outline in em units (1.0 = em square,
	// y up, origin at baseline).
	OutlineEm(gid uint16) (*graphics.Path, error)
	// AdvanceEm returns the horizontal advance in em units.
	AdvanceEm(gid uint16) (float64, error)
	// GIDForRune maps a unicode code point through the font's cmap.
	GIDForRune(r rune) uint16
	NumGlyphs() int
}

// unicodeFromGID is optionally implemented by sources that can reverse-map
// a glyph ID to Unicode (embedded sfnt fonts).
type unicodeFromGID interface {
	UnicodeForGID(gid uint16) string
}

// sfntSource adapts x/image/font/sfnt (TrueType glyf + CFF-in-OpenType).
type sfntSource struct {
	f            *sfnt.Font
	buf          sfnt.Buffer
	upem         float64
	ppem         fixed.Int26_6
	gidToUnicode map[uint16]string // lazy reverse cmap for HTML Unicode fallback
}

func newSfntSource(data []byte) (*sfntSource, []byte, error) {
	f, err := sfnt.Parse(data)
	if err != nil {
		// PDF subsets frequently ship invalid cmap tables. Repair and retry
		// so glyph outlines remain addressable by GID.
		fixed, rerr := repairSfntCMap(data)
		if rerr != nil {
			return nil, nil, err
		}
		f, err = sfnt.Parse(fixed)
		if err != nil {
			return nil, nil, err
		}
		data = fixed
	}
	upem := float64(f.UnitsPerEm())
	if upem <= 0 {
		upem = 1000
	}
	return &sfntSource{f: f, upem: upem, ppem: fixed.I(int(upem))}, data, nil
}

func (s *sfntSource) NumGlyphs() int { return s.f.NumGlyphs() }

func (s *sfntSource) OutlineEm(gid uint16) (*graphics.Path, error) {
	if int(gid) >= s.f.NumGlyphs() {
		return nil, errors.New("font: gid out of range")
	}
	segs, err := s.f.LoadGlyph(&s.buf, sfnt.GlyphIndex(gid), s.ppem, nil)
	if err != nil {
		return nil, err
	}
	// segments are y-down at ppem == upem: 26.6 fixed font units.
	// convert to em units with y up.
	inv := 1 / (s.upem * 64)
	cv := func(p fixed.Point26_6) (float64, float64) {
		return float64(p.X) * inv, -float64(p.Y) * inv
	}
	path := &graphics.Path{}
	var curX, curY float64
	for _, seg := range segs {
		switch seg.Op {
		case sfnt.SegmentOpMoveTo:
			x, y := cv(seg.Args[0])
			path.MoveTo(x, y)
			curX, curY = x, y
		case sfnt.SegmentOpLineTo:
			x, y := cv(seg.Args[0])
			path.LineTo(x, y)
			curX, curY = x, y
		case sfnt.SegmentOpQuadTo:
			cx, cy := cv(seg.Args[0])
			x, y := cv(seg.Args[1])
			// elevate quadratic to cubic
			c1x := curX + 2.0/3.0*(cx-curX)
			c1y := curY + 2.0/3.0*(cy-curY)
			c2x := x + 2.0/3.0*(cx-x)
			c2y := y + 2.0/3.0*(cy-y)
			path.CurveTo(c1x, c1y, c2x, c2y, x, y)
			curX, curY = x, y
		case sfnt.SegmentOpCubeTo:
			c1x, c1y := cv(seg.Args[0])
			c2x, c2y := cv(seg.Args[1])
			x, y := cv(seg.Args[2])
			path.CurveTo(c1x, c1y, c2x, c2y, x, y)
			curX, curY = x, y
		}
	}
	path.Close()
	return path, nil
}

func (s *sfntSource) AdvanceEm(gid uint16) (float64, error) {
	adv, err := s.f.GlyphAdvance(&s.buf, sfnt.GlyphIndex(gid), s.ppem, 0)
	if err != nil {
		return 0, err
	}
	return float64(adv) / (s.upem * 64), nil
}

func (s *sfntSource) GIDForRune(r rune) uint16 {
	gid, err := s.f.GlyphIndex(&s.buf, r)
	if err != nil {
		return 0
	}
	return uint16(gid)
}

// UnicodeForGID returns a Unicode string for gid via a reverse cmap built
// from Japanese-relevant + ASCII ranges. Empty when unknown.
func (s *sfntSource) UnicodeForGID(gid uint16) string {
	if gid == 0 {
		return ""
	}
	s.ensureReverseCMap()
	return s.gidToUnicode[gid]
}

func (s *sfntSource) ensureReverseCMap() {
	if s.gidToUnicode != nil {
		return
	}
	s.gidToUnicode = map[uint16]string{}
	for _, r := range reverseCMapProbeRunes() {
		gid := s.GIDForRune(r)
		if gid == 0 {
			continue
		}
		if _, ok := s.gidToUnicode[gid]; !ok {
			s.gidToUnicode[gid] = string(r)
		}
	}
}

// reverseCMapProbeRunes covers ASCII, Japanese kana, CJK punctuation, and
// the BMP CJK Unified Ideographs block used by typical Japanese PDFs.
func reverseCMapProbeRunes() []rune {
	var out []rune
	appendRange := func(lo, hi rune) {
		for r := lo; r <= hi; r++ {
			out = append(out, r)
		}
	}
	appendRange(0x0020, 0x007E) // ASCII
	appendRange(0x00A0, 0x00FF) // Latin-1
	appendRange(0x3000, 0x303F) // CJK punctuation
	appendRange(0x3040, 0x309F) // Hiragana
	appendRange(0x30A0, 0x30FF) // Katakana
	appendRange(0x31F0, 0x31FF) // Katakana phonetic extensions
	appendRange(0x3400, 0x4DBF) // CJK Ext A (common)
	appendRange(0x4E00, 0x9FFF) // CJK Unified
	appendRange(0xF900, 0xFAFF) // CJK Compatibility Ideographs
	appendRange(0xFF00, 0xFFEF) // Halfwidth/Fullwidth
	return out
}
