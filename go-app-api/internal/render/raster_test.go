package render

import (
	"math"
	"testing"

	"p4wn/internal/graphics"
)

// coverageMap rasterizes into a W×H byte grid for inspection.
func coverageMap(ras *Rasterizer, evenOdd bool, w, h int) []uint8 {
	cov := make([]uint8, w*h)
	ras.Convert(evenOdd, func(y, x0 int, c []uint8) {
		if y < 0 || y >= h {
			return
		}
		copy(cov[y*w+x0:], c)
	})
	return cov
}

func TestFillAlignedRect(t *testing.T) {
	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 20, Y1: 20})
	// pixel-aligned 10×10 rect at (5,5)
	ras.InsertLine(5, 5, 15, 5)
	ras.InsertLine(15, 5, 15, 15)
	ras.InsertLine(15, 15, 5, 15)
	ras.InsertLine(5, 15, 5, 5)
	cov := coverageMap(ras, false, 20, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			inside := x >= 5 && x < 15 && y >= 5 && y < 15
			c := cov[y*20+x]
			if inside && c != 255 {
				t.Fatalf("(%d,%d) inside: coverage %d, want 255", x, y, c)
			}
			if !inside && c != 0 {
				t.Fatalf("(%d,%d) outside: coverage %d, want 0", x, y, c)
			}
		}
	}
}

func TestFillHalfPixelRect(t *testing.T) {
	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 10, Y1: 10})
	// rect covering x ∈ [2.5, 7.5], y ∈ [2, 8): edge columns half-covered
	ras.InsertLine(2.5, 2, 7.5, 2)
	ras.InsertLine(7.5, 2, 7.5, 8)
	ras.InsertLine(7.5, 8, 2.5, 8)
	ras.InsertLine(2.5, 8, 2.5, 2)
	cov := coverageMap(ras, false, 10, 10)
	c := int(cov[5*10+2]) // half-covered left column
	if c < 108 || c > 148 {
		t.Errorf("half-pixel coverage = %d, want ≈128", c)
	}
	if cov[5*10+5] != 255 {
		t.Errorf("interior coverage = %d, want 255", cov[5*10+5])
	}
}

func TestFillRules(t *testing.T) {
	// five-point star centered at (10,10): center is winding-2 region
	star := [][2]float64{{10, 2}, {14.7, 16.5}, {2.3, 7.5}, {17.7, 7.5}, {5.3, 16.5}}
	insert := func(ras *Rasterizer) {
		for i := range star {
			j := (i + 1) % len(star)
			ras.InsertLine(star[i][0], star[i][1], star[j][0], star[j][1])
		}
	}
	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 20, Y1: 20})
	insert(ras)
	nz := coverageMap(ras, false, 20, 20)
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 20, Y1: 20})
	insert(ras)
	eo := coverageMap(ras, true, 20, 20)

	center := 10*20 + 10
	if nz[center] != 255 {
		t.Errorf("nonzero center = %d, want 255 (filled)", nz[center])
	}
	if eo[center] != 0 {
		t.Errorf("even-odd center = %d, want 0 (hole)", eo[center])
	}
	// a point in one arm is filled under both rules
	arm := 4*20 + 10
	if nz[arm] == 0 || eo[arm] == 0 {
		t.Errorf("arm coverage: nz=%d eo=%d, want >0 both", nz[arm], eo[arm])
	}
}

func TestScissorClipsSpans(t *testing.T) {
	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 4, Y0: 4, X1: 8, Y1: 8})
	// big rect crossing the whole clip
	ras.InsertLine(0, 0, 20, 0)
	ras.InsertLine(20, 0, 20, 20)
	ras.InsertLine(20, 20, 0, 20)
	ras.InsertLine(0, 20, 0, 0)
	cov := coverageMap(ras, false, 20, 20)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			in := x >= 4 && x < 8 && y >= 4 && y < 8
			c := cov[y*20+x]
			if in && c != 255 {
				t.Fatalf("(%d,%d) in clip: %d", x, y, c)
			}
			if !in && c != 0 {
				t.Fatalf("(%d,%d) outside clip painted: %d", x, y, c)
			}
		}
	}
}

