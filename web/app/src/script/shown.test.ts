import type { BranchOutput, Connection, Node } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { testConnection, testNode } from "../test/canvas";
import { leavingByNode, scriptOrder } from "./order";
import { nothingShown, type Placed, placeShown, type Shown } from "./shown";

// Where the script view shows its entries (plan note N75): where the order
// puts them, but an open form's entry, and choice, where the view showed
// them last, whatever moved them, so that the form keeps what was typed.

/** Returns an element of ID id created at the minute minute. */
function node(id: string, minute: number): Node {
  return testNode(id, 0, 0, {
    created_at: `2026-11-01T10:${String(minute).padStart(2, "0")}:00Z`,
  });
}

/**
 * Returns what the view shows of nodes and connections, of the entry node
 * nd_a, with the forms of open, after it showed last, of which creating
 * created.
 */
function place(
  nodes: Node[],
  connections: Connection[],
  open: Record<string, "node" | "choice">,
  last: Shown = nothingShown,
  creating: string[] = [],
): Placed {
  const leaving = leavingByNode(nodes, connections);
  return placeShown(
    scriptOrder(nodes, leaving, "nd_a"),
    leaving,
    new Map(connections.map((c) => [c.id, c])),
    new Map(Object.entries(open).map(([id, kind]) => [id, { kind }])),
    last,
    new Set(creating),
  );
}

/** Returns the IDs of the nodes of each part that placed shows. */
function parts(placed: Placed) {
  return {
    reached: placed.sections.reached.map((n) => n.id),
    unreached: placed.sections.unreached.map((n) => n.id),
  };
}

/** Returns the IDs of the choices that placed shows in the entry of nodeId. */
function choicesIn(placed: Placed, nodeId: string): string[] {
  return (placed.leaving.get(nodeId) ?? []).map((c) => c.id);
}

