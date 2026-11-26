import { describe, expect, test } from "vitest";
import { World, withInline } from "../test/analysis";
import { expression, richText } from "../test/play";
import { contributionsOf, graphOf } from "./graph";

// A world of three boards: main, a, and b. a's start writes gold twice
// over, at its on-enter script and at a choice's on-traverse script, and
// Elara's trust; b's text shows gold, its choice reads gold and whether a's
// start was visited, and a jumper of b leads to a's start. Elara's mood,
// written on a, and Mira's trust, read on b, are no dependency of b on a's
// writes of Elara's trust (FR-DEP-01 AC4).
function world() {
  const w = new World();
  const a = w.board("A");
  const b = w.board("B");
  const cast = w.componentType("cast", [
    { key: "trust", type: "int", runtime: true },
    { key: "mood", type: "int", runtime: true },
  ]);
  const [trust, mood] = cast.attributes;
  const elara = w.component(cast.id, "elara");
  const mira = w.component(cast.id, "mira");
  const gold = w.variable(false, false, { key: "gold" });
  const aStart = w.element(a, {
    title: "A start",
    on_enter: expression("gold = 1; elara.trust = 2; elara.mood = 3"),
  });
  const aEnd = w.element(a, { is_ending: true });
  const aChoice = w.connect(aStart, aEnd, {
    on_traverse: expression("gold += 1"),
  });
  const bStart = w.element(b, {
    title: "B start",
    text: richText({ type: "valueRef", attrs: { ref: gold } }),
  });
  const bEnd = w.element(b, { is_ending: true });
  const bChoice = w.connect(bStart, bEnd, {
    condition: expression(
      `gold > 1 and mira.trust > 1 and visited(node("${aStart}"))`,
    ),
  });
  const jumper = w.jumper(b, aStart);
  w.connect(bStart, jumper);
  // A site of a's own that reads gold is no edge of a to itself.
  w.nodeOf(aEnd).text = withInline(w.nodeOf(aEnd).text, {
    type: "valueRef",
    attrs: { ref: gold },
  });
  return {
    w,
    a,
    b,
    trust,
    mood,
    elara,
    mira,
    gold,
    aStart,
    aChoice,
    bStart,
    bChoice,
    jumper,
  };
}

