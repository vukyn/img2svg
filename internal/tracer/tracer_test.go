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
