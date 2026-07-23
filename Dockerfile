# img2svg needs three things at runtime: the Go server binary, the python CLI,
# and python3 + vtracer (the tracer shells out to `python3 cli/img2svg.py`).
# Multi-stage: build the React UI, build the Go binary (embedding the UI), then
# assemble a small python runtime image with vtracer installed.

# ── Stage 1: build the React UI → internal/web/dist ──
FROM node:22-slim AS ui
WORKDIR /app
COPY ui/package.json ui/package-lock.json ./ui/
RUN cd ui && npm ci
COPY ui/ ./ui/
# vite outDir is ../internal/web/dist (relative to ui/)
RUN cd ui && npm run build

# ── Stage 2: build the Go server (embeds internal/web/dist) ──
FROM golang:1.25 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# use the freshly built UI, not whatever dist happened to be in the context
COPY --from=ui /app/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/server ./cmd/server

# ── Stage 3: runtime — python3 + vtracer + the binary + the CLI ──
FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir vtracer
COPY --from=build /app/bin/server /app/bin/server
COPY cli/ /app/cli/
ENV PORT=8090 \
    PYTHON_BIN=python3 \
    IMG2SVG_CLI=cli/img2svg.py
EXPOSE 8090
ENTRYPOINT ["/app/bin/server"]
