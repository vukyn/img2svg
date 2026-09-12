// Command server runs the img2svg HTTP service: serves the embedded web UI and
// a POST /api/trace endpoint that shells out to the python CLI to vectorize an
// uploaded raster image into SVG.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/vukyn/img2svg/internal/tracer"
	"github.com/vukyn/img2svg/internal/web"
)

const (
	// max upload size for an input image. This bounds the ENCODED bytes only;
	// the decoded pixel count is bounded separately, inside the tracer.
	maxImageBytes = 20 * 1024 * 1024

	// maxConcurrentTraces bounds how many python+vtracer subprocesses run at once.
	// Sized for the deployment's single shared vCPU (see fly.toml): a trace is
	// CPU-bound, so more of them in parallel makes every one slower without
	// finishing any sooner, and each carries its own interpreter's memory.
	maxConcurrentTraces = 2

	// traceTimeout caps a single subprocess. Long enough for a large image on a
	// shared vCPU, short enough that a stuck trace releases its slot promptly —
	// with only a couple of slots, slot hold time is what the endpoint's
	// availability is made of.
	traceTimeout = 20 * time.Second

	// Per-caller budget on the trace endpoint. The endpoint is unauthenticated and
	// every call forks a subprocess, so the limiter is the outer bound on how much
	// of the machine one caller can ask for.
	traceRateLimit  = 10
	traceRateWindow = time.Minute

	// genericErrorMessage is what an unhandled error is allowed to say. Fiber's
	// default — and the handler this replaced — sends err.Error() straight to the
	// client, which is how whatever a middleware happened to wrap in reaches an
	// unauthenticated caller. The detail goes to the log instead.
	genericErrorMessage = "internal server error"

	// svgAttachment is the Content-Disposition on a successful trace. The body is
	// attacker-influenced markup served as image/svg+xml, and an SVG opened as a
	// top-level document is a full scripting context on this origin — same-origin
	// to the app, its storage and its cookies. Naming the response a download
	// removes that context. It costs the app nothing: the UI reads this endpoint
	// with fetch() and renders the bytes itself, so nothing ever navigates to it.
	svgAttachment = `attachment; filename="trace.svg"`

	// contentSecurityPolicy is the fallback that catches whatever the app's own
	// escaping misses. It is written against what the bundle actually loads, and
	// each directive is load-bearing rather than copied:
	//
	//   script-src 'self'      — vite emits one external module script and no
	//                            inline script, so no nonce or hash is needed.
	//   style-src  'unsafe-inline' — vite links a stylesheet from 'self', but React
	//                            style props set element.style, which CSP does not
	//                            govern; the keyword is kept because a future
	//                            <style> injection is cheap to reintroduce.
	//   img-src    blob:       — ⚠️ NOT optional. Every preview in the app is an
	//                            object URL (the upload thumbnail, the compare
	//                            raster, the traced SVG), and blob: is not covered
	//                            by 'self'. Without it the app loads and silently
	//                            shows nothing.
	//   object-src 'none', base-uri 'self', frame-ancestors 'none' — plugin
	//                            embedding, <base> hijacking and clickjacking are
	//                            all things this app never needs.
	//
	// data: is deliberately absent: nothing in the bundle uses a data URL, and an
	// unused allowance is only an allowance for an injection.
	contentSecurityPolicy = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' blob:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"base-uri 'self'; " +
		"frame-ancestors 'none'"

	// headerSecFetchSite is the browser's own statement about where a request came
	// from. Fiber v2 has no constant for it.
	headerSecFetchSite = "Sec-Fetch-Site"

	// crossSiteMessage is what a request from another site is told.
	crossSiteMessage = "cross-site requests are not accepted"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// sanitizedErrorHandler answers an error that reached the framework. Fiber's
// default — and the handler this replaced — sends err.Error() to the client, so
// whatever detail an error happened to be carrying becomes a response body on an
// unauthenticated endpoint. It goes to the log instead.
//
// A named function rather than a closure so a test can assert that this is what
// newApp installs: a sanitizer nothing installs sanitizes nothing.
func sanitizedErrorHandler(c *fiber.Ctx, err error) error {
	log.Printf("unhandled error: %s %s: %v", c.Method(), c.Path(), err)
	return c.Status(fiber.StatusInternalServerError).SendString(genericErrorMessage)
}

// securityHeaders puts the headers that are true of every response on every
// response. Mounted globally rather than on the SPA branch alone, because the
// trace endpoint wants both of them too and a header a route has to remember to
// set is a header a route eventually forgets.
//
// nosniff is what makes the Content-Type on each response binding. Without it a
// browser is free to re-guess, and the two responses that matter here are exactly
// the ones guessing goes wrong on: a traced SVG whose bytes came from an upload,
// and the bundle's own assets.
func securityHeaders(c *fiber.Ctx) error {
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderContentSecurityPolicy, contentSecurityPolicy)
	return c.Next()
}

