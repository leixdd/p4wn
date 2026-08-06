package content

import (
	"bytes"
	"context"
	"errors"

	"p4wn/internal/filter"
	"p4wn/internal/font"
	"p4wn/internal/graphics"
	"p4wn/internal/pdf"
	"p4wn/internal/render"
)

const (
	maxFormDepth    = 16  // nested Form XObject recursion cap
	maxSyntaxErrors = 100 // per stream before giving up
	opsPerCtxCheck  = 4096
)

// interp runs one content stream against a device.
type interp struct {
	ctx       context.Context
	doc       *pdf.Document
	dev       render.Device
	gs        gstate
	gsStack   []gstate
	resources []pdf.Dict // resource dict stack, innermost last

	path        *graphics.Path
	pendingClip int // 0 none, 1 nonzero (W), 2 even-odd (W*)

	imageCache map[*pdf.Stream]cachedImage
	fontCache  map[pdf.Ref]*font.Font

	formDepth int
	errs      int
	opCount   int
}

type cachedImage struct {
	img    *render.Image
	isMask bool
}

// Run interprets a content stream. baseGS supplies the initial gstate
// (Identity CTM at page level — the page transform is baked into the
// device).
func Run(ctx context.Context, doc *pdf.Document, resources pdf.Dict, contentBytes []byte, dev render.Device) error {
	in := &interp{
		ctx:  ctx,
		doc:  doc,
		dev:  dev,
		gs:   newGState(),
		path: &graphics.Path{},
	}
	in.resources = append(in.resources, resources)
	err := in.runStream(contentBytes)
	// balance any clips left on the device
	for in.gs.clipDepth > 0 {
		dev.PopClip()
		in.gs.clipDepth--
	}
	for i := len(in.gsStack) - 1; i >= 0; i-- {
		for c := in.gsStack[i].clipDepth; c > 0; c-- {
			dev.PopClip()
		}
	}
	return err
}

// lookupResource searches the resource stack innermost-first.
func (in *interp) lookupResource(category, name pdf.Name) pdf.Object {
	for i := len(in.resources) - 1; i >= 0; i-- {
		res := in.resources[i]
		if res == nil {
			continue
		}
		if cat := in.doc.GetDict(res.Get(category)); cat != nil {
			if v := cat.Get(name); v != nil {
				return v
			}
		}
	}
	return nil
}

func (in *interp) curResources() pdf.Dict {
	for i := len(in.resources) - 1; i >= 0; i-- {
		if in.resources[i] != nil {
			return in.resources[i]
		}
	}
	return nil
}

func (in *interp) runStream(data []byte) error {
	p := pdf.NewParser(pdf.NewLexer(data))
	var stack []pdf.Object
	for {
		obj, op, err := p.NextContentItem()
		if err != nil {
			return nil // clean EOF
		}
		if op == "" {
			if len(stack) < 32 {
				stack = append(stack, obj)
			}
			continue
		}
		in.opCount++
		if in.opCount%opsPerCtxCheck == 0 {
			if err := in.ctx.Err(); err != nil {
				return err
			}
		}
		if err := in.exec(op, stack, p); err != nil {
			if errors.Is(err, errFatal) || errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			in.errs++
			if in.errs > maxSyntaxErrors {
				return nil // too broken; render what we have
			}
		}
		stack = stack[:0]
	}
}

var errFatal = errors.New("content: fatal")

// operand helpers — tolerant: missing operands read as zero.
func fnum(stack []pdf.Object, i int) float64 {
	if i < 0 || i >= len(stack) {
		return 0
	}
	v, _ := pdf.ToFloat(stack[i])
	return v
}

func fnums(stack []pdf.Object) []float64 {
	out := make([]float64, len(stack))
	for i := range stack {
		out[i], _ = pdf.ToFloat(stack[i])
	}
	return out
}

