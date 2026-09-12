# img2svg needs three things at runtime: the Go server binary, the python CLI,
# and python3 + vtracer (the tracer shells out to `python3 cli/img2svg.py`).
# Multi-stage: build the React UI, build the Go binary (embedding the UI), then
# assemble a small python runtime image with vtracer installed.

# ── Stage 1: build the React UI → internal/web/dist ──
FROM node:22-alpine AS ui
WORKDIR /app
COPY ui/package.json ui/package-lock.json ./ui/
RUN cd ui && npm ci
COPY ui/ ./ui/
# vite outDir is ../internal/web/dist (relative to ui/)
RUN cd ui && npm run build

# ── Stage 2: build the Go server (embeds internal/web/dist) ──
FROM golang:1.27.1-alpine AS build
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

# Both paths are absolute, and that is the security property rather than tidiness.
# A bare `python3` is resolved through $PATH, so an earlier writable entry runs as
# the server user; a relative `cli/img2svg.py` is resolved against the working
# directory, so a process started elsewhere loads that directory's script. The
# server refuses to resolve either late — see resolveTracerPaths in cmd/server.
#
# /usr/local/bin/python3 is where this base image puts it (a symlink to
# python3.12); `docker run --rm python:3.12-slim which python3` is the check if the
# base image is ever bumped.
ENV PORT=8090 \
    PYTHON_BIN=/usr/local/bin/python3 \
    IMG2SVG_CLI=/app/cli/img2svg.py

# Runs unprivileged. Nothing here wants root: the port is 8090, the UI is served
# out of the binary's embedded FS, and a trace is a pipe in and a pipe out with no
# temp file anywhere. Ownership is left with root deliberately — appuser needs to
# read /app, never to write it.
# --system is deliberately absent: the uid is named explicitly and 10001 is above
# this image's SYS_UID_MAX, so the flag only prints a warning on every build.
RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin appuser
USER appuser

EXPOSE 8090
ENTRYPOINT ["/app/bin/server"]
