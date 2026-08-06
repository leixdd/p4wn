package filter

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"io"
)

// flateDecode inflates zlib data, retrying as raw deflate for files with a
// missing/corrupt zlib header. Truncated input returns what decoded plus err.
func flateDecode(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		out, rerr := io.ReadAll(zr)
		zr.Close()
		if rerr == nil || len(out) > 0 {
			return out, rerr
		}
	}
	// raw deflate fallback (some writers omit the zlib wrapper)
	fr := flate.NewReader(bytes.NewReader(data))
	out, rerr := io.ReadAll(fr)
	fr.Close()
	return out, rerr
}
