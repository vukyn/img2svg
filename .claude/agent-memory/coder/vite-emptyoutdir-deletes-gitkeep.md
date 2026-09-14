---
name: vite-emptyoutdir-deletes-gitkeep
description: `npm run build` in ui/ wipes the tracked .gitkeep that keeps go:embed valid — always `make build-web`, never bare vite build
metadata:
  type: feedback
---

In every repo following the platform "embed built UI via `go:embed`" standard, the
built bundle is gitignored except a committed `internal/web/dist/.gitkeep`, which
is what keeps the `//go:embed all:dist` directive compiling on a fresh checkout.
Vite's `build.emptyOutDir: true` **deletes that placeholder** on every build.

**Why:** running the documented-looking gate `cd ui && npm run build` leaves
`git status` showing ` D internal/web/dist/.gitkeep` and turns
`go test ./internal/web/...` red with
`embed root must contain the dist placeholder: open .gitkeep: file does not exist`
— a failure that has nothing to do with the change under review, and which is one
careless `git add` away from committing the deletion and breaking a clean
checkout for everyone. Hit on img2svg 2026-09-13; the repo's `Makefile` already
carried the fix (`build-web` runs `touch internal/web/dist/.gitkeep` after vite)
so the bug only appears when the Makefile is bypassed.

**How to apply:** use `make build-web`, never bare `npm run build`, whenever the
Go build or `go test ./internal/web/...` follows. If a task hands you
`cd ui && npm run build` as the gate, run the make target instead and say why.
After any UI build, check `git status internal/web/` before staging — and stage
explicit paths regardless ([[stage-explicit-paths-parallel-sessions]]).
