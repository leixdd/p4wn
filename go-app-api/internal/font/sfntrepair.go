package font

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

type sfntTable struct {
	tag  [4]byte
	data []byte
}

// repairSfntCMap replaces a broken or incomplete cmap table with a minimal
// valid stub. PDF TrueType subsets often ship invalid cmaps (glyphs are
// addressed by GID via CIDToGIDMap / Encoding instead). golang.org/x/image/sfnt
// rejects those fonts entirely; a stub cmap lets glyf/CFF outlines load by GID.
func repairSfntCMap(src []byte) ([]byte, error) {
	if len(src) < 12 {
		return nil, errors.New("font: sfnt too short")
	}
	scaler := binary.BigEndian.Uint32(src[0:4])
	// TrueType (0x00010000), OpenType with CFF ('OTTO'), TrueType Apple ('true')
	switch scaler {
	case 0x00010000, 0x4F54544F, 0x74727565:
	default:
		return nil, fmt.Errorf("font: unsupported sfnt scaler %08x", scaler)
	}
	numTables := int(binary.BigEndian.Uint16(src[4:6]))
	if numTables <= 0 || numTables > 256 || 12+numTables*16 > len(src) {
		return nil, errors.New("font: bad sfnt table count")
	}

	tables := make([]sfntTable, 0, numTables+2)
	haveCmap, havePost := false, false
	for i := 0; i < numTables; i++ {
		rec := src[12+i*16:]
		var tag [4]byte
		copy(tag[:], rec[0:4])
		off := binary.BigEndian.Uint32(rec[8:12])
		length := binary.BigEndian.Uint32(rec[12:16])
		if int(off) < 0 || int(off)+int(length) > len(src) {
			return nil, fmt.Errorf("font: table %s out of range", tag)
		}
		data := make([]byte, length)
		copy(data, src[off:off+length])
		switch string(tag[:]) {
		case "cmap":
			haveCmap = true
			data = stubCMap()
		case "post":
			havePost = true
			// PDF subsets often truncate format-2.0 post tables; v3 is enough
			// for outline loading by GID.
			data = stubPost()
		}
		tables = append(tables, sfntTable{tag: tag, data: data})
	}
	if !haveCmap {
		tables = append(tables, sfntTable{tag: [4]byte{'c', 'm', 'a', 'p'}, data: stubCMap()})
	}
	if !havePost {
		tables = append(tables, sfntTable{tag: [4]byte{'p', 'o', 's', 't'}, data: stubPost()})
	}
	sort.Slice(tables, func(i, j int) bool {
		return string(tables[i].tag[:]) < string(tables[j].tag[:])
	})
	return writeSfnt(scaler, tables), nil
}

func stubCMap() []byte {
	// format-4 subtable with a single 0xFFFF sentinel segment (no mapped glyphs)
	cmapSub := make([]byte, 24)
	binary.BigEndian.PutUint16(cmapSub[0:], 4)       // format
	binary.BigEndian.PutUint16(cmapSub[2:], 24)      // length
	binary.BigEndian.PutUint16(cmapSub[6:], 2)       // segCountX2
	binary.BigEndian.PutUint16(cmapSub[8:], 2)       // searchRange
	binary.BigEndian.PutUint16(cmapSub[16:], 0xFFFF) // endCode
	binary.BigEndian.PutUint16(cmapSub[20:], 0xFFFF) // startCode
	binary.BigEndian.PutUint16(cmapSub[22:], 1)      // idDelta
	cmap := make([]byte, 12+len(cmapSub))
	binary.BigEndian.PutUint16(cmap[2:], 1)  // numTables
	binary.BigEndian.PutUint16(cmap[4:], 3)  // platform Microsoft
	binary.BigEndian.PutUint16(cmap[6:], 1)  // encoding Unicode BMP
	binary.BigEndian.PutUint32(cmap[8:], 12) // offset
	copy(cmap[12:], cmapSub)
	return cmap
}

func stubPost() []byte {
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post[0:], 0x00030000) // version 3.0 — no glyph names
	return post
}

func writeSfnt(scaler uint32, tables []sfntTable) []byte {
	n := len(tables)
	headerLen := 12 + 16*n
	total := headerLen
	for _, t := range tables {
		total += (len(t.data) + 3) &^ 3
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:], scaler)
	binary.BigEndian.PutUint16(out[4:], uint16(n))
	sr := uint16(16)
	e := uint16(0)
	for (1 << (e + 1)) <= n {
		e++
		sr = uint16(1<<e) * 16
	}
	binary.BigEndian.PutUint16(out[6:], sr)
	binary.BigEndian.PutUint16(out[8:], e)
	binary.BigEndian.PutUint16(out[10:], uint16(n*16)-sr)

	off := headerLen
	for i, t := range tables {
		rec := out[12+16*i:]
		copy(rec[0:4], t.tag[:])
		binary.BigEndian.PutUint32(rec[4:], sfntChecksum(t.data))
		binary.BigEndian.PutUint32(rec[8:], uint32(off))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(t.data)))
		copy(out[off:], t.data)
		off += (len(t.data) + 3) &^ 3
	}
	return out
}

func sfntChecksum(data []byte) uint32 {
	var sum uint32
	padded := data
	if len(data)%4 != 0 {
		padded = make([]byte, (len(data)+3)&^3)
		copy(padded, data)
	}
	for i := 0; i+3 < len(padded); i += 4 {
		sum += binary.BigEndian.Uint32(padded[i:])
	}
	return sum
}
