package jbig2

import (
	"bytes"
	"fmt"
	"io"

	"golang.org/x/image/ccitt"
)

// bitmap holds one byte per pixel, 1 = black (JBIG2 polarity).
type bitmap struct {
	w, h int
	pix  []byte
}

func newBitmap(w, h int) (*bitmap, error) {
	if !sizeOK(int64(w), int64(h)) {
		return nil, fmt.Errorf("jbig2: bitmap %dx%d too large", w, h)
	}
	return &bitmap{w: w, h: h, pix: make([]byte, w*h)}, nil
}

func (b *bitmap) get(x, y int) int {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return 0
	}
	return int(b.pix[y*b.w+x])
}

// Combination operators (7.4.1.5 / page and region flags).
const (
	opOR = iota
	opAND
	opXOR
	opXNOR
	opREPLACE
)

// compose draws src onto b with its top-left corner at (x, y), clipped.
func (b *bitmap) compose(src *bitmap, x, y int64, op int) {
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+int64(src.w), int64(b.w)), min(y+int64(src.h), int64(b.h))
	if x0 >= x1 || y0 >= y1 {
		return // entirely outside
	}
	for dy := y0; dy < y1; dy++ {
		d := b.pix[dy*int64(b.w)+x0 : dy*int64(b.w)+x1]
		s := src.pix[(dy-y)*int64(src.w)+x0-x:]
		for i := range d {
			switch op {
			case opOR:
				d[i] |= s[i]
			case opAND:
				d[i] &= s[i]
			case opXOR:
				d[i] ^= s[i]
			case opXNOR:
				d[i] = 1 ^ d[i] ^ s[i]
			default:
				d[i] = s[i]
			}
		}
	}
}

// genericParams are the generic region decoding parameters (6.2.2).
type genericParams struct {
	mmr      bool
	template int
	tpgdon   bool
	at       [4][2]int // adaptive template pixels (x, y); template 0 uses 4
}

// contextSize is the number of GB contexts for a template.
func contextSize(template int) int {
	return [4]int{1 << 16, 1 << 13, 1 << 10, 1 << 10}[template]
}

// sltpContext is the context TPGDON's SLTP bit is coded in (6.2.5.7).
var sltpContext = [4]int{0x9B25, 0x0795, 0x00E5, 0x0195}

// decodeGeneric runs the arithmetic generic region decoding procedure
// (6.2.5) with contexts cx (shared across calls by symbol dictionaries).
func decodeGeneric(d *mqDecoder, cx contexts, w, h int, p *genericParams) (*bitmap, error) {
	b, err := newBitmap(w, h)
	if err != nil {
		return nil, err
	}
	at := p.at
	fast := p.template == 0 && at == nominalAT0 && !slowGeneric
	ltp := 0
	for y := 0; y < h; y++ {
		if p.tpgdon {
			ltp ^= d.decode(cx, sltpContext[p.template])
			if ltp == 1 {
				if y > 0 {
					copy(b.pix[y*w:(y+1)*w], b.pix[(y-1)*w:y*w])
				}
				continue
			}
		}
		row := b.pix[y*w : (y+1)*w]
		if fast {
			decodeRowT0(d, cx, b, y)
			continue
		}
		for x := 0; x < w; x++ {
			var c int
			// bit layout of Figures 3–6 (A1..A4 = AT pixels)
			switch p.template {
			case 0:
				c = b.get(x-1, y) | b.get(x-2, y)<<1 | b.get(x-3, y)<<2 | b.get(x-4, y)<<3 |
					b.get(x+at[0][0], y+at[0][1])<<4 |
					b.get(x+2, y-1)<<5 | b.get(x+1, y-1)<<6 | b.get(x, y-1)<<7 | b.get(x-1, y-1)<<8 | b.get(x-2, y-1)<<9 |
					b.get(x+at[1][0], y+at[1][1])<<10 | b.get(x+at[2][0], y+at[2][1])<<11 |
					b.get(x+1, y-2)<<12 | b.get(x, y-2)<<13 | b.get(x-1, y-2)<<14 |
					b.get(x+at[3][0], y+at[3][1])<<15
			case 1:
				c = b.get(x-1, y) | b.get(x-2, y)<<1 | b.get(x-3, y)<<2 |
					b.get(x+at[0][0], y+at[0][1])<<3 |
					b.get(x+2, y-1)<<4 | b.get(x+1, y-1)<<5 | b.get(x, y-1)<<6 | b.get(x-1, y-1)<<7 | b.get(x-2, y-1)<<8 |
					b.get(x+2, y-2)<<9 | b.get(x+1, y-2)<<10 | b.get(x, y-2)<<11 | b.get(x-1, y-2)<<12
			case 2:
				c = b.get(x-1, y) | b.get(x-2, y)<<1 |
					b.get(x+at[0][0], y+at[0][1])<<2 |
					b.get(x+1, y-1)<<3 | b.get(x, y-1)<<4 | b.get(x-1, y-1)<<5 | b.get(x-2, y-1)<<6 |
					b.get(x+1, y-2)<<7 | b.get(x, y-2)<<8 | b.get(x-1, y-2)<<9
			default:
				c = b.get(x-1, y) | b.get(x-2, y)<<1 | b.get(x-3, y)<<2 | b.get(x-4, y)<<3 |
					b.get(x+at[0][0], y+at[0][1])<<4 |
					b.get(x+1, y-1)<<5 | b.get(x, y-1)<<6 | b.get(x-1, y-1)<<7 | b.get(x-2, y-1)<<8 | b.get(x-3, y-1)<<9
			}
			row[x] = byte(d.decode(cx, c))
		}
	}
	return b, nil
}

