package font

import "testing"

func TestParseToUnicodeBFChar(t *testing.T) {
	data := []byte(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
1 begincodespacerange
<00> <FF>
endcodespacerange
2 beginbfchar
<41> <0041>
<42> <0042>
endbfchar
endcmap
`)
	m := parseToUnicode(data)
	if m[0x41] != "A" || m[0x42] != "B" {
		t.Fatalf("got %#v", m)
	}
}

func TestParseToUnicodeBFRange(t *testing.T) {
	data := []byte(`
beginbfrange
<21> <23> <0041>
<30> <31> [<0061> <0062>]
endbfrange
`)
	m := parseToUnicode(data)
	if m[0x21] != "A" || m[0x22] != "B" || m[0x23] != "C" {
		t.Fatalf("incremental range: %#v", m)
	}
	if m[0x30] != "a" || m[0x31] != "b" {
		t.Fatalf("array range: %#v", m)
	}
}

func TestUnicodeForCodeSimple(t *testing.T) {
	if got := unicodeForCode(nil, 'A', "WinAnsiEncoding", false); got != "A" {
		t.Fatalf("got %q", got)
	}
	if got := unicodeForCode(map[uint32]string{1: "€"}, 1, "WinAnsiEncoding", false); got != "€" {
		t.Fatalf("got %q", got)
	}
	if got := unicodeForCode(nil, 0x4E00, "", true); got != "" {
		t.Fatalf("cid without map should be empty, got %q", got)
	}
}

func TestParseToUnicodeJapaneseTwoByte(t *testing.T) {
	data := []byte(`
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
3 beginbfchar
<3042> <3042>
<30A2> <30A2>
<65E5> <65E5>
endbfchar
1 beginbfrange
<3044> <3046> <3044>
endbfrange
`)
	m := parseToUnicode(data)
	if m[0x3042] != "あ" {
		t.Fatalf("hiragana a: %#v", m[0x3042])
	}
	if m[0x30A2] != "ア" {
		t.Fatalf("katakana a: %#v", m[0x30A2])
	}
	if m[0x65E5] != "日" {
		t.Fatalf("kanji: %#v", m[0x65E5])
	}
	if m[0x3044] != "い" || m[0x3045] != "ぅ" || m[0x3046] != "う" {
		t.Fatalf("hiragana range: %#v %#v %#v", m[0x3044], m[0x3045], m[0x3046])
	}
}

func TestStripSubsetPrefix(t *testing.T) {
	if got := stripSubsetPrefix("ABCDEF+Helvetica"); got != "Helvetica" {
		t.Fatalf("got %q", got)
	}
	if got := stripSubsetPrefix("Helvetica"); got != "Helvetica" {
		t.Fatalf("got %q", got)
	}
}
