// Package filter implements PDF stream decode filters.
package filter

import (
	"fmt"
)

// Params carries the /DecodeParms values a filter may need.
type Params struct {
	Predictor        int // 1 = none, 2 = TIFF, >= 10 = PNG
	Columns          int
	Colors           int
	BitsPerComponent int
	EarlyChange      int // LZW; default 1

	// CCITTFaxDecode. Columns above is also the fax width; the PDF default
	// for CCITT is 1728, used when ColumnsSet is false.
	ColumnsSet       bool
	K                int // <0 Group 4, 0 Group 3 1D, >0 Group 3 2D
	Rows             int // 0 = unknown (decode until end of data)
	BlackIs1         bool
	EncodedByteAlign bool

	JBIG2Globals []byte // decoded /JBIG2Globals stream, if any
}

func DefaultParams() Params {
	return Params{Predictor: 1, Columns: 1, Colors: 1, BitsPerComponent: 8, EarlyChange: 1}
}

// Decode applies the named filter to data.
func Decode(name string, p Params, data []byte) ([]byte, error) {
	switch name {
	case "FlateDecode", "Fl":
		out, err := flateDecode(data)
		if err != nil && len(out) == 0 {
			return nil, err
		}
		// tolerate truncated streams: use what inflated
		return applyPredictor(p, out)
	case "LZWDecode", "LZW":
		out, err := lzwDecode(data, p.EarlyChange != 0)
		if err != nil && len(out) == 0 {
			return nil, err
		}
		return applyPredictor(p, out)
	case "ASCIIHexDecode", "AHx":
		return asciiHexDecode(data)
	case "ASCII85Decode", "A85":
		return ascii85Decode(data)
	case "RunLengthDecode", "RL":
		return runLengthDecode(data)
	case "CCITTFaxDecode", "CCF":
		return ccittDecode(p, data)
	case "JBIG2Decode":
		return jbig2Decode(p, data)
	case "DCTDecode", "DCT", "JPXDecode":
		// image-only filters: decoded by the image loader, not here.
		// Return the data untouched so the image layer can handle it.
		return data, nil
	case "Crypt":
		return data, nil // Identity crypt filter
	default:
		return nil, fmt.Errorf("filter: unknown filter %q", name)
	}
}

// IsImageFilter reports whether the named filter must be decoded by the
// image codec layer rather than as a byte stream. CCITTFaxDecode and
// JBIG2Decode are not: they decode here to packed 1-bpc rows that the
// normal sample path reads.
func IsImageFilter(name string) bool {
	switch name {
	case "DCTDecode", "DCT", "JPXDecode":
		return true
	}
	return false
}
