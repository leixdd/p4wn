package font

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
)

// cidCMap is a predefined Type0 /Encoding CMap. p4wn embeds no Adobe CMap
// tables: a code is split off by the CMap's codespace and converted to
// Unicode directly (Uni*-UCS2/UTF16/UTF8/UTF32) or with x/text (Shift-JIS,
// EUC-JP, ISO-2022-JP). The CID is known for Identity, and otherwise only
// for the few single-byte / ASCII / half-width-katakana ranges in cids
// (enough for /W widths of Latin text); two-byte codes have no CID.
type cidCMap struct {
	identity bool
	vertical bool
	split    func(s []byte) int       // byte length of the next code
	unicode  func(code []byte) string // "" when unmapped
	cids     []cidRange               // keyed by code, or by Unicode when byUni
	byUni    bool
}

type cidRange struct{ lo, hi, cid uint32 }

// Single-byte / ASCII CID ranges of the Adobe-Japan1 CMaps, checked against
// Adobe's cmap-resources (the -V CMaps share them via usecmap).
var (
	cids90ms  = []cidRange{{0x20, 0x7d, 231}, {0x7e, 0x7e, 631}, {0xa0, 0xdf, 326}}
	cids90msp = []cidRange{{0x20, 0x7e, 1}, {0xa0, 0xdf, 326}}
	cidsEUC   = []cidRange{{0x20, 0x7e, 231}, {0x8ea0, 0x8edf, 326}}
	cidsUCS2  = []cidRange{{0x20, 0x5b, 1}, {0x5c, 0x5c, 97}, {0x5d, 0x7e, 62}, {0xff61, 0xff9f, 327}}
	cidsUTF   = []cidRange{{0x20, 0x5b, 1}, {0x5c, 0x5c, 97}, {0x5d, 0x7b, 62}, {0x7c, 0x7c, 99},
		{0x7d, 0x7d, 94}, {0x7e, 0x7e, 100}, {0xff61, 0xff9f, 327}}
)

var identityCMap = &cidCMap{identity: true, split: split2}

// lookupCMap resolves a predefined CMap name; nil when unsupported.
func lookupCMap(name string) *cidCMap {
	base, vertical := strings.CutSuffix(name, "-V")
	if !vertical {
		var ok bool
		if base, ok = strings.CutSuffix(name, "-H"); !ok {
			if name != "H" && name != "V" {
				return nil
			}
			base, vertical = "", name == "V"
		}
	}
	m := cmapForBase(base)
	if m == nil {
		return nil
	}
	c := *m
	c.vertical = vertical
	return &c
}

func cmapForBase(base string) *cidCMap {
	switch base {
	case "Identity":
		return identityCMap
	case "90ms-RKSJ":
		return &cidCMap{split: splitSJIS, unicode: viaX(japanese.ShiftJIS, 0), cids: cids90ms}
	case "90msp-RKSJ":
		return &cidCMap{split: splitSJIS, unicode: viaX(japanese.ShiftJIS, 0), cids: cids90msp}
	case "EUC":
		return &cidCMap{split: splitEUC, unicode: viaX(japanese.EUCJP, 0), cids: cidsEUC}
	case "": // "H" / "V": ISO-2022-JP JIS X 0208 codes = EUC-JP minus the high bits
		return &cidCMap{split: split2, unicode: viaX(japanese.EUCJP, 0x80)}
	}
	// Uni{JIS,GB,CNS,KS}-{UCS2,UCS2-HW,UTF16,UTF8,UTF32}: the code is Unicode
	// itself, so any ordering decodes; ASCII CIDs are known for Japan1 only.
	ordering, enc, ok := strings.Cut(strings.TrimPrefix(base, "Uni"), "-")
	if !ok || !strings.HasPrefix(base, "Uni") {
		return nil
	}
	japan := ordering == "JIS"
	if !japan && ordering != "GB" && ordering != "CNS" && ordering != "KS" {
		return nil
	}
	var c cidCMap
	switch enc {
	case "UCS2", "UCS2-HW":
		c = cidCMap{split: split2, unicode: decodeUCS2}
		if enc == "UCS2" && japan {
			c.cids = cidsUCS2
		}
	case "UTF16":
		c = cidCMap{split: splitUTF16, unicode: decodeUTF16, cids: cidsUTF}
	case "UTF8":
		c = cidCMap{split: splitUTF8, unicode: decodeUTF8, cids: cidsUTF}
	case "UTF32":
		c = cidCMap{split: split4, unicode: decodeUTF32, cids: cidsUTF}
	default:
		return nil
	}
	if !japan {
		c.cids = nil
	}
	c.byUni = true
	return &c
}

