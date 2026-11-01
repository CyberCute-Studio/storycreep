// The end-to-end tests (PRD §10.6, NFR-A11Y-08): the app as the server
// serves it, in a desktop browser and on a touch tablet, which run every
// test of e2e/. Playwright starts the server (e2e/server.ts) with a database
// of the development compose file (deploy/README.md), or the one that the
// STORYCREEP_E2E_DATABASE_* variables name, as CI's does.

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.STORYCREEP_E2E_PORT ?? 8788);
const baseURL = `http://127.0.0.1:${port}`;
const database = (login: string) =>
  `postgres://${login}:storycreep-dev@localhost:5432/storycreep`;

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
  webServer: {
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
    env: {
      STORYCREEP_HTTP_ADDR: `127.0.0.1:${port}`,
      STORYCREEP_ADMIN_ADDR: "off",
      STORYCREEP_PUBLIC_URL: baseURL,
      STORYCREEP_LOG_LEVEL: "warn",
      STORYCREEP_DATABASE_URL:
        process.env.STORYCREEP_E2E_DATABASE_URL ?? database("storycreep"),
      STORYCREEP_DATABASE_ADMIN_URL:
        process.env.STORYCREEP_E2E_DATABASE_ADMIN_URL ?? database("postgres"),
      STORYCREEP_DATABASE_MAINTENANCE_URL:
        process.env.STORYCREEP_E2E_DATABASE_MAINTENANCE_URL ??
        database("storycreep_maintainer"),
    },
  },
});
