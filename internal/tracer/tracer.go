// Package tracer shells out to the img2svg python CLI to trace a raster image
// into an SVG. Image bytes go in via the CLI's stdin (pipe mode), SVG comes back
// on stdout — no temp files.
//
// The package also owns the resource limits that keep one upload from taking the
// whole machine down: a cap on the DECODED pixel count of the input, a cap on the
// SVG the subprocess may produce, and a cap on how many CLI subprocesses may run
// at once. They live here rather than in the HTTP handler so that every caller of
// Trace is covered by them.
//
// It owns one more thing, for the same reason: what a failed subprocess is
// allowed to say. Trace never returns the CLI's stderr, because a python
// traceback carries absolute host paths, source line numbers and interpreter
// internals out to an unauthenticated caller. The detail is logged here; callers
// get a sentinel.
package tracer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os/exec"
	"regexp"
	"time"

	// Format decoders, registered for their side effect so image.DecodeConfig can
	// read a header. These mirror the formats the CLI's magic-byte detection
	// accepts (png/jpg/gif/bmp/webp) — keep the two lists in step.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var (
	svgTagRe    = regexp.MustCompile(`<svg\b[^>]*>`)
	svgWidthRe  = regexp.MustCompile(`\bwidth="([0-9.]+)"`)
	svgHeightRe = regexp.MustCompile(`\bheight="([0-9.]+)"`)
)

// Resource limits on a single trace.
//
// The encoded upload is already bounded elsewhere (the HTTP body limit), but
// encoded size says nothing about decoded size: a flat-colour PNG compresses so
// well that a few hundred kilobytes on the wire becomes gigabytes of raw pixels
// once vtracer opens it. These bound what the subprocess will actually allocate.
const (
	// maxImagePixels is the ceiling on width×height. At 4 bytes per pixel a raw
	// RGBA buffer of this many pixels is ~160 MB, which the deployment's memory
	// can carry alongside the tracer's own working set.
	maxImagePixels = 40_000_000

	// maxImageDimension rejects extreme aspect ratios that slip under the pixel
	// budget — a 1×400,000,000 strip is only 400 megapixels of area but still an
	// absurd allocation, and nothing legitimate is this long on one side.
	maxImageDimension = 10_000

	// maxSVGBytes is the ceiling on what one trace may emit. The decoded-pixel cap
	// bounds the INPUT and says nothing about the output: a high-entropy image at
	// the faithful preset (filter_speckle=4) yields roughly a path per speckle, so
	// an in-budget upload can expand into a vastly larger SVG. A detailed piece of
	// art traces to about a megabyte, so this is a wide margin over anything
	// legitimate and only an adversarial input reaches it.
	//
	// ⚠️ It is a ceiling on the worst case, not a working-set estimate. A trace at
	// the ceiling holds the capture here and again in the response body, so the
	// deployment's memory divided by the concurrency cap is what makes this number
	// survivable — raising either without the other is what would not be.
	maxSVGBytes = 64 << 20
)

// Sentinel errors the HTTP layer maps onto status codes. They are returned
// wrapped with detail, so match them with errors.Is rather than ==.
var (
	// ErrImageTooLarge means the input decodes to more pixels than the budget
	// allows. Maps to 413.
	ErrImageTooLarge = errors.New("image exceeds the decoded-size limit")

	// ErrBusy means every trace slot is occupied. The work is deliberately NOT
	// queued — a queue behind a subprocess this expensive only converts a
	// rejection into a timeout for everybody. Maps to 503 + Retry-After.
	ErrBusy = errors.New("tracer is at capacity")

	// ErrOutputTooLarge means the trace produced more SVG than the ceiling allows.
	// Separate from ErrImageTooLarge on purpose: both map to 413, but conflating
	// them would tell a caller their input was too big when it was the output.
	ErrOutputTooLarge = errors.New("traced output exceeds the size limit")

	// ErrUnsupportedFormat is the one subprocess failure whose detail is worth
	// relaying. It is a statement about the bytes the caller uploaded, it says
	// nothing about the host, and it is the difference between a user fixing their
	// file and a user retrying a bad one forever. Maps to 422.
	//
	// ⚠️ The text mirrors the CLI's own message — keep the two in step, the same
	// way the quality presets are kept in step.
	ErrUnsupportedFormat = errors.New("unrecognized image format (want png/jpg/gif/bmp/webp)")

	// ErrTraceFailed is every other way the subprocess can fail, deliberately
	// collapsed into one content-free message. The detail — a python traceback
	// quoting absolute host paths and line numbers, or an exec error quoting the
	// interpreter path — is disclosure, and it is also how a prober confirms which
	// limit they just tripped. It is logged instead. Maps to 422.
	ErrTraceFailed = errors.New("trace failed")
)

