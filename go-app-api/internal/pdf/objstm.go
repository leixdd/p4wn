package pdf

// loadFromObjStm extracts an object stored inside an object stream. The whole
// ObjStm is parsed on first access and its members cached.
func (d *Document) loadFromObjStm(wantNum, stmNum int) Object {
	d.mu.Lock()
	if d.loading[stmNum] {
		d.mu.Unlock()
		return Null{} // recursive ObjStm reference: corrupt file
	}
	d.loading[stmNum] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.loading, stmNum)
		d.mu.Unlock()
	}()

	container := d.loadObject(stmNum)
	stm, ok := container.(*Stream)
	if !ok {
		return Null{}
	}
	data, err := d.DecodeStream(stm)
	if err != nil || len(data) == 0 {
		return Null{}
	}
	n, _ := d.GetInt(stm.Dict.Get("N"))
	first, _ := d.GetInt(stm.Dict.Get("First"))
	if n <= 0 || first < 0 || first > int64(len(data)) {
		return Null{}
	}

	// header: N pairs of "objnum offset" (offsets relative to First)
	lex := NewLexer(data[:first])
	type member struct {
		num int
		off int64
	}
	members := make([]member, 0, n)
	for i := int64(0); i < n; i++ {
		tNum := lex.Next()
		tOff := lex.Next()
		if tNum.Kind != TokInt || tOff.Kind != TokInt {
			break
		}
		members = append(members, member{num: clampInt(tNum.Int), off: tOff.Int})
	}

	var result Object = Null{}
	body := NewLexer(data)
	for _, m := range members {
		pos := first + m.off
		if pos < 0 || pos >= int64(len(data)) {
			continue
		}
		// only members this xref actually maps into this ObjStm are cached,
		// so a direct object added by a later incremental update is not
		// shadowed by a stale ObjStm copy
		d.mu.Lock()
		entry, mapped := d.xref[m.num]
		_, cached := d.cache[m.num]
		d.mu.Unlock()
		if cached || !mapped || entry.kind != 'o' || entry.stmNum != stmNum {
			if m.num != wantNum {
				continue
			}
		}
		body.Seek(int(pos))
		p := NewParser(body)
		obj, err := p.ParseObject()
		if err != nil {
			continue
		}
		if m.num == wantNum {
			result = obj
		} else {
			d.mu.Lock()
			d.cache[m.num] = obj
			d.mu.Unlock()
		}
	}
	return result
}
