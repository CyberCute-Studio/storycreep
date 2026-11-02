// The end-to-end tests (PRD §10.6, NFR-A11Y-08): the app as the server
// serves it, in a desktop browser and on a touch tablet, which run every
// test of e2e/. Playwright starts the server (e2e/server.ts) on a database
// of its own, on the PostgreSQL server of the development compose file
// (deploy/README.md), or the one that the STORYCREEP_E2E_DATABASE_*
// variables name, as CI's does (e2e/environment.ts).

import { defineConfig, devices } from "@playwright/test";
import { baseURL, owner, port } from "./e2e/environment.ts";

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
      STORYCREEP_PUBLIC_URL: baseURL,
      // First-run setup completes unattended, with the owner whom the tests
      // sign in as to take the links that an instance admin gives out, and
      // anyone may register, with an invitation or without.
      STORYCREEP_SETUP_OWNER_EMAIL: owner.email,
      STORYCREEP_SETUP_OWNER_NAME: owner.name,
      STORYCREEP_SETUP_OWNER_PASSWORD: owner.password,
      STORYCREEP_SETUP_WORKSPACE_NAME: owner.workspace,
      STORYCREEP_SIGNUP: "open",
      // Each test is a client of an address of its own, which its
      // X-Forwarded-For names (e2e/fixtures.ts), so that the tests, which all
      // come from 127.0.0.1, do not share the rate limits of one address, as
      // the 20 requests a minute of signing in (PRD NFR-SEC-06).
      STORYCREEP_TRUSTED_PROXIES: "127.0.0.1",
    },
  },
});