describe("the script view's places", () => {
  it("is the order, as it is, while no form is open", () => {
    const nodes = [node("nd_a", 0), node("nd_b", 1)];
    const connections = [testConnection("cn_ab", "nd_a", "nd_b")];
    const leaving = leavingByNode(nodes, connections);
    const order = scriptOrder(nodes, leaving, "nd_a");
    const placed = placeShown(
      order,
      leaving,
      new Map(),
      new Map(),
      nothingShown,
      new Set(),
    );
    expect(placed.sections).toBe(order);
    expect(placed.leaving).toBe(leaving);
    expect(placed.ghosts.size).toBe(0);
  });

  it("shows the entries of open forms whose nodes went as ghosts where they were, two of a part as they were among themselves", () => {
    const g1 = node("nd_g1", 1);
    const g2 = node("nd_g2", 2);
    const nodes = [g1, g2, node("nd_c", 3)];
    // Nothing reaches them from nd_a, which is not on the board: all three
    // are of the second part, in the order of their creation.
    const before = place(nodes, [], { nd_g2: "node", nd_g1: "node" });
    expect(parts(before).unreached).toEqual(["nd_g1", "nd_g2", "nd_c"]);
    // Both go, of the form of nd_g2 opened before nd_g1's.
    const after = place(
      [node("nd_c", 3)],
      [],
      { nd_g2: "node", nd_g1: "node" },
      before.shown,
    );
    expect(parts(after).unreached).toEqual(["nd_g1", "nd_g2", "nd_c"]);
    expect([...after.ghosts].sort()).toEqual(["nd_g1", "nd_g2"]);
    expect(after.sections.unreached[0]).toBe(g1);
  });

  it("keeps an open form's entry in its part, as the connections that reach its node go", () => {
    const nodes = [node("nd_a", 0), node("nd_b", 1), node("nd_c", 2)];
    const connections = [
      testConnection("cn_ab", "nd_a", "nd_b"),
      testConnection("cn_ac", "nd_a", "nd_c"),
    ];
    const before = place(nodes, connections, { nd_b: "node" });
    expect(parts(before)).toEqual({
      reached: ["nd_a", "nd_b", "nd_c"],
      unreached: [],
    });
    const after = place(
      nodes,
      [testConnection("cn_ac", "nd_a", "nd_c")],
      { nd_b: "node" },
      before.shown,
    );
    expect(parts(after)).toEqual({
      reached: ["nd_a", "nd_b", "nd_c"],
      unreached: [],
    });
    // Not as a ghost: the board has the node, which its form saves.
    expect(after.ghosts.size).toBe(0);
    // Once the form closes, the entry is where the order puts it.
    expect(
      parts(place(nodes, [testConnection("cn_ac", "nd_a", "nd_c")], {})),
    ).toEqual({ reached: ["nd_a", "nd_c"], unreached: ["nd_b"] });
  });

  it("keeps a choice whose label's form is open in the entry where it was, as a node added after its own reconnects it", () => {
    const nodes = [node("nd_a", 0), node("nd_b", 1)];
    const on = testConnection("cn_on", "nd_a", "nd_b");
    const before = place(nodes, [on], { cn_on: "choice" });
    expect(choicesIn(before, "nd_a")).toEqual(["cn_on"]);
    // "Add node after" nd_a: cn_on leaves the new nd_n, which nd_a leads to.
    const moved = { ...on, from_node_id: "nd_n", rev: 2 };
    const after = place(
      [...nodes, node("nd_n", 3)],
      [moved, testConnection("cn_an", "nd_a", "nd_n", { order: "W" })],
      { cn_on: "choice", nd_n: "node" },
      before.shown,
    );
    expect(choicesIn(after, "nd_a")).toEqual(["cn_on", "cn_an"]);
    expect(choicesIn(after, "nd_n")).toEqual([]);
    // The choice as the board has it now, which its form saves, no ghost.
    expect(after.leaving.get("nd_a")?.[0]).toBe(moved);
    expect(after.ghosts.size).toBe(0);
  });

  it("keeps a branch's choice whose label's form is open under the output where it was, as another session reconnects it to another", () => {
    const output = (id: string, kind: BranchOutput["kind"]): BranchOutput => ({
      id,
      node_id: "nd_a",
      kind,
      rev: 1,
      created_at: "2026-11-01T10:00:00Z",
      updated_at: "2026-11-01T10:00:00Z",
    });
    const branch = testNode("nd_a", 0, 0, {
      type: "branch",
      outputs: [output("bo_if", "if"), output("bo_else", "else")],
    });
    const nodes = [branch, node("nd_b", 1)];
    const choice = testConnection("cn_1", "nd_a", "nd_b", {
      from_port: "bo_if",
    });
    const before = place(nodes, [choice], { cn_1: "choice" });
    const moved = { ...choice, from_port: "bo_else", rev: 2 };
    const after = place(nodes, [moved], { cn_1: "choice" }, before.shown);
    // Under the if, as the view listed it, but as the board has it now.
    const shown = after.leaving.get("nd_a")?.[0];
    expect(shown).toEqual({ ...moved, from_port: "bo_if" });
    expect(after.ghosts.size).toBe(0);
    // And there still as it is shown anew.
    const again = place(nodes, [moved], { cn_1: "choice" }, after.shown);
    expect(again.leaving.get("nd_a")?.[0]?.from_port).toBe("bo_if");
    // Once the form closes, it is under the output that it leaves.
    expect(place(nodes, [moved], {}).leaving.get("nd_a")?.[0]).toBe(moved);
  });

  it("shows a choice whose label's form is open, and whose connection went, as a ghost where it was", () => {
    const nodes = [node("nd_a", 0), node("nd_b", 1), node("nd_c", 2)];
    const first = testConnection("cn_1", "nd_a", "nd_b", { order: "U" });
    const second = testConnection("cn_2", "nd_a", "nd_c", { order: "V" });
    const before = place(nodes, [first, second], { cn_1: "choice" });
    const after = place(nodes, [second], { cn_1: "choice" }, before.shown);
    expect(choicesIn(after, "nd_a")).toEqual(["cn_1", "cn_2"]);
    expect(after.leaving.get("nd_a")?.[0]).toBe(first);
    expect([...after.ghosts]).toEqual(["cn_1"]);
  });

  it("shows no ghost of what an addition under way created, as its refused batch takes it from the board", () => {
    const nodes = [node("nd_a", 0)];
    const added = node("nd_n", 1);
    const choice = testConnection("cn_n", "nd_a", "nd_n");
    const before = place(
      [...nodes, added],
      [choice],
      { nd_n: "node", cn_n: "choice" },
      nothingShown,
      ["nd_n", "cn_n"],
    );
    expect(parts(before).reached).toEqual(["nd_a", "nd_n"]);
    const after = place(
      nodes,
      [],
      { nd_n: "node", cn_n: "choice" },
      before.shown,
      ["nd_n", "cn_n"],
    );
    expect(parts(after).reached).toEqual(["nd_a"]);
    expect(choicesIn(after, "nd_a")).toEqual([]);
    expect(after.ghosts.size).toBe(0);
  });
});
