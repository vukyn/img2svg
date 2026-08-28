.PHONY: run dev build build-web web deps clean cli-deps

# --- Go service ---
run: ## run the HTTP service (serves embedded UI + /api/trace on :8090)
	go run ./cmd/server

dev: build-web run ## build the UI then run the service (one-shot local preview)

build: build-web ## build server binary to bin/ (embeds the built UI)
	go build -o bin/server ./cmd/server

deps: ## tidy go modules
	go mod tidy

# --- React UI (Vite + React 19 + TS) ---
web: ## Vite dev server in ui/ (proxies /api → :8090)
	cd ui && npm run dev

build-web: ## build the UI into internal/web/dist (must run before `go build`)
	cd ui && npm install && npm run build
	touch internal/web/dist/.gitkeep

# --- python CLI ---
cli-deps: ## install python CLI deps (vtracer, Pillow for --decheck)
	python3 -m pip install -r cli/requirements.txt

clean:
	rm -rf bin/
	rm -rf ui/node_modules ui/dist
	find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
