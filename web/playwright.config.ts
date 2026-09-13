import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.PW_PREVIEW_PORT ?? 4173);

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  timeout: 30_000,
  use: {
    baseURL: `http://localhost:${port}`,
    ...devices["Pixel 5"],
    trace: "retain-on-failure",
  },
  webServer: {
    // The preview server carries the same proxy table as dev, so /api and
    // /testauth reach the compose stack from the built bundle.
    command: `npm run build && npx vite preview --port ${port}`,
    port,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
