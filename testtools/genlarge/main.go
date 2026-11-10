// Generates testdata/pdfs/large-format-a0.pdf: one A0 page (2384x3370 pt)
// with a frame and a diagonal grid. At 300 dpi it is 9934x14042 px
// (139 MP): over p4wn's default 80 MP page cap, so it must fail cleanly.
//
//	go run ./genlarge ../testdata/pdfs/large-format-a0.pdf
package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genlarge out.pdf")
		os.Exit(2)
	}
	var content bytes.Buffer
	content.WriteString("20 w 0 0 0.6 RG 50 50 2284 3270 re S\n4 w 0.8 0 0 RG\n")
	for i := 0; i <= 3370; i += 200 {
		fmt.Fprintf(&content, "0 %d m 2384 %d l S\n", i, i+600)
	}
	content.WriteString("0 0 0 rg 400 1500 1584 400 re f\n")

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
	buf.WriteString("%PDF-1.4\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 2384 3370] /Contents 4 0 R >>", nil)
	obj(4, "", content.Bytes())
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 5\n0000000000 65535 f \n")
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if err := os.WriteFile(os.Args[1], buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote", os.Args[1])
}
