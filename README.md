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
go run p4wn/cmd/p4wn-server -addr :8081 -data-dir ./data
```

Flags: `-ttl` (job retention, default 1h), `-page-timeout` (per-page render
timeout, default 60s), plus:

- `-workers` (default `max(1, CPUs/2)`; env `P4WN_WORKERS` when the flag is
  not given): jobs converted at the same time, 1–256; further jobs queue.
  An invalid value stops the server at startup.
- `-page-workers` (default 4; env `P4WN_PAGE_WORKERS` when the flag is not
  given): pages of one job rendered at the same time, 1–256. An invalid
  value stops the server at startup.
- `-max-dpi` (default 300): highest `dpi` (or `72·scale`) a job may request;
  above it `POST /v1/jobs` returns 400. Pixmap memory grows with dpi² — a
  Letter page at 1200 dpi needs ~1 GB.
- `-max-megapixels` (default 80): largest page, as width × height in
  millions of pixels, that is rendered; a bigger page (large formats at
  high dpi — A0 at 300 dpi is 139 MP) fails with an error naming the dpi
  that would fit, before anything is allocated. Peak memory per page is
  about 7 bytes/pixel for RGB output (pixmap + RGBA copy for PNG encoding,
  ~560 MB at 80 MP) plus up to 4 bytes/pixel per nested non-rectangular
  clip, times `-page-workers`. The server falls back to
  `P4WN_MAX_MEGAPIXELS` when the flag is not given; an invalid value (not
  in (0, 400]) stops it at startup. The CLI takes the same flag (no env).
- `-cjk-font` (default empty): a TrueType/OpenType font or collection
  (`.ttf`, `.otf` incl. CID-keyed CFF, `.ttc`; not variable/CFF2, WOFF or
  Type1) used for CJK text whose font is not embedded; in a collection the
  first face with kana and kanji is used. Without it such text is blank in
  PNG output. Tested with macOS Hiragino (`ヒラギノ角ゴシック W3.ttc`, 7 MB;
  `ヒラギノ明朝 ProN.ttc`, 18 MB) and `Arial Unicode.ttf` (22 MB); on Debian,
  `fonts-ipaexfont` / `fonts-noto-cjk` are the usual choices (not tested
  here). The whole file is held in memory for the process lifetime. The CLI
  takes the same flag. The server falls back to `P4WN_CJK_FONT` when the
  flag is not given.
- `-token` (default empty = no auth): when set, every route except
  `/healthz` requires `Authorization: Bearer <token>`, else 401. The webui
  forwards one with `go run testtools/webui -token <token>`. The server
  falls back to `P4WN_TOKEN` when the flag is not given (keeps it out of
  `ps`).
- `-max-upload-mb` (default 100; env `P4WN_MAX_UPLOAD_MB` when the flag is
  not given): largest PDF `POST /v1/jobs` accepts, in whole MB (2^20 bytes),
  1–1024; the multipart framing around the file does not count. A bigger
  upload gets `413` (`upload too large (limit N MB)`); an invalid value stops
  the server at startup. The whole-request read timeout follows it (see
  below). Memory: file parts above 4 MB are spooled to a temp file while the
  request is parsed, but a running job holds its whole PDF in memory, so
  each of `-workers` concurrent jobs needs up to the limit on top of its
  page pixmaps; disk needs about twice the limit per upload in flight
  (temp file + the job's copy). A proxy in front of the server needs its
  own body-size limit and timeouts raised to match.
- `-max-pages` (default 1000; env `P4WN_MAX_PAGES` when the flag is not
  given): most pages one job renders, 1–10000. A job whose page selection
  (all pages unless `pages` is given) is longer fails before anything is
  rendered (`selection has N pages, limit is M`); a job's own `max_pages`
  can only lower it. An invalid value stops the server at startup. Pages
  render one after another per job (`-page-workers` at a time), so memory
  does not grow with the page count, but every page keeps a status entry
  (~150 bytes in `GET /v1/jobs/{id}`) and its PNG stays in `-data-dir`
  until the job expires (`-ttl`): at 150 dpi a page is typically
  0.05–0.5 MB, so a 1000-page job can hold a few hundred MB of disk.

Sizing: rendering is CPU-bound, so `-workers` × `-page-workers` above the
number of cores does not make conversion faster; it only queues work inside
the process instead of in front of it. Worst-case pixmap memory is about
`-workers` × `-page-workers` × 7 bytes per pixel of the `-max-megapixels`
cap — about 560 MB per page at 80 MP, about 140 MB at 20 MP — plus each
running job's PDF (see `-max-upload-mb`). To run more pages in parallel
within a fixed memory budget, lower `-max-megapixels` (larger pages then
fail with an error naming the dpi that would fit) rather than raising
the memory limit. The effective workers, page-workers and megapixel cap are
logged at startup.

### S3 upload (optional)

With `P4WN_S3_BUCKET` set, every rendered PNG page is also PUT to that
bucket as `<key_prefix>-<page>.png` and the job status reports it as
`"key"` on the page; a page whose upload fails (3 attempts) is `failed`.
`key_prefix` is a job option (`[A-Za-z0-9_-]` segments separated by `/`,
e.g. `docs/7`); it defaults to the job ID. HTML jobs are not uploaded.
Objects are never deleted by p4wn (job TTL and `DELETE` only affect local
files), so the credentials need `s3:PutObject` only.

| Env | Meaning |
|-----|---------|
| `P4WN_S3_BUCKET` | bucket; unset = uploads off |
| `P4WN_S3_ACCESS_KEY`, `P4WN_S3_SECRET_KEY` | required with the bucket |
| `P4WN_S3_REGION` | default `us-east-1` |
| `P4WN_S3_ENDPOINT` | default `https://s3.<region>.amazonaws.com`; for an S3-compatible service, e.g. a MinIO/LocalStack URL such as `http://localhost:9000` |

