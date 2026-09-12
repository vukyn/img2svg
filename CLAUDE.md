# CLAUDE.md — img2svg

Raster→vector tracing tool. **Cross-language**: python CLI engine + Go HTTP service that calls it via `os/exec`.

## The memory layer

@MEMORY.md

⚠️ **That import is the point of the file, not decoration.** `MEMORY.md` and
`memory/` are the distilled layer — one hard-won fact per file, with why it
matters — and they live **in the repository** because a machine's own Claude
memory directory is workspace-scoped and machine-local: this repo opened on
another machine, or outside the workspace the notes were written in, arrived with
none of them.

It is a **distillation, not the record.** This file and the repository's other
documents stay the authority; where a note disagrees with the file that owns the
subject, the repository wins and the note is what to fix. `MEMORY.md` carries the
rules the notes are written under — one line per note in the index, one fact per
file, say why rather than only what, and delete a wrong note rather than adding a
second one beside it.

## What it is / isn't

- **Is**: a standalone utility repo (module `github.com/vukyn/img2svg`) with a web UI. Not part of the clean-arch service fleet (isme/medioa2/rainy).
- **No** DB, no DI (sarulabs), no domains, no clean-arch layers, no kuery dependency, no mprocs/hosts entry, no isme SSO.
- Like `sgo`/`gobuild`/`speedtest`: the platform's Go conventions (kuery shared-pkg rule, clean-arch, DI) do **NOT** apply here.

## Architecture

```
cli/img2svg.py       # tracing engine — vtracer (Rust) color trace
cmd/server/main.go   # Fiber v2 HTTP service
internal/tracer/     # os/exec wrapper: pipes image→CLI stdin, reads SVG←stdout
internal/web/        # go:embed the built React UI (internal/web/dist)
ui/                  # React 19 + Vite 7 + TypeScript frontend (source)
```

## Web UI (React, embedded)

The UI is a **React app** (Vite 7 + React 19 + TypeScript), **not** Chakra/kuery
— plain CSS ported from `demo/img2svg-redesign.html` (the approved design source
of truth). It is built into `internal/web/dist` and embedded into the Go binary
via `//go:embed all:dist` (mirrors the platform "embed built UI via go:embed"
standard: built bundle git-ignored, a committed `.gitkeep` keeps the embed valid
on a fresh checkout).

- **`make build-web` MUST run before `go build`** — go:embed reads the files at
  compile time. `make build` / `make dev` chain build-web for you.
- `make web` runs the Vite dev server (HMR) and proxies `/api` → `:8090`.
- Components live in `ui/src/components/`; the real trace/canvas logic
  (drag-drop/paste upload, `prepareInput` resize + `keyOutBackground` flood-fill,
  `POST /api/trace`, metrics, compare slider, zoom/pan lightbox) is in
  `ui/src/App.tsx` + `ui/src/lib/`.
- Still standalone: **no** DB/DI/domains/clean-arch/kuery/Chakra/SSO. The Go
  handler contract (`POST /api/trace` multipart `image`+`quality` → `image/svg+xml`)
  is unchanged.

⚠️ **The traced SVG is rendered as `<img src={objectURL}>`, never injected as
markup.** All three views (result layer, compare slider, lightbox) used
`dangerouslySetInnerHTML` and no longer do. The bytes come out of a subprocess, and
SVG injected into this DOM runs animation and event handlers — `<animate onbegin>`,
`<image onerror>` — even though it cannot run a `<script>`, which is what makes the
risk easy to talk yourself out of. An `<img>` gets none of that.

- It costs the views nothing, which is why `<img>` rather than a sanitiser: the
  compare clip is on the **wrapper**, the lightbox zoom is a CSS transform on the
  **wrapper**, and `.stage svg, .stage img` / `.lb-stage svg, .lb-stage img` in
  `index.css` already size both the same way. Verified in a real browser — the
  raster and vector layers measure pixel-identical.
- `App.tsx` owns the object URL's lifetime alongside `rasterUrlRef` / `thumbUrlRef`
  and revokes it on reset and on unmount. ⚠️ The blob **must** carry
  `type: "image/svg+xml"` — an untyped blob loads as a download, so the preview
  goes blank with nothing in the console.
- ⚠️ `svgText` is still kept, for copy/download/metrics. Rendering from it is the
  regression; `src/components/svg-rendering.test.tsx` bans the pattern at source
  level so a fourth view cannot reintroduce it quietly.
- UI tests run on **vitest + jsdom** (`make test-web` / `npm test` in `ui/`). They
  are the repo's only frontend tests; there was no runner before them.

**Integration = exec subprocess.** The Go service runs `python3 cli/img2svg.py - -q <quality>` per request, writes the uploaded image bytes to the CLI's **stdin**, and reads the SVG from **stdout**. No temp files, no long-lived python process. Cost: ~python+vtracer startup per call (acceptable for a low-QPS tool). If throughput ever matters, swap `internal/tracer` for an HTTP call to a long-lived python sidecar — the Go handler contract stays the same.

## CLI modes (`cli/img2svg.py`)

