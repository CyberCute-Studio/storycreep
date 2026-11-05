// The end-to-end tests (PRD §10.6, NFR-A11Y-08): the app as the server
// serves it, in a desktop browser and on a touch tablet, which run every
// test of e2e/. Playwright starts the server (e2e/server.ts) on a database
// of its own, on the PostgreSQL server of the development compose file
// (deploy/README.md), or the one that the STORYCREEP_E2E_DATABASE_*
// variables name, as CI's does (e2e/environment.ts).

import { defineConfig, devices } from "@playwright/test";
import { baseURL, peerURL } from "./e2e/environment.ts";

// Playwright has no build of Chromium for Windows on ARM64, whose x64 build
// would run emulated (PRD §9.9), so the tests run in Windows's own Edge
// there, which is Chromium too.
const channel =
  process.platform === "win32" && process.arch === "arm64"
    ? "msedge"
    : undefined;

export default defineConfig({
  testDir: "e2e",
  forbidOnly: process.env.CI !== undefined,
  retries: process.env.CI === undefined ? 0 : 1,
  reporter: process.env.CI === undefined ? "list" : [["list"], ["html"]],
  use: { baseURL, trace: "retain-on-failure" },
  projects: [
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"], channel },
    },
    {
      // The reference tablet is a mid-range Android tablet (PRD §9.1).
      name: "tablet",
      use: { ...devices["Galaxy Tab S9"], channel },
    },
  ],
  // Playwright starts its servers one after another, each once the one
  // before it answers its url, and stops them the other way round: the
  // peer, a second process of the server's instance, on its database
  // (e2e/peer.ts), starts once the server has built the binaries and made
  // and migrated the database, and stops before the server drops it. One
  // peer for the run, rather than one that each test that needs it starts,
  // adds a process's connections to PostgreSQL once, not once a worker.
  webServer: [
    {
      command: "node e2e/server.ts",
      url: `${baseURL}/readyz`,
      // Building the server, and migrating a new database, take a while.
      timeout: 300_000,
      reuseExistingServer: false,
      // Without it, Playwright kills the server outright, and e2e/server.ts
      // could not remove its directory. Windows has no such signal, and there
      // Playwright kills it anyway, so the next run on the port reuses the
      // directory.
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
    },
    {
      command: "node e2e/peer.ts",
      url: `${peerURL}/readyz`,
      timeout: 120_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
    },
  ],
});
