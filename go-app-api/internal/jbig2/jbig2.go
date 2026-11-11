// Package jbig2 decodes JBIG2 streams embedded in PDF (T.88 sequential
// organization, no file header), as produced by scanners and jbig2enc.
//
// Implemented segment types: symbol dictionary (0) and immediate text
// regions (6, 7) with arithmetic coding and no refinement, immediate generic
// regions (38, 39; arithmetic templates 0–3 with AT pixels and TPGDON, or
// MMR), page information (48), end of page/stripe/file (49–51); profiles
// (52), tables (53) and extensions (62) are skipped. Everything else —
// Huffman-coded symbol dictionaries and text regions, refinement and
// aggregation, pattern dictionaries, halftone regions, generic refinement
// regions, intermediate regions — fails the image with an error naming it.
package jbig2

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// maxPixels bounds any bitmap (page, region, symbol); it matches the
// image loader's 64M pixel cap.
const maxPixels = 64 << 20

type segment struct {
	num  uint32
	typ  int
	page uint32
	refs []uint32
	data []byte

	symbols []*bitmap // symbol dictionary exports
	gbcx    contexts  // retained generic contexts (symbol dictionary)
	gbp     genericParams
}

var segmentNames = map[int]string{
	0: "symbol dictionary", 4: "intermediate text region", 6: "text region", 7: "text region",
	16: "pattern dictionary", 20: "intermediate halftone region", 22: "halftone region",
	23: "halftone region", 36: "intermediate generic region", 38: "generic region",
	39: "generic region", 40: "intermediate generic refinement region",
	42: "generic refinement region", 43: "generic refinement region", 48: "page information",
	49: "end of page", 50: "end of stripe", 51: "end of file", 52: "profiles", 53: "tables",
	62: "extension",
}

func unsupported(typ int, what string) error {
	name := segmentNames[typ]
	if name == "" {
		name = "unknown"
	}
	if what != "" {
		what = " (" + what + ")"
	}
	return fmt.Errorf("jbig2: unsupported %s segment type %d%s", name, typ, what)
}

// parseSegments splits a sequential-organization stream into segments.
func parseSegments(data []byte) ([]*segment, error) {
	var segs []*segment
	p := 0
	need := func(n int) error {
		if n < 0 || p+n > len(data) {
			return errors.New("jbig2: truncated segment header")
		}
		return nil
	}
	for p < len(data) {
		if err := need(6); err != nil {
			return nil, err
		}
		s := &segment{num: binary.BigEndian.Uint32(data[p:])}
		flags := data[p+4]
		s.typ = int(flags & 0x3f)
		p += 5
		count := int(data[p] >> 5)
		if count == 7 { // long form: 29-bit count, then retain bits
			if err := need(4); err != nil {
				return nil, err
			}
			c := int(binary.BigEndian.Uint32(data[p:]) & 0x1fffffff)
			p += 4
			if err := need((c + 8) / 8); err != nil {
				return nil, err
			}
			p += (c + 8) / 8
			count = c
		} else if count > 4 {
			return nil, fmt.Errorf("jbig2: bad referred-to segment count %d", count)
		} else {
			p++
		}
		refSize := 4
		if s.num <= 256 {
			refSize = 1
		} else if s.num <= 65536 {
			refSize = 2
		}
		if err := need(count * refSize); err != nil {
			return nil, err
		}
		for i := 0; i < count; i++ {
			var r uint32
			for k := 0; k < refSize; k++ {
				r = r<<8 | uint32(data[p+k])
			}
			s.refs = append(s.refs, r)
			p += refSize
		}
		pageSize := 1
		if flags&0x40 != 0 {
			pageSize = 4
		}
		if err := need(pageSize + 4); err != nil {
			return nil, err
		}
		for k := 0; k < pageSize; k++ {
			s.page = s.page<<8 | uint32(data[p+k])
		}
		p += pageSize
		length := binary.BigEndian.Uint32(data[p:])
		p += 4
		n := int(length)
		if length == 0xffffffff {
			var err error
			if n, err = unknownLength(s.typ, data[p:]); err != nil {
				return nil, err
			}
		}
		if err := need(n); err != nil {
			return nil, fmt.Errorf("jbig2: segment %d data truncated", s.num)
		}
		s.data = data[p : p+n]
		p += n
		segs = append(segs, s)
		if s.typ == 51 { // end of file
			break
		}
	}
	return segs, nil
}

