package jbig2_test

import (
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"

	"p4wn/internal/jbig2"
	"p4wn/internal/pdf"
)

// fixtureStreams returns the raw JBIG2 data of testdata/pdfs/jbig2.pdf
// (built by testtools/genjbig2): the generic-region page (obj 5), the text
// page (obj 6) and its JBIG2Globals (obj 7), the halftone page (obj 8) and
// the context-reuse page (obj 9, same globals).
func fixtureStreams(t *testing.T) (gen, txt, globals, halftone, reuse []byte) {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/pdfs/jbig2.pdf")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	raw := func(n int) []byte {
		s, ok := doc.Resolve(pdf.Ref{Num: n}).(*pdf.Stream)
		if !ok {
			t.Fatalf("object %d is not a stream", n)
		}
		return doc.RawStreamData(s)
	}
	return raw(5), raw(6), raw(7), raw(8), raw(9)
}

// golden is the oracle-checked page: poppler's pdfimages output equals the
// generator's truth bitmaps bit for bit, except for the context-reuse image
// (poppler ignores context reuse); 1 image pixel = 1 px at 72 dpi.
func golden(t *testing.T) image.Image {
	t.Helper()
	f, err := os.Open("../../../testdata/golden/jbig2-72.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func checkAgainstGolden(t *testing.T, name string, out []byte, w, h, x0, y0 int) {
	t.Helper()
	g := golden(t)
	stride := (w + 7) / 8
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, gg, b, _ := g.At(x0+x, y0+y).RGBA()
			want := r+gg+b < 3*0x8000
			if got := out[y*stride+x/8]>>(7-x%8)&1 == 1; got != want {
				diff++
			}
		}
	}
	if diff != 0 {
		t.Errorf("%s: %d pixels differ from the golden", name, diff)
	}
}

func TestDecodeGenericRegions(t *testing.T) {
	gen, _, _, _, _ := fixtureStreams(t)
	out, w, h, err := jbig2.Decode(nil, gen)
	if err != nil {
		t.Fatal(err)
	}
	if w != 400 || h != 180 { // striped page: height from the end-of-stripe
		t.Fatalf("size %dx%d", w, h)
	}
	checkAgainstGolden(t, "templates 0-3 + MMR", out, w, h, 10, 10)
}

func TestDecodeTextRegions(t *testing.T) {
	_, txt, globals, _, reuse := fixtureStreams(t)
	out, w, h, err := jbig2.Decode(globals, txt)
	if err != nil {
		t.Fatal(err)
	}
	if w != 400 || h != 150 {
		t.Fatalf("size %dx%d", w, h)
	}
	checkAgainstGolden(t, "symbol dictionary + text regions", out, w, h, 10, 210)
	if _, _, _, err := jbig2.Decode(nil, txt); err == nil || !strings.Contains(err.Error(), "missing segment") {
		t.Errorf("without globals: err = %v", err)
	}
	// a page dictionary starting from the global one's retained contexts
	out, w, h, err = jbig2.Decode(globals, reuse)
	if err != nil {
		t.Fatal(err)
	}
	checkAgainstGolden(t, "reused coding contexts", out, w, h, 450, 400-40-h)
}

// seg builds a segment header (segment number < 256, page 1) plus data.
func seg(num, typ int, refs []byte, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(num))
	out = append(out, byte(typ), byte(len(refs)<<5))
	out = append(out, refs...)
	out = append(out, 1)
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

func u32(vs ...uint32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	return b
}

func pageInfo(w, h uint32) []byte { return seg(0, 48, nil, append(u32(w, h, 0, 0), 0, 0, 0)) }

func region(w, h uint32) []byte { return append(u32(w, h, 0, 0), 0) }

// TestUnsupportedAndHostile: unsupported segments and hostile sizes fail
// with an error (never a panic or a huge allocation).
func TestUnsupportedAndHostile(t *testing.T) {
	_, _, _, halftone, _ := fixtureStreams(t)
	page := pageInfo(64, 64)
	cases := []struct {
		name, want string
		data       []byte
	}{
		{"halftone", "halftone region segment type 22", halftone},
		{"pattern dictionary", "pattern dictionary segment type 16", append(page, seg(1, 16, nil, make([]byte, 7))...)},
		{"refinement region", "generic refinement region segment type 42", append(page, seg(1, 42, nil, region(8, 8))...)},
		{"intermediate generic", "intermediate generic region segment type 36", append(page, seg(1, 36, nil, region(8, 8))...)},
		{"unknown type", "unknown segment type 33", append(page, seg(1, 33, nil, nil)...)},
		{"Huffman symbol dictionary", "Huffman coding", append(page, seg(1, 0, nil, []byte{0, 1})...)},
		{"refagg symbol dictionary", "refinement/aggregate", append(page, seg(1, 0, nil, append([]byte{0, 2}, make([]byte, 16)...))...)},
		{"Huffman text region", "Huffman coding", append(page, seg(1, 6, nil, append(region(8, 8), 0, 1))...)},
		{"refined text region", "refinement", append(page, seg(1, 6, nil, append(region(8, 8), 0, 2))...)},
		{"huge page", "too large", pageInfo(1<<20, 1<<20)},
		{"huge region", "too large", append(page, seg(1, 38, nil, append(region(1<<16, 1<<16), 0))...)},
		{"huge symbol count", "symbols", append(page, seg(1, 0, nil, append([]byte{0, 0, 3, 0xFF, 0, 0, 0, 0, 0, 0}, u32(1, 0xFFFFFFFF)...))...)},
		{"huge instance count", "instances", append(page, seg(1, 6, nil, append(append(region(8, 8), 0, 0), u32(0xFFFFFFFF)...))...)},
		{"truncated header", "truncated", append(page, 0, 0, 0, 1, 38)},
		{"truncated data", "truncated", append(page, seg(1, 38, nil, region(8, 8))[:20]...)},
		{"no page", "page information", seg(1, 38, nil, append(region(8, 8), 0, 3, 0xFF, 0, 0, 0, 0, 0, 0))},
		{"bad referral", "missing segment", append(page, seg(1, 6, []byte{9}, append(append(region(8, 8), 0, 0), u32(1)...))...)},
	}
	for _, c := range cases {
		_, _, _, err := jbig2.Decode(nil, c.data)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// BenchmarkGenericPage decodes a 600 dpi A4 page (4960x7016) as one
// template-0 generic region (jbig2enc's generic-mode layout) without
// TPGDON, so every pixel is arithmetic-decoded: the worst case per page.
// The coded bytes are arbitrary; cost per pixel does not depend on them.
func BenchmarkGenericPage(b *testing.B) {
	const w, h = 4960, 7016
	coded := make([]byte, 400<<10)
	for i := range coded {
		coded[i] = byte(i*7919>>3) ^ byte(i>>11)
	}
	data := pageInfo(w, h)
	gr := append(region(w, h), 0, 3, 0xFF, 0xFD, 0xFF, 2, 0xFE, 0xFE, 0xFE) // template 0, no TPGDON: every pixel coded
	data = append(data, seg(1, 38, nil, append(gr, coded...))...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := jbig2.Decode(nil, data); err != nil {
			b.Fatal(err)
		}
	}
}
