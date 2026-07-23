// Command server runs the img2svg HTTP service: serves the embedded web UI and
// a POST /api/trace endpoint that shells out to the python CLI to vectorize an
// uploaded raster image into SVG.
package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/vukyn/img2svg/internal/tracer"
	"github.com/vukyn/img2svg/internal/web"
)

// max upload size for an input image
const maxImageBytes = 20 * 1024 * 1024

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	port := env("PORT", "8090")
	pythonBin := env("PYTHON_BIN", "python3")
	scriptPath := env("IMG2SVG_CLI", "cli/img2svg.py")

	trace := tracer.New(pythonBin, scriptPath, 60*time.Second)

	app := fiber.New(fiber.Config{
		BodyLimit:    maxImageBytes,
		ErrorHandler: func(c *fiber.Ctx, err error) error { return c.Status(500).SendString(err.Error()) },
	})
	app.Use(logger.New())

	app.Post("/api/trace", func(c *fiber.Ctx) error {
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
		if err != nil {
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

	log.Printf("img2svg listening on :%s (python=%s cli=%s)", port, pythonBin, scriptPath)
	log.Fatal(app.Listen(":" + port))
}
