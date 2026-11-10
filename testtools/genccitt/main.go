// Generates testdata/pdfs/ccitt.pdf: CCITT fax images encoded by libtiff
// (tiffcp must be on PATH) as an independent encoder.
//
//	go run ./genccitt ../testdata/pdfs/ccitt.pdf
//	go run ./genccitt -hex   # print small G4/G3 streams for filter unit tests
//
// The page shows the same 250x120 bitmap four ways: Group 4 as a gray image,
// Group 3 1D (EOL before each row) as a red /ImageMask, Group 4 with
// /BlackIs1 true + /Decode [1 0], and a small Group 4 inline image (/CCF).
// A fifth image is Group 3 2D (K>0), which p4wn does not support: it must be
// skipped with a warning, leaving that area blank.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Bitmap is 1 = black, packed MSB-first, rows byte-aligned (TIFF MinIsWhite).
type bitmap struct {
	w, h int
	pix  []byte
}

func newBitmap(w, h int) *bitmap { return &bitmap{w, h, make([]byte, (w+7)/8*h)} }

func (b *bitmap) set(x, y int) {
	if x >= 0 && y >= 0 && x < b.w && y < b.h {
		b.pix[y*((b.w+7)/8)+x/8] |= 0x80 >> (x % 8)
	}
}

// pattern draws the test bitmap: frame, filled disc, diagonal, bars, checker.
func pattern(w, h int) *bitmap {
	b := newBitmap(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			frame := x < 3 || y < 3 || x >= w-3 || y >= h-3
			dx, dy := x-h/2, y-h/2
			disc := dx*dx+dy*dy < (h/3)*(h/3)
			diag := x-y >= h && x-y < h+4
			bars := x >= w*3/4 && (x/5)%2 == 0 && y > h/4 && y < h*3/4
			check := x >= w/2 && x < w*3/4-8 && y >= h*2/3 && ((x/4)+(y/4))%2 == 0
			if frame || disc || diag || bars || check {
				b.set(x, y)
			}
		}
	}
	return b
}

// writeTIFF writes an uncompressed single-strip bilevel MinIsWhite TIFF.
func writeTIFF(path string, b *bitmap) error {
	var buf bytes.Buffer
	le := binary.LittleEndian
	buf.WriteString("II*\x00")
	binary.Write(&buf, le, uint32(8))
	type entry struct {
		tag, typ uint16
		val      uint32
	}
	const nEntries = 9
	dataOff := uint32(8 + 2 + nEntries*12 + 4)
	entries := []entry{
		{256, 4, uint32(b.w)}, {257, 4, uint32(b.h)}, {258, 3, 1}, {259, 3, 1},
		{262, 3, 0}, {273, 4, dataOff}, {277, 3, 1}, {278, 4, uint32(b.h)},
		{279, 4, uint32(len(b.pix))},
	}
	binary.Write(&buf, le, uint16(len(entries)))
	for _, e := range entries {
		binary.Write(&buf, le, e.tag)
		binary.Write(&buf, le, e.typ)
		binary.Write(&buf, le, uint32(1))
		if e.typ == 3 {
			binary.Write(&buf, le, uint16(e.val))
			binary.Write(&buf, le, uint16(0))
		} else {
			binary.Write(&buf, le, e.val)
		}
	}
	binary.Write(&buf, le, uint32(0))
	buf.Write(b.pix)
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// stripData returns the concatenated strip bytes of a TIFF's first IFD.
func stripData(data []byte) ([]byte, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if string(data[:2]) == "MM" {
		bo = binary.BigEndian
	}
	ifd := bo.Uint32(data[4:])
	n := int(bo.Uint16(data[ifd:]))
	var offs, counts []uint32
	for i := 0; i < n; i++ {
		e := data[int(ifd)+2+i*12:]
		tag, typ, cnt := bo.Uint16(e), bo.Uint16(e[2:]), bo.Uint32(e[4:])
		if tag != 273 && tag != 279 {
			continue
		}
		size := uint32(4)
		if typ == 3 {
			size = 2
		}
		vals := e[8:12]
		if size*cnt > 4 {
			vals = data[bo.Uint32(e[8:]):]
		}
		var out []uint32
		for k := uint32(0); k < cnt; k++ {
			if size == 2 {
				out = append(out, uint32(bo.Uint16(vals[k*2:])))
			} else {
				out = append(out, bo.Uint32(vals[k*4:]))
			}
		}
		if tag == 273 {
			offs = out
		} else {
			counts = out
		}
	}
	if len(offs) != 1 || len(counts) != 1 {
		return nil, fmt.Errorf("want one strip, got %d/%d", len(offs), len(counts))
	}
	return data[offs[0] : offs[0]+counts[0]], nil
}

// encode runs tiffcp with the given compression and returns the strip.
func encode(dir string, b *bitmap, comp string) []byte {
	in := filepath.Join(dir, "in.tif")
	out := filepath.Join(dir, "out.tif")
	if err := writeTIFF(in, b); err != nil {
		panic(err)
	}
	os.Remove(out)
	cmd := exec.Command("tiffcp", "-c", comp, "-r", fmt.Sprint(b.h), in, out)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Errorf("tiffcp -c %s: %w", comp, err))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		panic(err)
	}
	s, err := stripData(data)
	if err != nil {
		panic(err)
	}
	return s
}

