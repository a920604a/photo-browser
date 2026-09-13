/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8081", changeOrigin: true },
      "/media": { target: "http://localhost:8081", changeOrigin: true },
    },
  },
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
