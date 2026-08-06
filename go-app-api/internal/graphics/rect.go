package graphics

import "math"

// Rect is a float rectangle with X0 <= X1, Y0 <= Y1 when non-empty.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

func (r Rect) IsEmpty() bool { return r.X0 >= r.X1 || r.Y0 >= r.Y1 }

func (r Rect) Width() float64  { return r.X1 - r.X0 }
func (r Rect) Height() float64 { return r.Y1 - r.Y0 }

func (r Rect) Intersect(s Rect) Rect {
	return Rect{
		X0: math.Max(r.X0, s.X0), Y0: math.Max(r.Y0, s.Y0),
		X1: math.Min(r.X1, s.X1), Y1: math.Min(r.Y1, s.Y1),
	}
}

func (r Rect) Union(s Rect) Rect {
	if r.IsEmpty() {
		return s
	}
	if s.IsEmpty() {
		return r
	}
	return Rect{
		X0: math.Min(r.X0, s.X0), Y0: math.Min(r.Y0, s.Y0),
		X1: math.Max(r.X1, s.X1), Y1: math.Max(r.Y1, s.Y1),
	}
}

// IRect is an integer (device pixel) rectangle, half-open [X0,X1) × [Y0,Y1).
type IRect struct {
	X0, Y0, X1, Y1 int
}

func (r IRect) IsEmpty() bool { return r.X0 >= r.X1 || r.Y0 >= r.Y1 }
func (r IRect) Width() int    { return r.X1 - r.X0 }
func (r IRect) Height() int   { return r.Y1 - r.Y0 }

func (r IRect) Intersect(s IRect) IRect {
	return IRect{
		X0: maxi(r.X0, s.X0), Y0: maxi(r.Y0, s.Y0),
		X1: mini(r.X1, s.X1), Y1: mini(r.Y1, s.Y1),
	}
}

// RoundRect converts a float rect to pixels with a small epsilon so that
// rects a hair over a pixel boundary don't claim an extra row/column.
func RoundRect(r Rect) IRect {
	const eps = 0.001
	return IRect{
		X0: int(math.Floor(r.X0 + eps)),
		Y0: int(math.Floor(r.Y0 + eps)),
		X1: int(math.Ceil(r.X1 - eps)),
		Y1: int(math.Ceil(r.Y1 - eps)),
	}
}

// OuterIRect rounds outward: every touched pixel included.
func OuterIRect(r Rect) IRect {
	return IRect{
		X0: int(math.Floor(r.X0)),
		Y0: int(math.Floor(r.Y0)),
		X1: int(math.Ceil(r.X1)),
		Y1: int(math.Ceil(r.Y1)),
	}
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
