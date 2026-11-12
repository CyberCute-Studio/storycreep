import type { RichTextDoc } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { docOf, testConnection, testNode } from "../test/canvas";
import { labelContext, labelsOf } from "./labels";
import { type NodeItem, Scene } from "./scene";
import { type TreeMessages, treeChoices, treeRows, typedRow } from "./tree";

/** The page's messages, as their keys and their arguments, in the arguments' order. */
const t: TreeMessages = (key, args) =>
  args === undefined ? key : `${key}(${Object.values(args).join("|")})`;

/** Returns a text of one paragraph for each of lines. */
function text(...lines: string[]): RichTextDoc {
  return {
    type: "doc",
    content: lines.map((line) => ({
      type: "paragraph",
      content: [{ type: "text", text: line }],
    })),
  };
}

const times = { rev: 1, created_at: "", updated_at: "" };

/** A board of an element, a branch of two outputs under it, and, beside them, a jumper and an element that starts at the first one's height. */
function board() {
  const nodes = [
    testNode("nd_j", 480, 400, { type: "jumper", title: "Onward" }),
    testNode("nd_b", 0, 400, {
      type: "branch",
      title: "Has the key",
      outputs: [
        {
          id: "bo_1",
          node_id: "nd_b",
          kind: "if",
          order: "A",
          condition: { source: "key\n  == 1", is_valid: true },
          ...times,
        },
        { id: "bo_2", node_id: "nd_b", kind: "else", ...times },
      ],
    }),
    testNode("nd_2", 480, 0, { title: "" }),
    testNode("nd_1", 0, 0, {
      title: "Harbour",
      text: text("The boat  waits.", "", "Elara nods."),
    }),
  ];
  const connections = [
    testConnection("cn_3", "nd_b", "nd_1", { from_port: "bo_2" }),
    testConnection("cn_2", "nd_1", "nd_b", {
      order: "b",
      label: text("Take the key"),
    }),
    testConnection("cn_1", "nd_1", "nd_2", { order: "a" }),
    testConnection("cn_4", "nd_b", "nd_j", { from_port: "bo_1" }),
  ];
  const doc = docOf(nodes, connections);
  const scene = new Scene("bd_a");
  scene.sync(doc);
  return { doc, scene, nodes, connections };
}

/** Returns what a card shows of a node of doc, as the canvas's page tells its controller. */
function describeOf(doc: ReturnType<typeof docOf>) {
  const c = labelContext(doc, t);
  return (item: NodeItem) => labelsOf(item.node, c);
}

describe("the canvas's accessibility tree", () => {
  // Verifies: NFR-A11Y-03
  it("lists the board's nodes from the board's top to its bottom, and across where they start at the same height, each named by what its card shows", () => {
    const { doc, scene } = board();
    const rows = treeRows()(scene, describeOf(doc), 0, t);
    expect(rows.map((row) => row.id)).toEqual(["nd_1", "nd_2", "nd_b", "nd_j"]);
    expect(rows.map((row) => row.label)).toEqual([
      "canvas.tree.node_body(Harbour|canvas.node.narration|The boat waits.; Elara nods.)",
      "canvas.tree.node(canvas.node.untitled|canvas.node.narration)",
      "canvas.tree.node_body(Has the key|canvas.node.branch|canvas.node.output_if(key == 1); canvas.node.output_else)",
      "canvas.tree.node_body(Onward|canvas.node.jumper|canvas.node.jumper_none)",
    ]);
    expect(rows.map((row) => row.title)).toEqual([
      "Harbour",
      "canvas.node.untitled",
      "Has the key",
      "Onward",
    ]);
    expect(rows.map((row) => row.leaving)).toEqual([2, 0, 2, 0]);
  });

  it("names a node anew once it changes, or once what its card says of other things does, and else as it named it", () => {
    const { doc, scene, nodes, connections } = board();
    let described = 0;
    const describe = (item: NodeItem) => {
      described++;
      return describeOf(doc)(item);
    };
    const rows = treeRows();
    rows(scene, describe, 0, t);
    expect(described).toBe(4);
    rows(scene, describe, 0, t);
    expect(described).toBe(4);
    // One node changes: it alone is named again.
    const next = docOf(
      nodes.map((n) => (n.id === "nd_2" ? { ...n, title: "Quay" } : n)),
      connections,
    );
    scene.sync(next);
    const after = rows(scene, describe, 0, t);
    expect(described).toBe(5);
    expect(after[1].title).toBe("Quay");
    // A name that cards show of another thing changed: all are.
    rows(scene, describe, 1, t);
    expect(described).toBe(9);
  });

  // Verifies: NFR-A11Y-03
  it("lists the connections that leave a node in the order of its ports and of their choices, each named by the node that it enters, its label, and the output that it leaves", () => {
    const { doc, scene } = board();
    const choices = treeChoices(
      scene,
      doc,
      new Set(["nd_1", "nd_b", "nd_2", "nd_gone"]),
      t,
    );
    // Neither a node that no connection leaves, nor one that the board has
    // not, has any.
    expect([...choices.keys()].sort()).toEqual(["nd_1", "nd_b"]);
    expect(treeChoices(scene, doc, new Set(), t).size).toBe(0);
    expect(choices.get("nd_1")).toEqual([
      { id: "cn_1", label: "canvas.tree.connection(canvas.node.untitled)" },
      {
        id: "cn_2",
        label: "canvas.tree.connection_labelled(Has the key|Take the key)",
      },
    ]);
    expect(choices.get("nd_b")).toEqual([
      {
        id: "cn_4",
        label:
          "canvas.tree.output_connection(canvas.node.output_if(key == 1)|Onward)",
      },
      {
        id: "cn_3",
        label: "canvas.tree.output_connection(canvas.node.output_else|Harbour)",
      },
    ]);
  });

  it("finds the next node whose title starts with what is typed, whatever its case, around the list's end, and stays on one that goes on matching", () => {
    const rows = [
      { title: "Harbour" },
      { title: "Quay" },
      { title: "harbour master" },
      { title: "Lighthouse" },
    ];
    expect(typedRow(rows, 0, "h")).toBe(2);
    expect(typedRow(rows, 2, "H")).toBe(0);
    expect(typedRow(rows, 3, "q")).toBe(1);
    // What goes on from a letter stays where it still matches.
    expect(typedRow(rows, 2, "harbour m")).toBe(2);
    expect(typedRow(rows, 0, "harbour m")).toBe(2);
    expect(typedRow(rows, 0, "x")).toBeUndefined();
    expect(typedRow(rows, 0, "")).toBeUndefined();
    expect(typedRow([], 0, "h")).toBeUndefined();
  });
});
