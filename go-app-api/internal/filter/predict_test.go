package filter

import (
	"bytes"
	"testing"
)

// encodePNGRow applies a PNG filter for test purposes (the inverse of what
// predictPNG undoes).
func encodePNGRow(tag byte, cur, prev []byte, bpp int) []byte {
	out := make([]byte, len(cur))
	for i := range cur {
		var left, up, upLeft byte
		if i >= bpp {
			left = cur[i-bpp]
			upLeft = prev[i-bpp]
		}
		up = prev[i]
		switch tag {
		case 0:
			out[i] = cur[i]
		case 1:
			out[i] = cur[i] - left
		case 2:
			out[i] = cur[i] - up
		case 3:
			out[i] = cur[i] - byte((int(left)+int(up))/2)
		case 4:
			out[i] = cur[i] - paeth(left, up, upLeft)
		}
	}
	return out
}

func TestPNGPredictorRoundTrip(t *testing.T) {
	// 3 columns × 2 colors × 8 bits = 6-byte rows
	p := Params{Predictor: 12, Columns: 3, Colors: 2, BitsPerComponent: 8}
	rows := [][]byte{
		{10, 20, 30, 40, 50, 60},
		{11, 22, 33, 44, 55, 66},
		{200, 100, 50, 25, 12, 6},
		{0, 0, 255, 255, 128, 128},
	}
	for tag := byte(0); tag <= 4; tag++ {
		var enc []byte
		prev := make([]byte, 6)
		for _, row := range rows {
			enc = append(enc, tag)
			enc = append(enc, encodePNGRow(tag, row, prev, p.bpp())...)
			prev = row
		}
		got, err := applyPredictor(p, enc)
		if err != nil {
			t.Fatalf("tag %d: %v", tag, err)
		}
		want := bytes.Join(rows, nil)
		if !bytes.Equal(got, want) {
			t.Errorf("tag %d: got % x, want % x", tag, got, want)
		}
	}
}

func TestTIFFPredictor(t *testing.T) {
	// one row, 4 columns, 1 color, 8bpc: stored deltas
	p := Params{Predictor: 2, Columns: 4, Colors: 1, BitsPerComponent: 8}
	enc := []byte{10, 5, 5, 236} // 10, 15, 20, 0 (wraps)
	got, err := applyPredictor(p, enc)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 15, 20, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestASCIIHex(t *testing.T) {
	got, _ := asciiHexDecode([]byte("48 65 6C 6C 6F>"))
	if string(got) != "Hello" {
		t.Errorf("got %q", got)
	}
}

func TestASCII85(t *testing.T) {
	// "Man " encodes to "9jqo^" in ascii85
	got, _ := ascii85Decode([]byte("9jqo^~>"))
	if string(got) != "Man " {
		t.Errorf("got %q", got)
	}
}

func TestRunLength(t *testing.T) {
	// 2 literals "AB", then 'C' × 3, then EOD
	got, _ := runLengthDecode([]byte{1, 'A', 'B', 254, 'C', 128})
	if string(got) != "ABCCC" {
		t.Errorf("got %q", got)
	}
}
