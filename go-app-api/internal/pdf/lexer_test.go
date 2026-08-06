package pdf

import (
	"bytes"
	"testing"
)

func lexAll(t *testing.T, src string) []Token {
	t.Helper()
	lex := NewLexer([]byte(src))
	var toks []Token
	for {
		tok := lex.Next()
		if tok.Kind == TokEOF {
			return toks
		}
		toks = append(toks, tok)
		if len(toks) > 1000 {
			t.Fatal("lexer runaway")
		}
	}
}

func TestLexNumbers(t *testing.T) {
	cases := []struct {
		src  string
		kind TokenKind
		i    int64
		r    float64
	}{
		{"42", TokInt, 42, 0},
		{"-17", TokInt, -17, 0},
		{"+3", TokInt, 3, 0},
		{"3.14", TokReal, 0, 3.14},
		{".5", TokReal, 0, 0.5},
		{"-.25", TokReal, 0, -0.25},
		{"4.", TokReal, 0, 4},
		{"--3", TokInt, 3, 0},     // doubled sign tolerated
		{"1.2.3", TokReal, 0, 1.2}, // second dot terminates
	}
	for _, c := range cases {
		toks := lexAll(t, c.src)
		if len(toks) == 0 {
			t.Fatalf("%q: no tokens", c.src)
		}
		tok := toks[0]
		if tok.Kind != c.kind {
			t.Errorf("%q: kind = %d, want %d", c.src, tok.Kind, c.kind)
			continue
		}
		if c.kind == TokInt && tok.Int != c.i {
			t.Errorf("%q: int = %d, want %d", c.src, tok.Int, c.i)
		}
		if c.kind == TokReal && (tok.Real < c.r-1e-9 || tok.Real > c.r+1e-9) {
			t.Errorf("%q: real = %v, want %v", c.src, tok.Real, c.r)
		}
	}
}

func TestLexStrings(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		{`(hello)`, "hello"},
		{`(a(b)c)`, "a(b)c"},                 // balanced nesting
		{`(a\(b)`, "a(b"},                    // escaped paren
		{"(a\\\nb)", "ab"},                   // line continuation
		{`(\101\102)`, "AB"},                 // octal
		{`(\61)`, "1"},                       // short octal
		{"(a\r\nb)", "a\nb"},                 // CRLF normalized
		{"(a\rb)", "a\nb"},                   // CR normalized
		{`(tab\there)`, "tab\there"},         // \t
		{`<48656C6C6F>`, "Hello"},            // hex
		{`<48 65 6C>`, "Hel"},                // hex with whitespace
		{`<486>`, "H`"},                      // odd nibble → pad 0: 0x48 0x60
	}
	for _, c := range cases {
		toks := lexAll(t, c.src)
		if len(toks) != 1 || toks[0].Kind != TokString {
			t.Errorf("%q: expected one string token, got %v", c.src, toks)
			continue
		}
		if !bytes.Equal(toks[0].Buf, []byte(c.want)) {
			t.Errorf("%q: got %q, want %q", c.src, toks[0].Buf, c.want)
		}
	}
}

func TestLexNames(t *testing.T) {
	cases := []struct{ src, want string }{
		{"/Name", "Name"},
		{"/A#20B", "A B"},         // hex escape
		{"/Lime#20Green", "Lime Green"},
		{"/", ""},                 // empty name is legal
		{"/paired#28#29", "paired()"},
	}
	for _, c := range cases {
		toks := lexAll(t, c.src)
		if len(toks) != 1 || toks[0].Kind != TokName {
			t.Errorf("%q: expected one name token, got %+v", c.src, toks)
			continue
		}
		if string(toks[0].Buf) != c.want {
			t.Errorf("%q: got %q, want %q", c.src, toks[0].Buf, c.want)
		}
	}
}

func TestLexStructure(t *testing.T) {
	toks := lexAll(t, "<< /Type /Page >> [ 1 2 R ] % comment\ntrue")
	kinds := []TokenKind{TokDictOpen, TokName, TokName, TokDictClose,
		TokArrayOpen, TokInt, TokInt, TokKeyword, TokArrayClose, TokKeyword}
	if len(toks) != len(kinds) {
		t.Fatalf("got %d tokens, want %d: %+v", len(toks), len(kinds), toks)
	}
	for i, k := range kinds {
		if toks[i].Kind != k {
			t.Errorf("token %d: kind %d, want %d", i, toks[i].Kind, k)
		}
	}
}

func TestParseObjects(t *testing.T) {
	parse := func(src string) Object {
		p := NewParser(NewLexer([]byte(src)))
		obj, err := p.ParseObject()
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		return obj
	}
	if v, ok := parse("42").(Integer); !ok || v != 42 {
		t.Errorf("int: %#v", parse("42"))
	}
	if _, ok := parse("<< /A 1 /B (x) >>").(Dict); !ok {
		t.Error("dict parse failed")
	}
	arr, ok := parse("[1 2 0 R 3]").(Array)
	if !ok || len(arr) != 3 {
		t.Fatalf("array with ref: %#v", parse("[1 2 0 R 3]"))
	}
	if r, ok := arr[1].(Ref); !ok || r.Num != 2 || r.Gen != 0 {
		t.Errorf("ref in array: %#v", arr[1])
	}
	if v, ok := arr[2].(Integer); !ok || v != 3 {
		t.Errorf("trailing int after ref: %#v", arr[2])
	}
	// "num gen" NOT followed by R must yield two integers
	arr2 := parse("[10 20 30]").(Array)
	if len(arr2) != 3 {
		t.Fatalf("plain int array: %#v", arr2)
	}
	if _, ok := parse("null").(Null); !ok {
		t.Error("null parse failed")
	}
}