// nominalAT0 is template 0's default AT placement (Figure 3); jbig2enc and
// most encoders use it, so it gets the rolling-window fast path.
var nominalAT0 = [4][2]int{{3, -1}, {-3, -1}, {2, -2}, {-2, -2}}

var slowGeneric bool // tests: force the general path

// decodeRowT0 decodes row y with template 0 and nominal AT pixels, keeping
// the context in three sliding windows instead of 16 lookups per pixel:
// row y-2 at x-2..x+2 (bits 15–11), row y-1 at x-3..x+3 (bits 10–4) and
// row y at x-4..x-1 (bits 3–0) — the same bit layout as the general path.
func decodeRowT0(d *mqDecoder, cx contexts, b *bitmap, y int) {
	w := b.w
	row := b.pix[y*w : (y+1)*w]
	var r1, r2 []byte
	if y >= 1 {
		r1 = b.pix[(y-1)*w : y*w]
	}
	if y >= 2 {
		r2 = b.pix[(y-2)*w : (y-1)*w]
	}
	px := func(r []byte, x int) int {
		if x < len(r) {
			return int(r[x])
		}
		return 0
	}
	w2 := px(r2, 0)<<2 | px(r2, 1)<<1 | px(r2, 2)
	w1 := px(r1, 0)<<3 | px(r1, 1)<<2 | px(r1, 2)<<1 | px(r1, 3)
	w0 := 0
	for x := range row {
		bit := d.decode(cx, w2<<11|w1<<4|w0)
		row[x] = byte(bit)
		w0 = (w0<<1 | bit) & 0xf
		w1 = (w1<<1 | px(r1, x+4)) & 0x7f
		w2 = (w2<<1 | px(r2, x+3)) & 0x1f
	}
}

// decodeMMR decodes an MMR (T.6) coded generic region.
func decodeMMR(data []byte, w, h int) (*bitmap, error) {
	b, err := newBitmap(w, h)
	if err != nil {
		return nil, err
	}
	if w == 0 || h == 0 {
		return b, nil
	}
	stride := (w + 7) / 8
	packed := make([]byte, stride*h)
	r := ccitt.NewReader(bytes.NewReader(data), ccitt.MSB, ccitt.Group4, w, h, &ccitt.Options{Invert: true})
	if _, err := io.ReadFull(r, packed); err != nil {
		return nil, fmt.Errorf("jbig2: MMR generic region: %w", err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			b.pix[y*w+x] = packed[y*stride+x/8] >> (7 - x%8) & 1
		}
	}
	return b, nil
}
