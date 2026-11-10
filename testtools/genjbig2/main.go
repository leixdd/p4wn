// Generates testdata/pdfs/jbig2.pdf: JBIG2Decode images built by this
// directory's own T.88 encoder (and libtiff's tiffcp for MMR data). Check
// the decoded bitmaps against an independent decoder, e.g. poppler:
//
//	go run ./genjbig2 ../testdata/pdfs/jbig2.pdf
//	pdfimages -png ../testdata/pdfs/jbig2.pdf /tmp/jb   # poppler's bitmaps
//	go run ./genjbig2 -truth /tmp/truth.png            # expected page
//
// Page content (612x400 pt, 1 image pixel = 1 pt):
//   - Generic: a striped 400x180 JBIG2 page with five generic regions —
//     templates 0 (TPGDON), 1, 2, 3 (non-nominal AT pixels) and MMR —
//     ended by an end-of-stripe segment, drawn as a gray image.
//   - Text: a symbol dictionary in JBIG2Globals and a page with three text
//     regions (bottom-left corner; top-left with 4 strips, DS offset and
//     XOR; transposed top-right), drawn as a blue /ImageMask.
//   - Reuse: a second page using the same globals whose own dictionary
//     starts from the global dictionary's retained coding contexts
//     (T.88 7.4.2.2), drawn as a green /ImageMask. poppler 26.05 and pdf.js
//     ignore the "context used" flag and decode this one wrongly; every
//     other image matches poppler's pdfimages output bit for bit.
//   - Halftone: a halftone region segment, which p4wn does not support;
//     the image must be skipped with an error naming the segment.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func be32(v int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }

// segment writes a segment header (short forms) plus data.
func segment(num, typ, pageAssoc int, refs []int, data []byte) []byte {
	out := be32(num)
	out = append(out, byte(typ), byte(len(refs)<<5))
	for _, r := range refs {
		out = append(out, byte(r)) // segment numbers stay <= 256
	}
	out = append(out, byte(pageAssoc))
	out = append(out, be32(len(data))...)
	return append(out, data...)
}

// pageInfo: h = 0xffffffff means unknown height, which requires striping.
func pageInfo(w, h, stripe int) []byte {
	d := append(be32(w), be32(h)...)
	d = append(d, be32(0)...)
	d = append(d, be32(0)...)
	d = append(d, 0) // flags
	if stripe > 0 {
		return append(d, byte(0x80|stripe>>8), byte(stripe))
	}
	return append(d, 0, 0)
}

func regionInfo(w, h, x, y, op int) []byte {
	d := append(be32(w), be32(h)...)
	d = append(d, be32(x)...)
	d = append(d, be32(y)...)
	return append(d, byte(op))
}

var nominalAT = [4][2]int{{3, -1}, {-3, -1}, {2, -2}, {-2, -2}}

func genericRegion(b *bitmap, x, y, template int, at [4][2]int, tpgdon bool) []byte {
	d := regionInfo(b.w, b.h, x, y, 0)
	flags := byte(template << 1)
	if tpgdon {
		flags |= 8
	}
	d = append(d, flags)
	n := 1
	if template == 0 {
		n = 4
	}
	for i := 0; i < n; i++ {
		d = append(d, byte(int8(at[i][0])), byte(int8(at[i][1])))
	}
	e := newMQEncoder()
	encodeGeneric(e, make([]uint8, ctxSize[template]), b, template, at, tpgdon)
	return append(d, e.finish()...)
}

// textBitmap renders s with the 7x13 basic font scaled by k.
func textBitmap(s string, k int) *bitmap {
	w := len(s) * 7
	img := image.NewGray(image.Rect(0, 0, w, 13))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	(&font.Drawer{Dst: img, Src: image.Black, Face: basicfont.Face7x13, Dot: fixed.P(0, 11)}).DrawString(s)
	b := newBitmap(w*k, 13*k)
	for y := 0; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			if img.GrayAt(x/k, y/k).Y < 128 {
				b.set(x, y)
			}
		}
	}
	return b
}

