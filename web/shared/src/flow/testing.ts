// What the tests of the flow share: they read the snapshots of the
// conformance suite from the disk, as Node does, which the browser's
// typecheck, of tsconfig.browser.json, leaves out with this file.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * schema/conformance, whose README describes its suites and scenarios, which
 * the Go reference runtime runs too (TestConformance).
 */
export const conformance = fileURLToPath(
  new URL("../../../../schema/conformance/", import.meta.url),
);

/**
 * Returns the files of the directory dir, by their paths under it, with
 * forward slashes.
 */
export function readTree(dir: string): Map<string, Uint8Array> {
  const files = new Map<string, Uint8Array>();
  const walk = (sub: string) => {
    for (const name of readdirSync(join(dir, sub))) {
      const path = sub === "" ? name : `${sub}/${name}`;
      if (statSync(join(dir, path)).isDirectory()) {
        walk(path);
      } else {
        files.set(path, new Uint8Array(readFileSync(join(dir, path))));
      }
    }
  };
  walk("");
  return files;
}
