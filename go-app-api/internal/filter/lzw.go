package filter

// lzwDecode implements the PDF/TIFF variant of LZW: MSB-first variable-width
// codes (9–12 bits), code 256 = clear table, 257 = EOD. With earlyChange the
// code width bumps one entry before the table would overflow (PDF default).
//
// Note: compress/lzw implements the GIF variant (LSB packing, no early
// change), so this is written out by hand.
func lzwDecode(data []byte, earlyChange bool) ([]byte, error) {
	const (
		clearCode = 256
		eodCode   = 257
		firstCode = 258
		maxCode   = 4096
	)
	type entry struct {
		prev  int // previous entry index, -1 for roots
		value byte
		len   int
	}
	table := make([]entry, firstCode, maxCode)
	for i := 0; i < 256; i++ {
		table[i] = entry{prev: -1, value: byte(i), len: 1}
	}
	resetWidth := func() int { return 9 }

	out := []byte{}
	expand := func(code int) []byte { // walk prev-chain, reverse
		e := table[code]
		buf := make([]byte, e.len)
		for i := e.len - 1; i >= 0; i-- {
			buf[i] = table[code].value
			code = table[code].prev
		}
		return buf
	}

	bitPos := 0
	width := resetWidth()
	readCode := func() int {
		if (bitPos+width+7)/8 > len(data) {
			return eodCode
		}
		v := 0
		for i := 0; i < width; i++ {
			byteIdx := (bitPos + i) / 8
			bitIdx := 7 - (bitPos+i)%8
			v = v<<1 | int(data[byteIdx]>>bitIdx&1)
		}
		bitPos += width
		return v
	}

	prev := -1
	for {
		code := readCode()
		if code == eodCode {
			break
		}
		if code == clearCode {
			table = table[:firstCode]
			width = resetWidth()
			prev = -1
			continue
		}
		if code > len(table) || (prev == -1 && code >= len(table)) {
			break // corrupt stream: stop with what we have
		}
		var seq []byte
		if code < len(table) {
			seq = expand(code)
		} else {
			// KwKwK case: code == len(table)
			s := expand(prev)
			seq = append(s, s[0])
		}
		out = append(out, seq...)
		if prev != -1 && len(table) < maxCode {
			table = append(table, entry{prev: prev, value: seq[0], len: table[prev].len + 1})
		}
		prev = code
		// width bump
		limit := len(table)
		if earlyChange {
			limit++
		}
		switch {
		case limit >= 4096:
			width = 12
		case limit >= 2048:
			width = 12
		case limit >= 1024:
			width = 11
		case limit >= 512:
			width = 10
		}
	}
	return out, nil
}
