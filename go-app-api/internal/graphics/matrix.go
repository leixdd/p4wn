// Package graphics holds the geometry and pixel-buffer primitives shared by
// the renderer. It knows nothing about PDF syntax.
package graphics

import "math"

// Matrix is an affine transform [a b c d e f] using the PDF row-vector
// convention:
//
//	| a b 0 |
//	| c d 0 |
//	| e f 1 |
//
// x' = a·x + c·y + e ;  y' = b·x + d·y + f
type Matrix struct {
	A, B, C, D, E, F float64
}

var Identity = Matrix{1, 0, 0, 1, 0, 0}

func Translate(tx, ty float64) Matrix { return Matrix{1, 0, 0, 1, tx, ty} }

func Scale(sx, sy float64) Matrix { return Matrix{sx, 0, 0, sy, 0, 0} }

// Rotate returns a rotation by degrees (counter-clockwise in PDF space).
func Rotate(degrees float64) Matrix {
	for degrees < 0 {
		degrees += 360
	}
	for degrees >= 360 {
		degrees -= 360
	}
	// exact values for the right angles that dominate page rotation
	switch degrees {
	case 0:
		return Identity
	case 90:
		return Matrix{0, 1, -1, 0, 0, 0}
	case 180:
		return Matrix{-1, 0, 0, -1, 0, 0}
	case 270:
		return Matrix{0, -1, 1, 0, 0, 0}
	}
	s, c := math.Sincos(degrees * math.Pi / 180)
	return Matrix{c, s, -s, c, 0, 0}
}

// Concat returns l × r: the transform "l then r".
func Concat(l, r Matrix) Matrix {
	return Matrix{
		A: l.A*r.A + l.B*r.C,
		B: l.A*r.B + l.B*r.D,
		C: l.C*r.A + l.D*r.C,
		D: l.C*r.B + l.D*r.D,
		E: l.E*r.A + l.F*r.C + r.E,
		F: l.E*r.B + l.F*r.D + r.F,
	}
}

// TransformPoint maps (x, y) through m.
func (m Matrix) TransformPoint(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// TransformVector maps a direction (ignores translation).
func (m Matrix) TransformVector(x, y float64) (float64, float64) {
	return m.A*x + m.C*y, m.B*x + m.D*y
}

// TransformRect returns the axis-aligned bounding box of the transformed rect.
func (m Matrix) TransformRect(r Rect) Rect {
	x0, y0 := m.TransformPoint(r.X0, r.Y0)
	x1, y1 := m.TransformPoint(r.X1, r.Y0)
	x2, y2 := m.TransformPoint(r.X0, r.Y1)
	x3, y3 := m.TransformPoint(r.X1, r.Y1)
	return Rect{
		X0: math.Min(math.Min(x0, x1), math.Min(x2, x3)),
		Y0: math.Min(math.Min(y0, y1), math.Min(y2, y3)),
		X1: math.Max(math.Max(x0, x1), math.Max(x2, x3)),
		Y1: math.Max(math.Max(y0, y1), math.Max(y2, y3)),
	}
}

// Det returns the determinant a·d − b·c.
func (m Matrix) Det() float64 { return m.A*m.D - m.B*m.C }

// Expansion is the average scale factor sqrt(|det|), used to convert
// device-space thresholds (flatness, min line width) into user space.
func (m Matrix) Expansion() float64 { return math.Sqrt(math.Abs(m.Det())) }

// MaxExpansion is max(|a|,|b|,|c|,|d|).
func (m Matrix) MaxExpansion() float64 {
	return math.Max(math.Max(math.Abs(m.A), math.Abs(m.B)),
		math.Max(math.Abs(m.C), math.Abs(m.D)))
}

// Invert returns the inverse matrix; ok is false when m is singular.
func (m Matrix) Invert() (Matrix, bool) {
	det := m.Det()
	if math.Abs(det) < 1e-12 {
		return Identity, false
	}
	inv := 1 / det
	r := Matrix{
		A: m.D * inv,
		B: -m.B * inv,
		C: -m.C * inv,
		D: m.A * inv,
	}
	r.E = -(m.E*r.A + m.F*r.C)
	r.F = -(m.E*r.B + m.F*r.D)
	return r, true
}

// IsRectilinear reports whether m maps axis-aligned rects to axis-aligned
// rects (only a/d or only b/c nonzero).
func (m Matrix) IsRectilinear() bool {
	const eps = 1e-9
	return (math.Abs(m.B) < eps && math.Abs(m.C) < eps) ||
		(math.Abs(m.A) < eps && math.Abs(m.D) < eps)
}
