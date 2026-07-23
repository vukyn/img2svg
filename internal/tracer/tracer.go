// Package tracer shells out to the img2svg python CLI to trace a raster image
// into an SVG. Image bytes go in via the CLI's stdin (pipe mode), SVG comes back
// on stdout — no temp files.
package tracer

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"time"
)

var (
	svgTagRe    = regexp.MustCompile(`<svg\b[^>]*>`)
	svgWidthRe  = regexp.MustCompile(`\bwidth="([0-9.]+)"`)
	svgHeightRe = regexp.MustCompile(`\bheight="([0-9.]+)"`)
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

type Tracer struct {
	pythonBin string        // e.g. "python3"
	scriptPath string       // path to cli/img2svg.py
	timeout    time.Duration
}

func New(pythonBin, scriptPath string, timeout time.Duration) *Tracer {
	return &Tracer{pythonBin: pythonBin, scriptPath: scriptPath, timeout: timeout}
}

// Trace pipes image bytes to the python CLI and returns the SVG bytes.
func (t *Tracer) Trace(ctx context.Context, image []byte, quality string) ([]byte, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if !validQuality[quality] {
		return nil, fmt.Errorf("invalid quality %q (want faithful|balanced|small)", quality)
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// pipe mode: "img2svg.py - -q <quality>" reads stdin, writes SVG to stdout
	command := exec.CommandContext(ctx, t.pythonBin, t.scriptPath, "-", "-q", quality)
	command.Stdin = bytes.NewReader(image)

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
