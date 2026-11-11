// Generates testdata/pdfs/japanese-cmaps.pdf: non-embedded Japanese Type0
// fonts (no ToUnicode) using predefined CMaps, one per line, plus two
// vertical (-V) columns. Unicode, CIDs and widths must come from the CMap.
//
//	go run ./gencjk ../testdata/pdfs/japanese-cmaps.pdf
package main

import (
	"bytes"
	"fmt"
	"os"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
)

// Text is the line every horizontal font shows: kanji, kana, punctuation,
// then ASCII (single-byte in RKSJ/EUC, so /W widths by CID apply).
const Text = "日本語のテキスト、カナ。 Ab1"

type font struct {
	cmap string
	w    string // CIDFont /W
	enc  func(string) []byte
}

func sjis(s string) []byte {
	b, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(s))
	must(err)
	return b
}

func euc(s string) []byte {
	b, err := japanese.EUCJP.NewEncoder().Bytes([]byte(s))
	must(err)
	return b
}

// jis is ISO-2022-JP's two-byte JIS X 0208 form (the "H" CMap): EUC-JP with
// the high bits cleared. ASCII has no code in H, so it is sent full-width.
func jis(s string) []byte {
	var full []rune
	for _, r := range s {
		switch {
		case r == ' ':
			r = '　'
		case r > ' ' && r < 0x7f:
			r += 0xfee0
		}
		full = append(full, r)
	}
	b := euc(string(full))
	for i := range b {
		b[i] &^= 0x80
	}
	return b
}

func ucs2(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r>>8), byte(r))
	}
	return b
}

func utf16be(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return b
}

func utf8b(s string) []byte {
	if !utf8.ValidString(s) {
		panic("bad utf8")
	}
	return []byte(s)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func hexStr(b []byte) string { return fmt.Sprintf("<%X>", b) }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gencjk out.pdf")
		os.Exit(2)
	}
	// half-width CIDs 231–389 are 500 wide; proportional ASCII (CIDs 1–100)
	// gets a distinctive 600 so the widths visibly come from /W by CID
	const halfW = "[231 389 500]"
	const propW = "[1 100 600]"
	fonts := []font{
		{"90ms-RKSJ-H", halfW, sjis},
		{"90msp-RKSJ-H", propW, sjis},
		{"EUC-H", halfW, euc},
		{"H", "", jis},
		{"UniJIS-UCS2-H", propW, ucs2},
		{"UniJIS-UTF16-H", propW, utf16be},
		{"UniJIS-UTF8-H", propW, utf8b},
	}
	vertical := []font{
		{"UniJIS-UTF16-V", "", utf16be},
		{"90ms-RKSJ-V", halfW, sjis},
	}
	const vText = "縦書き、「テスト」ー。"

	var content bytes.Buffer
	y := 370
	for i, f := range fonts {
		fmt.Fprintf(&content, "BT /L 9 Tf 20 %d Td (%s) Tj ET\n", y, f.cmap)
		fmt.Fprintf(&content, "BT /F%d 18 Tf 120 %d Td %s Tj ET\n", i, y, hexStr(f.enc(Text)))
		y -= 34
	}
	for i, f := range vertical {
		x := 470 + i*80
		fmt.Fprintf(&content, "BT /L 7 Tf %d 385 Td (%s) Tj ET\n", x-25, f.cmap)
		// TJ with a 500-unit gap: in vertical mode it moves down, not right
		fmt.Fprintf(&content, "BT /V%d 18 Tf %d 370 Td [%s 500 %s] TJ ET\n", i, x,
			hexStr(f.enc(vText[:len("縦書き")])), hexStr(f.enc(vText[len("縦書き"):])))
	}

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
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	var fontRes bytes.Buffer
	fontRes.WriteString("/L 6 0 R ")
	next := 7
	all := append(append([]font{}, fonts...), vertical...)
	for i, f := range all {
		name := fmt.Sprintf("F%d", i)
		if i >= len(fonts) {
			name = fmt.Sprintf("V%d", i-len(fonts))
		}
		fmt.Fprintf(&fontRes, "/%s %d 0 R ", name, next)
		w := ""
		if f.w != "" {
			w = "/W " + f.w
		}
		obj(next, fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /MS-Mincho /Encoding /%s /DescendantFonts [%d 0 R] >>", f.cmap, next+1), nil)
		obj(next+1, fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType0 /BaseFont /MS-Mincho "+
			"/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> "+
			"/FontDescriptor %d 0 R /DW 1000 %s >>", next+2, w), nil)
		obj(next+2, "<< /Type /FontDescriptor /FontName /MS-Mincho /Flags 6 /FontBBox [0 -141 1000 859] "+
			"/ItalicAngle 0 /Ascent 859 /Descent -141 /CapHeight 700 /StemV 80 >>", nil)
		next += 3
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	obj(3, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 620 400] /Contents 4 0 R /Resources << /Font << %s>> >> >>", fontRes.String()), nil)
	obj(4, "", content.Bytes())
	obj(5, "<< >>", nil)
	obj(6, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", nil)

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", next)
	for i := 1; i < next; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", next, xref)
	must(os.WriteFile(os.Args[1], buf.Bytes(), 0o644))
	fmt.Println("wrote", os.Args[1])
}