func main() {
	hexMode := flag.Bool("hex", false, "print a small 20x6 pattern's G4 and G3-1D streams as Go literals")
	flag.Parse()
	dir, err := os.MkdirTemp("", "genccitt-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	if *hexMode {
		b := newBitmap(20, 6) // same rule as filter.TestCCITT's want()
		for y := 0; y < b.h; y++ {
			for x := 0; x < b.w; x++ {
				if (x/3+y/2)%2 == 0 || x == y*3 {
					b.set(x, y)
				}
			}
		}
		fmt.Printf("bitmap (1=black): %#v\n", b.pix)
		fmt.Printf("g4: %#v\n", encode(dir, b, "g4"))
		fmt.Printf("g3: %#v\n", encode(dir, b, "g3:1d"))
		return
	}
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: genccitt out.pdf | genccitt -hex")
		os.Exit(2)
	}

	const w, h = 250, 120
	big := pattern(w, h)
	g4 := encode(dir, big, "g4")
	g3 := encode(dir, big, "g3:1d")
	g32d := encode(dir, big, "g3:2d")
	small := pattern(40, 20)
	g4small := encode(dir, small, "g4")

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
	img := fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /Filter /CCITTFaxDecode", w, h)
	var content bytes.Buffer
	fmt.Fprintf(&content, "q 250 0 0 120 20 300 cm /G4 Do Q\n")
	fmt.Fprintf(&content, "q 1 0 0 rg 250 0 0 120 300 300 cm /G3 Do Q\n")
	fmt.Fprintf(&content, "q 250 0 0 120 20 160 cm /Inv Do Q\n")
	fmt.Fprintf(&content, "q 250 0 0 120 300 160 cm /G32D Do Q\n")
	fmt.Fprintf(&content, "q 0 0 1 rg 80 0 0 40 20 60 cm\nBI /W 40 /H 20 /IM true /F /CCF /DP << /K -1 /Columns 40 /Rows 20 >> ID\n")
	content.Write(g4small)
	content.WriteString("\nEI Q\n")

	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 580 440] /Contents 4 0 R "+
		"/Resources << /XObject << /G4 5 0 R /G3 6 0 R /Inv 7 0 R /G32D 8 0 R >> >> >>", nil)
	obj(4, "", content.Bytes())
	obj(5, img+fmt.Sprintf(" /ColorSpace /DeviceGray /BitsPerComponent 1 /DecodeParms << /K -1 /Columns %d /Rows %d >>", w, h), g4)
	obj(6, img+fmt.Sprintf(" /ImageMask true /DecodeParms << /K 0 /Columns %d /Rows %d /EndOfLine true >>", w, h), g3)
	obj(7, img+fmt.Sprintf(" /ColorSpace /DeviceGray /BitsPerComponent 1 /Decode [1 0] /DecodeParms << /K -1 /Columns %d /Rows %d /BlackIs1 true >>", w, h), g4)
	obj(8, img+fmt.Sprintf(" /ColorSpace /DeviceGray /BitsPerComponent 1 /DecodeParms << /K 1 /Columns %d /Rows %d /EndOfLine true >>", w, h), g32d)

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 9\n0000000000 65535 f \n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 9 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	if err := os.WriteFile(flag.Arg(0), buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote", flag.Arg(0))
}