// shapes is a bitmap with a frame, a disc, diagonal hatching and a label.
func shapes(w, h int, label string) *bitmap {
	b := newBitmap(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-w/4, y-h/2
			if x < 2 || y < 2 || x >= w-2 || y >= h-2 || dx*dx+dy*dy < (h/3)*(h/3) ||
				(x > w/2 && (x+y)%9 < 2 && y > h/2) {
				b.set(x, y)
			}
		}
	}
	t := textBitmap(label, 2)
	compose(b, t, w/2, 6)
	return b
}

func compose(dst, src *bitmap, x0, y0 int) {
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			if src.get(x, y) == 1 {
				dst.set(x0+x, y0+y)
			}
		}
	}
}

// mmr encodes b with tiffcp -c g4 (bit 1 = black, as MinIsWhite).
func mmr(b *bitmap) []byte {
	dir, err := os.MkdirTemp("", "genjbig2-")
	must(err)
	defer os.RemoveAll(dir)
	stride := (b.w + 7) / 8
	packed := make([]byte, stride*b.h)
	for y := 0; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			packed[y*stride+x/8] |= byte(b.get(x, y)) << (7 - x%8)
		}
	}
	var t bytes.Buffer
	le := binary.LittleEndian
	t.WriteString("II*\x00")
	binary.Write(&t, le, uint32(8))
	entries := [][3]uint32{{256, 4, uint32(b.w)}, {257, 4, uint32(b.h)}, {258, 3, 1}, {259, 3, 1},
		{262, 3, 0}, {273, 4, 8 + 2 + 9*12 + 4}, {277, 3, 1}, {278, 4, uint32(b.h)}, {279, 4, uint32(len(packed))}}
	binary.Write(&t, le, uint16(len(entries)))
	for _, e := range entries {
		binary.Write(&t, le, uint16(e[0]))
		binary.Write(&t, le, uint16(e[1]))
		binary.Write(&t, le, uint32(1))
		binary.Write(&t, le, e[2])
	}
	binary.Write(&t, le, uint32(0))
	t.Write(packed)
	in, out := filepath.Join(dir, "in.tif"), filepath.Join(dir, "out.tif")
	must(os.WriteFile(in, t.Bytes(), 0o644))
	cmd := exec.Command("tiffcp", "-c", "g4", "-r", fmt.Sprint(b.h), in, out)
	cmd.Stderr = os.Stderr
	must(cmd.Run())
	data, err := os.ReadFile(out)
	must(err)
	// single strip: StripOffsets (273) / StripByteCounts (279) are inline
	ifd := le.Uint32(data[4:])
	var off, n uint32
	for i := 0; i < int(le.Uint16(data[ifd:])); i++ {
		e := data[int(ifd)+2+i*12:]
		v := le.Uint32(e[8:])
		if le.Uint16(e[2:]) == 3 {
			v = uint32(le.Uint16(e[8:]))
		}
		switch le.Uint16(e) {
		case 273:
			off = v
		case 279:
			n = v
		}
	}
	return data[off : off+n]
}

// The generic-region test page and its regions (x, y, template, AT).
const gW, gH = 400, 180

type genRegion struct {
	b       *bitmap
	x, y, t int
	at      [4][2]int
	tpgdon  bool
	mmr     bool
}

func genericRegions() []genRegion {
	return []genRegion{
		{shapes(190, 80, "T0"), 5, 5, 0, nominalAT, true, false},
		{shapes(190, 80, "T1"), 205, 5, 1, [4][2]int{{-2, -2}}, false, false},
		{shapes(120, 80, "T2"), 5, 95, 2, [4][2]int{{-3, -1}}, true, false},
		{shapes(120, 80, "T3"), 140, 95, 3, [4][2]int{{1, -2}}, false, false},
		{shapes(120, 80, "MMR"), 275, 95, 0, nominalAT, false, true},
	}
}

