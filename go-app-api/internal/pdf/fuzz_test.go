package pdf

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzOpen throws mutated PDFs at the whole document layer: it must never
// panic — errors are fine.
func FuzzOpen(f *testing.F) {
	entries, _ := os.ReadDir("../../../testdata/pdfs")
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("../../../testdata/pdfs", e.Name()))
		if err == nil {
			f.Add(data)
		}
	}
	f.Add([]byte("%PDF-1.7\ntrailer<</Root 1 0 R>>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Open(data)
		if err != nil || doc == nil {
			return
		}
		// touch the machinery: pages, content, resolution
		for i := 0; i < doc.NumPages() && i < 3; i++ {
			page, err := doc.GetPage(i)
			if err != nil {
				continue
			}
			page.ContentStreams()
		}
	})
}

// FuzzLexer must terminate and never panic on arbitrary bytes.
func FuzzLexer(f *testing.F) {
	f.Add([]byte("<< /A [1 2.5 (str\\) more) /N#20ame <DEAD>] >> stream"))
	f.Add([]byte("1.2.3 --4 +.5 % comment\n(unterminated"))
	f.Fuzz(func(t *testing.T, data []byte) {
		lex := NewLexer(data)
		for i := 0; i < 100000; i++ {
			tok := lex.Next()
			if tok.Kind == TokEOF {
				return
			}
		}
	})
}
