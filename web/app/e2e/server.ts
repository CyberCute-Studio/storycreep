// The server that the end-to-end tests run against, which Playwright starts
// (playwright.config.ts): the storycreep binary, built with the webdist tag,
// so that it serves the app's build, which the e2e script makes first, as
// the container image does. It runs as the binary of the image runs,
// migrating its database at startup, and stops when Playwright stops this
// process. Node runs this file as it is, without a build.

import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const server = fileURLToPath(new URL("../../../server/", import.meta.url));
const dir = mkdtempSync(join(tmpdir(), "storycreep-e2e-"));
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
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.on(signal, () => child.kill(signal));
}
child.on("exit", (code, signal) => {
  rmSync(dir, { recursive: true, force: true });
  process.exit(code ?? (signal === null ? 0 : 1));
});
