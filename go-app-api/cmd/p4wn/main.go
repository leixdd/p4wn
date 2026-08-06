// p4wn renders a PDF file to per-page PNG images or a single HTML document.
//
//	p4wn [-dpi 150] [-gray] [-alpha] [-pages 1-3,7] [-format png|html] input.pdf [outdir]
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"p4wn/api"
	"p4wn/internal/content"
	"p4wn/internal/pdf"
)

func main() {
	dpi := flag.Float64("dpi", 150, "output resolution in dots per inch (png)")
	gray := flag.Bool("gray", false, "grayscale output (png)")
	alpha := flag.Bool("alpha", false, "transparent background (png)")
	pages := flag.String("pages", "", "page selection, e.g. 1-3,7 (default all)")
	format := flag.String("format", "png", "output format: png or html")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: p4wn [flags] input.pdf [outdir]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	input := flag.Arg(0)
	outdir := "."
	if flag.NArg() > 1 {
		outdir = flag.Arg(1)
	}

	if err := run(input, outdir, *dpi, *gray, *alpha, *pages, *format); err != nil {
		fmt.Fprintln(os.Stderr, "p4wn:", err)
		os.Exit(1)
	}
}

func run(input, outdir string, dpi float64, gray, alpha bool, pageSel, format string) error {
	switch format {
	case "png", "html":
	default:
		return fmt.Errorf("invalid format %q (want png or html)", format)
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	doc, err := pdf.Open(data)
	if err != nil {
		return err
	}
	selected, err := api.ParsePageRanges(pageSel, doc.NumPages())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return err
	}

	if format == "html" {
		var pages []content.PageHTML
		for _, n := range selected {
			frag, err := content.RenderPageHTML(context.Background(), doc, n)
			if err != nil {
				return fmt.Errorf("page %d: %w", n+1, err)
			}
			pages = append(pages, frag)
			fmt.Printf("page %d  %.0fx%.0f\n", n+1, frag.Width, frag.Height)
		}
		name := filepath.Join(outdir, api.DocumentFileName)
		if err := os.WriteFile(name, []byte(content.AssembleHTMLDocument(pages)), 0o644); err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	}

	opts := content.RenderOptions{DPI: dpi, Gray: gray, Alpha: alpha}
	for _, n := range selected {
		pix, err := content.RenderPage(context.Background(), doc, n, opts)
		if err != nil {
			return fmt.Errorf("page %d: %w", n+1, err)
		}
		name := filepath.Join(outdir, fmt.Sprintf("page-%04d.png", n+1))
		f, err := os.Create(name)
		if err != nil {
			return err
		}
		err = png.Encode(f, pix.ToImage())
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		fmt.Printf("%s  %dx%d\n", name, pix.W, pix.H)
	}
	return nil
}
