---
name: csp-self-does-not-cover-blob
description: CSP `default-src 'self'` blocks every blob: object URL — the app loads clean, renders nothing, and the failure is invisible unless you open it in a real browser
metadata:
  type: feedback
---

A CSP of `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'`
— the policy everyone reaches for first — **blocks every `blob:` URL**. `'self'`
means the document's own scheme+host+port; `blob:` is a different scheme and is
not covered. Any `<img src={URL.createObjectURL(…)}>` is silently refused.

**Why:** it is a *silent* break. The page loads, the bundle runs, React renders,
no exception is thrown — the images are just never painted. Found on img2svg
(PR #23), where the proposed starting CSP would have blanked all three previews
(upload thumbnail, compare raster, traced SVG) because every one of them is an
object URL. Nothing in `go test`, `npm run lint`, `npm run build` or a jsdom test
can see it: jsdom does not implement object URLs or CSP at all.

**How to apply:**

- Before shipping a CSP, grep the UI for `createObjectURL`, `data:`, `URL.create`
  and external font/CDN hosts, and write `img-src`/`font-src`/`connect-src` from
  what you find. `img-src 'self' blob:` is the usual minimum for any app with a
  file preview. Leave `data:` **out** unless something uses it — an unused
  allowance is only an allowance for an injection.
- Then prove it in a **real browser**, not jsdom: headless Chrome over CDP with
  `Log.enable` + `Runtime.enable` + `Network.enable`, navigate, drive the real
  flow, and assert zero `Log.entryAdded` with `source: "security"`, zero console
  errors, zero `Network.loadingFailed`. Chrome needs
  `--remote-allow-origins=*` or the CDP websocket handshake 403s.
- Assert the specific directive in a unit test, not just header presence
  (`expect(policy).toContain("img-src 'self' blob:")`). A presence check passes
  the exact policy that breaks the app. See [[feedback-prove-regression-tests]].

Related: [[gardener-fe-headless-verification]], [[storybook-browser-check]] —
same lesson, that a headless real browser is the only instrument for a class of
failure that leaves no trace in the test output.
