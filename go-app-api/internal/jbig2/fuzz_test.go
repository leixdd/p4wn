package jbig2

import (
	"bytes"
	"os"
	"strconv"
	"testing"
)

// FuzzDecode: arbitrary streams must not panic (decode, not Decode, so the
// recover safety net does not hide bugs) or allocate past the caps.
//
//	go test p4wn/internal/jbig2 -fuzz FuzzDecode
func FuzzDecode(f *testing.F) {
	// seeds: the JBIG2 streams of the fixture PDF
	pdf, _ := os.ReadFile("../../../testdata/pdfs/jbig2.pdf")
	var streams [][]byte
	for rest := pdf; ; {
		i := bytes.Index(rest, []byte("/Length "))
		if i < 0 {
			break
		}
		rest = rest[i+8:]
		end := bytes.IndexAny(rest, " >")
		n, err := strconv.Atoi(string(rest[:end]))
		j := bytes.Index(rest, []byte("stream\n"))
		if err != nil || j < 0 || j+7+n > len(rest) {
			continue
		}
		streams = append(streams, rest[j+7:j+7+n])
	}
	// file order: page content, generic page, text page, globals,
	// halftone, context-reuse page
	for i, s := range streams {
		var g []byte
		if (i == 2 || i == 5) && len(streams) > 3 {
			g = streams[3] // pages that need the globals
		}
		f.Add(g, s)
	}
	f.Fuzz(func(t *testing.T, globals, data []byte) {
		decode(globals, data)
	})
}
