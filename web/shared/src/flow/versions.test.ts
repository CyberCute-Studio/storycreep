import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import {
  ASTUnsupportedError,
  LoadError,
  load,
  SchemaUnsupportedError,
} from "./load";
import { fixture, withManifest } from "./testing";
import { registerSchemaVersion, supportedSchema } from "./versions";

// The versions of the published format that play mode's loader reads, in a
// build: version 1 alone (PRD §6.4, plan item 77).

const root = join(import.meta.dirname, "../../../..");

describe("the versions that a build reads", () => {
  test("are version 1 alone", () => {
    expect(supportedSchema()).toEqual({ min: 1, max: 1 });
  });

  // Each version's fixture validates, as load checks each file against its
  // version's schemas.
  //
  // Verifies: FR-DATA-03/AC1
  test("load version 1's fixture", async () => {
    await expect(load(fixture(1))).resolves.toBeDefined();
  });

  // A manifest of another version is the runtime's schema_unsupported (PRD
  // §6.4), before anything else of it is checked, a newer astVersion among
  // it, as Go's Load refuses it, but that it is JSON as every version's
  // manifest is, so that one that holds null is a LoadError whatever its
  // version; and one that is no version is a LoadError of its schema.
  test("refuse another version as schema_unsupported", async () => {
    const refused = load(fixture(2));
    await expect(refused).rejects.toBeInstanceOf(SchemaUnsupportedError);
    await expect(refused).rejects.toMatchObject({
      code: "schema_unsupported",
      version: 2,
      supported: { min: 1, max: 1 },
    });
    await expect(refused).rejects.toThrow(
      "the snapshot's schemaVersion is 2, and this runtime reads 1",
    );
    const older = load(
      withManifest(fixture(1), { schemaVersion: 0, astVersion: 99 }),
    );
    await expect(older).rejects.toBeInstanceOf(SchemaUnsupportedError);
    await expect(older).rejects.not.toBeInstanceOf(ASTUnsupportedError);
    const withNull = load(
      withManifest(fixture(1), { schemaVersion: 2, generator: null }),
    );
    await expect(withNull).rejects.toThrow("holds null");
    await expect(withNull).rejects.not.toBeInstanceOf(SchemaUnsupportedError);
    for (const schemaVersion of [1.5, "1", 2 ** 53]) {
      const none = load(withManifest(fixture(1), { schemaVersion }));
      await expect(none, String(schemaVersion)).rejects.toBeInstanceOf(
        LoadError,
      );
      await expect(none, String(schemaVersion)).rejects.not.toBeInstanceOf(
        SchemaUnsupportedError,
      );
    }
  });

  test("register a version only after the newest", () => {
    const docs = {
      manifest: {},
      settings: {},
      variables: {},
      componentTypes: {},
      components: {},
      board: {},
    };
    expect(() => registerSchemaVersion(1, docs)).toThrow();
    expect(() => registerSchemaVersion(3, docs)).toThrow();
    expect(supportedSchema()).toEqual({ min: 1, max: 1 });
  });

  // No module of the web packages but a test imports, re-exports, or
  // requires the synthetic version's registration hook, so that no build
  // reads the version (plan note N2): a module of TypeScript or JavaScript,
  // of .ts, .tsx, .js, .jsx, .mts, .cts, .mjs, or .cjs, and a test one of
  // them named .test or .spec.
  test("are never extended by a file that is not a test", () => {
    const imports =
      /\b(?:from|import|require)\s*\(?\s*["'`][^"'`]*synthetic-schema/;
    const module = /\.[cm]?[jt]sx?$/;
    const testModule = /\.(test|spec)\.[cm]?[jt]sx?$/;
    for (const statement of [
      'import "../../testdata/synthetic-schema/register";',
      'import { SYNTHETIC_VERSION } from "../testdata/synthetic-schema/register";',
      'export * from "../testdata/synthetic-schema/register";',
      'await import("../testdata/synthetic-schema/register.ts");',
      'require("../testdata/synthetic-schema/register");',
    ]) {
      expect(imports.test(statement), statement).toBe(true);
    }
    expect(imports.test("// testdata/synthetic-schema holds its schemas")).toBe(
      false,
    );
    for (const name of ["a.ts", "a.tsx", "a.jsx", "a.mts", "a.cts", "a.cjs"]) {
      expect(module.test(name) && !testModule.test(name), name).toBe(true);
    }
    for (const name of ["a.test.ts", "a.spec.mjs", "a.test.cts"]) {
      expect(testModule.test(name), name).toBe(true);
    }
    const importers: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const path = join(dir, name);
        if (name === "node_modules" || name === "dist") {
          continue;
        }
        if (statSync(path).isDirectory()) {
          walk(path);
        } else if (
          module.test(name) &&
          !testModule.test(name) &&
          imports.test(readFileSync(path, "utf8"))
        ) {
          importers.push(path);
        }
      }
    };
    walk(join(root, "web"));
    expect(importers).toEqual([]);
  });
});
