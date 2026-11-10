package jbig2

import (
	"bytes"
	"testing"
)

// TestTemplate0FastPath: the rolling-window path decodes exactly what the
// general per-pixel path does (any input bytes are a valid code stream).
func TestTemplate0FastPath(t *testing.T) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i*131) ^ byte(i>>3)
	}
	for _, tpgdon := range []bool{false, true} {
		p := genericParams{template: 0, tpgdon: tpgdon, at: nominalAT0}
		run := func(slow bool) *bitmap {
			slowGeneric = slow
			defer func() { slowGeneric = false }()
			b, err := decodeGeneric(newMQDecoder(data), make(contexts, 1<<16), 133, 61, &p)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		fast, slow := run(false), run(true)
		if !bytes.Equal(fast.pix, slow.pix) {
			t.Fatalf("tpgdon=%v: fast path differs from general path", tpgdon)
		}
		if bytes.Count(fast.pix, []byte{1}) == 0 {
			t.Fatal("degenerate test bitmap")
		}
	}
}
