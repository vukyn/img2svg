/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go server embeds the build output (internal/web/dist) via go:embed, so the
// UI ships inside the binary. `base: "./"` makes asset URLs relative, which the
// Fiber filesystem middleware serves cleanly. The dev server proxies /api to the
// Go service so `make web` works against a locally-running backend.
// https://vite.dev/config/
export default defineConfig({
	base: "./",
	plugins: [react()],
	build: {
		outDir: "../internal/web/dist",
		emptyOutDir: true,
	},
	server: {
		proxy: {
			"/api": {
				target: "http://127.0.0.1:8090",
				changeOrigin: true,
			},
		},
	},
	// Component tests run in jsdom against the real React renderer. Excluding the
	// build output matters: internal/web/dist is the embedded bundle and picking a
	// file out of it would run the shipped code rather than the source.
	test: {
		environment: "jsdom",
		include: ["src/**/*.test.{ts,tsx}"],
		exclude: ["node_modules", "dist", "../internal/web/dist"],
	},
});
