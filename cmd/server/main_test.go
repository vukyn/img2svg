package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/vukyn/img2svg/internal/tracer"
)

// proxyHeader is the header a fronting proxy puts the real client address in.
// Its production value comes from the PROXY_HEADER env var (fly.toml sets it);
// the tests name it directly so they exercise the same code path.
const proxyHeader = "Fly-Client-IP"

// oversizeSVGBytes is comfortably past the tracer's output ceiling. The ceiling
// itself is unexported, so the number is named here rather than derived — if the
// ceiling is ever raised above this, the 413 test stops testing anything and
// fails loudly on the status instead of passing quietly.
//
// ⚠️ It tracks the ceiling: it was 68 MB while the ceiling was 64, and moved with
// it to 32. A stale value here is not a wrong test, it is a test that quietly
// allocates twice what it needs on every run.
const oversizeSVGBytes = 36 << 20

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

// --- what a failed trace tells the client ---------------------------------

// tracebackCLI stands in for the CLI dying on an uncaught python exception: a
// traceback on stderr naming the absolute script path and the line it died on.
const tracebackCLI = `printf 'Traceback (most recent call last):\n  File "/srv/img2svg/cli/img2svg.py", line 163, in <module>\n    main()\nException: Failed to decode img_bytes.\n' >&2
exit 1`

// TestTracebackDoesNotReachTheClient is the disclosure fix end to end. The
// endpoint is unauthenticated, so before this the response body handed any caller
// the deploy layout, the python version's internals and a reliable oracle for
// which limit they had just tripped.
func TestTracebackDoesNotReachTheClient(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, tracebackCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	status, _, body := statusOf(t, app, newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10"))
	if status != fiber.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", status, body)
	}
	for _, leak := range []string{"Traceback", "/srv/img2svg", "line 163", "img_bytes", "main()"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the response body carries %q from the subprocess: %q", leak, body)
		}
	}
}

// TestUnrecognizedFormatReachesTheClient is the counterweight, and the thing a
// blanket suppression would have broken: the one diagnostic that is about the
// caller's own file, not about the host, must survive.
func TestUnrecognizedFormatReachesTheClient(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, `echo 'unrecognized image format (want png/jpg/gif/bmp/webp)' >&2
exit 1`)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	status, _, body := statusOf(t, app, newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10"))
	if status != fiber.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", status, body)
	}
	if !strings.Contains(body, "unrecognized image format") {
		t.Fatalf("a caller who uploaded a bad file must be told so, got %q", body)
	}
}

// TestUnhandledErrorIsNotRelayedToTheClient covers the global ErrorHandler, which
// had the same shape as the tracer's: err.Error() straight into the response.
//
// It is driven through a bare app rather than the real one because the real one
// has no route that reaches the ErrorHandler — the SPA catch-all answers every
// unmatched path and the trace handler converts its own errors — which is what
// makes this a latent leak rather than a reachable one, and why the wiring has to
// be asserted separately below.
func TestUnhandledErrorIsNotRelayedToTheClient(t *testing.T) {
	const secret = "dial tcp 10.0.0.4:5432: connect refused for /srv/img2svg/cli/img2svg.py"

	app := fiber.New(fiber.Config{ErrorHandler: sanitizedErrorHandler})
	app.Get("/boom", func(c *fiber.Ctx) error { return errors.New(secret) })

	status, _, body := statusOf(t, app, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", status, body)
	}
	if body != genericErrorMessage {
		t.Fatalf("body = %q, want the fixed message %q", body, genericErrorMessage)
	}
	for _, leak := range []string{"10.0.0.4", "5432", "/srv/img2svg"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the response body carries %q from the error: %q", leak, body)
		}
	}
}

// TestNewAppInstallsTheSanitizingErrorHandler is the other half of the test
// above: sanitizing a handler nothing installs would prove nothing. Compared by
// code pointer because funcs are not comparable any other way.
func TestNewAppInstallsTheSanitizingErrorHandler(t *testing.T) {
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	app := newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)

	installed := reflect.ValueOf(app.Config().ErrorHandler).Pointer()
	if want := reflect.ValueOf(sanitizedErrorHandler).Pointer(); installed != want {
		t.Fatal("newApp installs some other ErrorHandler, so the sanitizing one is not what runs")
	}
}

// --- the output ceiling over HTTP -----------------------------------------

