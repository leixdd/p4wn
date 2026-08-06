package font

import (
	"strings"

	"p4wn/internal/graphics"
	"p4wn/internal/pdf"
)

// GlyphRun is one decoded glyph from a PDF show-text string.
type GlyphRun struct {
	GID     uint16
	Code    uint32  // original character code
	WidthEm float64 // advance in em units (PDF widths take precedence)
	IsSpace bool    // single-byte code 32 (word-spacing applies)
	Bytes   int     // bytes consumed from the string
	Unicode string  // decoded Unicode when known; empty otherwise
}

// FontFileKind identifies the browser-usable embedded font container.
type FontFileKind int

const (
	FontFileNone FontFileKind = iota
	FontFileTTF               // FontFile2 TrueType / OpenType with glyf
	FontFileOTF               // FontFile3 CFF-in-OpenType or wrapped CFF
)

// Font is a resolved PDF font ready for layout and outline extraction.
// Not safe for concurrent use (cached per page render).
type Font struct {
	src      glyphSource // nil when even substitution failed
	isCID    bool
	type3    bool
	symbolic bool

	BaseName string // PDF /BaseFont (may include subset prefix)
	FaceID   string // stable CSS font-family id for HTML output
	FileData []byte // raw embedded font bytes for @font-face, if any
	FileKind FontFileKind

	// unicode mapping: character code → Unicode string
	toUnicode map[uint32]string
	simpleEnc string // base encoding name for simple fonts without ToUnicode

	// simple fonts: per-code lookup tables
	codeToGID   [256]uint16
	codeWidthEm [256]float64 // 0 = unknown → ask font program
	hasWidth    [256]bool

	// CID fonts
	cidToGID  []uint16 // nil = identity
	widthsCID map[uint32]float64
	defaultW  float64
}

// Load resolves a PDF font dict. Never returns nil: on total failure a
// substitute-backed font is produced so text still lays out.
func Load(doc *pdf.Document, dict pdf.Dict) *Font {
	if dict == nil {
		return substituteFont("", 0)
	}
	subtype, _ := dict.GetName("Subtype")
	switch subtype {
	case "Type0":
		return loadType0(doc, dict)
	case "Type3":
		f := substituteFont("", 0)
		f.type3 = true // drawn as nothing for now; layout via /Widths below
		loadSimpleWidths(doc, dict, f)
		return f
	default: // Type1, MMType1, TrueType, or missing
		return loadSimple(doc, dict)
	}
}

// substituteFont builds a gofont-backed fallback with a usable
// StandardEncoding code→GID table (callers with real encodings overwrite it).
func substituteFont(baseName string, flags int) *Font {
	mono, bold, italic := styleFromName(baseName)
	if flags&flagFixedPitch != 0 {
		mono = true
	}
	if flags&flagItalic != 0 {
		italic = true
	}
	if flags&flagForceBold != 0 {
		bold = true
	}
	f := &Font{src: substituteSource(mono, bold, italic)}
	for code := 0; code < 256; code++ {
		f.codeToGID[code] = f.resolveSimpleGID(byte(code), "StandardEncoding", nil)
	}
	return f
}

// --- simple fonts (Type1 / TrueType, 1-byte codes) --------------------------

