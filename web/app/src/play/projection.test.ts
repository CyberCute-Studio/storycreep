import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import type { Connection, Node } from "@storycreep/shared/api";
import { readJSON } from "@storycreep/shared/expr";
import { load, Runtime, type Schema, validate } from "@storycreep/shared/flow";
import {
  conformance,
  readScenarios,
  readTree,
  runScenarios,
} from "@storycreep/shared/flow/testing";
import type { Board as BoardFile } from "@storycreep/shared/published";
import { describe, expect, test } from "vitest";
import boardSchema from "../../../../schema/published/v1/board.schema.json" with {
  type: "json",
};
import componentTypesSchema from "../../../../schema/published/v1/component_types.schema.json" with {
  type: "json",
};
import componentsSchema from "../../../../schema/published/v1/components.schema.json" with {
  type: "json",
};
import settingsSchema from "../../../../schema/published/v1/settings.schema.json" with {
  type: "json",
};
import manifestSchema from "../../../../schema/published/v1/storycreep.schema.json" with {
  type: "json",
};
import variablesSchema from "../../../../schema/published/v1/variables.schema.json" with {
  type: "json",
};
import { testConnection, testNode } from "../test/canvas";
import {
  draftOf,
  expression,
  richText,
  testBoard,
  testDraft,
  testProjectId,
  testVariable,
  text,
} from "../test/play";
import { encoded, projectDraft } from "./projection";

const now = new Date("2026-11-14T12:00:00.250Z");

// The conformance suite's suites, by name, each its snapshot's files, as Go
// wrote them, and its scenarios.
const suites = readdirSync(conformance)
  .filter((name) => statSync(join(conformance, name)).isDirectory())
  .sort()
  .map((name) => ({
    name,
    files: readTree(join(conformance, name, "snapshot")),
    scenarios: readScenarios(join(conformance, name)),
  }));

const decoder = new TextDecoder();

// manifestOf returns the manifest of files without its generator and its
// snapshot, which are of what wrote it and when.
function manifestOf(files: ReadonlyMap<string, Uint8Array>): unknown {
  const m = JSON.parse(
    decoder.decode(files.get("storycreep.json") as Uint8Array),
  ) as Record<string, unknown>;
  delete m.generator;
  delete m.snapshot;
  return m;
}

// The schema of each file of a snapshot, by its path's place.
function schemaOf(path: string): Schema {
  if (path === "storycreep.json") {
    return manifestSchema as Schema;
  }
  if (path.startsWith("boards/")) {
    return boardSchema as Schema;
  }
  return {
    "project/settings.json": settingsSchema,
    "project/variables.json": variablesSchema,
    "project/component_types.json": componentTypesSchema,
    "project/components.json": componentsSchema,
  }[path] as Schema;
}

// invalidFiles returns why each of files is not what its published JSON
// Schema takes, [] for none, each against the whole schema, its ASTs'
// definitions among it.
function invalidFiles(files: ReadonlyMap<string, Uint8Array>): string[] {
  const out: string[] = [];
  for (const [path, bytes] of files) {
    const problem = validate(schemaOf(path), readJSON(decoder.decode(bytes)));
    if (problem !== undefined) {
      out.push(`${path}: ${problem}`);
    }
  }
  return out;
}

