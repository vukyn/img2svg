.PHONY: run build deps clean cli-deps

# --- Go service ---
run: ## run the HTTP service (serves UI + /api/trace on :8090)
	go run ./cmd/server

build: ## build server binary to bin/
	go build -o bin/server ./cmd/server

deps: ## tidy go modules
	go mod tidy

# --- python CLI ---
cli-deps: ## install python CLI deps (vtracer)
	python3 -m pip install -r cli/requirements.txt

clean:
	rm -rf bin/
