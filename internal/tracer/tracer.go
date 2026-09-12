// Package tracer shells out to the img2svg python CLI to trace a raster image
// into an SVG. Image bytes go in via the CLI's stdin (pipe mode), SVG comes back
// on stdout — no temp files.
//
// The package also owns the two resource limits that keep one upload from taking
// the whole machine down: a cap on the DECODED pixel count of the input, and a
// cap on how many CLI subprocesses may run at once. Both live here rather than in
// the HTTP handler so that every caller of Trace is covered by them.
package tracer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
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
)

// ensureViewBox injects a viewBox onto the root <svg> when vtracer emits only
// width/height. Without a viewBox the SVG has no coordinate scaling, so CSS
// max-width/max-height crops it instead of resizing — the preview clips tall
// or large images. Deriving viewBox from width/height makes it scale cleanly.
func ensureViewBox(svg []byte) []byte {
	tag := svgTagRe.Find(svg)
	if tag == nil || bytes.Contains(tag, []byte("viewBox")) {
		return svg
	}
	width := svgWidthRe.FindSubmatch(tag)
	height := svgHeightRe.FindSubmatch(tag)
	if width == nil || height == nil {
		return svg
	}
	viewBox := fmt.Sprintf(` viewBox="0 0 %s %s">`, width[1], height[1])
	newTag := append(tag[:len(tag)-1:len(tag)-1], []byte(viewBox)...)
	return bytes.Replace(svg, tag, newTag, 1)
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

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("trace timed out after %s", t.timeout)
		}
		return nil, fmt.Errorf("trace failed: %v: %s", err, stderr.String())
	}
	return ensureViewBox(stdout.Bytes()), nil
}
