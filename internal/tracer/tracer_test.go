package tracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fixtures -------------------------------------------------------------

// stubCLI writes a shell script standing in for the python CLI and returns the
// interpreter + script path to hand to New. The real CLI is invoked as
// "<bin> <script> - -q <quality>", and sh ignores the trailing arguments, so a
// script substitutes cleanly for it without needing python or vtracer installed.
func stubCLI(t *testing.T, body string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub-cli.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write stub CLI: %v", err)
	}
	return "/bin/sh", path
}

// echoingCLI is a stub that always succeeds with a minimal SVG.
const echoingCLI = `printf '<svg width="10" height="10"></svg>'`

// pngHeader builds a PNG carrying nothing but a valid signature and IHDR chunk
// declaring the given dimensions. That is all image.DecodeConfig reads for a
// truecolor image, so a header claiming hundreds of megapixels costs ~45 bytes —
// which is the whole point of the decoded-size cap: encoded size is no evidence
// about decoded size.
func pngHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	chunk := make([]byte, 0, 17)
	chunk = append(chunk, 'I', 'H', 'D', 'R')
	chunk = binary.BigEndian.AppendUint32(chunk, width)
	chunk = binary.BigEndian.AppendUint32(chunk, height)
	// bit depth 8, colour type 2 (truecolor), deflate, adaptive filtering, no interlace
	chunk = append(chunk, 8, 2, 0, 0, 0)

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	if err := binary.Write(&out, binary.BigEndian, uint32(len(chunk)-4)); err != nil {
		t.Fatalf("write chunk length: %v", err)
	}
	out.Write(chunk)
	if err := binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(chunk)); err != nil {
		t.Fatalf("write chunk crc: %v", err)
	}
	return out.Bytes()
}

// --- decoded-pixel cap ----------------------------------------------------

// TestCheckPixelBudget pins the cap's three outcomes directly: measurable and
// within budget passes, measurable and over budget is rejected, and unmeasurable
// passes (fail-open — see the doc comment on checkPixelBudget for why).
func TestCheckPixelBudget(t *testing.T) {
	// Sanity-check the synthetic header against a real encoder's output first, so
	// a broken fixture cannot make the over-budget cases pass for the wrong reason.
	var realPNG bytes.Buffer
	if err := png.Encode(&realPNG, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatalf("encode reference png: %v", err)
	}

	testCases := []struct {
		name       string
		imageBytes []byte
		wantReject bool
	}{
		{
			name:       "a real small png is measurable and within budget",
			imageBytes: realPNG.Bytes(),
		},
		{
			name:       "just under the pixel budget passes",
			imageBytes: pngHeader(t, 5_000, 8_000), // 40,000,000 px — exactly the limit
		},
		{
			name:       "over the pixel budget is rejected",
			imageBytes: pngHeader(t, 8_000, 8_000), // 64,000,000 px, both sides legal
			wantReject: true,
		},
		{
			name: "over the per-side limit is rejected even when the area is small",
			// 10,001 x 100 is only ~1 megapixel, so it clears the pixel budget and can
			// only be caught by the dimension check.
			imageBytes: pngHeader(t, 10_001, 100),
			wantReject: true,
		},
		{
			name:       "an unmeasurable format is allowed through rather than refused",
			imageBytes: []byte("\x00\x01NOTANIMAGEFORMAT-but-vtracer-might-know-it"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := checkPixelBudget(testCase.imageBytes)
			if testCase.wantReject {
				if !errors.Is(err, ErrImageTooLarge) {
					t.Fatalf("want ErrImageTooLarge, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want the input accepted, got %v", err)
			}
		})
	}
}

// TestOversizeImageIsRejectedBeforeSubprocess proves the cap runs BEFORE the CLI
// is spawned. A cap that rejects after the subprocess has already opened the
// image protects nothing: the allocation it exists to prevent has happened.
func TestOversizeImageIsRejectedBeforeSubprocess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	pythonBin, scriptPath := stubCLI(t, "touch "+marker+"\n"+echoingCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 2)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 20_000, 20_000), "faithful")
	if !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("want ErrImageTooLarge, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the CLI was spawned for an oversize image: the cap runs too late to prevent the allocation")
	}

	// The same tracer must still run an in-budget image, so the assertion above is
	// about the cap and not about a stub that never works.
	svg, err := tracer.Trace(context.Background(), pngHeader(t, 100, 100), "faithful")
	if err != nil {
		t.Fatalf("in-budget trace: %v", err)
	}
	if !bytes.Contains(svg, []byte("<svg")) {
		t.Fatalf("want SVG output, got %q", svg)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the CLI should have been spawned for an in-budget image: %v", statErr)
	}
}

