package content

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"p4wn/internal/pdf"
	"p4wn/internal/render"
)

// PageHTML is one rendered page fragment for HTML assembly.
type PageHTML struct {
	PageNum int // 0-based
	Width   float64
	Height  float64
	SVG     string
	Fonts   []EmbeddedFont
}

// EmbeddedFont is a browser-usable face discovered while rendering a page.
type EmbeddedFont struct {
	Family string
	MIME   string
	Data   []byte
}

// RenderPageHTML interprets one page into an SVG fragment at 72 CSS px / PDF pt.
func RenderPageHTML(ctx context.Context, doc *pdf.Document, pageNum int) (PageHTML, error) {
	page, err := doc.GetPage(pageNum)
	if err != nil {
		return PageHTML{}, err
	}
	ctm, bbox := PageCTM(page, 72)
	if bbox.IsEmpty() {
		return PageHTML{}, fmt.Errorf("content: page %d has an empty box", pageNum+1)
	}
	w, h := float64(bbox.Width()), float64(bbox.Height())
	if err := ctx.Err(); err != nil {
		return PageHTML{}, err
	}
	data, err := page.ContentStreams()
	if err != nil {
		return PageHTML{}, err
	}
	dev := render.NewHTMLDevice(ctm, w, h)
	defer dev.Close()
	if err := Run(ctx, doc, page.Resources, data, dev); err != nil {
		return PageHTML{}, err
	}
	out := PageHTML{
		PageNum: pageNum,
		Width:   w,
		Height:  h,
		SVG:     dev.PageSVG(),
	}
	for _, f := range dev.FontFaces() {
		out.Fonts = append(out.Fonts, EmbeddedFont{Family: f.Family, MIME: f.MIME, Data: f.Data})
	}
	return out, nil
}

// AssembleHTMLDocument builds one self-contained, vertically scrollable HTML
// document from already-rendered page fragments (in display order).
func AssembleHTMLDocument(pages []PageHTML) string {
	fonts := map[string]EmbeddedFont{}
	var order []string
	for _, p := range pages {
		for _, f := range p.Fonts {
			if _, ok := fonts[f.Family]; ok {
				continue
			}
			fonts[f.Family] = f
			order = append(order, f.Family)
		}
	}

	lang := "en"
	for _, p := range pages {
		if containsCJK(p.SVG) {
			lang = "ja"
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<!DOCTYPE html>\n<html lang=\"%s\"><head><meta charset=\"utf-8\">", lang)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString("<title>PDF</title><style>")
	b.WriteString(`html,body{margin:0;padding:0;background:#e8e8e8;}`)
	b.WriteString(`.page{margin:16px auto;background:#fff;box-shadow:0 1px 4px rgba(0,0,0,.2);overflow:hidden;}`)
	b.WriteString(`.page svg{display:block;}`)
	for _, id := range order {
		f := fonts[id]
		mime := f.MIME
		if mime == "" {
			mime = "font/ttf"
		}
		fmt.Fprintf(&b, "@font-face{font-family:'%s';src:url(data:%s;base64,%s) format('%s');font-display:block;}",
			cssEscapeIdent(f.Family), mime, base64.StdEncoding.EncodeToString(f.Data), fontFormat(mime))
	}
	b.WriteString("</style></head><body>\n")
	for _, p := range pages {
		fmt.Fprintf(&b, `<section class="page" data-page="%d" style="width:%gpx;height:%gpx">%s</section>`+"\n",
			p.PageNum+1, p.Width, p.Height, p.SVG)
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func containsCJK(s string) bool {
	for _, r := range s {
		switch {
		case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
			return true
		case r >= 0x3400 && r <= 0x4DBF: // CJK Ext A
			return true
		case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified
			return true
		case r >= 0xF900 && r <= 0xFAFF: // Compatibility Ideographs
			return true
		case r >= 0xFF66 && r <= 0xFF9D: // Halfwidth Katakana
			return true
		}
	}
	return false
}

func fontFormat(mime string) string {
	switch mime {
	case "font/otf", "application/font-sfnt":
		return "opentype"
	default:
		return "truetype"
	}
}

func cssEscapeIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "\\%x ", r)
		}
	}
	return b.String()
}
