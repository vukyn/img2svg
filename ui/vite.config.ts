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
});