// TestUnmeasurableFormatReachesTheCLI is the fail-open decision as behaviour: an
// input the Go decoders cannot read must still be handed to the CLI, because the
// tracer behind it accepts formats they do not and refusing there would turn a
// size guard into a format guard.
func TestUnmeasurableFormatReachesTheCLI(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	pythonBin, scriptPath := stubCLI(t, "touch "+marker+"\n"+echoingCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 2)

	_, err := tracer.Trace(context.Background(), []byte("\x00\x01NOTANIMAGEFORMAT"), "faithful")
	if err != nil {
		t.Fatalf("an unmeasurable input must reach the CLI, not be rejected here: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the CLI should have been spawned: %v", statErr)
	}
}

// --- concurrency cap ------------------------------------------------------

// gatedCLI returns a stub that announces itself and then blocks until a release
// file appears, so a test can hold trace slots open for as long as it needs
// without sleeping for a guessed duration.
func gatedCLI(t *testing.T, gateDir string) (string, string) {
	t.Helper()
	return stubCLI(t, `touch "`+gateDir+`/started.$$"
while [ ! -f "`+gateDir+`/release" ]; do sleep 0.01; done
`+echoingCLI)
}

func countStarted(t *testing.T, gateDir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(gateDir, "started.*"))
	if err != nil {
		t.Fatalf("glob started markers: %v", err)
	}
	return len(matches)
}

// TestSaturatedTracerRejectsInsteadOfQueueing covers both halves of the
// concurrency cap: a request arriving with every slot busy is refused with
// ErrBusy, and it is refused IMMEDIATELY. The second half is the one that
// matters — an unbounded queue in front of a subprocess this expensive does not
// prevent the overload, it just converts a rejection into a timeout for
// everybody, including the callers already being served.
func TestSaturatedTracerRejectsInsteadOfQueueing(t *testing.T) {
	const maxConcurrent = 2

	gateDir := t.TempDir()
	pythonBin, scriptPath := gatedCLI(t, gateDir)
	// A generous timeout, so a slow rejection cannot be mistaken for a fast one:
	// anything that queued would be stuck here for the full hold.
	tracer := New(pythonBin, scriptPath, 30*time.Second, maxConcurrent)

	var holders sync.WaitGroup
	for range maxConcurrent {
		holders.Add(1)
		go func() {
			defer holders.Done()
			if _, err := tracer.Trace(context.Background(), pngHeader(t, 100, 100), "faithful"); err != nil {
				t.Errorf("a holder trace failed: %v", err)
			}
		}()
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(gateDir, "release"), nil, 0o600)
		holders.Wait()
	})

	// Wait for every slot to be genuinely occupied before probing.
	deadline := time.Now().Add(10 * time.Second)
	for countStarted(t, gateDir) < maxConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d stub CLIs started", countStarted(t, gateDir), maxConcurrent)
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	_, err := tracer.Trace(context.Background(), pngHeader(t, 100, 100), "faithful")
	elapsed := time.Since(start)

	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy when every slot is occupied, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("the rejection took %s: that is a queue, not a refusal", elapsed)
	}
	if started := countStarted(t, gateDir); started != maxConcurrent {
		t.Fatalf("the refused request spawned a CLI anyway: %d started, want %d", started, maxConcurrent)
	}

	// Releasing the holders must return their slots, or the cap is a one-way latch.
	if err := os.WriteFile(filepath.Join(gateDir, "release"), nil, 0o600); err != nil {
		t.Fatalf("release holders: %v", err)
	}
	holders.Wait()

	if _, err := tracer.Trace(context.Background(), pngHeader(t, 100, 100), "faithful"); err != nil {
		t.Fatalf("a slot must be reusable once its trace finishes: %v", err)
	}
}

