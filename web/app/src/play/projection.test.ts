import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import type { Attribute, Connection, Node } from "@storycreep/shared/api";
import { readJSON } from "@storycreep/shared/expr";
import {
  attributeKey,
  load,
  Runtime,
  type Schema,
  validate,
} from "@storycreep/shared/flow";
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
import { applyCommands } from "../document/apply";
import { Draft } from "../document/doc";
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
import { encoded, ProjectionError, projectDraft } from "./projection";

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
  test("project back to their own files, whose scenarios pass", async () => {
    expect(suites.length).toBeGreaterThan(0);
    for (const { name, files, scenarios } of suites) {
      const projected = await projectDraft(draftOf(files), { now });
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
    }
  });

  // What play's inspector lists is what the runtime holds, each of its
  // declaration's type (FR-PLAY-02), and each condition that the snapshot
  // holds has its compiled AST, of the source, which the author view reads
  // the failed parts of (FR-PLAY-03).
  test("list the values and the conditions that the runtime holds", async () => {
    for (const { name, files } of suites) {
      const projected = await projectDraft(draftOf(files), { now });
      const snapshot = await load(projected.files);
      expect(
        projected.values.map((v) => v.key),
        name,
      ).toEqual([
        ...snapshot.variables,
        ...snapshot.attributes.map(([c, a]) => attributeKey(c, a)),
      ]);
      for (const v of projected.values) {
        expect(v.type, `${name}: ${v.name}`).toBe(
          v.kind === "variable"
            ? snapshot.declared.get(v.variable)?.type
            : snapshot.declaredTypes.get(v.key),
        );
        if (v.type === "enum") {
          expect(v.options).toEqual(snapshot.options.get(v.key));
        }
      }
      const held = new Map<string, string>();
      for (const f of projected.boards.values()) {
        for (const c of f.connections) {
          if (c.condition !== undefined) {
            held.set(c.id, c.condition.source);
          }
        }
        for (const n of f.nodes) {
          if (n.type === "branch") {
            for (const o of n.outputs) {
              if (o.kind !== "else") {
                held.set(o.id, o.condition.source);
              }
            }
          }
        }
      }
      expect([...projected.conditions.keys()].sort(), name).toEqual(
        [...held.keys()].sort(),
      );
      for (const [entity, tree] of projected.conditions) {
        // The whole source, from its first token to its last.
        const source = Array.from(held.get(entity) ?? "");
        expect(
          source.slice(tree.src.start, tree.src.end).join("").trim(),
          `${name}: ${entity}`,
        ).toBe(source.join("").trim());
      }
    }
  });
});

