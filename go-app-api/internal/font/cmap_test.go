package font

import (
	"os"
	"strings"
	"testing"

	"p4wn/internal/pdf"
)

func TestLookupCMap(t *testing.T) {
	for _, name := range []string{"Identity-H", "Identity-V", "UniJIS-UCS2-H", "UniJIS-UCS2-V",
		"UniJIS-UCS2-HW-H", "UniJIS-UTF16-H", "UniJIS-UTF16-V", "UniJIS-UTF8-H", "UniJIS-UTF32-V",
		"90ms-RKSJ-H", "90ms-RKSJ-V", "90msp-RKSJ-H", "90msp-RKSJ-V", "EUC-H", "EUC-V", "H", "V",
		"UniGB-UCS2-H", "UniKS-UTF16-H", "UniCNS-UTF16-V"} {
		m := lookupCMap(name)
		if m == nil {
			t.Errorf("%s: unsupported", name)
			continue
		}
		if want := strings.HasSuffix(name, "V"); m.vertical != want {
			t.Errorf("%s: vertical = %v", name, m.vertical)
		}
	}
	for _, name := range []string{"78-RKSJ-H", "Ext-RKSJ-H", "GBK-EUC-H", "Foo-H", "Identity", "UniFoo-UCS2-H", "UniJIS-UCS4-H"} {
		if lookupCMap(name) != nil {
			t.Errorf("%s: unexpectedly supported", name)
		}
	}
}

func TestCMapDecode(t *testing.T) {
	type code struct {
		n   int
		uni string
		cid int // -1 = unknown
	}
	cases := []struct {
		cmap string
		in   []byte
		want []code
	}{
		// 日 A ｡(half-width) via Shift-JIS; single bytes map to half-width CIDs
		{"90ms-RKSJ-H", []byte{0x93, 0xFA, 0x41, 0xA1, 0x7E}, []code{{2, "日", -1}, {1, "A", 264}, {1, "｡", 327}, {1, "~", 631}}},
		{"90msp-RKSJ-H", []byte{0x41, 0x82, 0xA0}, []code{{1, "A", 34}, {2, "あ", -1}}},
		{"EUC-H", []byte{0xC6, 0xFC, 0x8E, 0xA1, 0x41}, []code{{2, "日", -1}, {2, "｡", 327}, {1, "A", 264}}},
		{"H", []byte{0x46, 0x7C, 0x24, 0x22}, []code{{2, "日", -1}, {2, "あ", -1}}},
		{"UniJIS-UCS2-H", []byte{0x65, 0xE5, 0x00, 0x41, 0x00, 0x7E}, []code{{2, "日", -1}, {2, "A", 34}, {2, "~", 95}}},
		{"UniJIS-UTF16-H", []byte{0xD8, 0x42, 0xDF, 0xB7, 0x00, 0x7E}, []code{{4, "𠮷", -1}, {2, "~", 100}}},
		{"UniJIS-UTF8-H", []byte("日A"), []code{{3, "日", -1}, {1, "A", 34}}},
		{"UniJIS-UTF32-H", []byte{0, 0, 0x65, 0xE5}, []code{{4, "日", -1}}},
		{"Identity-H", []byte{0x12, 0x34, 0x56}, []code{{2, "", 0x1234}}}, // odd trailing byte dropped
	}
	for _, c := range cases {
		m := lookupCMap(c.cmap)
		var got []code
		for s := c.in; ; {
			v, n := m.next(s)
			if n == 0 {
				break
			}
			g := code{n: n, cid: -1}
			if m.unicode != nil {
				g.uni = m.unicode(s[:n])
			}
			if cid, ok := m.cid(v, g.uni); ok {
				g.cid = int(cid)
			}
			got = append(got, g)
			s = s[n:]
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: got %+v, want %+v", c.cmap, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s code %d: got %+v, want %+v", c.cmap, i, got[i], c.want[i])
			}
		}
	}
}

// TestJapaneseCMapsFixture loads the gencjk fixture's fonts (non-embedded,
// no ToUnicode) and checks that every predefined CMap yields the same
// Unicode text, the /W widths by CID, and vertical metrics for -V.
func TestJapaneseCMapsFixture(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/pdfs/japanese-cmaps.pdf")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	page, err := doc.GetPage(0)
	if err != nil {
		t.Fatal(err)
	}
	content, err := page.ContentStreams()
	if err != nil {
		t.Fatal(err)
	}
	fonts := doc.GetDict(page.Resources.Get("Font"))
	// pair each "/Fn 18 Tf ... <hex> Tj" / "[<hex> 500 <hex>] TJ" with its font
	p := pdf.NewParser(pdf.NewLexer(content))
	var stack []pdf.Object
	var cur *Font
	seen := 0
	for {
		obj, op, err := p.NextContentItem()
		if err != nil {
			break
		}
		if op == "" {
			stack = append(stack, obj)
			continue
		}
		switch op {
		case "Tf":
			name := stack[0].(pdf.Name)
			cur = Load(doc, doc.GetDict(fonts.Get(name)))
			if name == "L" {
				cur = nil
			}
		case "Tj", "TJ":
			if cur == nil {
				break
			}
			var runs []GlyphRun
			if op == "Tj" {
				runs = cur.Decode([]byte(stack[0].(pdf.String)))
			} else {
				for _, o := range stack[0].(pdf.Array) {
					if s, ok := o.(pdf.String); ok {
						runs = append(runs, cur.Decode([]byte(s))...)
					}
				}
			}
			var text strings.Builder
			widths := map[string]float64{}
			for _, g := range runs {
				text.WriteString(g.Unicode)
				widths[g.Unicode] = g.WidthEm
			}
			seen++
			if cur.Vertical() {
				if text.String() != "縦書き、「テスト」ー。" {
					t.Errorf("vertical font %d: text %q", seen, text.String())
				}
				if g := runs[0]; g.W1Y != -1 || g.VX != 0.5 || g.VY != 0.88 {
					t.Errorf("vertical font %d: metrics %+v", seen, g)
				}
				continue
			}
			// fixture order: 90ms, 90msp, EUC, H, UCS2, UTF16, UTF8. 90ms/EUC
			// ASCII uses half-width CIDs (/W 500), 90msp/Uni* proportional
			// CIDs (/W 600); H has no ASCII, so the line is full-width (DW).
			want, latin, latinW := "日本語のテキスト、カナ。 Ab1", "A", []float64{0.5, 0.6, 0.5, 0, 0.6, 0.6, 0.6}[seen-1]
			if seen == 4 {
				want, latin, latinW = "日本語のテキスト、カナ。　Ａｂ１", "Ａ", 1
			}
			if text.String() != want {
				t.Errorf("font %d: text %q, want %q", seen, text.String(), want)
			}
			if widths["日"] != 1 || widths[latin] != latinW {
				t.Errorf("font %d: widths 日=%g %s=%g, want 1 and %g", seen, widths["日"], latin, widths[latin], latinW)
			}
			if sp := runs[len([]rune("日本語のテキスト、カナ。"))]; seen != 4 && sp.IsSpace != (sp.Bytes == 1) { // Tw: single-byte 32 only
				t.Errorf("font %d: space IsSpace=%v bytes=%d", seen, sp.IsSpace, sp.Bytes)
			}
		}
		stack = stack[:0]
	}
	if seen != 9 {
		t.Fatalf("checked %d show ops, want 9", seen)
	}
}
