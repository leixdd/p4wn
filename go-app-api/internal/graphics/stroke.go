package graphics

// Line cap styles (PDF values).
const (
	CapButt   = 0
	CapRound  = 1
	CapSquare = 2
)

// Line join styles (PDF values).
const (
	JoinMiter = 0
	JoinRound = 1
	JoinBevel = 2
)

// StrokeState carries the PDF stroking parameters.
type StrokeState struct {
	LineWidth  float64
	LineCap    int
	LineJoin   int
	MiterLimit float64
	Dashes     []float64
	DashPhase  float64
}

func DefaultStroke() StrokeState {
	return StrokeState{LineWidth: 1, MiterLimit: 10}
}
