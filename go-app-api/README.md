# p4wn

**p4wn** is a conversion tool. In chess, the pawn is the foundational piece — and
it can be promoted into something more powerful. Likewise, in this workflow p4wn
is the foundational tool that turns specifications and other source material into
forms that are easier to understand and work with.

A pure-Go PDF→PNG/HTML rendering service: a from-scratch PDF renderer exposed
as an async HTTP API and a CLI.

p4wn aims to convert any source material in the future. Today it only supports:

1. PDF → PNG
2. PDF → HTML

## What it renders

- **File structure**: classic xref tables, xref streams, object streams,
  incremental updates, and a repair scanner for broken files
- **Vector graphics**: fills (nonzero + even-odd), strokes (joins, caps,
  miter limits, dashes), bezier curves, scissor and arbitrary-path clipping —
  rasterized with 17×15 subpixel anti-aliasing (PNG) or emitted as SVG (HTML)
- **Images**: JPEG (DCTDecode), Flate/LZW/ASCII/RunLength raw images at
  1–16 bits per component, Indexed palettes, ImageMask stencils, SMask soft
  masks, color-key masking, inline images
- **Text**: embedded TrueType and CFF/OpenType fonts, bare-CFF (Type1C),
  Type0/CID (Identity-H) fonts, base-14 substitution via the Go fonts with
  PDF /Widths-driven layout, all text render modes; HTML also uses ToUnicode /
  encoding maps / embedded cmap reverse-lookup for selectable text (including
  Japanese) with glyph-outline fallback and CJK system font-family fallbacks
- **Color**: DeviceRGB/Gray/CMYK, Indexed, ICCBased (via alternate),
  Separation/DeviceN with sampled/exponential/stitching tint transforms
- **Encryption**: standard security handler — RC4-40/128, AES-128, AES-256
  (empty user password)

Known degradations (never fatal): shadings render as nothing, JBIG2/JPX/CCITT
images are skipped, Type1 (FontFile) and Type3 fonts substitute, text-clip
modes fill without clipping. HTML does not export annotations, links, or
tagged reading order. Type0 encodings other than Identity-H/V (e.g.
`90ms-RKSJ-H`, `UniJIS-UTF16-H`) are not decoded. PDF TrueType subsets with
broken/missing `cmap`/`post` tables are repaired so embedded outlines (Cambria,
MS-PGothic, etc.) still load by GID.

## Usage

All commands run from the repository root (the `go.work` directory, one level
above this file):

```sh
go run p4wn/cmd/p4wn -dpi 150 -pages 1-3,7 input.pdf outdir/          # PNG
go run p4wn/cmd/p4wn -format html -pages 1-3 input.pdf outdir/        # HTML
go run p4wn/cmd/p4wn-server -addr :8080 -data-dir ./data              # server
go run testtools/imgdiff a.png b.png                                        # tools
```

```
POST   /v1/jobs                      multipart "file" + dpi|scale, pages, gray, alpha, format
GET    /v1/jobs/{id}                 job status incl. per-page progress
GET    /v1/jobs/{id}/pages/{n}.png   rendered page (format=png)
GET    /v1/jobs/{id}/document.html   scrollable HTML document (format=html)
GET    /v1/jobs/{id}/archive.zip     all pages zipped / document.html
DELETE /v1/jobs/{id}
GET    /healthz
```

Jobs are processed asynchronously by a worker pool; pages within a document
render in parallel. Per-page panics and timeouts fail that page only (job
ends `partial`). Artifacts live on disk under `-data-dir` and expire after
`-ttl` (default 1h). No persistence across restarts.

## Architecture

```
bytes → lexer → objects → xref/repair → page tree
      → content interpreter (operator dispatch, gstate/text state)
      → Device interface (the interpreter↔renderer seam)
      → DrawDevice: flatten → stroke-to-outline → scanline rasterizer
        (edge lists, subpixel coverage) → premultiplied compositing
        → Pixmap → PNG
      → HTMLDevice: paths/images/text → SVG page fragments
        → AssembleHTMLDocument → document.html
```

Repository layout (a `go.work` workspace ties the modules together):

```
pdf-to-png/
├── go.work
├── go-app-api/    module "p4wn" — the renderer and service
│   ├── api/       job store, worker pool, HTTP handlers
│   ├── cmd/       p4wn (CLI) and p4wn-server binaries
│   └── internal/  pdf, filter, graphics, render, font, content
├── testdata/      committed PDF fixtures + golden PNGs
└── testtools/     module "testtools" — development utilities
```

Inside `go-app-api/internal`:

- `pdf` — object model, lexer/parser, xref machinery, decryption
- `filter` — stream decode filters + predictors
- `graphics` — matrices, rects, paths, colorspaces, pixmaps
- `render` — rasterizer, stroker, draw device, HTML/SVG device, image sampling
- `font` — font loading, encodings, ToUnicode CMaps, CFF wrapping, substitution
- `content` — content-stream interpreter, images, functions, HTML assembly

The `testtools` module:

- `webui` — browser frontend to exercise the HTTP API by hand (reverse-proxies
  to the real server, so no CORS): `go run testtools/webui`, open `:3000`
- `imgdiff` — pixel-diff two PNGs (alpha-composited over white)
- `inkprofile` — structural layout comparison via ink-coverage profiles
- `genimg`, `gentext`, `genjp` — generators for the image/font/Japanese test fixtures

The only dependency is `golang.org/x/image` (sfnt font parsing + the Go
fonts used as base-14 substitutes). Everything else is stdlib.

## Testing

```sh
go test p4wn/...                                  # unit + golden tests
go test p4wn/internal/content -run TestGolden -update  # regenerate goldens
go test p4wn/internal/pdf -fuzz FuzzOpen          # fuzz the document layer
```

Golden fixtures live in `testdata/` at the repo root; `testtools/genimg` and
`testtools/gentext` regenerate the image and embedded-font fixture PDFs.

Golden tests pin renders of committed fixtures; during development output
was cross-checked against macOS Quartz (`sips`) as an independent oracle.