func loadSimple(doc *pdf.Document, dict pdf.Dict) *Font {
	baseName := ""
	if n, ok := dict.GetName("BaseFont"); ok {
		baseName = string(n)
	}
	desc := doc.GetDict(dict.Get("FontDescriptor"))
	flags := 0
	if desc != nil {
		if v, ok := doc.GetInt(desc.Get("Flags")); ok {
			flags = int(v)
		}
	}

	var src glyphSource
	var fileData []byte
	var fileKind FontFileKind
	embeddedNames := false // embedded Type1/CFF resolve best by glyph name
	if desc != nil {
		src, embeddedNames, fileData, fileKind = loadEmbedded(doc, desc)
	}
	f := &Font{BaseName: baseName, FaceID: cssFaceID(baseName), FileData: fileData, FileKind: fileKind}
	if src == nil {
		sub := substituteFont(baseName, flags)
		f.src = sub.src
	} else {
		f.src = src
	}
	f.symbolic = flags&flagSymbolic != 0 && flags&(flagSerif|flagFixedPitch) == 0
	_ = embeddedNames

	// encoding chain: base encoding → /Differences → glyph names → GID
	baseEnc := "StandardEncoding"
	if f.symbolic {
		baseEnc = "" // use font's built-in cmap directly
	}
	var differences map[byte]string
	switch enc := doc.Resolve(dict.Get("Encoding")).(type) {
	case pdf.Name:
		baseEnc = string(enc)
	case pdf.Dict:
		if n, ok := enc.GetName("BaseEncoding"); ok {
			baseEnc = string(n)
		}
		differences = parseDifferences(doc, enc)
	}
	f.simpleEnc = baseEnc

	for code := 0; code < 256; code++ {
		f.codeToGID[code] = f.resolveSimpleGID(byte(code), baseEnc, differences)
	}
	loadSimpleWidths(doc, dict, f)
	if desc != nil {
		if mw, ok := doc.GetFloat(desc.Get("MissingWidth")); ok {
			for c := 0; c < 256; c++ {
				if !f.hasWidth[c] {
					f.codeWidthEm[c] = mw / 1000
					f.hasWidth[c] = true
				}
			}
		}
	}
	loadToUnicode(doc, dict, f)
	// differences override encoding-based Unicode when ToUnicode is absent
	if f.toUnicode == nil && differences != nil {
		f.toUnicode = map[uint32]string{}
		for code, name := range differences {
			if r := glyphNameToUnicode(name); r != 0 {
				f.toUnicode[uint32(code)] = string(r)
			}
		}
	}
	return f
}

func parseDifferences(doc *pdf.Document, enc pdf.Dict) map[byte]string {
	arr := doc.GetArray(enc.Get("Differences"))
	if arr == nil {
		return nil
	}
	out := map[byte]string{}
	code := 0
	for _, o := range arr {
		switch v := doc.Resolve(o).(type) {
		case pdf.Integer:
			code = int(v)
		case pdf.Real:
			code = int(v)
		case pdf.Name:
			if code >= 0 && code < 256 {
				out[byte(code)] = string(v)
			}
			code++
		}
	}
	return out
}

// codeMapper is implemented by sources with their own code→GID table
// (bare CFF via its Encoding).
type codeMapper interface {
	GIDForCode(code byte) (uint16, bool)
}

func (f *Font) resolveSimpleGID(code byte, baseEnc string, differences map[byte]string) uint16 {
	if f.src == nil {
		return 0
	}
	// 1. /Differences name
	if name, ok := differences[code]; ok {
		if r := glyphNameToUnicode(name); r != 0 {
			if gid := f.src.GIDForRune(r); gid != 0 {
				return gid
			}
		}
	}
	// 1b. the font program's own encoding (bare CFF)
	if cm, ok := f.src.(codeMapper); ok {
		if gid, ok := cm.GIDForCode(code); ok && gid != 0 {
			return gid
		}
	}
	// 2. symbolic: font's built-in cmap on the raw code (plus the F0xx
	// microsoft-symbol convention)
	if baseEnc == "" {
		if gid := f.src.GIDForRune(rune(code)); gid != 0 {
			return gid
		}
		if gid := f.src.GIDForRune(rune(0xF000 + int(code))); gid != 0 {
			return gid
		}
		// subset fonts without cmap: identity is the last resort
		if int(code) < f.src.NumGlyphs() {
			return uint16(code)
		}
		return 0
	}
	// 3. base encoding → unicode → cmap
	if r := encodingToUnicode(baseEnc, code); r != 0 {
		if gid := f.src.GIDForRune(r); gid != 0 {
			return gid
		}
	}
	// 4. desperate: raw code as unicode, then as GID
	if gid := f.src.GIDForRune(rune(code)); gid != 0 {
		return gid
	}
	if int(code) < f.src.NumGlyphs() {
		return uint16(code)
	}
	return 0
}

func loadSimpleWidths(doc *pdf.Document, dict pdf.Dict, f *Font) {
	first, _ := doc.GetInt(dict.Get("FirstChar"))
	widths := doc.GetArray(dict.Get("Widths"))
	for i, o := range widths {
		code := int(first) + i
		if code < 0 || code > 255 {
			continue
		}
		if w, ok := doc.GetFloat(o); ok {
			f.codeWidthEm[code] = w / 1000
			f.hasWidth[code] = true
		}
	}
}

