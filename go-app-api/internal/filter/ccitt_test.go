package filter

import (
	"bytes"
	"strings"
	"testing"
)

// Streams produced by libtiff (testtools/genccitt -hex) from a 20x6 bitmap
// where pixel (x,y) is black iff (x/3+y/2)%2 == 0 || x == y*3.
var (
	ccittG4 = []byte{0x26, 0xb1, 0x88, 0xc4, 0x16, 0xff, 0x6, 0x21, 0x2, 0x4, 0x8, 0x2d,
		0x7c, 0x10, 0x20, 0x40, 0x81, 0x2, 0xb, 0xf7, 0x80, 0x8, 0x0, 0x80}
	ccittG3 = []byte{0x0, 0x13, 0x5a, 0x28, 0xa3, 0x0, 0x13, 0x56, 0xf4, 0x51, 0x80, 0xc,
		0x37, 0xa2, 0x70, 0x1, 0x8a, 0x28, 0x9c, 0x0, 0x4d, 0x68, 0xa2, 0x8c, 0x0, 0x4d,
		0x68, 0xa1, 0xbe}
)

const ccittW, ccittH = 20, 6

func ccittBlack(x, y int) bool { return (x/3+y/2)%2 == 0 || x == y*3 }

// checkBits compares packed 1-bpc rows against the pattern; blackBit is the
// sample value expected for black pixels (row padding bits are ignored).
func checkBits(t *testing.T, got []byte, rows int, blackBit byte) {
	t.Helper()
	stride := (ccittW + 7) / 8
	if len(got) != stride*rows {
		t.Fatalf("got %d bytes, want %d", len(got), stride*rows)
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < ccittW; x++ {
			bit := got[y*stride+x/8] >> (7 - x%8) & 1
			want := 1 - blackBit
			if ccittBlack(x, y) {
				want = blackBit
			}
			if bit != want {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, bit, want)
			}
		}
	}
}

func ccittParams(k, rows int) Params {
	p := DefaultParams()
	p.K, p.Rows, p.Columns, p.ColumnsSet = k, rows, ccittW, true
	return p
}

func TestCCITTGroup4(t *testing.T) {
	for _, rows := range []int{ccittH, 0} { // known height, and decode-to-EOFB
		out, err := Decode("CCITTFaxDecode", ccittParams(-1, rows), ccittG4)
		if err != nil {
			t.Fatalf("rows=%d: %v", rows, err)
		}
		checkBits(t, out, ccittH, 0)
	}
}

func TestCCITTGroup3(t *testing.T) {
	out, err := Decode("CCF", ccittParams(0, ccittH), ccittG3)
	if err != nil {
		t.Fatal(err)
	}
	checkBits(t, out, ccittH, 0)
}

func TestCCITTBlackIs1(t *testing.T) {
	p := ccittParams(-1, ccittH)
	p.BlackIs1 = true
	out, err := Decode("CCITTFaxDecode", p, ccittG4)
	if err != nil {
		t.Fatal(err)
	}
	checkBits(t, out, ccittH, 1)
}

func TestCCITTTruncatedKeepsRows(t *testing.T) {
	out, err := Decode("CCITTFaxDecode", ccittParams(-1, ccittH), ccittG4[:9])
	if err != nil {
		t.Fatal(err)
	}
	stride := (ccittW + 7) / 8
	if len(out) == 0 || len(out)%stride != 0 || len(out) >= stride*ccittH {
		t.Fatalf("truncated stream gave %d bytes", len(out))
	}
	checkBits(t, out, len(out)/stride, 0)
}

func TestCCITTUnsupported(t *testing.T) {
	_, err := Decode("CCITTFaxDecode", ccittParams(1, ccittH), ccittG3)
	if err == nil || !strings.Contains(err.Error(), "Group 3 2D") {
		t.Fatalf("K>0: err = %v", err)
	}
	// Group 3 data without the leading EOL the library requires
	_, err = Decode("CCITTFaxDecode", ccittParams(0, ccittH), ccittG4)
	if err == nil || !strings.Contains(err.Error(), "EOL") {
		t.Fatalf("G3 without EOL: err = %v", err)
	}
}

// A few bytes of all-V0 codes would expand to whole rows each; the pixel cap
// must stop it instead of allocating.
func TestCCITTBomb(t *testing.T) {
	p := DefaultParams()
	p.K, p.Columns, p.ColumnsSet = -1, 1<<20, true
	_, err := Decode("CCITTFaxDecode", p, bytes.Repeat([]byte{0xFF}, 1<<16))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
	p.Rows = 1 << 20
	if _, err := Decode("CCITTFaxDecode", p, []byte{0xFF}); err == nil {
		t.Fatal("oversized Rows accepted")
	}
}