- file: `img2svg.py <img> [-o out.svg]` → writes `<img>.svg`
- stdout: `img2svg.py <img> -o -`
- **pipe** (used by the service): `img2svg.py - -q <q>` → stdin bytes → stdout SVG. Format auto-detected from magic bytes (png/jpg/gif/bmp/webp).

Quality presets `faithful|balanced|small` live in `PRESETS` in the CLI and are mirrored in `internal/tracer` validation — **keep both in sync** when adding a preset.

`--decheck` (`cli/decheck.py`) strips a **baked-in transparency chequerboard**: a
transparent PNG saved as JPEG keeps the chequer the viewer was painting behind it,
as ordinary pixels, and tracing that wraps the subject in a grey-and-white
background. **Off by default** — it is a repair, and a clean image should not go
through a filter that could take a white collar off it.

- ⚠️ **Not a colour to erase.** Pale-and-unsaturated also describes an eye
  highlight, a white fur collar and a metal headband; the first version holed all
  three. A border flood spares them (a drawing's whites are enclosed by its
  outlines) and cannot reach a chequer patch enclosed between an arm and a coat.
- ⚠️ **The grid is what separates them.** A chequer alternates on a fixed pitch,
  fitted from the flood; an enclosed patch is cut only at ≥90% agreement. **No
  pitch fitted ⇒ only the flood runs**, which is the safe failure.
- ⚠️ **CLI only, and NOT the UI's `keyOutBackground`.** That one averages the four
  corners into one colour and floods with a tolerance, client-side on the canvas
  before upload. Two algorithms, neither calling the other; `POST /api/trace` does
  **not** expose `--decheck`, because the UI already keys backgrounds itself.
- ⚠️ Adds **Pillow** to `cli/requirements.txt`. The tracer itself still never
  opens a pixel — only `--decheck` does.

## Resource limits on `POST /api/trace`

The endpoint is **unauthenticated** and every call forks a python interpreter plus
a vtracer run, on a 512 mb scale-to-zero machine. Five limits bound that, and they
are load-bearing rather than tuning — each closes a way one caller takes the whole
machine down. The numbers live in `cmd/server/main.go` consts and
`internal/tracer/tracer.go` consts.

- **Encoded body ≤ 20 MB** (`fiber.Config.BodyLimit` + the `io.LimitReader`).
- **Decoded pixels ≤ 40 M, and ≤ 10 000 px on a side** → **413**. ⚠️ The body limit
  does not imply this one: a flat-colour PNG of a few hundred kilobytes decodes to
  gigabytes of RGBA, so encoded size is no evidence about decoded size. Checked via
  `image.DecodeConfig`, which reads the header only, **before** the subprocess
  starts — after it starts, the allocation the check exists to prevent has already
  happened.
- **Traced SVG ≤ 32 MB** → **413**. ⚠️ The decoded-pixel cap bounds the INPUT and
  says nothing about the output: at `faithful` (`filter_speckle=4`,
  `path_precision=6`) a high-entropy image yields roughly a path per speckle, so an
  in-budget upload can expand into an SVG far larger than itself. Enforced by a
  capped `io.Writer` on the subprocess's stdout (`cappedBuffer`), which **refuses
  the write that would cross the ceiling** and cancels the subprocess's context at
  that instant — a ceiling that only noticed the overflow afterwards would mean the
  bytes were already generated and already in memory. Detailed art traces to about
  a megabyte, so only an adversarial input gets near this.
- **≤ 2 concurrent traces** → **503 + `Retry-After`**. Deliberately a refusal, not
  a queue: a queue in front of a subprocess this expensive does not prevent the
  overload, it converts one caller's rejection into everybody's timeout.
