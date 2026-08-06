package content

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // DCTDecode
	_ "image/png"  // some writers embed PNG via unusual toolchains

	"p4wn/internal/filter"
	"p4wn/internal/pdf"
	"p4wn/internal/render"
)

const maxImagePixels = 64 << 20 // 64M pixels ≈ scanner A0 @ 600dpi

// imgKey abstracts full vs abbreviated (inline) dict keys.
func imgGet(doc *pdf.Document, d pdf.Dict, full, abbrev pdf.Name) pdf.Object {
	if v := d.Get(full); v != nil {
		return doc.Resolve(v)
	}
	if abbrev != "" {
		if v := d.Get(abbrev); v != nil {
			return doc.Resolve(v)
		}
	}
	return nil
}

// loadImage decodes an image XObject or inline image into a render.Image.
// isMask reports an /ImageMask stencil (paint with fill color).
func loadImage(doc *pdf.Document, resources pdf.Dict, dict pdf.Dict, data []byte, filters []pdf.Name) (img *render.Image, isMask bool, err error) {
	w, _ := pdf.ToInt(imgGet(doc, dict, "Width", "W"))
	h, _ := pdf.ToInt(imgGet(doc, dict, "Height", "H"))
	if w <= 0 || h <= 0 || w*h > maxImagePixels {
		return nil, false, fmt.Errorf("content: bad image size %dx%d", w, h)
	}
	bpc64, _ := pdf.ToInt(imgGet(doc, dict, "BitsPerComponent", "BPC"))
	bpc := int(bpc64)
	if m, ok := imgGet(doc, dict, "ImageMask", "IM").(pdf.Bool); ok && bool(m) {
		isMask = true
		bpc = 1
	}
	if bpc == 0 {
		bpc = 8
	}
	interpolate := false
	if v, ok := imgGet(doc, dict, "Interpolate", "I").(pdf.Bool); ok {
		interpolate = bool(v)
	}
	defer func() {
		if img != nil {
			img.Interpolate = interpolate
		}
	}()

	// image-codec filters take over decoding entirely
	for _, f := range filters {
		if filter.IsImageFilter(string(f)) {
			switch f {
			case "DCTDecode", "DCT":
				img, err = decodeDCT(data)
			default:
				return nil, isMask, fmt.Errorf("content: unsupported image filter %s", f)
			}
			if err != nil {
				return nil, isMask, err
			}
			applySMask(doc, resources, dict, img)
			return img, isMask, nil
		}
	}

	decode := decodeArray(doc, dict)

	if isMask {
		img, err = unpackStencil(data, int(w), int(h), decode)
		return img, true, err
	}

	cs := resolveImageColorspace(doc, resources, imgGet(doc, dict, "ColorSpace", "CS"))
	if cs == nil {
		cs = &imgColorspace{kind: csGray, nComp: 1}
	}
	img, err = unpackSamples(data, int(w), int(h), bpc, cs, decode)
	if err != nil {
		return nil, false, err
	}
	applyColorKeyMask(doc, dict, data, img, int(w), int(h), bpc, cs.nComp)
	applySMask(doc, resources, dict, img)
	return img, false, nil
}

func decodeArray(doc *pdf.Document, dict pdf.Dict) []float64 {
	arr, ok := imgGet(doc, dict, "Decode", "D").(pdf.Array)
	if !ok {
		return nil
	}
	out := make([]float64, len(arr))
	for i, o := range arr {
		out[i], _ = doc.GetFloat(o)
	}
	return out
}

// --- DCT ---------------------------------------------------------------

func decodeDCT(data []byte) (*render.Image, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("content: jpeg decode: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w*h > maxImagePixels {
		return nil, errors.New("content: jpeg too large")
	}
	switch s := src.(type) {
	case *image.Gray:
		img := &render.Image{W: w, H: h, NColor: 1,
			Samples: make([]byte, w*h)}
		for y := 0; y < h; y++ {
			copy(img.Samples[y*w:], s.Pix[y*s.Stride:y*s.Stride+w])
		}
		return img, nil
	default:
		img := &render.Image{W: w, H: h, NColor: 3,
			Samples: make([]byte, w*h*3)}
		i := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := src.At(x, y).RGBA()
				img.Samples[i] = uint8(r >> 8)
				img.Samples[i+1] = uint8(g >> 8)
				img.Samples[i+2] = uint8(bl >> 8)
				i += 3
			}
		}
		return img, nil
	}
}