// TestCheapRejectionsDoNotConsumeASlot makes sure input that was going to be
// refused anyway cannot exhaust the capacity that real work needs — otherwise
// the concurrency cap becomes the cheapest way to deny the service.
func TestCheapRejectionsDoNotConsumeASlot(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 1)

	for range 5 {
		if _, err := tracer.Trace(context.Background(), nil, "faithful"); err == nil {
			t.Fatal("an empty image must be refused")
		}
		if _, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "enormous"); err == nil {
			t.Fatal("an unknown quality must be refused")
		}
		if _, err := tracer.Trace(context.Background(), pngHeader(t, 20_000, 20_000), "faithful"); !errors.Is(err, ErrImageTooLarge) {
			t.Fatal("an oversize image must be refused")
		}
	}

	if _, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful"); err != nil {
		t.Fatalf("the single slot must still be free after refused requests: %v", err)
	}
}

// TestNewClampsConcurrency guards the footgun in a counting semaphore built from
// a buffered channel: at capacity zero the channel is unbuffered, so a
// non-blocking send never succeeds and every request would be refused.
func TestNewClampsConcurrency(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)

	for _, maxConcurrent := range []int{-1, 0} {
		tracer := New(pythonBin, scriptPath, 5*time.Second, maxConcurrent)
		if _, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful"); err != nil {
			t.Fatalf("maxConcurrent=%d must be clamped to a usable capacity, got %v", maxConcurrent, err)
		}
	}
}

// TestTimeoutIsReportedForRetryHints pins the accessor the HTTP layer turns into
// a Retry-After value on a 503.
func TestTimeoutIsReportedForRetryHints(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	if got := New(pythonBin, scriptPath, 20*time.Second, 2).Timeout(); got != 20*time.Second {
		t.Fatalf("Timeout() = %s, want 20s", got)
	}
}

// --- existing behaviour ---------------------------------------------------

func TestEnsureViewBoxIsPreserved(t *testing.T) {
	withoutViewBox := []byte(`<svg width="120" height="80" xmlns="http://www.w3.org/2000/svg"></svg>`)
	if got := ensureViewBox(withoutViewBox); !bytes.Contains(got, []byte(`viewBox="0 0 120 80"`)) {
		t.Fatalf("want an injected viewBox, got %s", got)
	}

	withViewBox := []byte(`<svg width="120" height="80" viewBox="0 0 1 1"></svg>`)
	if got := ensureViewBox(withViewBox); !bytes.Equal(got, withViewBox) {
		t.Fatalf("an existing viewBox must be left alone, got %s", got)
	}
}

func TestStubCLIScriptIsSelfConsistent(t *testing.T) {
	// The stub is load-bearing for every test above; a silent failure to run it
	// would make "the CLI was not spawned" assertions vacuously true.
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 1)
	svg, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if err != nil {
		t.Fatalf("stub CLI must run: %v", err)
	}
	if !strings.Contains(string(svg), "<svg") {
		t.Fatalf("stub CLI output: %q", svg)
	}
}

// --- what a failed subprocess is allowed to say ---------------------------

// tracebackCLI stands in for the CLI dying on an uncaught python exception. The
// shape is the real one: vtracer raises, python prints a traceback naming the
// absolute script path and the line it died on, and the process exits non-zero.
const tracebackCLI = `printf 'Traceback (most recent call last):\n  File "/srv/img2svg/cli/img2svg.py", line 163, in <module>\n    main()\n  File "/srv/img2svg/cli/img2svg.py", line 129, in main\n    sys.stdout.write(trace_bytes(data, args.quality))\nException: Failed to decode img_bytes.\n' >&2
exit 1`