func (in *interp) exec(op string, stack []pdf.Object, p *pdf.Parser) error {
	gs := &in.gs
	n := len(stack)
	switch op {
	// --- graphics state ---
	case "q":
		in.gsStack = append(in.gsStack, gs.clone())
		gs.clipDepth = 0
	case "Q":
		for gs.clipDepth > 0 {
			in.dev.PopClip()
			gs.clipDepth--
		}
		if len(in.gsStack) > 0 {
			in.gs = in.gsStack[len(in.gsStack)-1]
			in.gsStack = in.gsStack[:len(in.gsStack)-1]
		}
	case "cm":
		m := graphics.Matrix{A: fnum(stack, n-6), B: fnum(stack, n-5), C: fnum(stack, n-4),
			D: fnum(stack, n-3), E: fnum(stack, n-2), F: fnum(stack, n-1)}
		gs.ctm = graphics.Concat(m, gs.ctm)
	case "w":
		gs.stroke.LineWidth = fnum(stack, n-1)
	case "J":
		gs.stroke.LineCap = int(fnum(stack, n-1))
	case "j":
		gs.stroke.LineJoin = int(fnum(stack, n-1))
	case "M":
		gs.stroke.MiterLimit = fnum(stack, n-1)
	case "d":
		if n >= 2 {
			if arr, ok := stack[n-2].(pdf.Array); ok {
				gs.stroke.Dashes = gs.stroke.Dashes[:0]
				for _, o := range arr {
					v, _ := pdf.ToFloat(o)
					gs.stroke.Dashes = append(gs.stroke.Dashes, v)
				}
				gs.stroke.DashPhase = fnum(stack, n-1)
			}
		}
	case "ri", "i":
		// rendering intent / flatness: no-op
	case "gs":
		if n >= 1 {
			if name, ok := stack[n-1].(pdf.Name); ok {
				in.applyExtGState(name)
			}
		}

	// --- path construction ---
	case "m":
		in.path.MoveTo(fnum(stack, n-2), fnum(stack, n-1))
	case "l":
		in.path.LineTo(fnum(stack, n-2), fnum(stack, n-1))
	case "c":
		in.path.CurveTo(fnum(stack, n-6), fnum(stack, n-5), fnum(stack, n-4),
			fnum(stack, n-3), fnum(stack, n-2), fnum(stack, n-1))
	case "v":
		in.path.CurveToV(fnum(stack, n-4), fnum(stack, n-3), fnum(stack, n-2), fnum(stack, n-1))
	case "y":
		in.path.CurveToY(fnum(stack, n-4), fnum(stack, n-3), fnum(stack, n-2), fnum(stack, n-1))
	case "h":
		in.path.Close()
	case "re":
		in.path.Rect(fnum(stack, n-4), fnum(stack, n-3), fnum(stack, n-2), fnum(stack, n-1))

	// --- path painting ---
	case "S":
		in.paintPath(false, true, false)
	case "s":
		in.path.Close()
		in.paintPath(false, true, false)
	case "f", "F":
		in.paintPath(true, false, false)
	case "f*":
		in.paintPath(true, false, true)
	case "B":
		in.paintPath(true, true, false)
	case "B*":
		in.paintPath(true, true, true)
	case "b":
		in.path.Close()
		in.paintPath(true, true, false)
	case "b*":
		in.path.Close()
		in.paintPath(true, true, true)
	case "n":
		in.paintPath(false, false, false)

	// --- clipping ---
	case "W":
		in.pendingClip = 1
	case "W*":
		in.pendingClip = 2

	// --- color ---
	case "CS":
		if n >= 1 {
			if cs := resolveColorspace(in.doc, in.curResources(), stack[n-1]); cs != nil {
				gs.strokeCS = cs
				gs.strokeColor = initialColor(cs)
			}
		}
	case "cs":
		if n >= 1 {
			if cs := resolveColorspace(in.doc, in.curResources(), stack[n-1]); cs != nil {
				gs.fillCS = cs
				gs.fillColor = initialColor(cs)
			}
		}
	case "SC", "SCN":
		gs.strokeColor = colorOperands(stack, gs.strokeCS)
	case "sc", "scn":
		gs.fillColor = colorOperands(stack, gs.fillCS)
	case "G":
		gs.strokeCS = graphics.DeviceGray
		gs.strokeColor = []float64{fnum(stack, n - 1)}
	case "g":
		gs.fillCS = graphics.DeviceGray
		gs.fillColor = []float64{fnum(stack, n - 1)}
	case "RG":
		gs.strokeCS = graphics.DeviceRGB
		gs.strokeColor = []float64{fnum(stack, n - 3), fnum(stack, n - 2), fnum(stack, n - 1)}
	case "rg":
		gs.fillCS = graphics.DeviceRGB
		gs.fillColor = []float64{fnum(stack, n - 3), fnum(stack, n - 2), fnum(stack, n - 1)}
	case "K":
		gs.strokeCS = graphics.DeviceCMYK
		gs.strokeColor = []float64{fnum(stack, n - 4), fnum(stack, n - 3), fnum(stack, n - 2), fnum(stack, n - 1)}
	case "k":
		gs.fillCS = graphics.DeviceCMYK
		gs.fillColor = []float64{fnum(stack, n - 4), fnum(stack, n - 3), fnum(stack, n - 2), fnum(stack, n - 1)}

	// --- xobjects ---
	case "Do":
		if n >= 1 {
			if name, ok := stack[n-1].(pdf.Name); ok {
				return in.doXObject(name)
			}
		}

	// --- inline images ---
	case "BI":
		return in.inlineImage(p)

	// --- text ---
	case "BT":
		in.beginText()
	case "ET":
		// nothing to flush: glyph runs emit per show op
	case "Tc":
		gs.text.charSpace = fnum(stack, n-1)
	case "Tw":
		gs.text.wordSpace = fnum(stack, n-1)
	case "Tz":
		gs.text.scale = fnum(stack, n-1) / 100
	case "TL":
		gs.text.leading = fnum(stack, n-1)
	case "Tf":
		if n >= 2 {
			if name, ok := stack[n-2].(pdf.Name); ok {
				in.setTextFont(name, fnum(stack, n-1))
			}
		}
	case "Tr":
		gs.text.render = int(fnum(stack, n-1))
	case "Ts":
		gs.text.rise = fnum(stack, n-1)
	case "Td":
		in.textNewline(fnum(stack, n-2), fnum(stack, n-1))
	case "TD":
		gs.text.leading = -fnum(stack, n-1)
		in.textNewline(fnum(stack, n-2), fnum(stack, n-1))
	case "Tm":
		m := graphics.Matrix{A: fnum(stack, n-6), B: fnum(stack, n-5), C: fnum(stack, n-4),
			D: fnum(stack, n-3), E: fnum(stack, n-2), F: fnum(stack, n-1)}
		gs.text.tm = m
		gs.text.tlm = m
	case "T*":
		in.textNewline(0, -gs.text.leading)
	case "Tj":
		if n >= 1 {
			if s, ok := stack[n-1].(pdf.String); ok {
				in.showText([]byte(s))
			}
		}
	case "TJ":
		if n >= 1 {
			if arr, ok := stack[n-1].(pdf.Array); ok {
				in.showTextAdjusted(arr)
			}
		}
	case "'":
		in.textNewline(0, -gs.text.leading)
		if n >= 1 {
			if s, ok := stack[n-1].(pdf.String); ok {
				in.showText([]byte(s))
			}
		}
	case "\"":
		if n >= 3 {
			gs.text.wordSpace = fnum(stack, n-3)
			gs.text.charSpace = fnum(stack, n-2)
			in.textNewline(0, -gs.text.leading)
			if s, ok := stack[n-1].(pdf.String); ok {
				in.showText([]byte(s))
			}
		}

	// --- shading: degrade to nothing (M5) ---
	case "sh":

	// --- marked content / compatibility: no-ops ---
	case "BMC", "BDC", "EMC", "MP", "DP", "BX", "EX",
		"d0", "d1":

	default:
		// unknown operator: tolerated
	}
	return nil
}

