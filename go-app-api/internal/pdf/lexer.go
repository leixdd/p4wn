package pdf

import (
	"strconv"
)

// TokenKind identifies a lexical token in a PDF file or content stream.
type TokenKind int

const (
	TokEOF TokenKind = iota
	TokInt
	TokReal
	TokName       // /Name, decoded
	TokString     // (literal) or <hex>, decoded
	TokArrayOpen  // [
	TokArrayClose // ]
	TokDictOpen   // <<
	TokDictClose  // >>
	TokBraceOpen  // { (type-4 functions)
	TokBraceClose // }
	TokKeyword    // obj, endobj, stream, R, true, false, null, operators...
)

type Token struct {
	Kind TokenKind
	Int  int64
	Real float64
	Buf  []byte // name/string/keyword payload
}

// IsKeyword reports whether t is the keyword kw.
func (t Token) IsKeyword(kw string) bool {
	return t.Kind == TokKeyword && string(t.Buf) == kw
}

// Lexer tokenizes PDF syntax over an in-memory byte slice.
type Lexer struct {
	data []byte
	pos  int
}

func NewLexer(data []byte) *Lexer { return &Lexer{data: data} }

func (l *Lexer) Pos() int        { return l.pos }
func (l *Lexer) Seek(pos int)    { l.pos = pos }
func (l *Lexer) Data() []byte    { return l.data }
func (l *Lexer) atEOF() bool     { return l.pos >= len(l.data) }
func (l *Lexer) peek() byte      { return l.data[l.pos] }
func (l *Lexer) next() byte      { c := l.data[l.pos]; l.pos++; return c }

