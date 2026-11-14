import { describe, expect, it } from "vitest";
import { testConnection, testNode } from "../test/canvas";
import { addAfterBatch, addChoiceBatch, placeAfter } from "./batches";

// The structural edits of the script view (PRD FR-TEXT-04): one batch each,
// of the commands that the canvas sends for the same changes.

const ids = { node: "nd_new", connection: "cn_new" };

describe("the script view's structural edits", () => {
  it("adds a node after an element: the element's choices leave the new node, which it leads to", () => {
    const a = testNode("nd_a", 0, 0);
    const leaving = [
      testConnection("cn_2", "nd_a", "nd_c", { order: "W", rev: 4 }),
      testConnection("cn_1", "nd_a", "nd_b", { order: "V", rev: 2 }),
    ];
    expect(addAfterBatch(a, leaving, { x: 400, y: 0 }, ids)).toEqual([
      {
        type: "node.create",
        payload: {
          board_id: "bd_a",
          id: "nd_new",
          type: "element",
          position: { x: 400, y: 0 },
        },
      },
      {
        type: "connection.reconnect",
        payload: { id: "cn_1", from_node_id: "nd_new", from_port: "out" },
        expected_rev: { cn_1: 2 },
      },
      {
        type: "connection.reconnect",
        payload: { id: "cn_2", from_node_id: "nd_new", from_port: "out" },
        expected_rev: { cn_2: 4 },
      },
      {
        type: "connection.create",
        payload: {
          id: "cn_new",
          from_node_id: "nd_a",
          from_port: "out",
          to_node_id: "nd_new",
        },
      },
    ]);
  });

  it("adds a choice to an element: a connection after its others, to a new node", () => {
    const a = testNode("nd_a", 0, 0);
    expect(addChoiceBatch(a, { x: 400, y: 160 }, ids)).toEqual([
      {
        type: "node.create",
        payload: {
          board_id: "bd_a",
          id: "nd_new",
          type: "element",
          position: { x: 400, y: 160 },
        },
      },
      {
        type: "connection.create",
        payload: {
          id: "cn_new",
          from_node_id: "nd_a",
          from_port: "out",
          to_node_id: "nd_new",
        },
      },
    ]);
  });

  it("places the new node to the right of the element, on the grid, and below the one before, where no node has its corner", () => {
    const a = testNode("nd_a", 100, 50);
    const first = placeAfter(a, [a]);
    expect(first.x).toBeGreaterThan(100);
    expect(first.x % 8).toBe(0);
    expect(first.y % 8).toBe(0);
    const second = placeAfter(a, [a], 1);
    expect(second.x).toBe(first.x);
    expect(second.y).toBeGreaterThan(first.y);
    // A node at the first place moves the new one off it.
    const taken = testNode("nd_b", first.x, first.y);
    expect(placeAfter(a, [a, taken])).not.toEqual(first);
  });
});