// TestOversizeSVGIsRejectedWith413 covers the gap the decoded-pixel cap leaves
// open: that cap bounds the INPUT, and a small high-entropy image at the faithful
// preset yields roughly a path per speckle, so an in-budget upload can still
// expand into an unbounded SVG held in the capture, the response body and every
// copy in between.
//
// Waiting on the subprocess is the load-bearing half. The stub sleeps for far
// longer than the test will wait once it has finished overflowing, so a ceiling
// that merely NOTICES the overflow — leaving the subprocess running with nobody
// draining it — fails here even though the status would be right. Past the
// harness's own 10 s window that shows up as an app.Test timeout rather than as
// the elapsed assertion below; the assertion is what catches a shorter stall.
func TestOversizeSVGIsRejectedWith413(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates the output ceiling")
	}

	markerDir := t.TempDir()
	pythonBin, scriptPath := stubCLI(t, `head -c `+strconv.Itoa(oversizeSVGBytes)+` /dev/zero | tr '\0' 'x'
sleep 30
touch "`+markerDir+`/finished"`)
	app := newApp(tracer.New(pythonBin, scriptPath, 25*time.Second, 1), proxyHeader)

	start := time.Now()
	status, _, body := statusOf(t, app, newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10"))
	elapsed := time.Since(start)

	if status != fiber.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", status, body)
	}
	if !strings.Contains(body, "bytes of SVG") {
		t.Fatalf("the 413 body should say what was too big, got %q", body)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the 413 took %s: the subprocess was left running after the overflow rather than cancelled", elapsed)
	}
	if _, err := os.Stat(filepath.Join(markerDir, "finished")); err == nil {
		t.Fatal("the subprocess ran to completion after its output was refused")
	}
}

// --- response headers -----------------------------------------------------

// testApp is the app every header and origin test below drives: a real middleware
// stack over a CLI stand-in that answers instantly.
func testApp(t *testing.T) *fiber.App {
	t.Helper()
	pythonBin, scriptPath := stubCLI(t, echoingCLI)
	return newApp(tracer.New(pythonBin, scriptPath, 5*time.Second, 2), proxyHeader)
}

// TestTracedSVGIsServedAsANonSniffableDownload covers the two headers that decide
// what a browser is allowed to DO with the response.
//
// The body is markup assembled from an upload, served as image/svg+xml. An SVG
// opened as a top-level document is a scripting context on this origin, so
// Content-Disposition is what stops the endpoint from being a way to host script
// under the app's own name; nosniff is what stops the browser re-deciding the type
// for an SVG whose leading bytes happen to look like something else.
func TestTracedSVGIsServedAsANonSniffableDownload(t *testing.T) {
	status, header, body := statusOf(t, testApp(t), newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10"))
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}
	if got := header.Get(fiber.HeaderXContentTypeOptions); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := header.Get(fiber.HeaderContentDisposition); got != svgAttachment {
		t.Fatalf("Content-Disposition = %q, want %q", got, svgAttachment)
	}
}

// TestSPAResponsesCarryTheContentSecurityPolicy asserts the policy reaches the
// document that runs the app, and that it still permits what the app actually
// loads. Only the header is asserted, not the status: the embedded bundle is a
// build artifact and a fresh checkout has only a .gitkeep, so a test that required
// a 200 here would fail for a reason that has nothing to do with the policy.
func TestSPAResponsesCarryTheContentSecurityPolicy(t *testing.T) {
	_, header, _ := statusOf(t, testApp(t), httptest.NewRequest(http.MethodGet, "/", nil))

	policy := header.Get(fiber.HeaderContentSecurityPolicy)
	if policy != contentSecurityPolicy {
		t.Fatalf("Content-Security-Policy = %q, want %q", policy, contentSecurityPolicy)
	}
	// ⚠️ The app's previews — upload thumbnail, compare raster, traced SVG — are
	// every one of them an object URL, and blob: is not covered by 'self'. A policy
	// without it loads cleanly and then shows nothing, which is the failure mode a
	// header-presence check would pass straight through.
	if !strings.Contains(policy, "img-src 'self' blob:") {
		t.Fatalf("the policy must allow blob: images or every preview in the app is blocked: %q", policy)
	}
	if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src must not allow inline script — the built bundle does not need it: %q", policy)
	}
}

// --- cross-site requests --------------------------------------------------

// traceRequestFrom builds a valid trace request carrying the browser-supplied
// provenance headers under test. An empty value omits the header entirely, which
// is a distinct case from sending it empty.
func traceRequestFrom(t *testing.T, secFetchSite, origin string) *http.Request {
	t.Helper()
	request := newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10")
	if secFetchSite != "" {
		request.Header.Set("Sec-Fetch-Site", secFetchSite)
	}
	if origin != "" {
		request.Header.Set(fiber.HeaderOrigin, origin)
	}
	return request
}

