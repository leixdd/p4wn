package jbig2

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	maxSymbols      = 1 << 20 // per dictionary / text region
	maxInstances    = 1 << 20 // symbol instances per text region
	maxComposedArea = 1 << 28 // pixels drawn by one text region

	// workBudget bounds the pixels one stream may arithmetic/MMR-decode
	// (generic regions and symbols together). Real pages need about their
	// area plus their symbols; chained hostile segments would otherwise
	// cost seconds of CPU each.
	workBudget = 2 * maxPixels
)

func spend(budget *int64, n int64) error {
	if *budget -= n; *budget < 0 {
		return errors.New("jbig2: stream decodes too many pixels")
	}
	return nil
}

// referredSymbols concatenates the exported symbols of the symbol
// dictionaries s refers to, in reference order.
func referredSymbols(s *segment, byNum map[uint32]*segment) (syms []*bitmap, last *segment, err error) {
	for _, r := range s.refs {
		ref, ok := byNum[r]
		if !ok {
			return nil, nil, fmt.Errorf("jbig2: segment %d refers to missing segment %d", s.num, r)
		}
		if ref.typ == 0 {
			syms = append(syms, ref.symbols...)
			last = ref
			if len(syms) > maxSymbols {
				return nil, nil, errors.New("jbig2: too many symbols")
			}
		}
	}
	return syms, last, nil
}

// decodeSymbolDict decodes an arithmetic-coded symbol dictionary without
// refinement/aggregation (6.5, 7.4.2) and stores its exported symbols.
func decodeSymbolDict(s *segment, byNum map[uint32]*segment, budget *int64) error {
	data := s.data
	if len(data) < 2 {
		return errors.New("jbig2: truncated symbol dictionary")
	}
	flags := binary.BigEndian.Uint16(data)
	data = data[2:]
	if flags&1 != 0 {
		return unsupported(s.typ, "Huffman coding")
	}
	if flags&2 != 0 {
		return unsupported(s.typ, "refinement/aggregate coding")
	}
	ctxUsed, ctxRetained := flags&0x100 != 0, flags&0x200 != 0
	p := genericParams{template: int(flags>>10) & 3}
	var err error
	if data, err = readAT(data, &p, p.template == 0); err != nil {
		return err
	}
	if len(data) < 8 {
		return errors.New("jbig2: truncated symbol dictionary")
	}
	numEx := int64(binary.BigEndian.Uint32(data))
	numNew := int64(binary.BigEndian.Uint32(data[4:]))
	data = data[8:]
	if numNew > maxSymbols || numEx > maxSymbols {
		return fmt.Errorf("jbig2: symbol dictionary with %d symbols", max(numNew, numEx))
	}
	input, last, err := referredSymbols(s, byNum)
	if err != nil {
		return err
	}

	gbcx := make(contexts, contextSize(p.template))
	if ctxUsed {
		if last == nil || last.gbcx == nil || last.gbp != p {
			return errors.New("jbig2: symbol dictionary reuses contexts that were not retained")
		}
		copy(gbcx, last.gbcx)
	}
	d := newMQDecoder(data)
	iadh, iadw, iaex := newIntDecoder(), newIntDecoder(), newIntDecoder()
	newSyms := make([]*bitmap, 0, numNew)
	var height, area int64
	for int64(len(newSyms)) < numNew {
		dh, ok := iadh.decode(d)
		if !ok {
			return errors.New("jbig2: bad symbol height class")
		}
		height += dh
		if height < 0 || height > maxPixels {
			return fmt.Errorf("jbig2: bad symbol height %d", height)
		}
		var width int64
		for {
			dw, ok := iadw.decode(d)
			if !ok {
				break // end of height class
			}
			width += dw
			if width < 0 || width > maxPixels || int64(len(newSyms)) >= numNew {
				return errors.New("jbig2: bad symbol width or count")
			}
			if area += width * height; area > maxPixels {
				return errors.New("jbig2: symbol dictionary too large")
			}
			if err := spend(budget, width*height); err != nil {
				return err
			}
			bm, err := decodeGeneric(d, gbcx, int(width), int(height), &p)
			if err != nil {
				return err
			}
			newSyms = append(newSyms, bm)
		}
	}

	// export flags: alternating run lengths, starting with "not exported"
	all := append(input[:len(input):len(input)], newSyms...)
	var exported []*bitmap
	export := false
	for i, rounds := 0, 0; i < len(all); rounds++ {
		run, ok := iaex.decode(d)
		if !ok || run < 0 || int64(i)+run > int64(len(all)) || rounds > 2*len(all)+2 {
			return errors.New("jbig2: bad symbol export flags")
		}
		if export {
			exported = append(exported, all[i:i+int(run)]...)
		}
		i += int(run)
		export = !export
	}
	s.symbols = exported
	if ctxRetained {
		s.gbcx, s.gbp = gbcx, p
	}
	return nil
}