// isCrossSite reports whether a browser told us this request was issued from
// another site.
//
// POST /api/trace takes multipart/form-data, which is a CORS-simple content type:
// any page anywhere can submit a form at it with no preflight and no consent. The
// endpoint is unauthenticated and changes no state, so nothing is forged — what is
// taken is the machine. It is the drive-by trigger for every resource the limits
// in internal/tracer exist to bound.
//
// The order of the two signals is the design.
//
//   - Sec-Fetch-Site first, because the browser computed it and page script cannot
//     set it (a forbidden header name). It is also the only signal that survives a
//     reverse proxy that rewrites Host, which is exactly what the Vite dev server
//     does (`changeOrigin: true`), so `make web` keeps working on any browser that
//     sends it — which is every current one.
//   - Origin against the request's own host second, for the older browser that
//     sends no fetch metadata.
func isCrossSite(c *fiber.Ctx) bool {
	switch c.Get(headerSecFetchSite) {
	case "cross-site":
		return true
	case "same-origin", "same-site", "none":
		return false
	}

	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		// ⚠️ Neither header present is ALLOWED, deliberately. A browser cannot get
		// here: the Fetch spec makes Origin mandatory on every method other than
		// GET and HEAD, so a POST with no Origin did not come from one. What does
		// arrive this way is curl, a CI job, a health probe and every server-side
		// caller — none of which this check defends against, because they can
		// address the endpoint directly no matter what it answers here. Refusing
		// them would break every script against a local run and stop nothing; the
		// rate limiter is what bounds a direct caller.
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		// Neither a URL nor the literal "null" (a sandboxed frame or an opaque
		// redirect) is a same-origin request, and no browser sends either from the
		// app's own page.
		return true
	}
	return !strings.EqualFold(parsed.Host, c.Hostname())
}

// rejectCrossSite refuses what isCrossSite recognises.
//
// It is mounted ahead of the rate limiter on purpose: a drive-by flood should not
// spend the budget of the address it is flooding from, which on a shared NAT is a
// real caller's. The refusal is cheap enough to serve without one.
func rejectCrossSite(c *fiber.Ctx) error {
	if isCrossSite(c) {
		return c.Status(fiber.StatusForbidden).SendString(crossSiteMessage)
	}
	return c.Next()
}

// resolveTracerPaths turns the configured interpreter and CLI script into absolute
// paths that exist, or explains at boot why it cannot.
//
// Both defaults were resolved late and against something the process does not
// own. A bare "python3" goes through exec.LookPath on every trace, so whoever can
// place a file earlier in $PATH than the real interpreter runs code as the server
// user. A relative "cli/img2svg.py" resolves against the working directory, so a
// process launched from somewhere else loads that directory's script instead — and
// the script's own `from decheck import decheck` then resolves beside it, so the
// substitution carries.
//
// baseDir is what a relative script path is resolved against — the executable's
// directory in main, a temp dir in the tests. Never the working directory: that is
// the input this function exists to stop trusting.
//
// Failing here rather than per-request is the other half. A missing interpreter
// used to surface as a 422 on a trace, indistinguishable from a bad upload.
func resolveTracerPaths(pythonBin, scriptPath, baseDir string) (string, string, error) {
	python, err := resolveInterpreter(pythonBin)
	if err != nil {
		return "", "", err
	}

	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(baseDir, scriptPath)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		return "", "", fmt.Errorf("IMG2SVG_CLI: %s: %w (set IMG2SVG_CLI to the absolute path of cli/img2svg.py)", scriptPath, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("IMG2SVG_CLI: %s is a directory, not the CLI script", scriptPath)
	}
	return python, scriptPath, nil
}

