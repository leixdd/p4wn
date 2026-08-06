package content

import (
	"p4wn/internal/graphics"
)

// gstate is the PDF graphics state (the q/Q-stacked subset the renderer
// needs).
type gstate struct {
	ctm    graphics.Matrix
	stroke graphics.StrokeState

	fillCS      graphics.Colorspace
	fillColor   []float64
	strokeCS    graphics.Colorspace
	strokeColor []float64

	fillAlpha   float64
	strokeAlpha float64

	text textState

	clipDepth int // device clips pushed while this gstate was current
}

func newGState() gstate {
	return gstate{
		ctm:         graphics.Identity,
		stroke:      graphics.DefaultStroke(),
		fillCS:      graphics.DeviceGray,
		fillColor:   []float64{0},
		strokeCS:    graphics.DeviceGray,
		strokeColor: []float64{0},
		fillAlpha:   1,
		strokeAlpha: 1,
		text:        newTextState(),
	}
}

// clone deep-copies the slices so child edits don't alias the parent.
func (g gstate) clone() gstate {
	g.fillColor = append([]float64(nil), g.fillColor...)
	g.strokeColor = append([]float64(nil), g.strokeColor...)
	g.stroke.Dashes = append([]float64(nil), g.stroke.Dashes...)
	return g
}