Requests are path-style (`<endpoint>/<bucket>/<key>`), SigV4-signed.

### Browser-direct uploads with tickets (optional)

A backend that decides who may convert what can let a browser (or any other
untrusted client) send its PDF straight to p4wn, so the file never passes
through the backend, without handing out the static token. Per upload the
backend mints a short-lived **ticket**, signed with a secret it shares with
p4wn, and gives it to the client:

1. client → backend: "I want to upload `doc.pdf`, 3 MB"; the backend checks
   permissions and mints a ticket (constraints of its choice, e.g. the S3
   key prefix the pages must land under);
2. client → p4wn: `POST /v1/jobs` with `Authorization: Ticket <ticket>` and
   the multipart `file`; p4wn answers `202 {"id": ...}` as usual;
3. client → backend: "done, job `<id>`"; the backend reads the job with the
   static token (`GET /v1/jobs/{id}`), checks that its `options` are the
   ones it signed (e.g. `key_prefix`), and from there polls it as usual.

**Format.** `ticket = P "." S` where `P = base64url(JSON payload)` and
`S = base64url(HMAC-SHA256(secret, P))` — base64url without padding
(RFC 4648 §5), the MAC computed over `P` exactly as sent. Payload:

| Field | Type | Meaning |
|-------|------|---------|
| `v` | int, required | format version, `1` |
| `exp` | int, required | expiry, unix seconds; refused 30 s after it (clock skew), and when more than 1 h away |
| `jti` | string, required | unique ID, `[A-Za-z0-9._:-]{1,128}`; a ticket is accepted **once** |
| `max_bytes` | int | lowers the server's upload limit for this request (never raises it); larger → `413` |
| `max_pages` | int | the job fails before rendering when the document has more pages |
| `key_prefix` | string | the job's S3 key prefix (see S3 upload); unset = the job ID |
| `dpi`, `pages`, `gray`, `alpha`, `format` | as the job options | unset = the server defaults (150 dpi, all pages, png) |

Unknown fields are refused (a constraint p4wn does not know is not silently
ignored). The job's options are exactly the ticket's: a ticket request that
carries any option itself (form field or query parameter: `dpi`, `scale`,
`pages`, `gray`, `alpha`, `format`, `key_prefix`, `max_pages`) gets `400`;
ticket options are validated like request options (`400 ticket: ...`).

**Rules.** A ticket opens `POST /v1/jobs` only — every other route
(status, pages, archive, delete) still needs the static token, and a
ticket there gets `401`. It is checked (signature compared in constant
time, version, expiry, fields) before the request body is read, and spent
before the body is read too, so a second request with it gets
`401 ticket already used` even while the first upload is still running.
Refused up front (too many jobs `429`, query options `400`, a
`Content-Length` over the limit `413`) it is not spent; refused after that
(file too large once read, bad form, queue full) it is. Used IDs are
remembered in memory until they expire (at most an hour's worth; when
100 000 unexpired IDs are held, new tickets get `503`). **After a restart
that memory is gone:** a ticket used before the restart is accepted once
more until its `exp` — keep expiries short (minutes). Refusals are `401`
with `WWW-Authenticate: Ticket` and an `{"error": ...}` naming the reason.
Logs name the ticket's `jti` and the outcome, never the ticket or secret.

