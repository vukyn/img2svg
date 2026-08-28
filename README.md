# img2svg

**🔗 Live: https://img2svg.fly.dev**

Turn any raster image — Pokémon art, logos, icons, screenshots — into a clean, infinitely-scalable **SVG**. Drop, click, or paste (⌘V) an image, tune the quality, size, and background, then trace it to vector you can zoom forever with **no blur**. Compare the result against the source side-by-side, inspect it full-screen, and copy or download the SVG.

## Features

- **Color tracing** (vtracer engine) with 3 quality presets — `faithful` (exact) · `balanced` · `small` (~5× smaller, great for icons).
- **Resize** before tracing — by ratio (%) or exact width/height with the **aspect ratio locked**.
- **Transparent background** — flood-removes the background so the SVG drops onto any surface.
- **Raster ↔ SVG compare slider**, a **zoom/pan fullscreen** viewer, and one-click **copy** / **download** of the SVG.
- Live **log** + **stat cards**: input/output size, path count, size ratio, trace time.

Supports png · jpg · gif · bmp · webp.

---

Two layers:
- **python CLI** (`cli/img2svg.py`) — the tracing engine, wraps [vtracer](https://github.com/visioncortex/vtracer) (Rust) color tracing.
- **Go service** (`cmd/server`) — Fiber HTTP server that serves a **React UI** (Vite + TypeScript, embedded via `go:embed`) and a `POST /api/trace` endpoint; it shells out to the CLI via `os/exec` (image bytes in via stdin, SVG out via stdout — no temp files).

```
img2svg/
  cli/
    img2svg.py        # tracing engine (file mode + stdin→stdout pipe mode)
    requirements.txt
  cmd/server/main.go  # Fiber HTTP service
  internal/
    tracer/           # os/exec wrapper around the CLI
    web/              # go:embed the built UI (internal/web/dist)
  ui/                 # React 19 + Vite 7 + TypeScript frontend (source)
```

## Setup

```bash
make cli-deps   # python3 -m pip install -r cli/requirements.txt (vtracer)
make deps       # go mod tidy
```

## Run the service

The UI is a React app that is **built and embedded** into the Go binary via
`go:embed`. `make build-web` MUST run before `go build` — it produces the
embedded assets in `internal/web/dist` (never committed; only a `.gitkeep`
placeholder is).

```bash
make build-web                  # build the UI → internal/web/dist
make run                        # http://localhost:8090 (serves the embedded UI)
# or, for local frontend work:
make web                        # Vite dev server (HMR), proxies /api → :8090
```

`make dev` runs `build-web` then `run` in one shot. `make build` builds the UI
then the server binary into `bin/`.

Web UI: drop / click / paste (⌘V) an image → pick quality, resize, transparent
background → **Trace → SVG**. Shows a live log, stat cards, a raster↔SVG compare
slider, a zoom/pan fullscreen lightbox, and copy/download of the SVG.

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
python3 cli/img2svg.py naruto.jpeg --decheck                   # strip a baked-in chequerboard first
```

## `--decheck`: a transparent PNG that was saved as JPEG

JPEG cannot carry transparency, so what such a file keeps is the **chequerboard
the viewer was painting behind the image** — and by then that is ordinary pixels
with nothing to say it was ever a background. Trace it and the vector comes out
with a grey-and-white pattern wrapped around the subject.

`--decheck` removes it first. **Off by default**: it is a repair, and an image
that never had the problem should not go through a filter that could take a white
collar off it.

```
decheck: 15 px squares, cut 75.4% (0 enclosed, 1476 feathered)
naruto.svg  (410 KB, balanced, dechecked)
```

The obvious version of this is wrong twice over, which is why it is not one line:

- **Erasing by colour holes the drawing.** An eye highlight, a white fur collar
  and a metal headband are all pale and unsaturated. The first attempt at this
  took pieces out of all three.
- **Flood-filling from the border spares those** — a drawing's own whites are
  enclosed by its outlines and the border cannot reach them — **but it cannot
  reach a chequer patch enclosed between an arm and a coat** either.

What tells the two apart is the **grid**. A chequer alternates on a fixed pitch;
a drawing's shading does not, however many tones it carries. The pitch is fitted
from the flood, where the background is certainly chequer, and an enclosed patch
is cut only when nine tenths of it agrees. When no pitch fits, only the flood
runs — a picture keeping a background it did not need to lose is the better of
the two failures.

⚠️ Not the same thing as the web UI's background keying. That one averages the
four corners into a single colour and floods from the border with a tolerance; it
runs in the browser, on the canvas, before the upload. `--decheck` is the CLI's
own and is the stronger of the two. Neither calls the other.

## Quality presets

| Preset     | Detail | File size | Use |
|------------|--------|-----------|-----|
| `faithful` | max    | large     | exact match (default) |
| `balanced` | mid    | mid       | general |
| `small`    | low    | small     | icons, web assets (~5× smaller) |

Input: png · jpg · gif · bmp · webp. Output: one stacked-layer color SVG.
