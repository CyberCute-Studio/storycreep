// The canvas's benchmark harness (PRD §9.1, plan item 59): the app as the
// server serves it, on a database of its own, loaded once with the medium,
// dense, and large fixtures and with boards of 200, 2,000, and 5,000 nodes
// (bench/fixtures.setup.ts), whose boards the canvas's scripted runs
// measure in a desktop browser and on an emulated tablet (bench/canvas.spec.ts).
// It runs alone, one test at a time, so that no other test shares the
// machine, as `pnpm bench`, and in CI's job of its own, which reports what it
// measured. It shares the end-to-end tests' server, port, and database
// settings (e2e/environment.ts), so the two do not run at once on one machine.

import { defineConfig, devices } from "@playwright/test";
import { benchBase, remote, signedInState } from "./bench/state.ts";

// As the end-to-end tests do: Playwright has no Chromium of Windows on ARM64.
const channel =
  process.platform === "win32" && process.arch === "arm64"
    ? "msedge"
    : undefined;

// WebGL in a browser without a GPU, as CI's runner, is SwiftShader's, which
// Chromium no longer falls back to by itself; and the heap's size exactly,
// rather than in buckets.
const launchOptions = {
  args: ["--enable-unsafe-swiftshader", "--enable-precise-memory-info"],
};

export default defineConfig({
  testDir: "bench",
  forbidOnly: process.env.CI !== undefined,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  // A run pans for a minute or more, after loading the fixtures.
  timeout: 15 * 60_000,
  reporter: process.env.CI === undefined ? "list" : [["list"], ["html"]],
  outputDir: "test-results/bench",
  use: { baseURL: benchBase, trace: "retain-on-failure" },
  projects: [
    { name: "setup", testMatch: /fixtures\.setup\.ts/, timeout: 30 * 60_000 },
    {
      name: "desktop",
      dependencies: ["setup"],
      use: {
        ...devices["Desktop Chrome"],
        // The reference desktop's screen, rather than Playwright's 1280 by 720.
        viewport: { width: 1920, height: 1080 },
        channel,
        launchOptions,
        storageState: signedInState,
      },
    },
    {
      // The reference tablet is a mid-range Android tablet (PRD §9.1),
      // whose processor the runs emulate as four times slower than the
      // runner's.
      name: "tablet",
      dependencies: ["setup"],
      use: {
        ...devices["Galaxy Tab S9"],
        channel,
        launchOptions,
        storageState: signedInState,
      },
    },
  ],
  // An instance of the reference tablet's checklist is running already.
  webServer: remote
    ? undefined
    : {
        command: "node e2e/server.ts",
        url: `${benchBase}/readyz`,
        timeout: 300_000,
        reuseExistingServer: false,
        gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
      },
});
