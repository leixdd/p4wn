// Generates an image-feature test PDF.
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
)

type builder struct {
	buf     bytes.Buffer
	offsets map[int]int
}

func (b *builder) obj(num int, body string, stream []byte) {
	b.offsets[num] = b.buf.Len()
	if stream != nil {
		fmt.Fprintf(&b.buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, body, len(stream))
		b.buf.Write(stream)
		b.buf.WriteString("\nendstream\nendobj\n")
	} else {
		fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
}

func main() {
	// 1. JPEG: gradient + circle
	img := image.NewRGBA(image.Rect(0, 0, 120, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 120; x++ {
			c := color.RGBA{uint8(x * 2), uint8(y * 2), 200, 255}
			dx, dy := float64(x-60), float64(y-45)
			if math.Hypot(dx, dy) < 25 {
				c = color.RGBA{255, 220, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var jpegBuf bytes.Buffer
	jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90})

	// 2. raw RGB 4x4 flate-compressed (2x2 pixel quadrants r/g/b/w)
	raw := []byte{}
	colors := [][3]byte{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 255}}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			q := (y/2)*2 + x/2
			raw = append(raw, colors[q][0], colors[q][1], colors[q][2])
		}
	}
	var flateBuf bytes.Buffer
	zw := zlib.NewWriter(&flateBuf)
	zw.Write(raw)
	zw.Close()

	// 3. indexed 8x8 checkerboard, palette [black, orange]
	idx := make([]byte, 64)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if (x+y)%2 == 1 {
				idx[y*8+x] = 1
			}
		}
	}

	// 4. 1-bit stencil: 16x16 diagonal stripes (bit=0 paints)
	sten := make([]byte, 2*16)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if (x+y)%4 < 2 {
				sten[y*2+x/8] |= 1 << (7 - x%8) // set bit → NOT painted
			}
		}
	}

	content := `
q 160 0 0 120 20 240 cm /Jpg Do Q
q 1 0 0 1 290 250 cm 0.7071 0.7071 -0.7071 0.7071 0 0 cm 100 0 0 75 0 0 cm /Jpg Do Q
q 80 0 0 80 20 130 cm /Raw Do Q
q 80 0 0 80 120 130 cm /Idx Do Q
0.9 0.1 0.5 rg
q 80 0 0 80 220 130 cm /Sten Do Q
q 60 0 0 60 20 40 cm
BI /W 2 /H 2 /CS /RGB /BPC 8 ID ` + "\xff\x00\x00\x00\xff\x00\x00\x00\xff\xff\xff\x00" + ` EI
Q
q 30 0 0 22.5 300 40 cm /Jpg Do Q
`
	b := &builder{offsets: map[int]int{}}
	b.buf.WriteString("%PDF-1.7\n")
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 400 400] >>", nil)
	b.obj(3, `<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /XObject << /Jpg 5 0 R /Raw 6 0 R /Idx 7 0 R /Sten 8 0 R >> >> >>`, nil)
	b.obj(4, "", []byte(content))
	b.obj(5, "/Type /XObject /Subtype /Image /Width 120 /Height 90 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", jpegBuf.Bytes())
	b.obj(6, "/Type /XObject /Subtype /Image /Width 4 /Height 4 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", flateBuf.Bytes())
	b.obj(7, "/Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace [/Indexed /DeviceRGB 1 <000000FF8800>] /BitsPerComponent 8", idx)
	b.obj(8, "/Type /XObject /Subtype /Image /Width 16 /Height 16 /ImageMask true", sten)

	xref := b.buf.Len()
	fmt.Fprintf(&b.buf, "xref\n0 9\n0000000000 65535 f \n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&b.buf, "%010d 00000 n \n", b.offsets[i])
	}
	fmt.Fprintf(&b.buf, "trailer\n<< /Size 9 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)

	os.WriteFile(os.Args[1], b.buf.Bytes(), 0o644)
	fmt.Println("wrote", os.Args[1], b.buf.Len(), "bytes")
}
