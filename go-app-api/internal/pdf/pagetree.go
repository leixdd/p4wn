package pdf

import (
	"errors"
)

// Page is one resolved page: its dict plus inherited attributes.
type Page struct {
	Dict      Dict
	MediaBox  [4]float64 // x0 y0 x1 y1 in PDF user space
	CropBox   [4]float64 // intersected with MediaBox
	Rotate    int        // 0/90/180/270
	Resources Dict
	doc       *Document
}

const maxPageTreeDepth = 64

// loadPageTree flattens /Root → /Pages → /Kids into d.pages.
func (d *Document) loadPageTree() error {
	root := d.GetDict(d.trailer.Get("Root"))
	if root == nil {
		return errors.New("pdf: catalog is not a dict")
	}
	pagesObj := d.DictGet(root, "Pages")
	visited := map[Ref]bool{}
	d.pages = nil
	d.walkPages(pagesObj, visited, 0)
	if len(d.pages) == 0 {
		return errors.New("pdf: no pages found")
	}
	return nil
}

func (d *Document) walkPages(node Object, visited map[Ref]bool, depth int) {
	if depth > maxPageTreeDepth || len(d.pages) > 100000 {
		return
	}
	if ref, ok := node.(Ref); ok {
		if visited[ref] {
			return // cycle
		}
		visited[ref] = true
	}
	dict := d.GetDict(node)
	if dict == nil {
		return
	}
	typ, _ := dict.GetName("Type")
	kids := d.GetArray(dict.Get("Kids"))
	switch {
	case typ == "Page":
		d.pages = append(d.pages, dict)
	case kids != nil: // Pages node (or missing /Type)
		for _, kid := range kids {
			d.walkPages(kid, visited, depth+1)
		}
	case typ == "" && dict.Get("Contents") != nil:
		// missing /Type but looks like a page
		d.pages = append(d.pages, dict)
	}
}

// GetPage returns page number n (0-based) with inherited attributes resolved.
func (d *Document) GetPage(n int) (*Page, error) {
	if n < 0 || n >= len(d.pages) {
		return nil, errors.New("pdf: page out of range")
	}
	dict := d.pages[n]
	p := &Page{Dict: dict, doc: d}

	mb := d.inheritedArray(dict, "MediaBox")
	if box, ok := d.rectFromArray(mb); ok {
		p.MediaBox = box
	} else {
		p.MediaBox = [4]float64{0, 0, 612, 792} // US Letter default
	}
	cb := d.inheritedArray(dict, "CropBox")
	if box, ok := d.rectFromArray(cb); ok {
		p.CropBox = intersectBox(box, p.MediaBox)
		if p.CropBox[2]-p.CropBox[0] < 1 || p.CropBox[3]-p.CropBox[1] < 1 {
			p.CropBox = p.MediaBox
		}
	} else {
		p.CropBox = p.MediaBox
	}
	if r, ok := d.GetInt(d.inherited(dict, "Rotate")); ok {
		rot := int(r) % 360
		if rot < 0 {
			rot += 360
		}
		p.Rotate = (rot + 45) / 90 % 4 * 90 // snap to nearest multiple of 90
	}
	p.Resources = d.GetDict(d.inherited(dict, "Resources"))
	return p, nil
}

// inherited walks up /Parent links to find an inheritable attribute.
func (d *Document) inherited(dict Dict, key Name) Object {
	for depth := 0; dict != nil && depth < maxPageTreeDepth; depth++ {
		if v := dict.Get(key); v != nil {
			return v
		}
		dict = d.GetDict(dict.Get("Parent"))
	}
	return nil
}

func (d *Document) inheritedArray(dict Dict, key Name) Array {
	return d.GetArray(d.inherited(dict, key))
}

// rectFromArray normalizes a 4-number array to x0<=x1, y0<=y1.
func (d *Document) rectFromArray(arr Array) ([4]float64, bool) {
	if len(arr) < 4 {
		return [4]float64{}, false
	}
	var v [4]float64
	for i := 0; i < 4; i++ {
		f, ok := d.GetFloat(arr[i])
		if !ok {
			return [4]float64{}, false
		}
		v[i] = f
	}
	if v[0] > v[2] {
		v[0], v[2] = v[2], v[0]
	}
	if v[1] > v[3] {
		v[1], v[3] = v[3], v[1]
	}
	if v[2]-v[0] <= 0 || v[3]-v[1] <= 0 {
		return [4]float64{}, false
	}
	return v, true
}

func intersectBox(a, b [4]float64) [4]float64 {
	r := [4]float64{
		max64(a[0], b[0]), max64(a[1], b[1]),
		min64(a[2], b[2]), min64(a[3], b[3]),
	}
	return r
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ContentStreams returns the page's decoded content stream(s) concatenated
// with a separating newline (a /Contents array is one logical stream).
func (p *Page) ContentStreams() ([]byte, error) {
	d := p.doc
	contents := d.Resolve(p.Dict.Get("Contents"))
	var out []byte
	appendStream := func(obj Object) {
		stm, ok := d.Resolve(obj).(*Stream)
		if !ok {
			return
		}
		data, err := d.DecodeStream(stm)
		if err != nil && len(data) == 0 {
			return
		}
		out = append(out, data...)
		out = append(out, '\n')
	}
	switch c := contents.(type) {
	case *Stream:
		appendStream(c)
	case Array:
		for _, o := range c {
			appendStream(o)
		}
	}
	return out, nil
}