func genericStream() []byte {
	var s []byte
	s = append(s, segment(0, 48, 1, nil, pageInfo(gW, 0xffffffff, gH))...)
	for i, r := range genericRegions() {
		var data []byte
		if r.mmr {
			data = append(regionInfo(r.b.w, r.b.h, r.x, r.y, 0), 1)
			data = append(data, mmr(r.b)...)
		} else {
			data = genericRegion(r.b, r.x, r.y, r.t, r.at, r.tpgdon)
		}
		s = append(s, segment(1+i, 38, 1, nil, data)...)
	}
	s = append(s, segment(6, 50, 1, nil, be32(gH-1))...) // end of stripe
	return append(s, segment(7, 49, 1, nil, nil)...)
}

// Text test: symbols are the distinct characters of the strings.
const tW, tH = 400, 150
const rW, rH = 140, 34 // context-reuse page

type textLine struct {
	s                  string
	x, y               int // reference corner position of the first symbol
	corner, strips, op int
	dsOffset           int
	transposed         bool
	dict2              bool // glyphs come from the second dictionary
}

var textLines = []textLine{
	{s: "JBIG2 text region", x: 10, y: 40, corner: 0, strips: 1},                      // bottom-left
	{s: "strips XOR offset", x: 10, y: 60, corner: 1, strips: 4, op: 2, dsOffset: -2}, // top-left
	{s: "TRANSPOSED", x: 380, y: 10, corner: 3, strips: 1, transposed: true},          // top-right
	{s: "2026 1110", x: 4, y: 29, corner: 0, strips: 1, dict2: true},                  // context-reuse page
}

// glyphs numbers the distinct characters: first those of the global
// dictionary's lines (n1 of them), then the second dictionary's.
func glyphs() (ids map[rune]int, syms []*bitmap, n1 int) {
	ids = map[rune]int{}
	for _, second := range []bool{false, true} {
		n1 = len(syms)
		for _, l := range textLines {
			if l.dict2 != second {
				continue
			}
			for _, r := range l.s {
				if _, ok := ids[r]; !ok && r != ' ' {
					ids[r] = len(syms)
					syms = append(syms, cropGlyph(textBitmap(string(r), 2)))
				}
			}
		}
	}
	return ids, syms, n1
}

// cropGlyph trims blank columns so symbols have varied widths.
func cropGlyph(b *bitmap) *bitmap {
	x0, x1 := b.w, 0
	for y := 0; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			if b.get(x, y) == 1 {
				x0, x1 = min(x0, x), max(x1, x+1)
			}
		}
	}
	if x0 >= x1 {
		return newBitmap(4, b.h)
	}
	out := newBitmap(x1-x0, b.h)
	for y := 0; y < b.h; y++ {
		copy(out.pix[y*out.w:], b.pix[y*b.w+x0:y*b.w+x1])
	}
	return out
}

// symbolDict codes syms with template 0 and nominal AT in one height class
// (all glyphs share one height), exporting them but none of the numInput
// referred symbols. gb carries the generic contexts between dictionaries:
// flags 0x200 = retain them, 0x100 = start from the retained ones.
func symbolDict(syms []*bitmap, numInput int, gb []uint8, flags int) []byte {
	d := []byte{byte(flags >> 8), byte(flags)} // arithmetic, no refagg, template 0
	for _, a := range nominalAT {
		d = append(d, byte(int8(a[0])), byte(int8(a[1])))
	}
	d = append(d, be32(len(syms))...)
	d = append(d, be32(len(syms))...)
	e := newMQEncoder()
	iadh, iadw, iaex := newEncInt(), newEncInt(), newEncInt()
	iadh.put(e, syms[0].h, false)
	w := 0
	for _, s := range syms {
		iadw.put(e, s.w-w, false)
		w = s.w
		encodeGeneric(e, gb, s, 0, nominalAT, false)
	}
	iadw.put(e, 0, true)
	iaex.put(e, numInput, false)  // referred symbols: not exported
	iaex.put(e, len(syms), false) // new symbols: all exported
	return append(d, e.finish()...)
}