**Settings** (environment only, so the secret stays out of `ps`):

| Env | Meaning |
|-----|---------|
| `P4WN_TICKET_SECRET` | shared HMAC secret, at least 32 bytes (`openssl rand -hex 32`); unset = tickets off. Needs `-token`/`P4WN_TOKEN` too (without a token every route is open and a ticket would restrict nothing): the server refuses to start otherwise |
| `P4WN_CORS_ORIGINS` | comma-separated exact origins allowed to call `POST /v1/jobs` from a browser, e.g. `https://app.example.com,https://admin.example.com`; unset = no CORS headers |

**CORS.** For an allowed `Origin`, p4wn answers the preflight
`OPTIONS /v1/jobs` itself (no auth; allows `POST` with the `Authorization`
and `Content-Type` headers, cached 10 min) and adds
`Access-Control-Allow-Origin: <origin>` + `Vary: Origin` to every
`/v1/jobs` answer, errors included, so the page can read `401`/`413`/`429`
bodies. Other origins get no CORS headers (the browser blocks the call);
other routes never get them. No credentials or cookies are involved.

**Minting** — any language with HMAC-SHA256 and base64; in shell:

```sh
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
P=$(printf '{"v":1,"exp":%d,"jti":"%s","key_prefix":"uploads/42","max_pages":200}' \
      "$(( $(date +%s) + 300 ))" "$(openssl rand -hex 16)" | b64url)
S=$(printf %s "$P" | openssl dgst -sha256 -hmac "$P4WN_TICKET_SECRET" -binary | b64url)
curl -s -H "Authorization: Ticket $P.$S" -F file=@doc.pdf https://convert.example.com/v1/jobs
```

```
payload = base64url(json({"v": 1, "exp": now + 300, "jti": random_hex(16), "key_prefix": "uploads/42"}))
ticket  = payload + "." + base64url(hmac_sha256(secret, payload))
```

**Security notes.** Keep expiries short (it only has to cover the time
until the upload *starts*; the upload itself may run up to the read
timeout). Serve p4wn over HTTPS — a ticket is a bearer credential until it
is used. A ticket holder can submit one PDF of at most the ticket's (or the
server's) size, rendered with the options the issuer chose, and nothing
else: no reading jobs, pages or archives, no deleting, no choosing the key
prefix. What the file contains is up to them (any bytes; p4wn treats every
input as hostile — page/pixel/time limits and per-page panic isolation),
so the issuer should check the job's outcome before trusting it. Rotate
the secret by changing it on both sides (tickets in flight then fail and
must be minted again). Rate limiting per client is not built in; put a
reverse proxy in front (below).

### Reverse proxy (nginx)

[`deploy/nginx.conf.sample`](deploy/nginx.conf.sample) puts p4wn on its own
HTTPS domain: `POST /v1/jobs` (and its preflight) public with a per-IP
rate limit, request buffering off and timeouts sized like the server's
read timeout; `/healthz` public; every other route only from trusted
backend addresses (`allow`/`deny`). `client_max_body_size` must stay above
`-max-upload-mb` so p4wn, not nginx, answers `413` (nginx's own error
pages carry no CORS headers, so a browser cannot read them). CORS is
answered by p4wn itself — do not add CORS headers in nginx as well.

Job directories left in `-data-dir` by a previous process (32-hex-char
job-ID dirs only; other files and dotfiles are left alone) are removed at
startup. The server sets a whole-request read timeout sized for a
full-size upload at ~1.4 Mbit/s (6 s per MB of `-max-upload-mb`, at least
1 min: 10 min at the default 100 MB) and a 2 min idle timeout, and no write
timeout so large zip downloads are not cut off. The effective upload limit
and read timeout are logged at startup.

### Docker

```sh
docker build -t p4wn .
docker run --rm -p 8081:8081 -v p4wn-data:/data p4wn -token "$P4WN_TOKEN"
```

Static `CGO_ENABLED=0` binary on distroless `static:nonroot`, listening on
`:8081` with `-data-dir /data` (owned by the nonroot user); extra flags are
appended after the image name. Only `go-app-api/` is built (`GOWORK=off`).

