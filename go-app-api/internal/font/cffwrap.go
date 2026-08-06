package font

import (
	"encoding/binary"
)

// wrapCFF synthesizes a minimal OpenType (OTTO) container around a bare CFF
// font program so x/image/font/sfnt can parse it. Returns nil when the CFF
// is unreadable. The stub cmap is empty — callers use cffCodeToGID for
// code-based lookup.
func wrapCFF(cff []byte) []byte {
	info := scanCFF(cff)
	if info == nil {
		return nil
	}
	numGlyphs := info.numGlyphs

	be16 := func(b []byte, v uint16) { binary.BigEndian.PutUint16(b, v) }
	be32 := func(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }

	// head (54 bytes)
	head := make([]byte, 54)
	be32(head[0:], 0x00010000)  // version
	be32(head[12:], 0x5F0F3CF5) // magic
	be16(head[18:], 1000)       // unitsPerEm
	be16(head[44:], 2)          // lowestRecPPEM
	// xMin/yMin/xMax/yMax stay 0 — sfnt tolerates it

	// hhea (36 bytes)
	hhea := make([]byte, 36)
	be32(hhea[0:], 0x00010000)
	be16(hhea[4:], 800)                 // ascent
	be16(hhea[6:], uint16(0x10000-200)) // descent -200
	be16(hhea[34:], 1)                  // numberOfHMetrics

	// hmtx: one metric shared by all glyphs
	hmtx := make([]byte, 4)
	be16(hmtx[0:], 500)

	// maxp v0.5 (6 bytes)
	maxp := make([]byte, 6)
	be32(maxp[0:], 0x00005000)
	be16(maxp[4:], uint16(numGlyphs))

	// cmap: header + one empty format-4 subtable (platform 3 encoding 1)
	cmapSub := make([]byte, 24)
	be16(cmapSub[0:], 4)       // format
	be16(cmapSub[2:], 24)      // length
	be16(cmapSub[6:], 2)       // segCountX2 (one segment)
	be16(cmapSub[8:], 2)       // searchRange
	be16(cmapSub[16:], 0xFFFF) // endCode[0]
	// reservedPad, startCode 0xFFFF, idDelta 1, idRangeOffset 0
	be16(cmapSub[20:], 0xFFFF)
	be16(cmapSub[22:], 1)
	cmap := make([]byte, 12+len(cmapSub))
	be16(cmap[2:], 1)  // numTables
	be16(cmap[4:], 3)  // platform
	be16(cmap[6:], 1)  // encoding
	be32(cmap[8:], 12) // offset
	copy(cmap[12:], cmapSub)

	// name (empty) and post (v3, no names)
	name := make([]byte, 6)
	post := make([]byte, 32)
	be32(post[0:], 0x00030000)

	type table struct {
		tag  string
		data []byte
	}
	tables := []table{
		{"CFF ", cff}, {"cmap", cmap}, {"head", head}, {"hhea", hhea},
		{"hmtx", hmtx}, {"maxp", maxp}, {"name", name}, {"post", post},
	}

	n := len(tables)
	headerLen := 12 + 16*n
	total := headerLen
	for _, t := range tables {
		total += (len(t.data) + 3) &^ 3
	}
	out := make([]byte, total)
	be32(out[0:], 0x4F54544F) // 'OTTO'
	be16(out[4:], uint16(n))
	// searchRange etc. — sfnt ignores them, fill plausibly
	be16(out[6:], 128)
	be16(out[8:], 3)
	be16(out[10:], uint16(16*n-128))
	off := headerLen
	for i, t := range tables {
		rec := out[12+16*i:]
		copy(rec[0:4], t.tag)
		be32(rec[8:], uint32(off))
		be32(rec[12:], uint32(len(t.data)))
		copy(out[off:], t.data)
		off += (len(t.data) + 3) &^ 3
	}
	return out
}

// cffSource pairs the sfnt-parsed wrapped font with the bare CFF's own
// encoding and charset (the wrapper's stub cmap is empty).
type cffSource struct {
	*sfntSource
	info *cffInfo
}