func TestFlattenBezierDeviation(t *testing.T) {
	// flatten a known cubic and verify every emitted segment stays close to
	// the exact curve (chord deviation bound)
	p := &graphics.Path{}
	p.MoveTo(0, 0)
	p.CurveTo(30, 60, 70, 60, 100, 0)

	type seg struct{ x0, y0, x1, y1 float64 }
	var segs []seg
	fl := newFlattener(graphics.Identity, func(x0, y0, x1, y1 float64) {
		segs = append(segs, seg{x0, y0, x1, y1})
	})
	p.Walk(fl)
	if len(segs) < 8 {
		t.Fatalf("only %d segments for a 100px-wide curve", len(segs))
	}
	// exact curve point at t
	bez := func(t float64) (float64, float64) {
		mt := 1 - t
		x := 3*mt*mt*t*30 + 3*mt*t*t*70 + t*t*t*100
		y := 3*mt*mt*t*60 + 3*mt*t*t*60
		return x, y
	}
	// sample curve densely; each sample must be within tolerance of some segment
	const tol = 0.75 // flatness 0.3 + subdivision slack
	for i := 0; i <= 200; i++ {
		tt := float64(i) / 200
		cx, cy := bez(tt)
		best := math.Inf(1)
		for _, s := range segs {
			d := pointSegDist(cx, cy, s.x0, s.y0, s.x1, s.y1)
			if d < best {
				best = d
			}
		}
		if best > tol {
			t.Fatalf("curve point t=%.3f deviates %.3f px from flattened path", tt, best)
		}
	}
}

func pointSegDist(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*dx + (py-y0)*dy) / l2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}

func TestStrokeCoversLine(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(2, 10)
	p.LineTo(18, 10)
	ss := graphics.DefaultStroke()
	ss.LineWidth = 4

	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 20, Y1: 20})
	StrokePathIntoRasterizer(ras, p, &ss, graphics.Identity)
	cov := coverageMap(ras, false, 20, 20)
	// stroke spans y ∈ [8,12); check interior and emptiness above/below
	if cov[10*20+10] != 255 {
		t.Errorf("stroke interior = %d", cov[10*20+10])
	}
	if cov[9*20+10] != 255 {
		t.Errorf("stroke row 9 = %d", cov[9*20+10])
	}
	if cov[5*20+10] != 0 || cov[15*20+10] != 0 {
		t.Errorf("outside stroke painted: %d %d", cov[5*20+10], cov[15*20+10])
	}
	// butt caps: nothing before x=2 / after x=18
	if cov[10*20+0] != 0 || cov[10*20+19] != 0 {
		t.Errorf("butt cap leaked: %d %d", cov[10*20+0], cov[10*20+19])
	}
}

func TestDashSplitsLine(t *testing.T) {
	p := &graphics.Path{}
	p.MoveTo(0, 5)
	p.LineTo(40, 5)
	ss := graphics.DefaultStroke()
	ss.LineWidth = 2
	ss.Dashes = []float64{10, 10}

	ras := NewRasterizer()
	ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 40, Y1: 10})
	StrokePathIntoRasterizer(ras, p, &ss, graphics.Identity)
	cov := coverageMap(ras, false, 40, 10)
	row := cov[5*40 : 5*40+40]
	if row[5] == 0 {
		t.Error("first dash (x=5) missing")
	}
	if row[15] != 0 {
		t.Errorf("gap (x=15) painted: %d", row[15])
	}
	if row[25] == 0 {
		t.Error("second dash (x=25) missing")
	}
	if row[35] != 0 {
		t.Errorf("gap (x=35) painted: %d", row[35])
	}
}

func TestMiterVsBevel(t *testing.T) {
	// sharp chevron: miter join must extend beyond the bevel version
	mk := func(join int, miterLimit float64) []uint8 {
		p := &graphics.Path{}
		p.MoveTo(5, 25)
		p.LineTo(15, 5)
		p.LineTo(25, 25)
		ss := graphics.DefaultStroke()
		ss.LineWidth = 4
		ss.LineJoin = join
		ss.MiterLimit = miterLimit
		ras := NewRasterizer()
		ras.Reset(graphics.IRect{X0: 0, Y0: 0, X1: 30, Y1: 30})
		StrokePathIntoRasterizer(ras, p, &ss, graphics.Identity)
		return coverageMap(ras, false, 30, 30)
	}
	miter := mk(graphics.JoinMiter, 10)
	bevel := mk(graphics.JoinBevel, 10)
	// the miter tip extends higher (smaller y) at the apex x=15
	miterTop, bevelTop := 30, 30
	for y := 0; y < 30; y++ {
		if miterTop == 30 && miter[y*30+15] > 128 {
			miterTop = y
		}
		if bevelTop == 30 && bevel[y*30+15] > 128 {
			bevelTop = y
		}
	}
	if miterTop >= bevelTop {
		t.Errorf("miter apex y=%d not above bevel apex y=%d", miterTop, bevelTop)
	}
	// tiny miter limit must fall back to bevel
	limited := mk(graphics.JoinMiter, 1.05)
	limTop := 30
	for y := 0; y < 30; y++ {
		if limited[y*30+15] > 128 {
			limTop = y
			break
		}
	}
	if limTop != bevelTop {
		t.Errorf("miter-limit fallback apex y=%d, bevel apex y=%d", limTop, bevelTop)
	}
}