func textRegion(l textLine, ids map[rune]int, syms []*bitmap, codeLen int) []byte {
	flags := l.corner<<4 | l.op<<7 | (l.dsOffset&0x1f)<<10
	for k := 0; 1<<k < l.strips; k++ {
		flags += 1 << 2
	}
	if l.transposed {
		flags |= 0x40
	}
	var runes []rune
	for _, r := range l.s {
		if r != ' ' {
			runes = append(runes, r)
		}
	}
	d := regionInfo(tW, tH, 0, 0, 0)
	d = append(d, byte(flags>>8), byte(flags))
	d = append(d, be32(len(runes))...)
	e := newMQEncoder()
	iadt, iafs, iads, iait := newEncInt(), newEncInt(), newEncInt(), newEncInt()
	iaid := make([]uint8, 1<<(codeLen+1))
	// one strip: T (the line's reference row or column) split into strip
	// base + CURT; S advances by the glyph extents plus gaps
	t, s0 := l.y, l.x
	if l.transposed {
		t, s0 = l.x, l.y
	}
	stripT := t / l.strips * l.strips
	iadt.put(e, 0, false) // initial STRIPT = 0
	iadt.put(e, stripT/l.strips, false)
	iafs.put(e, s0, false)
	curs := s0
	for i, r := range l.s {
		if r == ' ' {
			continue
		}
		sym := syms[ids[r]]
		if l.strips > 1 {
			iait.put(e, t-stripT, false)
		}
		putID(e, iaid, ids[r], codeLen)
		ext := sym.w
		if l.transposed {
			ext = sym.h
		}
		curs += ext - 1
		if i == len(l.s)-1 {
			iads.put(e, 0, true)
			break
		}
		gap := 3
		if l.s[i+1] == ' ' {
			gap = 12
		}
		iads.put(e, gap-l.dsOffset, false)
		curs += gap
	}
	return append(d, e.finish()...)
}

// textStreams: the global dictionary (retaining its coding contexts), the
// text page, and the context-reuse page whose own dictionary (segment 5)
// starts from those retained contexts (7.4.2.2).
func textStreams() (globals, page, reuse []byte) {
	ids, syms, n1 := glyphs()
	gb := make([]uint8, ctxSize[0])
	globals = segment(0, 0, 0, nil, symbolDict(syms[:n1], 0, gb, 0x200))
	page = segment(1, 48, 1, nil, pageInfo(tW, tH, 0))
	reuse = segment(1, 48, 1, nil, pageInfo(rW, rH, 0))
	reuse = append(reuse, segment(5, 0, 1, []int{0}, symbolDict(syms[n1:], n1, gb, 0x100))...)
	for i, l := range textLines {
		refs, n := []int{0}, n1
		if l.dict2 {
			refs, n = []int{0, 5}, len(syms)
		}
		codeLen := 0
		for 1<<codeLen < n {
			codeLen++
		}
		seg := segment(6+i, 6, 1, refs, textRegion(l, ids, syms, codeLen))
		if l.dict2 {
			reuse = append(reuse, seg...)
		} else {
			page = append(page, seg...)
		}
	}
	page = append(page, segment(20, 49, 1, nil, nil)...)
	reuse = append(reuse, segment(20, 49, 1, nil, nil)...)
	return globals, page, reuse
}

// truth draws the expected 1-bpp pages directly (no coding involved).
func truth() (g, text, reuse *bitmap) {
	g = newBitmap(gW, gH)
	for _, r := range genericRegions() {
		compose(g, r.b, r.x, r.y)
	}
	ids, syms, _ := glyphs()
	text, reuse = newBitmap(tW, tH), newBitmap(rW, rH)
	for _, l := range textLines {
		t := text
		if l.dict2 {
			t = reuse
		}
		curs := l.x
		if l.transposed {
			curs = l.y
		}
		for i, r := range l.s {
			if r == ' ' {
				continue
			}
			sym := syms[ids[r]]
			wi, hi := sym.w, sym.h
			x, y := curs, l.y
			if l.transposed {
				x, y = l.x, curs
			}
			if l.corner == 2 || l.corner == 3 {
				x -= wi - 1
			}
			if l.corner == 0 || l.corner == 2 {
				y -= hi - 1
			}
			for yy := 0; yy < hi; yy++ {
				for xx := 0; xx < wi; xx++ {
					if p := y + yy; p >= 0 && p < t.h && x+xx >= 0 && x+xx < t.w {
						t.pix[p*t.w+x+xx] |= byte(sym.get(xx, yy)) // glyphs never overlap: XOR = OR
					}
				}
			}
			ext := wi
			if l.transposed {
				ext = hi
			}
			curs += ext - 1
			if i < len(l.s)-1 {
				gap := 3
				if l.s[i+1] == ' ' {
					gap = 12
				}
				curs += gap
			}
		}
	}
	return g, text, reuse
}