// next splits the next code off s: its value (big-endian bytes, the key
// used by ToUnicode and the CID ranges) and length; n == 0 when s is too
// short for a whole code.
func (c *cidCMap) next(s []byte) (code uint32, n int) {
	if len(s) == 0 {
		return 0, 0
	}
	n = c.split(s)
	if n > len(s) || n == 0 {
		return 0, 0
	}
	for _, b := range s[:n] {
		code = code<<8 | uint32(b)
	}
	return code, n
}

// cid maps a code to its CID when known.
func (c *cidCMap) cid(code uint32, uni string) (uint32, bool) {
	if c.identity {
		return code, true
	}
	key := code
	if c.byUni {
		r, size := utf8.DecodeRuneInString(uni)
		if size == 0 || size != len(uni) {
			return 0, false
		}
		key = uint32(r)
	}
	for _, r := range c.cids {
		if key >= r.lo && key <= r.hi {
			return r.cid + key - r.lo, true
		}
	}
	return 0, false
}

func split2(s []byte) int { return 2 }
func split4(s []byte) int { return 4 }

// splitSJIS: lead bytes 81–9F and E0–FC start two-byte codes (90ms-RKSJ
// codespace); everything else is one byte.
func splitSJIS(s []byte) int {
	if b := s[0]; b >= 0x81 && b <= 0x9f || b >= 0xe0 && b <= 0xfc {
		return 2
	}
	return 1
}

// splitEUC: 8E (half-width katakana) and A1–FE lead two-byte codes (EUC-H
// has no three-byte JIS X 0212 codes).
func splitEUC(s []byte) int {
	if b := s[0]; b == 0x8e || b >= 0xa1 && b <= 0xfe {
		return 2
	}
	return 1
}

func splitUTF16(s []byte) int {
	if s[0] >= 0xd8 && s[0] <= 0xdb {
		return 4
	}
	return 2
}

func splitUTF8(s []byte) int {
	switch b := s[0]; {
	case b >= 0xf0 && b <= 0xf7:
		return 4
	case b >= 0xe0:
		return 3
	case b >= 0xc0:
		return 2
	}
	return 1
}

func printable(r rune) string {
	if r < 0x20 || r == utf8.RuneError || r > utf8.MaxRune || r >= 0xd800 && r <= 0xdfff {
		return ""
	}
	return string(r)
}

func decodeUCS2(b []byte) string { return printable(rune(b[0])<<8 | rune(b[1])) }

func decodeUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[i*2])<<8 | uint16(b[i*2+1])
	}
	r := utf16.Decode(u)
	if len(r) != 1 {
		return ""
	}
	return printable(r[0])
}

func decodeUTF8(b []byte) string {
	r, size := utf8.DecodeRune(b)
	if size != len(b) {
		return ""
	}
	return printable(r)
}

func decodeUTF32(b []byte) string {
	return printable(rune(b[0])<<24 | rune(b[1])<<16 | rune(b[2])<<8 | rune(b[3]))
}

// viaX decodes one code with an x/text decoder, OR-ing high into every byte
// first (JIS X 0208 → EUC-JP). Invalid or unmapped codes give "".
func viaX(enc encoding.Encoding, high byte) func([]byte) string {
	return func(code []byte) string {
		var buf [4]byte
		b := buf[:len(code)]
		for i, c := range code {
			b[i] = c | high
		}
		out, err := enc.NewDecoder().Bytes(b)
		if err != nil {
			return ""
		}
		r, size := utf8.DecodeRune(out)
		if size != len(out) {
			return ""
		}
		return printable(r)
	}
}
