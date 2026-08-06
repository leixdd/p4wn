# AGENTS.md — p4wn

## What this is

**p4wn** is a foundational conversion tool: it turns PDFs (specs and other source
material) into PNG pages or a scrollable HTML document so humans and downstream
agents can understand them more easily.

North star: **readable conversion for understanding**, not a full PDF suite.
Known degradations documented in `go-app-api/README.md` stay known unless the
user explicitly asks to fix them.

## Identity

| Item | Value |
|------|--------|
| Go module | `p4wn` (lives in `go-app-api/`) |
| CLI | `p4wn` → `go-app-api/cmd/p4wn` |
| Server | `p4wn-server` → `go-app-api/cmd/p4wn-server` |
| Workspace | `go.work` at repo root — **run all commands from repo root** |
| Dev tools | `testtools` module (webui, imgdiff, inkprofile, fixture generators) |

## Layout

```
.
├── go.work
├── go-app-api/          module p4wn
│   ├── api/             async jobs, HTTP handlers, worker pool
│   ├── cmd/p4wn         CLI
│   ├── cmd/p4wn-server  HTTP API
│   └── internal/
│       ├── pdf          lexer/parser, xref, decrypt, page tree
│       ├── filter       stream filters + predictors
│       ├── graphics     matrices, paths, colors, pixmaps
│       ├── render       Device, DrawDevice (PNG), HTMLDevice (SVG/HTML)
│       ├── font         embedded fonts, ToUnicode, base-14
│       └── content      interpreter, images, HTML assembly
├── testdata/            PDF fixtures + golden PNGs
├── testtools/           manual/dev utilities
└── data/                runtime job artifacts (gitignored; do not commit)
```

## Architecture seam (must respect)

```
PDF bytes → pdf → content interpreter → Device → PNG (DrawDevice) or HTML (HTMLDevice)
```

`internal/content` talks to rendering **only** through `render.Device`.
Do not bypass that seam. PNG and HTML are two Device implementations, not
parallel interpreters.

Details and capability matrix: [`go-app-api/README.md`](go-app-api/README.md).
Runbook / API / smoke: [`README.md`](README.md).

## How to run

```sh
go run p4wn/cmd/p4wn-server -addr :8080 -data-dir ./data
go run p4wn/cmd/p4wn -dpi 150 -pages 1-3 input.pdf outdir/
go run p4wn/cmd/p4wn -format html -pages 1-3 input.pdf outdir/
go run testtools/webui   # UI on :3000, proxies to API :8080
```

API: async `POST /v1/jobs` → poll `GET /v1/jobs/{id}` → fetch pages / `document.html` / zip.
See root README for full routes and options.

## How to test

```sh
go test p4wn/...
go test p4wn/internal/content -run TestGolden -update   # only when intentional
go test p4wn/internal/pdf -fuzz FuzzOpen
go test p4wn/internal/filter -fuzz FuzzDecode
```

## Agent change stance

- **`api/`, `cmd/`, docs, `testtools/`**: change freely when the task asks.
- **`internal/*` (renderer/PDF stack)**: small, test-backed fixes only unless the
  user explicitly requests a fidelity or architecture change.
- Prefer extending `testdata/` fixtures over one-off scripts.
- Do not expand scope beyond the asked change.
- Never commit contents of `data/` (job artifacts).
- This project is Go; do not introduce Node/npm.

## Git hooks

`.githooks/commit-msg` and `prepare-commit-msg` strip LLM `Co-Authored-By`
trailers (Claude, Cursor, Copilot, etc.) from commit messages. Human
co-authors are kept. After clone: `./scripts/install-githooks.sh`.
