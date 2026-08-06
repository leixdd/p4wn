package pdf

import (
	"bytes"
	"fmt"
	"testing"
)

// buildTestPDF assembles a classic-xref PDF with correct offsets:
// catalog → pages → two pages (one rotated, one with own MediaBox).
func buildTestPDF() []byte {
	var buf bytes.Buffer
	offsets := map[int]int{}
	addObj := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	buf.WriteString("%PDF-1.7\n")
	addObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	addObj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 /MediaBox [0 0 612 792] /Resources << >> >>")
	addObj(3, "<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>")
	addObj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 400] /Rotate 90 >>")
	content := "0 0 100 100 re f"
	addObj(5, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))

	xrefPos := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefPos)
	return buf.Bytes()
}

func TestOpenClassicXref(t *testing.T) {
	doc, err := Open(buildTestPDF())
	if err != nil {
		t.Fatal(err)
	}
	if doc.NumPages() != 2 {
		t.Fatalf("NumPages = %d, want 2", doc.NumPages())
	}

	p0, err := doc.GetPage(0)
	if err != nil {
		t.Fatal(err)
	}
	if p0.MediaBox != [4]float64{0, 0, 612, 792} {
		t.Errorf("page 0 inherited MediaBox = %v", p0.MediaBox)
	}
	if p0.Rotate != 0 {
		t.Errorf("page 0 Rotate = %d", p0.Rotate)
	}
	if p0.Resources == nil {
		t.Error("page 0 should inherit Resources")
	}
	content, err := p0.ContentStreams()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("100 100 re")) {
		t.Errorf("content = %q", content)
	}

	p1, err := doc.GetPage(1)
	if err != nil {
		t.Fatal(err)
	}
	if p1.MediaBox != [4]float64{0, 0, 200, 400} {
		t.Errorf("page 1 own MediaBox = %v", p1.MediaBox)
	}
	if p1.Rotate != 90 {
		t.Errorf("page 1 Rotate = %d", p1.Rotate)
	}
}

func TestRepairFallback(t *testing.T) {
	data := buildTestPDF()
	// destroy the startxref offset so classic loading fails
	data = bytes.Replace(data, []byte("startxref"), []byte("startxren"), 1)
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("repair path: %v", err)
	}
	if doc.NumPages() != 2 {
		t.Fatalf("repaired NumPages = %d, want 2", doc.NumPages())
	}
	p0, err := doc.GetPage(0)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := p0.ContentStreams()
	if !bytes.Contains(content, []byte("re")) {
		t.Errorf("repaired content = %q", content)
	}
}

func TestResolveCycle(t *testing.T) {
	// object 1 refers to itself; Resolve must not loop forever
	d := &Document{
		xref:  map[int]xrefEntry{},
		cache: map[int]Object{1: Ref{Num: 1}},
	}
	if _, ok := d.Resolve(Ref{Num: 1}).(Null); !ok {
		t.Error("self-referencing ref should resolve to Null")
	}
}
