import type {
  Clipboard,
  ClipboardNode,
  ClipboardReport,
} from "@storycreep/shared/api";
import type { IdKind } from "@storycreep/shared/ids";
import { describe, expect, it } from "vitest";
import type { Command } from "../document/store";
import {
  boundsOf,
  commandBytes,
  creatable,
  createCommands,
  lacksAny,
  pasteBatches,
  pasteCommand,
  pasteOffset,
  withinLimits,
} from "./clipboard";
import { positionLimit } from "./edit";

/** Returns a clipboard of nodes, copied from the board bd_harbour, with connections. */
function clipboardOf(
  nodes: ClipboardNode[],
  connections: Clipboard["connections"] = [],
): Clipboard {
  return {
    format: "storycreep.clipboard",
    version: 1,
    project_id: "prj_harbour",
    board_id: "bd_harbour",
    nodes,
    connections,
    variables: [],
    components: [],
  };
}

/** Returns an element of the clipboard at x, y. */
function element(id: string, x: number, y: number): ClipboardNode {
  return { id, type: "element", position: { x, y } };
}

/** Returns a MakeId that numbers the IDs that it makes, by their kind. */
function counter() {
  const counts = new Map<string, number>();
  return (kind: IdKind) => {
    const n = (counts.get(kind) ?? 0) + 1;
    counts.set(kind, n);
    return `${kind}_${n}`;
  };
}

/** A report that lists nothing. */
const nothing: ClipboardReport = {
  variables: [],
  components: [],
  attributes: [],
  speakers: [],
  nodes: [],
  assets: [],
};

describe("the paste of a clipboard", () => {
  // Verifies: FR-EDIT-07/AC2
  it("gives each node, output, and connection a new ID, and moves every node by one offset, keeping the clipboard's connections as they are", () => {
    const clipboard = clipboardOf(
      [
        element("nd_quay", 0, 0),
        {
          id: "nd_fork",
          type: "branch",
          position: { x: 320, y: 0 },
          outputs: [
            { id: "bo_if", condition: "true" },
            { id: "bo_else", else: true },
          ],
        },
        element("nd_dock", 640, 0),
      ],
      [
        { id: "cn_a", from_node_id: "nd_quay", to_node_id: "nd_fork" },
        {
          id: "cn_b",
          from_node_id: "nd_fork",
          from_port: "bo_if",
          to_node_id: "nd_dock",
        },
        {
          id: "cn_c",
          from_node_id: "nd_fork",
          from_port: "bo_else",
          to_node_id: "nd_quay",
        },
      ],
    );
    const { command, nodes } = pasteCommand(
      "bd_lighthouse",
      clipboard,
      { x: 40, y: -8 },
      counter(),
    );
    expect(nodes).toEqual(["node_1", "node_2", "node_3"]);
    expect(command).toEqual({
      type: "clipboard.paste",
      payload: {
        board_id: "bd_lighthouse",
        clipboard,
        offset: { x: 40, y: -8 },
        ids: {
          nd_quay: "node_1",
          nd_fork: "node_2",
          bo_if: "branch_output_1",
          bo_else: "branch_output_2",
          nd_dock: "node_3",
          cn_a: "connection_1",
          cn_b: "connection_2",
          cn_c: "connection_3",
        },
      },
    });
  });

  it("puts the middle of the nodes at the point asked for, on the grid where it snaps, and moves on from where they would hide a node", () => {
    // Two elements of 240 by 128, at 0 and 320 across: 560 by 128 whole,
    // whose middle is at 280, 64.
    const clipboard = clipboardOf([
      element("nd_a", 0, 0),
      element("nd_b", 320, 0),
    ]);
    expect(boundsOf(clipboard)).toEqual({
      x: 0,
      y: 0,
      width: 560,
      height: 128,
    });
    expect(
      pasteOffset(clipboard, { x: 1003, y: 501 }, true, () => false),
    ).toEqual({
      x: 720,
      y: 440,
    });
    expect(
      pasteOffset(clipboard, { x: 1003.333, y: 501 }, false, () => false),
    ).toEqual({
      x: 723.33,
      y: 437,
    });
    // Pasted back where they were copied from, onto the board that has them
    // still, the copies move on from them, as often as it takes.
    const corners = new Set(["0,0", "320,0", "32,32"]);
    expect(
      pasteOffset(clipboard, { x: 280, y: 64 }, true, (p) =>
        corners.has(`${p.x},${p.y}`),
      ),
    ).toEqual({ x: 64, y: 64 });
  });

  it("keeps every node within the board's limits, which the server would refuse the paste for", () => {
    const clipboard = clipboardOf([
      element("nd_a", positionLimit - 100, 0),
      element("nd_b", positionLimit - 300, -positionLimit + 10.5),
    ]);
    expect(withinLimits(clipboard, { x: 500, y: -50 })).toEqual({
      x: 100,
      y: -10.5,
    });
    expect(withinLimits(clipboard, { x: -20, y: 20 })).toEqual({
      x: -20,
      y: 20,
    });
    expect(
      pasteOffset(
        clipboard,
        { x: positionLimit + 1000, y: 0 },
        true,
        () => false,
      ).x,
    ).toBe(100);
  });
});

