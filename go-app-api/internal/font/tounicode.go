package font

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// parseToUnicode builds a character-code → Unicode string map from a PDF
// ToUnicode CMap stream. Supports codespacerange, bfchar, and bfrange.
func parseToUnicode(data []byte) map[uint32]string {
	out := map[uint32]string{}
	toks := tokenizeCMap(data)
	i := 0
	for i < len(toks) {
		t := toks[i]
		i++
		switch t {
		case "beginbfchar":
			for i+1 < len(toks) && toks[i] != "endbfchar" {
				src := toks[i]
				dst := toks[i+1]
				i += 2
				code, ok := parseHexCode(src)
				if !ok {
					continue
				}
				if s, ok := parseHexUTF16(dst); ok {
					out[code] = s
				}
			}
			if i < len(toks) && toks[i] == "endbfchar" {
				i++
			}
		case "beginbfrange":
			for i < len(toks) && toks[i] != "endbfrange" {
				if i+2 >= len(toks) {
					break
				}
				loTok, hiTok, third := toks[i], toks[i+1], toks[i+2]
				lo, ok1 := parseHexCode(loTok)
				hi, ok2 := parseHexCode(hiTok)
				if !ok1 || !ok2 || lo > hi || hi-lo > 65535 {
					i++
					continue
				}
				if third == "[" {
					i += 3
					code := lo
					for i < len(toks) && toks[i] != "]" {
						if s, ok := parseHexUTF16(toks[i]); ok {
							out[code] = s
						}
						code++
						i++
					}
					if i < len(toks) && toks[i] == "]" {
						i++
					}
					continue
				}
				start, ok := parseHexUTF16Runes(third)
				i += 3
				if !ok || len(start) == 0 {
					continue
				}
				base := start[len(start)-1]
				prefix := start[:len(start)-1]
				for c := lo; c <= hi; c++ {
					runes := append(append([]rune(nil), prefix...), base+rune(c-lo))
					out[c] = string(runes)
				}
			}
			if i < len(toks) && toks[i] == "endbfrange" {
				i++
			}
		}
	}
	return out
}

func tokenizeCMap(data []byte) []string {
	var out []string
	i := 0
	for i < len(data) {
		for i < len(data) && isCMapSpace(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		// % comment
		if data[i] == '%' {
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}
		if data[i] == '<' {
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			if j >= len(data) {
				break
			}
			out = append(out, string(data[i:j+1]))
			i = j + 1
			continue
		}
		if data[i] == '[' || data[i] == ']' {
			out = append(out, string(data[i]))
			i++
			continue
		}
		j := i
		for j < len(data) && !isCMapSpace(data[j]) && data[j] != '<' && data[j] != '%' &&
			data[j] != '[' && data[j] != ']' {
			j++
		}
		if j > i {
			out = append(out, string(data[i:j]))
			i = j
		} else {
			i++
		}
	}
	return out
}

func isCMapSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\x00'
}

func parseHexCode(tok string) (uint32, bool) {
	raw, ok := decodeHexTok(tok)
	if !ok || len(raw) == 0 || len(raw) > 4 {
		return 0, false
	}
	var v uint32
	for _, b := range raw {
		v = v<<8 | uint32(b)
	}
	return v, true
}

func parseHexUTF16(tok string) (string, bool) {
	runes, ok := parseHexUTF16Runes(tok)
	if !ok {
		return "", false
	}
	return string(runes), true
}

func parseHexUTF16Runes(tok string) ([]rune, bool) {
	raw, ok := decodeHexTok(tok)
	if !ok || len(raw) == 0 {
		return nil, false
	}
	// odd length: pad trailing nibble as zero byte (rare); truncate instead
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	if len(raw) == 0 {
		return nil, false
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[i*2])<<8 | uint16(raw[i*2+1])
	}
	return utf16.Decode(u16), true
}

func decodeHexTok(tok string) ([]byte, bool) {
	if len(tok) < 2 || tok[0] != '<' || tok[len(tok)-1] != '>' {
		return nil, false
	}
	hex := make([]byte, 0, len(tok)-2)
	for _, c := range []byte(tok[1 : len(tok)-1]) {
		if isCMapSpace(c) {
			continue
		}
		hex = append(hex, c)
	}
	if len(hex)%2 != 0 {
		hex = append(hex, '0')
	}
	out := make([]byte, len(hex)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(string(hex[i*2:i*2+2]), 16, 8)
		if err != nil {
			return nil, false
		}
		out[i] = byte(v)
	}
	return out, true
}

// unicodeForCode looks up ToUnicode, then falls back to a simple-font encoding.
func unicodeForCode(toUnicode map[uint32]string, code uint32, simpleEnc string, isCID bool) string {
	if toUnicode != nil {
		if s, ok := toUnicode[code]; ok && s != "" {
			return s
		}
	}
	if isCID {
		return ""
	}
	if code > 255 {
		return ""
	}
	r := encodingToUnicode(simpleEnc, byte(code))
	if r == 0 {
		return ""
	}
	return string(r)
}

// stripSubsetPrefix removes the ABCDEF+ prefix from a PDF BaseFont name.
func stripSubsetPrefix(name string) string {
	if i := strings.IndexByte(name, '+'); i >= 0 && i <= 6 {
		return name[i+1:]
	}
	return name
}