// initialColor is the per-space default (all zeros = black in additive
// spaces; CMYK all-zero is white but PDF's default is 0,0,0,1 handled by
// the interpreter setting color right after cs anyway).
func initialColor(cs graphics.Colorspace) []float64 {
	c := make([]float64, cs.N())
	if cs == graphics.DeviceCMYK {
		c[3] = 1
	}
	return c
}

// colorOperands extracts up to cs.N() numeric operands (scn may carry a
// trailing pattern name — ignored here).
func colorOperands(stack []pdf.Object, cs graphics.Colorspace) []float64 {
	vals := fnums(stack)
	// drop a trailing pattern name operand if present
	if len(stack) > 0 {
		if _, isName := stack[len(stack)-1].(pdf.Name); isName {
			vals = vals[:len(vals)-1]
		}
	}
	if cs != nil && len(vals) > cs.N() {
		vals = vals[len(vals)-cs.N():]
	}
	return vals
}

// paintPath handles the shared fill/stroke/clip epilogue of painting ops.
func (in *interp) paintPath(fill, stroke, evenOdd bool) {
	gs := &in.gs
	if fill && !in.path.IsEmpty() {
		in.dev.FillPath(in.path, evenOdd, gs.ctm, gs.fillCS, gs.fillColor, gs.fillAlpha)
	}
	if stroke && !in.path.IsEmpty() {
		in.dev.StrokePath(in.path, &gs.stroke, gs.ctm, gs.strokeCS, gs.strokeColor, gs.strokeAlpha)
	}
	if in.pendingClip != 0 && !in.path.IsEmpty() {
		in.dev.ClipPath(in.path, in.pendingClip == 2, gs.ctm)
		gs.clipDepth++
	}
	in.pendingClip = 0
	in.path = &graphics.Path{}
}

