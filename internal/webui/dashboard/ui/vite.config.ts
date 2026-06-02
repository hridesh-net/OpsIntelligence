import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";
import { rmSync } from "node:fs";

// Output lands in ../assets so Go's //go:embed assets/* picks it up unchanged.
// Two HTML entries: app.html (post-login frame) and login.html.
// base must be /dashboard/ because the binary mounts this handler under /dashboard/.
// Wipe just the hashed-bundle subdir before each build so stale chunks
// don't pile up in the embedded binary. We can't use emptyOutDir on the
// parent (../assets) because app.html/login.html and favicon.svg live there.
const HASHED_DIR = resolve(__dirname, "../assets/assets");
try { rmSync(HASHED_DIR, { recursive: true, force: true }); } catch { /* first run */ }

export default defineConfig({
  plugins: [react()],
  base: "/dashboard/",
  resolve: {
    alias: { "@": resolve(__dirname, "src") },
  },
  build: {
    outDir: resolve(__dirname, "../assets"),
    emptyOutDir: false,
    sourcemap: false,
    rollupOptions: {
      input: {
        app: resolve(__dirname, "app.html"),
        login: resolve(__dirname, "login.html"),
      },
    },
  },
  server: {
    port: 5174,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
});