describe("the conformance suite's snapshots", () => {
  // Each suite's snapshot, which the Go suites wrote, read back as the
  // draft that it was written of, projects to the same bytes, file by file,
  // and to the same manifest but for what wrote it and when, so that the
  // projection writes the published format as the server does; and play
  // mode's engine, on the projected snapshot, runs every scenario as the
  // reference runtime does, the values that texts show among them (FR-TEXT-02
  // AC3), in the order of the choices that the author gave (FR-EDIT-03 AC1).
  // Verifies: FR-TEXT-02/AC3
  test.each(suites)(
    "project back to their own files: $name",
    async ({ name, files, scenarios }) => {
      const projected = projectDraft(draftOf(files), { now });
      expect(projected.standIns).toEqual([]);
      expect([...projected.files.keys()].sort()).toEqual(
        [...files.keys()].sort(),
      );
      for (const [path, bytes] of files) {
        if (path !== "storycreep.json") {
          expect(
            decoder.decode(projected.files.get(path)),
            `${name}: ${path}`,
          ).toBe(decoder.decode(bytes));
        }
      }
      expect(manifestOf(projected.files)).toEqual(manifestOf(files));
      expect(invalidFiles(projected.files)).toEqual([]);
      // The scenarios of a release's snapshot save states with its ID and
      // version, which a draft has neither of: they run on the projected
      // files under the suite's own manifest's snapshot.
      const manifest = JSON.parse(
        decoder.decode(projected.files.get("storycreep.json")),
      );
      const own = JSON.parse(decoder.decode(files.get("storycreep.json")));
      const snapshot = await load(
        new Map([
          ...projected.files,
          ["storycreep.json", encoded({ ...manifest, snapshot: own.snapshot })],
        ]),
      );
      expect(runScenarios(name, snapshot, scenarios)).toEqual([]);
    },
  );
});

test("the manifest says that the snapshot is of the draft, pulled now", () => {
  const { files } = projectDraft(draftOf(suites[0].files), { now });
  const m = JSON.parse(
    decoder.decode(files.get("storycreep.json") as Uint8Array),
  );
  expect(m.generator).toEqual({ name: "storycreep-web", version: "play" });
  expect(m.snapshot).toEqual({
    channel: "draft",
    contentHash: expect.stringMatching(/^sha256:[0-9a-f]{64}$/),
    pulledAt: "2026-11-14T12:00:00Z",
  });
});

test("a file is laid out as the server writes it", () => {
  expect(
    decoder.decode(
      encoded({ b: [], a: { z: "<&>\u2028\u0007", y: 1.5e-7 }, c: {} }),
    ),
  ).toBe(
    '{\n  "a": {\n    "y": 1.5e-7,\n    "z": "<&>\\u2028\\u0007"\n  },\n  "b": [],\n  "c": {}\n}\n',
  );
});

// A board of a draft with what no snapshot holds, each of which the
// projection stands in for.
const intro = testBoard("bd_00000000000000000000000001", "Intro", {
  entry_node_id: "nd_00000000000000000000000002",
});
const other = testBoard("bd_00000000000000000000000003", "Other");
const other_gold = testVariable(
  "var_00000000000000000000000004",
  "secret",
  "int",
  1,
  {
    scope: "board",
    board_id: other.id,
  },
);
const gold = testVariable("var_00000000000000000000000005", "gold", "int", 3);
const onIntro = { board_id: intro.id };
const brokenDraft = testDraft({
  boards: [intro, other],
  variables: [gold, other_gold],
  nodes: [
    testNode("nd_00000000000000000000000006", 0, 0, {
      ...onIntro,
      on_enter: expression("gold +="),
      speaker_component_id: "cmp_00000000000000000000000007",
      text: richText(
        text("You have "),
        { type: "valueRef", attrs: { ref: gold.id } },
        text(" and "),
        { type: "valueRef", attrs: { ref: other_gold.id } },
        text(" gold, "),
        { type: "mention", attrs: { id: "cmp_00000000000000000000000007" } },
        text("."),
      ),
    }),
    testNode("nd_00000000000000000000000008", 200, 0, { ...onIntro }),
    {
      ...testNode("nd_00000000000000000000000009", 400, 0, { ...onIntro }),
      type: "jumper",
      target: { board_id: other.id, node_id: "nd_00000000000000000000000010" },
    } as Node,
    {
      ...testNode("nd_00000000000000000000000011", 400, 200, { ...onIntro }),
      type: "jumper",
      target: { board_id: intro.id, node_id: "nd_00000000000000000000000009" },
    } as Node,
    testNode("nd_00000000000000000000000010", 0, 0, {
      board_id: other.id,
      deleted_at: "2026-11-02T10:00:00Z",
    }),
  ],
  connections: [
    testConnection(
      "cn_00000000000000000000000012",
      "nd_00000000000000000000000006",
      "nd_00000000000000000000000008",
      {
        ...onIntro,
        condition: expression("gold >"),
        order: "a",
      },
    ),
    testConnection(
      "cn_00000000000000000000000013",
      "nd_00000000000000000000000006",
      "nd_00000000000000000000000009",
      {
        ...onIntro,
        order: "b",
      },
    ),
    testConnection(
      "cn_00000000000000000000000014",
      "nd_00000000000000000000000008",
      "nd_00000000000000000000000006",
      {
        ...onIntro,
        from_port: "bo_00000000000000000000000015",
      },
    ),
  ] as Connection[],
  entry: { board_id: other.id, node_id: "nd_00000000000000000000000010" },
});

