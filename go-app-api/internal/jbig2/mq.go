package jbig2

// MQ arithmetic decoder (T.88 Annex E), integer decoders (Annex A.2) and
// symbol ID decoder (A.3).

type qe struct {
	qe         uint32
	nmps, nlps uint8
	switchMPS  bool
}

// qeTable is Table E.1.
var qeTable = [47]qe{
	{0x5601, 1, 1, true}, {0x3401, 2, 6, false}, {0x1801, 3, 9, false}, {0x0AC1, 4, 12, false},
	{0x0521, 5, 29, false}, {0x0221, 38, 33, false}, {0x5601, 7, 6, true}, {0x5401, 8, 14, false},
	{0x4801, 9, 14, false}, {0x3801, 10, 14, false}, {0x3001, 11, 17, false}, {0x2401, 12, 18, false},
	{0x1C01, 13, 20, false}, {0x1601, 29, 21, false}, {0x5601, 15, 14, true}, {0x5401, 16, 14, false},
	{0x5101, 17, 15, false}, {0x4801, 18, 16, false}, {0x3801, 19, 17, false}, {0x3401, 20, 18, false},
	{0x3001, 21, 19, false}, {0x2801, 22, 19, false}, {0x2401, 23, 20, false}, {0x2201, 24, 21, false},
	{0x1C01, 25, 22, false}, {0x1801, 26, 23, false}, {0x1601, 27, 24, false}, {0x1401, 28, 25, false},
	{0x1201, 29, 26, false}, {0x1101, 30, 27, false}, {0x0AC1, 31, 28, false}, {0x09C1, 32, 29, false},
	{0x08A1, 33, 30, false}, {0x0521, 34, 31, false}, {0x0441, 35, 32, false}, {0x02A1, 36, 33, false},
	{0x0221, 37, 34, false}, {0x0141, 38, 35, false}, {0x0111, 39, 36, false}, {0x0085, 40, 37, false},
	{0x0049, 41, 38, false}, {0x0025, 42, 39, false}, {0x0015, 43, 40, false}, {0x0009, 44, 41, false},
	{0x0005, 45, 42, false}, {0x0001, 45, 43, false}, {0x5601, 46, 46, false},
}

// context state: index into qeTable << 1 | MPS.
type contexts []uint8

// mqDecoder implements INITDEC / DECODE / BYTEIN / RENORMD. Reads past the
// end of data see 0xFF bytes, so damaged input decodes garbage but always
// terminates (callers bound every loop).
type mqDecoder struct {
	data []byte
	bp   int
	c    uint32
	a    uint32
	ct   int
}

func newMQDecoder(data []byte) *mqDecoder {
	d := &mqDecoder{data: data}
	d.c = uint32(d.byteAt(0)) << 16
	d.byteIn()
	d.c <<= 7
	d.ct -= 7
	d.a = 0x8000
	return d
}

func (d *mqDecoder) byteAt(i int) byte {
	if i < len(d.data) {
		return d.data[i]
	}
	return 0xFF
}

func (d *mqDecoder) byteIn() {
	if d.byteAt(d.bp) == 0xFF {
		if d.byteAt(d.bp+1) > 0x8F {
			d.c += 0xFF00
			d.ct = 8
		} else {
			d.bp++
			d.c += uint32(d.byteAt(d.bp)) << 9
			d.ct = 7
		}
	} else {
		d.bp++
		d.c += uint32(d.byteAt(d.bp)) << 8
		d.ct = 8
	}
}

func (d *mqDecoder) decode(cx contexts, i int) int {
	st := cx[i]
	q := &qeTable[st>>1]
	mps := int(st & 1)
	d.a -= q.qe
	var bit int
	if d.c>>16 < q.qe {
		// LPS exchange
		if d.a < q.qe {
			bit = mps
			st = q.nmps<<1 | uint8(mps)
		} else {
			bit = 1 - mps
			if q.switchMPS {
				mps = bit
			}
			st = q.nlps<<1 | uint8(mps)
		}
		d.a = q.qe
	} else {
		d.c -= q.qe << 16
		if d.a&0x8000 != 0 {
			return mps
		}
		// MPS exchange
		if d.a < q.qe {
			bit = 1 - mps
			if q.switchMPS {
				mps = bit
			}
			st = q.nlps<<1 | uint8(mps)
		} else {
			bit = mps
			st = q.nmps<<1 | uint8(mps)
		}
	}
	cx[i] = st
	for {
		if d.ct == 0 {
			d.byteIn()
		}
		d.a <<= 1
		d.c <<= 1
		d.ct--
		if d.a&0x8000 != 0 {
			break
		}
	}
	return bit
}

// intDecoder is one IAx integer arithmetic decoding procedure (A.2).
type intDecoder contexts

func newIntDecoder() intDecoder { return make(intDecoder, 512) }

// decode returns the value, or ok=false for OOB.
func (cx intDecoder) decode(d *mqDecoder) (v int64, ok bool) {
	prev := 1
	bits := func(n int) int64 {
		var v int64
		for i := 0; i < n; i++ {
			b := d.decode(contexts(cx), prev)
			if prev < 256 {
				prev = prev<<1 | b
			} else {
				prev = (prev<<1|b)&511 | 256
			}
			v = v<<1 | int64(b)
		}
		return v
	}
	s := bits(1)
	switch {
	case bits(1) == 0:
		v = bits(2)
	case bits(1) == 0:
		v = bits(4) + 4
	case bits(1) == 0:
		v = bits(6) + 20
	case bits(1) == 0:
		v = bits(8) + 84
	case bits(1) == 0:
		v = bits(12) + 340
	default:
		v = bits(32) + 4436
	}
	if s == 1 {
		if v == 0 {
			return 0, false
		}
		v = -v
	}
	return v, true
}

// idDecoder is IAID (A.3) for codeLen-bit symbol IDs.
type idDecoder struct {
	cx      contexts
	codeLen int
}

func newIDDecoder(codeLen int) *idDecoder {
	return &idDecoder{cx: make(contexts, 1<<(codeLen+1)), codeLen: codeLen}
}

func (id *idDecoder) decode(d *mqDecoder) int {
	prev := 1
	for i := 0; i < id.codeLen; i++ {
		prev = prev<<1 | d.decode(id.cx, prev)
	}
	return prev - 1<<id.codeLen
}