func main() {
	truthOut := flag.String("truth", "", "write the expected page (same layout as the PDF at 72 dpi) as PNG and exit")
	flag.Parse()
	if *truthOut != "" {
		writeTruth(*truthOut)
		return
	}
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: genjbig2 out.pdf | genjbig2 -truth out.png")
		os.Exit(2)
	}
	gen := genericStream()
	glob, txt, reuse := textStreams()
	// halftone region (type 22) behind a valid page: must fail cleanly
	ht := segment(0, 48, 1, nil, pageInfo(40, 40, 0))
	ht = append(ht, segment(1, 22, 1, nil, append(regionInfo(40, 40, 0, 0, 0), make([]byte, 20)...))...)

	var buf bytes.Buffer
	offsets := map[int]int{}
	obj := func(num int, body string, stream []byte) {
		offsets[num] = buf.Len()
		if stream != nil {
			fmt.Fprintf(&buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, body, len(stream))
			buf.Write(stream)
			buf.WriteString("\nendstream\nendobj\n")
		} else {
			fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
		}
	}
	content := fmt.Sprintf("q %d 0 0 %d 10 210 cm /Gen Do Q\nq 0 0 1 rg %d 0 0 %d 10 40 cm /Txt Do Q\nq 40 0 0 40 500 300 cm /HT Do Q\n"+
		"q 0 0.5 0 rg %d 0 0 %d 450 40 cm /Reuse Do Q\n", gW, gH, tW, tH, rW, rH)
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 400] /Contents 4 0 R "+
		"/Resources << /XObject << /Gen 5 0 R /Txt 6 0 R /HT 8 0 R /Reuse 9 0 R >> >> >>", nil)
	obj(4, "", []byte(content))
	obj(5, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JBIG2Decode", gW, gH), gen)
	obj(6, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ImageMask true /Filter /JBIG2Decode /DecodeParms << /JBIG2Globals 7 0 R >>", tW, tH), txt)
	obj(7, "", glob)
	obj(8, "/Type /XObject /Subtype /Image /Width 40 /Height 40 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JBIG2Decode", ht)
	obj(9, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ImageMask true /Filter /JBIG2Decode /DecodeParms << /JBIG2Globals 7 0 R >>", rW, rH), reuse)
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 10\n0000000000 65535 f \n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 10 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	must(os.WriteFile(flag.Arg(0), buf.Bytes(), 0o644))
	fmt.Println("wrote", flag.Arg(0))
}

// writeTruth paints both expected bitmaps where the PDF draws them.
func writeTruth(path string) {
	g, t, r := truth()
	img := image.NewRGBA(image.Rect(0, 0, 612, 400))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	put := func(b *bitmap, x0, y0 int, c color.Color) {
		for y := 0; y < b.h; y++ {
			for x := 0; x < b.w; x++ {
				if b.get(x, y) == 1 {
					img.Set(x0+x, y0+y, c)
				}
			}
		}
	}
	put(g, 10, 400-210-gH, color.Black)
	put(t, 10, 400-40-tH, color.RGBA{0, 0, 255, 255})
	put(r, 450, 400-40-rH, color.RGBA{0, 128, 0, 255})
	f, err := os.Create(path)
	must(err)
	must(png.Encode(f, img))
	must(f.Close())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
