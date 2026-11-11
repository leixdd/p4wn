package main

// A minimal JBIG2 encoder (T.88) for test fixtures: MQ coder (Annex E.2),
// integer and symbol-ID coders (Annex A), generic regions (6.2), symbol
// dictionaries without refinement (6.5) and text regions (6.4). Written from
// the spec independently of p4wn's decoder; fixtures are cross-checked
// against poppler.

type qe struct {
	qe         uint32
	nmps, nlps uint8
	switchMPS  bool
}

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

type mqEncoder struct {
	a, c uint32
	ct   int
	buf  []byte // buf[0] is the virtual byte before the stream (BP = -1)
}

func newMQEncoder() *mqEncoder { return &mqEncoder{a: 0x8000, ct: 12, buf: []byte{0}} }

func (e *mqEncoder) encode(cx []uint8, i int, bit int) {
	st := cx[i]
	q := qeTable[st>>1]
	mps := int(st & 1)
	if bit == mps { // CODEMPS
		e.a -= q.qe
		if e.a&0x8000 == 0 {
			if e.a < q.qe {
				e.a = q.qe
			} else {
				e.c += q.qe
			}
			cx[i] = q.nmps<<1 | uint8(mps)
			e.renorm()
		} else {
			e.c += q.qe
		}
		return
	}
	e.a -= q.qe // CODELPS
	if e.a < q.qe {
		e.c += q.qe
	} else {
		e.a = q.qe
	}
	if q.switchMPS {
		mps = 1 - mps
	}
	cx[i] = q.nlps<<1 | uint8(mps)
	e.renorm()
}

func (e *mqEncoder) renorm() {
	for {
		e.a <<= 1
		e.c <<= 1
		e.ct--
		if e.ct == 0 {
			e.byteOut()
		}
		if e.a&0x8000 != 0 {
			return
		}
	}
}

func (e *mqEncoder) byteOut() {
	b := &e.buf[len(e.buf)-1]
	if *b == 0xFF {
		e.buf = append(e.buf, byte(e.c>>20))
		e.c &= 0xFFFFF
		e.ct = 7
		return
	}
	if e.c < 0x8000000 {
		e.buf = append(e.buf, byte(e.c>>19))
		e.c &= 0x7FFFF
		e.ct = 8
		return
	}
	*b++
	if *b == 0xFF {
		e.c &= 0x7FFFFFF
		e.buf = append(e.buf, byte(e.c>>20))
		e.c &= 0xFFFFF
		e.ct = 7
		return
	}
	e.buf = append(e.buf, byte(e.c>>19))
	e.c &= 0x7FFFF
	e.ct = 8
}

// finish flushes (E.2.9) and appends the FF AC end marker.
func (e *mqEncoder) finish() []byte {
	temp := e.c + e.a
	e.c |= 0xFFFF
	if e.c >= temp {
		e.c -= 0x8000
	}
	e.c <<= uint(e.ct)
	e.byteOut()
	e.c <<= uint(e.ct)
	e.byteOut()
	out := e.buf[1:]
	if n := len(out); n > 0 && out[n-1] == 0xFF {
		out = out[:n-1]
	}
	return append(out, 0xFF, 0xAC)
}

// encInt is one IAx procedure (A.2 in reverse); oob encodes OOB.
type encInt []uint8

func newEncInt() encInt { return make(encInt, 512) }

func (cx encInt) put(e *mqEncoder, v int, oob bool) {
	prev := 1
	bits := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			b := val >> i & 1
			e.encode(cx, prev, b)
			if prev < 256 {
				prev = prev<<1 | b
			} else {
				prev = (prev<<1|b)&511 | 256
			}
		}
	}
	if oob {
		bits(1, 1)
		bits(0, 1)
		bits(0, 2)
		return
	}
	s, a := 0, v
	if v < 0 {
		s, a = 1, -v
	}
	bits(s, 1)
	switch {
	case a < 4:
		bits(0, 1)
		bits(a, 2)
	case a < 20:
		bits(2, 2)
		bits(a-4, 4)
	case a < 84:
		bits(6, 3)
		bits(a-20, 6)
	case a < 340:
		bits(14, 4)
		bits(a-84, 8)
	case a < 4436:
		bits(30, 5)
		bits(a-340, 12)
	default:
		bits(31, 5)
		bits(a-4436, 32)
	}
}

