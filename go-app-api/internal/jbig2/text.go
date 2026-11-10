package jbig2

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Reference corners (7.4.3.1.1).
const (
	cornerBottomLeft = iota
	cornerTopLeft
	cornerBottomRight
	cornerTopRight
)

// decodeTextRegion decodes an arithmetic-coded text region without
// refinement (6.4, 7.4.3).
func decodeTextRegion(s *segment, byNum map[uint32]*segment) (*bitmap, regionInfo, error) {
	ri, data, err := parseRegionInfo(s.data)
	if err != nil {
		return nil, ri, err
	}
	if len(data) < 2 {
		return nil, ri, errors.New("jbig2: truncated text region")
	}
	flags := binary.BigEndian.Uint16(data)
	data = data[2:]
	if flags&1 != 0 {
		return nil, ri, unsupported(s.typ, "Huffman coding")
	}
	if flags&2 != 0 {
		return nil, ri, unsupported(s.typ, "refinement")
	}
	strips := int64(1) << (flags >> 2 & 3)
	corner := int(flags>>4) & 3
	transposed := flags&0x40 != 0
	combOp := int(flags>>7) & 3
	dsOffset := int64(flags>>10) & 0x1f
	if dsOffset > 15 {
		dsOffset -= 32
	}
	if len(data) < 4 {
		return nil, ri, errors.New("jbig2: truncated text region")
	}
	numInst := int64(binary.BigEndian.Uint32(data))
	data = data[4:]
	if numInst > maxInstances {
		return nil, ri, fmt.Errorf("jbig2: text region with %d symbol instances", numInst)
	}
	syms, _, err := referredSymbols(s, byNum)
	if err != nil {
		return nil, ri, err
	}
	codeLen := 0
	for 1<<codeLen < len(syms) {
		codeLen++
	}

	region, err := newBitmap(int(ri.w), int(ri.h))
	if err != nil {
		return nil, ri, err
	}
	if flags&0x200 != 0 { // SBDEFPIXEL
		for i := range region.pix {
			region.pix[i] = 1
		}
	}
	d := newMQDecoder(data)
	iadt, iafs, iads, iait := newIntDecoder(), newIntDecoder(), newIntDecoder(), newIntDecoder()
	iaid := newIDDecoder(codeLen)
	next := func(cx intDecoder) (int64, error) {
		v, ok := cx.decode(d)
		if !ok {
			return 0, errors.New("jbig2: unexpected OOB in text region")
		}
		return v, nil
	}

	stript, err := next(iadt)
	if err != nil {
		return nil, ri, err
	}
	stript *= -strips
	var firsts, n, area int64
	for n < numInst {
		dt, err := next(iadt)
		if err != nil {
			return nil, ri, err
		}
		stript += dt * strips
		dfs, err := next(iafs)
		if err != nil {
			return nil, ri, err
		}
		firsts += dfs
		curs := firsts
		for {
			var curt int64
			if strips > 1 {
				if curt, err = next(iait); err != nil {
					return nil, ri, err
				}
			}
			t := stript + curt
			id := iaid.decode(d)
			if id >= len(syms) {
				return nil, ri, fmt.Errorf("jbig2: symbol ID %d out of range (%d symbols)", id, len(syms))
			}
			sym := syms[id]
			wi, hi := int64(sym.w), int64(sym.h)
			if area += wi * hi; area > maxComposedArea {
				return nil, ri, errors.New("jbig2: text region draws too many pixels")
			}
			if !transposed && (corner == cornerTopRight || corner == cornerBottomRight) {
				curs += wi - 1
			} else if transposed && (corner == cornerBottomLeft || corner == cornerBottomRight) {
				curs += hi - 1
			}
			// the reference corner sits at (S, T), or (T, S) when transposed
			x, y := curs, t
			if transposed {
				x, y = t, curs
			}
			if corner == cornerTopRight || corner == cornerBottomRight {
				x -= wi - 1
			}
			if corner == cornerBottomLeft || corner == cornerBottomRight {
				y -= hi - 1
			}
			region.compose(sym, x, y, combOp)
			if !transposed && (corner == cornerTopLeft || corner == cornerBottomLeft) {
				curs += wi - 1
			} else if transposed && (corner == cornerTopLeft || corner == cornerTopRight) {
				curs += hi - 1
			}
			n++
			ds, ok := iads.decode(d)
			if !ok || n >= numInst {
				break // OOB ends the strip
			}
			curs += ds + dsOffset
		}
	}
	return region, ri, nil
}