// unknownFormatCLI stands in for the CLI's own sys.exit on an input whose format
// it cannot identify: one line on stderr, exit 1, no traceback.
const unknownFormatCLI = `echo 'unrecognized image format (want png/jpg/gif/bmp/webp)' >&2
exit 1`

// TestSubprocessTracebackIsNotReturnedToTheCaller is the disclosure fix as
// behaviour. The tracer used to fold stderr into the returned error, and the
// handler sends that error to an unauthenticated client — so a python traceback
// carrying absolute host paths, source line numbers and interpreter internals was
// a response body. Nothing of it may survive into the error.
func TestSubprocessTracebackIsNotReturnedToTheCaller(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, tracebackCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 1)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if !errors.Is(err, ErrTraceFailed) {
		t.Fatalf("want ErrTraceFailed, got %v", err)
	}

	// Named individually rather than as one blob, so a failure says which piece of
	// the host leaked.
	for _, leak := range []string{"Traceback", "/srv/img2svg", "line 163", "sys.stdout", "img_bytes", "main()"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("the returned error carries %q from the subprocess: %q", leak, err)
		}
	}
	if err.Error() != ErrTraceFailed.Error() {
		t.Fatalf("the message must be the fixed sentinel, got %q", err)
	}
}

// TestUnrecognizedFormatStillReachesTheCaller is the other half, and the half a
// blanket "never say anything" fix would have broken: this one diagnostic is
// about the bytes the caller uploaded, discloses nothing about the host, and is
// the difference between fixing a bad file and retrying it forever.
func TestUnrecognizedFormatStillReachesTheCaller(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, unknownFormatCLI)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 1)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("want ErrUnsupportedFormat, got %v", err)
	}
	if !strings.Contains(err.Error(), "unrecognized image format") {
		t.Fatalf("the caller needs to be told what was wrong with their file, got %q", err)
	}
}

// TestTraceFailureClassificationIsAnAllowlist pins the direction of the rule. An
// exit that merely avoids looking like a traceback must still be suppressed —
// the CLI has other exits that quote host paths, and a rule built from the shapes
// we happened to think of relays the next one nobody thought of.
func TestTraceFailureClassificationIsAnAllowlist(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, `echo 'no such file: /srv/img2svg/cli/img2svg.py' >&2
exit 1`)
	tracer := New(pythonBin, scriptPath, 5*time.Second, 1)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if !errors.Is(err, ErrTraceFailed) {
		t.Fatalf("want ErrTraceFailed for an unrecognised diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), "/srv/img2svg") {
		t.Fatalf("a single-line stderr is still stderr: %q", err)
	}
}

// TestMissingCLIIsNotReportedVerbatim covers the misconfiguration path, where the
// leak is the interpreter and script path rather than a traceback.
func TestMissingCLIIsNotReportedVerbatim(t *testing.T) {
	tracer := New("/bin/sh", "/srv/img2svg/cli/definitely-not-here.py", 5*time.Second, 1)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if !errors.Is(err, ErrTraceFailed) {
		t.Fatalf("want ErrTraceFailed, got %v", err)
	}
	if strings.Contains(err.Error(), "definitely-not-here") {
		t.Fatalf("the deploy layout leaked through a misconfiguration: %q", err)
	}
}

// --- the output ceiling ---------------------------------------------------