// applyExtGState merges the named /ExtGState dict.
func (in *interp) applyExtGState(name pdf.Name) {
	obj := in.lookupResource("ExtGState", name)
	dict := in.doc.GetDict(obj)
	if dict == nil {
		return
	}
	gs := &in.gs
	if v, ok := in.doc.GetFloat(dict.Get("LW")); ok {
		gs.stroke.LineWidth = v
	}
	if v, ok := in.doc.GetInt(dict.Get("LC")); ok {
		gs.stroke.LineCap = int(v)
	}
	if v, ok := in.doc.GetInt(dict.Get("LJ")); ok {
		gs.stroke.LineJoin = int(v)
	}
	if v, ok := in.doc.GetFloat(dict.Get("ML")); ok {
		gs.stroke.MiterLimit = v
	}
	if v, ok := in.doc.GetFloat(dict.Get("CA")); ok {
		gs.strokeAlpha = v
	}
	if v, ok := in.doc.GetFloat(dict.Get("ca")); ok {
		gs.fillAlpha = v
	}
	if arr := in.doc.GetArray(dict.Get("D")); len(arr) == 2 {
		if dashes := in.doc.GetArray(arr[0]); dashes != nil {
			gs.stroke.Dashes = gs.stroke.Dashes[:0]
			for _, o := range dashes {
				v, _ := in.doc.GetFloat(o)
				gs.stroke.Dashes = append(gs.stroke.Dashes, v)
			}
			gs.stroke.DashPhase, _ = in.doc.GetFloat(arr[1])
		}
	}
}

