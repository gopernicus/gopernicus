import { defineConfig, devices } from "@playwright/test";

// The showcase Go server is started by Playwright's webServer from the module
// root (cwd: ".."). Port is overridable so the harness can run alongside a dev
// server.
const PORT = process.env.SHOWCASE_PORT ?? "8099";
const HOST = "127.0.0.1";
const baseURL = `http://${HOST}:${PORT}`;

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // Bounded flake policy: at most 2 retries (3 attempts total) in CI, 0 locally.
  // A genuinely flaky spec is retried a bounded number of times; a hard failure
  // still fails the gate. Retries are recorded in the report, not hidden.
  retries: process.env.CI ? 2 : 0,
  // Serialize workers in CI so the single Go webServer is not overwhelmed and
  // results are deterministic across the three engines.
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI
    ? [["github"], ["html", { open: "never" }], ["list"]]
    : [["list"]],
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  // The three-engine release gate (Gate A ratified): Chromium, Firefox, WebKit.
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
  webServer: {
    command: "go run ./cmd/server",
    cwd: "..",
    url: baseURL,
    env: { PORT, HOST, LOG_LEVEL: "ERROR" },
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