// loadEmbedded parses FontFile2 (TrueType) or FontFile3 (CFF/OpenType).
// FontFile (Type1) falls through to substitution for now.
func loadEmbedded(doc *pdf.Document, desc pdf.Dict) (glyphSource, bool, []byte, FontFileKind) {
	if stm, ok := doc.Resolve(desc.Get("FontFile2")).(*pdf.Stream); ok {
		if data, err := doc.DecodeStream(stm); err == nil || len(data) > 0 {
			data = truncateFontFile(doc, stm, data)
			if src, fixed, err := newSfntSource(data); err == nil {
				return src, false, fixed, FontFileTTF
			}
		}
	}
	if stm, ok := doc.Resolve(desc.Get("FontFile3")).(*pdf.Stream); ok {
		data, err := doc.DecodeStream(stm)
		if err == nil || len(data) > 0 {
			data = truncateFontFile(doc, stm, data)
			// OpenType wrapper?
			if src, fixed, err := newSfntSource(data); err == nil {
				return src, true, fixed, FontFileOTF
			}
			// bare CFF: wrap in a minimal OTF container; keep the CFF's own
			// encoding/charset for code lookups (the stub cmap is empty)
			if wrapped := wrapCFF(data); wrapped != nil {
				if src, fixed, err := newSfntSource(wrapped); err == nil {
					return &cffSource{sfntSource: src, info: scanCFF(data)}, true, fixed, FontFileOTF
				}
			}
		}
	}
	// FontFile (Type1 with eexec): substitute — ponytail: full Type1
	// charstring interpreter is a known gap, tracked for M5+.
	return nil, false, nil, FontFileNone
}

// truncateFontFile respects /Length1 (TrueType) or /Length1+/Length2+/Length3
// when present so trailing padding after the font program is dropped.
func truncateFontFile(doc *pdf.Document, stm *pdf.Stream, data []byte) []byte {
	if l1, ok := doc.GetInt(stm.Dict.Get("Length1")); ok && l1 > 0 && int(l1) < len(data) {
		return data[:l1]
	}
	return data
}

// --- Type0 / CID fonts -------------------------------------------------------

func loadType0(doc *pdf.Document, dict pdf.Dict) *Font {
	baseName := ""
	if n, ok := dict.GetName("BaseFont"); ok {
		baseName = string(n)
	}
	// only Identity-H/V CMaps are supported; others degrade to Identity
	descFonts := doc.GetArray(dict.Get("DescendantFonts"))
	var cidFont pdf.Dict
	if len(descFonts) > 0 {
		cidFont = doc.GetDict(descFonts[0])
	}
	f := &Font{isCID: true, defaultW: 1.0, BaseName: baseName, FaceID: cssFaceID(baseName)}
	if cidFont == nil {
		sub := substituteFont(baseName, 0)
		f.src = sub.src
		loadToUnicode(doc, dict, f)
		return f
	}
	desc := doc.GetDict(cidFont.Get("FontDescriptor"))
	flags := 0
	if desc != nil {
		if v, ok := doc.GetInt(desc.Get("Flags")); ok {
			flags = int(v)
		}
	}
	var src glyphSource
	if desc != nil {
		var fileData []byte
		var fileKind FontFileKind
		src, _, fileData, fileKind = loadEmbedded(doc, desc)
		f.FileData, f.FileKind = fileData, fileKind
	}
	if src == nil {
		sub := substituteFont(baseName, flags)
		src = sub.src
	}
	f.src = src

	// CIDToGIDMap
	switch m := doc.Resolve(cidFont.Get("CIDToGIDMap")).(type) {
	case *pdf.Stream:
		if data, err := doc.DecodeStream(m); err == nil || len(data) > 0 {
			f.cidToGID = make([]uint16, len(data)/2)
			for i := range f.cidToGID {
				f.cidToGID[i] = uint16(data[i*2])<<8 | uint16(data[i*2+1])
			}
		}
	default:
		// /Identity or absent → identity
	}

	// widths: /DW default (1000 = 1em), /W ranges
	if dw, ok := doc.GetFloat(cidFont.Get("DW")); ok {
		f.defaultW = dw / 1000
	}
	f.widthsCID = parseCIDWidths(doc, doc.GetArray(cidFont.Get("W")))
	loadToUnicode(doc, dict, f)
	return f
}

