package render

import (
	"strings"
	"testing"

	"p4wn/internal/graphics"
)

func TestPathToSVG(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(0, 0)
	p.LineTo(10, 0)
	p.LineTo(10, 10)
	p.Close()
	d := pathToSVG(p, graphics.Identity)
	if !strings.Contains(d, "M0 0") || !strings.Contains(d, "L10 0") || !strings.HasSuffix(d, "Z") {
		t.Fatalf("d=%q", d)
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscapeText(`a<b>&"c`); got != `a&lt;b&gt;&amp;"c` {
		t.Fatalf("got %q", got)
	}
	if got := xmlEscapeAttr(`a"b`); got != `a&quot;b` {
		t.Fatalf("got %q", got)
	}
}

func TestCSSFontStack(t *testing.T) {
	got := cssFontStack("pdf-Mincho")
	if !strings.Contains(got, `"pdf-Mincho"`) || !strings.Contains(got, "Hiragino Sans") {
		t.Fatalf("got %q", got)
	}
	if got := cssFontStack(""); !strings.Contains(got, "Noto Sans CJK JP") {
		t.Fatalf("empty face: %q", got)
	}
}

func TestHTMLDeviceSemanticText(t *testing.T) {
	dev := NewHTMLDevice(graphics.Identity, 100, 100)
	dev.FillText(&Text{
		FontFace: "pdf-Test",
		Glyphs: []Glyph{{
			Unicode: "Hi",
			Trm:     graphics.Matrix{A: 12, D: 12, E: 10, F: 20},
		}},
	}, graphics.Identity, graphics.DeviceRGB, []float64{0, 0, 0}, 1)
	svg := dev.PageSVG()
	if !strings.Contains(svg, ">Hi</text>") {
		t.Fatalf("expected semantic text, got %s", svg)
	}
	if !strings.Contains(svg, "Hiragino Sans") {
		t.Fatalf("expected CJK font fallback stack, got %s", svg)
	}
	if strings.Contains(svg, "<path") {
		t.Fatalf("unexpected outline path: %s", svg)
	}
}

func TestHTMLDeviceOutlineFallback(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(0, 0)
	p.LineTo(1, 0)
	p.LineTo(1, 1)
	p.Close()
	dev := NewHTMLDevice(graphics.Identity, 100, 100)
	dev.FillText(&Text{
		Glyphs: []Glyph{{
			Path: p,
			Trm:  graphics.Identity,
			// no Unicode → outline
		}},
	}, graphics.Identity, graphics.DeviceRGB, []float64{1, 0, 0}, 1)
	svg := dev.PageSVG()
	if !strings.Contains(svg, "<path") || !strings.Contains(svg, `fill="#ff0000"`) {
		t.Fatalf("expected outline path, got %s", svg)
	}
	if strings.Contains(svg, "<text") {
		t.Fatalf("unexpected text: %s", svg)
	}
}

func TestHTMLDeviceStrokeTextWidthUsesUserSpace(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(0, 0)
	p.LineTo(1, 0)
	p.LineTo(1, 1)
	p.Close()
	dev := NewHTMLDevice(graphics.Identity, 200, 200)
	// Font-size scale in Trm must not inflate stroke-width (DrawDevice parity).
	dev.StrokeText(&Text{
		Glyphs: []Glyph{{
			Path: p,
			Trm:  graphics.Matrix{A: 64, D: 64, E: 10, F: 20},
		}},
	}, &graphics.StrokeState{LineWidth: 2}, graphics.Identity,
		graphics.DeviceRGB, []float64{1, 1, 1}, 1)
	svg := dev.PageSVG()
	if strings.Contains(svg, `stroke-width="128"`) {
		t.Fatalf("stroke-width incorrectly includes text matrix: %s", svg)
	}
	if !strings.Contains(svg, `stroke-width="2"`) {
		t.Fatalf("expected user-space stroke-width=2, got %s", svg)
	}
}

func TestHTMLDeviceSemanticTextUnderClip(t *testing.T) {
	dev := NewHTMLDevice(graphics.Identity, 100, 100)
	clip := &graphics.Path{}
	clip.Rect(0, 0, 100, 100)
	dev.ClipPath(clip, false, graphics.Identity)
	dev.FillText(&Text{
		FontFace: "pdf-Test",
		Glyphs: []Glyph{{
			Unicode: "あ",
			Trm:     graphics.Matrix{A: 12, D: 12, E: 10, F: 20},
		}},
	}, graphics.Identity, graphics.DeviceRGB, []float64{0, 0, 0}, 1)
	svg := dev.PageSVG()
	if !strings.Contains(svg, ">あ</text>") {
		t.Fatalf("unicode-only glyphs should stay semantic under clip: %s", svg)
	}
}

func TestSVGImageMatrix(t *testing.T) {
	// PDF image CTM after page Y-flip: maps unit square with negative D.
	pdf := graphics.Matrix{A: 34.56, D: -34.56, E: 166.32, F: 262.08}
	svg := svgImageMatrix(pdf)
	if svg.A != 34.56 || svg.D != 34.56 {
		t.Fatalf("expected positive scales, got %+v", svg)
	}
	// Same device quad: PDF (0,0)/(0,1) vs SVG (0,1)/(0,0)
	px0, py0 := pdf.TransformPoint(0, 0)
	px1, py1 := pdf.TransformPoint(0, 1)
	sx0, sy0 := svg.TransformPoint(0, 0) // SVG top → PDF top (0,1)
	sx1, sy1 := svg.TransformPoint(0, 1) // SVG bottom → PDF bottom (0,0)
	if abs(sx0-px1) > 1e-9 || abs(sy0-py1) > 1e-9 {
		t.Fatalf("SVG(0,0)=(%g,%g) want PDF(0,1)=(%g,%g)", sx0, sy0, px1, py1)
	}
	if abs(sx1-px0) > 1e-9 || abs(sy1-py0) > 1e-9 {
		t.Fatalf("SVG(0,1)=(%g,%g) want PDF(0,0)=(%g,%g)", sx1, sy1, px0, py0)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestHTMLDevicePrefersOutlineWhenPathExists(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(0, 0)
	p.LineTo(1, 0)
	p.LineTo(0.5, 1)
	p.Close()
	dev := NewHTMLDevice(graphics.Identity, 100, 100)
	dev.FillText(&Text{
		FontFace: "pdf-Test",
		Glyphs: []Glyph{{
			Unicode: "A",
			Path:    p,
			Trm:     graphics.Matrix{A: 12, D: -12, E: 10, F: 20},
		}},
	}, graphics.Identity, graphics.DeviceRGB, []float64{1, 1, 1}, 1)
	svg := dev.PageSVG()
	if strings.Contains(svg, "<text") {
		t.Fatalf("expected outline path, not semantic text: %s", svg)
	}
	if !strings.Contains(svg, "<path") {
		t.Fatalf("expected path: %s", svg)
	}
}
