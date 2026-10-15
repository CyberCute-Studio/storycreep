import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, expect, it } from "vitest";

// The lint rule against text that is not from the message catalog is a Biome
// plugin, web/shared/lint/hardcoded-text.grit, which biome.json applies to
// the web app's and the public site's source. The test lints the fixtures in
// web/shared/lint/testdata as if they were that source: in a copy of the
// repository's biome.json and the plugin, so that it checks the
// configuration that CI runs, not a copy of it.

const root = fileURLToPath(new URL("../../../../", import.meta.url));
const biome = join(root, "node_modules", "@biomejs", "biome", "bin", "biome");
const plugin = "web/shared/lint/hardcoded-text.grit";

interface Diagnostic {
  category: string;
  message: string;
  location: { path: string; start: { line: number } };
}

let dir: string;
let diagnostics: Diagnostic[];

// The fixtures, where they are linted: as the app's and the site's source,
// as a test, which the rule leaves alone, and outside the web's source.
const placed: Record<string, string> = {
  "web/app/src/board/hardcoded.tsx": "hardcoded.tsx",
  "web/site/src/pages/hardcoded.tsx": "hardcoded.tsx",
  "web/app/src/board/hardcoded.test.tsx": "hardcoded.tsx",
  "server/hardcoded.tsx": "hardcoded.tsx",
  "web/app/src/board/translated.tsx": "translated.tsx",
};

beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "storycreep-lint-"));
  const copy = (from: string, to: string) => {
    mkdirSync(dirname(join(dir, to)), { recursive: true });
    copyFileSync(join(root, from), join(dir, to));
  };
  copy("biome.json", "biome.json");
  copy(plugin, plugin);
  for (const [to, fixture] of Object.entries(placed)) {
    copy(join("web/shared/lint/testdata", fixture), to);
  }
  // The copy is not a git checkout, so Biome is told not to look for one.
  const run = spawnSync(
    process.execPath,
    [biome, "lint", "--vcs-enabled=false", "--reporter=json", "."],
    { cwd: dir, encoding: "utf8" },
  );
  if (run.error) {
    throw run.error;
  }
  diagnostics = (JSON.parse(run.stdout) as { diagnostics: Diagnostic[] })
    .diagnostics;
});

afterAll(() => {
  rmSync(dir, { recursive: true, force: true });
});

// The lines of the plugin's diagnostics in a file, in order.
function reported(path: string): number[] {
  return diagnostics
    .filter(
      (d) =>
        d.category === "plugin" &&
        d.location.path.replaceAll("\\", "/") === path,
    )
    .map((d) => d.location.start.line)
    .sort((a, b) => a - b);
}

// Verifies: FR-I18N-01/AC1
it("reports text that is not from the message catalog in the app's and the site's source", () => {
  // JSX text starts after the tag before it, so the text "Board settings"
  // is reported on the line of <section>, as its title is.
  const lines = [11, 11, 13, 14, 15, 16, 18, 20, 21, 22, 23, 24, 25];
  expect(reported("web/app/src/board/hardcoded.tsx")).toEqual(lines);
  expect(reported("web/site/src/pages/hardcoded.tsx")).toEqual(lines);
  const first = diagnostics.find((d) => d.category === "plugin");
  expect(first?.message).toContain("web/shared/i18n/en.json");
});

it("leaves alone text from the catalog, tests, and code outside the web's source", () => {
  expect(reported("web/app/src/board/translated.tsx")).toEqual([]);
  expect(reported("web/app/src/board/hardcoded.test.tsx")).toEqual([]);
  expect(reported("server/hardcoded.tsx")).toEqual([]);
});

it("is the plugin that biome.json applies", () => {
  const config = JSON.parse(readFileSync(join(root, "biome.json"), "utf8")) as {
    overrides?: { plugins?: string[] }[];
  };
  expect(
    config.overrides?.some((o) => o.plugins?.includes(`./${plugin}`)),
  ).toBe(true);
});
