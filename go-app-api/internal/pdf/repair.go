package pdf

import (
	"bytes"
	"errors"
	"regexp"
)

var objHeaderRe = regexp.MustCompile(`(?m)^[ \t]*(\d{1,9})[ \t]+(\d{1,5})[ \t]+obj\b`)

// repair rebuilds a synthetic xref by scanning the whole file for
// "N G obj" headers. Later definitions of the same object win (they come
// from later incremental updates). Trailer /Root is taken from any trailer
// dict found, else by scanning for a /Type /Catalog object.
func (d *Document) repair() error {
	d.xref = make(map[int]xrefEntry)
	d.cache = make(map[int]Object)

	locs := objHeaderRe.FindAllSubmatchIndex(d.data, -1)
	if len(locs) == 0 {
		return errors.New("pdf: repair found no objects")
	}
	for _, loc := range locs {
		numStart, numEnd := loc[2], loc[3]
		num := 0
		for _, c := range d.data[numStart:numEnd] {
			num = num*10 + int(c-'0')
		}
		// later occurrences overwrite earlier ones
		d.xref[num] = xrefEntry{kind: 'n', offset: int64(loc[0] + leadingWS(d.data[loc[0]:loc[1]]))}
	}

	// register ObjStm members so compressed objects survive repair
	for num, e := range d.xref {
		if e.kind != 'n' {
			continue
		}
		obj := d.parseObjectAt(num, e.offset)
		stm, ok := obj.(*Stream)
		if !ok {
			continue
		}
		if t, _ := stm.Dict.GetName("Type"); t != "ObjStm" {
			continue
		}
		d.registerObjStmMembers(num, stm)
	}

	// find trailer keys: prefer real trailer dicts near end of file
	if err := d.repairTrailer(); err != nil {
		return err
	}
	return nil
}

func leadingWS(b []byte) int {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return i
}

// registerObjStmMembers maps every member of an ObjStm into the xref unless
// a directly-stored object with that number already exists.
func (d *Document) registerObjStmMembers(stmNum int, stm *Stream) {
	data, err := d.DecodeStream(stm)
	if err != nil {
		return
	}
	n, _ := d.GetInt(stm.Dict.Get("N"))
	first, _ := d.GetInt(stm.Dict.Get("First"))
	if n <= 0 || first <= 0 || first > int64(len(data)) {
		return
	}
	lex := NewLexer(data[:first])
	for i := int64(0); i < n; i++ {
		tNum := lex.Next()
		tOff := lex.Next()
		if tNum.Kind != TokInt || tOff.Kind != TokInt {
			return
		}
		num := clampInt(tNum.Int)
		if _, exists := d.xref[num]; !exists {
			d.xref[num] = xrefEntry{kind: 'o', stmNum: stmNum, stmIdx: int(i)}
		}
	}
}

func (d *Document) repairTrailer() error {
	// scan trailers from the end of the file
	lex := NewLexer(d.data)
	for pos := len(d.data); pos > 0; {
		idx := lastIndexBefore(d.data, []byte("trailer"), pos)
		if idx < 0 {
			break
		}
		pos = idx
		lex.Seek(idx + len("trailer"))
		p := NewParser(lex)
		if obj, err := p.ParseObject(); err == nil {
			if t, ok := obj.(Dict); ok {
				for k, v := range t {
					if _, exists := d.trailer[k]; !exists {
						d.trailer[k] = v
					}
				}
			}
		}
		if d.trailer.Get("Root") != nil {
			return nil
		}
	}
	if d.trailer.Get("Root") != nil {
		return nil
	}
	// last resort: scan every object for /Type /Catalog
	for num := range d.xref {
		obj := d.loadObject(num)
		dict, ok := obj.(Dict)
		if !ok {
			if s, ok := obj.(*Stream); ok {
				dict = s.Dict
			} else {
				continue
			}
		}
		if t, _ := dict.GetName("Type"); t == "Catalog" {
			d.trailer["Root"] = Ref{Num: num}
			return nil
		}
	}
	return errors.New("pdf: repair could not locate catalog")
}

func lastIndexBefore(data, sep []byte, before int) int {
	if before > len(data) {
		before = len(data)
	}
	return bytes.LastIndex(data[:before], sep)
}