// parseCIDWidths expands /W: [c [w w ...] cFirst cLast w ...].
func parseCIDWidths(doc *pdf.Document, arr pdf.Array) map[uint32]float64 {
	if arr == nil {
		return nil
	}
	out := map[uint32]float64{}
	i := 0
	for i < len(arr) {
		c1, ok := doc.GetInt(arr[i])
		if !ok {
			break
		}
		i++
		if i >= len(arr) {
			break
		}
		switch v := doc.Resolve(arr[i]).(type) {
		case pdf.Array:
			for j, wo := range v {
				if w, ok := doc.GetFloat(wo); ok {
					out[uint32(c1)+uint32(j)] = w / 1000
				}
			}
			i++
		default:
			c2, ok2 := doc.GetInt(arr[i])
			if !ok2 || i+1 >= len(arr) {
				return out
			}
			w, _ := doc.GetFloat(arr[i+1])
			if c2 >= c1 && c2-c1 < 65536 {
				for c := c1; c <= c2; c++ {
					out[uint32(c)] = w / 1000
				}
			}
			i += 2
			_ = v
		}
	}
	return out
}

// --- layout & outlines -------------------------------------------------------

// Decode splits a PDF show-string into positioned glyphs.
func (f *Font) Decode(s []byte) []GlyphRun {
	var out []GlyphRun
	if f.isCID {
		for i := 0; i+1 < len(s); i += 2 {
			cid := uint32(s[i])<<8 | uint32(s[i+1])
			gid := uint16(cid)
			if f.cidToGID != nil {
				if int(cid) < len(f.cidToGID) {
					gid = f.cidToGID[cid]
				} else {
					gid = 0
				}
			}
			w, ok := f.widthsCID[cid]
			if !ok {
				w = f.defaultW
			}
			out = append(out, GlyphRun{
				GID: gid, Code: cid, WidthEm: w, Bytes: 2,
				Unicode: f.resolveUnicode(cid, gid, true),
			})
		}
		return out
	}
	for _, b := range s {
		g := GlyphRun{
			GID: f.codeToGID[b], Code: uint32(b), IsSpace: b == 32, Bytes: 1,
			Unicode: f.resolveUnicode(uint32(b), f.codeToGID[b], false),
		}
		if f.hasWidth[b] {
			g.WidthEm = f.codeWidthEm[b]
		} else if f.src != nil {
			g.WidthEm, _ = f.src.AdvanceEm(g.GID)
		}
		out = append(out, g)
	}
	return out
}

// resolveUnicode prefers ToUnicode / encoding maps, then embedded cmap reverse lookup.
func (f *Font) resolveUnicode(code uint32, gid uint16, isCID bool) string {
	if s := unicodeForCode(f.toUnicode, code, f.simpleEnc, isCID); s != "" {
		return s
	}
	if gid == 0 || f.src == nil {
		return ""
	}
	if u, ok := f.src.(unicodeFromGID); ok {
		return u.UnicodeForGID(gid)
	}
	return ""
}

// CSSFontFamily returns a browser-safe font-family name for this face.
func (f *Font) CSSFontFamily() string {
	if f.FaceID != "" {
		return f.FaceID
	}
	return cssFaceID(f.BaseName)
}

// HasEmbeddedFont reports whether FileData can be used in @font-face.
func (f *Font) HasEmbeddedFont() bool {
	return len(f.FileData) > 0 && f.FileKind != FontFileNone
}

// FontMIME returns the MIME type for @font-face src.
func (f *Font) FontMIME() string {
	switch f.FileKind {
	case FontFileTTF:
		return "font/ttf"
	case FontFileOTF:
		return "font/otf"
	default:
		return "application/octet-stream"
	}
}

func loadToUnicode(doc *pdf.Document, dict pdf.Dict, f *Font) {
	stm, ok := doc.Resolve(dict.Get("ToUnicode")).(*pdf.Stream)
	if !ok {
		return
	}
	data, err := doc.DecodeStream(stm)
	if err != nil && len(data) == 0 {
		return
	}
	if m := parseToUnicode(data); len(m) > 0 {
		f.toUnicode = m
	}
}

func cssFaceID(baseName string) string {
	name := stripSubsetPrefix(baseName)
	if name == "" {
		name = "PDFFont"
	}
	var b strings.Builder
	b.WriteString("pdf-")
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Outline returns the glyph path in em units, or nil (Type3/missing glyph).
func (f *Font) Outline(gid uint16) *graphics.Path {
	if f.src == nil || f.type3 {
		return nil
	}
	p, err := f.src.OutlineEm(gid)
	if err != nil {
		return nil
	}
	return p
}
