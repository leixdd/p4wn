package render

import (
	"math"

	"p4wn/internal/graphics"
)

// applyDashes splits every subpath of pl into the "on" runs of the dash
// pattern. Resulting subpaths are open (each dash gets caps).
func applyDashes(pl *polyline, ss *graphics.StrokeState, ctm graphics.Matrix) *polyline {
	// dash array is in user space; scale to device space
	scale := ctm.Expansion()
	var pattern []float64
	total := 0.0
	for _, d := range ss.Dashes {
		v := d * scale
		if v < 0 {
			return pl // invalid pattern: draw solid
		}
		pattern = append(pattern, v)
		total += v
	}
	if total <= 1e-6 {
		return pl // all-zero pattern means solid
	}
	if len(pattern)%2 == 1 {
		pattern = append(pattern, pattern...) // odd patterns repeat doubled
		total *= 2
	}
	phase := math.Mod(ss.DashPhase*scale, total)
	if phase < 0 {
		phase += total
	}

	out := &polyline{}
	for i, pts := range pl.subpaths {
		dashSubpath(out, pts, pl.closed[i], pattern, phase)
	}
	out.flush()
	return out
}

func dashSubpath(out *polyline, pts []point, closed bool, pattern []float64, phase float64) {
	if len(pts) < 2 {
		if len(pts) == 1 {
			out.MoveTo(pts[0].x, pts[0].y)
			out.flush()
		}
		return
	}
	if closed {
		pts = append(append([]point(nil), pts...), pts[0])
	}
	// dash state: consume the phase to find the starting run
	idx := 0
	remaining := pattern[0]
	if remaining <= 0 {
		remaining = 1e-9
	}
	for p := phase; p >= remaining; {
		p -= remaining
		idx = (idx + 1) % len(pattern)
		remaining = pattern[idx]
		if remaining <= 0 {
			remaining = 1e-9 // avoid infinite loop on zero entries
		}
		if p < remaining {
			remaining -= p
			break
		}
	}
	if phase > 0 && phase < remaining {
		remaining -= phase
	}
	on := idx%2 == 0

	penDown := false
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		segLen := math.Hypot(b.x-a.x, b.y-a.y)
		if segLen < 1e-12 {
			continue
		}
		pos := 0.0
		for pos < segLen {
			step := math.Min(remaining, segLen-pos)
			t0 := pos / segLen
			t1 := (pos + step) / segLen
			x0, y0 := a.x+(b.x-a.x)*t0, a.y+(b.y-a.y)*t0
			x1, y1 := a.x+(b.x-a.x)*t1, a.y+(b.y-a.y)*t1
			if on {
				if !penDown {
					out.MoveTo(x0, y0)
					penDown = true
				}
				out.LineTo(x1, y1)
			}
			pos += step
			remaining -= step
			if remaining <= 1e-12 {
				idx = (idx + 1) % len(pattern)
				remaining = pattern[idx]
				if remaining <= 0 {
					remaining = 1e-9
				}
				on = !on
				if !on {
					penDown = false
				}
			}
		}
	}
	out.flush()
}
