package content

import (
	"math"

	"p4wn/internal/pdf"
)

// pdfFunction evaluates PDF function objects (tint transforms, shading
// functions). Types: 0 (sampled), 2 (exponential), 3 (stitching).
// Type 4 (PostScript calculator) degrades to a linear ramp.
type pdfFunction struct {
	kind    int
	domain  []float64
	rng     []float64
	nOut    int
	// type 0
	samples  []float64 // normalized 0..1
	size     []int
	bps      int
	encode   []float64
	decode   []float64
	// type 2
	c0, c1 []float64
	expN   float64
	// type 3
	funcs  []*pdfFunction
	bounds []float64
	enc3   []float64
}

// loadFunction parses a function object; nil on failure.
func loadFunction(doc *pdf.Document, obj pdf.Object) *pdfFunction {
	obj = doc.Resolve(obj)
	var dict pdf.Dict
	var stm *pdf.Stream
	switch v := obj.(type) {
	case pdf.Dict:
		dict = v
	case *pdf.Stream:
		stm = v
		dict = v.Dict
	case pdf.Array:
		// array of functions (one per output component): wrap as stitch-less
		// multi-func — evaluate each for one output
		fns := make([]*pdfFunction, 0, len(v))
		for _, o := range v {
			f := loadFunction(doc, o)
			if f == nil {
				return nil
			}
			fns = append(fns, f)
		}
		if len(fns) == 0 {
			return nil
		}
		return &pdfFunction{kind: -1, funcs: fns, nOut: len(fns),
			domain: fns[0].domain}
	default:
		return nil
	}
	ft, ok := doc.GetInt(dict.Get("FunctionType"))
	if !ok {
		return nil
	}
	f := &pdfFunction{kind: int(ft)}
	f.domain = floatArray(doc, dict.Get("Domain"))
	f.rng = floatArray(doc, dict.Get("Range"))
	if len(f.domain) < 2 {
		f.domain = []float64{0, 1}
	}
	switch ft {
	case 0:
		if stm == nil {
			return nil
		}
		data, err := doc.DecodeStream(stm)
		if err != nil && len(data) == 0 {
			return nil
		}
		sizeArr := doc.GetArray(dict.Get("Size"))
		for _, o := range sizeArr {
			v, _ := doc.GetInt(o)
			f.size = append(f.size, int(v))
		}
		bps, _ := doc.GetInt(dict.Get("BitsPerSample"))
		f.bps = int(bps)
		if len(f.size) == 0 || f.bps == 0 || len(f.rng) < 2 {
			return nil
		}
		f.nOut = len(f.rng) / 2
		f.encode = floatArray(doc, dict.Get("Encode"))
		f.decode = floatArray(doc, dict.Get("Decode"))
		f.samples = unpackFunctionSamples(data, f.size, f.nOut, f.bps)
		if f.samples == nil {
			return nil
		}
	case 2:
		f.c0 = floatArray(doc, dict.Get("C0"))
		f.c1 = floatArray(doc, dict.Get("C1"))
		if len(f.c0) == 0 {
			f.c0 = []float64{0}
		}
		if len(f.c1) == 0 {
			f.c1 = []float64{1}
		}
		f.expN, _ = doc.GetFloat(dict.Get("N"))
		if f.expN == 0 {
			f.expN = 1
		}
		f.nOut = len(f.c0)
	case 3:
		fnArr := doc.GetArray(dict.Get("Functions"))
		for _, o := range fnArr {
			sub := loadFunction(doc, o)
			if sub == nil {
				return nil
			}
			f.funcs = append(f.funcs, sub)
		}
		if len(f.funcs) == 0 {
			return nil
		}
		f.bounds = floatArray(doc, dict.Get("Bounds"))
		f.enc3 = floatArray(doc, dict.Get("Encode"))
		f.nOut = f.funcs[0].nOut
	case 4:
		// PostScript calculator: not implemented; treat as identity-ish ramp
		if len(f.rng) >= 2 {
			f.nOut = len(f.rng) / 2
		} else {
			f.nOut = 1
		}
	default:
		return nil
	}
	return f
}

func floatArray(doc *pdf.Document, obj pdf.Object) []float64 {
	arr := doc.GetArray(obj)
	if arr == nil {
		return nil
	}
	out := make([]float64, len(arr))
	for i, o := range arr {
		out[i], _ = doc.GetFloat(o)
	}
	return out
}

// unpackFunctionSamples reads the big-endian packed sample grid, normalized
// to 0..1. Multi-dim grids are stored first-dimension-fastest.
func unpackFunctionSamples(data []byte, size []int, nOut, bps int) []float64 {
	total := nOut
	for _, s := range size {
		if s <= 0 || total > 1<<24 {
			return nil
		}
		total *= s
	}
	if total <= 0 || total > 1<<24 {
		return nil
	}
	max := float64(uint64(1)<<uint(bps) - 1)
	out := make([]float64, total)
	bit := 0
	for i := range out {
		v := uint64(0)
		for k := 0; k < bps; k++ {
			byteIdx := bit >> 3
			if byteIdx >= len(data) {
				return out
			}
			v = v<<1 | uint64(data[byteIdx]>>(7-bit&7)&1)
			bit++
		}
		out[i] = float64(v) / max
	}
	return out
}