// doXObject executes /Name Do — Form XObjects recurse into the interpreter;
// Image XObjects arrive in M3.
func (in *interp) doXObject(name pdf.Name) error {
	obj := in.lookupResource("XObject", name)
	stm, ok := in.doc.Resolve(obj).(*pdf.Stream)
	if !ok {
		return nil
	}
	subtype, _ := stm.Dict.GetName("Subtype")
	switch subtype {
	case "Form":
		if in.formDepth >= maxFormDepth {
			return nil
		}
		data, err := in.doc.DecodeStream(stm)
		if err != nil && len(data) == 0 {
			return nil
		}
		// gsave
		saved := in.gs.clone()
		savedClip := in.gs.clipDepth
		in.gs.clipDepth = 0

		// /Matrix maps form space into current user space
		if arr := in.doc.GetArray(stm.Dict.Get("Matrix")); len(arr) == 6 {
			var m graphics.Matrix
			m.A, _ = in.doc.GetFloat(arr[0])
			m.B, _ = in.doc.GetFloat(arr[1])
			m.C, _ = in.doc.GetFloat(arr[2])
			m.D, _ = in.doc.GetFloat(arr[3])
			m.E, _ = in.doc.GetFloat(arr[4])
			m.F, _ = in.doc.GetFloat(arr[5])
			in.gs.ctm = graphics.Concat(m, in.gs.ctm)
		}
		// clip to /BBox
		if arr := in.doc.GetArray(stm.Dict.Get("BBox")); len(arr) == 4 {
			var b [4]float64
			for i := 0; i < 4; i++ {
				b[i], _ = in.doc.GetFloat(arr[i])
			}
			clip := &graphics.Path{}
			clip.Rect(min(b[0], b[2]), min(b[1], b[3]),
				abs(b[2]-b[0]), abs(b[3]-b[1]))
			in.dev.ClipPath(clip, false, in.gs.ctm)
			in.gs.clipDepth++
		}
		in.resources = append(in.resources, in.doc.GetDict(stm.Dict.Get("Resources")))
		in.formDepth++

		err = in.runStream(data)

		in.formDepth--
		in.resources = in.resources[:len(in.resources)-1]
		for in.gs.clipDepth > 0 {
			in.dev.PopClip()
			in.gs.clipDepth--
		}
		in.gs = saved
		in.gs.clipDepth = savedClip
		return err
	case "Image":
		return in.drawImageXObject(stm)
	}
	return nil
}

// drawImageXObject decodes (with caching) and draws an image XObject.
func (in *interp) drawImageXObject(stm *pdf.Stream) error {
	entry, ok := in.imageCache[stm]
	if !ok {
		data, err := in.doc.DecodeStream(stm)
		if err != nil && len(data) == 0 {
			return nil
		}
		names, _ := in.doc.StreamFilters(stm)
		img, isMask, err := loadImage(in.doc, in.curResources(), stm.Dict, data, names)
		if err != nil {
			img = nil // degrade: draw nothing
		}
		entry = cachedImage{img: img, isMask: isMask}
		if in.imageCache == nil {
			in.imageCache = map[*pdf.Stream]cachedImage{}
		}
		in.imageCache[stm] = entry
	}
	if entry.img == nil {
		return nil
	}
	gs := &in.gs
	if entry.isMask {
		in.dev.FillImageMask(entry.img, gs.ctm, gs.fillCS, gs.fillColor, gs.fillAlpha)
	} else {
		in.dev.FillImage(entry.img, gs.ctm, gs.fillAlpha)
	}
	return nil
}

