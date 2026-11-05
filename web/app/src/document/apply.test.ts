import { readFileSync } from "node:fs";
import { join } from "node:path";
import type {
  Attribute,
  Board,
  BranchOutput,
  ComponentType,
  Connection,
  Folder,
  Node,
  RichTextDoc,
  Variable,
} from "@storycreep/shared/api";
import { encodeId, type IdKind } from "@storycreep/shared/ids";
import { describe, expect, it } from "vitest";
import {
  type AnyCommand,
  appliers,
  applyCommands,
  type CommandType,
  type PayloadOf,
} from "./apply";
import {
  boardPart,
  type ComponentEntry,
  type Doc,
  Draft,
  emptyDoc,
  type Part,
  type Project,
} from "./doc";
import { applyOperation, mergeEntity, mergeList } from "./reconcile";

const earlier = "2026-01-01T00:00:00.000Z";
const time = "2026-11-05T10:00:00.000Z";

/** Returns the ID of kind whose UUID ends in n, in its canonical spelling. */
function id(kind: IdKind, n: number): string {
  const uuid = new Uint8Array(16);
  uuid[6] = 0x70;
  uuid[8] = 0x80;
  uuid[15] = n;
  return encodeId(kind, uuid);
}

const projectId = id("project", 1);
const boardA = id("board", 1);
const boardB = id("board", 2);
const stamps = { rev: 3, created_at: earlier, updated_at: earlier };

const project: Project = {
  id: projectId,
  workspace_id: id("workspace", 1),
  name: "Harbour",
  slug: "harbour",
  description: "",
  default_locale: "en",
  publish_min_role: "editor",
  entry: {},
  ...stamps,
};

function paragraph(...texts: string[]): RichTextDoc {
  return {
    type: "doc",
    content: [
      {
        type: "paragraph",
        content: texts.map((text) => ({ type: "text", text })),
      },
    ],
  };
}

const empty: RichTextDoc = { content: [{ type: "paragraph" }], type: "doc" };

function board(n: number, fields: Partial<Board> = {}): Board {
  return {
    id: id("board", n),
    project_id: projectId,
    order: "V",
    name: `Board ${n}`,
    slug: `board-${n}`,
    description: "",
    color: "none",
    ...stamps,
    ...fields,
  };
}

function folder(
  n: number,
  kind: Folder["kind"],
  fields: Partial<Folder> = {},
): Folder {
  return {
    id: id("folder", n),
    project_id: projectId,
    kind,
    name: `Folder ${n}`,
    order: "V",
    ...stamps,
    ...fields,
  };
}

function node(n: number, fields: Partial<Node> = {}): Node {
  return {
    id: id("node", n),
    project_id: projectId,
    board_id: boardA,
    type: "element",
    kind: "dialogue",
    title: "",
    text: empty,
    color: "none",
    tags: [],
    position: { x: 0, y: 0 },
    is_ending: false,
    ...stamps,
    ...fields,
  };
}

function output(
  n: number,
  nodeN: number,
  fields: Partial<BranchOutput> = {},
): BranchOutput {
  return {
    id: id("branch_output", n),
    node_id: id("node", nodeN),
    kind: "elseif",
    order: "V",
    condition: { source: "true", is_valid: true, ast_version: 1, ast: true },
    ...stamps,
    ...fields,
  };
}

function branch(n: number, outputs: BranchOutput[]): Node {
  const { kind: _, text: __, ...rest } = node(n, { type: "branch" });
  return { ...rest, outputs };
}

function connection(
  n: number,
  from: number,
  to: number,
  fields: Partial<Connection> = {},
): Connection {
  return {
    id: id("connection", n),
    project_id: projectId,
    board_id: boardA,
    from_node_id: id("node", from),
    from_port: "out",
    to_node_id: id("node", to),
    order: "V",
    label: empty,
    ...stamps,
    ...fields,
  };
}

function variable(n: number, fields: Partial<Variable> = {}): Variable {
  return {
    id: id("variable", n),
    project_id: projectId,
    scope: "global",
    order: "V",
    key: `v${n}`,
    type: "int",
    default: 0,
    description: "",
    reset_on_enter: false,
    ...stamps,
    ...fields,
  };
}

function attribute(
  n: number,
  typeN: number,
  fields: Partial<Attribute> = {},
): Attribute {
  return {
    id: id("attribute_def", n),
    component_type_id: id("component_type", typeN),
    order: "V",
    key: `a${n}`,
    name: `A${n}`,
    type: "int",
    default: 0,
    runtime: false,
    ...stamps,
    ...fields,
  };
}

function componentType(
  n: number,
  attributes: Attribute[],
  fields: Partial<ComponentType> = {},
): ComponentType {
  return {
    id: id("component_type", n),
    project_id: projectId,
    key: `t${n}`,
    name: `T${n}`,
    icon: "none",
    attributes,
    ...stamps,
    ...fields,
  };
}

/** Returns a component as the list answers it, without its description and its values. */
function summary(
  n: number,
  typeN: number,
  fields: Partial<ComponentEntry> = {},
): ComponentEntry {
  return {
    id: id("component", n),
    project_id: projectId,
    component_type_id: id("component_type", typeN),
    order: "V",
    key: `c${n}`,
    name: `C${n}`,
    ...stamps,
    ...fields,
  };
}

/** Returns a component as its own read answers it, with its description and its values. */
function component(
  n: number,
  typeN: number,
  attributes: Record<string, boolean | number | string>,
  fields: Partial<ComponentEntry> = {},
): ComponentEntry {
  return summary(n, typeN, { description: empty, attributes, ...fields });
}

interface Held {
  parts?: Part[];
  project?: Project;
  folders?: Folder[];
  boards?: Board[];
  nodes?: Node[];
  connections?: Connection[];
  variables?: Variable[];
  componentTypes?: ComponentType[];
  components?: ComponentEntry[];
}

function mapOf<T extends { id: string }>(items: T[] = []): Map<string, T> {
  return new Map(items.map((item) => [item.id, item]));
}

/** Returns a document as of seq 7 that holds what held gives, and by default every part but board B's. */
function docOf(held: Held = {}): Doc {
  return {
    ...emptyDoc(7),
    project: held.project ?? project,
    parts: new Set(
      held.parts ?? ["tree", "variables", "components", boardPart(boardA)],
    ),
    folders: mapOf(held.folders),
    boards: mapOf(held.boards),
    nodes: mapOf(held.nodes),
    connections: mapOf(held.connections),
    variables: mapOf(held.variables),
    componentTypes: mapOf(held.componentTypes),
    components: mapOf(held.components),
  };
}

function command<T extends CommandType>(
  type: T,
  payload: PayloadOf<T>,
): AnyCommand {
  return { type, payload };
}

/**
 * Applies commands to doc, as of the next seq, once each applier has been
 * run on a draft of its own outside applyCommands, which would hide what it
 * throws as changes that are not sure.
 */
function apply(doc: Doc, ...commands: AnyCommand[]) {
  const probe = new Draft(doc, time);
  for (const c of commands.filter((c) => Object.hasOwn(appliers, c.type))) {
    (appliers[c.type as CommandType] as (d: Draft, p: unknown) => void)(
      probe,
      c.payload,
    );
  }
  const draft = new Draft(doc, time);
  applyCommands(draft, commands);
  return {
    doc: draft.finish(doc.seq + 1),
    draft,
    /** Returns whether the batch changed the entity of id surely, or undefined if it did not change it. */
    sure: (entityId: string) => draft.changes.get(entityId)?.sure,
  };
}

/** Reports whether key, a key of order, comes after than. */
function isAfter(key: string | undefined, than: string): boolean {
  return key !== undefined && key > than;
}

/** Returns the JSON of the file at path in the repository's schema directory. */
function schemaFile(path: string): unknown {
  return JSON.parse(
    readFileSync(join(import.meta.dirname, "../../../../schema", path), "utf8"),
  );
}

/** Returns value with every object's fields in alphabetical order, as the server writes JSON. */
function sorted(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sorted);
  }
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([k, v]) => [k, sorted(v)]),
    );
  }
  return value;
}

