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
`-page-timeout`, plus:

- `-max-dpi` (default 300): highest `dpi` (or `72·scale`) a job may request;
  above it `POST /v1/jobs` returns 400. Pixmap memory grows with dpi² — a
  Letter page at 1200 dpi needs ~1 GB.
- `-max-megapixels` (default 80): largest page, as width × height in
  millions of pixels, that is rendered; a bigger page (large formats at
  high dpi — A0 at 300 dpi is 139 MP) fails with an error naming the dpi
  that would fit, before anything is allocated. Peak memory per page is
  about 7 bytes/pixel for RGB output (pixmap + RGBA copy for PNG encoding,
  ~560 MB at 80 MP) plus up to 4 bytes/pixel per nested non-rectangular
  clip, times `-page-workers`. The CLI takes the same flag.
- `-cjk-font` (default empty): a TrueType/OpenType font or collection
  (`.ttf`, `.otf` incl. CID-keyed CFF, `.ttc`; not variable/CFF2, WOFF or
  Type1) used for CJK text whose font is not embedded; in a collection the
  first face with kana and kanji is used. Without it such text is blank in
  PNG output. Tested with macOS Hiragino (`ヒラギノ角ゴシック W3.ttc`, 7 MB;
  `ヒラギノ明朝 ProN.ttc`, 18 MB) and `Arial Unicode.ttf` (22 MB); on Debian,
  `fonts-ipaexfont` / `fonts-noto-cjk` are the usual choices (not tested
  here). The whole file is held in memory for the process lifetime. The CLI
  takes the same flag.
- `-token` (default empty = no auth): when set, every route except
  `/healthz` requires `Authorization: Bearer <token>`, else 401. The webui
  forwards one with `go run testtools/webui -token <token>`.

Job directories left in `-data-dir` by a previous process (32-hex-char
job-ID dirs only; other files and dotfiles are left alone) are removed at
startup. The server sets a 5 min whole-request read timeout (50 MB upload
at ~1.4 Mbit/s) and a 2 min idle timeout, and no write timeout so large zip
downloads are not cut off.

### Docker

```sh
docker build -t p4wn .
docker run --rm -p 8081:8081 -v p4wn-data:/data p4wn -token "$P4WN_TOKEN"
```

Static `CGO_ENABLED=0` binary on distroless `static:nonroot`, listening on
`:8081` with `-data-dir /data` (owned by the nonroot user); extra flags are
appended after the image name. Only `go-app-api/` is built (`GOWORK=off`).

### systemd

[`deploy/p4wn.service`](deploy/p4wn.service) is a sample unit for a small
host: `127.0.0.1:8081`, `-workers 1 -page-workers 2 -max-dpi 300`, state in
`/var/lib/p4wn` (`StateDirectory`), token from `/etc/p4wn.env`
(`P4WN_TOKEN=...`), `MemoryMax=1500M`, `Restart=always`. Install steps are
in the file header. The token ends up on the command line, so it is visible
to local users via `ps`.

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

Options (form field or query param): `dpi` (8–`-max-dpi`, default 150) **or**
`scale` (dpi = 72·scale); `pages` like `1-3,7,9-` (default all); `gray` and
`alpha` booleans (png); `format` = `png` (default) or `html`.

### HTML output

`format=html` produces one self-contained `document.html` with all selected
pages stacked vertically (scroll to read). Text is emitted as selectable SVG
`<text>` when a reliable Unicode mapping exists (ToUnicode / encodings /
embedded cmap); otherwise glyph outlines are used. Japanese (hiragana,
katakana, kanji) works for **Identity-H/V + ToUnicode** (or embedded font
cmap) and for the predefined `UniJIS-*`, `90ms(p)-RKSJ`, `EUC` and `H`/`V`
CMaps. The HTML `font-family` includes CJK system fallbacks. This is a
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
`failed`. A done PNG page may carry `"warnings"`: content that was left out
(e.g. an image in an unsupported codec variant). Fetching a page before it's ready returns `409` with `Retry-After`.

Limits: 50 MB upload, 500 pages/job, 20000 px max dimension, 80 MP per page
(`-max-megapixels`).

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
go test p4wn/internal/jbig2 -fuzz FuzzDecode            # fuzz JBIG2
```

Golden PNGs and fixture PDFs live in [`testdata/`](testdata/). Dev utilities
in [`testtools/`](testtools/): `webui` (browser API tester), `imgdiff` (pixel
diff), `inkprofile` (layout diff), `genimg`/`gentext`/`genjp`/`gencjk`/`genccitt`/`genjbig2`/`genlarge`
(regenerate fixtures), `fidelity` (corpus check, below).

### Fidelity harness

```sh
go run testtools/fidelity [-dpi 150] [-blank 0.001] [-cjk-font f.ttc] path/to/pdfs/
```

Renders every page of every PDF in the directory through the server's job
pool and prints one line per file: page count, failed pages with errors,
near-blank pages (ink coverage below `-blank`), page warnings (images that
failed to decode, e.g. unsupported JBIG2/CCITT variants), features p4wn
silently degrades (JPX images,
shadings, Type3/Type1 fonts, non-Identity CMaps, non-embedded fonts — found
by scanning the file), and render time; then a total. Exits 1 if any page
failed.

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
