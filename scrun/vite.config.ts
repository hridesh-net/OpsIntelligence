import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Scrun is served by the Go dashboard binary at /dashboard/scrun/* via embed.FS.
// The build output is emitted directly into the dashboard's assets/ tree so
// `//go:embed assets/*` in internal/webui/dashboard/dashboard.go picks it up.
export default defineConfig({
  base: "/dashboard/scrun/",
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dashboard/assets/scrun",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5174,
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