// TestCappedBufferRefusesTheOverflowingWrite covers the mechanism directly: the
// write that would cross the ceiling stores NOTHING and reports the overflow, so
// the capture stops growing at the moment of overflow rather than being measured
// after it.
func TestCappedBufferRefusesTheOverflowingWrite(t *testing.T) {
	var cancellations int
	buffer := &cappedBuffer{max: 10, onExceeded: func() { cancellations++ }}

	if written, err := buffer.Write([]byte("123456")); written != 6 || err != nil {
		t.Fatalf("a write within the cap: wrote %d, err %v", written, err)
	}
	if written, err := buffer.Write([]byte("78901")); written != 0 || !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("the overflowing write: wrote %d, err %v", written, err)
	}
	if got := string(buffer.Bytes()); got != "123456" {
		t.Fatalf("the refused write was stored anyway: %q", got)
	}
	if !buffer.exceeded {
		t.Fatal("the overflow was not recorded, so Trace cannot fail on it")
	}

	// Cancelling the producer is what makes this a stop rather than a report, and
	// it must happen once — cancel is idempotent, but a per-write callback would
	// mean the accounting is wrong.
	if cancellations != 1 {
		t.Fatalf("onExceeded ran %d times, want 1", cancellations)
	}
	if _, err := buffer.Write([]byte("x")); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("writes after an overflow must keep failing, got %v", err)
	}
	if cancellations != 1 {
		t.Fatalf("onExceeded ran again on a later write: %d", cancellations)
	}
}

// TestCappedBufferNeverAllocatesPastTheCap is the reason this is not a
// bytes.Buffer. Doubling growth overshoots: a buffer filled to a 1000-byte
// ceiling in small writes would end up sitting on a 1023-byte array, and at the
// real ceiling that overshoot is tens of megabytes of exactly the allocation the
// cap exists to bound.
func TestCappedBufferNeverAllocatesPastTheCap(t *testing.T) {
	const max = 1000
	buffer := &cappedBuffer{max: max}

	for written := 0; written < max; written++ {
		if _, err := buffer.Write([]byte{'x'}); err != nil {
			t.Fatalf("byte %d of %d: %v", written, max, err)
		}
	}
	if got := len(buffer.Bytes()); got != max {
		t.Fatalf("captured %d bytes, want %d", got, max)
	}
	if got := cap(buffer.Bytes()); got > max {
		t.Fatalf("the backing array grew to %d for a %d-byte ceiling", got, max)
	}
	if _, err := buffer.Write([]byte{'x'}); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("a buffer filled to exactly the cap must refuse the next byte, got %v", err)
	}
}

// TestOutputCeilingIsWiredIntoTrace proves the ceiling is actually applied to the
// subprocess's stdout, with a tracer whose cap is the production one — the stub
// emits just over it.
func TestOutputCeilingIsWiredIntoTrace(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates the output ceiling")
	}
	pythonBin, scriptPath := stubCLI(t, oversizeOutputCLI(t.TempDir()))
	tracer := New(pythonBin, scriptPath, 30*time.Second, 1)

	_, err := tracer.Trace(context.Background(), pngHeader(t, 10, 10), "faithful")
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
}

// oversizeOutputCLI is a stub that writes more SVG than the ceiling allows and
// then sleeps. The sleep is the assertion in tests that time the call: if the
// overflow does not cancel the subprocess, the caller waits it out instead of
// being told, which is the difference between stopping the producer and merely
// noticing it produced too much.
func oversizeOutputCLI(markerDir string) string {
	return `head -c ` + strconv.Itoa(maxSVGBytes+(4<<20)) + ` /dev/zero | tr '\0' 'x'
sleep 30
touch "` + markerDir + `/finished"`
}

// TestOutputCeilingFitsTheDeploymentBudget is the arithmetic the ceiling is only
// safe because of, written down where a change to it fails.
//
// The ceiling is not a number on its own: a trace at it holds the capture in this
// package AND again in the response body fasthttp copies, and that happens once
// per concurrent trace. So what has to fit the machine is
// ceiling × copies × concurrency, and a change to any of the three is a change to
// all of it — which is exactly the reasoning that is easy to lose when only one of
// them is being edited.
//
// The headroom requirement is half the machine rather than all of it because the
// other half is not free: two python interpreters, two vtracer working sets, the
// decoded upload and the Go runtime all live in it.
func TestOutputCeilingFitsTheDeploymentBudget(t *testing.T) {
	const (
		// fly.toml: [[vm]] memory = '512mb'.
		deploymentBytes = 512 << 20
		// The capture here, and the response body copied from it.
		copiesPerTrace = 2
		// ⚠️ Mirrors maxConcurrentTraces in cmd/server. Deliberately duplicated:
		// the coupling between the two packages is the thing under test, and a
		// test that imported the real value would silently follow it upward.
		deployedConcurrency = 2
	)

	worstCase := maxSVGBytes * copiesPerTrace * deployedConcurrency
	if headroom := deploymentBytes / 2; worstCase >= headroom {
		t.Fatalf("worst case is %d MB (%d MB ceiling × %d copies × %d traces), which leaves under half of the %d MB machine for the interpreters, vtracer and the runtime",
			worstCase>>20, maxSVGBytes>>20, copiesPerTrace, deployedConcurrency, deploymentBytes>>20)
	}
}