- **10 requests/minute per caller** (Fiber's `limiter`) → **429**.

⚠️ The rate limit depends on `fiber.Config`'s **`ProxyHeader` + `EnableIPValidation`
together**. `PROXY_HEADER` (set to `Fly-Client-IP` in `fly.toml`) is what makes
`c.IP()` the real caller — without it every request carries fly-proxy's address and
the per-IP budget becomes one global bucket. Without the validation flag Fiber
returns the header verbatim, so junk rotated through it mints unlimited fresh
budgets. Leave `PROXY_HEADER` unset for a local run, where the socket address is
already the truth.

Subprocess timeout is **20 s**. With only two slots, slot hold time *is* the
endpoint's availability, so raising it costs more than it looks.

⚠️ The output ceiling and the concurrency cap are **one number between them**: a
trace at the ceiling holds the capture and again the response body, so the worst
case is `32 MB × 2 copies × 2 concurrent traces` = **128 MB** of a 512 mb machine.
Raising either without the other is what turns a survivable ceiling into an OOM.
The ceiling was 64 MB until it was lowered on exactly this arithmetic — that made
the same worst case 256 MB, half the machine before the two python interpreters,
their vtracer working sets and the Go runtime are counted. 32 MB is still a 32×
margin over the ~1 MB a detailed `faithful` trace actually emits.
`TestOutputCeilingFitsTheDeploymentBudget` holds the sum, so raising the ceiling
without raising the machine fails a test rather than a deploy.

### Who is allowed to call it, and what the answer may do

`multipart/form-data` is a **CORS-simple** content type, so any page on the web can
submit a form at `/api/trace` with no preflight and no consent. Nothing is forged
— the endpoint is unauthenticated and changes no state — but the machine is spent,
which makes a drive-by the trigger for every limit above. `rejectCrossSite` refuses
those with a **403**, ahead of the rate limiter so a flood cannot spend the budget
of the address it rides in on.

- **`Sec-Fetch-Site` decides when it is present**, because the browser computed it
  and page script cannot forge it. It is also the only signal that survives a proxy
  rewriting `Host`, which is what the Vite dev server does (`changeOrigin: true`) —
  so `make web` keeps working.
- **`Origin` vs the request's host** is the fallback for a browser old enough to
  send no fetch metadata. A malformed or `null` Origin is refused.
- ⚠️ **Neither header present is ALLOWED, deliberately.** The Fetch spec makes
  `Origin` mandatory on every method but GET/HEAD, so a POST without one did not
  come from a browser — it is `curl`, CI, or a probe, none of which this check
  defends against, since they can address the endpoint directly regardless. The
  rate limiter is what bounds them.

Response headers, set by `securityHeaders` on **every** response (globally, because
a header a route has to remember is a header a route eventually forgets):

- **`X-Content-Type-Options: nosniff`** — the traced SVG's bytes come from an
  upload, so the browser must not re-guess its type.
- **`Content-Security-Policy`** — ⚠️ `img-src` **must** keep `blob:`. Every preview
  in the UI (upload thumbnail, compare raster, traced SVG) is an object URL, and
  `blob:` is **not** covered by `'self'`; dropping it leaves an app that loads
  cleanly and displays nothing. `script-src 'self'` with no nonce is only correct
  because Vite emits one external module script and no inline script — re-check the
  built `index.html` if the bundler config changes.
- **`Content-Disposition: attachment`** on a successful trace — an SVG opened as a
  top-level document is a scripting context on this origin. Safe to add because the
  UI reads the endpoint with `fetch()` and renders the bytes itself; nothing ever
  navigates to it.

### What a failure is allowed to say

`Trace` **never returns the CLI's stderr**, and the global `ErrorHandler` never
returns `err.Error()`. A python traceback carries absolute host paths, source line
numbers and interpreter internals, and this endpoint hands its response body to
anyone — it is also a clean oracle for which of the limits above a prober just
tripped. The detail is `log.Printf`-ed server-side; the caller gets a sentinel
(`ErrTraceFailed` → 422, `genericErrorMessage` → 500).

⚠️ The one exception is **`unrecognized image format`**, relayed as
`ErrUnsupportedFormat`: it is about the bytes the caller uploaded, discloses
nothing about the host, and is the difference between a user fixing their file and
retrying a bad one forever. It is matched on an **allowlist** of that one marker
in stderr — never a blocklist of leaky shapes, because a rule built from the shapes
somebody thought of still relays the next one nobody did, and the CLI has other
`sys.exit` paths that quote host paths. ⚠️ The marker text must stay in step with
`cli/img2svg.py`, the same way the quality presets do.

`cmd/server/main_test.go` and `internal/tracer/tracer_test.go` cover all of the
above; both suites use a shell-script stand-in for the CLI, so they run without
python or vtracer on the host.

## Commands

```bash
make cli-deps   # pip install vtracer
make deps       # go mod tidy
make build-web  # build the React UI → internal/web/dist (run before go build)
make test-web   # UI component tests (vitest + jsdom)
make web        # Vite dev server (HMR) — proxies /api → :8090
make run        # serve on :8090 (serves the embedded UI)
make dev        # build-web + run (one-shot local preview)
make build      # build-web + bin/server
```

## Gotchas

- Requires `python3` + `vtracer` on the host/deploy image. ⚠️ A missing interpreter
  or CLI is now a **boot failure with a named setting**, not a 422 on somebody's
  upload — `resolveTracerPaths` in `cmd/server` stats both at startup.
- ⚠️ **`IMG2SVG_CLI` is no longer resolved against the working directory.** It used
  to be, which meant a server started from an attacker-writable directory loaded
  that directory's copy of the CLI (and the script's own `from decheck import …`
  followed it). A relative value now resolves against **the executable's own
  directory**; anything else must be absolute. Likewise `PYTHON_BIN`: a bare name is
  looked up on `$PATH` **once, at boot**, and the absolute result is what every
  subprocess runs. The Dockerfile sets both absolute
  (`/usr/local/bin/python3`, `/app/cli/img2svg.py`).
- Because of that, `make run` / `make dev` export `IMG2SVG_CLI=$(CURDIR)/cli/img2svg.py`
  — under `go run` the binary lives in a build cache far from the repo, so the path
  has to be named. Running `./bin/server` by hand needs the same variable.
- Generated `*.svg` and `bin/` are gitignored.
- `faithful` output is large (~1MB for detailed art); `small` ~5× smaller.
