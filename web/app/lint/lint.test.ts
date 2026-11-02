// @vitest-environment node
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  type Diagnostic,
  lintPlaced,
  reportedLines,
  repositoryRoot,
} from "@storycreep/shared/lint/testing";
import { beforeAll, expect, it } from "vitest";

// The web app calls the server only through the API client of
// @storycreep/shared/api, whose types are generated from the OpenAPI
// document, which a Biome plugin, web/app/lint/direct-request.grit, holds
// the app's source to. The test lints the fixtures in web/app/lint/testdata
// as if they were that source, with the repository's biome.json, so that it
// checks the configuration that CI runs.

const plugin = "web/app/lint/direct-request.grit";
const fixtures = "web/app/lint/testdata";
const message = "@storycreep/shared/api";

let diagnostics: Diagnostic[];

beforeAll(() => {
  // As the app's source, as its test, which the rule leaves alone, and as
  // the shared package's source, where the client is.
  diagnostics = lintPlaced({
    "web/app/src/board/requests.ts": `${fixtures}/requests.ts`,
    "web/app/src/board/requests.test.ts": `${fixtures}/requests.ts`,
    "web/shared/src/api/requests.ts": `${fixtures}/requests.ts`,
    "web/app/src/board/client.ts": `${fixtures}/client.ts`,
  });
});

// Verifies: FR-API-02/AC2
it("reports a request that the app's source makes around the generated API client", () => {
  expect(
    reportedLines(diagnostics, "web/app/src/board/requests.ts", message),
  ).toEqual([9, 10, 11, 12, 13, 14, 16, 17, 18, 20, 22, 24, 29, 31, 32, 33]);
  expect(
    reportedLines(diagnostics, "web/app/src/board/client.ts", message),
  ).toEqual([]);
});

it("leaves alone the app's tests and the shared package", () => {
  expect(
    reportedLines(diagnostics, "web/app/src/board/requests.test.ts"),
  ).toEqual([]);
  expect(reportedLines(diagnostics, "web/shared/src/api/requests.ts")).toEqual(
    [],
  );
});

it("is a plugin that biome.json applies to the app's source", () => {
  const config = JSON.parse(
    readFileSync(join(repositoryRoot, "biome.json"), "utf8"),
  ) as { overrides?: { includes?: string[]; plugins?: string[] }[] };
  expect(
    config.overrides?.some(
      (o) =>
        o.includes?.includes("web/app/src/**") &&
        o.plugins?.includes(`./${plugin}`),
    ),
  ).toBe(true);
});
