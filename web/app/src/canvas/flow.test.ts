import type { BranchOutput } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { docOf, testConnection, testNode } from "../test/canvas";
import {
  choicesOf,
  conditionalsOf,
  connectCommand,
  connectNewBatch,
  createOutputCommand,
  deleteConnectionCommand,
  deleteOutputCommand,
  keyAt,
  reconnectCommand,
  reorderConnectionCommand,
  reorderOutputCommand,
} from "./flow";
import { curveDistance, curveOf, portsOf } from "./layout";
import { Scene } from "./scene";

const at = "2026-01-01T00:00:00Z";

/** Returns an output of the branch nd_b of kind, at order. */
function output(
  id: string,
  kind: BranchOutput["kind"],
  order?: string,
): BranchOutput {
  return {
    id,
    node_id: "nd_b",
    kind,
    ...(order === undefined ? {} : { order }),
    ...(kind === "else"
      ? {}
      : { condition: { source: "", is_valid: false } as never }),
    rev: 2,
    created_at: at,
    updated_at: at,
  };
}

describe("the commands of a board's connections", () => {
  it("lists a port's connections in the order of their keys, and those of one key by their IDs, without another port's or the trash's", () => {
    const view = docOf(
      [testNode("nd_a", 0, 0), testNode("nd_c", 400, 0)],
      [
        testConnection("cn_3", "nd_a", "nd_c", { order: "V" }),
        testConnection("cn_1", "nd_a", "nd_c", { order: "k" }),
        testConnection("cn_2", "nd_a", "nd_c", { order: "V" }),
        testConnection("cn_4", "nd_a", "nd_c", { from_port: "bo_1" }),
        testConnection("cn_5", "nd_c", "nd_a"),
        testConnection("cn_6", "nd_a", "nd_c", {
          order: "A",
          deleted_at: "2026-01-02T00:00:00Z",
        }),
      ],
    );
    expect(choicesOf(view, "nd_a", "out").map((c) => c.id)).toEqual([
      "cn_2",
      "cn_3",
      "cn_1",
    ]);
    expect(choicesOf(view, "nd_a", "bo_1").map((c) => c.id)).toEqual(["cn_4"]);
  });

  // Verifies: FR-EDIT-03/AC2
  it("places a choice that moves by a key between its new neighbours' alone, and by none where it stays or no key goes between them", () => {
    const siblings = [{ order: "A" }, { order: "V" }, { order: "k" }];
    // Up, before the one that is there: between nothing and A, and between
    // A and V.
    const first = keyAt(siblings, 2, 0);
    expect(first !== undefined && first < "A").toBe(true);
    const second = keyAt(siblings, 2, 1);
    expect(second !== undefined && second > "A" && second < "V").toBe(true);
    // Down, after the one that is there.
    const last = keyAt(siblings, 0, 2);
    expect(last !== undefined && last > "k").toBe(true);
    const middle = keyAt(siblings, 0, 1);
    expect(middle !== undefined && middle > "V" && middle < "k").toBe(true);
    expect(keyAt(siblings, 1, 1)).toBeUndefined();
    expect(keyAt(siblings, 0, -1)).toBeUndefined();
    expect(keyAt(siblings, 2, 3)).toBeUndefined();
    // Between two of the same key there is none, but before both there is.
    const same = [{ order: "V" }, { order: "V" }, { order: "k" }];
    const before = keyAt(same, 2, 1);
    expect(before !== undefined && before < "V").toBe(true);
  });

  it("connects a port to a node, and creates a node and connects it in one batch", () => {
    expect(
      connectCommand({ node: "nd_a", port: "out" }, "nd_c", "cn_new"),
    ).toEqual({
      type: "connection.create",
      payload: {
        id: "cn_new",
        from_node_id: "nd_a",
        from_port: "out",
        to_node_id: "nd_c",
      },
    });
    // Verifies nothing of the server's: the batch is the two commands, the
    // node first, which the connection then enters.
    expect(
      connectNewBatch(
        "bd_a",
        { node: "nd_b", port: "bo_1" },
        "jumper",
        { x: 480, y: 64 },
        { node: "nd_new", connection: "cn_new" },
      ),
    ).toEqual([
      {
        type: "node.create",
        payload: {
          board_id: "bd_a",
          id: "nd_new",
          type: "jumper",
          position: { x: 480, y: 64 },
        },
      },
      {
        type: "connection.create",
        payload: {
          id: "cn_new",
          from_node_id: "nd_b",
          from_port: "bo_1",
          to_node_id: "nd_new",
        },
      },
    ]);
  });

  it("reconnects, reorders, and deletes a connection expecting its revision", () => {
    expect(reconnectCommand("cn_1", { to: "nd_c" }, 4)).toEqual({
      type: "connection.reconnect",
      payload: { id: "cn_1", to_node_id: "nd_c" },
      expected_rev: { cn_1: 4 },
    });
    expect(
      reconnectCommand("cn_1", { from: { node: "nd_b", port: "bo_2" } }, 4),
    ).toEqual({
      type: "connection.reconnect",
      payload: { id: "cn_1", from_node_id: "nd_b", from_port: "bo_2" },
      expected_rev: { cn_1: 4 },
    });
    const connection = testConnection("cn_1", "nd_a", "nd_c", { rev: 3 });
    expect(reorderConnectionCommand(connection, "7V")).toEqual({
      type: "connection.reorder",
      payload: { id: "cn_1", order: "7V" },
      expected_rev: { cn_1: 3 },
    });
    expect(deleteConnectionCommand(connection)).toEqual({
      type: "connection.delete",
      payload: { id: "cn_1" },
      expected_rev: { cn_1: 3 },
    });
  });
});