// unsupportedFormatMarker is how the CLI announces the failure above, on stderr.
//
// ⚠️ Matching an allowlist of one is the point, and it must stay an allowlist. A
// rule that instead stripped the shapes we thought to name — a "Traceback"
// prefix, say — would still relay the next shape nobody named, and the CLI has
// other exits that quote host paths verbatim. Anything unrecognised is logged,
// never returned.
var unsupportedFormatMarker = []byte("unrecognized image format")

// cappedBuffer is an io.Writer that accumulates up to max bytes and REFUSES the
// write that would carry it past that, calling onExceeded instead of storing it.
//
// A capped Writer rather than an io.LimitReader over the subprocess's stdout
// pipe, for two reasons. It drops into exec's existing Stdout field, so there is
// no hand-rolled pipe / read-loop / Wait ordering to get wrong — and getting that
// ordering wrong is how a subprocess is left running with nobody draining it. And
// it can tell "exactly at the ceiling" from "past it", where a LimitReader
// truncates silently and hands back a short SVG indistinguishable from a small
// one, which would ship corrupt output under the name of a size limit.
//
// Refusing the whole overflowing write rather than storing a prefix of it is what
// makes the ceiling a stop rather than a report: the buffer never holds more than
// max, and the error propagates out of Cmd.Run.
type cappedBuffer struct {
	max int
	// onExceeded is called once, at the moment of overflow. It carries the
	// subprocess's context cancel: without it the producer keeps generating into a
	// pipe nobody drains, which is a stall rather than a stop.
	onExceeded func()

	data     []byte
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.exceeded || len(b.data)+len(p) > b.max {
		if !b.exceeded {
			b.exceeded = true
			if b.onExceeded != nil {
				b.onExceeded()
			}
		}
		return 0, ErrOutputTooLarge
	}

	if cap(b.data)-len(b.data) < len(p) {
		// Doubling growth, clamped at the ceiling. A bytes.Buffer would double
		// straight past it and end up holding an array of roughly twice the cap,
		// which is the allocation this type exists to bound.
		grown := 2*cap(b.data) + len(p)
		if grown > b.max {
			grown = b.max
		}
		next := make([]byte, len(b.data), grown)
		copy(next, b.data)
		b.data = next
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// Bytes returns what was captured. It is only meaningful when the buffer did not
// overflow; past the ceiling the content is a prefix and the caller must fail.
func (b *cappedBuffer) Bytes() []byte { return b.data }

// ensureViewBox injects a viewBox onto the root <svg> when vtracer emits only
// width/height. Without a viewBox the SVG has no coordinate scaling, so CSS
// max-width/max-height crops it instead of resizing — the preview clips tall
// or large images. Deriving viewBox from width/height makes it scale cleanly.
//
// It runs on the whole traced SVG at the moment that SVG is already in memory, so
// how it builds its result is a memory decision and not a style one: prefix +
// viewBox + suffix into one exactly-sized buffer, rather than bytes.Replace,
// which rescans the input for occurrences and allocates a grown result plus an
// intermediate tag on top of it.
//
// ⚠️ Two properties are load-bearing and any rewrite must keep both. Nothing is
// interpolated that did not come out of a [0-9.]+ capture, so no SVG content
// reaches the output unvalidated. And the result is a freshly allocated buffer,
// never a subslice of svg — appending onto svg[:n] would write the viewBox over
// the source's own bytes whenever svg has spare capacity, corrupting the input
// and the output with it.
func ensureViewBox(svg []byte) []byte {
	location := svgTagRe.FindIndex(svg)
	if location == nil {
		return svg
	}
	tag := svg[location[0]:location[1]]
	if bytes.Contains(tag, []byte("viewBox")) {
		return svg
	}
	width := svgWidthRe.FindSubmatch(tag)
	height := svgHeightRe.FindSubmatch(tag)
	if width == nil || height == nil {
		return svg
	}
	viewBox := fmt.Sprintf(` viewBox="0 0 %s %s"`, width[1], height[1])

	// Just before the ">" that closes the opening tag.
	insertAt := location[1] - 1
	out := make([]byte, 0, len(svg)+len(viewBox))
	out = append(out, svg[:insertAt]...)
	out = append(out, viewBox...)
	out = append(out, svg[insertAt:]...)
	return out
}

// Quality presets mirror the CLI's -q flag.
var validQuality = map[string]bool{
	"faithful": true,
	"balanced": true,
	"small":    true,
}

// checkPixelBudget reads only the image header — DecodeConfig never allocates the
// pixel buffer — and rejects inputs whose decoded form would be too big.
//
// ⚠️ It deliberately FAILS OPEN when the header cannot be parsed. The Go decoders
// registered above and the Rust decoders behind the CLI are different
// implementations and do not accept the same subsets — an RLE-compressed BMP, or
// one carrying the old OS/2 header, is a valid file the CLI traces happily and
// x/image/bmp refuses outright. Rejecting what we merely failed to MEASURE would
// turn a size guard into a format guard and refuse uploads that work today. An
// unmeasurable input therefore continues to the CLI, where it either traces
// normally or fails on its own error path, and the encoded body limit still
// bounds what it can be.
func checkPixelBudget(imageBytes []byte) error {
	config, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil {
		return nil
	}

	if config.Width > maxImageDimension || config.Height > maxImageDimension {
		return fmt.Errorf("%w: %dx%d exceeds the %d px limit on a single side",
			ErrImageTooLarge, config.Width, config.Height, maxImageDimension)
	}
	// int64 because the operands come from an attacker-supplied header.
	if pixels := int64(config.Width) * int64(config.Height); pixels > maxImagePixels {
		return fmt.Errorf("%w: %dx%d is %d pixels, over the %d limit",
			ErrImageTooLarge, config.Width, config.Height, pixels, maxImagePixels)
	}
	return nil
}

type Tracer struct {
	pythonBin  string // e.g. "python3"
	scriptPath string // path to cli/img2svg.py
	timeout    time.Duration
	// sem is a counting semaphore over concurrent CLI subprocesses. Acquisition is
	// non-blocking on purpose: a full channel is an immediate ErrBusy, never a wait.
	sem chan struct{}
}

// New builds a Tracer. maxConcurrent bounds how many CLI subprocesses may run at
// once and should track the machine's CPU allowance — each trace is a whole
// python interpreter plus a vtracer run, so oversubscribing turns every request
// slow instead of making any of them fast. Values below 1 are clamped to 1, since
// a zero-capacity semaphore would reject every request.
func New(pythonBin, scriptPath string, timeout time.Duration, maxConcurrent int) *Tracer {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Tracer{
		pythonBin:  pythonBin,
		scriptPath: scriptPath,
		timeout:    timeout,
		sem:        make(chan struct{}, maxConcurrent),
	}
}

// Timeout is how long a single trace may run. It doubles as the hint for how long
// a caller turned away with ErrBusy should wait, since that is the longest an
// occupied slot can stay occupied.
func (t *Tracer) Timeout() time.Duration {
	return t.timeout
}

// Trace pipes image bytes to the python CLI and returns the SVG bytes.
func (t *Tracer) Trace(ctx context.Context, imageBytes []byte, quality string) ([]byte, error) {
	// Cheap rejections first: nothing that is going to be refused anyway should
	// ever hold a trace slot.
	if len(imageBytes) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if !validQuality[quality] {
		return nil, fmt.Errorf("invalid quality %q (want faithful|balanced|small)", quality)
	}
	if err := checkPixelBudget(imageBytes); err != nil {
		return nil, err
	}

	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	default:
		return nil, fmt.Errorf("%w: %d traces already running", ErrBusy, cap(t.sem))
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// pipe mode: "img2svg.py - -q <quality>" reads stdin, writes SVG to stdout
	command := exec.CommandContext(ctx, t.pythonBin, t.scriptPath, "-", "-q", quality)
	command.Stdin = bytes.NewReader(imageBytes)

	// Handing cancel to the capture is what makes the output ceiling a stop: on
	// overflow the context is cancelled at that instant, CommandContext kills the
	// subprocess, and the producer stops producing. Detecting the overflow after
	// Run returned would mean the bytes had already been generated and the pipe
	// had already been drained into memory.
	stdout := &cappedBuffer{max: maxSVGBytes, onExceeded: cancel}
	var stderr bytes.Buffer
	command.Stdout = stdout
	command.Stderr = &stderr

	runErr := command.Run()

	// Before runErr, because an overflow surfaces as whatever the kill happened to
	// look like — a signal, a closed pipe, a copy error — and reading the cause off
	// the exit status would be guessing at it.
	if stdout.exceeded {
		return nil, fmt.Errorf("%w: a trace may produce at most %d bytes of SVG", ErrOutputTooLarge, maxSVGBytes)
	}

	if runErr != nil {
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			return nil, fmt.Errorf("trace timed out after %s", t.timeout)
		case bytes.Contains(stderr.Bytes(), unsupportedFormatMarker):
			return nil, ErrUnsupportedFormat
		default:
			// Logged, never returned. This is the branch a python traceback lands
			// in, and it is the reason the returned error is a bare sentinel.
			log.Printf("tracer: subprocess failed: %v: stderr: %s", runErr, stderr.Bytes())
			return nil, ErrTraceFailed
		}
	}
	return ensureViewBox(stdout.Bytes()), nil
}