describe("the graph", () => {
  // Verifies: FR-DEP-01/AC4
  test("has an edge of each kind between two boards, counting its contributions", () => {
    const { w, a, b } = world();
    const { draft, index } = w.analyse();
    const graph = graphOf(draft, index);
    expect(graph.seq).toBe(draft.seq);
    expect(graph.edges).toEqual([
      // Two sites of a write gold, which two of b read; a's write of
      // Elara's trust is read nowhere.
      { from_board_id: a, to_board_id: b, kind: "variable", contributions: 4 },
      { from_board_id: a, to_board_id: b, kind: "visit", contributions: 1 },
      { from_board_id: b, to_board_id: a, kind: "jumper", contributions: 1 },
    ]);
    expect(graph.boards).toEqual([
      { id: w.main, name: w.boardOf(w.main).name, inbound: 0, outbound: 0 },
      { id: a, name: w.boardOf(a).name, inbound: 1, outbound: 1 },
      { id: b, name: w.boardOf(b).name, inbound: 1, outbound: 1 },
    ]);
  });

  test("has an edge of an attribute of one component and one definition alone", () => {
    const { w, a, b, trust, elara } = world();
    // b reads Elara's trust too.
    const bStart = w.nodes.find((n) => n.title === "B start");
    if (bStart === undefined) {
      throw new Error("no B start");
    }
    bStart.on_exit = expression("gold = elara.trust");
    const { draft, index } = w.analyse();
    const graph = graphOf(draft, index, { kind: "attribute" });
    expect(graph.edges).toEqual([
      { from_board_id: a, to_board_id: b, kind: "attribute", contributions: 1 },
    ]);
    expect(contributionsOf(draft, index, a, b, "attribute")).toEqual([
      {
        component_id: elara,
        attribute_id: trust,
        from: {
          site: "node_on_enter",
          node_id: w.nodes.find((n) => n.title === "A start")?.id,
          node_title: "A start",
        },
        to: { site: "node_on_exit", node_id: bStart.id, node_title: "B start" },
      },
    ]);
    // And b now writes gold too, which a reads at its end's text and at
    // its choice's update, which reads what it updates.
    expect(graphOf(draft, index, { kind: "variable" }).edges).toEqual([
      { from_board_id: a, to_board_id: b, kind: "variable", contributions: 4 },
      { from_board_id: b, to_board_id: a, kind: "variable", contributions: 2 },
    ]);
  });

  test("keeps the edges and the contributions that its filters keep", () => {
    const { w, a, b, gold, elara, trust } = world();
    const { draft, index } = w.analyse();
    const kinds = (f: Parameters<typeof graphOf>[2]) =>
      graphOf(draft, index, f).edges.map((e) => e.kind);
    expect(kinds({ kind: "jumper" })).toEqual(["jumper"]);
    expect(kinds({ variable: gold })).toEqual(["variable"]);
    expect(kinds({ variable: gold, kind: "jumper" })).toEqual([]);
    expect(kinds({ variable: w.id("var") })).toEqual([]);
    expect(kinds({ component: elara })).toEqual([]);
    expect(kinds({ attribute: trust })).toEqual([]);
    expect(kinds({ board: w.main })).toEqual([]);
    expect(kinds({ board: b })).toEqual(["variable", "visit", "jumper"]);
    // A board's badge counts the edges that the filters keep.
    expect(
      graphOf(draft, index, { kind: "jumper" }).boards.map((x) => [
        x.inbound,
        x.outbound,
      ]),
    ).toEqual([
      [0, 0],
      [1, 0],
      [0, 1],
    ]);
    expect(
      contributionsOf(draft, index, a, b, "variable", {
        variable: w.id("var"),
      }),
    ).toEqual([]);
  });

  // Verifies: FR-DEP-02/AC2
  test("lists the contributions of each kind of edge", () => {
    const { w, a, b, gold, aStart, aChoice, bStart, bChoice, jumper } = world();
    const { draft, index } = w.analyse();
    const aEnd = {
      site: "node_on_enter",
      node_id: aStart,
      node_title: "A start",
    } as const;
    const aTraverse = {
      site: "connection_on_traverse",
      node_id: aStart,
      node_title: "A start",
      connection_id: aChoice,
    } as const;
    const bText = {
      site: "node_text",
      node_id: bStart,
      node_title: "B start",
    } as const;
    const bCondition = {
      site: "connection_condition",
      node_id: bStart,
      node_title: "B start",
      connection_id: bChoice,
    } as const;
    expect(contributionsOf(draft, index, a, b, "variable")).toEqual([
      { variable_id: gold, from: aEnd, to: bText },
      { variable_id: gold, from: aEnd, to: bCondition },
      { variable_id: gold, from: aTraverse, to: bText },
      { variable_id: gold, from: aTraverse, to: bCondition },
    ]);
    expect(contributionsOf(draft, index, a, b, "visit")).toEqual([
      {
        from: { site: "node", node_id: aStart, node_title: "A start" },
        to: bCondition,
      },
    ]);
    expect(contributionsOf(draft, index, b, a, "jumper")).toEqual([
      {
        from: { site: "node", node_id: jumper, node_title: "" },
        to: { site: "node", node_id: aStart, node_title: "A start" },
      },
    ]);
    // None of a board to itself, and none of a board that the draft does
    // not hold.
    expect(contributionsOf(draft, index, a, a, "variable")).toEqual([]);
    expect(
      contributionsOf(draft, index, a, w.id("bd"), "variable"),
    ).toBeUndefined();
  });

  test("leaves out what is in the trash", () => {
    const { w, a, b, aStart } = world();
    // a's start, which writes gold, and whose choice does too, which is
    // visited, and which is the jumper's target, goes to the trash: b's
    // condition, which names it, no longer compiles.
    w.nodeOf(aStart).deleted_at = "2026-11-02T10:00:00Z";
    const { draft, index } = w.analyse();
    expect(graphOf(draft, index).edges).toEqual([]);
    expect(contributionsOf(draft, index, b, a, "jumper")).toEqual([]);
    expect(contributionsOf(draft, index, a, b, "variable")).toEqual([]);
  });
});