describe("the appliers' registry", () => {
  // Verifies: FR-OFF-03/AC2
  it("has an applier for every type of command that the API's union of commands holds", () => {
    const { schemas } = (
      schemaFile("api/v1/openapi.json") as {
        components: {
          schemas: Record<
            string,
            {
              oneOf?: { $ref: string }[];
              properties?: { type?: { enum?: string[] } };
            }
          >;
        };
      }
    ).components;
    const types = (schemas.CommandBody.oneOf ?? []).flatMap(
      ({ $ref }) =>
        schemas[$ref.slice("#/components/schemas/".length)].properties?.type
          ?.enum ?? [],
    );
    expect(types.length).toBeGreaterThan(70);
    expect(Object.keys(appliers).sort()).toEqual([...new Set(types)].sort());
  });

  it("leaves a command of a type that it does not know out, and changes nothing", () => {
    const result = apply(docOf(), { type: "node.levitate", payload: {} });
    expect(result.draft.changes.size).toBe(0);
  });
});

describe("the board appliers", () => {
  it("renames a board surely", () => {
    const result = apply(
      docOf({ boards: [board(1)] }),
      command("board.rename", { id: boardA.toLowerCase(), name: "Docks" }),
    );
    expect(result.doc.boards.get(boardA)).toMatchObject({
      name: "Docks",
      slug: "board-1",
      updated_at: time,
    });
    expect(result.sure(boardA)).toBe(true);
  });

  it("moves a board into a folder at the key that the payload gives, surely", () => {
    const f = folder(1, "board");
    const result = apply(
      docOf({ folders: [f], boards: [board(1)] }),
      command("board.move", { id: boardA, folder_id: f.id, order: "k" }),
    );
    expect(result.doc.boards.get(boardA)).toMatchObject({
      folder_id: f.id,
      order: "k",
    });
    expect(result.sure(boardA)).toBe(true);
  });

  it("moves a board without a key after the folder's last child, not surely", () => {
    const f = folder(1, "board");
    const result = apply(
      docOf({
        folders: [f],
        boards: [board(1), board(2, { folder_id: f.id, order: "a" })],
      }),
      command("board.move", { id: boardA, folder_id: f.id }),
    );
    expect(isAfter(result.doc.boards.get(boardA)?.order, "a")).toBe(true);
    expect(result.sure(boardA)).toBe(false);
  });

  it("creates a board with the slug and the key that the payload gives, surely", () => {
    const result = apply(
      docOf(),
      command("board.create", {
        id: boardB,
        name: "Docks",
        slug: "the-docks",
        order: "k",
      }),
    );
    expect(result.doc.boards.get(boardB)).toEqual({
      id: boardB,
      project_id: projectId,
      name: "Docks",
      slug: "the-docks",
      description: "",
      color: "none",
      order: "k",
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(boardB)).toBe(true);
  });

  it("creates a board without a slug or a key with ones that the server may pick otherwise, not surely", () => {
    const result = apply(
      docOf({ boards: [board(1, { order: "a" })] }),
      command("board.create", { id: boardB, name: "Les Quais" }),
    );
    expect(result.doc.boards.get(boardB)?.slug).toBe("les-quais");
    expect(isAfter(result.doc.boards.get(boardB)?.order, "a")).toBe(true);
    expect(result.sure(boardB)).toBe(false);
  });

  it("takes a deleted board out, with its part, its nodes, and its connections", () => {
    const result = apply(
      docOf({
        boards: [board(1)],
        nodes: [node(1), node(2)],
        connections: [connection(1, 1, 2)],
      }),
      command("board.delete", { id: boardA }),
    );
    expect(result.doc.boards.has(boardA)).toBe(false);
    expect(result.doc.parts.has(boardPart(boardA))).toBe(false);
    expect(result.doc.nodes.size).toBe(0);
    expect(result.doc.connections.size).toBe(0);
    expect(result.draft.dropped).toEqual(new Set([boardPart(boardA)]));
    expect(result.sure(boardA)).toBe(true);
  });

  it("takes a deleted board's variables out with it, without a change of theirs, and reads the variables again once it is restored", () => {
    const own = variable(1, { scope: "board", board_id: boardA });
    const other = variable(2, { scope: "board", board_id: boardB });
    const global = variable(3);
    const deleted = apply(
      docOf({ boards: [board(1)], variables: [own, other, global] }),
      command("board.delete", { id: boardA }),
    );
    expect([...deleted.doc.variables.keys()]).toEqual([other.id, global.id]);
    expect(deleted.sure(own.id)).toBeUndefined();
    const restored = apply(
      deleted.doc,
      command("board.restore", { id: boardA }),
    );
    expect(restored.draft.rereads).toEqual(new Set(["variables"]));
  });

  it("archives the project and unarchives it, surely", () => {
    const archived = apply(docOf(), command("project.archive", {}));
    expect(archived.doc.project?.archived_at).toBe(time);
    expect(archived.sure(projectId)).toBe(true);
    const unarchived = apply(archived.doc, command("project.unarchive", {}));
    expect(unarchived.doc.project).not.toHaveProperty("archived_at");
    expect(unarchived.sure(projectId)).toBe(true);
  });

  it("updates the project's settings that the payload gives, and keeps the others", () => {
    const result = apply(
      docOf(),
      command("project.update_settings", { publish_min_role: "admin" }),
    );
    expect(result.doc.project).toMatchObject({
      publish_min_role: "admin",
      default_locale: "en",
    });
    expect(result.sure(projectId)).toBe(true);
  });
});

describe("an operation held to its revisions (reconcile.ts)", () => {
  // A branch node with three conditional outputs, the last of which a batch
  // moves between the others, at the key that it gives, surely.
  const outputs = [
    output(1, 1, { kind: "if", order: "V" }),
    output(2, 1, { kind: "elseif", order: "W" }),
    output(3, 1, { kind: "elseif", order: "X" }),
  ];
  const reorder = command("branch_output.reorder", {
    id: id("branch_output", 3),
    order: "Vm",
  });

  it("gives a branch output that a batch changed before its node the output's revision, whatever the order of the commands", () => {
    const op = {
      seq: 8,
      commands: [
        reorder,
        command("node.update", { id: id("node", 1), title: "Fork" }),
      ],
      revisions: [
        { id: id("branch_output", 3), rev: 4 },
        { id: id("node", 1), rev: 4 },
      ],
      time,
    };
    const { doc } = applyOperation(docOf({ nodes: [branch(1, outputs)] }), op);
    const node = doc.nodes.get(id("node", 1));
    expect(node).toMatchObject({ title: "Fork", rev: 4 });
    expect(
      node?.outputs?.find((o) => o.id === id("branch_output", 3)),
    ).toMatchObject({ order: "Vm", rev: 4 });
  });

  it("keeps the change of a branch output whose node the batch set back, as one that the server did not change", () => {
    const op = {
      seq: 8,
      commands: [
        reorder,
        // The title that the node has already, which changes nothing.
        command("node.update", { id: id("node", 1), title: "" }),
      ],
      revisions: [{ id: id("branch_output", 3), rev: 4 }],
      time,
    };
    const { doc } = applyOperation(docOf({ nodes: [branch(1, outputs)] }), op);
    const node = doc.nodes.get(id("node", 1));
    expect(node).toMatchObject({ rev: 3, updated_at: earlier });
    expect(
      node?.outputs?.find((o) => o.id === id("branch_output", 3)),
    ).toMatchObject({ order: "Vm", rev: 4 });
  });
});

describe("an entity merged as a read of it answered (reconcile.ts)", () => {
  const nodeId = id("node", 1);

  it("keeps what the document holds of a node's branch outputs that is later than the read's, or that went since the read began, once the read's node is later", () => {
    // The node's second output went by the operation of seq 9, after the
    // read began, as of 7, which found it, and the first as it was before
    // its change to 4.
    const doc: Doc = {
      ...docOf({
        nodes: [
          {
            ...branch(1, [output(1, 1, { kind: "if", order: "V", rev: 4 })]),
            rev: 4,
          },
        ],
      }),
      gone: new Map([[id("branch_output", 2), { rev: 4, seq: 9 }]]),
    };
    const read = {
      ...branch(1, [
        output(1, 1, { kind: "if", order: "V" }),
        output(2, 1, { order: "W" }),
      ]),
      title: "Fork",
      rev: 5,
    };
    const node = mergeEntity(doc, nodeId, read, 7).nodes.get(nodeId);
    expect(node).toMatchObject({ title: "Fork", rev: 5 });
    expect(node?.outputs).toEqual([
      output(1, 1, { kind: "if", order: "V", rev: 4 }),
    ]);
  });

  it("leaves out of a list read before an operation took a node to the trash the connections that went with it, without revisions of their own, until a list read after", () => {
    const doc = docOf({
      nodes: [node(1), node(2)],
      connections: [connection(1, 1, 2)],
    });
    const { doc: after } = applyOperation(doc, {
      seq: 8,
      commands: [command("node.delete", { id: id("node", 1) })],
      revisions: [{ id: id("node", 1), rev: 4 }],
      time,
    });
    expect(after.connections.has(id("connection", 1))).toBe(false);
    const part = boardPart(boardA);
    const listed = [connection(1, 1, 2)];
    // The list of the board's connections, read as of 7, before the node
    // went, still has its connection.
    const stale = mergeList(after, "connections", part, listed, 7, new Set());
    expect(stale.connections.has(id("connection", 1))).toBe(false);
    // A list read as of 9, once the node came back, has it again.
    const back = mergeList(stale, "connections", part, listed, 9, new Set());
    expect(back.connections.get(id("connection", 1))).toEqual(listed[0]);
    expect(back.gone.has(id("connection", 1))).toBe(false);
  });

  it("takes the read's fields of an entity at the same revision, keeping what the read leaves out, as a component's values that its list does not answer, and the document as it was where nothing differs", () => {
    const full = component(1, 1, { [id("attribute_def", 1)]: 5 });
    const doc = docOf({ components: [{ ...full, name: "Guess" }] });
    const merged = mergeEntity(doc, full.id, summary(1, 1), 7);
    expect(merged.components.get(full.id)).toEqual(full);
    expect(mergeEntity(merged, full.id, summary(1, 1), 7)).toBe(merged);
  });

  it("keeps a branch output that an operation put in while the read of its node was under way, which the read does not have", () => {
    const added = output(3, 1, { order: "X", rev: 1 });
    const doc = docOf({
      nodes: [{ ...branch(1, [output(1, 1, { kind: "if" }), added]), rev: 4 }],
    });
    const read = { ...branch(1, [output(1, 1, { kind: "if" })]), rev: 5 };
    expect(
      mergeEntity(doc, nodeId, read, 7, new Set([added.id])).nodes.get(nodeId)
        ?.outputs,
    ).toEqual([output(1, 1, { kind: "if" }), added]);
    // One that no operation changed since went before the read began.
    expect(
      mergeEntity(doc, nodeId, read, 7).nodes.get(nodeId)?.outputs,
    ).toEqual([output(1, 1, { kind: "if" })]);
  });
});

describe("a part taken out of a document (Draft.drop)", () => {
  it("takes out the tree with its folders of boards and its boards, and leaves the other parts' entities", () => {
    const doc = docOf({
      folders: [folder(1, "board"), folder(2, "variable")],
      boards: [board(1)],
      variables: [variable(1)],
    });
    const draft = new Draft(doc, time);
    draft.drop("tree");
    const after = draft.finish(doc.seq);
    expect(after.parts.has("tree")).toBe(false);
    expect([...after.folders.keys()]).toEqual([id("folder", 2)]);
    expect(after.boards.size).toBe(0);
    expect(after.variables.size).toBe(1);
  });
});

describe("the node appliers", () => {
  it("creates an element with the server's defaults, at its position rounded half away from zero, surely", () => {
    const result = apply(
      docOf(),
      command("node.create", {
        id: id("node", 9).toLowerCase(),
        board_id: boardA.toLowerCase(),
        type: "element",
        position: { x: 0.125, y: -0.125 },
        size: { width: 240.004, height: 120.5 },
        tags: null,
      }),
    );
    expect(result.doc.nodes.get(id("node", 9))).toEqual({
      id: id("node", 9),
      project_id: projectId,
      board_id: boardA,
      type: "element",
      kind: "dialogue",
      title: "",
      text: empty,
      color: "none",
      tags: [],
      position: { x: 0.13, y: -0.13 },
      size: { width: 240, height: 120.5 },
      is_ending: false,
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(id("node", 9))).toBe(true);
  });

  it("creates a node with a script not surely, its source not compiled", () => {
    const result = apply(
      docOf(),
      command("node.create", {
        id: id("node", 9),
        board_id: boardA,
        type: "branch",
        position: { x: 1, y: 2 },
        on_enter: "met = true",
        on_exit: "",
      }),
    );
    const created = result.doc.nodes.get(id("node", 9));
    expect(created?.on_enter).toEqual({
      source: "met = true",
      is_valid: false,
    });
    expect(created).not.toHaveProperty("on_exit");
    expect(created).not.toHaveProperty("kind");
    expect(created).not.toHaveProperty("outputs");
    expect(result.sure(id("node", 9))).toBe(false);
  });

  it("leaves out a node created on a board whose content the document does not hold", () => {
    const result = apply(
      docOf(),
      command("node.create", {
        id: id("node", 9),
        board_id: boardB,
        type: "jumper",
        position: { x: 0, y: 0 },
      }),
    );
    expect(result.doc.nodes.size).toBe(0);
    expect(result.draft.changes.size).toBe(0);
  });

  it("updates the fields that the payload gives, takes out those that it empties, and keeps the others, surely", () => {
    const result = apply(
      docOf({
        nodes: [
          node(1, {
            title: "Old",
            speaker_component_id: id("component", 1),
            size: { width: 10, height: 10 },
            color: "red",
          }),
        ],
      }),
      command("node.update", {
        id: id("node", 1),
        title: "New",
        tags: ["Night", "dock"],
        speaker_component_id: "",
        size: {},
      }),
    );
    const updated = result.doc.nodes.get(id("node", 1));
    expect(updated).toMatchObject({
      title: "New",
      tags: ["Night", "dock"],
      color: "red",
      updated_at: time,
    });
    expect(updated).not.toHaveProperty("speaker_component_id");
    expect(updated).not.toHaveProperty("size");
    expect(result.sure(id("node", 1))).toBe(true);
  });

  it("sets and clears a jumper's target, its IDs canonical", () => {
    const set = apply(
      docOf({ nodes: [node(1, { type: "jumper" })] }),
      command("node.update", {
        id: id("node", 1),
        target: {
          board_id: boardB.toLowerCase(),
          node_id: id("node", 5).toLowerCase(),
        },
      }),
    );
    expect(set.doc.nodes.get(id("node", 1))?.target).toEqual({
      board_id: boardB,
      node_id: id("node", 5),
    });
    const cleared = apply(
      set.doc,
      command("node.update", { id: id("node", 1), target: {} }),
    );
    expect(cleared.doc.nodes.get(id("node", 1))).not.toHaveProperty("target");
  });

  it("stores a text in the canonical form surely: marks in order, neighbouring texts joined, IDs canonical", () => {
    const result = apply(
      docOf({ nodes: [node(1)] }),
      command("node.update", {
        id: id("node", 1),
        text: {
          type: "doc",
          content: [
            {
              type: "paragraph",
              content: [
                { type: "text", text: "Go ", marks: [] },
                {
                  type: "text",
                  text: "now",
                  marks: [{ type: "italic" }, { type: "bold" }],
                },
                {
                  type: "text",
                  text: "!",
                  marks: [{ type: "bold" }, { type: "italic" }],
                },
                {
                  type: "mention",
                  attrs: { id: id("component", 1).toLowerCase() },
                },
              ],
            },
            {
              type: "orderedList",
              attrs: { start: 1 },
              content: [
                {
                  type: "listItem",
                  content: [{ type: "paragraph", content: [] }],
                },
              ],
            },
          ],
        },
      }),
    );
    expect(JSON.stringify(result.doc.nodes.get(id("node", 1))?.text)).toBe(
      JSON.stringify({
        content: [
          {
            content: [
              { text: "Go ", type: "text" },
              {
                marks: [{ type: "bold" }, { type: "italic" }],
                text: "now!",
                type: "text",
              },
              { attrs: { id: id("component", 1) }, type: "mention" },
            ],
            type: "paragraph",
          },
          {
            content: [{ content: [{ type: "paragraph" }], type: "listItem" }],
            type: "orderedList",
          },
        ],
        type: "doc",
      }),
    );
    expect(result.sure(id("node", 1))).toBe(true);
  });

  it("stores each valid document of the rich-text vectors in the canonical form that they give", () => {
    const vectors = schemaFile("rich-text/vectors.json") as {
      valid: { note: string; doc: RichTextDoc; canonical?: unknown }[];
    };
    for (const vector of vectors.valid) {
      const result = apply(
        docOf({ nodes: [node(1)] }),
        command("node.update", { id: id("node", 1), text: vector.doc }),
      );
      expect(
        JSON.stringify(result.doc.nodes.get(id("node", 1))?.text),
        vector.note,
      ).toBe(JSON.stringify(sorted(vector.canonical ?? vector.doc)));
      expect(result.sure(id("node", 1)), vector.note).toBe(true);
    }
  });

  it("stores a text that holds a node of rich text that it does not know as it is, not surely", () => {
    const text = {
      type: "doc",
      content: [{ type: "heading", content: [] }],
    } as unknown as RichTextDoc;
    const result = apply(
      docOf({ nodes: [node(1)] }),
      command("node.update", { id: id("node", 1), text }),
    );
    expect(result.doc.nodes.get(id("node", 1))?.text).toBe(text);
    expect(result.sure(id("node", 1))).toBe(false);
  });

  it("writes a script's new source not surely, keeps one whose source is its own surely, and removes one for the empty source surely", () => {
    const compiled = {
      source: "met",
      is_valid: true,
      ast_version: 1,
      ast: { var: "x" },
    };
    const held = docOf({
      nodes: [
        node(1, { on_enter: compiled }),
        node(2, { on_enter: compiled, on_exit: compiled }),
      ],
    });
    const written = apply(
      held,
      command("node.update", { id: id("node", 1), on_enter: "met and seen" }),
    );
    expect(written.doc.nodes.get(id("node", 1))?.on_enter).toEqual({
      source: "met and seen",
      is_valid: false,
    });
    expect(written.sure(id("node", 1))).toBe(false);
    const kept = apply(
      held,
      command("node.update", {
        id: id("node", 2),
        on_enter: "met",
        on_exit: "",
      }),
    );
    expect(kept.doc.nodes.get(id("node", 2))?.on_enter).toBe(compiled);
    expect(kept.doc.nodes.get(id("node", 2))).not.toHaveProperty("on_exit");
    expect(kept.sure(id("node", 2))).toBe(true);
  });

  it("moves the nodes whose positions change, rounded, surely, and records no change of the others", () => {
    const result = apply(
      docOf({ nodes: [node(1), node(2, { position: { x: 5, y: 5 } })] }),
      command("node.move", {
        nodes: [
          { id: id("node", 1), position: { x: 10.555, y: -3.004 } },
          { id: id("node", 2), position: { x: 5, y: 5.001 } },
        ],
      }),
    );
    expect(result.doc.nodes.get(id("node", 1))?.position).toEqual({
      x: 10.56,
      y: -3,
    });
    expect(result.sure(id("node", 1))).toBe(true);
    expect(result.sure(id("node", 2))).toBeUndefined();
  });

  it("duplicates a node without its outputs and its connections, with the source's scripts, surely", () => {
    const compiled = {
      source: "true",
      is_valid: true,
      ast_version: 1,
      ast: true,
    };
    const result = apply(
      docOf({
        nodes: [
          {
            ...branch(1, [output(1, 1, { kind: "if" })]),
            title: "Pick",
            on_enter: compiled,
          },
          node(2),
        ],
        connections: [
          connection(1, 1, 2, { from_port: id("branch_output", 1) }),
        ],
      }),
      command("node.duplicate", {
        id: id("node", 9),
        source_id: id("node", 1).toLowerCase(),
        position: { x: 3.333, y: 4 },
      }),
    );
    const copy = result.doc.nodes.get(id("node", 9));
    expect(copy).toMatchObject({
      type: "branch",
      title: "Pick",
      on_enter: compiled,
      position: { x: 3.33, y: 4 },
      rev: 1,
      created_at: time,
    });
    expect(copy).not.toHaveProperty("outputs");
    expect(result.doc.connections.size).toBe(1);
    expect(result.sure(id("node", 9))).toBe(true);
  });

  it("takes a deleted node out surely, with the connections that leave it or enter it, which have no revision of their own to record", () => {
    const result = apply(
      docOf({
        nodes: [node(1), node(2), node(3)],
        connections: [
          connection(1, 1, 2),
          connection(2, 3, 1),
          connection(3, 2, 3),
        ],
      }),
      command("node.delete", { id: id("node", 1).toLowerCase() }),
    );
    expect([...result.doc.nodes.keys()]).toEqual([
      id("node", 2),
      id("node", 3),
    ]);
    expect([...result.doc.connections.keys()]).toEqual([id("connection", 3)]);
    expect(result.sure(id("node", 1))).toBe(true);
    // Recorded, they would be set back, as changes that the operation's
    // revisions do not name.
    expect(result.sure(id("connection", 1))).toBeUndefined();
    expect(result.sure(id("connection", 2))).toBeUndefined();
  });

  it("takes many deleted nodes out at once", () => {
    const result = apply(
      docOf({ nodes: [node(1), node(2), node(3)] }),
      command("node.delete_many", { ids: [id("node", 1), id("node", 3)] }),
    );
    expect([...result.doc.nodes.keys()]).toEqual([id("node", 2)]);
  });

  it("has every board that the document holds read again for a node that comes back from the trash", () => {
    const doc = docOf({
      parts: ["tree", boardPart(boardA), boardPart(boardB)],
    });
    const result = apply(doc, command("node.restore", { id: id("node", 1) }));
    expect(result.draft.rereads).toEqual(
      new Set([boardPart(boardA), boardPart(boardB)]),
    );
    expect(result.draft.changes.size).toBe(0);
    const many = apply(
      doc,
      command("node.restore_many", { ids: [id("node", 1), id("node", 2)] }),
    );
    expect(many.draft.rereads.size).toBe(2);
  });

  it("does nothing to restore a node that is not in the trash", () => {
    const result = apply(
      docOf({ nodes: [node(1)] }),
      command("node.restore", { id: id("node", 1) }),
    );
    expect(result.draft.rereads.size).toBe(0);
    expect(result.draft.changes.size).toBe(0);
  });
});

describe("the derivation that compiles a batch's expressions again", () => {
  const scripted = node(1, {
    on_enter: { source: "v1 = 1", is_valid: true, ast_version: 1, ast: {} },
  });

  it("makes a change of a node with a script not sure in a batch that creates a variable after it", () => {
    const result = apply(
      docOf({ nodes: [scripted, node(2)] }),
      command("node.update", { id: id("node", 1), title: "A" }),
      command("node.update", { id: id("node", 2), title: "B" }),
      command("variable.create", {
        id: id("variable", 1),
        key: "v1",
        type: "int",
        scope: "global",
        order: "V",
      }),
    );
    expect(result.sure(id("node", 1))).toBe(false);
    expect(result.sure(id("node", 2))).toBe(true);
    expect(result.sure(id("variable", 1))).toBe(true);
  });

  it("makes a change of a node with a script not sure in a batch that renamed a variable before it", () => {
    const result = apply(
      docOf({ nodes: [scripted], variables: [variable(1)] }),
      command("variable.rename", { id: id("variable", 1), key: "v2" }),
      command("node.move", {
        nodes: [{ id: id("node", 1), position: { x: 1, y: 1 } }],
      }),
    );
    expect(result.sure(id("node", 1))).toBe(false);
    expect(result.sure(id("variable", 1))).toBe(true);
  });

  it("makes a change of a connection whose expression names a node that the batch deletes not sure, by the node's ID in either case", () => {
    const names = connection(1, 2, 3, {
      condition: {
        source: `visited(node("${id("node", 1).toLowerCase()}"))`,
        is_valid: true,
        ast_version: 1,
        ast: {},
      },
    });
    const other = connection(2, 2, 3, {
      condition: { source: "true", is_valid: true, ast_version: 1, ast: true },
    });
    const result = apply(
      docOf({
        nodes: [node(1), node(2), node(3)],
        connections: [names, other],
      }),
      command("connection.update", { id: names.id, label: paragraph("a") }),
      command("connection.update", { id: other.id, label: paragraph("b") }),
      command("node.delete", { id: id("node", 1) }),
    );
    expect(result.sure(names.id)).toBe(false);
    expect(result.sure(other.id)).toBe(true);
  });
});

describe("the branch output appliers", () => {
  const branchOf = (...outputs: BranchOutput[]) =>
    docOf({ nodes: [branch(1, outputs), node(2)] });

  it("creates a conditional output before the first, which becomes the if, and records the old if's change of kind by the outputs' own IDs", () => {
    const result = apply(
      branchOf(
        output(1, 1, { kind: "if", order: "V" }),
        output(2, 1, { kind: "else", order: undefined, condition: undefined }),
      ),
      command("branch_output.create", {
        id: id("branch_output", 9).toLowerCase(),
        node_id: id("node", 1),
        order: "F",
        condition: "met",
      }),
    );
    const outputs = result.doc.nodes.get(id("node", 1))?.outputs ?? [];
    expect(outputs.map((o) => [o.id, o.kind])).toEqual([
      [id("branch_output", 9), "if"],
      [id("branch_output", 1), "elseif"],
      [id("branch_output", 2), "else"],
    ]);
    expect(outputs[0]).toMatchObject({
      order: "F",
      condition: { source: "met", is_valid: false },
      rev: 1,
    });
    expect(result.sure(id("branch_output", 9))).toBe(false);
    expect(result.sure(id("branch_output", 1))).toBe(true);
    expect(result.sure(id("node", 1))).toBeUndefined();
  });

  it("creates a conditional output without a key after the last as an elseif, its key not sure", () => {
    const result = apply(
      branchOf(output(1, 1, { kind: "if", order: "V" })),
      command("branch_output.create", {
        id: id("branch_output", 9),
        node_id: id("node", 1),
      }),
    );
    const outputs = result.doc.nodes.get(id("node", 1))?.outputs ?? [];
    expect(outputs.map((o) => o.kind)).toEqual(["if", "elseif"]);
    expect(isAfter(outputs[1].order, "V")).toBe(true);
    expect(outputs[1].condition).toEqual({ source: "", is_valid: false });
    expect(result.sure(id("branch_output", 1))).toBeUndefined();
  });

  it("creates an else after the conditional outputs, surely", () => {
    const result = apply(
      branchOf(output(1, 1, { kind: "if" })),
      command("branch_output.create", {
        id: id("branch_output", 9),
        node_id: id("node", 1),
        else: true,
      }),
    );
    expect(result.doc.nodes.get(id("node", 1))?.outputs?.[1]).toEqual({
      id: id("branch_output", 9),
      node_id: id("node", 1),
      kind: "else",
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(id("branch_output", 9))).toBe(true);
  });

  it("writes an output's condition not surely, and changes nothing for its own", () => {
    const doc = branchOf(output(1, 1, { kind: "if" }));
    const written = apply(
      doc,
      command("branch_output.update", {
        id: id("branch_output", 1),
        condition: "",
      }),
    );
    expect(
      written.doc.nodes.get(id("node", 1))?.outputs?.[0].condition,
    ).toEqual({ source: "", is_valid: false });
    expect(written.sure(id("branch_output", 1))).toBe(false);
    const own = apply(
      doc,
      command("branch_output.update", {
        id: id("branch_output", 1),
        condition: "true",
      }),
    );
    expect(own.draft.changes.size).toBe(0);
  });

  it("reorders an output to the key that the payload gives, rekinding the outputs surely", () => {
    const result = apply(
      branchOf(
        output(1, 1, { kind: "if", order: "F" }),
        output(2, 1, { kind: "elseif", order: "V" }),
      ),
      command("branch_output.reorder", {
        id: id("branch_output", 1),
        order: "k",
      }),
    );
    const outputs = result.doc.nodes.get(id("node", 1))?.outputs ?? [];
    expect(outputs.map((o) => [o.id, o.kind, o.order])).toEqual([
      [id("branch_output", 2), "if", "V"],
      [id("branch_output", 1), "elseif", "k"],
    ]);
    expect(result.sure(id("branch_output", 1))).toBe(true);
    expect(result.sure(id("branch_output", 2))).toBe(true);
  });

  it("keeps the key of an output that is the last already, reordered without one, and changes nothing", () => {
    const result = apply(
      branchOf(
        output(1, 1, { kind: "if", order: "F" }),
        output(2, 1, { kind: "elseif", order: "V" }),
      ),
      command("branch_output.reorder", { id: id("branch_output", 2) }),
    );
    expect(result.draft.changes.size).toBe(0);
  });

  it("takes a deleted output out, makes the next the if, and takes out the connections that leave it, surely", () => {
    const result = apply(
      docOf({
        nodes: [
          branch(1, [
            output(1, 1, { kind: "if", order: "F" }),
            output(2, 1, { order: "V" }),
          ]),
          node(2),
        ],
        connections: [
          connection(1, 1, 2, { from_port: id("branch_output", 1) }),
          connection(2, 1, 2, { from_port: id("branch_output", 2) }),
        ],
      }),
      command("branch_output.delete", {
        id: id("branch_output", 1),
        confirm: true,
      }),
    );
    const outputs = result.doc.nodes.get(id("node", 1))?.outputs ?? [];
    expect(outputs.map((o) => [o.id, o.kind])).toEqual([
      [id("branch_output", 2), "if"],
    ]);
    expect([...result.doc.connections.keys()]).toEqual([id("connection", 2)]);
    expect(result.sure(id("branch_output", 1))).toBe(true);
    expect(result.sure(id("branch_output", 2))).toBe(true);
    expect(result.sure(id("node", 1))).toBeUndefined();
  });

  it("leaves a branch without outputs without the field, as the API does", () => {
    const result = apply(
      branchOf(output(1, 1, { kind: "if" })),
      command("branch_output.delete", { id: id("branch_output", 1) }),
    );
    expect(result.doc.nodes.get(id("node", 1))).not.toHaveProperty("outputs");
  });

  it("has every board that the document holds read again for an output that comes back from the trash", () => {
    const result = apply(
      branchOf(),
      command("branch_output.restore", { id: id("branch_output", 1) }),
    );
    expect(result.draft.rereads).toEqual(new Set([boardPart(boardA)]));
  });
});

describe("the connection appliers", () => {
  const nodes = [node(1), node(2), branch(3, [output(1, 3, { kind: "if" })])];

  it("creates a connection on its node's board, from out, with the empty label, surely, when the payload gives its key", () => {
    const result = apply(
      docOf({ nodes }),
      command("connection.create", {
        id: id("connection", 9),
        from_node_id: id("node", 1).toLowerCase(),
        to_node_id: id("node", 2).toLowerCase(),
        order: "V",
      }),
    );
    expect(result.doc.connections.get(id("connection", 9))).toEqual({
      id: id("connection", 9),
      project_id: projectId,
      board_id: boardA,
      from_node_id: id("node", 1),
      from_port: "out",
      to_node_id: id("node", 2),
      order: "V",
      label: empty,
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(id("connection", 9))).toBe(true);
  });

  it("creates a connection without a key after the port's last, and with a condition, not surely", () => {
    const result = apply(
      docOf({
        nodes,
        connections: [
          connection(1, 3, 2, {
            from_port: id("branch_output", 1),
            order: "k",
          }),
        ],
      }),
      command("connection.create", {
        id: id("connection", 9),
        from_node_id: id("node", 3),
        from_port: id("branch_output", 1).toLowerCase(),
        to_node_id: id("node", 1),
        condition: "met",
        on_traverse: "",
      }),
    );
    const created = result.doc.connections.get(id("connection", 9));
    expect(created).toMatchObject({
      from_port: id("branch_output", 1),
      condition: { source: "met", is_valid: false },
    });
    expect(isAfter(created?.order, "k")).toBe(true);
    expect(created).not.toHaveProperty("on_traverse");
    expect(result.sure(id("connection", 9))).toBe(false);
  });

  it("leaves out a connection from a node that the document does not hold", () => {
    const result = apply(
      docOf({ nodes }),
      command("connection.create", {
        id: id("connection", 9),
        from_node_id: id("node", 8),
        to_node_id: id("node", 7),
        order: "V",
      }),
    );
    expect(result.draft.changes.size).toBe(0);
  });

  it("updates a label surely, removes an expression for the empty source surely, and writes one not surely", () => {
    const compiled = {
      source: "true",
      is_valid: true,
      ast_version: 1,
      ast: true,
    };
    const doc = docOf({
      nodes,
      connections: [connection(1, 1, 2, { condition: compiled })],
    });
    const labelled = apply(
      doc,
      command("connection.update", {
        id: id("connection", 1),
        label: paragraph("Run", "!"),
        condition: "",
      }),
    );
    const updated = labelled.doc.connections.get(id("connection", 1));
    expect(updated?.label).toEqual({
      content: [
        { content: [{ text: "Run!", type: "text" }], type: "paragraph" },
      ],
      type: "doc",
    });
    expect(updated).not.toHaveProperty("condition");
    expect(labelled.sure(id("connection", 1))).toBe(true);
    const scripted = apply(
      doc,
      command("connection.update", {
        id: id("connection", 1),
        on_traverse: "n = n + 1",
      }),
    );
    expect(
      scripted.doc.connections.get(id("connection", 1))?.on_traverse,
    ).toEqual({ source: "n = n + 1", is_valid: false });
    expect(scripted.sure(id("connection", 1))).toBe(false);
  });

  it("reconnects a connection to another element's out after its last, not surely, and to another node it enters, surely", () => {
    const doc = docOf({
      nodes,
      connections: [
        connection(1, 3, 2, { from_port: id("branch_output", 1), order: "V" }),
        connection(2, 1, 2, { order: "k" }),
      ],
    });
    const moved = apply(
      doc,
      command("connection.reconnect", {
        id: id("connection", 1),
        from_node_id: id("node", 1),
      }),
    );
    const reconnected = moved.doc.connections.get(id("connection", 1));
    expect(reconnected).toMatchObject({
      from_node_id: id("node", 1),
      from_port: "out",
    });
    expect(isAfter(reconnected?.order, "k")).toBe(true);
    expect(moved.sure(id("connection", 1))).toBe(false);
    const entered = apply(
      doc,
      command("connection.reconnect", {
        id: id("connection", 1),
        to_node_id: id("node", 1),
      }),
    );
    expect(entered.doc.connections.get(id("connection", 1))).toMatchObject({
      from_port: id("branch_output", 1),
      to_node_id: id("node", 1),
      order: "V",
    });
    expect(entered.sure(id("connection", 1))).toBe(true);
  });

  it("reorders a connection to the key that the payload gives surely, and after the port's last without one, not surely", () => {
    const doc = docOf({
      nodes,
      connections: [
        connection(1, 1, 2, { order: "F" }),
        connection(2, 1, 2, { order: "V" }),
      ],
    });
    const given = apply(
      doc,
      command("connection.reorder", { id: id("connection", 2), order: "A" }),
    );
    expect(given.doc.connections.get(id("connection", 2))?.order).toBe("A");
    expect(given.sure(id("connection", 2))).toBe(true);
    const last = apply(
      doc,
      command("connection.reorder", { id: id("connection", 1) }),
    );
    expect(
      isAfter(last.doc.connections.get(id("connection", 1))?.order, "V"),
    ).toBe(true);
    expect(last.sure(id("connection", 1))).toBe(false);
    const stays = apply(
      doc,
      command("connection.reorder", { id: id("connection", 2) }),
    );
    expect(stays.draft.changes.size).toBe(0);
  });

  it("takes a deleted connection out surely", () => {
    const result = apply(
      docOf({ nodes, connections: [connection(1, 1, 2)] }),
      command("connection.delete", { id: id("connection", 1).toLowerCase() }),
    );
    expect(result.doc.connections.size).toBe(0);
    expect(result.sure(id("connection", 1))).toBe(true);
  });

  it("has the store read a connection that comes back from the trash", () => {
    const result = apply(
      docOf({ nodes }),
      command("connection.restore", { id: id("connection", 1) }),
    );
    expect(result.sure(id("connection", 1))).toBe(false);
    expect(result.doc.connections.size).toBe(0);
  });
});

describe("the variable appliers", () => {
  it("creates a board's variable with the zero value of its type, surely, when the payload gives its key", () => {
    const result = apply(
      docOf(),
      command("variable.create", {
        id: id("variable", 9).toLowerCase(),
        key: "met",
        type: "bool",
        scope: "board",
        board_id: boardA.toLowerCase(),
        reset_on_enter: true,
        order: "V",
      }),
    );
    expect(result.doc.variables.get(id("variable", 9))).toEqual({
      id: id("variable", 9),
      project_id: projectId,
      scope: "board",
      board_id: boardA,
      order: "V",
      key: "met",
      type: "bool",
      default: false,
      description: "",
      reset_on_enter: true,
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(id("variable", 9))).toBe(true);
  });

  it("creates a variable without a key of order after the folder's last child, not surely", () => {
    const f = folder(1, "variable");
    const result = apply(
      docOf({
        folders: [f],
        variables: [variable(1, { folder_id: f.id, order: "k" })],
      }),
      command("variable.create", {
        id: id("variable", 9),
        key: "n",
        type: "float",
        scope: "global",
        folder_id: f.id,
        default: -0,
      }),
    );
    const created = result.doc.variables.get(id("variable", 9));
    expect(isAfter(created?.order, "k")).toBe(true);
    expect(Object.is(created?.default, 0)).toBe(true);
    expect(result.sure(id("variable", 9))).toBe(false);
  });

  it("leaves out a variable created while the document does not hold the variables", () => {
    const result = apply(
      docOf({ parts: ["tree"] }),
      command("variable.create", {
        id: id("variable", 9),
        key: "n",
        type: "int",
        scope: "global",
        order: "V",
      }),
    );
    expect(result.doc.variables.size).toBe(0);
    expect(result.sure(id("variable", 9))).toBeUndefined();
  });

  it("renames, describes, and sets a default, surely", () => {
    const result = apply(
      docOf({ variables: [variable(1)] }),
      command("variable.rename", { id: id("variable", 1), key: "gold" }),
      command("variable.describe", {
        id: id("variable", 1),
        description: "Coins",
      }),
      command("variable.set_default", { id: id("variable", 1), default: 3 }),
    );
    expect(result.doc.variables.get(id("variable", 1))).toMatchObject({
      key: "gold",
      description: "Coins",
      default: 3,
      updated_at: time,
    });
    expect(result.sure(id("variable", 1))).toBe(true);
  });

  it("retypes a variable with the zero value of its new type, not the old default converted", () => {
    const result = apply(
      docOf({ variables: [variable(1, { default: 5 })] }),
      command("variable.retype", { id: id("variable", 1), type: "string" }),
    );
    expect(result.doc.variables.get(id("variable", 1))).toMatchObject({
      type: "string",
      default: "",
    });
    expect(result.sure(id("variable", 1))).toBe(true);
  });

  it("makes a board's variable a global one without its board, and sets whether it resets", () => {
    const scoped = apply(
      docOf({
        variables: [
          variable(1, {
            scope: "board",
            board_id: boardA,
            reset_on_enter: true,
          }),
        ],
      }),
      command("variable.set_reset_on_enter", {
        id: id("variable", 1),
        reset_on_enter: false,
      }),
      command("variable.set_scope", { id: id("variable", 1), scope: "global" }),
    );
    const v = scoped.doc.variables.get(id("variable", 1));
    expect(v).toMatchObject({ scope: "global", reset_on_enter: false });
    expect(v).not.toHaveProperty("board_id");
    expect(scoped.sure(id("variable", 1))).toBe(true);
  });

  it("moves a variable to the key that the payload gives surely, keeps the key of the last one surely, and moves one to another folder not surely", () => {
    const f = folder(1, "variable");
    const doc = docOf({
      folders: [f],
      variables: [variable(1, { order: "F" }), variable(2, { order: "V" })],
    });
    const given = apply(
      doc,
      command("variable.move", { id: id("variable", 2), order: "A" }),
    );
    expect(given.doc.variables.get(id("variable", 2))?.order).toBe("A");
    expect(given.sure(id("variable", 2))).toBe(true);
    const stays = apply(
      doc,
      command("variable.move", { id: id("variable", 2) }),
    );
    expect(stays.doc.variables.get(id("variable", 2))?.order).toBe("V");
    expect(stays.sure(id("variable", 2))).toBe(true);
    const into = apply(
      doc,
      command("variable.move", {
        id: id("variable", 2),
        folder_id: f.id.toLowerCase(),
      }),
    );
    expect(into.doc.variables.get(id("variable", 2))?.folder_id).toBe(f.id);
    expect(into.sure(id("variable", 2))).toBe(false);
  });

  it("takes a deleted variable out surely, and predicts nothing of one restored", () => {
    const doc = docOf({ variables: [variable(1)] });
    const deleted = apply(
      doc,
      command("variable.delete", { id: id("variable", 1), force: true }),
    );
    expect(deleted.doc.variables.size).toBe(0);
    expect(deleted.sure(id("variable", 1))).toBe(true);
    const restored = apply(
      deleted.doc,
      command("variable.restore", { id: id("variable", 1) }),
    );
    expect(restored.draft.changes.size).toBe(0);
  });
});

describe("the component appliers", () => {
  const typed = (...components: ComponentEntry[]) =>
    docOf({
      componentTypes: [
        componentType(1, [
          attribute(1, 1, { order: "F" }),
          attribute(2, 1, {
            type: "enum",
            options: ["a", "b"],
            default: "a",
            order: "V",
          }),
        ]),
        componentType(2, []),
      ],
      components,
    });

  it("creates a type with the key that the payload gives surely, and derives one without it, not surely", () => {
    const given = apply(
      docOf(),
      command("component_type.create", {
        id: id("component_type", 9),
        name: "Character",
        key: "character",
      }),
    );
    expect(given.doc.componentTypes.get(id("component_type", 9))).toEqual({
      id: id("component_type", 9),
      project_id: projectId,
      key: "character",
      name: "Character",
      icon: "none",
      attributes: [],
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(given.sure(id("component_type", 9))).toBe(true);
    const derived = apply(
      docOf(),
      command("component_type.create", {
        id: id("component_type", 9),
        name: "Ship Crew",
      }),
    );
    expect(derived.doc.componentTypes.get(id("component_type", 9))?.key).toBe(
      "ship_crew",
    );
    expect(derived.sure(id("component_type", 9))).toBe(false);
  });

  it("renames a type, sets its key and its icon, surely", () => {
    const result = apply(
      typed(),
      command("component_type.rename", {
        id: id("component_type", 1),
        name: "Person",
      }),
      command("component_type.set_key", {
        id: id("component_type", 1),
        key: "person",
      }),
      command("component_type.set_icon", {
        id: id("component_type", 1),
        icon: "person",
      }),
    );
    expect(
      result.doc.componentTypes.get(id("component_type", 1)),
    ).toMatchObject({ name: "Person", key: "person", icon: "person" });
    expect(result.sure(id("component_type", 1))).toBe(true);
  });

  it("takes a deleted type out surely, and predicts nothing of one restored", () => {
    const deleted = apply(
      typed(),
      command("component_type.delete", { id: id("component_type", 2) }),
    );
    expect(deleted.doc.componentTypes.has(id("component_type", 2))).toBe(false);
    expect(deleted.sure(id("component_type", 2))).toBe(true);
    const restored = apply(
      deleted.doc,
      command("component_type.restore", { id: id("component_type", 2) }),
    );
    expect(restored.draft.changes.size).toBe(0);
  });

  it("adds a definition to its type in its order, by its own ID, and gives each component of the type its default", () => {
    const result = apply(
      typed(
        component(1, 1, {
          [id("attribute_def", 1)]: 4,
          [id("attribute_def", 2)]: "b",
        }),
        summary(2, 1),
        summary(3, 2),
      ),
      command("attribute.create", {
        id: id("attribute_def", 9),
        component_type_id: id("component_type", 1).toLowerCase(),
        name: "Trust",
        key: "trust",
        type: "float",
        default: 0.5,
        order: "K",
      }),
    );
    const type = result.doc.componentTypes.get(id("component_type", 1));
    expect(type?.attributes.map((a) => a.id)).toEqual([
      id("attribute_def", 1),
      id("attribute_def", 9),
      id("attribute_def", 2),
    ]);
    expect(type?.attributes[1]).toEqual({
      id: id("attribute_def", 9),
      component_type_id: id("component_type", 1),
      order: "K",
      key: "trust",
      name: "Trust",
      type: "float",
      default: 0.5,
      runtime: false,
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(type?.updated_at).toBe(earlier);
    expect(result.sure(id("attribute_def", 9))).toBe(true);
    expect(result.sure(id("component_type", 1))).toBeUndefined();
    expect(result.doc.components.get(id("component", 1))?.attributes).toEqual({
      [id("attribute_def", 1)]: 4,
      [id("attribute_def", 2)]: "b",
      [id("attribute_def", 9)]: 0.5,
    });
    expect(result.sure(id("component", 1))).toBe(true);
    expect(result.doc.components.get(id("component", 2))).not.toHaveProperty(
      "attributes",
    );
    expect(result.sure(id("component", 2))).toBe(true);
    expect(result.sure(id("component", 3))).toBeUndefined();
  });

  it("adds an enum without a key or a key of order with its first option for default, not surely", () => {
    const result = apply(
      typed(),
      command("attribute.create", {
        id: id("attribute_def", 9),
        component_type_id: id("component_type", 1),
        name: "Mood",
        type: "enum",
        options: ["calm", "angry"],
      }),
    );
    const added = result.doc.componentTypes
      .get(id("component_type", 1))
      ?.attributes.at(-1);
    expect(added).toMatchObject({
      id: id("attribute_def", 9),
      key: "mood",
      options: ["calm", "angry"],
      default: "calm",
    });
    expect(isAfter(added?.order, "V")).toBe(true);
    expect(result.sure(id("attribute_def", 9))).toBe(false);
  });

  it("renames a definition, sets its key, its default, and whether it is runtime, by its own ID, surely", () => {
    const result = apply(
      typed(),
      command("attribute.rename", { id: id("attribute_def", 1), name: "Gold" }),
      command("attribute.set_key", { id: id("attribute_def", 1), key: "gold" }),
      command("attribute.set_default", {
        id: id("attribute_def", 1),
        default: 7,
      }),
      command("attribute.set_runtime", {
        id: id("attribute_def", 1).toLowerCase(),
        runtime: true,
      }),
    );
    expect(
      result.doc.componentTypes.get(id("component_type", 1))?.attributes[0],
    ).toMatchObject({
      name: "Gold",
      key: "gold",
      default: 7,
      runtime: true,
      updated_at: time,
    });
    expect(result.sure(id("attribute_def", 1))).toBe(true);
    expect(result.sure(id("component_type", 1))).toBeUndefined();
  });

  it("sets an enum's options, giving a component the payload's value, keeping one that is still an option, and the default otherwise", () => {
    const enumId = id("attribute_def", 2);
    const result = apply(
      typed(
        component(1, 1, { [enumId]: "a" }),
        component(2, 1, { [enumId]: "b" }),
        component(3, 1, { [enumId]: "b" }),
        summary(4, 1),
      ),
      command("attribute.set_options", {
        id: enumId,
        options: ["b", "c"],
        default: "c",
        values: { [id("component", 3).toLowerCase()]: "c" },
      }),
    );
    expect(
      result.doc.componentTypes.get(id("component_type", 1))?.attributes[1],
    ).toMatchObject({ options: ["b", "c"], default: "c" });
    expect(result.sure(enumId)).toBe(true);
    expect(
      result.doc.components.get(id("component", 1))?.attributes?.[enumId],
    ).toBe("c");
    expect(result.sure(id("component", 1))).toBe(true);
    expect(result.sure(id("component", 2))).toBeUndefined();
    expect(
      result.doc.components.get(id("component", 3))?.attributes?.[enumId],
    ).toBe("c");
    expect(result.sure(id("component", 4))).toBeUndefined();
  });

  it("retypes a definition, resetting the values to its new default, or leaving those that it converts to the server", () => {
    const intId = id("attribute_def", 1);
    const doc = typed(component(1, 1, { [intId]: 4 }));
    const reset = apply(
      doc,
      command("attribute.retype", {
        id: intId,
        type: "string",
        conversion: "reset",
        default: "none",
      }),
    );
    const retyped = reset.doc.componentTypes.get(id("component_type", 1))
      ?.attributes[0];
    expect(retyped).toMatchObject({ type: "string", default: "none" });
    expect(retyped).not.toHaveProperty("options");
    expect(
      reset.doc.components.get(id("component", 1))?.attributes?.[intId],
    ).toBe("none");
    expect(reset.sure(id("component", 1))).toBe(true);
    const converted = apply(
      doc,
      command("attribute.retype", {
        id: intId,
        type: "enum",
        options: ["4", "5"],
        conversion: "convert",
      }),
    );
    expect(
      converted.doc.componentTypes.get(id("component_type", 1))?.attributes[0],
    ).toMatchObject({ type: "enum", options: ["4", "5"], default: "4" });
    expect(converted.sure(id("component", 1))).toBeUndefined();
  });

  it("moves a definition to the key that the payload gives, in its type's order, surely", () => {
    const result = apply(
      typed(),
      command("attribute.move", { id: id("attribute_def", 1), order: "k" }),
    );
    const attributes =
      result.doc.componentTypes.get(id("component_type", 1))?.attributes ?? [];
    expect(attributes.map((a) => [a.id, a.order])).toEqual([
      [id("attribute_def", 2), "V"],
      [id("attribute_def", 1), "k"],
    ]);
    expect(result.sure(id("attribute_def", 1))).toBe(true);
  });

  it("takes a deleted definition out of its type and the components' values, surely", () => {
    const result = apply(
      typed(
        component(1, 1, {
          [id("attribute_def", 1)]: 4,
          [id("attribute_def", 2)]: "a",
        }),
        summary(2, 1),
      ),
      command("attribute.delete", { id: id("attribute_def", 1) }),
    );
    expect(
      result.doc.componentTypes
        .get(id("component_type", 1))
        ?.attributes.map((a) => a.id),
    ).toEqual([id("attribute_def", 2)]);
    expect(result.sure(id("attribute_def", 1))).toBe(true);
    expect(result.doc.components.get(id("component", 1))?.attributes).toEqual({
      [id("attribute_def", 2)]: "a",
    });
    expect(result.sure(id("component", 1))).toBe(true);
    expect(result.sure(id("component", 2))).toBe(true);
  });

  it("predicts nothing of a definition restored", () => {
    const result = apply(
      typed(summary(1, 1)),
      command("attribute.restore", { id: id("attribute_def", 3) }),
    );
    expect(result.draft.changes.size).toBe(0);
  });

  it("creates a component with the payload's values and its type's defaults for the others, and the empty description, surely", () => {
    const result = apply(
      typed(),
      command("component.create", {
        id: id("component", 9),
        component_type_id: id("component_type", 1),
        name: "Elara",
        key: "elara",
        order: "V",
        attributes: { [id("attribute_def", 1).toLowerCase()]: 3 },
      }),
    );
    expect(result.doc.components.get(id("component", 9))).toEqual({
      id: id("component", 9),
      project_id: projectId,
      component_type_id: id("component_type", 1),
      order: "V",
      key: "elara",
      name: "Elara",
      description: empty,
      attributes: {
        [id("attribute_def", 1)]: 3,
        [id("attribute_def", 2)]: "a",
      },
      rev: 1,
      created_at: time,
      updated_at: time,
    });
    expect(result.sure(id("component", 9))).toBe(true);
  });

  it("renames a component, sets its key and its image, and takes its image out, surely", () => {
    const result = apply(
      typed(summary(1, 1, { image_asset_id: id("asset", 1) })),
      command("component.rename", { id: id("component", 1), name: "Mara" }),
      command("component.set_key", { id: id("component", 1), key: "mara" }),
      command("component.set_image", { id: id("component", 1) }),
    );
    const c = result.doc.components.get(id("component", 1));
    expect(c).toMatchObject({ name: "Mara", key: "mara" });
    expect(c).not.toHaveProperty("image_asset_id");
    expect(result.sure(id("component", 1))).toBe(true);
  });

  it("describes a component that the document holds without its description, surely", () => {
    const result = apply(
      typed(summary(1, 1)),
      command("component.describe", {
        id: id("component", 1),
        description: paragraph("A ", "sailor"),
      }),
    );
    expect(result.doc.components.get(id("component", 1))?.description).toEqual({
      content: [
        { content: [{ text: "A sailor", type: "text" }], type: "paragraph" },
      ],
      type: "doc",
    });
    expect(result.sure(id("component", 1))).toBe(true);
  });

  it("sets a value surely, and one of a component that the document holds without its values not surely", () => {
    const full = apply(
      typed(
        component(1, 1, {
          [id("attribute_def", 1)]: 4,
          [id("attribute_def", 2)]: "a",
        }),
      ),
      command("component.set_attribute", {
        id: id("component", 1),
        attribute_id: id("attribute_def", 1).toLowerCase(),
        value: 5,
      }),
    );
    expect(full.doc.components.get(id("component", 1))?.attributes).toEqual({
      [id("attribute_def", 1)]: 5,
      [id("attribute_def", 2)]: "a",
    });
    expect(full.sure(id("component", 1))).toBe(true);
    const listed = apply(
      typed(summary(1, 1)),
      command("component.set_attribute", {
        id: id("component", 1),
        attribute_id: id("attribute_def", 1),
        value: 5,
      }),
    );
    expect(listed.doc.components.get(id("component", 1))?.attributes).toEqual({
      [id("attribute_def", 1)]: 5,
    });
    expect(listed.sure(id("component", 1))).toBe(false);
  });

  it("moves a component into a folder at the key that the payload gives, surely", () => {
    const f = folder(1, "component");
    const result = apply(
      { ...typed(summary(1, 1)), folders: new Map([[f.id, f]]) },
      command("component.move", {
        id: id("component", 1),
        folder_id: f.id,
        order: "k",
      }),
    );
    expect(result.doc.components.get(id("component", 1))).toMatchObject({
      folder_id: f.id,
      order: "k",
    });
    expect(result.sure(id("component", 1))).toBe(true);
  });

  it("takes a deleted component out surely, and predicts nothing of one restored", () => {
    const deleted = apply(
      typed(summary(1, 1)),
      command("component.delete", { id: id("component", 1), force: true }),
    );
    expect(deleted.doc.components.size).toBe(0);
    expect(deleted.sure(id("component", 1))).toBe(true);
    const restored = apply(
      deleted.doc,
      command("component.restore", { id: id("component", 1) }),
    );
    expect(restored.draft.changes.size).toBe(0);
  });
});

describe("the clipboard and validation appliers", () => {
  it("has the board that a paste lands on read again, and predicts none of what it creates", () => {
    const result = apply(
      docOf(),
      command("clipboard.paste", {
        board_id: boardA.toLowerCase(),
        offset: { x: 10, y: 10 },
        ids: { [id("node", 1)]: id("node", 9) },
        clipboard: {
          format: "storycreep.clipboard",
          version: 1,
          project_id: projectId,
          board_id: boardB,
          nodes: [
            { id: id("node", 1), type: "element", position: { x: 0, y: 0 } },
          ],
          connections: [],
          variables: [],
          components: [],
        },
      }),
    );
    expect(result.draft.rereads).toEqual(new Set([boardPart(boardA)]));
    expect(result.draft.changes.size).toBe(0);
  });

  it("changes the entity whose finding a mute or an unmute changes, an output by its own ID", () => {
    const result = apply(
      docOf({
        nodes: [branch(1, [output(1, 1, { kind: "if" })])],
        variables: [variable(1)],
        boards: [board(1)],
      }),
      command("validation.mute", {
        entity_id: id("branch_output", 1).toLowerCase(),
        code: "port_unconnected",
        reason: "Later",
      }),
      command("validation.unmute", {
        entity_id: id("variable", 1),
        code: "var_read_never_written",
      }),
      command("validation.mute", {
        entity_id: boardA,
        code: "board_entry_missing",
        reason: "Later",
      }),
    );
    expect(result.doc.nodes.get(id("node", 1))?.outputs?.[0].updated_at).toBe(
      time,
    );
    expect(result.sure(id("branch_output", 1))).toBe(true);
    expect(result.sure(id("node", 1))).toBeUndefined();
    expect(result.doc.variables.get(id("variable", 1))?.updated_at).toBe(time);
    expect(result.sure(id("variable", 1))).toBe(true);
    expect(result.sure(boardA)).toBe(true);
  });
});