describe("a draft with what no snapshot holds", () => {
  test("is projected with what it stands in for listed", () => {
    const { standIns } = projectDraft(brokenDraft, { now });
    expect(standIns).toEqual([
      {
        kind: "speaker",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        ref: "cmp_00000000000000000000000007",
      },
      {
        kind: "reference",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        field: "text",
        ref: other_gold.id,
      },
      {
        kind: "reference",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        field: "text",
        ref: "cmp_00000000000000000000000007",
      },
      expect.objectContaining({
        kind: "expression",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        field: "on_enter",
        source: "gold +=",
      }),
      {
        kind: "target",
        board: intro.id,
        node: "nd_00000000000000000000000009",
        entity: "nd_00000000000000000000000009",
        ref: "nd_00000000000000000000000010",
      },
      {
        kind: "target",
        board: intro.id,
        node: "nd_00000000000000000000000011",
        entity: "nd_00000000000000000000000011",
        ref: "nd_00000000000000000000000009",
      },
      {
        kind: "connection",
        board: intro.id,
        node: "nd_00000000000000000000000008",
        entity: "cn_00000000000000000000000014",
      },
      expect.objectContaining({
        kind: "expression",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "cn_00000000000000000000000012",
        field: "condition",
        source: "gold >",
      }),
      {
        kind: "board_entry",
        board: intro.id,
        entity: intro.id,
        ref: "nd_00000000000000000000000002",
      },
      {
        kind: "entry",
        board: other.id,
        entity: testProjectId,
        ref: "nd_00000000000000000000000010",
      },
    ]);
  });

  // Every projected file, of a draft that the projection stands in for
  // anything of or not, validates against its published JSON Schema.
  test("validates against the published JSON Schemas", () => {
    expect(invalidFiles(projectDraft(brokenDraft, { now }).files)).toEqual([]);
  });

  test("plays with each stand-in in place", async () => {
    const projection = projectDraft(brokenDraft, { now });
    const r = new Runtime(await load(projection.files), true);
    // The script that does not compile does nothing, and the references that
    // went are left out of the text, which the board's own value shows in.
    const started = r.start(intro.id, "nd_00000000000000000000000006");
    expect(started.filter((e) => e.kind === "error")).toEqual([]);
    expect(r.text()).toBe("You have 3 and  gold, .");
    // The condition that does not compile never holds.
    expect(r.choices().map((c) => c.id)).toEqual([
      "cn_00000000000000000000000013",
    ]);
    // A jumper whose target went has none, which the runtime reports.
    expect(r.choose("cn_00000000000000000000000013")).toContainEqual({
      kind: "error",
      code: "jumper_broken",
      at: "nd_00000000000000000000000009",
      expression: "",
    });
    const file = projection.boards.get(intro.id) as BoardFile;
    expect(file.board.entryNodeId).toBeUndefined();
    expect(file.connections.map((c) => c.id)).toEqual([
      "cn_00000000000000000000000012",
      "cn_00000000000000000000000013",
    ]);
  });
});
