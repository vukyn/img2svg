package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/vukyn/img2svg/internal/tracer"
)

// proxyHeader is the header a fronting proxy puts the real client address in.
// Its production value comes from the PROXY_HEADER env var (fly.toml sets it);
// the tests name it directly so they exercise the same code path.
const proxyHeader = "Fly-Client-IP"

// --- fixtures -------------------------------------------------------------

// stubCLI substitutes a shell script for the python CLI, so the HTTP tests can
// run without python or vtracer on the host. The CLI is invoked as
// "<bin> <script> - -q <quality>" and sh ignores the trailing arguments.
func stubCLI(t *testing.T, body string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub-cli.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write stub CLI: %v", err)
	}
	return "/bin/sh", path
}

const echoingCLI = `printf '<svg width="10" height="10"></svg>'`

// pngHeader builds a PNG that is nothing but a signature and an IHDR chunk
// declaring the given dimensions — all image.DecodeConfig reads for a truecolor
// image. A few dozen bytes on the wire can therefore claim any decoded size,
// which is exactly the case the decoded-pixel cap exists for.
func pngHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	chunk := make([]byte, 0, 17)
	chunk = append(chunk, 'I', 'H', 'D', 'R')
	chunk = binary.BigEndian.AppendUint32(chunk, width)
	chunk = binary.BigEndian.AppendUint32(chunk, height)
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

