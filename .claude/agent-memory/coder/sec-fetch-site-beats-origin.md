---
name: sec-fetch-site-beats-origin
description: An Origin-vs-Host cross-site check breaks the Vite dev proxy (changeOrigin rewrites Host); Sec-Fetch-Site is the signal that survives, and a missing Origin must be ALLOWED
metadata:
  type: feedback
---

When adding a cross-site / CSRF-shaped check to a Fiber endpoint, order the two
signals deliberately:

1. **`Sec-Fetch-Site` first.** Allow `same-origin` / `same-site` / `none`, refuse
   `cross-site`. The browser computes it and page script cannot forge it (a
   forbidden header name).
2. **`Origin` vs `c.Hostname()` second**, for browsers too old to send fetch
   metadata. Refuse a malformed or `null` Origin.
3. **Neither header present ⇒ ALLOW.**

**Why each step:**

- The Origin-vs-Host comparison **breaks `make web`**. The Vite dev proxy sets
  `changeOrigin: true`, which rewrites `Host` to the backend (`127.0.0.1:8090`)
  while forwarding the browser's `Origin` (`http://localhost:5173`) untouched —
  so a legitimate same-origin request looks cross-site. `Sec-Fetch-Site` is
  computed before the proxy exists and still says `same-origin`, which is the
  only reason the dev flow survives.
- Allowing a missing Origin is not laziness: the Fetch spec makes `Origin`
  mandatory on every method but GET/HEAD, so a POST without one **did not come
  from a browser**. It is curl, CI or a probe — none of which the check defends
  against, because they can address the endpoint directly regardless. Refusing
  them breaks every script and stops nothing; the rate limiter is what bounds a
  direct caller.
- **Mount it ahead of the rate limiter.** Fiber's limiter counts 4xx
  ([[fiber-ip-and-buffer-gotchas]]), so behind it a drive-by flood spends the
  budget of the address it rides in on — a shared NAT, a corporate egress — and
  locks out the real callers there. Pin that ordering with its own test; the
  route's argument order is otherwise invisible to a reviewer.

⚠️ `c.Hostname()` honours `X-Forwarded-Host` whenever `IsProxyTrusted()` is true,
which it is by default (`EnableTrustedProxyCheck: false`). Not exploitable from
the drive-by threat model — an HTML form cannot set arbitrary headers — but it is
why step 1 has to outrank step 2 rather than merely complement it.

Applied in img2svg PR #23 (`rejectCrossSite` / `isCrossSite` in `cmd/server`).
