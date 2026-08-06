package pdf

import (
	"errors"
	"fmt"
)

// Parser builds Objects from a Lexer. It implements the classic PDF
// three-token lookahead needed for "num gen R" indirect references.
type Parser struct {
	lex *Lexer
	// up to two pushed-back tokens; tok is consumed before tok2
	peeked  bool
	tok     Token
	peeked2 bool
	tok2    Token
}

func NewParser(lex *Lexer) *Parser { return &Parser{lex: lex} }

func (p *Parser) next() Token {
	if p.peeked {
		p.peeked = false
		return p.tok
	}
	if p.peeked2 {
		p.peeked2 = false
		return p.tok2
	}
	return p.lex.Next()
}

func (p *Parser) unread(t Token) {
	if p.peeked {
		// shift existing token to the second slot
		p.tok2 = p.tok
		p.peeked2 = true
	}
	p.tok = t
	p.peeked = true
}

var errUnexpected = errors.New("pdf: unexpected token")

const maxParseDepth = 100 // recursion guard for hostile files

// ParseObject parses one object (not an indirect "N G obj" wrapper).
func (p *Parser) ParseObject() (Object, error) {
	return p.parseObject(0)
}

func (p *Parser) parseObject(depth int) (Object, error) {
	if depth > maxParseDepth {
		return nil, errors.New("pdf: object nesting too deep")
	}
	t := p.next()
	return p.parseFromToken(t, depth)
}

func (p *Parser) parseFromToken(t Token, depth int) (Object, error) {
	switch t.Kind {
	case TokEOF:
		return nil, fmt.Errorf("%w: EOF", errUnexpected)
	case TokReal:
		return Real(t.Real), nil
	case TokString:
		return String(append([]byte(nil), t.Buf...)), nil
	case TokName:
		return Name(t.Buf), nil
	case TokArrayOpen:
		return p.parseArray(depth + 1)
	case TokDictOpen:
		return p.parseDict(depth + 1)
	case TokInt:
		// Might begin "num gen R". Look ahead two tokens.
		t2 := p.next()
		if t2.Kind == TokInt {
			t3 := p.next()
			if t3.IsKeyword("R") {
				return Ref{Num: clampInt(t.Int), Gen: clampInt(t2.Int)}, nil
			}
			// not a ref: push back what we can (one-token buffer). We
			// re-lex t3 by seeking is not possible; instead treat t2/t3 via
			// a small queue — see pushback note below.
			p.pushback2(t2, t3)
			return Integer(t.Int), nil
		}
		p.unread(t2)
		return Integer(t.Int), nil
	case TokKeyword:
		switch string(t.Buf) {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		case "null":
			return Null{}, nil
		}
		return nil, fmt.Errorf("%w: keyword %q", errUnexpected, t.Buf)
	default:
		return nil, fmt.Errorf("%w: kind %d", errUnexpected, t.Kind)
	}
}

// pushback2 pushes back two tokens; a is consumed before b.
func (p *Parser) pushback2(a, b Token) {
	p.tok = a
	p.peeked = true
	p.tok2 = b
	p.peeked2 = true
}

func (p *Parser) parseArray(depth int) (Object, error) {
	arr := Array{}
	for {
		t := p.next()
		switch t.Kind {
		case TokArrayClose:
			return arr, nil
		case TokEOF:
			return arr, nil // tolerate unterminated arrays
		case TokDictClose:
			// broken file: "]" missing; back out
			p.unread(t)
			return arr, nil
		}
		obj, err := p.parseFromToken(t, depth+1)
		if err != nil {
			// skip junk tokens inside arrays (tolerance)
			continue
		}
		arr = append(arr, obj)
	}
}

func (p *Parser) parseDict(depth int) (Object, error) {
	d := Dict{}
	for {
		t := p.next()
		switch t.Kind {
		case TokDictClose:
			return d, nil
		case TokEOF:
			return d, nil // tolerate unterminated dicts
		}
		if t.Kind != TokName {
			// "ID" terminates inline-image dicts; other junk keys skipped
			if t.IsKeyword("ID") {
				p.unread(t)
				return d, nil
			}
			continue
		}
		key := Name(t.Buf)
		val, err := p.parseObject(depth + 1)
		if err != nil {
			continue
		}
		d[key] = val
	}
}

// ParseIndirect parses "num gen obj <object> (stream ...)? endobj" at the
// current position. Returns the object number/gen actually found, the parsed
// object (a *Stream if stream data follows), or an error.
func (p *Parser) ParseIndirect() (num, gen int, obj Object, err error) {
	t1 := p.next()
	t2 := p.next()
	t3 := p.next()
	if t1.Kind != TokInt || t2.Kind != TokInt || !t3.IsKeyword("obj") {
		return 0, 0, nil, fmt.Errorf("pdf: expected 'N G obj', got %v %v %v", t1.Kind, t2.Kind, string(t3.Buf))
	}
	num, gen = clampInt(t1.Int), clampInt(t2.Int)
	obj, err = p.ParseObject()
	if err != nil {
		return num, gen, nil, err
	}
	t := p.next()
	if t.IsKeyword("stream") {
		dict, ok := obj.(Dict)
		if !ok {
			return num, gen, nil, errors.New("pdf: stream keyword after non-dict")
		}
		// after "stream": optional \r then required \n (tolerate bare data)
		data := p.lex.Data()
		pos := p.lex.Pos()
		if pos < len(data) && data[pos] == '\r' {
			pos++
		}
		if pos < len(data) && data[pos] == '\n' {
			pos++
		}
		return num, gen, &Stream{Dict: dict, Offset: int64(pos)}, nil
	}
	// "endobj" expected; tolerate its absence
	return num, gen, obj, nil
}

// ErrContentEOF signals the end of a content stream to the interpreter.
var ErrContentEOF = errors.New("pdf: end of content")

// NextContentItem reads the next item of a content stream: either an operand
// Object (op == "") or an operator keyword (obj == nil). Unparseable junk is
// returned as Null so the interpreter can keep going.
func (p *Parser) NextContentItem() (obj Object, op string, err error) {
	t := p.next()
	switch t.Kind {
	case TokEOF:
		return nil, "", ErrContentEOF
	case TokKeyword:
		switch string(t.Buf) {
		case "true":
			return Bool(true), "", nil
		case "false":
			return Bool(false), "", nil
		case "null":
			return Null{}, "", nil
		}
		return nil, string(t.Buf), nil
	case TokBraceOpen, TokBraceClose:
		return Null{}, "", nil // type-4 function syntax: not valid here
	default:
		o, perr := p.parseFromToken(t, 0)
		if perr != nil {
			return Null{}, "", nil
		}
		return o, "", nil
	}
}

// Lexer exposes the underlying lexer (the interpreter needs raw positioning
// for inline image data).
func (p *Parser) Lexer() *Lexer { return p.lex }

// FlushPushback drops any buffered lookahead tokens (call before seeking the
// underlying lexer).
func (p *Parser) FlushPushback() { p.peeked, p.peeked2 = false, false }