// --- colorspaces for raw samples ----------------------------------------

type csKind int

const (
	csGray csKind = iota
	csRGB
	csCMYK
	csIndexed
	csSeparation
)

type imgColorspace struct {
	kind    csKind
	nComp   int    // components per source pixel
	palette []byte // indexed: rgb triples
	hival   int
}

func resolveImageColorspace(doc *pdf.Document, resources pdf.Dict, obj pdf.Object) *imgColorspace {
	return resolveImageCS(doc, resources, obj, 0)
}

func resolveImageCS(doc *pdf.Document, resources pdf.Dict, obj pdf.Object, depth int) *imgColorspace {
	if depth > maxCSDepth {
		return nil
	}
	obj = doc.Resolve(obj)
	switch v := obj.(type) {
	case pdf.Name:
		switch v {
		case "DeviceGray", "G", "CalGray", "I": // "I" only valid as Indexed inside arrays; harmless
			return &imgColorspace{kind: csGray, nComp: 1}
		case "DeviceRGB", "RGB", "CalRGB":
			return &imgColorspace{kind: csRGB, nComp: 3}
		case "DeviceCMYK", "CMYK":
			return &imgColorspace{kind: csCMYK, nComp: 4}
		}
		if resources != nil {
			if csDict := doc.GetDict(resources.Get("ColorSpace")); csDict != nil {
				if entry := csDict.Get(v); entry != nil {
					return resolveImageCS(doc, resources, entry, depth+1)
				}
			}
		}
		return nil
	case pdf.Array:
		if len(v) == 0 {
			return nil
		}
		family, _ := doc.GetName(v[0])
		switch family {
		case "DeviceGray", "G", "CalGray":
			return &imgColorspace{kind: csGray, nComp: 1}
		case "DeviceRGB", "RGB", "CalRGB", "Lab":
			return &imgColorspace{kind: csRGB, nComp: 3}
		case "DeviceCMYK", "CMYK":
			return &imgColorspace{kind: csCMYK, nComp: 4}
		case "ICCBased":
			if len(v) >= 2 {
				if stm, ok := doc.Resolve(v[1]).(*pdf.Stream); ok {
					if alt := stm.Dict.Get("Alternate"); alt != nil {
						if cs := resolveImageCS(doc, resources, alt, depth+1); cs != nil {
							return cs
						}
					}
					if n, ok := doc.GetInt(stm.Dict.Get("N")); ok {
						switch n {
						case 1:
							return &imgColorspace{kind: csGray, nComp: 1}
						case 4:
							return &imgColorspace{kind: csCMYK, nComp: 4}
						}
					}
				}
			}
			return &imgColorspace{kind: csRGB, nComp: 3}
		case "Indexed", "I":
			if len(v) < 4 {
				return nil
			}
			base := resolveImageCS(doc, resources, v[1], depth+1)
			if base == nil || base.kind == csIndexed {
				return nil
			}
			hival, _ := doc.GetInt(v[2])
			lookup := paletteBytes(doc, v[3])
			pal := buildRGBPalette(base, int(hival), lookup)
			return &imgColorspace{kind: csIndexed, nComp: 1, palette: pal, hival: int(hival)}
		case "Separation", "DeviceN":
			n := 1
			if family == "DeviceN" {
				if names, ok := doc.Resolve(v[1]).(pdf.Array); ok {
					n = max(1, len(names))
				}
			}
			return &imgColorspace{kind: csSeparation, nComp: n}
		}
	}
	return nil
}

func paletteBytes(doc *pdf.Document, obj pdf.Object) []byte {
	switch v := doc.Resolve(obj).(type) {
	case pdf.String:
		return []byte(v)
	case *pdf.Stream:
		data, err := doc.DecodeStream(v)
		if err != nil && len(data) == 0 {
			return nil
		}
		return data
	}
	return nil
}

