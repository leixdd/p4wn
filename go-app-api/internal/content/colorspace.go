package content

import (
	"p4wn/internal/graphics"
	"p4wn/internal/pdf"
)

// resolveColorspace turns a colorspace name or array into a
// graphics.Colorspace, consulting the page's /ColorSpace resources.
// Unknown spaces fall back by component count where possible, else nil.
func resolveColorspace(doc *pdf.Document, resources pdf.Dict, obj pdf.Object) graphics.Colorspace {
	obj = doc.Resolve(obj)
	switch v := obj.(type) {
	case pdf.Name:
		switch v {
		case "DeviceGray", "G", "CalGray":
			return graphics.DeviceGray
		case "DeviceRGB", "RGB", "CalRGB":
			return graphics.DeviceRGB
		case "DeviceCMYK", "CMYK":
			return graphics.DeviceCMYK
		case "Pattern":
			return nil // patterns handled separately (M5)
		}
		// named resource lookup
		if resources != nil {
			csDict := doc.GetDict(resources.Get("ColorSpace"))
			if csDict != nil {
				if entry := csDict.Get(v); entry != nil {
					return resolveColorspaceArray(doc, resources, doc.Resolve(entry), 0)
				}
			}
		}
		return nil
	default:
		return resolveColorspaceArray(doc, resources, obj, 0)
	}
}

const maxCSDepth = 8

func resolveColorspaceArray(doc *pdf.Document, resources pdf.Dict, obj pdf.Object, depth int) graphics.Colorspace {
	if depth > maxCSDepth {
		return nil
	}
	if n, ok := obj.(pdf.Name); ok {
		return resolveColorspace(doc, resources, n)
	}
	arr, ok := obj.(pdf.Array)
	if !ok || len(arr) == 0 {
		return nil
	}
	family, _ := doc.GetName(arr[0])
	switch family {
	case "DeviceGray", "G", "CalGray":
		return graphics.DeviceGray
	case "DeviceRGB", "RGB", "CalRGB":
		return graphics.DeviceRGB
	case "DeviceCMYK", "CMYK":
		return graphics.DeviceCMYK
	case "Lab":
		return graphics.DeviceRGB // approximation until Lab support
	case "ICCBased":
		// no ICC engine: fall back to /Alternate, else device-by-N
		if len(arr) >= 2 {
			if stm, ok := doc.Resolve(arr[1]).(*pdf.Stream); ok {
				if alt := stm.Dict.Get("Alternate"); alt != nil {
					if cs := resolveColorspaceArray(doc, resources, doc.Resolve(alt), depth+1); cs != nil {
						return cs
					}
				}
				if n, ok := doc.GetInt(stm.Dict.Get("N")); ok {
					return deviceByN(int(n))
				}
			}
		}
		return graphics.DeviceRGB
	case "Indexed", "I":
		// full palette lookup arrives with images (M3); for vector color
		// this appears rarely — approximate with the base space black
		if len(arr) >= 2 {
			if base := resolveColorspaceArray(doc, resources, doc.Resolve(arr[1]), depth+1); base != nil {
				return base
			}
		}
		return graphics.DeviceRGB
	case "Separation", "DeviceN":
		if len(arr) >= 3 {
			if alt := resolveColorspaceArray(doc, resources, doc.Resolve(arr[2]), depth+1); alt != nil {
				n := separationN(doc, arr)
				if len(arr) >= 4 {
					if fn := loadFunction(doc, arr[3]); fn != nil {
						return separationCS{alt: alt, fn: fn, n: n}
					}
				}
				return separationApprox{alt: alt, n: n}
			}
		}
		return graphics.DeviceGray
	case "Pattern":
		return nil
	}
	return nil
}

func deviceByN(n int) graphics.Colorspace {
	switch n {
	case 1:
		return graphics.DeviceGray
	case 4:
		return graphics.DeviceCMYK
	default:
		return graphics.DeviceRGB
	}
}

func separationN(doc *pdf.Document, arr pdf.Array) int {
	if names, ok := doc.Resolve(arr[1]).(pdf.Array); ok {
		return max(1, len(names))
	}
	return 1
}

// separationCS maps tint values through the /Function tint transform into
// the alternate colorspace.
type separationCS struct {
	alt graphics.Colorspace
	fn  *pdfFunction
	n   int
}

func (s separationCS) N() int       { return s.n }
func (s separationCS) Name() string { return "Separation" }
func (s separationCS) ToRGB(src []float64) (float64, float64, float64) {
	out := s.fn.Eval(src)
	if len(out) == 0 {
		return 0, 0, 0
	}
	return s.alt.ToRGB(out)
}

// separationApprox maps tint → gray-scale application of the alternate
// space's black axis: tint 0 = white, 1 = full ink (used when the tint
// transform function cannot be loaded).
type separationApprox struct {
	alt graphics.Colorspace
	n   int
}

func (s separationApprox) N() int       { return s.n }
func (s separationApprox) Name() string { return "SeparationApprox" }
func (s separationApprox) ToRGB(src []float64) (float64, float64, float64) {
	tint := 0.0
	for _, v := range src {
		if v > tint {
			tint = v
		}
	}
	v := 1 - tint // ink coverage → darkness
	return v, v, v
}
