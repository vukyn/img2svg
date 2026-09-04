---
name: kuery-shared-lib-rule
description: "Rule — anything reusable across services must become a versioned kuery package, imported not duplicated; keep max 5 newest version tags"
metadata: 
  node_type: memory
  type: feedback
---

When coding in pet-platform, any logic reusable now or in the future (helpers, middleware, shared models, adapters) must be written as a package in the `kuery` repo (`github.com/vukyn/kuery`, local at `pet-platform/kuery`), given a new version tag, and imported by the services — never scattered/duplicated per repo. Kuery IS the shared-lib repo. Version retention: keep at most the 5 newest tags (delete older ones local + remote; old versions stay fetchable via proxy.golang.org).

**Why:** the pkg/ duplication across isme/medioa2/rainy drifted into 3 incompatible variants and a shared bug; consolidation (see [[kuery-shared-pkg-consolidation]]) was expensive.

**How to apply:** before writing a helper inside a service, check if it's service-specific; if reusable → add to kuery, tag (bump minor), `go get` the new version in consumers. After tagging, prune tags beyond the 5 newest.

⚠️ **CORRECTION 2026-08-24 — the "old versions stay fetchable via proxy.golang.org" part is FALSE in practice.** `sgo` pins `kuery v1.23.0`; the remote now holds only v1.53.0–v1.57.0, and the proxy fallback did NOT work: `govulncheck ./...` and any build in `sgo` fail with `reading github.com/vukyn/kuery/go.mod at revision v1.23.0: unknown revision` → `could not import github.com/vukyn/kuery/query`. **`sgo` cannot build right now.** So tag pruning silently breaks any consumer that has not been bumped along with it. Before pruning, `go get` every consumer up to a surviving tag first — or accept that a stale consumer becomes unbuildable. Unfixed as of this note; `sgo` needs `go get github.com/vukyn/kuery@latest`.
