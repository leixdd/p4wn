package filter

import "testing"

// FuzzDecode: every filter must survive arbitrary input without panicking.
func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x78, 0x9C, 0x01, 0x02}, 12, 4)
	f.Add([]byte("9jqo^~>"), 1, 1)
	f.Add([]byte{0x80, 0x0B, 0x60}, 2, 3)
	f.Fuzz(func(t *testing.T, data []byte, predictor, columns int) {
		p := DefaultParams()
		p.Predictor = predictor % 20
		p.Columns = columns%5000 + 1
		for _, name := range []string{"FlateDecode", "LZWDecode", "ASCIIHexDecode",
			"ASCII85Decode", "RunLengthDecode"} {
			Decode(name, p, data)
		}
	})
}