With compose (`docker-compose.yml`), settings come from `.secrets/.env`
(optional; copy [`.secrets/.env.example`](.secrets/.env.example)):
`P4WN_TOKEN`, `P4WN_CJK_FONT`, `P4WN_MAX_UPLOAD_MB`, `P4WN_MAX_PAGES`,
`P4WN_WORKERS`, `P4WN_PAGE_WORKERS`, `P4WN_MAX_MEGAPIXELS`,
the `P4WN_S3_*` variables, `P4WN_TICKET_SECRET` and `P4WN_CORS_ORIGINS`
above.
`P4WN_PORT` in the shell (default 8081) sets the host port. Job data is in the
`p4wn-data` volume. For CJK text put a font in `.secrets/fonts/` (git-ignored,
mounted read-only at `/fonts`, must be readable by uid 65532) and set
`P4WN_CJK_FONT=/fonts/<file>`.

```sh
docker compose up --build -d
docker compose down
```

### systemd

[`deploy/p4wn.service`](deploy/p4wn.service) is a sample unit for a small
host: `127.0.0.1:8081`, `-workers 1 -page-workers 2 -max-dpi 300`, state in
`/var/lib/p4wn` (`StateDirectory`), token from `/etc/p4wn.env`
(`P4WN_TOKEN=...`), `MemoryMax=1500M`, `Restart=always`. Flags on
`ExecStart` win over the same settings in `/etc/p4wn.env`
(`P4WN_WORKERS`, `P4WN_PAGE_WORKERS`, `P4WN_MAX_MEGAPIXELS`, ...). Install steps are
in the file header. For browser uploads put it behind nginx as above and add
`P4WN_TICKET_SECRET` / `P4WN_CORS_ORIGINS` to `/etc/p4wn.env`.

## API

Async job model: upload a PDF, poll for status, fetch PNG pages or a single
HTML document.

```
POST   /v1/jobs                      multipart "file" + dpi|scale, pages, gray, alpha, format, key_prefix, max_pages
                                     (or only "file" with "Authorization: Ticket <t>", see tickets above)
GET    /v1/jobs/{id}                 status + per-page progress (+ document_url for html)
GET    /v1/jobs/{id}/pages/{n}.png   one rendered page (format=png)
GET    /v1/jobs/{id}/document.html   self-contained scrollable HTML (format=html)
GET    /v1/jobs/{id}/archive.zip     all pages zipped, or document.html (when done)
DELETE /v1/jobs/{id}
GET    /healthz
```

Options (form field or query param): `dpi` (8–`-max-dpi`, default 150) **or**
`scale` (dpi = 72·scale); `pages` like `1-3,7,9-` (default all); `gray` and
`alpha` booleans (png); `format` = `png` (default) or `html`; `max_pages` fails the job before
rendering when the document has more pages (it can only lower `-max-pages`); `key_prefix` (see S3 upload).

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
curl -s -F file=@doc.pdf "localhost:8081/v1/jobs?dpi=150&pages=1-3"
# → {"id":"a1b2...","status":"queued","status_url":"/v1/jobs/a1b2..."}

# submit (HTML)
curl -s -F file=@doc.pdf "localhost:8081/v1/jobs?format=html&pages=1-3"

# poll
curl -s localhost:8081/v1/jobs/a1b2...
# → {"status":"done","pages_done":3,"pages":[{"page":1,"status":"done","url":"..."}]}
# HTML jobs include "document_url":"/v1/jobs/.../document.html"

# fetch
curl -o page1.png localhost:8081/v1/jobs/a1b2.../pages/1.png
curl -o doc.html  localhost:8081/v1/jobs/a1b2.../document.html
curl -o all.zip   localhost:8081/v1/jobs/a1b2.../archive.zip
```

Status values: `queued`, `processing`, `done`, `partial` (some pages failed),
`failed`. A done PNG page may carry `"warnings"`: content that was left out
(e.g. an image in an unsupported codec variant). Fetching a page before it's ready returns `409` with `Retry-After`.

Limits: 100 MB upload (`-max-upload-mb`), 1000 pages/job (`-max-pages`), 20000 px max dimension, 80 MP per page
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
go run testtools/webui          # serves http://localhost:3000, proxies to :8081
```

The UI has a `format` selector; HTML jobs preview `document.html` in an iframe.

### End-to-end smoke

```sh
go run p4wn/cmd/p4wn-server -addr :8081 -data-dir /tmp/p4wn &
ID=$(curl -s -F file=@testdata/pdfs/vec.pdf localhost:8081/v1/jobs | \
     sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
sleep 1
curl -s -o out.png localhost:8081/v1/jobs/$ID/pages/1.png && file out.png

ID=$(curl -s -F file=@testdata/pdfs/vec.pdf "localhost:8081/v1/jobs?format=html" | \
     sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
sleep 1
curl -s -o out.html localhost:8081/v1/jobs/$ID/document.html && head -c 80 out.html
```