func isWhite(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(c byte) bool { return !isWhite(c) && !isDelim(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// skipWhite skips whitespace and %-comments.
func (l *Lexer) skipWhite() {
	for !l.atEOF() {
		c := l.peek()
		if isWhite(c) {
			l.pos++
		} else if c == '%' {
			for !l.atEOF() && l.peek() != '\n' && l.peek() != '\r' {
				l.pos++
			}
		} else {
			return
		}
	}
}

// Next returns the next token.
func (l *Lexer) Next() Token {
	l.skipWhite()
	if l.atEOF() {
		return Token{Kind: TokEOF}
	}
	c := l.peek()
	switch {
	case c == '[':
		l.pos++
		return Token{Kind: TokArrayOpen}
	case c == ']':
		l.pos++
		return Token{Kind: TokArrayClose}
	case c == '{':
		l.pos++
		return Token{Kind: TokBraceOpen}
	case c == '}':
		l.pos++
		return Token{Kind: TokBraceClose}
	case c == '<':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return Token{Kind: TokDictOpen}
		}
		l.pos++
		return l.lexHexString()
	case c == '>':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '>' {
			l.pos += 2
			return Token{Kind: TokDictClose}
		}
		// stray '>' — treat as keyword so callers can recover
		l.pos++
		return Token{Kind: TokKeyword, Buf: []byte{'>'}}
	case c == '(':
		l.pos++
		return l.lexString()
	case c == ')':
		// stray ')' — skip and recover
		l.pos++
		return l.Next()
	case c == '/':
		l.pos++
		return l.lexName()
	case isDigit(c) || c == '+' || c == '-' || c == '.':
		return l.lexNumber()
	default:
		return l.lexKeyword()
	}
}

// lexNumber tolerates real-world garbage: doubled signs, multiple dots
// (takes digits up to the second dot's junk), embedded minus (truncates).
func (l *Lexer) lexNumber() Token {
	start := l.pos
	isReal := false
	// optional sign(s)
	for !l.atEOF() && (l.peek() == '+' || l.peek() == '-') {
		l.pos++
	}
	numStart := l.pos
	for !l.atEOF() {
		c := l.peek()
		if isDigit(c) {
			l.pos++
		} else if c == '.' {
			isReal = true
			l.pos++
		} else if c == '-' || c == '+' || c == 'e' || c == 'E' {
			// junk inside a number (e.g. "1.5-3"): stop here
			break
		} else {
			break
		}
	}
	text := l.data[start:l.pos]
	if len(text) == 0 || l.pos == numStart && !isReal {
		// bare sign with no digits — treat as keyword
		return Token{Kind: TokKeyword, Buf: text}
	}
	if isReal {
		f := parseRealTolerant(text)
		return Token{Kind: TokReal, Real: f}
	}
	// integer; doubled signs and overflow fall back to the tolerant parser
	i, err := strconv.ParseInt(string(text), 10, 64)
	if err != nil {
		return Token{Kind: TokInt, Int: int64(parseRealTolerant(text))}
	}
	return Token{Kind: TokInt, Int: i}
}

// parseRealTolerant parses a possibly malformed real ("1.2.3", "--2", ".").
func parseRealTolerant(text []byte) float64 {
	neg := false
	i := 0
	for i < len(text) && (text[i] == '+' || text[i] == '-') {
		if text[i] == '-' {
			neg = !neg
		}
		i++
	}
	var intPart, fracPart float64
	var fracDiv float64 = 1
	seenDot := false
	for ; i < len(text); i++ {
		c := text[i]
		if c == '.' {
			if seenDot {
				break // second dot terminates
			}
			seenDot = true
			continue
		}
		if !isDigit(c) {
			break
		}
		if seenDot {
			fracDiv *= 10
			fracPart += float64(c-'0') / fracDiv
		} else {
			intPart = intPart*10 + float64(c-'0')
		}
	}
	v := intPart + fracPart
	if neg {
		v = -v
	}
	return v
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// lexName lexes a name after the leading '/', decoding #xx escapes.
func (l *Lexer) lexName() Token {
	var buf []byte
	for !l.atEOF() && isRegular(l.peek()) {
		c := l.next()
		if c == '#' && l.pos+1 < len(l.data) {
			h1, ok1 := hexVal(l.data[l.pos])
			h2, ok2 := hexVal(l.data[l.pos+1])
			if ok1 && ok2 {
				buf = append(buf, h1<<4|h2)
				l.pos += 2
				continue
			}
		}
		buf = append(buf, c)
	}
	return Token{Kind: TokName, Buf: buf}
}

// lexString lexes a literal string after the opening '(' with escapes and
// balanced-paren nesting; raw CR / CRLF normalize to LF.
func (l *Lexer) lexString() Token {
	var buf []byte
	depth := 1
	for !l.atEOF() {
		c := l.next()
		switch c {
		case '(':
			depth++
			buf = append(buf, c)
		case ')':
			depth--
			if depth == 0 {
				return Token{Kind: TokString, Buf: buf}
			}
			buf = append(buf, c)
		case '\r':
			// CR or CRLF → LF
			if !l.atEOF() && l.peek() == '\n' {
				l.pos++
			}
			buf = append(buf, '\n')
		case '\\':
			if l.atEOF() {
				break
			}
			e := l.next()
			switch e {
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case '(', ')', '\\':
				buf = append(buf, e)
			case '\r':
				// line continuation; swallow optional LF
				if !l.atEOF() && l.peek() == '\n' {
					l.pos++
				}
			case '\n':
				// line continuation
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && !l.atEOF(); k++ {
						d := l.peek()
						if d < '0' || d > '7' {
							break
						}
						v = v*8 + int(d-'0')
						l.pos++
					}
					buf = append(buf, byte(v))
				} else {
					// unknown escape: emit char as-is
					buf = append(buf, e)
				}
			}
		default:
			buf = append(buf, c)
		}
	}
	return Token{Kind: TokString, Buf: buf} // unterminated: return what we have
}

// lexHexString lexes <hex...> after the opening '<'.
func (l *Lexer) lexHexString() Token {
	var buf []byte
	var hi byte
	half := false
	for !l.atEOF() {
		c := l.next()
		if c == '>' {
			break
		}
		v, ok := hexVal(c)
		if !ok {
			continue // whitespace and junk tolerated
		}
		if half {
			buf = append(buf, hi<<4|v)
			half = false
		} else {
			hi = v
			half = true
		}
	}
	if half {
		buf = append(buf, hi<<4) // odd nibble padded with 0
	}
	return Token{Kind: TokString, Buf: buf}
}

func (l *Lexer) lexKeyword() Token {
	start := l.pos
	for !l.atEOF() && isRegular(l.peek()) {
		l.pos++
	}
	if l.pos == start {
		// lone delimiter we don't understand: skip it
		l.pos++
		return l.Next()
	}
	return Token{Kind: TokKeyword, Buf: l.data[start:l.pos]}
}
