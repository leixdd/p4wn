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
  1–16 bits per component, JBIG2 (pure Go `internal/jbig2`: generic regions
  — arithmetic templates 0–3 with AT pixels and TPGDON, or MMR — plus
  arithmetic symbol dictionaries, incl. JBIG2Globals and reused coding
  contexts, and text regions; striped pages), CCITT fax (Group 4, and Group 3 1D with EOL
  codes, via `golang.org/x/image/ccitt`; honours Columns, Rows, BlackIs1,
  EncodedByteAlign), Indexed palettes, ImageMask stencils, SMask soft
  masks, color-key masking, inline images
- **Text**: embedded TrueType and CFF/OpenType fonts, bare-CFF (Type1C),
  Type0/CID fonts with Identity and the common Japanese predefined CMaps
  (Shift-JIS / EUC-JP / JIS via `golang.org/x/text`, UniJIS UCS2/UTF-*),
  horizontal and vertical writing, base-14 substitution via the Go fonts
  with PDF /Widths-driven layout, an optional runtime CJK fallback font
  (`-cjk-font`: .ttf, .otf incl. CID-keyed CFF, .ttc/.otc; not CFF2/variable,
  WOFF or Type1), all text render modes; HTML also uses ToUnicode / CMap /
  encoding maps / embedded cmap reverse-lookup for selectable text (including
  Japanese) with glyph-outline fallback and CJK system font-family fallbacks
- **Color**: DeviceRGB/Gray/CMYK, Indexed, ICCBased (via alternate),
  Separation/DeviceN with sampled/exponential/stitching tint transforms
- **Encryption**: standard security handler — RC4-40/128, AES-128, AES-256
  (empty user password)

Known degradations (never fatal):

- Shadings render as nothing; Type1 (FontFile) and Type3 fonts substitute;
  text-clip modes fill without clipping.
- JPX images are skipped.
- JBIG2 images using Huffman-coded symbol dictionaries or text regions
  (and table segments), refinement/aggregate coding, generic refinement
  regions, pattern dictionaries / halftone regions, intermediate regions or
  the extended generic template are skipped with an error naming the
  segment. Caps per stream: 64 MP per bitmap, 2×64 MP decoded in total,
  2^20 symbols, 2^20 text instances, 2^28 composited text pixels.
- CCITT: Group 3 2D (K>0), Group 3 1D without an EOL before every row, and
  Group 3 with EncodedByteAlign fill bits before the EOLs are skipped
  (x/image/ccitt limits).
- Images that fail to decode are left out of the page and reported as page
  warnings (API `warnings`, CLI stderr, fidelity harness); the other
  degradations here are silent.
- **Non-embedded CJK fonts render blank in PNG output without `-cjk-font`**
  (server and CLI flag): the Go substitute fonts have no CJK glyphs. HTML
  still emits the text when its Unicode is known.
- Type0 CMaps decoded: Identity-H/V, `Uni{JIS,GB,CNS,KS}-{UCS2,UCS2-HW,UTF16,
  UTF8,UTF32}-H/V`, `90ms-RKSJ-H/V`, `90msp-RKSJ-H/V`, `EUC-H/V`, `H`/`V`.
  Others (e.g. `78-RKSJ`, `90pv-RKSJ`, `Ext-RKSJ`, `Add-*`, `GBK-EUC`,
  `KSC-*`) and embedded CMap streams are read as Identity (an embedded
  stream's `/WMode` is kept).
- No Adobe CMap tables are embedded, so with a non-Identity CMap a two-byte
  code has no CID: its glyph is looked up by Unicode (embedded font's cmap,
  Go substitute, then `-cjk-font`), its width is `/DW`, and `/W2` does not
  apply. Only single-byte / ASCII / half-width-katakana codes get CIDs (for
  `/W`). An embedded CID-keyed CFF font with such a CMap therefore draws
  from the fallback font, not its own glyphs. x/text's Shift-JIS/EUC-JP
  decoding differs from Adobe-Japan1 Unicode for a handful of codes
  (〜/～, ‖/∥, −/－).
- Vertical writing (`-V` CMaps, WMode 1) lays out top-to-bottom with
  `/DW2`/`/W2`, but glyph substitution for vertical forms only exists on the
  `-cjk-font` path (punctuation presentation forms, ー/～ rotated); embedded
  fonts draw whatever CID/GID the PDF selects.
- HTML does not export annotations, links, or tagged reading order.

PDF TrueType subsets with broken/missing `cmap`/`post` tables are repaired so
embedded outlines (Cambria, MS-PGothic, etc.) still load by GID.

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
- `filter` — stream decode filters + predictors (incl. CCITT, JBIG2)
- `jbig2` — pure-Go JBIG2 decoder used by `filter`
- `graphics` — matrices, rects, paths, colorspaces, pixmaps
- `render` — rasterizer, stroker, draw device, HTML/SVG device, image sampling
- `font` — font loading, encodings, ToUnicode CMaps, CFF wrapping, substitution
- `content` — content-stream interpreter, images, functions, HTML assembly

The `testtools` module:

- `webui` — browser frontend to exercise the HTTP API by hand (reverse-proxies
  to the real server, so no CORS): `go run testtools/webui`, open `:3000`
- `imgdiff` — pixel-diff two PNGs (alpha-composited over white)
- `inkprofile` — structural layout comparison via ink-coverage profiles
- `fidelity` — render a directory of PDFs; report failed / near-blank pages
  and silently-degraded features per file
- `genimg`, `gentext`, `genjp`, `gencjk`, `genccitt`, `genjbig2`,
  `genlarge` — generators for the image/font/Japanese/CMap/CCITT/JBIG2/
  large-page test fixtures (`genccitt` and `genjbig2` need libtiff's
  `tiffcp`; `genjbig2` carries its own spec-based JBIG2 encoder, and its
  output is meant to be cross-checked with poppler's `pdfimages`)

Dependencies: `golang.org/x/image` (sfnt font parsing, the Go fonts used as
base-14 substitutes, CCITT decoding) and `golang.org/x/text` (Shift-JIS /
EUC-JP decoding for Japanese CMaps). Everything else is stdlib.

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
