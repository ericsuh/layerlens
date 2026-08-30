import { defineConfig, devices } from "@playwright/test";

/**
 * End-to-end tests run against the **real built binary** serving the vendored
 * fixtures: no Docker daemon, no network, no mock server (ARCHITECTURE §9.4).
 * That is the whole point — the golden workflow is an acceptance criterion for
 * the shipped artifact, so anything that stands in for the artifact would test
 * the wrong thing.
 */
const PORT = 43117;

export default defineConfig({
  testDir: "./e2e",
  outputDir: "./.e2e-artifacts",
  fullyParallel: true,
  forbidOnly: process.env.CI !== undefined,
  retries: 0,
  workers: 4,
  reporter: [["list"]],
  timeout: 45_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: `http://127.0.0.1:${String(PORT)}`,
    // 1440×900 is DESIGN §8's target viewport; the screenshots reviewed for
    // this phase were taken at exactly this size.
    viewport: { width: 1440, height: 900 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      // The viewport goes *after* the device spread: `Desktop Chrome` carries
      // its own 1280×720, which would silently override the DESIGN §8 target
      // this phase's screenshots were reviewed at.
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } },
    },
  ],
  webServer: {
    // The binary and the fixtures live at the repository root; this config
    // lives under web/ because that is where node_modules is — the repo has
    // exactly one npm project and a second one just to host a config would be
    // a worse trade (DECISIONS, phase 007 delta).
    cwd: "..",
    // `.e2e-data` is wiped first so an LRU or a half-written cache entry from a
    // previous run can never make a test flake (or pass).
    command: `rm -rf .e2e-data && ./bin/layerlens --listen 127.0.0.1:${String(PORT)} --data-dir .e2e-data --fixtures-dir fixtures`,
    url: `http://127.0.0.1:${String(PORT)}/healthz`,
    reuseExistingServer: false,
    stdout: "ignore",
    stderr: "pipe",
    timeout: 120_000,
  },
});
