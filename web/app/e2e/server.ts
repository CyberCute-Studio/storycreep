// The server that the end-to-end tests run against, which Playwright starts
// (playwright.config.ts): the storycreep binary, built with the webdist tag,
// so that it serves the app's build, which the e2e script makes first, as
// the container image does (instance.ts). It runs on a database of its own,
// made anew for the run, as the binary of the image runs, migrating it at
// startup, and its first-run setup is completed from the configuration,
// unattended, with the owner of environment.ts, whose configuration is
// this file's alone (serverEnv). It stops when Playwright
// stops this process's group, which it is in. Node runs this file as it is,
// without a build.

import { spawn } from "node:child_process";
import { rmSync } from "node:fs";
import { baseURL, binaries, binary, owner, port } from "./environment.ts";
import { build, createDatabase, dropDatabase, serverEnv } from "./instance.ts";

// The run starts the directory of the binaries and of the uploaded files
// anew, and removes it, with the database, when Playwright stops the server.
// On Windows, Playwright kills the server instead, which leaves both for the
// next run on the port to replace, rather than one more each run.
build();
const database = `storycreep_e2e_${port}`;
createDatabase(database);

const cleanUp = () => {
  try {
    dropDatabase(database);
  } catch (error) {
    console.error(`e2e/server.ts: ${(error as Error).message}`);
  }
  rmSync(binaries, { recursive: true, force: true });
};

const child = spawn(binary("storycreep"), ["serve"], {
  stdio: "inherit",
  env: serverEnv({
    database,
    address: `127.0.0.1:${port}`,
    publicURL: baseURL,
    env: {
      // First-run setup completes unattended, with the owner whom the tests
      // sign in as to take the links that an instance admin gives out, and
      // anyone may register, with an invitation or without.
      STORYCREEP_SETUP_OWNER_EMAIL: owner.email,
      STORYCREEP_SETUP_OWNER_NAME: owner.name,
      STORYCREEP_SETUP_OWNER_PASSWORD: owner.password,
      STORYCREEP_SETUP_WORKSPACE_NAME: owner.workspace,
      STORYCREEP_SIGNUP: "open",
      // Each test is a client of an address of its own, which its
      // X-Forwarded-For names (fixtures.ts), so that the tests, which all
      // come from 127.0.0.1, do not share the rate limits of one address, as
      // the 20 requests a minute of signing in (PRD NFR-SEC-06).
      STORYCREEP_TRUSTED_PROXIES: "127.0.0.1",
    },
  }),
});
// Playwright stops the server by signalling this process's whole group, as
// a terminal's Ctrl+C does, so the binary gets the signal itself: passing it
// on would send it a second, which ends it at once rather than let it shut
// down (cmd/storycreep). This process only waits for the binary to exit, to
// clean up, rather than end at the signal, as it would without a handler.
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {});
}
child.on("exit", (code, signal) => {
  cleanUp();
  process.exit(code ?? (signal === null ? 0 : 1));
});
// A binary that cannot be started, as one that the system refuses to run,
// never exits, so this process says why and ends, rather than crash on the
// event and leave Playwright to wait for a server that never comes.
child.on("error", (error) => {
  console.error(
    `e2e/server.ts: cannot start ${binary("storycreep")}: ${error.message}`,
  );
  cleanUp();
  process.exit(1);
});