// inlineImage handles BI <dict> ID <data> EI: parse the abbreviated dict,
// slice the raw data, decode and draw.
func (in *interp) inlineImage(p *pdf.Parser) error {
	// parse the parameter dict (key/value pairs until the ID keyword)
	dict := pdf.Dict{}
	var pendingKey pdf.Name
	haveKey := false
	for {
		obj, op, err := p.NextContentItem()
		if err != nil {
			return nil
		}
		if op == "ID" {
			break
		}
		if op != "" {
			return nil // desynced: bail
		}
		if !haveKey {
			if k, ok := obj.(pdf.Name); ok {
				pendingKey = k
				haveKey = true
			}
			continue
		}
		dict[pendingKey] = obj
		haveKey = false
	}

	// raw data starts after exactly one whitespace byte
	p.FlushPushback()
	lex := p.Lexer()
	data := lex.Data()
	pos := lex.Pos()
	if pos < len(data) && isPDFWhite(data[pos]) {
		pos++
	}

	// collect filter names
	var filters []pdf.Name
	switch f := in.doc.Resolve(imgGet(in.doc, dict, "Filter", "F")).(type) {
	case pdf.Name:
		filters = []pdf.Name{f}
	case pdf.Array:
		for _, o := range f {
			if n, ok := in.doc.GetName(o); ok {
				filters = append(filters, n)
			}
		}
	}

	var raw []byte
	end := -1
	if len(filters) == 0 {
		// uncompressed: size is exactly the row-aligned sample data
		w, _ := pdf.ToInt(imgGet(in.doc, dict, "Width", "W"))
		h, _ := pdf.ToInt(imgGet(in.doc, dict, "Height", "H"))
		bpc, _ := pdf.ToInt(imgGet(in.doc, dict, "BitsPerComponent", "BPC"))
		if bpc == 0 {
			bpc = 8
		}
		if m, ok := imgGet(in.doc, dict, "ImageMask", "IM").(pdf.Bool); ok && bool(m) {
			bpc = 1
		}
		nc := int64(1)
		if cs := resolveImageColorspace(in.doc, in.curResources(), imgGet(in.doc, dict, "ColorSpace", "CS")); cs != nil {
			nc = int64(cs.nComp)
		}
		size := ((w*nc*bpc + 7) / 8) * h
		if size > 0 && pos+int(size) <= len(data) {
			raw = data[pos : pos+int(size)]
			end = pos + int(size)
		}
	}
	if raw == nil {
		// scan for the EI delimiter
		scan := pos
		for scan < len(data) {
			idx := bytes.Index(data[scan:], []byte("EI"))
			if idx < 0 {
				lex.Seek(len(data))
				return nil
			}
			at := scan + idx
			okBefore := at == 0 || isPDFWhite(data[at-1])
			okAfter := at+2 >= len(data) || isPDFWhite(data[at+2]) || isPDFDelim(data[at+2])
			if okBefore && okAfter {
				raw = data[pos:at]
				end = at
				break
			}
			scan = at + 2
		}
		if raw == nil {
			lex.Seek(len(data))
			return nil
		}
		// trim the whitespace before EI
		for len(raw) > 0 && isPDFWhite(raw[len(raw)-1]) {
			raw = raw[:len(raw)-1]
		}
	}
	// position the lexer after EI
	after := end
	for after < len(data) && isPDFWhite(data[after]) {
		after++
	}
	if after+1 < len(data) && data[after] == 'E' && data[after+1] == 'I' {
		after += 2
	}
	lex.Seek(after)

	// decode byte-stream filters (image-codec filters handled by loadImage)
	decoded := raw
	for _, f := range filters {
		if filter.IsImageFilter(string(f)) {
			break
		}
		out, err := filter.Decode(string(f), inlineFilterParams(in.doc, dict), decoded)
		if err != nil && len(out) == 0 {
			return nil
		}
		decoded = out
	}

	img, isMask, err := loadImage(in.doc, in.curResources(), dict, decoded, filters)
	if err != nil || img == nil {
		return nil
	}
	gs := &in.gs
	if isMask {
		in.dev.FillImageMask(img, gs.ctm, gs.fillCS, gs.fillColor, gs.fillAlpha)
	} else {
		in.dev.FillImage(img, gs.ctm, gs.fillAlpha)
	}
	return nil
}

func inlineFilterParams(doc *pdf.Document, dict pdf.Dict) filter.Params {
	p := filter.DefaultParams()
	dp := doc.GetDict(imgGet(doc, dict, "DecodeParms", "DP"))
	if dp == nil {
		return p
	}
	if v, ok := doc.GetInt(dp.Get("Predictor")); ok {
		p.Predictor = int(v)
	}
	if v, ok := doc.GetInt(dp.Get("Columns")); ok {
		p.Columns = int(v)
	}
	if v, ok := doc.GetInt(dp.Get("Colors")); ok {
		p.Colors = int(v)
	}
	if v, ok := doc.GetInt(dp.Get("BitsPerComponent")); ok {
		p.BitsPerComponent = int(v)
	}
	return p
}

func isPDFWhite(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isPDFDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