// resolveInterpreter absolutises PYTHON_BIN. An absolute value is taken as given
// and only checked; a bare name is looked up ONCE, here, so the $PATH that decides
// which binary runs is the one at boot rather than the one at each request.
//
// A bare name is still a weaker position than an absolute path, because the lookup
// itself reads $PATH — which is why the deployment image sets an absolute one. It
// stays supported because it is what a developer has: `make run` on a host whose
// python3 lives wherever the version manager put it.
func resolveInterpreter(pythonBin string) (string, error) {
	if pythonBin == "" {
		return "", errors.New("PYTHON_BIN is empty")
	}

	resolved := pythonBin
	if !filepath.IsAbs(resolved) {
		found, err := exec.LookPath(resolved)
		if err != nil {
			return "", fmt.Errorf("PYTHON_BIN: %q not found on $PATH: %w (set PYTHON_BIN to an absolute interpreter path)", pythonBin, err)
		}
		if resolved, err = filepath.Abs(found); err != nil {
			return "", fmt.Errorf("PYTHON_BIN: %q: %w", pythonBin, err)
		}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("PYTHON_BIN: %s: %w", resolved, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("PYTHON_BIN: %s is not an executable file", resolved)
	}
	return resolved, nil
}

// newApp wires the HTTP surface. It is separate from main so tests can drive the
// real middleware stack (limiter, body limit, error mapping) against a tracer of
// their choosing.
//
// proxyHeader is the header a fronting proxy puts the real client address in
// ("Fly-Client-IP" on fly.io); empty for a direct run, where the socket's remote
// address is the truth.
func newApp(trace *tracer.Tracer, proxyHeader string) *fiber.App {
	app := fiber.New(fiber.Config{
		BodyLimit:    maxImageBytes,
		ErrorHandler: sanitizedErrorHandler,
		// ⚠️ These two belong together or not at all, and between them they are what
		// makes c.IP() — and therefore the rate limiter's bucket key — the actual
		// caller. Without ProxyHeader, the proxy's own address keys every request
		// and the per-IP limit becomes one global bucket that any single sprayer
		// spends for everyone. Without EnableIPValidation, Fiber returns the header
		// verbatim with no fallback, so an absent header yields an empty key (one
		// shared bucket again) and rotating junk through the header would mint
		// unlimited fresh budgets. With the flag, absent or malformed falls back to
		// the socket's remote address.
		ProxyHeader:        proxyHeader,
		EnableIPValidation: true,
	})
	app.Use(logger.New())
	app.Use(securityHeaders)

	traceLimiter := limiter.New(limiter.Config{
		Max:        traceRateLimit,
		Expiration: traceRateWindow,
		LimitReached: func(c *fiber.Ctx) error {
			// The limiter has already set Retry-After to the window remainder.
			return c.Status(fiber.StatusTooManyRequests).
				SendString("too many trace requests; try again shortly")
		},
	})

	app.Post("/api/trace", rejectCrossSite, traceLimiter, func(c *fiber.Ctx) error {
		quality := c.FormValue("quality", "faithful")

		fileHeader, err := c.FormFile("image")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("missing 'image' file field")
		}
		file, err := fileHeader.Open()
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("cannot open upload")
		}
		defer file.Close()

		imageBytes, err := io.ReadAll(io.LimitReader(file, maxImageBytes))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("cannot read upload")
		}

		svg, err := trace.Trace(c.Context(), imageBytes, quality)
		switch {
		case errors.Is(err, tracer.ErrImageTooLarge), errors.Is(err, tracer.ErrOutputTooLarge):
			// Input too big and output too big are both 413: the request was, in
			// the end, for more bytes than the endpoint will move.
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString(err.Error())
		case errors.Is(err, tracer.ErrBusy):
			// A slot frees within at most one trace timeout, so that is the honest
			// hint. Turning the caller away beats queueing them behind a subprocess.
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(trace.Timeout().Seconds())))
			return c.Status(fiber.StatusServiceUnavailable).
				SendString("tracer is busy; retry shortly")
		case err != nil:
			// Safe to relay as-is: the tracer sanitises subprocess failures at the
			// source, so what arrives here is either its own diagnostic or a
			// content-free sentinel — never the CLI's stderr.
			return c.Status(fiber.StatusUnprocessableEntity).SendString(err.Error())
		}

		c.Set(fiber.HeaderContentType, "image/svg+xml")
		c.Set(fiber.HeaderContentDisposition, svgAttachment)
		return c.Send(svg)
	})

	// embedded web UI: the built React bundle (index.html + assets/) produced by
	// `make build-web`. SPA fallback serves index.html for unknown paths.
	app.Use("/", filesystem.New(filesystem.Config{
		Root:         http.FS(web.FS()),
		Index:        "index.html",
		NotFoundFile: "index.html",
	}))

	return app
}

func main() {
	port := env("PORT", "8090")
	pythonBin := env("PYTHON_BIN", "python3")
	scriptPath := env("IMG2SVG_CLI", "cli/img2svg.py")
	proxyHeader := env("PROXY_HEADER", "")

	// A relative script path is resolved against the binary's own directory, never
	// the working directory — see resolveTracerPaths. Both Makefile targets that
	// start the server hand IMG2SVG_CLI an absolute path, so a local run never
	// depends on where the binary happens to sit.
	executable, err := os.Executable()
	if err != nil {
		log.Fatalf("cannot locate the running executable: %v", err)
	}
	pythonBin, scriptPath, err = resolveTracerPaths(pythonBin, scriptPath, filepath.Dir(executable))
	if err != nil {
		log.Fatalf("tracer paths: %v", err)
	}

	trace := tracer.New(pythonBin, scriptPath, traceTimeout, maxConcurrentTraces)
	app := newApp(trace, proxyHeader)

	// Deploying behind a proxy without naming its client-address header is a silent
	// mis-binding: it looks fine, and the per-IP rate limit quietly becomes global.
	// Say so at boot rather than leaving it to be found by a lockout.
	if proxyHeader == "" {
		log.Print("PROXY_HEADER is unset: rate-limit buckets are keyed by the socket address, which is the fronting proxy's if there is one")
	}

	log.Printf("img2svg listening on :%s (python=%s cli=%s)", port, pythonBin, scriptPath)
	log.Fatal(app.Listen(":" + port))
}
