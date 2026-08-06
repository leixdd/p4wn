// Generates a minimal Type0 Identity-H PDF with Japanese ToUnicode mappings
// (hiragana あ, katakana ア, kanji 日) for HTML selectable-text tests.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genjp out.pdf")
		os.Exit(2)
	}
	content := "BT /F1 24 Tf 50 100 Td <3042> Tj 30 0 Td <30A2> Tj 30 0 Td <65E5> Tj ET\n"
	tounicode := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
3 beginbfchar
<3042> <3042>
<30A2> <30A2>
<65E5> <65E5>
endbfchar
endcmap
CMapName currentdict /CMap defineresource pop
end
end
`

	var out []byte
	offsets := map[int]int{}
	write := func(s string) { out = append(out, s...) }
	obj := func(num int, body string, stream []byte) {
		offsets[num] = len(out)
		if stream != nil {
			write(fmt.Sprintf("%d 0 obj\n<< %s /Length %d >>\nstream\n", num, body, len(stream)))
			out = append(out, stream...)
			write("\nendstream\nendobj\n")
		} else {
			write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", num, body))
		}
	}

	write("%PDF-1.7\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 400 200] >>", nil)
	obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>", nil)
	obj(4, "", []byte(content))
	obj(5, `<< /Type /Font /Subtype /Type0 /BaseFont /JapanTest /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 8 0 R >>`, nil)
	obj(6, `<< /Type /Font /Subtype /CIDFontType2 /BaseFont /JapanTest /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor 7 0 R /DW 1000 /CIDToGIDMap /Identity >>`, nil)
	obj(7, `<< /Type /FontDescriptor /FontName /JapanTest /Flags 4 /FontBBox [0 -200 1000 800] /ItalicAngle 0 /Ascent 800 /Descent -200 /CapHeight 700 /StemV 80 >>`, nil)
	obj(8, "", []byte(tounicode))

	xref := len(out)
	write(fmt.Sprintf("xref\n0 9\n0000000000 65535 f \n"))
	for i := 1; i <= 8; i++ {
		write(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	write(fmt.Sprintf("trailer\n<< /Size 9 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref))

	if err := os.WriteFile(os.Args[1], out, 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote", os.Args[1])
}
