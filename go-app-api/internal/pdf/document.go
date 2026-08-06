package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

// xrefEntry locates one object.
type xrefEntry struct {
	kind   byte  // 'n' = at offset, 'o' = in object stream, 'f' = free
	offset int64 // for 'n'
	gen    int
	stmNum int // for 'o': object number of the containing ObjStm
	stmIdx int // for 'o': index within the ObjStm
}

// Document is an open PDF file. Safe for concurrent Resolve calls.
type Document struct {
	data    []byte
	trailer Dict
	xref    map[int]xrefEntry
	crypt   *crypt // nil = unencrypted

	mu      sync.Mutex
	cache   map[int]Object
	loading map[int]bool // ObjStm recursion guard

	pages []Dict // flattened page dicts, populated by loadPageTree
}

// Open parses the xref machinery of a PDF held in memory.
func Open(data []byte) (*Document, error) {
	doc := &Document{
		data:    data,
		trailer: Dict{},
		xref:    make(map[int]xrefEntry),
		cache:   make(map[int]Object),
		loading: make(map[int]bool),
	}
	if err := doc.loadXref(); err != nil {
		// broken xref: fall back to the repair scanner
		if rerr := doc.repair(); rerr != nil {
			return nil, fmt.Errorf("pdf: load xref: %w (repair also failed: %v)", err, rerr)
		}
	}
	if doc.trailer.Get("Root") == nil {
		if rerr := doc.repair(); rerr != nil || doc.trailer.Get("Root") == nil {
			return nil, errors.New("pdf: no document catalog")
		}
	}
	if err := doc.initCrypt(); err != nil {
		return nil, err
	}
	if err := doc.loadPageTree(); err != nil {
		return nil, err
	}
	return doc, nil
}

// NumPages returns the number of pages.
func (d *Document) NumPages() int { return len(d.pages) }

// Trailer returns the (merged) trailer dictionary.
func (d *Document) Trailer() Dict { return d.trailer }

// Resolve chases indirect references until a direct object is reached.
func (d *Document) Resolve(obj Object) Object {
	for i := 0; i < 32; i++ { // ref-chain cycle guard
		ref, ok := obj.(Ref)
		if !ok {
			return obj
		}
		obj = d.loadObject(ref.Num)
	}
	return Null{}
}

// GetDict resolves obj and returns it as a Dict (nil if not one).
func (d *Document) GetDict(obj Object) Dict {
	dict, _ := d.Resolve(obj).(Dict)
	return dict
}

// GetArray resolves obj and returns it as an Array (nil if not one).
func (d *Document) GetArray(obj Object) Array {
	arr, _ := d.Resolve(obj).(Array)
	return arr
}

// GetInt resolves obj to an integer.
func (d *Document) GetInt(obj Object) (int64, bool) {
	return ToInt(d.Resolve(obj))
}

// GetFloat resolves obj to a float.
func (d *Document) GetFloat(obj Object) (float64, bool) {
	return ToFloat(d.Resolve(obj))
}

// GetName resolves obj to a Name.
func (d *Document) GetName(obj Object) (Name, bool) {
	n, ok := d.Resolve(obj).(Name)
	return n, ok
}

// DictGet resolves dict[key].
func (d *Document) DictGet(dict Dict, key Name) Object {
	return d.Resolve(dict.Get(key))
}

// loadObject returns the object with the given number, loading and caching
// it on first use.
func (d *Document) loadObject(num int) Object {
	d.mu.Lock()
	if obj, ok := d.cache[num]; ok {
		d.mu.Unlock()
		return obj
	}
	entry, ok := d.xref[num]
	d.mu.Unlock()
	if !ok || entry.kind == 'f' {
		return Null{}
	}

	var obj Object
	switch entry.kind {
	case 'n':
		obj = d.parseObjectAt(num, entry.offset)
	case 'o':
		obj = d.loadFromObjStm(num, entry.stmNum)
	default:
		obj = Null{}
	}

	d.mu.Lock()
	d.cache[num] = obj
	d.mu.Unlock()
	return obj
}

// parseObjectAt parses the indirect object at a byte offset.
func (d *Document) parseObjectAt(wantNum int, offset int64) Object {
	if offset < 0 || offset >= int64(len(d.data)) {
		return Null{}
	}
	lex := NewLexer(d.data)
	lex.Seek(int(offset))
	p := NewParser(lex)
	num, gen, obj, err := p.ParseIndirect()
	if err != nil || num != wantNum {
		return Null{}
	}
	if d.crypt != nil {
		obj = d.decryptObject(obj, num, gen)
	}
	return obj
}

// --- xref loading -----------------------------------------------------------

const maxXrefSections = 512 // /Prev chain cycle/bomb guard

func (d *Document) loadXref() error {
	start, err := d.findStartXref()
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	offset := start
	for n := 0; n < maxXrefSections; n++ {
		if offset < 0 || offset >= int64(len(d.data)) || seen[offset] {
			break
		}
		seen[offset] = true
		trailer, err := d.readXrefSection(offset)
		if err != nil {
			return err
		}
		// first-seen (newest) trailer keys win
		for k, v := range trailer {
			if _, exists := d.trailer[k]; !exists {
				d.trailer[k] = v
			}
		}
		// hybrid-reference files: /XRefStm points at an xref stream whose
		// entries take precedence over this classic section's — but since we
		// fill first-seen-wins and the classic section was already read, read
		// it anyway to pick up entries the classic table marked free.
		if xs, ok := ToInt(trailer.Get("XRefStm")); ok && !seen[xs] {
			seen[xs] = true
			if _, err := d.readXrefSection(xs); err == nil {
				// entries merged inside readXrefSection
			}
		}
		prev, ok := ToInt(trailer.Get("Prev"))
		if !ok {
			break
		}
		offset = prev
	}
	if len(d.xref) == 0 {
		return errors.New("pdf: empty xref")
	}
	return nil
}

