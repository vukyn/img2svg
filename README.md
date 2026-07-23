# img2svg

Trace a raster image (Pokémon art, icons, logos) into scalable vector **SVG**. Vector = scale infinite, no blur.

Two layers:
- **python CLI** (`cli/img2svg.py`) — the tracing engine, wraps [vtracer](https://github.com/visioncortex/vtracer) (Rust) color tracing.
- **Go service** (`cmd/server`) — Fiber HTTP server that serves a web UI and a `POST /api/trace` endpoint; it shells out to the CLI via `os/exec` (image bytes in via stdin, SVG out via stdout — no temp files).

```
img2svg/
  cli/
    img2svg.py        # tracing engine (file mode + stdin→stdout pipe mode)
    requirements.txt
  cmd/server/main.go  # Fiber HTTP service
  internal/
    tracer/           # os/exec wrapper around the CLI
    web/              # go:embed static UI (upload · log · output)
```

## Setup

```bash
make cli-deps   # python3 -m pip install -r cli/requirements.txt (vtracer)
make deps       # go mod tidy
```

## Run the service

```bash
make run                        # http://localhost:8090
```

Web UI: drop an image → pick quality → **Trace → SVG**. Shows a live log and the rendered output with a download button.

### Config (env)

| Var | Default | Meaning |
|-----|---------|---------|
| `PORT` | `8090` | HTTP port |
| `PYTHON_BIN` | `python3` | python interpreter |
| `IMG2SVG_CLI` | `cli/img2svg.py` | path to the CLI (relative to run dir) |

### API

```
POST /api/trace   (multipart)
  image=@art.png            required
  quality=faithful|balanced|small   default faithful
→ 200 image/svg+xml   |   400 bad upload   |   422 trace error
```

## CLI standalone

```bash
python3 cli/img2svg.py bulbasaur.png              # -> bulbasaur.svg
python3 cli/img2svg.py art.webp -o icon.svg -q small
cat art.png | python3 cli/img2svg.py - -q balanced > art.svg   # pipe mode (used by the Go service)
```

## Quality presets

| Preset     | Detail | File size | Use |
|------------|--------|-----------|-----|
| `faithful` | max    | large     | exact match (default) |
| `balanced` | mid    | mid       | general |
| `small`    | low    | small     | icons, web assets (~5× smaller) |

Input: png · jpg · gif · bmp · webp. Output: one stacked-layer color SVG.
