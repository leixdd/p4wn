package filter

import "fmt"

// applyPredictor undoes the TIFF (2) or PNG (>=10) predictor on decoded data.
func applyPredictor(p Params, data []byte) ([]byte, error) {
	// hostile /DecodeParms: clamp to sane values before any arithmetic
	if p.Columns < 1 || p.Colors < 1 || p.Colors > 64 ||
		p.BitsPerComponent < 1 || p.BitsPerComponent > 32 {
		return data, nil
	}
	switch {
	case p.Predictor <= 1:
		return data, nil
	case p.Predictor == 2:
		return predictTIFF(p, data)
	case p.Predictor >= 10:
		return predictPNG(p, data)
	default:
		return nil, fmt.Errorf("filter: unsupported predictor %d", p.Predictor)
	}
}

func (p Params) stride() int {
	return (p.BitsPerComponent*p.Colors*p.Columns + 7) / 8
}

func (p Params) bpp() int {
	b := (p.BitsPerComponent*p.Colors + 7) / 8
	if b < 1 {
		b = 1
	}
	return b
}

// predictTIFF undoes horizontal differencing: each component is stored as a
// delta from the same component one pixel to the left.
func predictTIFF(p Params, data []byte) ([]byte, error) {
	if p.BitsPerComponent != 8 {
		// sub-byte TIFF prediction is vanishingly rare; pass through
		return data, nil
	}
	stride := p.stride()
	bpp := p.bpp()
	for row := 0; row+stride <= len(data); row += stride {
		line := data[row : row+stride]
		for i := bpp; i < len(line); i++ {
			line[i] += line[i-bpp]
		}
	}
	return data, nil
}

// predictPNG undoes PNG row filters. Each encoded row is one tag byte
// followed by stride data bytes.
func predictPNG(p Params, data []byte) ([]byte, error) {
	stride := p.stride()
	bpp := p.bpp()
	if stride <= 0 {
		return nil, fmt.Errorf("filter: bad predictor columns")
	}
	nrows := len(data) / (stride + 1)
	out := make([]byte, 0, nrows*stride)
	prev := make([]byte, stride) // previous decoded row, zero for first
	cur := make([]byte, stride)
	for row := 0; row < nrows; row++ {
		base := row * (stride + 1)
		tag := data[base]
		src := data[base+1 : base+1+stride]
		switch tag {
		case 0: // None
			copy(cur, src)
		case 1: // Sub
			for i := 0; i < stride; i++ {
				left := byte(0)
				if i >= bpp {
					left = cur[i-bpp]
				}
				cur[i] = src[i] + left
			}
		case 2: // Up
			for i := 0; i < stride; i++ {
				cur[i] = src[i] + prev[i]
			}
		case 3: // Average
			for i := 0; i < stride; i++ {
				left := 0
				if i >= bpp {
					left = int(cur[i-bpp])
				}
				cur[i] = src[i] + byte((left+int(prev[i]))/2)
			}
		case 4: // Paeth
			for i := 0; i < stride; i++ {
				var left, upLeft byte
				if i >= bpp {
					left = cur[i-bpp]
					upLeft = prev[i-bpp]
				}
				cur[i] = src[i] + paeth(left, prev[i], upLeft)
			}
		default:
			// unknown tag: treat as None (tolerance)
			copy(cur, src)
		}
		out = append(out, cur...)
		prev, cur = cur, prev
	}
	return out, nil
}

// paeth picks whichever of a (left), b (up), c (up-left) is closest to a+b-c.
func paeth(a, b, c byte) byte {
	pa := abs(int(b) - int(c)) // p-a = a+b-c-a = b-c
	pb := abs(int(a) - int(c))
	pc := abs(int(a) + int(b) - 2*int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