// findStartXref scans the file tail for the startxref offset.
func (d *Document) findStartXref() (int64, error) {
	tail := d.data
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, errors.New("pdf: startxref not found")
	}
	lex := NewLexer(tail)
	lex.Seek(idx + len("startxref"))
	t := lex.Next()
	if t.Kind != TokInt {
		return 0, errors.New("pdf: malformed startxref")
	}
	return t.Int, nil
}

// setEntry records an xref entry unless a newer section already defined the
// object (sections are read newest → oldest).
func (d *Document) setEntry(num int, e xrefEntry) {
	if _, exists := d.xref[num]; !exists {
		d.xref[num] = e
	}
}

// readXrefSection reads either a classic table or an xref stream at offset,
// merging entries, and returns that section's trailer dict.
func (d *Document) readXrefSection(offset int64) (Dict, error) {
	lex := NewLexer(d.data)
	lex.Seek(int(offset))
	t := lex.Next()
	if t.IsKeyword("xref") {
		return d.readClassicXref(lex)
	}
	// else it must be "N G obj" introducing an xref stream
	lex.Seek(int(offset))
	return d.readXrefStream(lex)
}

func (d *Document) readClassicXref(lex *Lexer) (Dict, error) {
	for {
		t := lex.Next()
		if t.IsKeyword("trailer") {
			p := NewParser(lex)
			obj, err := p.ParseObject()
			if err != nil {
				return nil, err
			}
			trailer, ok := obj.(Dict)
			if !ok {
				return nil, errors.New("pdf: trailer is not a dict")
			}
			return trailer, nil
		}
		if t.Kind != TokInt {
			return nil, fmt.Errorf("pdf: malformed xref table (kind %d)", t.Kind)
		}
		start := t.Int
		t = lex.Next()
		if t.Kind != TokInt {
			return nil, errors.New("pdf: malformed xref subsection header")
		}
		count := t.Int
		if count < 0 || count > 1<<24 {
			return nil, errors.New("pdf: unreasonable xref subsection size")
		}
		for i := int64(0); i < count; i++ {
			// fixed-width entries, but lex tolerantly
			tOff := lex.Next()
			tGen := lex.Next()
			tKind := lex.Next()
			if tOff.Kind != TokInt || tGen.Kind != TokInt || tKind.Kind != TokKeyword {
				return nil, errors.New("pdf: malformed xref entry")
			}
			num := clampInt(start + i)
			switch string(tKind.Buf) {
			case "n":
				d.setEntry(num, xrefEntry{kind: 'n', offset: tOff.Int, gen: clampInt(tGen.Int)})
			case "f":
				d.setEntry(num, xrefEntry{kind: 'f'})
			}
		}
	}
}

func (d *Document) readXrefStream(lex *Lexer) (Dict, error) {
	p := NewParser(lex)
	_, _, obj, err := p.ParseIndirect()
	if err != nil {
		return nil, err
	}
	stm, ok := obj.(*Stream)
	if !ok {
		return nil, errors.New("pdf: xref stream expected")
	}
	data, err := d.DecodeStream(stm)
	if err != nil {
		return nil, fmt.Errorf("pdf: decode xref stream: %w", err)
	}
	dict := stm.Dict

	// /W field widths
	wArr, ok := dict.Get("W").(Array)
	if !ok || len(wArr) < 3 {
		return nil, errors.New("pdf: xref stream missing /W")
	}
	var w [3]int
	for i := 0; i < 3; i++ {
		v, _ := ToInt(wArr[i])
		if v < 0 || v > 8 {
			return nil, errors.New("pdf: bad /W width")
		}
		w[i] = int(v)
	}
	entryLen := w[0] + w[1] + w[2]
	if entryLen <= 0 {
		return nil, errors.New("pdf: zero-width xref entries")
	}

	size, _ := ToInt(dict.Get("Size"))
	// /Index defaults to [0 Size]
	var index []int64
	if idxArr, ok := dict.Get("Index").(Array); ok {
		for _, o := range idxArr {
			v, _ := ToInt(o)
			index = append(index, v)
		}
	}
	if len(index) < 2 {
		index = []int64{0, size}
	}

	pos := 0
	readField := func(width int) int64 {
		v := int64(0)
		for i := 0; i < width; i++ {
			v = v<<8 | int64(data[pos])
			pos++
		}
		return v
	}
	for i := 0; i+1 < len(index); i += 2 {
		start, count := index[i], index[i+1]
		for j := int64(0); j < count; j++ {
			if pos+entryLen > len(data) {
				return dict, nil // truncated stream: keep what we parsed
			}
			typ := int64(1) // missing type field defaults to 1
			if w[0] > 0 {
				typ = readField(w[0])
			}
			f2 := readField(w[1])
			f3 := readField(w[2])
			num := clampInt(start + j)
			switch typ {
			case 0:
				d.setEntry(num, xrefEntry{kind: 'f'})
			case 1:
				d.setEntry(num, xrefEntry{kind: 'n', offset: f2, gen: clampInt(f3)})
			case 2:
				d.setEntry(num, xrefEntry{kind: 'o', stmNum: clampInt(f2), stmIdx: clampInt(f3)})
			}
		}
	}
	return dict, nil
}