// unknownLength finds the end of an immediate generic region whose length
// is 0xffffffff (7.2.7): the end marker (FF AC, or 00 00 for MMR) followed
// by the 4-byte row count.
func unknownLength(typ int, data []byte) (int, error) {
	if (typ != 38 && typ != 39) || len(data) < 18 {
		return 0, errors.New("jbig2: unknown segment length outside a generic region")
	}
	marker := []byte{0xff, 0xac}
	if data[17]&1 != 0 {
		marker = []byte{0, 0}
	}
	for i := 18; i+6 <= len(data); i++ {
		if data[i] == marker[0] && data[i+1] == marker[1] &&
			string(data[i+2:i+6]) == string(data[4:8]) { // row count == region height
			return i + 6, nil
		}
	}
	return 0, errors.New("jbig2: end of generic region of unknown length not found")
}

// sizeOK bounds each side before multiplying, so the product cannot overflow.
func sizeOK(w, h int64) bool {
	return w >= 0 && h >= 0 && w <= maxPixels && h <= maxPixels && w*h <= maxPixels
}

type regionInfo struct {
	w, h, x, y int64
	op         int
}

func parseRegionInfo(data []byte) (regionInfo, []byte, error) {
	if len(data) < 17 {
		return regionInfo{}, nil, errors.New("jbig2: truncated region segment information")
	}
	be := binary.BigEndian
	r := regionInfo{
		w: int64(be.Uint32(data)), h: int64(be.Uint32(data[4:])),
		x: int64(be.Uint32(data[8:])), y: int64(be.Uint32(data[12:])),
		op: int(data[16] & 7),
	}
	if !sizeOK(r.w, r.h) {
		return r, nil, fmt.Errorf("jbig2: region %dx%d too large", r.w, r.h)
	}
	return r, data[17:], nil
}

// Decode decodes the page in a PDF JBIG2Decode stream (with the optional
// JBIG2Globals stream) to packed rows, MSB first, each row byte-aligned,
// 1 = black.
func Decode(globals, data []byte) (out []byte, w, h int, err error) {
	defer func() {
		// a decoder bug on hostile input must fail the image, not the page
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("jbig2: corrupt stream (%v)", r)
		}
	}()
	return decode(globals, data)
}

func decode(globals, data []byte) (out []byte, w, h int, err error) {
	var segs []*segment
	if len(globals) > 0 {
		g, err := parseSegments(globals)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%w (globals)", err)
		}
		segs = g
	}
	s, err := parseSegments(data)
	if err != nil {
		return nil, 0, 0, err
	}
	segs = append(segs, s...)
	pg, err := decodePage(segs)
	if err != nil {
		return nil, 0, 0, err
	}
	stride := (pg.w + 7) / 8
	out = make([]byte, stride*pg.h)
	for y := 0; y < pg.h; y++ {
		row := pg.pix[y*pg.w : (y+1)*pg.w]
		for x, v := range row {
			out[y*stride+x/8] |= v << (7 - x%8)
		}
	}
	return out, pg.w, pg.h, nil
}

type page struct {
	*bitmap
	striped bool // height unknown: grows with stripes / regions
	defPix  byte
}