// TestCrossSiteProvenanceIsRefused walks every way a request can announce where it
// came from. multipart/form-data is a CORS-simple content type, so any page
// anywhere can submit a form at this endpoint with no preflight and no consent —
// which makes a drive-by the trigger for every resource the tracer's limits bound.
//
// The allowed cases are as load-bearing as the refused ones. httptest.NewRequest
// gives the request Host "example.com", so an Origin naming that host is the
// app's own page; and the two no-header rows are the deliberate decision that a
// client sending no provenance at all (curl, CI, a probe) is not a browser and is
// not what this check defends against.
func TestCrossSiteProvenanceIsRefused(t *testing.T) {
	cases := []struct {
		name         string
		secFetchSite string
		origin       string
		wantRefused  bool
	}{
		{name: "fetch metadata says cross-site", secFetchSite: "cross-site", wantRefused: true},
		{name: "fetch metadata says same-origin", secFetchSite: "same-origin", wantRefused: false},
		{name: "fetch metadata says same-site", secFetchSite: "same-site", wantRefused: false},
		{name: "fetch metadata says none (user-initiated)", secFetchSite: "none", wantRefused: false},
		{
			// The dev-server shape: Vite proxies with changeOrigin, so Host is
			// rewritten to the backend and only the browser's own metadata still
			// tells the truth. Refusing this would break `make web`.
			name:         "fetch metadata outranks a mismatched Origin",
			secFetchSite: "same-origin",
			origin:       "http://localhost:5173",
			wantRefused:  false,
		},
		{name: "origin from another site, no metadata", origin: "https://evil.example", wantRefused: true},
		{name: "origin is the app's own host", origin: "http://example.com", wantRefused: false},
		{name: "origin differs only by port", origin: "http://example.com:8443", wantRefused: true},
		{name: "opaque origin", origin: "null", wantRefused: true},
		{name: "no provenance headers at all", wantRefused: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, _, body := statusOf(t, testApp(t), traceRequestFrom(t, testCase.secFetchSite, testCase.origin))

			refused := status == fiber.StatusForbidden
			if refused != testCase.wantRefused {
				t.Fatalf("status = %d (refused=%v), want refused=%v (body: %s)",
					status, refused, testCase.wantRefused, body)
			}
			if refused && body != crossSiteMessage {
				t.Fatalf("body = %q, want the fixed refusal %q", body, crossSiteMessage)
			}
			if !refused && status != fiber.StatusOK {
				t.Fatalf("an allowed request must still be traced, got status %d (body: %s)", status, body)
			}
		})
	}
}

// TestCrossSiteRefusalDoesNotSpendTheCallersRateBudget pins the mount ORDER, which
// is the half of this that a reviewer cannot see from the handler. Behind the
// limiter, a drive-by flood would spend the budget of whatever address it rides —
// a shared NAT, a corporate egress — and lock out the real callers behind it. The
// refusal is cheap enough to serve without a budget.
func TestCrossSiteRefusalDoesNotSpendTheCallersRateBudget(t *testing.T) {
	app := testApp(t)

	for attempt := 0; attempt <= traceRateLimit; attempt++ {
		request := newTraceRequest(t, nil, "203.0.113.10")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		if status, _, body := statusOf(t, app, request); status != fiber.StatusForbidden {
			t.Fatalf("request %d: status = %d, want 403 (body: %s)", attempt, status, body)
		}
	}

	request := newTraceRequest(t, pngHeader(t, 64, 64), "203.0.113.10")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if status, _, body := statusOf(t, app, request); status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200: the cross-site flood spent this caller's budget, so the origin check runs behind the limiter (body: %s)",
			status, body)
	}
}

// --- interpreter and script paths -----------------------------------------

// fakeExecutable writes a file that looks like an interpreter to the checks.
func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	return path
}

// fakeScript writes a file that looks like the CLI to the checks.
func fakeScript(t *testing.T, dir, relative string) string {
	t.Helper()
	path := filepath.Join(dir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make script dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("# stand-in\n"), 0o600); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return path
}

// TestResolveTracerPathsAcceptsAbsolutePaths is the deployment's configuration:
// both values absolute and both present, taken as given.
func TestResolveTracerPathsAcceptsAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	interpreter := fakeExecutable(t, dir, "python3")
	script := fakeScript(t, dir, "img2svg.py")

	python, resolvedScript, err := resolveTracerPaths(interpreter, script, t.TempDir())
	if err != nil {
		t.Fatalf("resolveTracerPaths: %v", err)
	}
	if python != interpreter {
		t.Fatalf("interpreter = %q, want %q", python, interpreter)
	}
	if resolvedScript != script {
		t.Fatalf("script = %q, want %q", resolvedScript, script)
	}
}

