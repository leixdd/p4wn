# p4wn

**p4wn** is a conversion tool. In chess, the pawn is the foundational piece — and
it can be promoted into something more powerful. Likewise, in this workflow p4wn
is the foundational tool that turns specifications and other source material into
forms that are easier to understand and work with.

Pure-Go PDF→PNG/HTML service. This file covers running the API and testing it —
see [`go-app-api/README.md`](go-app-api/README.md) for architecture and
rendering details.

## Run the server

```sh
go run p4wn/cmd/p4wn-server -addr :8080 -data-dir ./data
```

Flags: `-ttl` (job retention, default 1h), `-workers`, `-page-workers`,
`-page-timeout`.

## API

Async job model: upload a PDF, poll for status, fetch PNG pages or a single
HTML document.

```
POST   /v1/jobs                      multipart "file" + dpi|scale, pages, gray, alpha, format
GET    /v1/jobs/{id}                 status + per-page progress (+ document_url for html)
GET    /v1/jobs/{id}/pages/{n}.png   one rendered page (format=png)
GET    /v1/jobs/{id}/document.html   self-contained scrollable HTML (format=html)
GET    /v1/jobs/{id}/archive.zip     all pages zipped, or document.html (when done)
DELETE /v1/jobs/{id}
GET    /healthz
```

Options (form field or query param): `dpi` (8–1200, default 150) **or**
`scale` (dpi = 72·scale); `pages` like `1-3,7,9-` (default all); `gray` and
`alpha` booleans (png); `format` = `png` (default) or `html`.

### HTML output

`format=html` produces one self-contained `document.html` with all selected
pages stacked vertically (scroll to read). Text is emitted as selectable SVG
`<text>` when a reliable Unicode mapping exists (ToUnicode / encodings /
embedded cmap); otherwise glyph outlines are used. Japanese (hiragana,
katakana, kanji) works for the common **Identity-H + ToUnicode** (or embedded
font cmap) path; older CMaps such as `90ms-RKSJ-H` / `UniJIS-UTF16-H` are not
supported yet. The HTML `font-family` includes CJK system fallbacks. This is a
visual layout conversion — not tagged-PDF reading order or link/annotation
export.

### Example

```sh
# submit (PNG)
curl -s -F file=@doc.pdf "localhost:8080/v1/jobs?dpi=150&pages=1-3"
# → {"id":"a1b2...","status":"queued","status_url":"/v1/jobs/a1b2..."}

# submit (HTML)
curl -s -F file=@doc.pdf "localhost:8080/v1/jobs?format=html&pages=1-3"

# poll
curl -s localhost:8080/v1/jobs/a1b2...
# → {"status":"done","pages_done":3,"pages":[{"page":1,"status":"done","url":"..."}]}
# HTML jobs include "document_url":"/v1/jobs/.../document.html"

# fetch
curl -o page1.png localhost:8080/v1/jobs/a1b2.../pages/1.png
curl -o doc.html  localhost:8080/v1/jobs/a1b2.../document.html
curl -o all.zip   localhost:8080/v1/jobs/a1b2.../archive.zip
```

Status values: `queued`, `processing`, `done`, `partial` (some pages failed),
`failed`. Fetching a page before it's ready returns `409` with `Retry-After`.

Limits: 50 MB upload, 500 pages/job, 20000 px max dimension.

## CLI (no server)

```sh
go run p4wn/cmd/p4wn -dpi 150 -pages 1-3,7 input.pdf outdir/
go run p4wn/cmd/p4wn -format html -pages 1-3 input.pdf outdir/   # writes document.html
```

## Test

```sh
go test p4wn/...                                        # unit + golden tests
go test p4wn/internal/content -run TestGolden -update   # regenerate goldens
go test p4wn/internal/pdf  -fuzz FuzzOpen               # fuzz document layer
go test p4wn/internal/filter -fuzz FuzzDecode           # fuzz filters
```

Golden PNGs and fixture PDFs live in [`testdata/`](testdata/). Dev utilities
in [`testtools/`](testtools/): `webui` (browser API tester), `imgdiff` (pixel
diff), `inkprofile` (layout diff), `genimg`/`gentext` (regenerate fixtures).

### Browser tester

With the server running, launch the UI (a reverse proxy, so it hits the real
routes without CORS) and click through upload → poll → view → zip → delete:

```sh
go run testtools/webui          # serves http://localhost:3000, proxies to :8080
```

The UI has a `format` selector; HTML jobs preview `document.html` in an iframe.

### End-to-end smoke

```sh
go run p4wn/cmd/p4wn-server -addr :8080 -data-dir /tmp/p4wn &
ID=$(curl -s -F file=@testdata/pdfs/vec.pdf localhost:8080/v1/jobs | \
     sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
sleep 1
curl -s -o out.png localhost:8080/v1/jobs/$ID/pages/1.png && file out.png

ID=$(curl -s -F file=@testdata/pdfs/vec.pdf "localhost:8080/v1/jobs?format=html" | \
     sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
sleep 1
curl -s -o out.html localhost:8080/v1/jobs/$ID/document.html && head -c 80 out.html
```