// buildRGBPalette converts an indexed lookup table (in base-space samples)
// into rgb triples.
func buildRGBPalette(base *imgColorspace, hival int, lookup []byte) []byte {
	if hival < 0 || hival > 255 {
		hival = 255
	}
	pal := make([]byte, (hival+1)*3)
	for i := 0; i <= hival; i++ {
		o := i * base.nComp
		var r, g, b byte
		if o+base.nComp <= len(lookup) {
			switch base.kind {
			case csGray:
				r, g, b = lookup[o], lookup[o], lookup[o]
			case csRGB:
				r, g, b = lookup[o], lookup[o+1], lookup[o+2]
			case csCMYK:
				rr, gg, bb := color.CMYKToRGB(lookup[o], lookup[o+1], lookup[o+2], lookup[o+3])
				r, g, b = rr, gg, bb
			default:
				r, g, b = lookup[o], lookup[o], lookup[o]
			}
		}
		pal[i*3], pal[i*3+1], pal[i*3+2] = r, g, b
	}
	return pal
}

// --- raw sample unpacking -------------------------------------------------

// bitReader reads fixed-width big-endian samples, row-aligned to bytes.
type bitReader struct {
	data   []byte
	bitPos int
}

func (br *bitReader) read(bits int) int {
	v := 0
	for i := 0; i < bits; i++ {
		byteIdx := br.bitPos >> 3
		if byteIdx >= len(br.data) {
			return v << (bits - i)
		}
		v = v<<1 | int(br.data[byteIdx]>>(7-br.bitPos&7)&1)
		br.bitPos++
	}
	return v
}

func (br *bitReader) alignRow() { br.bitPos = (br.bitPos + 7) &^ 7 }

// unpackSamples converts raw samples into an RGB or Gray render.Image.
func unpackSamples(data []byte, w, h, bpc int, cs *imgColorspace, decode []float64) (*render.Image, error) {
	switch bpc {
	case 1, 2, 4, 8, 16:
	default:
		return nil, fmt.Errorf("content: bad BitsPerComponent %d", bpc)
	}
	maxVal := float64(int(1)<<bpc - 1)
	nc := cs.nComp

	// per-component decode mapping (defaults to [0 1] × n)
	dmin := make([]float64, nc)
	dmax := make([]float64, nc)
	for c := 0; c < nc; c++ {
		dmin[c], dmax[c] = 0, 1
		if len(decode) >= (c+1)*2 {
			dmin[c], dmax[c] = decode[c*2], decode[c*2+1]
		}
	}
	// indexed decode default is [0 hival]
	if cs.kind == csIndexed && len(decode) < 2 {
		dmin[0], dmax[0] = 0, maxVal
	} else if cs.kind == csIndexed {
		// decode maps into index range directly
	}

	outN := 3
	if cs.kind == csGray || cs.kind == csSeparation {
		outN = 1
	}
	img := &render.Image{W: w, H: h, NColor: outN,
		Samples: make([]byte, w*h*outN)}

	br := &bitReader{data: data}
	comps := make([]float64, nc)
	oi := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < nc; c++ {
				raw := float64(br.read(bpc))
				if cs.kind == csIndexed {
					// map through decode into index space
					lo, hi := dmin[c], dmax[c]
					if len(decode) < 2 {
						lo, hi = 0, maxVal
					}
					comps[c] = lo + raw*(hi-lo)/maxVal
				} else {
					comps[c] = dmin[c] + raw*(dmax[c]-dmin[c])/maxVal
				}
			}
			switch cs.kind {
			case csGray:
				img.Samples[oi] = f01to255(comps[0])
			case csSeparation:
				t := 0.0
				for _, v := range comps {
					if v > t {
						t = v
					}
				}
				img.Samples[oi] = f01to255(1 - t) // ink coverage → darkness
			case csRGB:
				img.Samples[oi] = f01to255(comps[0])
				img.Samples[oi+1] = f01to255(comps[1])
				img.Samples[oi+2] = f01to255(comps[2])
			case csCMYK:
				r, g, b := color.CMYKToRGB(f01to255(comps[0]), f01to255(comps[1]),
					f01to255(comps[2]), f01to255(comps[3]))
				img.Samples[oi], img.Samples[oi+1], img.Samples[oi+2] = r, g, b
			case csIndexed:
				idx := int(comps[0] + 0.5)
				if idx < 0 {
					idx = 0
				}
				if idx > cs.hival {
					idx = cs.hival
				}
				p := idx * 3
				if p+2 < len(cs.palette) {
					img.Samples[oi] = cs.palette[p]
					img.Samples[oi+1] = cs.palette[p+1]
					img.Samples[oi+2] = cs.palette[p+2]
				}
			}
			oi += outN
		}
		br.alignRow()
	}
	return img, nil
}