// TestRelativeScriptResolvesAgainstTheBaseDirectory is the fix itself. The script
// is placed beside the "binary" and nowhere else, so a resolution that consulted
// anything but baseDir cannot produce this answer.
func TestRelativeScriptResolvesAgainstTheBaseDirectory(t *testing.T) {
	baseDir := t.TempDir()
	want := fakeScript(t, baseDir, filepath.Join("cli", "img2svg.py"))

	_, script, err := resolveTracerPaths("/bin/sh", filepath.Join("cli", "img2svg.py"), baseDir)
	if err != nil {
		t.Fatalf("resolveTracerPaths: %v", err)
	}
	if script != want {
		t.Fatalf("script = %q, want %q", script, want)
	}
}

// TestRelativeScriptIgnoresTheWorkingDirectory is the vulnerability, stated as a
// test. A copy of the CLI sits in the working directory and nowhere else; before
// this change that copy is what the tracer would have executed, for the life of
// the process, as the server user. Resolving against the binary's own directory
// means the only outcome now is a refusal at boot.
func TestRelativeScriptIgnoresTheWorkingDirectory(t *testing.T) {
	plantedDir := t.TempDir()
	fakeScript(t, plantedDir, filepath.Join("cli", "img2svg.py"))
	t.Chdir(plantedDir)

	// An empty base directory: the script exists in the cwd, and only there.
	_, _, err := resolveTracerPaths("/bin/sh", filepath.Join("cli", "img2svg.py"), t.TempDir())
	if err == nil {
		t.Fatal("a relative script path resolved against the working directory: a process started in an attacker-writable directory would load that directory's CLI")
	}
	if !strings.Contains(err.Error(), "IMG2SVG_CLI") {
		t.Fatalf("the failure must name the setting to fix, got %v", err)
	}
}

// TestResolveTracerPathsRejectsUnusablePaths covers everything that must stop the
// process at boot rather than surface later as a 422 on somebody's upload.
func TestResolveTracerPathsRejectsUnusablePaths(t *testing.T) {
	dir := t.TempDir()
	interpreter := fakeExecutable(t, dir, "python3")
	script := fakeScript(t, dir, "img2svg.py")
	notExecutable := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(notExecutable, []byte("plain\n"), 0o600); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}

	cases := []struct {
		name       string
		pythonBin  string
		scriptPath string
		wantNamed  string
	}{
		{name: "empty interpreter", pythonBin: "", scriptPath: script, wantNamed: "PYTHON_BIN"},
		{name: "absolute interpreter missing", pythonBin: filepath.Join(dir, "absent"), scriptPath: script, wantNamed: "PYTHON_BIN"},
		{name: "interpreter is a directory", pythonBin: dir, scriptPath: script, wantNamed: "PYTHON_BIN"},
		{name: "interpreter is not executable", pythonBin: notExecutable, scriptPath: script, wantNamed: "PYTHON_BIN"},
		{name: "bare interpreter not on PATH", pythonBin: "img2svg-no-such-interpreter", scriptPath: script, wantNamed: "PYTHON_BIN"},
		{name: "absolute script missing", pythonBin: interpreter, scriptPath: filepath.Join(dir, "absent.py"), wantNamed: "IMG2SVG_CLI"},
		{name: "script is a directory", pythonBin: interpreter, scriptPath: dir, wantNamed: "IMG2SVG_CLI"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := resolveTracerPaths(testCase.pythonBin, testCase.scriptPath, t.TempDir())
			if err == nil {
				t.Fatal("want a boot failure, got none")
			}
			if !strings.Contains(err.Error(), testCase.wantNamed) {
				t.Fatalf("the failure must name %s so the operator knows what to fix, got %v", testCase.wantNamed, err)
			}
		})
	}
}

// TestBareInterpreterIsResolvedToAnAbsolutePath is what keeps a developer's host
// working: `python3` is still accepted, but it is looked up once at boot and the
// absolute result is what every later subprocess runs, so the $PATH that decides
// which binary executes is fixed at startup rather than re-read per request.
func TestBareInterpreterIsResolvedToAnAbsolutePath(t *testing.T) {
	python, _, err := resolveTracerPaths("sh", fakeScript(t, t.TempDir(), "img2svg.py"), t.TempDir())
	if err != nil {
		t.Fatalf("resolveTracerPaths: %v", err)
	}
	if !filepath.IsAbs(python) {
		t.Fatalf("interpreter = %q, want an absolute path", python)
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatalf("the resolved interpreter must exist: %v", err)
	}
}
