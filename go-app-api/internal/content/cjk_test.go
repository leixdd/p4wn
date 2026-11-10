package content

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"

	"p4wn/internal/font"
	"p4wn/internal/pdf"
)

// cjkTestFont finds a CJK font for the fallback tests: $P4WN_TEST_CJK_FONT,
// else common macOS / Debian locations. Pixel output depends on the font, so
// these tests check ink placement, not goldens.
func cjkTestFont(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		os.Getenv("P4WN_TEST_CJK_FONT"),
		"/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/ipafont-gothic/ipag.ttf",
	} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no CJK font found (set P4WN_TEST_CJK_FONT)")
	return ""
}

// inkBox returns the bounding box of dark pixels inside r (empty if none).
func inkBox(img image.Image, r image.Rectangle) image.Rectangle {
	var box image.Rectangle
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if cr, cg, cb, _ := img.At(x, y).RGBA(); cr+cg+cb < 3*0x8000 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

func TestCJKFallbackRender(t *testing.T) {
	cjkPath := cjkTestFont(t)
	// without the fallback, non-embedded CJK glyphs are blank (as before)
	img := renderFixture(t, "japanese-identity-h.pdf", RenderOptions{DPI: 72})
	if b := inkBox(img, img.Bounds()); !b.Empty() {
		t.Fatalf("ink without -cjk-font: %v", b)
	}
	if err := font.SetCJKFallback(cjkPath); err != nil {
		t.Fatal(err)
	}
	defer font.SetCJKFallback("")

	img = renderFixture(t, "japanese-identity-h.pdf", RenderOptions{DPI: 72})
	// あ ア 日 at x = 50, 80, 110 on baseline y = 100 (24 pt): three glyphs
	for i, x := range []int{50, 80, 110} {
		if b := inkBox(img, image.Rect(x, 75, x+24, 105)); b.Dx() < 8 || b.Dy() < 8 {
			t.Errorf("identity-h glyph %d: ink %v", i, b)
		}
	}

	// gencjk fixture at 72 dpi: page 620x400 pt, lines at y = 370, 336, ...
	// (PDF) from x = 120; vertical columns at x = 470 and 550 from y = 370.
	img = renderFixture(t, "japanese-cmaps.pdf", RenderOptions{DPI: 72})
	for line := 0; line < 7; line++ {
		top := 400 - (370 + 16) + line*34
		if b := inkBox(img, image.Rect(120, top, 330, top+22)); b.Dx() < 180 {
			t.Errorf("line %d: CJK ink %v, want ~200 px wide", line, b)
		}
	}
	for col, x := range []int{470, 550} {
		b := inkBox(img, image.Rect(x-12, 400-372, x+12, 400))
		if b.Dy() < 180 || b.Dx() > 24 {
			t.Errorf("vertical column %d: ink %v, want a tall narrow column", col, b)
		}
	}
}

// TestJapaneseCMapsHTML: predefined CMaps give selectable Unicode text in
// HTML even with no font file at all.
func TestJapaneseCMapsHTML(t *testing.T) {
	data, err := os.ReadFile(testdataPDF(t, "japanese-cmaps.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	frag, err := RenderPageHTML(context.Background(), doc, 0)
	if err != nil {
		t.Fatal(err)
	}
	for ch, want := range map[string]int{"日": 7, "語": 7, "カ": 7, "縦": 2, "テ": 9} {
		if n := strings.Count(frag.SVG, ">"+ch+"<"); n != want {
			t.Errorf("%q appears %d times as text, want %d", ch, n, want)
		}
	}
}
