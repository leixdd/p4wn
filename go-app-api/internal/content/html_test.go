package content

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"p4wn/internal/pdf"
)

func testdataPDF(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	// go-app-api/internal/content → repo root testdata/pdfs
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "pdfs", name)
	if _, err := os.Stat(root); err != nil {
		t.Skipf("fixture missing: %s", root)
	}
	return root
}

func TestAssembleHTMLDocumentOrder(t *testing.T) {
	html := AssembleHTMLDocument([]PageHTML{
		{PageNum: 0, Width: 10, Height: 20, SVG: `<svg id="p1"/>`},
		{PageNum: 2, Width: 10, Height: 20, SVG: `<svg id="p3"/>`},
	})
	i1 := strings.Index(html, `data-page="1"`)
	i3 := strings.Index(html, `data-page="3"`)
	if i1 < 0 || i3 < 0 || i1 > i3 {
		t.Fatalf("page order wrong:\n%s", html)
	}
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Fatal("missing doctype")
	}
}

func TestAssembleHTMLDocumentJapaneseLang(t *testing.T) {
	html := AssembleHTMLDocument([]PageHTML{
		{PageNum: 0, Width: 10, Height: 20, SVG: `<svg><text>あ</text></svg>`},
	})
	if !strings.Contains(html, `lang="ja"`) {
		t.Fatalf("expected lang=ja:\n%s", html)
	}
}

func TestRenderPageHTMLJapanese(t *testing.T) {
	path := testdataPDF(t, "japanese-identity-h.pdf")
	data, err := os.ReadFile(path)
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
	html := AssembleHTMLDocument([]PageHTML{frag})
	for _, want := range []string{"あ", "ア", "日", `lang="ja"`, "Hiragino Sans"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in:\n%s", want, html)
		}
	}
	// Selectable SVG text nodes (not outline-only)
	if !strings.Contains(html, ">あ</text>") {
		t.Fatalf("expected selectable hiragana text node:\n%s", html)
	}
}

func TestRenderPageHTMLSmoke(t *testing.T) {
	path := testdataPDF(t, "vec.pdf")
	data, err := os.ReadFile(path)
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
	if frag.Width <= 0 || frag.Height <= 0 {
		t.Fatalf("bad size %#v", frag)
	}
	if !strings.Contains(frag.SVG, "<svg") {
		t.Fatalf("no svg: %s", frag.SVG)
	}
	html := AssembleHTMLDocument([]PageHTML{frag})
	if !strings.Contains(html, "data-page=\"1\"") {
		t.Fatal(html)
	}
}