// --- ensureViewBox --------------------------------------------------------

// TestEnsureViewBoxOutputIsUnchanged pins the exact bytes for every branch, so a
// rewrite made for memory reasons cannot quietly change what is emitted. The
// expectations are the pre-rewrite output, character for character.
func TestEnsureViewBoxOutputIsUnchanged(t *testing.T) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "width and height, no viewBox",
			in:   `<svg width="120" height="80" xmlns="http://www.w3.org/2000/svg"></svg>`,
			want: `<svg width="120" height="80" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 80"></svg>`,
		},
		{
			name: "an existing viewBox is left alone",
			in:   `<svg width="120" height="80" viewBox="0 0 1 1"></svg>`,
			want: `<svg width="120" height="80" viewBox="0 0 1 1"></svg>`,
		},
		{
			name: "content before the root tag is preserved",
			in:   "<?xml version=\"1.0\"?>\n<svg width=\"4\" height=\"2\"><path d=\"M0 0\"/></svg>",
			want: "<?xml version=\"1.0\"?>\n<svg width=\"4\" height=\"2\" viewBox=\"0 0 4 2\"><path d=\"M0 0\"/></svg>",
		},
		{
			name: "fractional dimensions",
			in:   `<svg width="12.5" height="8.25"></svg>`,
			want: `<svg width="12.5" height="8.25" viewBox="0 0 12.5 8.25"></svg>`,
		},
		{
			name: "no dimensions to derive from",
			in:   `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
			want: `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
		},
		{
			// The captures are [0-9.]+ and anchored to the closing quote, so a
			// dimension carrying a unit does not match and nothing is injected.
			// That is the validation the interpolation rests on: only digits and
			// dots ever reach the output.
			name: "a unit suffix is not interpolated",
			in:   `<svg width="120px" height="80px"></svg>`,
			want: `<svg width="120px" height="80px"></svg>`,
		},
		{
			name: "not an SVG at all",
			in:   `this is not markup`,
			want: `this is not markup`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := string(ensureViewBox([]byte(testCase.in))); got != testCase.want {
				t.Fatalf("ensureViewBox:\n got  %s\n want %s", got, testCase.want)
			}
		})
	}
}

// TestEnsureViewBoxDoesNotAliasItsInput guards the property the old three-index
// slice existed for, which a rewrite is exactly the moment to lose. A traced SVG
// arrives in a grown buffer with spare capacity, and appending onto a subslice of
// a buffer that has room writes into that buffer rather than allocating — so the
// obvious way to build the result corrupts the source it is still reading from.
func TestEnsureViewBoxDoesNotAliasItsInput(t *testing.T) {
	// Spare capacity is the whole hazard; without it every append allocates and
	// the bug cannot reproduce.
	source := make([]byte, 0, 4096)
	source = append(source, `<svg width="120" height="80"><path d="M0 0"/></svg>`...)
	before := string(source)

	out := ensureViewBox(source)

	if string(source) != before {
		t.Fatalf("ensureViewBox wrote into its own input:\n before %s\n after  %s", before, source)
	}
	if len(out) > 0 && len(source) > 0 && &out[0] == &source[0] {
		t.Fatal("the result shares storage with the input")
	}

	// A later append to the result must not reach back into the source either.
	out = append(out, "<!-- appended by a caller -->"...)
	if string(source) != before {
		t.Fatalf("appending to the result wrote into the input:\n before %s\n after  %s", before, source)
	}
}
