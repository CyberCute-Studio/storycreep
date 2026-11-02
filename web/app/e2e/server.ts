// The server that the end-to-end tests run against, which Playwright starts
// (playwright.config.ts): the storycreep binary, built with the webdist tag,
// so that it serves the app's build, which the e2e script makes first, as
// the container image does. It runs as the binary of the image runs,
// migrating its database at startup, and stops when Playwright stops this
// process's group, which it is in. Node runs this file as it is, without a
// build.

import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const server = fileURLToPath(new URL("../../../server/", import.meta.url));
// The directory of the binary and of the uploaded files: one for each port
// that the server listens on, so that runs on other ports keep apart. A run
// starts it anew, and removes it when Playwright stops the server. On
// Windows, Playwright kills the server instead, which leaves the directory
// for the next run on the port to remove, rather than one more each run.
const address = process.env.STORYCREEP_HTTP_ADDR ?? "";
const port = address.slice(address.lastIndexOf(":") + 1) || "default";
const dir = join(tmpdir(), `storycreep-e2e-${port}`);
rmSync(dir, { recursive: true, force: true });
mkdirSync(dir, { recursive: true });
const binary = join(
  dir,
  process.platform === "win32" ? "storycreep.exe" : "storycreep",
);

const build = spawnSync(
  "go",
  ["build", "-tags", "webdist", "-o", binary, "./cmd/storycreep"],
  { cwd: server, stdio: "inherit" },
);
if (build.status !== 0) {
  rmSync(dir, { recursive: true, force: true });
  process.exit(build.status ?? 1);
}

const child = spawn(binary, ["serve"], {
  stdio: "inherit",
  env: { ...process.env, STORYCREEP_ASSET_STORAGE: join(dir, "assets") },
});
// Playwright stops the server by signalling this process's whole group, as
// a terminal's Ctrl+C does, so the binary gets the signal itself: passing it
// on would send it a second, which ends it at once rather than let it shut
// down (cmd/storycreep). This process only waits for the binary to exit, to
// remove the directory, rather than end at the signal, as it would without
// a handler.
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => {});
}
child.on("exit", (code, signal) => {
  rmSync(dir, { recursive: true, force: true });
  process.exit(code ?? (signal === null ? 0 : 1));
});
// A binary that cannot be started, as one that the system refuses to run,
// never exits, so this process says why and ends, rather than crash on the
// event and leave Playwright to wait for a server that never comes.
child.on("error", (error) => {
  console.error(`e2e/server.ts: cannot start ${binary}: ${error.message}`);
  rmSync(dir, { recursive: true, force: true });
  process.exit(1);
});
