package graphics

// Colorspace converts component values (0..1) to RGB. Implementations for
// the device spaces live here; PDF-specific spaces (Indexed, ICC fallback,
// Separation) wrap these in the content layer.
type Colorspace interface {
	N() int // component count
	Name() string
	ToRGB(src []float64) (r, g, b float64)
}

type deviceGray struct{}

func (deviceGray) N() int       { return 1 }
func (deviceGray) Name() string { return "DeviceGray" }
func (deviceGray) ToRGB(src []float64) (float64, float64, float64) {
	g := clamp01(at(src, 0))
	return g, g, g
}

type deviceRGB struct{}

func (deviceRGB) N() int       { return 3 }
func (deviceRGB) Name() string { return "DeviceRGB" }
func (deviceRGB) ToRGB(src []float64) (float64, float64, float64) {
	return clamp01(at(src, 0)), clamp01(at(src, 1)), clamp01(at(src, 2))
}

type deviceCMYK struct{}

func (deviceCMYK) N() int       { return 4 }
func (deviceCMYK) Name() string { return "DeviceCMYK" }
func (deviceCMYK) ToRGB(src []float64) (float64, float64, float64) {
	c, m, y, k := clamp01(at(src, 0)), clamp01(at(src, 1)), clamp01(at(src, 2)), clamp01(at(src, 3))
	return 1 - min(1, c+k), 1 - min(1, m+k), 1 - min(1, y+k)
}

var (
	DeviceGray Colorspace = deviceGray{}
	DeviceRGB  Colorspace = deviceRGB{}
	DeviceCMYK Colorspace = deviceCMYK{}
)

func at(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// RGBToGray converts using the NTSC luma weights.
func RGBToGray(r, g, b float64) float64 {
	return 0.299*r + 0.587*g + 0.114*b
}