func putID(e *mqEncoder, cx []uint8, id, codeLen int) {
	prev := 1
	for i := codeLen - 1; i >= 0; i-- {
		b := id >> i & 1
		e.encode(cx, prev, b)
		prev = prev<<1 | b
	}
}

// bitmap: one byte per pixel, 1 = black.
type bitmap struct {
	w, h int
	pix  []byte
}

func newBitmap(w, h int) *bitmap { return &bitmap{w, h, make([]byte, w*h)} }

func (b *bitmap) get(x, y int) int {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return 0
	}
	return int(b.pix[y*b.w+x])
}

func (b *bitmap) set(x, y int) {
	if x >= 0 && y >= 0 && x < b.w && y < b.h {
		b.pix[y*b.w+x] = 1
	}
}

var ctxSize = [4]int{1 << 16, 1 << 13, 1 << 10, 1 << 10}
var sltpCtx = [4]int{0x9B25, 0x0795, 0x00E5, 0x0195}

// genericContext follows T.88 Figures 3–6 bit by bit.
func genericContext(b *bitmap, x, y, template int, at [4][2]int) int {
	g := b.get
	switch template {
	case 0:
		return g(x-1, y) | g(x-2, y)<<1 | g(x-3, y)<<2 | g(x-4, y)<<3 | g(x+at[0][0], y+at[0][1])<<4 |
			g(x+2, y-1)<<5 | g(x+1, y-1)<<6 | g(x, y-1)<<7 | g(x-1, y-1)<<8 | g(x-2, y-1)<<9 |
			g(x+at[1][0], y+at[1][1])<<10 | g(x+at[2][0], y+at[2][1])<<11 |
			g(x+1, y-2)<<12 | g(x, y-2)<<13 | g(x-1, y-2)<<14 | g(x+at[3][0], y+at[3][1])<<15
	case 1:
		return g(x-1, y) | g(x-2, y)<<1 | g(x-3, y)<<2 | g(x+at[0][0], y+at[0][1])<<3 |
			g(x+2, y-1)<<4 | g(x+1, y-1)<<5 | g(x, y-1)<<6 | g(x-1, y-1)<<7 | g(x-2, y-1)<<8 |
			g(x+2, y-2)<<9 | g(x+1, y-2)<<10 | g(x, y-2)<<11 | g(x-1, y-2)<<12
	case 2:
		return g(x-1, y) | g(x-2, y)<<1 | g(x+at[0][0], y+at[0][1])<<2 |
			g(x+1, y-1)<<3 | g(x, y-1)<<4 | g(x-1, y-1)<<5 | g(x-2, y-1)<<6 |
			g(x+1, y-2)<<7 | g(x, y-2)<<8 | g(x-1, y-2)<<9
	default:
		return g(x-1, y) | g(x-2, y)<<1 | g(x-3, y)<<2 | g(x-4, y)<<3 | g(x+at[0][0], y+at[0][1])<<4 |
			g(x+1, y-1)<<5 | g(x, y-1)<<6 | g(x-1, y-1)<<7 | g(x-2, y-1)<<8 | g(x-3, y-1)<<9
	}
}

func rowsEqual(b *bitmap, y int) bool {
	for x := 0; x < b.w; x++ {
		if b.get(x, y) != b.get(x, y-1) {
			return false
		}
	}
	return true
}

// encodeGeneric codes b with the generic region procedure into e using cx.
func encodeGeneric(e *mqEncoder, cx []uint8, b *bitmap, template int, at [4][2]int, tpgdon bool) {
	ltp := 0
	for y := 0; y < b.h; y++ {
		if tpgdon {
			l := 0
			if rowsEqual(b, y) {
				l = 1
			}
			e.encode(cx, sltpCtx[template], l^ltp)
			ltp = l
			if ltp == 1 {
				continue
			}
		}
		for x := 0; x < b.w; x++ {
			e.encode(cx, genericContext(b, x, y, template, at), b.get(x, y))
		}
	}
}
