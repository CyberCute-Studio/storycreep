import { readFileSync } from "node:fs";
import { join } from "node:path";
import { beforeAll, expect, it } from "vitest";
import {
  type Diagnostic,
  lintPlaced,
  reportedLines,
  repositoryRoot,
} from "../lint/testing";

// The lint rule against text that is not from the message catalog is a Biome
// plugin, web/shared/lint/hardcoded-text.grit, which biome.json applies to
// the web app's and the public site's source. The test lints the fixtures in
// web/shared/lint/testdata as if they were that source (lintPlaced), with
// the repository's biome.json, so that it checks the configuration that CI
// runs, not a copy of it.

const plugin = "web/shared/lint/hardcoded-text.grit";
const fixtures = "web/shared/lint/testdata";

let diagnostics: Diagnostic[];

beforeAll(() => {
  // The fixtures, where they are linted: as the app's and the site's
  // source, as a test, which the rule leaves alone, and outside the web's
  // source.
  diagnostics = lintPlaced({
    "web/app/src/board/hardcoded.tsx": `${fixtures}/hardcoded.tsx`,
    "web/site/src/pages/hardcoded.tsx": `${fixtures}/hardcoded.tsx`,
    "web/app/src/board/hardcoded.test.tsx": `${fixtures}/hardcoded.tsx`,
    "server/hardcoded.tsx": `${fixtures}/hardcoded.tsx`,
    "web/app/src/board/translated.tsx": `${fixtures}/translated.tsx`,
  });
});

// The lines of the rule's diagnostics in a file, in order: the app's source
// has the web app's rules too.
function reported(path: string): number[] {
  return reportedLines(diagnostics, path, "web/shared/i18n/en.json");
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
  const config = JSON.parse(
    readFileSync(join(repositoryRoot, "biome.json"), "utf8"),
  ) as {
    overrides?: { plugins?: string[] }[];
  };
  expect(
    config.overrides?.some((o) => o.plugins?.includes(`./${plugin}`)),
  ).toBe(true);
});
