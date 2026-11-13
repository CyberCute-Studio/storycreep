import { describe, expect, it } from "vitest";
import { editOf, offersAt, type Spot, spotAt } from "./complete";
import { englishWords, harbourScope, ids } from "./testdata";

// What the expression editor offers to complete at the caret (PRD
// FR-EXPR-01 AC2, plan note N74): only what is valid where the source is,
// at its board, each as the board's expressions write it.

const scope = harbourScope();
const words = englishWords();

/** Returns the spot at caret of source, which the test expects there. */
function spotOf(source: string, caret: number): Spot {
  const spot = spotAt(source, caret);
  if (spot === undefined) {
    throw new Error(`no spot at ${caret} of ${source}`);
  }
  return spot;
}

/** Returns the labels of what the editor offers at the end of source, of a source of context. */
function offered(
  source: string,
  context: "condition" | "script" = "condition",
) {
  const spot = spotAt(source, source.length);
  if (spot === undefined) {
    return undefined;
  }
  return offersAt(spot, context, scope.view, scope.boardId, words).offers.map(
    (o) => o.label,
  );
}

describe("where the editor offers to complete", () => {
  it('offers at a name, after global., board., cmp., and a component\'s key, and within node("…")', () => {
    expect(spotAt("go", 2)).toMatchObject({
      kind: "name",
      query: "go",
      from: 0,
      to: 2,
    });
    expect(spotAt("global.go", 9)).toMatchObject({
      kind: "global",
      query: "go",
      from: 7,
    });
    expect(spotAt("board.", 6)).toMatchObject({ kind: "board", query: "" });
    expect(spotAt("cmp.el", 6)).toMatchObject({ kind: "cmp", query: "el" });
    expect(spotAt("elara.", 6)).toMatchObject({
      kind: "member",
      component: { key: "elara", explicit: false },
    });
    expect(spotAt("cmp.elara.t", 11)).toMatchObject({
      kind: "member",
      component: { key: "elara", explicit: true },
      query: "t",
    });
    expect(spotAt('visited(node("Qu', 16)).toMatchObject({
      kind: "node",
      query: "Qu",
      from: 13,
    });
    // The name after the caret is replaced too.
    expect(spotAt("goXld > 1", 2)).toMatchObject({ from: 0, to: 5 });
  });

  it("offers nothing within a string, a comment, or a number, nor where nothing is typed", () => {
    expect(spotAt('"go', 3)).toBeUndefined();
    expect(spotAt("gold // go", 10)).toBeUndefined();
    expect(spotAt("3.5", 3)).toBeUndefined();
    expect(spotAt("gold > ", 7)).toBeUndefined();
    expect(spotAt("", 0)).toBeUndefined();
  });
});

describe("what the editor offers", () => {
  // Verifies: FR-EXPR-01/AC2
  it("offers only the names valid at the board: its own variables and the global ones, and the runtime attributes, but nothing in the trash", () => {
    // Harbour's own trust, and the global one written as global.trust, as
    // the bare key names Harbour's.
    expect(offered("tr")).toEqual(["global.trust", "trim(s)", "trust"]);
    // Lighthouse's lamp is not Harbour's to read, nor is binned, in the
    // trash.
    expect(offered("la")).toEqual([]);
    expect(offered("bi")).toEqual([]);
    expect(
      offersAt(
        spotOf("la", 2),
        "condition",
        scope.view,
        ids.lighthouse,
        words,
      ).offers.map((o) => o.label),
    ).toEqual(["lamp"]);
    // Elara's runtime attributes, but not her notes, which are not.
    expect(offered("elara.")).toEqual(["elara.mood", "elara.trust"]);
    expect(offered("board.")).toEqual(["board.trust"]);
    expect(offered("global.t")).toEqual(["global.trust"]);
  });

  it("offers the functions that a condition may call, and signal only in a script", () => {
    expect(offered("si")).toEqual([]);
    expect(offered("si", "script")).toEqual(["signal(name[, value])"]);
    expect(offered("vis")).toEqual(["visited(node)", "visits(node)"]);
  });

  it("tells of each offer's type and what it is", () => {
    const spot = spotOf("met", 3);
    const [offer] = offersAt(
      spot,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers;
    expect(offer).toMatchObject({
      label: "met_elara",
      detail: "Global variable, true or false",
      description: "Whether the player has met Elara.",
      variable: ids.metElara,
    });
    const [fn] = offersAt(
      spotOf("cla", 3),
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers;
    expect(fn).toMatchObject({
      label: "clamp(x, lo, hi)",
      detail: "Function, gives number",
    });
  });

  it('offers the nodes that the page has read within node("…"), by their titles, but no jumper', () => {
    expect(offered('visited(node("')).toEqual(["Quay", "Tower"]);
    expect(offered('visited(node("tow')).toEqual(["Tower"]);
  });

  it("writes what is chosen in place of what was typed of it", () => {
    const source = "go > 1";
    const spot = spotOf(source, 2);
    const [gold] = offersAt(
      spot,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers;
    expect(editOf(source, spot, gold)).toEqual({
      from: 0,
      to: 2,
      text: "gold",
      caret: 4,
    });
    // A function, with its (, or after the ( that the source has already.
    const call = spotOf("cla", 3);
    const [clamp] = offersAt(
      call,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers;
    expect(editOf("cla", call, clamp)).toMatchObject({
      text: "clamp(",
      caret: 6,
    });
    expect(editOf("cla(x)", spotOf("cla(x)", 3), clamp)).toMatchObject({
      text: "clamp",
      caret: 6,
    });
    // A node, as its reference.
    const inNode = 'visited(node("Qu")) and gold';
    const at = spotOf(inNode, 16);
    const [quay] = offersAt(
      at,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers;
    const edit = editOf(inNode, at, quay);
    expect(inNode.slice(0, edit.from) + edit.text + inNode.slice(edit.to)).toBe(
      `visited(node("${ids.quay}")) and gold`,
    );
  });
});