test("the manifest says that the snapshot is of the draft, pulled now", async () => {
  const { files } = await projectDraft(draftOf(suites[0].files), { now });
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
  test("is projected with what it stands in for listed", async () => {
    const { standIns } = await projectDraft(brokenDraft, { now });
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
        reason: "board",
      },
      {
        kind: "reference",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        field: "text",
        ref: "cmp_00000000000000000000000007",
        reason: "gone",
      },
      expect.objectContaining({
        kind: "expression",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "nd_00000000000000000000000006",
        owner: "node",
        field: "onEnter",
        source: "gold +=",
      }),
      {
        kind: "target",
        board: intro.id,
        node: "nd_00000000000000000000000009",
        entity: "nd_00000000000000000000000009",
        ref: "nd_00000000000000000000000010",
        reason: "gone",
      },
      {
        kind: "target",
        board: intro.id,
        node: "nd_00000000000000000000000011",
        entity: "nd_00000000000000000000000011",
        ref: "nd_00000000000000000000000009",
        reason: "jumper",
      },
      {
        kind: "connection",
        board: intro.id,
        node: "nd_00000000000000000000000008",
        entity: "cn_00000000000000000000000014",
        reason: "port",
      },
      expect.objectContaining({
        kind: "expression",
        board: intro.id,
        node: "nd_00000000000000000000000006",
        entity: "cn_00000000000000000000000012",
        owner: "choice",
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
        reason: "gone",
      },
    ]);
  });

  // Every projected file, of a draft that the projection stands in for
  // anything of or not, validates against its published JSON Schema.
  test("validates against the published JSON Schemas", async () => {
    expect(
      invalidFiles((await projectDraft(brokenDraft, { now })).files),
    ).toEqual([]);
  });

  test("plays with each stand-in in place", async () => {
    const projection = await projectDraft(brokenDraft, { now });
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

// A draft whose project has threaded comments (plan item 74), on a node, a
// connection, the board, and a variable, with a reply, an edit, and a
// resolve, of a word that no other text of the draft has: the operations
// of the comments' commands, as the page's own batches and other sessions'
// bring them, apply to the project's document by its appliers.
describe("a draft whose project has comments", () => {
  // Verifies: FR-COMMENT-01/AC3
  test("is projected without a comment's text or ID in any file", async () => {
    const word = "Zanzibar";
    const ids = [
      "cmt_00000000000000000000000031",
      "cmt_00000000000000000000000032",
      "cmt_00000000000000000000000033",
      "cmt_00000000000000000000000034",
      "cmt_00000000000000000000000035",
    ];
    const body = (n: number) => richText(text(`${word} ${n}`));
    const draft = new Draft(brokenDraft, now.toISOString());
    applyCommands(draft, [
      {
        type: "comment.create",
        payload: {
          id: ids[0],
          target: "nd_00000000000000000000000006",
          body: body(0),
        },
      },
      {
        type: "comment.create",
        payload: {
          id: ids[1],
          target: "cn_00000000000000000000000012",
          body: body(1),
        },
      },
      {
        type: "comment.create",
        payload: { id: ids[2], target: intro.id, body: body(2) },
      },
      {
        type: "comment.create",
        payload: { id: ids[3], target: gold.id, body: body(3) },
      },
      {
        type: "comment.create",
        payload: { id: ids[4], parent_id: ids[0], body: body(4) },
      },
      { type: "comment.edit", payload: { id: ids[4], body: body(5) } },
      { type: "comment.resolve", payload: { id: ids[0] } },
      { type: "comment.delete", payload: { id: ids[3] } },
      { type: "comment.restore", payload: { id: ids[3] } },
    ]);
    const commented = draft.finish(brokenDraft.seq + 1);
    const { files } = await projectDraft(commented, { now });
    for (const [path, bytes] of files) {
      const written = decoder.decode(bytes);
      expect(written, path).not.toContain(word);
      expect(written.toLowerCase(), path).not.toContain("cmt_");
    }
    // The comments change nothing of the snapshot: its files are those of
    // the draft without them.
    expect(files).toEqual((await projectDraft(brokenDraft, { now })).files);
  });
});

// A board of a draft with a stand-in of each reason that a reference or a
// connection is left out for, and of an expression of a connection out of
// a branch.
const reasons = testBoard("bd_00000000000000000000000020", "Reasons");
const onReasons = { board_id: reasons.id };
const trashed = testVariable(
  "var_00000000000000000000000028",
  "trashed",
  "int",
  0,
  { deleted_at: "2026-11-02T10:00:00Z" },
);
const mood: Attribute = {
  id: "atr_00000000000000000000000030",
  component_type_id: "ct_00000000000000000000000029",
  key: "mood",
  name: "Mood",
  type: "string",
  default: "calm",
  runtime: false,
  order: "a",
  rev: 1,
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};
const reasonsDraft = testDraft({
  boards: [reasons],
  variables: [trashed],
  componentTypes: [
    {
      id: mood.component_type_id,
      project_id: testProjectId,
      key: "character",
      name: "Character",
      icon: "person",
      attributes: [mood],
      rev: 1,
      created_at: mood.created_at,
      updated_at: mood.updated_at,
    },
  ],
  components: [
    {
      id: "cmp_00000000000000000000000031",
      project_id: testProjectId,
      component_type_id: mood.component_type_id,
      key: "keeper",
      name: "Keeper",
      order: "a",
      attributes: { [mood.id]: "calm" },
      rev: 1,
      created_at: mood.created_at,
      updated_at: mood.updated_at,
    },
  ],
  nodes: [
    testNode("nd_00000000000000000000000021", 0, 0, {
      ...onReasons,
      text: richText(
        { type: "valueRef", attrs: { ref: trashed.id } },
        {
          type: "valueRef",
          attrs: { ref: "cmp_00000000000000000000000031", attribute: mood.id },
        },
      ),
    }),
    {
      ...testNode("nd_00000000000000000000000022", 200, 0, onReasons),
      type: "branch",
      outputs: [
        {
          id: "bo_00000000000000000000000027",
          node_id: "nd_00000000000000000000000022",
          kind: "if",
          order: "a",
          condition: expression("true"),
          rev: 1,
          created_at: mood.created_at,
          updated_at: mood.updated_at,
        },
      ],
    } as Node,
    testNode("nd_00000000000000000000000023", 400, 0, onReasons),
    testNode("nd_00000000000000000000000024", 600, 0, {
      ...onReasons,
      deleted_at: "2026-11-02T10:00:00Z",
    }),
  ],
  connections: [
    testConnection(
      "cn_00000000000000000000000025",
      "nd_00000000000000000000000021",
      "nd_00000000000000000000000024",
      onReasons,
    ),
    testConnection(
      "cn_00000000000000000000000026",
      "nd_00000000000000000000000022",
      "nd_00000000000000000000000023",
      {
        ...onReasons,
        from_port: "bo_00000000000000000000000027",
        condition: expression("gold >"),
        on_traverse: expression("gold +="),
      },
    ),
  ] as Connection[],
});

test("a stand-in says why a reference or a connection is left out, and whose an expression is", async () => {
  const { standIns } = await projectDraft(reasonsDraft, { now });
  const at = { board: reasons.id, node: "nd_00000000000000000000000021" };
  expect(standIns).toEqual([
    {
      kind: "reference",
      ...at,
      entity: at.node,
      field: "text",
      ref: trashed.id,
      reason: "gone",
    },
    {
      kind: "reference",
      ...at,
      entity: at.node,
      field: "text",
      ref: "cmp_00000000000000000000000031",
      reason: "runtime",
    },
    {
      kind: "connection",
      ...at,
      entity: "cn_00000000000000000000000025",
      reason: "target",
    },
    expect.objectContaining({
      kind: "expression",
      node: "nd_00000000000000000000000022",
      entity: "cn_00000000000000000000000026",
      owner: "connection",
      field: "condition",
    }),
    expect.objectContaining({
      kind: "expression",
      node: "nd_00000000000000000000000022",
      entity: "cn_00000000000000000000000026",
      owner: "connection",
      field: "onTraverse",
    }),
  ]);
});

// A jumper's target and the project's entry that name a node of another
// board than they name are stood in for as such, rather than as gone.
test("a target or an entry of a node on another board than it names says so", async () => {
  const jumper = {
    ...testNode("nd_00000000000000000000000040", 0, 0, onReasons),
    type: "jumper",
    target: { board_id: other.id, node_id: "nd_00000000000000000000000023" },
  } as Node;
  const draft = {
    ...reasonsDraft,
    boards: new Map(reasonsDraft.boards).set(other.id, other),
    nodes: new Map(reasonsDraft.nodes).set(jumper.id, jumper),
    project: {
      ...(reasonsDraft.project as NonNullable<typeof reasonsDraft.project>),
      entry: { board_id: other.id, node_id: "nd_00000000000000000000000023" },
    },
  };
  const { standIns } = await projectDraft(draft, { now });
  expect(standIns).toContainEqual({
    kind: "target",
    board: reasons.id,
    node: jumper.id,
    entity: jumper.id,
    ref: "nd_00000000000000000000000023",
    reason: "board",
  });
  expect(standIns).toContainEqual({
    kind: "entry",
    board: other.id,
    entity: testProjectId,
    ref: "nd_00000000000000000000000023",
    reason: "board",
  });
});

// What the projection cannot write at all: a component whose values the
// document does not hold, which its type's defaults would stand in for
// unsaid, and two boards of one slug, whose files would be one.
describe("a document that the projection cannot write", () => {
  test("is refused where it does not hold a component whole", async () => {
    const keeper = reasonsDraft.components.get(
      "cmp_00000000000000000000000031",
    );
    const partial = { ...keeper } as NonNullable<typeof keeper>;
    delete partial.attributes;
    const refused = projectDraft(
      { ...reasonsDraft, components: new Map([[partial.id, partial]]) },
      { now },
    );
    await expect(refused).rejects.toBeInstanceOf(ProjectionError);
    await expect(refused).rejects.toMatchObject({ reason: "unread" });
  });

  test("is refused where two boards have one slug", async () => {
    const twin = { ...other, slug: reasons.slug };
    await expect(
      projectDraft(
        {
          ...reasonsDraft,
          boards: new Map(reasonsDraft.boards).set(twin.id, twin),
        },
        { now },
      ),
    ).rejects.toMatchObject({ reason: "inconsistent" });
  });
});
