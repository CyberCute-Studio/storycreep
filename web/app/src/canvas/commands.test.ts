import { describe, expect, it } from "vitest";
import { docOf, gridNodes, testNode } from "../test/canvas";
import {
  deleteBatch,
  duplicateBatch,
  maxEdited,
  maxNodes,
  moveBatch,
  updateBatch,
} from "./commands";

describe("the commands of the canvas's edits", () => {
  // Verifies: FR-EDIT-02/AC2
  it("moves 100 selected nodes with one command, expecting the revision shown of each", () => {
    const nodes = gridNodes(100, 10).map((n, i) => ({ ...n, rev: i + 1 }));
    const view = docOf(nodes);
    const places = new Map(
      nodes.map((n) => [n.id, { x: n.position.x + 8, y: 0 }]),
    );
    const batch = moveBatch(view, places);
    expect(batch).toHaveLength(1);
    const [move] = batch;
    expect(move.type).toBe("node.move");
    if (move.type !== "node.move") {
      return;
    }
    expect(move.payload.nodes).toHaveLength(100);
    expect(move.payload.nodes[3]).toEqual({
      id: nodes[3].id,
      position: { x: nodes[3].position.x + 8, y: 0 },
    });
    expect(move.expected_rev?.[nodes[3].id]).toBe(4);
    expect(Object.keys(move.expected_rev ?? {})).toHaveLength(100);
  });

  it("moves and sizes nodes expecting the revisions that a drag had of them as it started, where it had them", () => {
    const view = docOf([
      testNode("nd_a", 0, 0, { rev: 5 }),
      testNode("nd_b", 0, 0, { rev: 9 }),
    ]);
    const [move] = moveBatch(
      view,
      new Map([
        ["nd_a", { x: 8, y: 0 }],
        ["nd_b", { x: 8, y: 0 }],
      ]),
      new Map([["nd_a", 4]]),
    );
    expect(move.expected_rev).toEqual({ nd_a: 4, nd_b: 9 });
    const [update] = updateBatch(
      view,
      ["nd_b"],
      () => ({ size: { width: 320, height: 200 } }),
      new Map([["nd_b", 8]]),
    );
    expect(update.expected_rev).toEqual({ nd_b: 8 });
  });

  it("moves more nodes than one command names with a command for each 10,000", () => {
    const nodes = gridNodes(maxNodes + 1, 100);
    const places = new Map(nodes.map((n) => [n.id, { x: 0, y: 0 }]));
    const batch = moveBatch(docOf(nodes), places);
    expect(batch.map((c) => c.type)).toEqual(["node.move", "node.move"]);
  });

  it("deletes one node with node.delete, and a selection with one node.delete_many", () => {
    const view = docOf([
      testNode("nd_a", 0, 0),
      testNode("nd_b", 0, 0, { rev: 7 }),
    ]);
    expect(deleteBatch(view, ["nd_b"])).toEqual([
      {
        type: "node.delete",
        payload: { id: "nd_b" },
        expected_rev: { nd_b: 7 },
      },
    ]);
    expect(deleteBatch(view, ["nd_a", "nd_b"])).toEqual([
      {
        type: "node.delete_many",
        payload: { ids: ["nd_a", "nd_b"] },
        expected_rev: { nd_a: 1, nd_b: 7 },
      },
    ]);
  });

  it("duplicates each node beside it, of new IDs, in one batch of as many as a batch holds", () => {
    const nodes = gridNodes(maxEdited, 100);
    let n = 0;
    const { batch, copies } = duplicateBatch(
      docOf(nodes),
      nodes.map((node) => node.id),
      { x: 32, y: 32 },
      () => `nd_copy_${n++}`,
    );
    expect(batch).toHaveLength(maxEdited);
    expect(copies).toHaveLength(maxEdited);
    expect(batch[1]).toEqual({
      type: "node.duplicate",
      payload: {
        id: "nd_copy_1",
        source_id: nodes[1].id,
        position: { x: nodes[1].position.x + 32, y: 32 },
      },
      expected_rev: { [nodes[1].id]: 1 },
    });
  });

  it("changes only the nodes that a change changes, each with its own revision", () => {
    const view = docOf([
      testNode("nd_a", 0, 0, { color: "red" }),
      testNode("nd_b", 0, 0, { rev: 3 }),
    ]);
    expect(
      updateBatch(view, ["nd_a", "nd_b", "nd_gone"], (node) =>
        node.color === "red" ? undefined : { color: "red" },
      ),
    ).toEqual([
      {
        type: "node.update",
        payload: { id: "nd_b", color: "red" },
        expected_rev: { nd_b: 3 },
      },
    ]);
  });
});
