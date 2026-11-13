import type { BranchOutput, Connection, Node } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { testConnection, testNode } from "../test/canvas";
import { leavingInOrder, scriptOrder } from "./order";

// The order of a board's script view (PRD FR-TEXT-04 AC2): breadth-first
// from the board's entry node, along the connections in the order of each
// node's ports and choices, then the nodes that it does not reach, in the
// order that they were created.

/** Returns an element of ID id created at the minute minute. */
function node(id: string, minute = 0, extra: Partial<Node> = {}): Node {
  return testNode(id, 0, 0, {
    created_at: `2026-11-01T10:${String(minute).padStart(2, "0")}:00Z`,
    ...extra,
  });
}

/** Returns a connection from the port of from to to, at the key order. */
function link(
  id: string,
  from: string,
  to: string,
  order = "V",
  port = "out",
): Connection {
  return testConnection(id, from, to, { order, from_port: port });
}

/** Returns an output of the branch nd_branch, of kind, at the key order of a conditional one. */
function output(
  id: string,
  kind: BranchOutput["kind"],
  order?: string,
): BranchOutput {
  return {
    id,
    node_id: "nd_branch",
    kind,
    ...(order === undefined ? {} : { order }),
    rev: 1,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** Returns the IDs of the nodes of order, reached and then not. */
function ids(order: ReturnType<typeof scriptOrder>) {
  return {
    reached: order.reached.map((n) => n.id),
    unreached: order.unreached.map((n) => n.id),
  };
}

describe("the script view's order", () => {
  // Verifies: FR-TEXT-04/AC2
  it("lists the nodes breadth-first from the entry node, each node's choices in their order, and the rest in the order of their creation", () => {
    const nodes = [
      node("nd_entry", 5),
      node("nd_a", 4),
      node("nd_b", 3),
      node("nd_c", 2),
      node("nd_d", 1),
      node("nd_lost2", 7),
      node("nd_lost1", 6),
    ];
    const connections = [
      // The entry's second choice, by its key, then its first.
      link("cn_2", "nd_entry", "nd_b", "W"),
      link("cn_1", "nd_entry", "nd_a", "U"),
      link("cn_3", "nd_a", "nd_d"),
      link("cn_4", "nd_b", "nd_c"),
      // Back to the entry: a cycle, which lists no node twice.
      link("cn_5", "nd_d", "nd_entry"),
      link("cn_6", "nd_lost1", "nd_entry"),
    ];
    expect(ids(scriptOrder(nodes, connections, "nd_entry"))).toEqual({
      reached: ["nd_entry", "nd_a", "nd_b", "nd_d", "nd_c"],
      unreached: ["nd_lost1", "nd_lost2"],
    });
  });

  // Verifies: FR-TEXT-04/AC2
  it("is the same order whatever the order of the nodes and connections it is given", () => {
    const nodes = [
      node("nd_entry", 0),
      node("nd_x", 1),
      node("nd_y", 1),
      node("nd_z", 1),
      node("nd_q", 1),
    ];
    const connections = [
      link("cn_b", "nd_entry", "nd_y", "V"),
      link("cn_a", "nd_entry", "nd_x", "V"),
      link("cn_c", "nd_entry", "nd_z", "a"),
    ];
    const expected = {
      // Choices of one key are in the order of their IDs.
      reached: ["nd_entry", "nd_x", "nd_y", "nd_z"],
      unreached: ["nd_q"],
    };
    expect(ids(scriptOrder(nodes, connections, "nd_entry"))).toEqual(expected);
    expect(
      ids(
        scriptOrder(
          [...nodes].reverse(),
          [...connections].reverse(),
          "nd_entry",
        ),
      ),
    ).toEqual(expected);
  });

  // Verifies: FR-TEXT-04/AC2
  it("follows a branch's outputs in their order, its if, its else ifs, and its else, and a connection of an output it no longer has after them", () => {
    const branch = node("nd_branch", 0, {
      type: "branch",
      kind: undefined,
      text: undefined,
      outputs: [
        output("bo_if", "if", "V"),
        output("bo_elseif", "elseif", "W"),
        output("bo_else", "else"),
      ],
    });
    const nodes = [
      branch,
      node("nd_else", 1),
      node("nd_if", 2),
      node("nd_elseif", 3),
      node("nd_astray", 4),
    ];
    const connections = [
      link("cn_else", "nd_branch", "nd_else", "V", "bo_else"),
      link("cn_astray", "nd_branch", "nd_astray", "V", "bo_trashed"),
      link("cn_elseif", "nd_branch", "nd_elseif", "V", "bo_elseif"),
      link("cn_if", "nd_branch", "nd_if", "V", "bo_if"),
    ];
    expect(ids(scriptOrder(nodes, connections, "nd_branch")).reached).toEqual([
      "nd_branch",
      "nd_if",
      "nd_elseif",
      "nd_else",
      "nd_astray",
    ]);
    expect(leavingInOrder(branch, connections).map((c) => c.id)).toEqual([
      "cn_if",
      "cn_elseif",
      "cn_else",
      "cn_astray",
    ]);
  });

  // Verifies: FR-TEXT-04/AC2
  it("does not follow a jumper's target, which is no connection", () => {
    const nodes = [
      node("nd_entry", 0),
      node("nd_jump", 1, {
        type: "jumper",
        kind: undefined,
        text: undefined,
        target: { board_id: "bd_a", node_id: "nd_far" },
      }),
      node("nd_far", 2),
    ];
    expect(
      ids(
        scriptOrder(nodes, [link("cn_1", "nd_entry", "nd_jump")], "nd_entry"),
      ),
    ).toEqual({ reached: ["nd_entry", "nd_jump"], unreached: ["nd_far"] });
  });

  // Verifies: FR-TEXT-04/AC2
  it("lists every node in the order of its creation, and of one time by its ID, on a board without an entry node, or whose entry node is not on it", () => {
    const nodes = [node("nd_b", 1), node("nd_c", 0), node("nd_a", 1)];
    const connections = [link("cn_1", "nd_c", "nd_b")];
    const expected = { reached: [], unreached: ["nd_c", "nd_a", "nd_b"] };
    expect(ids(scriptOrder(nodes, connections, undefined))).toEqual(expected);
    expect(ids(scriptOrder(nodes, connections, "nd_trashed"))).toEqual(
      expected,
    );
  });
});
