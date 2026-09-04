---
name: img2svg-onboarded
description: NEW pet-platform repo — cross-lang raster→SVG tracer; python vtracer CLI + Go/Fiber service calling it via os/exec pipe
metadata: 
  node_type: memory
  type: project
  modified: 2026-07-23T17:18:01.600Z
---

Onboarded 2026-07-23. `img2svg/` = raster→vector **tracing tool**, module `github.com/vukyn/img2svg`. Built from scratch this session (git init'd, initial commit, **no remote yet**).

**Cross-language design** (user's idea): python CLI = engine, Go service = wrapper.
- `cli/img2svg.py` — wraps Rust **vtracer** color tracer. 3 modes: file, stdout (`-o -`), and **pipe** (`img2svg.py - -q <q>`: stdin bytes → stdout SVG, format auto-detected from magic bytes).
- `cmd/server/main.go` — Fiber v2 HTTP service, `POST /api/trace` (multipart image+quality) + serves embedded UI.
- `internal/tracer/tracer.go` — the integration: **`os/exec` subprocess** per request, pipes image→CLI stdin, reads SVG←stdout. No temp files, no long-lived python. Chosen over HTTP sidecar for simplicity (single deploy); swap tracer pkg for sidecar HTTP call if throughput ever matters — handler contract unchanged. Also injects `viewBox` into vtracer SVG (had width/height only → CSS cropped instead of scaling).

Quality presets `faithful|balanced|small` live in CLI `PRESETS` AND `internal/tracer` validQuality — **keep both in sync**.

**UI = React now (PR#9, was vanilla single-file).** `ui/` = Vite 7 + React 19 + TS, **plain CSS (NO Chakra)** ported from `demo/img2svg-redesign.html` (approved design source of truth). Built into `internal/web/dist`, embedded via `//go:embed all:dist` + SPA fallback — platform "embed built UI" standard: bundle git-ignored, committed `.gitkeep` keeps embed valid on fresh checkout, **`make build-web` MUST run before `go build`** (chained by `make build`/`make dev`). `make web`=Vite HMR proxying /api→:8090. Features: drag/click/⌘V-paste upload, segmented quality+resize, aspect-locked W/H, transparent-bg flood-fill (`keyOutBackground` in `ui/src/lib/image.ts`), stat cards, compare slider, zoom/pan lightbox, copy/download, empty/loading/error states, responsive. mprocs entries: img2svg-{build-web,run,build-run,web,build,cli-deps}.

Still standalone like [[sgo-onboarded]] / [[gobuild-preset-system]] / speedtest: **NO** DB/DI/domains/clean-arch/[[kuery-shared-lib-rule]]/SSO. Go clean-arch conventions N/A (has React UI but not platform Chakra/service scaffolding). Requires `python3`+`vtracer` on host (`make cli-deps`). Env: `PORT`=8090, `PYTHON_BIN`, `IMG2SVG_CLI` (relative to run dir — run from repo root). PRs #1-9 all merged. `faithful` ~1MB detailed art, `small` ~5× smaller.
