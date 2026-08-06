package filter

// asciiHexDecode decodes hex pairs, ignoring whitespace, terminated by '>'.
func asciiHexDecode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)/2)
	var hi byte
	half := false
	for _, c := range data {
		if c == '>' {
			break
		}
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue // whitespace/junk
		}
		if half {
			out = append(out, hi<<4|v)
			half = false
		} else {
			hi = v
			half = true
		}
	}
	if half {
		out = append(out, hi<<4)
	}
	return out, nil
}

// ascii85Decode decodes Adobe ASCII85: 5 chars '!'..'u' → 4 bytes, 'z' → four
// zeros, terminated by "~>".
func ascii85Decode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*4/5)
	var group [5]byte
	n := 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '~':
			goto done
		case c == 'z' && n == 0:
			out = append(out, 0, 0, 0, 0)
		case c >= '!' && c <= 'u':
			group[n] = c - '!'
			n++
			if n == 5 {
				v := uint32(0)
				for _, g := range group {
					v = v*85 + uint32(g)
				}
				out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
				n = 0
			}
		default:
			// whitespace/junk ignored
		}
	}
done:
	if n > 0 {
		// partial group: pad with 'u' (84), emit n-1 bytes
		for i := n; i < 5; i++ {
			group[i] = 84
		}
		v := uint32(0)
		for _, g := range group {
			v = v*85 + uint32(g)
		}
		b := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = append(out, b[:n-1]...)
	}
	return out, nil
}

// runLengthDecode: length byte L; L<128 → copy L+1 literal bytes;
// L>128 → repeat next byte 257-L times; L==128 → EOD.
func runLengthDecode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*2)
	for i := 0; i < len(data); {
		l := data[i]
		i++
		if l == 128 {
			break
		}
		if l < 128 {
			n := int(l) + 1
			if i+n > len(data) {
				n = len(data) - i
			}
			out = append(out, data[i:i+n]...)
			i += n
		} else {
			if i >= len(data) {
				break
			}
			n := 257 - int(l)
			b := data[i]
			i++
			for k := 0; k < n; k++ {
				out = append(out, b)
			}
		}
	}
	return out, nil
}