describe("what a paste creates first", () => {
  it("is nothing for a report that lists nothing, and tells what it lists", () => {
    expect(createCommands(nothing, "bd_a")).toEqual([]);
    expect(lacksAny(nothing)).toBe(false);
    expect(creatable({ ...nothing, speakers: ["cmp_ghost"] })).toBe(false);
    expect(lacksAny({ ...nothing, speakers: ["cmp_ghost"] })).toBe(true);
    expect(lacksAny({ ...nothing, assets: ["ast_cover"] })).toBe(true);
    expect(lacksAny({ ...nothing, nodes: ["nd_far"] })).toBe(true);
  });

  // Verifies: FR-EDIT-07/AC1
  it("creates each variable as declared, a board's of the board, the types that the project lacks once each with their definitions, the definitions that a type lacks, and then each component", () => {
    const fear = {
      id: "atr_fear",
      key: "fear",
      name: "Fear",
      type: "int" as const,
      runtime: true,
      default: 0,
    };
    const mood = {
      id: "atr_mood",
      key: "mood",
      name: "Mood",
      type: "enum" as const,
      options: ["calm", "wild"],
    };
    const report: ClipboardReport = {
      ...nothing,
      variables: [
        {
          id: "var_trust",
          key: "trust",
          type: "int",
          scope: "global",
          default: 2,
          description: "How far",
        },
        {
          id: "var_mood",
          key: "mood",
          type: "string",
          scope: "board",
          board_id: "bd_harbour",
          reset_on_enter: true,
        },
      ],
      components: [
        // Elara and Mira are of a type that the project lacks, which is
        // created once, with the definitions that either declares, once
        // each; Pell is of the project's type of its type's key.
        {
          component: {
            id: "cmp_elara",
            key: "elara",
            name: "Elara",
            type: { id: "ct_person", key: "person", name: "Person" },
            attributes: [fear],
          },
        },
        {
          component: {
            id: "cmp_mira",
            key: "mira",
            name: "Mira",
            type: { id: "ct_person", key: "person", name: "Person" },
            attributes: [fear, mood],
          },
        },
        {
          component: {
            id: "cmp_pell",
            key: "pell",
            name: "Pell",
            type: { id: "ct_hero", key: "hero", name: "Hero" },
            attributes: [],
          },
          component_type_id: "ct_theirs",
        },
      ],
      attributes: [{ attribute: fear, component_type_id: "ct_theirs" }],
    };
    expect(createCommands(report, "bd_lighthouse", counter())).toEqual([
      {
        type: "variable.create",
        payload: {
          id: "variable_1",
          key: "trust",
          type: "int",
          scope: "global",
          default: 2,
          description: "How far",
        },
      },
      {
        type: "variable.create",
        payload: {
          id: "variable_2",
          key: "mood",
          type: "string",
          scope: "board",
          board_id: "bd_lighthouse",
          reset_on_enter: true,
        },
      },
      {
        type: "component_type.create",
        payload: { id: "component_type_1", name: "Person", key: "person" },
      },
      {
        type: "attribute.create",
        payload: {
          id: "attribute_def_1",
          component_type_id: "component_type_1",
          name: "Fear",
          key: "fear",
          type: "int",
          default: 0,
          runtime: true,
        },
      },
      {
        type: "attribute.create",
        payload: {
          id: "attribute_def_2",
          component_type_id: "component_type_1",
          name: "Mood",
          key: "mood",
          type: "enum",
          options: ["calm", "wild"],
        },
      },
      {
        type: "attribute.create",
        payload: {
          id: "attribute_def_3",
          component_type_id: "ct_theirs",
          name: "Fear",
          key: "fear",
          type: "int",
          default: 0,
          runtime: true,
        },
      },
      {
        type: "component.create",
        payload: {
          id: "component_1",
          component_type_id: "component_type_1",
          name: "Elara",
          key: "elara",
        },
      },
      {
        type: "component.create",
        payload: {
          id: "component_2",
          component_type_id: "component_type_1",
          name: "Mira",
          key: "mira",
        },
      },
      {
        type: "component.create",
        payload: {
          id: "component_3",
          component_type_id: "ct_theirs",
          name: "Pell",
          key: "pell",
        },
      },
    ]);
  });
});

describe("the batches of a paste", () => {
  /** Returns n commands that create variables. */
  const creates = (n: number): Command[] =>
    Array.from({ length: n }, (_, i) => ({
      type: "variable.create",
      payload: { id: `var_${i}`, key: `v${i}`, type: "int", scope: "global" },
    }));
  const paste = pasteCommand("bd_a", clipboardOf([element("nd_a", 0, 0)]), {
    x: 0,
    y: 0,
  }).command;

  it("sends what it creates in the paste's batch, before the paste, where it fits", () => {
    expect(pasteBatches([], paste)).toEqual([[paste]]);
    const some = creates(3);
    expect(pasteBatches(some, paste)).toEqual([[...some, paste]]);
  });

  it("sends what does not fit with the paste in batches before it, of at most 1,000 commands, in their order", () => {
    const many = creates(2500);
    const batches = pasteBatches(many, paste);
    expect(batches.map((b) => b.length)).toEqual([1000, 1000, 501]);
    expect(batches.flat()).toEqual([...many, paste]);
    // A last run of 1,000 leaves the paste a batch of its own.
    const full = pasteBatches(creates(1000), paste);
    expect(full.map((b) => b.length)).toEqual([1000, 1]);
    expect(full[1]).toEqual([paste]);
  });

  it("sends what it creates in batches of at most as many bytes as a request takes, and the paste with the last of them where it fits", () => {
    const some = creates(4);
    const size = commandBytes(some[0]);
    expect(
      pasteBatches(some, paste, { commands: 1000, bytes: size * 2 }),
    ).toEqual([some.slice(0, 2), some.slice(2), [paste]]);
    expect(
      pasteBatches(some.slice(0, 3), paste, { commands: 2, bytes: 5 << 20 }),
    ).toEqual([some.slice(0, 2), [some[2], paste]]);
  });
});
