# Coder memory — img2svg

One line per memory: link + hook only. Detail lives in the topic file.

## Security hardening (2026-09-13)
- [CSP 'self' excludes blob:](csp-self-does-not-cover-blob.md) — every object-URL preview blocked; app loads clean and paints nothing; only real-Chrome CDP catches it
- [Sec-Fetch-Site outranks Origin](sec-fetch-site-beats-origin.md) — Origin-vs-Host breaks the Vite proxy (changeOrigin); missing Origin ⇒ ALLOW; mount ahead of the limiter
- [DecodeConfig pixel guards fail OPEN](go-image-decodeconfig-divergence.md) — x/image/bmp refuses valid RLE BMPs; VP8X WebP IS measurable; 45-byte PNG header claims any size
- [exec Write error ≠ dead child](exec-write-error-does-not-kill-child.md) — capping Cmd.Stdout detects the overflow but never stops the subprocess; cancel the context, and TIME the test

## Build traps
- [vite emptyOutDir eats .gitkeep](vite-emptyoutdir-deletes-gitkeep.md) — bare `npm run build` reds `go test ./internal/web`; always `make build-web`