func f01to255(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// unpackStencil decodes a 1-bpc /ImageMask into an alpha image: 255 where
// paint happens. Default Decode [0 1]: sample 0 paints.
func unpackStencil(data []byte, w, h int, decode []float64) (*render.Image, error) {
	paintOnOne := len(decode) >= 2 && decode[0] > decode[1] // Decode [1 0]
	img := &render.Image{W: w, H: h, NColor: 1, Samples: make([]byte, w*h)}
	br := &bitReader{data: data}
	oi := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bit := br.read(1)
			paint := bit == 0
			if paintOnOne {
				paint = bit == 1
			}
			if paint {
				img.Samples[oi] = 255
			}
			oi++
		}
		br.alignRow()
	}
	return img, nil
}

// --- masks -----------------------------------------------------------------

// applySMask loads /SMask and attaches it as the image's alpha channel.
func applySMask(doc *pdf.Document, resources pdf.Dict, dict pdf.Dict, img *render.Image) {
	if img == nil {
		return
	}
	sm, ok := doc.Resolve(dict.Get("SMask")).(*pdf.Stream)
	if !ok {
		// /Mask may also be a stencil stream — treat its painted area as opaque=0
		return
	}
	data, err := doc.DecodeStream(sm)
	if err != nil && len(data) == 0 {
		return
	}
	names, _ := doc.StreamFilters(sm)
	mask, _, err := loadImage(doc, resources, sm.Dict, data, names)
	if err != nil || mask == nil || mask.NColor != 1 {
		return
	}
	attachAlpha(img, mask)
}

// attachAlpha resamples mask (nearest) to img's size and interleaves it as
// the alpha channel.
func attachAlpha(img, mask *render.Image) {
	n := img.NColor
	out := make([]byte, img.W*img.H*(n+1))
	mn := mask.N()
	for y := 0; y < img.H; y++ {
		my := y * mask.H / img.H
		for x := 0; x < img.W; x++ {
			mx := x * mask.W / img.W
			so := (y*img.W + x) * n
			do := (y*img.W + x) * (n + 1)
			copy(out[do:do+n], img.Samples[so:so+n])
			out[do+n] = mask.Samples[(my*mask.W+mx)*mn]
		}
	}
	img.Samples = out
	img.Alpha = true
}

// applyColorKeyMask handles /Mask given as a color-key range array: source
// pixels whose raw components all fall inside the ranges become transparent.
func applyColorKeyMask(doc *pdf.Document, dict pdf.Dict, raw []byte, img *render.Image, w, h, bpc, nc int) {
	arr, ok := doc.Resolve(dict.Get("Mask")).(pdf.Array)
	if !ok || len(arr) < nc*2 || img == nil {
		return
	}
	ranges := make([]int, nc*2)
	for i := 0; i < nc*2; i++ {
		v, _ := doc.GetInt(arr[i])
		ranges[i] = int(v)
	}
	n := img.NColor
	if !img.Alpha {
		// add an opaque alpha channel first
		out := make([]byte, w*h*(n+1))
		for i := 0; i < w*h; i++ {
			copy(out[i*(n+1):], img.Samples[i*n:i*n+n])
			out[i*(n+1)+n] = 255
		}
		img.Samples = out
		img.Alpha = true
	}
	br := &bitReader{data: raw}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			masked := true
			for c := 0; c < nc; c++ {
				v := br.read(bpc)
				if v < ranges[c*2] || v > ranges[c*2+1] {
					masked = false
				}
			}
			if masked {
				img.Samples[(y*w+x)*(n+1)+n] = 0
			}
		}
		br.alignRow()
	}
}