func (c *cffSource) GIDForRune(r rune) uint16 {
	if c.info != nil && c.info.unicodeToGID != nil {
		if gid, ok := c.info.unicodeToGID[r]; ok {
			return gid
		}
	}
	return c.sfntSource.GIDForRune(r)
}

func (c *cffSource) GIDForCode(code byte) (uint16, bool) {
	if c.info == nil || c.info.codeToGID == nil {
		return 0, false
	}
	return c.info.codeToGID[code], true
}

// cffInfo carries what the wrapper and lookup need from a bare CFF program.
type cffInfo struct {
	numGlyphs int
	// codeToGID from the CFF Encoding (nil when the font uses the standard
	// encoding — resolved through charset names instead)
	codeToGID []uint16
	// unicodeToGID derived from charset glyph names
	unicodeToGID map[rune]uint16
}

// cffIndex locates a CFF INDEX structure; returns items and the offset just
// past it.
func cffIndex(data []byte, pos int) (items [][]byte, next int, ok bool) {
	if pos+2 > len(data) {
		return nil, 0, false
	}
	count := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if count == 0 {
		return nil, pos, true
	}
	if pos >= len(data) {
		return nil, 0, false
	}
	offSize := int(data[pos])
	pos++
	if offSize < 1 || offSize > 4 {
		return nil, 0, false
	}
	readOff := func(i int) int {
		p := pos + i*offSize
		if p+offSize > len(data) {
			return -1
		}
		v := 0
		for k := 0; k < offSize; k++ {
			v = v<<8 | int(data[p+k])
		}
		return v
	}
	dataStart := pos + (count+1)*offSize - 1
	for i := 0; i < count; i++ {
		o1, o2 := readOff(i), readOff(i+1)
		if o1 < 1 || o2 < o1 || dataStart+o2 > len(data) {
			return nil, 0, false
		}
		items = append(items, data[dataStart+o1:dataStart+o2])
	}
	end := readOff(count)
	return items, dataStart + end, true
}

// cffDict parses a CFF DICT into op → operands.
func cffDict(data []byte) map[int][]float64 {
	out := map[int][]float64{}
	var operands []float64
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b <= 21: // operator
			op := int(b)
			i++
			if b == 12 && i < len(data) {
				op = 1200 + int(data[i])
				i++
			}
			out[op] = append([]float64(nil), operands...)
			operands = operands[:0]
		case b == 28:
			if i+2 >= len(data) {
				return out
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(data[i+1:]))))
			i += 3
		case b == 29:
			if i+4 >= len(data) {
				return out
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(data[i+1:]))))
			i += 5
		case b == 30: // real number — skip nibbles
			i++
			for i < len(data) {
				lo := data[i] & 0x0F
				hi := data[i] >> 4
				i++
				if lo == 0xF || hi == 0xF {
					break
				}
			}
			operands = append(operands, 0)
		case b >= 32 && b <= 246:
			operands = append(operands, float64(int(b)-139))
			i++
		case b >= 247 && b <= 250:
			if i+1 >= len(data) {
				return out
			}
			operands = append(operands, float64((int(b)-247)*256+int(data[i+1])+108))
			i += 2
		case b >= 251 && b <= 254:
			if i+1 >= len(data) {
				return out
			}
			operands = append(operands, float64(-(int(b)-251)*256-int(data[i+1])-108))
			i += 2
		default:
			i++
		}
	}
	return out
}

