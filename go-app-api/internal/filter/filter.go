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
	case "DCTDecode", "DCT", "JPXDecode", "CCITTFaxDecode", "CCF", "JBIG2Decode":
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
// image codec layer rather than as a byte stream.
func IsImageFilter(name string) bool {
	switch name {
	case "DCTDecode", "DCT", "JPXDecode", "CCITTFaxDecode", "CCF", "JBIG2Decode":
		return true
	}
	return false
}
