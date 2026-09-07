---
name: mutate-the-producer-not-just-the-logic
description: "Mutation-test the PRODUCER (does the caller actually pass the value?), not only the consumer logic — this gap appeared twice in one session, both times on the most dangerous property"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 46df600c-9e93-4b9d-ac14-8da77bea20c1
  modified: 2026-08-12T10:31:34.872Z
---

When a feature threads a value from producers into a decision, mutation-testing the *decision* leaves the *producers* unpinned. Both misses in one session were on the highest-stakes property in their PR, and both looked fully covered:

- **medioa2 #93** — the reaper's two gates were mutation-verified, but adding `UploadPending: true` to the **single-shot** `Create` compiled and **zero tests failed**. Single-shot has no multipart, so the reaper takes the never-staged branch and deletes the record with no abort gate: a refactor merging the create paths would silently make every ordinary file a deletion candidate, suite still green.
- **rainy #325** — the pre-flight logic was covered, but mutating `TotalSize` to `0` in the HTTP handler and in both worker call sites produced **0 failures**. A size read but never forwarded looks identical in the source.

**Why:** A value that is read and discarded is indistinguishable from one that is used, by reading the code. Only a test that captures what the callee received can tell.

**How to apply:**
- After verifying the logic, enumerate **every write site** of the new field (`grep` the struct literal — #93 had exactly 3) and mutate each one. If a named test doesn't fail, the producer is unpinned.
- Assert on the **captured request** in a stub (`capturingUsecase`, `singleShotStorageRepo`), never indirectly through the consumer. Asserting a producer through the consumer is circular when the consumer is the thing being starved of input.
- Prefer a structural fix over a test when one exists: #93 ended up adding a second reaper gate (empty-Token) so the mutation became *harmless*, not just *detected*.

**Mutations that fail to COMPILE prove nothing.** Twice a mutation left a variable unused (`cfg`, `err`) → build error, no test result, false confidence. Adjust until it compiles.

⚠️ **And a scripted mutation harness will report a compile failure as an ESCAPE unless it recognises the message.** Measured while writing `internal/draft`: a harness classifying a run as "caught" or "NOBODY CAUGHT IT" by grepping stderr for `cannot use` / `undefined` / `declared and not used` missed `"slices" imported and not used` — which is what mutating the body of `Has` from `slices.ContainsFunc(...)` to a bare `return false` produces — and printed a clean *escape* for a mutation that never built. The redraft that kept the import used (`return false` **inside** the predicate) was caught immediately. So a harness must decide on the **exit status of `go build`** before it looks at test output, not on a list of error strings; and any "escape" a harness reports has to be re-run by hand before it is believed, because a false escape costs a whole test-writing cycle aimed at nothing.

**Never restore a mutation with `git checkout <file>`** — it discards all other uncommitted work in that file. It silently wiped a finished handler + helper once. Copy the file aside and copy it back. See [[verify-committer-staged-files]], [[medioa-early-size-rejection]].