func decodePage(segs []*segment) (*bitmap, error) {
	byNum := map[uint32]*segment{}
	budget := int64(workBudget)
	var pg *page
	var pageNum uint32
	for _, s := range segs {
		if s.page != 0 && pg != nil && s.page != pageNum {
			continue // a second page in one PDF stream: ignored
		}
		byNum[s.num] = s
		switch s.typ {
		case 48:
			if pg != nil {
				continue
			}
			var err error
			if pg, err = newPage(s.data); err != nil {
				return nil, err
			}
			pageNum = s.page
		case 0:
			if err := decodeSymbolDict(s, byNum, &budget); err != nil {
				return nil, err
			}
		case 6, 7, 38, 39:
			if pg == nil {
				return nil, errors.New("jbig2: region before page information")
			}
			var bm *bitmap
			var ri regionInfo
			var err error
			if s.typ <= 7 {
				bm, ri, err = decodeTextRegion(s, byNum)
			} else {
				bm, ri, err = decodeGenericRegion(s, &budget)
			}
			if err != nil {
				return nil, err
			}
			if err := pg.grow(ri.y + ri.h); err != nil {
				return nil, err
			}
			pg.compose(bm, ri.x, ri.y, ri.op)
		case 50: // end of stripe
			if pg != nil && len(s.data) >= 4 {
				if err := pg.grow(int64(binary.BigEndian.Uint32(s.data)) + 1); err != nil {
					return nil, err
				}
			}
		case 49, 51:
			if pg == nil {
				return nil, errors.New("jbig2: no page information segment")
			}
			return pg.bitmap, nil
		case 52, 53, 62:
			// profiles, tables (only used by Huffman coding, which fails
			// where it is selected), extensions (comments): nothing to draw
		default:
			return nil, unsupported(s.typ, "")
		}
	}
	if pg == nil {
		return nil, errors.New("jbig2: no page information segment")
	}
	return pg.bitmap, nil
}

func newPage(data []byte) (*page, error) {
	if len(data) < 19 {
		return nil, errors.New("jbig2: truncated page information")
	}
	w := int64(binary.BigEndian.Uint32(data))
	h := int64(binary.BigEndian.Uint32(data[4:]))
	pg := &page{defPix: data[16] >> 2 & 1}
	if h == 0xffffffff {
		pg.striped, h = true, 0
	}
	if w <= 0 || w*h > maxPixels || w > maxPixels {
		return nil, fmt.Errorf("jbig2: page %dx%d too large", w, h)
	}
	bm, err := newBitmap(int(w), int(h))
	if err != nil {
		return nil, err
	}
	if pg.defPix == 1 {
		for i := range bm.pix {
			bm.pix[i] = 1
		}
	}
	pg.bitmap = bm
	return pg, nil
}

// grow extends a striped page to at least rows rows.
func (pg *page) grow(rows int64) error {
	if !pg.striped || rows <= int64(pg.h) {
		return nil
	}
	if rows*int64(pg.w) > maxPixels {
		return fmt.Errorf("jbig2: page %dx%d too large", pg.w, rows)
	}
	add := make([]byte, (int(rows)-pg.h)*pg.w)
	if pg.defPix == 1 {
		for i := range add {
			add[i] = 1
		}
	}
	pg.pix = append(pg.pix, add...)
	pg.h = int(rows)
	return nil
}

// decodeGenericRegion handles immediate generic regions (7.4.6).
func decodeGenericRegion(s *segment, budget *int64) (*bitmap, regionInfo, error) {
	ri, data, err := parseRegionInfo(s.data)
	if err != nil {
		return nil, ri, err
	}
	if err := spend(budget, ri.w*ri.h); err != nil {
		return nil, ri, err
	}
	if len(data) < 1 {
		return nil, ri, errors.New("jbig2: truncated generic region")
	}
	flags := data[0]
	p := genericParams{mmr: flags&1 != 0, template: int(flags>>1) & 3, tpgdon: flags&8 != 0}
	if flags&0x10 != 0 {
		return nil, ri, unsupported(s.typ, "extended template")
	}
	data = data[1:]
	if p.mmr {
		bm, err := decodeMMR(data, int(ri.w), int(ri.h))
		return bm, ri, err
	}
	if data, err = readAT(data, &p, p.template == 0); err != nil {
		return nil, ri, err
	}
	bm, err := decodeGeneric(newMQDecoder(data), make(contexts, contextSize(p.template)), int(ri.w), int(ri.h), &p)
	return bm, ri, err
}

// readAT reads 4 (template 0) or 1 adaptive template pixel pairs.
func readAT(data []byte, p *genericParams, four bool) ([]byte, error) {
	n := 1
	if four {
		n = 4
	}
	if len(data) < 2*n {
		return nil, errors.New("jbig2: truncated AT pixels")
	}
	for i := 0; i < n; i++ {
		p.at[i] = [2]int{int(int8(data[2*i])), int(int8(data[2*i+1]))}
	}
	return data[2*n:], nil
}
