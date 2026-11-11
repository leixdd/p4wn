package filter

import "p4wn/internal/jbig2"

// jbig2Decode decodes an embedded JBIG2 page to packed 1-bpc rows in PDF
// sample polarity: JBIG2's 1 = black becomes 0 = black, as for DeviceGray
// and /ImageMask (whose default Decode paints 0 samples).
func jbig2Decode(p Params, data []byte) ([]byte, error) {
	out, _, _, err := jbig2.Decode(p.JBIG2Globals, data)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i] ^= 0xFF
	}
	return out, nil
}
