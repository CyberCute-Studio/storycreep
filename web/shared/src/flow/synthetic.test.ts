import { describe, expect, test } from "vitest";
import { SYNTHETIC_VERSION } from "../../../../testdata/synthetic-schema/register";
import { LoadError, load, SchemaUnsupportedError } from "./load";
import { edited, type FileJSON, fixture, withManifest } from "./testing";
import { supportedSchema } from "./versions";

// The loader with the test-only synthetic version registered, as its hook
// registers it (plan notes N2 and N84), which this file alone imports, as
// vitest runs each test file in a module graph of its own.

describe("with the synthetic version registered", () => {
  test("the loader reads it after version 1", () => {
    expect(SYNTHETIC_VERSION).toBe(2);
    expect(supportedSchema()).toEqual({ min: 1, max: SYNTHETIC_VERSION });
  });

  // Each version's fixture validates against its own version's schemas.
  //
  // Verifies: FR-DATA-03/AC1
  test("load each version's fixture", async () => {
    await expect(load(fixture(1))).resolves.toBeDefined();
    await expect(load(fixture(SYNTHETIC_VERSION))).resolves.toBeDefined();
  });

  test("refuse a version above it as schema_unsupported", async () => {
    await expect(
      load(withManifest(fixture(1), { schemaVersion: SYNTHETIC_VERSION + 1 })),
    ).rejects.toMatchObject({
      code: "schema_unsupported",
      supported: { min: 1, max: SYNTHETIC_VERSION },
    });
  });

  // Each file is checked against the schemas of its manifest's version: a
  // position that is not whole, which version 1 holds, and a board's file of
  // version 1 beside a manifest of the synthetic version.
  test("check each file against its manifest's version", async () => {
    const board = "boards/harbour-intro.json";
    for (const edit of [
      (b: FileJSON) => {
        b.nodes[0].position = { x: 1.5, y: 0 };
      },
      (b: FileJSON) => {
        b.schemaVersion = 1;
      },
    ]) {
      const refused = load(
        edited(fixture(SYNTHETIC_VERSION), { [board]: edit }),
      );
      await expect(refused, String(edit)).rejects.toBeInstanceOf(LoadError);
      await expect(refused, String(edit)).rejects.not.toBeInstanceOf(
        SchemaUnsupportedError,
      );
      await expect(refused, String(edit)).rejects.toThrow(board);
    }
  });
});
