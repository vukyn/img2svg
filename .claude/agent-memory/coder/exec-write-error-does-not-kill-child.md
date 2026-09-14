---
name: exec-write-error-does-not-kill-child
description: os/exec — returning an error from your Cmd.Stdout io.Writer stops the copy but does NOT kill the subprocess; a size cap on captured output must cancel the context too
metadata:
  type: feedback
---

When `Cmd.Stdout` is an `io.Writer` that is not an `*os.File`, `os/exec` opens a
pipe and runs `io.Copy(yourWriter, pipeReader)` in a goroutine. If your `Write`
returns an error, `io.Copy` stops and the goroutine closes the read end — **and
that is all it does.** The child is not signalled. It learns about it only on its
*next* write to the pipe, as EPIPE/SIGPIPE. A child that has stopped writing —
sleeping, doing CPU work, waiting on something — never finds out, and `Cmd.Run`
blocks until it exits on its own.

So a bounded-output guard built only out of a capped `io.Writer` **detects** the
overflow without **stopping** the producer. Hand the writer the `cancel` from
`exec.CommandContext`'s context and call it at the moment of overflow; that is
what turns the ceiling into a stop.

**Why:** measured on img2svg 2026-09-13 (PR #22). A stub CLI wrote past a 64 MB
ceiling and then `sleep 30`. With the cancel wired in, the request answered 413 in
well under a second. With the cancel removed and everything else identical, the
status was still going to be 413 — but the call sat there until the test harness
gave up at 10 s, because `Run` was still waiting on a subprocess nobody was
reading from any more. The status assertion alone would have passed the mutation.

**How to apply:** any time you cap, filter or reject what a subprocess writes,
ask what stops the subprocess — the answer is never the `Write` error. Two
corollaries worth keeping:

- **Time the call in the test, don't just assert the status.** The orphan and the
  clean stop produce the *same* status code; only elapsed time separates them.
  Past the harness's own window it surfaces as `app.Test: timeout` rather than as
  your assertion, which still fails red but reads confusingly — say so in the
  test comment.
- **Refuse the overflowing write entirely; never store a prefix of it.** Storing
  part of it makes the cap a measurement taken after the growth it was supposed
  to prevent. Returning `(0, err)` is what keeps the buffer at or under the
  ceiling.
- Reading `cmd.Stdout`'s state after `Run()` returns is race-free: `Wait` joins
  the copy goroutines, so there is a happens-before. Confirmed under `-race`.

Related: [[feedback-prove-regression-tests]],
[[crg-graph-skips-untracked-files]].
