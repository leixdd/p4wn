package content

import (
	"context"
	"os"
	"strings"
	"testing"

	"p4wn/internal/pdf"
)

// TestPageAreaCap: an A0 page at 300 dpi (139 MP) exceeds the default 80 MP
// cap and fails with a clear error before the pixmap is allocated; a
// smaller explicit cap applies too, and lower dpi still renders.
func TestPageAreaCap(t *testing.T) {
	data, err := os.ReadFile(testdataPDF(t, "large-format-a0.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RenderPage(context.Background(), doc, 0, RenderOptions{DPI: 300})
	if err == nil || !strings.Contains(err.Error(), "9934x14042 px (139.5 MP) at 300 dpi, over the 80.0 MP page limit; use dpi <= 227") {
		t.Fatalf("300 dpi: err = %v", err)
	}
	_, err = RenderPage(context.Background(), doc, 0, RenderOptions{DPI: 30, MaxPixels: 500_000})
	if err == nil || !strings.Contains(err.Error(), "over the 0.5 MP page limit") {
		t.Fatalf("custom cap: err = %v", err)
	}
	pix, err := RenderPage(context.Background(), doc, 0, RenderOptions{DPI: 227})
	if err != nil {
		t.Fatalf("227 dpi (suggested) failed: %v", err)
	}
	if px := pix.W * pix.H; px > DefaultMaxPagePixels {
		t.Fatalf("rendered %d px", px)
	}
}
