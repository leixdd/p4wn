// Generates a PDF with an embedded TrueType font (Go Regular) to exercise
// the FontFile2 path: subsetless embed, WinAnsiEncoding, explicit Widths.
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func main() {
	ttf := goregular.TTF
	f, err := sfnt.Parse(ttf)
	if err != nil {
		panic(err)
	}
	upem := float64(f.UnitsPerEm())
	var buf sfnt.Buffer

	// widths for codes 32..126 in 1000-unit space
	widths := make([]int, 95)
	for c := 32; c <= 126; c++ {
		gid, _ := f.GlyphIndex(&buf, rune(c))
		adv, _ := f.GlyphAdvance(&buf, gid, fixed.I(int(upem)), 0)
		widths[c-32] = int(float64(adv) / 64 / upem * 1000)
	}
	wstr := ""
	for _, w := range widths {
		wstr += fmt.Sprintf("%d ", w)
	}

	var zf bytes.Buffer
	zw := zlib.NewWriter(&zf)
	zw.Write(ttf)
	zw.Close()

	var out bytes.Buffer
	offsets := map[int]int{}
	obj := func(num int, body string, stream []byte) {
		offsets[num] = out.Len()
		if stream != nil {
			fmt.Fprintf(&out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, body, len(stream))
			out.Write(stream)
			out.WriteString("\nendstream\nendobj\n")
		} else {
			fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", num, body)
		}
	}
	out.WriteString("%PDF-1.7\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 400 200] >>", nil)
	obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /E1 5 0 R >> >> >>", nil)
	content := `BT
/E1 24 Tf 20 150 Td (Embedded TrueType!) Tj
/E1 14 Tf 0 -30 Td (quick brown fox 0123456789) Tj
0 -25 Td (WIDTH-test iiiiii MMMMMM .,;:!?) Tj
ET`
	obj(4, "", []byte(content))
	obj(5, `<< /Type /Font /Subtype /TrueType /BaseFont /GoRegular /FirstChar 32 /LastChar 126 /Widths [`+wstr+`] /Encoding /WinAnsiEncoding /FontDescriptor 6 0 R >>`, nil)
	obj(6, `<< /Type /FontDescriptor /FontName /GoRegular /Flags 32 /FontBBox [-200 -300 1200 1000] /ItalicAngle 0 /Ascent 800 /Descent -200 /CapHeight 700 /StemV 80 /FontFile2 7 0 R >>`, nil)
	obj(7, fmt.Sprintf("/Length1 %d /Filter /FlateDecode", len(ttf)), zf.Bytes())

	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 8\n0000000000 65535 f \n")
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	os.WriteFile(os.Args[1], out.Bytes(), 0o644)
	fmt.Println("wrote", os.Args[1])
}