// scanCFF extracts glyph count, encoding, and charset name mapping.
func scanCFF(data []byte) *cffInfo {
	if len(data) < 4 {
		return nil
	}
	hdrSize := int(data[2])
	pos := hdrSize
	// Name INDEX
	_, pos, ok := cffIndex(data, pos)
	if !ok {
		return nil
	}
	// Top DICT INDEX
	topDicts, pos, ok := cffIndex(data, pos)
	if !ok || len(topDicts) == 0 {
		return nil
	}
	top := cffDict(topDicts[0])
	// String INDEX
	strings_, _, ok := cffIndex(data, pos)
	if !ok {
		return nil
	}

	charStringsOff := dictInt(top, 17)
	if charStringsOff <= 0 || charStringsOff >= len(data) {
		return nil
	}
	charStrings, _, ok := cffIndex(data, charStringsOff)
	if !ok {
		return nil
	}
	info := &cffInfo{numGlyphs: len(charStrings)}

	// charset: gid → SID (names)
	sids := readCharset(data, dictInt(top, 15), info.numGlyphs)

	// encoding: code → gid
	encOff := dictInt(top, 16)
	switch encOff {
	case 0, 1:
		// standard/expert encoding: resolve through names below
	default:
		info.codeToGID = readCFFEncoding(data, encOff, info.numGlyphs)
	}

	// name-based unicode map from charset
	if sids != nil {
		info.unicodeToGID = map[rune]uint16{}
		for gid, sid := range sids {
			name := sidName(sid, strings_)
			if name == "" {
				continue
			}
			if r := glyphNameToUnicode(name); r != 0 {
				if _, dup := info.unicodeToGID[r]; !dup {
					info.unicodeToGID[r] = uint16(gid)
				}
			}
		}
	}
	return info
}

func dictInt(d map[int][]float64, op int) int {
	if v, ok := d[op]; ok && len(v) > 0 {
		return int(v[len(v)-1])
	}
	return 0
}

// readCharset returns gid → SID.
func readCharset(data []byte, off, numGlyphs int) []int {
	sids := make([]int, numGlyphs)
	if off == 0 { // ISOAdobe: SID = gid
		for i := range sids {
			sids[i] = i
		}
		return sids
	}
	if off <= 2 || off >= len(data) {
		return nil // expert charsets unsupported
	}
	format := data[off]
	pos := off + 1
	sids[0] = 0 // .notdef
	gid := 1
	switch format {
	case 0:
		for gid < numGlyphs && pos+1 < len(data) {
			sids[gid] = int(binary.BigEndian.Uint16(data[pos:]))
			pos += 2
			gid++
		}
	case 1, 2:
		step := 1
		if format == 2 {
			step = 2
		}
		for gid < numGlyphs && pos+1+step < len(data) {
			first := int(binary.BigEndian.Uint16(data[pos:]))
			var nLeft int
			if format == 1 {
				nLeft = int(data[pos+2])
			} else {
				nLeft = int(binary.BigEndian.Uint16(data[pos+2:]))
			}
			pos += 2 + step
			for k := 0; k <= nLeft && gid < numGlyphs; k++ {
				sids[gid] = first + k
				gid++
			}
		}
	default:
		return nil
	}
	return sids
}

// readCFFEncoding parses encoding formats 0/1 into code → gid.
func readCFFEncoding(data []byte, off, numGlyphs int) []uint16 {
	if off <= 0 || off >= len(data) {
		return nil
	}
	table := make([]uint16, 256)
	format := data[off] & 0x7F
	pos := off + 1
	switch format {
	case 0:
		if pos >= len(data) {
			return nil
		}
		nCodes := int(data[pos])
		pos++
		for i := 1; i <= nCodes && pos < len(data); i++ {
			code := data[pos]
			pos++
			if i < numGlyphs {
				table[code] = uint16(i)
			}
		}
	case 1:
		if pos >= len(data) {
			return nil
		}
		nRanges := int(data[pos])
		pos++
		gid := 1
		for r := 0; r < nRanges && pos+1 < len(data); r++ {
			first := int(data[pos])
			nLeft := int(data[pos+1])
			pos += 2
			for k := 0; k <= nLeft; k++ {
				code := first + k
				if code < 256 && gid < numGlyphs {
					table[code] = uint16(gid)
				}
				gid++
			}
		}
	default:
		return nil
	}
	return table
}

// sidName resolves a SID: 0–390 are the standard strings, the rest index
// the font's String INDEX.
func sidName(sid int, strIndex [][]byte) string {
	if sid >= 0 && sid < len(cffStandardStrings) {
		return cffStandardStrings[sid]
	}
	i := sid - len(cffStandardStrings)
	if i >= 0 && i < len(strIndex) {
		return string(strIndex[i])
	}
	return ""
}
