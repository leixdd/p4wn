package font

// CFF standard strings, reconstructed at init rather than embedded verbatim.
//
// Only SIDs 0..95 are populated: SID 0 is .notdef and SIDs 1..95 are exactly
// the Adobe StandardEncoding glyph names for codes 32..126 in code order,
// which we can derive from the encoding tables already in encoding.go.
// Higher SIDs (accented Latin, small caps, ...) are left empty deliberately:
// a missing name falls back to code-order GID mapping, whereas a wrongly
// guessed name would silently render the wrong glyph. Fonts that need those
// glyphs almost always carry an explicit CFF Encoding or custom strings
// (SID ≥ 391 reads the font's own String INDEX, which is exact).
var cffStandardStrings [391]string

func init() {
	cffStandardStrings[0] = ".notdef"

	// invert name→unicode to recover canonical names per code point
	uniToName := map[rune]string{}
	for name, r := range aglSubset {
		if _, exists := uniToName[r]; !exists {
			uniToName[r] = name
		}
	}
	for c := byte('A'); c <= 'Z'; c++ {
		uniToName[rune(c)] = string(c)
	}
	for c := byte('a'); c <= 'z'; c++ {
		uniToName[rune(c)] = string(c)
	}
	digits := []string{"zero", "one", "two", "three", "four",
		"five", "six", "seven", "eight", "nine"}
	for i, d := range digits {
		uniToName[rune('0'+i)] = d
	}

	// SIDs 1..95 ↔ StandardEncoding codes 32..126
	for code := 32; code <= 126; code++ {
		sid := code - 31
		r := encodingToUnicode("StandardEncoding", byte(code))
		if r == 0 {
			continue
		}
		if name, ok := uniToName[r]; ok {
			cffStandardStrings[sid] = name
		}
	}
}
