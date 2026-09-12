// Command server runs the img2svg HTTP service: serves the embedded web UI and
// a POST /api/trace endpoint that shells out to the python CLI to vectorize an
// uploaded raster image into SVG.
package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
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

	traceLimiter := limiter.New(limiter.Config{
		Max:        traceRateLimit,
		Expiration: traceRateWindow,
		LimitReached: func(c *fiber.Ctx) error {
			// The limiter has already set Retry-After to the window remainder.
			return c.Status(fiber.StatusTooManyRequests).
				SendString("too many trace requests; try again shortly")
		},
	})

	app.Post("/api/trace", traceLimiter, func(c *fiber.Ctx) error {
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

		c.Set("Content-Type", "image/svg+xml")
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
