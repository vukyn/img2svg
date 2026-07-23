# CLAUDE.md — img2svg

Raster→vector tracing tool. **Cross-language**: python CLI engine + Go HTTP service that calls it via `os/exec`.

## What it is / isn't

- **Is**: a standalone utility repo (module `github.com/vukyn/img2svg`) with a web UI. Not part of the clean-arch service fleet (isme/medioa2/rainy).
- **No** DB, no DI (sarulabs), no domains, no clean-arch layers, no kuery dependency, no mprocs/hosts entry, no isme SSO.
- Like `sgo`/`gobuild`/`speedtest`: the platform's Go conventions (kuery shared-pkg rule, clean-arch, DI) do **NOT** apply here.

## Architecture

```
cli/img2svg.py       # tracing engine — vtracer (Rust) color trace
cmd/server/main.go   # Fiber v2 HTTP service
internal/tracer/     # os/exec wrapper: pipes image→CLI stdin, reads SVG←stdout
internal/web/        # go:embed static UI (single index.html, vanilla JS)
```

**Integration = exec subprocess.** The Go service runs `python3 cli/img2svg.py - -q <quality>` per request, writes the uploaded image bytes to the CLI's **stdin**, and reads the SVG from **stdout**. No temp files, no long-lived python process. Cost: ~python+vtracer startup per call (acceptable for a low-QPS tool). If throughput ever matters, swap `internal/tracer` for an HTTP call to a long-lived python sidecar — the Go handler contract stays the same.

## CLI modes (`cli/img2svg.py`)

- file: `img2svg.py <img> [-o out.svg]` → writes `<img>.svg`
- stdout: `img2svg.py <img> -o -`
- **pipe** (used by the service): `img2svg.py - -q <q>` → stdin bytes → stdout SVG. Format auto-detected from magic bytes (png/jpg/gif/bmp/webp).

Quality presets `faithful|balanced|small` live in `PRESETS` in the CLI and are mirrored in `internal/tracer` validation — **keep both in sync** when adding a preset.

## Commands

```bash
make cli-deps   # pip install vtracer
make deps       # go mod tidy
make run        # serve on :8090
make build      # bin/server
```

## Gotchas

- Requires `python3` + `vtracer` on the host/deploy image. The service fails a trace (422) if the CLI is missing — check `PYTHON_BIN` / `IMG2SVG_CLI` env.
- `IMG2SVG_CLI` default `cli/img2svg.py` is **relative to the run directory** — run from repo root, or set an absolute path.
- Generated `*.svg` and `bin/` are gitignored.
- `faithful` output is large (~1MB for detailed art); `small` ~5× smaller.