// newTraceRequest builds a multipart POST to /api/trace. A nil imageBytes omits
// the file part entirely, which the handler refuses before it reaches the
// tracer — useful for driving the rate limiter without spawning subprocesses.
// clientIP, when non-empty, is sent as the proxy's client-address header.
func newTraceRequest(t *testing.T, imageBytes []byte, clientIP string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("quality", "faithful"); err != nil {
		t.Fatalf("write quality field: %v", err)
	}
	if imageBytes != nil {
		part, err := form.CreateFormFile("image", "input.png")
		if err != nil {
			t.Fatalf("create image part: %v", err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatalf("write image part: %v", err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close multipart form: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/trace", &body)
	request.Header.Set(fiber.HeaderContentType, form.FormDataContentType())
	if clientIP != "" {
		request.Header.Set(proxyHeader, clientIP)
	}
	return request
}

func statusOf(t *testing.T, app *fiber.App, request *http.Request) (int, http.Header, string) {
	t.Helper()
	response, err := app.Test(request, 10_000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return response.StatusCode, response.Header, string(body)
}

// --- decoded-pixel cap over HTTP -----------------------------------------

// TestOversizeImageIsRejectedWith413 pins the status the cap surfaces. The upload
// is far under the 20 MB body limit, so nothing but the decoded-size check can
// refuse it — which is the gap the cap closes.
func TestOversizeImageIsRejectedWith413(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	oversize := pngHeader(t, 20_000, 20_000)
	if len(oversize) > maxImageBytes {
		t.Fatalf("fixture must be within the body limit, got %d bytes", len(oversize))
	}

	status, _, body := statusOf(t, app, newTraceRequest(t, oversize, "203.0.113.10"))
	if status != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", status, body)
	}
	if !bytes.Contains([]byte(body), []byte("exceeds")) {
		t.Fatalf("the 413 body should explain the limit, got %q", body)
	}
}

// TestInBudgetImageIsTraced is the counterweight: the cap must not be refusing
// ordinary uploads, or the 413 above proves only that the endpoint is broken.
func TestInBudgetImageIsTraced(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	status, header, body := statusOf(t, app, newTraceRequest(t, pngHeader(t, 640, 480), "203.0.113.10"))
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}
	if got := header.Get(fiber.HeaderContentType); got != "image/svg+xml" {
		t.Fatalf("Content-Type = %q, want image/svg+xml", got)
	}
}

// --- concurrency cap over HTTP -------------------------------------------

// TestSaturatedTracerReturns503WithRetryAfter checks the refusal reaches the
// client as a 503 carrying a retry hint, rather than as a queued request that
// eventually times out.
func TestSaturatedTracerReturns503WithRetryAfter(t *testing.T) {
	const traceTimeoutForTest = 20 * time.Second

	gateDir := t.TempDir()
	pythonBin, scriptPath := stubCLI(t, `touch "`+gateDir+`/started.$$"
while [ ! -f "`+gateDir+`/release" ]; do sleep 0.01; done
`+echoingCLI)

	// One slot, and the test holds it, so the request under test arrives to a
	// tracer with nothing free.
	trace := tracer.New(pythonBin, scriptPath, traceTimeoutForTest, 1)
	app := newApp(trace, proxyHeader)

	held := make(chan struct{})
	go func() {
		defer close(held)
		_, _ = trace.Trace(context.Background(), pngHeader(t, 100, 100), "faithful")
	}()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(gateDir, "release"), nil, 0o600)
		<-held
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		started, err := filepath.Glob(filepath.Join(gateDir, "started.*"))
		if err != nil {
			t.Fatalf("glob started markers: %v", err)
		}
		if len(started) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the holding trace never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	status, header, body := statusOf(t, app, newTraceRequest(t, pngHeader(t, 100, 100), "203.0.113.10"))
	elapsed := time.Since(start)

	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", status, body)
	}
	if got := header.Get(fiber.HeaderRetryAfter); got == "" {
		t.Fatal("a 503 from the concurrency cap must carry Retry-After")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the 503 took %s: the request was queued behind the busy slot, not refused", elapsed)
	}
}

// --- rate limit -----------------------------------------------------------

// TestRateLimitIsEnforcedPerCaller proves two things at once, and they are
// inseparable: the limiter is actually mounted on /api/trace, and its buckets key
// on the CALLER rather than on the fronting proxy. A limiter keyed on the proxy
// would put every client of the deployment in one bucket, so the second caller
// below would be rejected for the first caller's traffic.
//
// The requests carry no image part, so they are refused by the handler before any
// subprocess starts; Fiber's limiter counts them regardless (SkipFailedRequests
// defaults to false), which is what lets the budget be driven cheaply.
func TestRateLimitIsEnforcedPerCaller(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	const noisyCaller = "203.0.113.10"
	for attempt := 1; attempt <= traceRateLimit; attempt++ {
		status, _, body := statusOf(t, app, newTraceRequest(t, nil, noisyCaller))
		if status == fiber.StatusTooManyRequests {
			t.Fatalf("request %d of %d was limited early (body: %s)", attempt, traceRateLimit, body)
		}
	}

	status, header, _ := statusOf(t, app, newTraceRequest(t, nil, noisyCaller))
	if status != fiber.StatusTooManyRequests {
		t.Fatalf("request %d must exceed the budget, got status %d", traceRateLimit+1, status)
	}
	if got := header.Get(fiber.HeaderRetryAfter); got == "" {
		t.Fatal("a 429 must carry Retry-After")
	}

	quietCaller := "198.51.100.7"
	if status, _, _ := statusOf(t, app, newTraceRequest(t, nil, quietCaller)); status == fiber.StatusTooManyRequests {
		t.Fatal("a second caller was limited by the first caller's traffic: the buckets are not per-caller")
	}
}

// TestMalformedProxyHeaderCollapsesOntoTheSocketAddress covers the other half of
// the proxy-header wiring. Naming a proxy header without validating it makes
// Fiber return the header verbatim, so a caller rotating junk through it mints a
// fresh budget on every request and the limit stops existing. With validation
// enabled, unparseable values fall back to the socket's address and share one
// bucket — so junk cannot buy extra budget.
func TestMalformedProxyHeaderCollapsesOntoTheSocketAddress(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	var lastStatus int
	for attempt := 0; attempt <= traceRateLimit; attempt++ {
		// A different unparseable value every time — each would be its own bucket
		// key if the header were trusted verbatim.
		junk := "not-an-address-" + string(rune('a'+attempt))
		lastStatus, _, _ = statusOf(t, app, newTraceRequest(t, nil, junk))
	}

	if lastStatus != fiber.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: rotating junk through the proxy header bought extra budget, so the header is being trusted without validation", lastStatus)
	}
}
