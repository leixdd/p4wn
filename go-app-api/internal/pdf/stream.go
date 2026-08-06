package pdf

import (
	"bytes"
	"fmt"

	"p4wn/internal/filter"
)

// RawStreamData returns the still-encoded bytes of a stream, using /Length
// when it is sane and scanning for "endstream" when it is not. Encrypted
// file streams are decrypted here (ObjStm-extracted streams were decrypted
// with their container).
func (d *Document) RawStreamData(s *Stream) []byte {
	if s.Raw != nil {
		return s.Raw
	}
	off := s.Offset
	if off < 0 || off > int64(len(d.data)) {
		return nil
	}
	var raw []byte
	length, hasLen := d.GetInt(s.Dict.Get("Length"))
	if hasLen && length >= 0 && off+length <= int64(len(d.data)) && d.endstreamFollows(off+length) {
		raw = d.data[off : off+length]
	} else {
		// /Length missing or wrong: scan for the closing keyword
		idx := bytes.Index(d.data[off:], []byte("endstream"))
		if idx < 0 {
			raw = d.data[off:]
		} else {
			end := off + int64(idx)
			// strip the EOL that precedes "endstream"
			for end > off && (d.data[end-1] == '\n' || d.data[end-1] == '\r') {
				end--
			}
			raw = d.data[off:end]
		}
	}
	if d.crypt != nil {
		raw = d.decrypt(raw, s.cryptNum, s.cryptGen, d.crypt.stmMethod)
	}
	return raw
}

// endstreamFollows checks that "endstream" appears at or shortly after pos.
func (d *Document) endstreamFollows(pos int64) bool {
	p := pos
	for p < int64(len(d.data)) && p < pos+4 && isWhite(d.data[p]) {
		p++
	}
	return bytes.HasPrefix(d.data[p:], []byte("endstream"))
}

// StreamFilters returns the stream's filter chain and matching decode params.
func (d *Document) StreamFilters(s *Stream) ([]Name, []filter.Params) {
	var names []Name
	switch f := d.Resolve(s.Dict.Get("Filter")).(type) {
	case Name:
		names = []Name{f}
	case Array:
		for _, o := range f {
			if n, ok := d.Resolve(o).(Name); ok {
				names = append(names, n)
			}
		}
	}
	parms := make([]filter.Params, len(names))
	dp := d.Resolve(s.Dict.Get("DecodeParms"))
	if dp == nil {
		dp = d.Resolve(s.Dict.Get("DP"))
	}
	getParams := func(obj Object) filter.Params {
		p := filter.DefaultParams()
		pd, ok := d.Resolve(obj).(Dict)
		if !ok {
			return p
		}
		if v, ok := d.GetInt(pd.Get("Predictor")); ok {
			p.Predictor = int(v)
		}
		if v, ok := d.GetInt(pd.Get("Columns")); ok {
			p.Columns = int(v)
		}
		if v, ok := d.GetInt(pd.Get("Colors")); ok {
			p.Colors = int(v)
		}
		if v, ok := d.GetInt(pd.Get("BitsPerComponent")); ok {
			p.BitsPerComponent = int(v)
		}
		if v, ok := d.GetInt(pd.Get("EarlyChange")); ok {
			p.EarlyChange = int(v)
		}
		return p
	}
	switch dpv := dp.(type) {
	case Dict:
		if len(parms) > 0 {
			parms[0] = getParams(dpv)
			for i := 1; i < len(parms); i++ {
				parms[i] = filter.DefaultParams()
			}
		}
	case Array:
		for i := range parms {
			if i < len(dpv) {
				parms[i] = getParams(dpv[i])
			} else {
				parms[i] = filter.DefaultParams()
			}
		}
	default:
		for i := range parms {
			parms[i] = filter.DefaultParams()
		}
	}
	return names, parms
}

// DecodeStream decodes all byte-stream filters. Image-codec filters
// (DCT/JPX/CCITT/JBIG2) pass their data through untouched — the image layer
// handles those; use StreamFilters to detect them.
func (d *Document) DecodeStream(s *Stream) ([]byte, error) {
	data := d.RawStreamData(s)
	names, parms := d.StreamFilters(s)
	for i, n := range names {
		if filter.IsImageFilter(string(n)) {
			return data, nil
		}
		out, err := filter.Decode(string(n), parms[i], data)
		if err != nil {
			return out, fmt.Errorf("pdf: filter %s: %w", n, err)
		}
		data = out
	}
	return data, nil
}