// Eval evaluates the function; in is clamped to the domain.
func (f *pdfFunction) Eval(in []float64) []float64 {
	if f == nil {
		return nil
	}
	switch f.kind {
	case -1: // array of single-output functions
		out := make([]float64, 0, len(f.funcs))
		for _, sub := range f.funcs {
			v := sub.Eval(in)
			if len(v) > 0 {
				out = append(out, v[0])
			} else {
				out = append(out, 0)
			}
		}
		return out
	case 0:
		return f.evalSampled(in)
	case 2:
		x := clampDomain(in, f.domain, 0)
		out := make([]float64, f.nOut)
		xn := math.Pow(x, f.expN)
		for i := range out {
			c0, c1 := 0.0, 1.0
			if i < len(f.c0) {
				c0 = f.c0[i]
			}
			if i < len(f.c1) {
				c1 = f.c1[i]
			}
			out[i] = c0 + xn*(c1-c0)
		}
		return f.clampRange(out)
	case 3:
		x := clampDomain(in, f.domain, 0)
		k := 0
		for k < len(f.bounds) && x >= f.bounds[k] {
			k++
		}
		if k >= len(f.funcs) {
			k = len(f.funcs) - 1
		}
		lo, hi := f.domain[0], f.domain[1]
		if k > 0 {
			lo = f.bounds[k-1]
		}
		if k < len(f.bounds) {
			hi = f.bounds[k]
		}
		e0, e1 := 0.0, 1.0
		if len(f.enc3) >= (k+1)*2 {
			e0, e1 = f.enc3[k*2], f.enc3[k*2+1]
		}
		t := 0.0
		if hi > lo {
			t = (x - lo) / (hi - lo)
		}
		return f.funcs[k].Eval([]float64{e0 + t*(e1-e0)})
	case 4:
		// unimplemented calculator: linear gray ramp approximation
		x := clampDomain(in, f.domain, 0)
		out := make([]float64, f.nOut)
		for i := range out {
			out[i] = x
		}
		return f.clampRange(out)
	}
	return nil
}

// evalSampled: multilinear interpolation over the sample grid (nearest for
// dims beyond the first two to stay simple).
func (f *pdfFunction) evalSampled(in []float64) []float64 {
	m := len(f.size)
	// map inputs → encoded grid coordinates
	coords := make([]float64, m)
	for i := 0; i < m; i++ {
		x := clampDomain(in, f.domain, i)
		d0, d1 := f.domain[i*2], f.domain[i*2+1]
		e0, e1 := 0.0, float64(f.size[i]-1)
		if len(f.encode) >= (i+1)*2 {
			e0, e1 = f.encode[i*2], f.encode[i*2+1]
		}
		t := 0.0
		if d1 > d0 {
			t = (x - d0) / (d1 - d0)
		}
		c := e0 + t*(e1-e0)
		coords[i] = math.Max(0, math.Min(float64(f.size[i]-1), c))
	}
	sampleAt := func(idx []int) []float64 {
		flat := 0
		stride := 1
		for i := 0; i < m; i++ {
			flat += idx[i] * stride
			stride *= f.size[i]
		}
		base := flat * f.nOut
		out := make([]float64, f.nOut)
		for c := 0; c < f.nOut; c++ {
			if base+c < len(f.samples) {
				out[c] = f.samples[base+c]
			}
		}
		return out
	}
	// linear interp on dim 0, nearest elsewhere
	idx := make([]int, m)
	for i := 1; i < m; i++ {
		idx[i] = int(coords[i] + 0.5)
	}
	x0 := int(math.Floor(coords[0]))
	x1 := x0 + 1
	if x1 > f.size[0]-1 {
		x1 = f.size[0] - 1
	}
	fr := coords[0] - float64(x0)
	idx[0] = x0
	s0 := sampleAt(idx)
	idx[0] = x1
	s1 := sampleAt(idx)
	out := make([]float64, f.nOut)
	for c := range out {
		v := s0[c]*(1-fr) + s1[c]*fr
		// decode: map 0..1 sample into output range
		d0, d1 := 0.0, 1.0
		if len(f.decode) >= (c+1)*2 {
			d0, d1 = f.decode[c*2], f.decode[c*2+1]
		} else if len(f.rng) >= (c+1)*2 {
			d0, d1 = f.rng[c*2], f.rng[c*2+1]
		}
		out[c] = d0 + v*(d1-d0)
	}
	return f.clampRange(out)
}

func clampDomain(in, domain []float64, i int) float64 {
	x := 0.0
	if i < len(in) {
		x = in[i]
	}
	if len(domain) >= (i+1)*2 {
		if x < domain[i*2] {
			x = domain[i*2]
		}
		if x > domain[i*2+1] {
			x = domain[i*2+1]
		}
	}
	return x
}

func (f *pdfFunction) clampRange(out []float64) []float64 {
	if len(f.rng) == 0 {
		return out
	}
	for i := range out {
		if len(f.rng) >= (i+1)*2 {
			if out[i] < f.rng[i*2] {
				out[i] = f.rng[i*2]
			}
			if out[i] > f.rng[i*2+1] {
				out[i] = f.rng[i*2+1]
			}
		}
	}
	return out
}
