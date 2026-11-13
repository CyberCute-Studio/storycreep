import { describe, expect, it } from "vitest";
import { editOf, offersAt, type Spot, spotAt, typedWhole } from "./complete";
import {
  elara,
  englishWords,
  harbourScope,
  ids,
  testSources,
  variable,
  variables,
} from "./testdata";

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
    // A \ before a line break escapes none: the string ends there, as the
    // lexer reads it, and the next line's node("…") is one.
    const script = 's = "a\\\ny = node("Qu';
    expect(spotAt(script, script.length)).toMatchObject({
      kind: "node",
      query: "Qu",
      from: 17,
    });
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

  it("offers a component by its name too, bare as after cmp.", () => {
    const npc = harbourScope(ids.harbour, {
      components: new Map([[ids.elara, { ...elara, key: "npc_1" }]]),
    });
    for (const source of ["ela", "cmp.ela"]) {
      expect(
        offersAt(
          spotOf(source, source.length),
          "condition",
          npc.view,
          npc.boardId,
          words,
        ).offers.map((o) => o.label),
      ).toEqual([source === "ela" ? "npc_1." : "cmp.npc_1."]);
    }
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
    // A function, with its ( at the end of its line, and its name alone
    // wherever anything follows it, as the ( that the source has already,
    // after spaces too, which the language reads as the call's, or a ).
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
    expect(editOf("cla\nx", spotOf("cla\nx", 3), clamp)).toMatchObject({
      text: "clamp(",
      caret: 6,
    });
    // Before spaces alone at the end of its line, as at its end.
    expect(editOf("cla \t\nx", spotOf("cla \t\nx", 3), clamp)).toMatchObject({
      text: "clamp(",
      caret: 6,
    });
    expect(editOf("cla(x)", spotOf("cla(x)", 3), clamp)).toMatchObject({
      text: "clamp",
      caret: 5,
    });
    expect(editOf("cla (x)", spotOf("cla (x)", 3), clamp)).toMatchObject({
      from: 0,
      to: 3,
      text: "clamp",
      caret: 5,
    });
    expect(editOf("abs(cla)", spotOf("abs(cla)", 7), clamp)).toMatchObject({
      from: 4,
      to: 7,
      text: "clamp",
      caret: 9,
    });
    // A component, with its dot at the end of its line, and its key alone
    // wherever anything follows it, as the dot of the attribute after it.
    const member = spotOf("ela.trust", 3);
    const component = offersAt(
      member,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers.find((o) => o.kind === "component");
    if (component === undefined) {
      throw new Error("no component offered of ela");
    }
    expect(editOf("ela", spotOf("ela", 3), component)).toMatchObject({
      text: "elara.",
      caret: 6,
    });
    expect(editOf("ela.trust", member, component)).toMatchObject({
      from: 0,
      to: 3,
      text: "elara",
      caret: 5,
    });
    expect(
      editOf("ela .trust", spotOf("ela .trust", 3), component),
    ).toMatchObject({ text: "elara", caret: 5 });
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
    // Of a string that no parenthesis closes, the spaces after its quote
    // stay.
    const open = 'visited(node("Qu" and gold';
    const unclosed = spotOf(open, 16);
    const toQuay = editOf(open, unclosed, quay);
    expect(
      open.slice(0, toQuay.from) + toQuay.text + open.slice(toQuay.to),
    ).toBe(`visited(node("${ids.quay}") and gold`);
    // Of a string that no quote closes, to the end of its line, where
    // nothing follows it; and of one that holds anything but a node ID's
    // characters, nothing, as the ) after node(" chosen within visited()
    // could be node's too, and the rest of a title after the caret is no
    // node's ID.
    const ended = 'visited(node("Qu';
    const toEnd = editOf(ended, spotOf(ended, 16), quay);
    expect(
      ended.slice(0, toEnd.from) + toEnd.text + ended.slice(toEnd.to),
    ).toBe(`visited(node("${ids.quay}")`);
    expect(spotAt('visited(node("Qu) and gold', 16)).toBeUndefined();
    expect(spotAt('node("Qu)', 8)).toBeUndefined();
    expect(spotAt('visited(node("Quay Inn"))', 18)).toBeUndefined();
    expect(spotAt('visited(node("Quay Inn"))', 23)).toBeUndefined();
    // What is left of a node's ID after the caret is replaced with it.
    const within = `visited(node("Qu${ids.quay}"))`;
    const toWithin = editOf(within, spotOf(within, 16), quay);
    expect(
      within.slice(0, toWithin.from) +
        toWithin.text +
        within.slice(toWithin.to),
    ).toBe(`visited(node("${ids.quay}"))`);
    // node("…"), where anything follows the name, as the ( and the string
    // that the source has already, or the ) of what it is within, is node
    // alone, rather than a quote that no quote closes.
    const retyped = `visited(node("${ids.quay}"))`;
    const name = spotOf(retyped, 12);
    const nodeCall = offersAt(
      name,
      "condition",
      scope.view,
      scope.boardId,
      words,
    ).offers.find((o) => o.id === "fn:node");
    if (nodeCall === undefined) {
      throw new Error("no node offered of node");
    }
    expect(editOf(retyped, name, nodeCall)).toEqual({
      from: 8,
      to: 12,
      text: "node",
      caret: 12,
    });
    expect(editOf("visited(no)", spotOf("visited(no)", 10), nodeCall)).toEqual({
      from: 8,
      to: 10,
      text: "node",
      caret: 12,
    });
    expect(
      editOf("no and gold", spotOf("no and gold", 2), nodeCall),
    ).toMatchObject({ text: "node", caret: 4 });
    // And with its ( and quote at the end of its line, whose nodes the list
    // offers then.
    expect(editOf("visited(no", spotOf("visited(no", 10), nodeCall)).toEqual({
      from: 8,
      to: 10,
      text: 'node("',
      caret: 14,
    });
    // Before spaces alone at the end of its line too, after which the
    // nodes are offered, and the chosen one written before the spaces.
    expect(
      editOf("visited(no  ", spotOf("visited(no  ", 10), nodeCall),
    ).toMatchObject({ text: 'node("', caret: 14 });
    const spaced = 'visited(node("  ';
    const before = spotOf(spaced, 14);
    expect(before).toMatchObject({ kind: "node", from: 13, to: 14 });
    const toSpaced = editOf(spaced, before, quay);
    expect(
      spaced.slice(0, toSpaced.from) +
        toSpaced.text +
        spaced.slice(toSpaced.to),
    ).toBe(`visited(node("${ids.quay}")  `);
  });

  it("has nothing to complete of a name typed whole, or of a word of the language, whatever starts as it", () => {
    // A variable whose key starts as true does.
    const view = testSources({
      variables: new Map(
        [
          ...variables,
          variable("var_01HQ8Z3XK4F7NAWR5TCQ9M2CT1", "true_ending", {
            type: "bool",
          }),
        ].map((v) => [v.id, v]),
      ),
    });
    const whole = (source: string) => {
      const spot = spotOf(source, source.length);
      const { offers } = offersAt(spot, "condition", view, ids.harbour, words);
      expect(offers.length).toBeGreaterThan(0);
      return typedWhole(source, spot, offers);
    };
    expect(whole("met_elara == tru")).toBe(false);
    expect(whole("met_elara == true")).toBe(true);
    expect(whole("met_elara == true_ending")).toBe(true);
    // A board's trust, whose global.trust the list has first.
    expect(whole("trust")).toBe(true);
    expect(whole("trus")).toBe(false);
  });

  it("offers a node by the start of its ID, in any case, as the parser reads it", () => {
    const source = `node("${ids.quay.slice(0, 10).toLowerCase()}`;
    const spot = spotOf(source, source.length);
    expect(
      offersAt(spot, "condition", scope.view, scope.boardId, words).offers.map(
        (o) => o.id,
      ),
    ).toContain(ids.quay);
  });
});
