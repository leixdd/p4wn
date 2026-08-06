// Package pdf implements the PDF file/object layer: lexing, parsing,
// cross-reference resolution and the page tree. It knows nothing about
// rendering.
package pdf

import (
	"fmt"
	"math"
)

// Object is any PDF object. Concrete types: Null, Bool, Integer, Real,
// String, Name, Array, Dict, Ref, *Stream.
type Object interface{}

type Null struct{}

type Bool bool

type Integer int64

type Real float64

// String is a PDF string (binary-safe, already unescaped/hex-decoded).
type String []byte

// Name is a PDF name with #xx escapes decoded, without the leading slash.
type Name string

type Array []Object

type Dict map[Name]Object

// Ref is an unresolved indirect reference "num gen R".
type Ref struct {
	Num int
	Gen int
}

// Stream is a stream object: its dictionary plus the byte offset of the raw
// (still encoded) data within the file. Data is fetched lazily via Document.
type Stream struct {
	Dict   Dict
	Offset int64 // absolute file offset of the first data byte
	// Raw holds the encoded bytes when the stream did not come directly from
	// the file (e.g. extracted from an object stream). Nil otherwise.
	Raw []byte

	// object identity for per-object decryption (0,0 when irrelevant)
	cryptNum, cryptGen int
}

func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// --- typed accessors -------------------------------------------------------
// These operate on already-resolved objects. Document.Resolve chases Refs.

// IsNull reports whether obj is nil or Null.
func IsNull(obj Object) bool {
	if obj == nil {
		return true
	}
	_, ok := obj.(Null)
	return ok
}

// ToInt returns the object as an int64 if it is numeric.
func ToInt(obj Object) (int64, bool) {
	switch v := obj.(type) {
	case Integer:
		return int64(v), true
	case Real:
		return int64(v), true
	}
	return 0, false
}

// ToFloat returns the object as a float64 if it is numeric.
func ToFloat(obj Object) (float64, bool) {
	switch v := obj.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// DictGet returns d[key] (nil if absent). Callers resolve refs themselves
// or via Document.Resolve.
func (d Dict) Get(key Name) Object {
	if d == nil {
		return nil
	}
	return d[key]
}

// GetName returns a Name value directly stored under key.
func (d Dict) GetName(key Name) (Name, bool) {
	n, ok := d.Get(key).(Name)
	return n, ok
}

// GetInt returns an integer value directly stored under key.
func (d Dict) GetInt(key Name) (int64, bool) {
	return ToInt(d.Get(key))
}

// clampInt converts to int guarding against overflow on 32-bit platforms.
func clampInt(v int64) int {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int(v)
}
