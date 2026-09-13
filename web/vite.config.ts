/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Everything the app talks to is proxied to the compose stack so the browser
// only ever makes same-origin requests: the API needs the Authorization header
// (a cross-origin preflight), and testauth serves no CORS headers at all.
// `preview` gets the same table because the Playwright run is served from it.
const proxy = {
  "/api": { target: "http://localhost:8081", changeOrigin: true },
  "/media": { target: "http://localhost:8081", changeOrigin: true },
  "/testauth": {
    target: "http://localhost:8090",
    changeOrigin: true,
    rewrite: (p: string) => p.replace(/^\/testauth/, ""),
  },
};

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, proxy },
  preview: { port: 4173, proxy },
  build: {
    target: "es2020",
    sourcemap: "hidden",
  },
  test: {
    environment: "jsdom",
    passWithNoTests: true,
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    coverage: { reporter: ["text", "html"] },
  },
});
