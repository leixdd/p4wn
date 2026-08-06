package content

import (
	"bytes"
	"context"
	"flag"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"p4wn/internal/pdf"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func renderFixture(t *testing.T, name string, opts RenderOptions) image.Image {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../../testdata/pdfs", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	pix, err := RenderPage(context.Background(), doc, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	return pix.ToImage()
}

// TestGolden renders committed fixtures and compares pixel-exact against
// committed goldens (own-renderer regression pinning; run with -update to
// regenerate after intentional changes).
func TestGolden(t *testing.T) {
	cases := []struct {
		pdfName string
		golden  string
		opts    RenderOptions
	}{
		{"vec.pdf", "vec-144.png", RenderOptions{DPI: 144}},
		{"vec.pdf", "vec-72-gray.png", RenderOptions{DPI: 72, Gray: true}},
		{"img.pdf", "img-144.png", RenderOptions{DPI: 144}},
		{"text.pdf", "text-144.png", RenderOptions{DPI: 144}},
		{"embedttf.pdf", "embedttf-144.png", RenderOptions{DPI: 144}},
		{"clip.pdf", "clip-144.png", RenderOptions{DPI: 144}},
		{"enc-aes-256.pdf", "enc-aes-256-72.png", RenderOptions{DPI: 72}},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			img := renderFixture(t, c.pdfName, c.opts)
			goldenPath := filepath.Join("../../../testdata/golden", c.golden)
			var got bytes.Buffer
			if err := png.Encode(&got, img); err != nil {
				t.Fatal(err)
			}
			if *updateGolden {
				if err := os.WriteFile(goldenPath, got.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			if !pixelsEqual(t, got.Bytes(), want) {
				out := filepath.Join(t.TempDir(), c.golden)
				os.WriteFile(out, got.Bytes(), 0o644)
				t.Errorf("render differs from golden %s (actual saved to %s)", c.golden, out)
			}
		})
	}
}

// pixelsEqual decodes both PNGs and compares samples (encoder settings may
// legitimately differ).
func pixelsEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	imgA, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	imgB, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if imgA.Bounds() != imgB.Bounds() {
		return false
	}
	bo := imgA.Bounds()
	for y := bo.Min.Y; y < bo.Max.Y; y++ {
		for x := bo.Min.X; x < bo.Max.X; x++ {
			r1, g1, b1, a1 := imgA.At(x, y).RGBA()
			r2, g2, b2, a2 := imgB.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				return false
			}
		}
	}
	return true
}

// TestRenderAllFixtures smoke-renders every committed fixture (no panics,
// non-empty output).
func TestRenderAllFixtures(t *testing.T) {
	entries, err := os.ReadDir("../../../testdata/pdfs")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			img := renderFixture(t, e.Name(), RenderOptions{DPI: 96})
			if img.Bounds().Dx() < 10 || img.Bounds().Dy() < 10 {
				t.Errorf("suspicious size %v", img.Bounds())
			}
		})
	}
}
