// The peer of the server that the end-to-end tests run against: a second
// process of the same instance, the binary that server.ts built, on that
// server's database, at a port and a public URL of its own, as a deployment
// runs several behind a load balancer, so that a test reaches the instance
// through two processes, whose hubs pass each other's operations on through
// PostgreSQL's LISTEN/NOTIFY (PRD FR-RT-02 AC1). Playwright starts it after
// the server, once that has built the binaries, made the database, migrated
// it, and completed first-run setup, and stops it before the server, which
// drops the database then (playwright.config.ts). So it makes, migrates,
// and drops nothing: its migrations, which run under the same advisory lock
// as the server's, find none to apply, and it is given no setup keys, which
// a completed setup ignores. It is not startInstance (instance.ts), which
// makes its database anew and drops it once stopped, as the server's would
// be under the server. Node runs this file as it is, without a build.

import { spawn } from "node:child_process";
import { binary, mainDatabase, peerPort, peerURL } from "./environment.ts";
import { serverEnv } from "./instance.ts";

const child = spawn(binary("storycreep"), ["serve"], {
  stdio: "inherit",
  env: serverEnv({
    database: mainDatabase,
    address: `127.0.0.1:${peerPort}`,
    // The peer's own URL, which its pages, and their sockets, come from:
    // the Origin that it checks a socket's handshake against.
    publicURL: peerURL,
    env: {
      // The configuration of the server's process that each process of an
      // instance reads for itself, as server.ts gives it.
      STORYCREEP_SIGNUP: "open",
      STORYCREEP_TRUSTED_PROXIES: "127.0.0.1",
    },
  }),
});
// The peer stops as the server does (server.ts): the binary gets the
// group's signal itself, and this process waits for it to exit.
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {});
}
child.on("exit", (code, signal) => {
  process.exit(code ?? (signal === null ? 0 : 1));
});
child.on("error", (error) => {
  console.error(
    `e2e/peer.ts: cannot start ${binary("storycreep")}: ${error.message}`,
  );
  process.exit(1);
});