describe("the commands of a branch's outputs", () => {
  it("orders the conditional outputs without the else", () => {
    const node = testNode("nd_b", 0, 0, {
      type: "branch",
      outputs: [
        output("bo_1", "if", "A"),
        output("bo_2", "elseif", "V"),
        output("bo_3", "else"),
      ],
    });
    expect(conditionalsOf(node).map((o) => o.id)).toEqual(["bo_1", "bo_2"]);
    expect(portsOf(node)).toEqual(["bo_1", "bo_2", "bo_3"]);
    expect(portsOf(testNode("nd_a", 0, 0))).toEqual(["out"]);
    expect(portsOf(testNode("nd_j", 0, 0, { type: "jumper" }))).toEqual([]);
  });

  // Verifies: FR-EDIT-04/AC3
  it("creates an output or the else, reorders one, and deletes one, confirmed where connections leave it", () => {
    expect(createOutputCommand("nd_b", "bo_new", false)).toEqual({
      type: "branch_output.create",
      payload: { id: "bo_new", node_id: "nd_b" },
    });
    expect(createOutputCommand("nd_b", "bo_new", true)).toEqual({
      type: "branch_output.create",
      payload: { id: "bo_new", node_id: "nd_b", else: true },
    });
    const one = output("bo_1", "if", "A");
    expect(reorderOutputCommand(one, "k")).toEqual({
      type: "branch_output.reorder",
      payload: { id: "bo_1", order: "k" },
      expected_rev: { bo_1: 2 },
    });
    expect(deleteOutputCommand(one, false)).toEqual({
      type: "branch_output.delete",
      payload: { id: "bo_1" },
      expected_rev: { bo_1: 2 },
    });
    expect(deleteOutputCommand(one, true)).toEqual({
      type: "branch_output.delete",
      payload: { id: "bo_1", confirm: true },
      expected_rev: { bo_1: 2 },
    });
  });
});

describe("a tap on a connection's line", () => {
  it("measures how far a point is from a curve, or from the straight line that the canvas draws below the zoom that shows text", () => {
    const curve = curveOf({ x: 0, y: 0 }, { x: 400, y: 200 }, false);
    expect(curveDistance(curve, { x: 0, y: 0 })).toBe(0);
    expect(curveDistance(curve, { x: 400, y: 200 })).toBeCloseTo(0, 6);
    // The curve leaves and enters level, so it is far from the middle of
    // the straight line's first half, which that line passes through.
    expect(curveDistance(curve, { x: 100, y: 50 })).toBeGreaterThan(20);
    expect(curveDistance(curve, { x: 100, y: 50 }, true)).toBeCloseTo(0, 6);
    // Beyond its end, the distance is to the end.
    expect(curveDistance(curve, { x: 403, y: 204 }, true)).toBeCloseTo(5, 6);
  });

  it("finds the connection whose line is within the slop, the nearest of several, and none beyond it", () => {
    const scene = new Scene("bd_a");
    scene.sync(
      docOf(
        [
          testNode("nd_1", 0, 0),
          testNode("nd_2", 480, 0),
          testNode("nd_3", 480, 400),
        ],
        [
          testConnection("cn_1", "nd_1", "nd_2"),
          testConnection("cn_2", "nd_1", "nd_3"),
        ],
      ),
    );
    // Both leave the middle of nd_1's right edge, at 240, 64: the level one
    // goes straight to nd_2.
    expect(scene.connectionAt({ x: 360, y: 66 }, 4)).toBe("cn_1");
    expect(scene.connectionAt({ x: 360, y: 90 }, 4)).toBeUndefined();
    // Near where the other enters nd_3, at 480, 464.
    expect(scene.connectionAt({ x: 470, y: 462 }, 6)).toBe("cn_2");
    // Drawn straight, it passes a quarter of the way between its ends,
    // which its curve, which leaves level, does not.
    expect(scene.connectionAt({ x: 300, y: 164 }, 4, true)).toBe("cn_2");
    expect(scene.connectionAt({ x: 300, y: 164 }, 4)).toBeUndefined();
  });
});
