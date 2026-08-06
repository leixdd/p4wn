package font

import (
	"strings"
	"sync"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
)

// substitute font selection: the Go font family stands in for the base-14
// (metrics come from PDF /Widths wherever present, so layout survives the
// shape substitution).

type substKey struct {
	mono, bold, italic bool
}

var (
	substMu    sync.Mutex
	substCache = map[substKey]*sfntSource{}
)

var substData = map[substKey][]byte{
	{false, false, false}: goregular.TTF,
	{false, true, false}:  gobold.TTF,
	{false, false, true}:  goitalic.TTF,
	{false, true, true}:   gobolditalic.TTF,
	{true, false, false}:  gomono.TTF,
	{true, true, false}:   gomonobold.TTF,
	{true, false, true}:   gomonoitalic.TTF,
	{true, true, true}:    gomonobolditalic.TTF,
}

// substituteSource returns a shared parsed gofont face. Concurrent use of
// sfnt.Buffer is unsafe, so each call returns a fresh source sharing the
// parsed *sfnt.Font? sfnt.Font itself is read-only; Buffer is per-source.
func substituteSource(mono, bold, italic bool) *sfntSource {
	key := substKey{mono, bold, italic}
	substMu.Lock()
	cached, ok := substCache[key]
	substMu.Unlock()
	if ok {
		// clone with a private buffer (Font is immutable, Buffer is not)
		return &sfntSource{f: cached.f, upem: cached.upem, ppem: cached.ppem}
	}
	src, errBytes, err := newSfntSource(substData[key])
	if err != nil {
		return nil // gofont data is known-good; unreachable in practice
	}
	_ = errBytes
	substMu.Lock()
	substCache[key] = src
	substMu.Unlock()
	return &sfntSource{f: src.f, upem: src.upem, ppem: src.ppem}
}

// styleFromName infers bold/italic/mono from a (Base)Font name like
// "ABCDEF+Times-BoldItalic".
func styleFromName(name string) (mono, bold, italic bool) {
	n := strings.ToLower(name)
	if i := strings.IndexByte(n, '+'); i >= 0 && i == 6 {
		n = n[7:] // strip subset prefix
	}
	bold = strings.Contains(n, "bold") || strings.Contains(n, "black") ||
		strings.Contains(n, "heavy")
	italic = strings.Contains(n, "italic") || strings.Contains(n, "oblique")
	mono = strings.Contains(n, "courier") || strings.Contains(n, "mono")
	return
}

// flags bits from /FontDescriptor /Flags
const (
	flagFixedPitch = 1 << 0
	flagSerif      = 1 << 1
	flagSymbolic   = 1 << 2
	flagItalic     = 1 << 6
	flagForceBold  = 1 << 18
)
