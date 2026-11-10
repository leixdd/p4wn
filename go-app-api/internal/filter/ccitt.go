package filter

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"golang.org/x/image/ccitt"
)

// MaxImagePixels caps the pixel count a bilevel codec (CCITT, JBIG2) may
// produce, matching the image loader's cap (64M px ≈ A3 @ 580 dpi). Both
// codecs can expand a few input bits into a whole row, so the cap is what
// stops a tiny stream from allocating gigabytes.
const MaxImagePixels = 64 << 20

// ccittDecode decodes CCITTFaxDecode data into packed 1-bpc rows (MSB first,
// each row byte-aligned) in PDF sample polarity: 0 = black unless BlackIs1.
//
// golang.org/x/image/ccitt supports Group 4 (K<0) and Group 3 1D (K=0) only
// when every row is preceded by an EOL code (PDF EndOfLine true, no fill bits
// before the EOL). Group 3 2D (K>0) is not supported by the library.
func ccittDecode(p Params, data []byte) ([]byte, error) {
	cols := p.Columns
	if !p.ColumnsSet {
		cols = 1728
	}
	if cols < 1 || cols > MaxImagePixels {
		return nil, fmt.Errorf("ccitt: bad Columns %d", cols)
	}
	var sf ccitt.SubFormat
	switch {
	case p.K < 0:
		sf = ccitt.Group4
	case p.K == 0:
		sf = ccitt.Group3
	default:
		return nil, errors.New("ccitt: Group 3 2D (K>0) is not supported")
	}
	rowBytes := (cols + 7) / 8
	maxRows := MaxImagePixels / cols
	rows := ccitt.AutoDetectHeight
	if p.Rows > 0 {
		if p.Rows > maxRows {
			return nil, fmt.Errorf("ccitt: image %dx%d too large", cols, p.Rows)
		}
		rows = p.Rows
	}
	// BlackIs1 false (the default) is the library's native polarity.
	opts := &ccitt.Options{Align: p.EncodedByteAlign, Invert: p.BlackIs1}
	r := ccitt.NewReader(bytes.NewReader(data), ccitt.MSB, sf, cols, rows, opts)
	limit := int64(maxRows) * int64(rowBytes)
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("ccitt: output exceeds %d pixels", MaxImagePixels)
	}
	out = out[:len(out)/rowBytes*rowBytes] // whole rows only
	if err != nil {
		if len(out) == 0 {
			if sf == ccitt.Group3 {
				return nil, fmt.Errorf("ccitt: Group 3 1D needs an EOL before every row: %w", err)
			}
			return nil, fmt.Errorf("ccitt: %w", err)
		}
		// damaged tail: keep the rows that decoded, like truncated Flate
		return out, nil
	}
	return out, nil
}
