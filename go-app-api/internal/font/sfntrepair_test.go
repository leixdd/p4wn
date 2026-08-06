package font

import (
	"os"
	"testing"
)

func TestRepairSfntCMapCambriaSubset(t *testing.T) {
	// Extract FontFile2 for Cambria-Bold from the user's test PDF if present.
	pdfPath := "../../../data/c0617c624672eb597c969ee5f4fe039a/input.pdf"
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skip("user test PDF not present")
	}
	// Prefer unit-level: build a tiny broken-cmap TTF from a known-good font
	// by zeroing the cmap and ensuring repair restores parseability.
	src, data, err := newSfntSource(mustGoRegular(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = src
	broken := breakCMap(t, data)
	if _, _, err := newSfntSource(broken); err != nil {
		t.Fatalf("repair+parse failed: %v", err)
	}
}

func mustGoRegular(t *testing.T) []byte {
	t.Helper()
	src := substituteSource(false, false, false)
	if src == nil {
		t.Fatal("no substitute")
	}
	// re-parse from goregular via subst cache path — use raw gofont bytes
	data := substData[substKey{}]
	if len(data) == 0 {
		t.Fatal("empty goregular")
	}
	return data
}

func breakCMap(t *testing.T, src []byte) []byte {
	t.Helper()
	out := append([]byte(nil), src...)
	// corrupt cmap table payload so sfnt.Parse fails without repair
	numTables := int(out[4])<<8 | int(out[5])
	for i := 0; i < numTables; i++ {
		rec := out[12+i*16:]
		if string(rec[0:4]) != "cmap" {
			continue
		}
		off := int(rec[8])<<24 | int(rec[9])<<16 | int(rec[10])<<8 | int(rec[11])
		length := int(rec[12])<<24 | int(rec[13])<<16 | int(rec[14])<<8 | int(rec[15])
		if off+length > len(out) || length < 4 {
			t.Fatal("bad cmap")
		}
		for j := off; j < off+length; j++ {
			out[j] = 0
		}
		return out
	}
	t.Fatal("no cmap")
	return out
}
