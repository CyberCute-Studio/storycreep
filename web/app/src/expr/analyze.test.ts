import { describe, expect, it } from "vitest";
import { analyze, runsOf, unitRange, unitsOf } from "./analyze";
import { errorText, placeText, signatureOf } from "./messages";
import { englishWords, harbourScope, ids } from "./testdata";

// What the expression editor makes of a source as it is typed (PRD
// FR-EXPR-01 AC1, AC2, FR-EXPR-02, FR-VAR-02 AC1, plan note N74): its AST,
// checked against what Harbour's expressions name, or its first error at
// the range of characters that the parser or the checker gives it, which
// the editor marks in UTF-16 code units and says in words; and the tints
// of its tokens.

const scope = harbourScope();
const words = englishWords();

/** Returns the text of the source that the error of analysis covers, as the editor marks it. */
function marked(source: string) {
  const a = analyze(source, "condition", scope);
  if (a.error === undefined) {
    return undefined;
  }
  const { from, to } = unitRange(a.units, a.error);
  return { code: a.error.code, text: source.slice(from, to), a };
}

describe("an expression's analysis", () => {
  it("counts a source's characters in code points, as the language does, and converts them to UTF-16", () => {
    expect(unitsOf("a😀b")).toEqual([0, 1, 3, 4]);
    expect(unitsOf("")).toEqual([0]);
    // A lone surrogate is a character of its own, of one code unit.
    expect(unitsOf("a\uD800b")).toEqual([0, 1, 2, 3]);
    expect(unitRange(unitsOf("a😀b"), { start: 1, end: 2 })).toEqual({
      from: 1,
      to: 3,
    });
  });

  // Verifies: FR-EXPR-01/AC1
  it("gives each error the exact range of characters that the parser or the checker gives it", () => {
    // The checker's: an unknown name, with the near match that it suggests.
    const unknown = marked("gld > 1");
    expect(unknown).toMatchObject({ code: "unknown_identifier", text: "gld" });
    expect(unknown?.a.error?.suggestions).toEqual(["gold"]);
    expect(errorText(words, "gld > 1", unknown?.a.error ?? { code: "", start: 0, end: 0 })).toBe(
      "No variable or component has this name here. (characters 1 to 3) Did you mean gold?",
    );
    // After a string of a character beyond the Basic Multilingual Plane,
    // which is one code point and two code units.
    const typed = marked('keeper == "😀" and gold');
    expect(typed).toMatchObject({ code: "type_mismatch", text: "gold" });
    expect(typed?.a.error).toMatchObject({ start: 18, end: 22 });
    // The parser's: an operator that nothing follows, at the end.
    const end = marked("gold >=");
    expect(end).toMatchObject({ code: "unexpected_end", text: "" });
    expect(placeText(words.t, "gold >=", { code: "unexpected_end", start: 7, end: 7 })).toBe(
      "at the end",
    );
    // And an assignment in a condition.
    expect(marked("gold = 3")).toMatchObject({
      code: "condition_must_be_pure",
      text: "=",
    });
  });

  it("says where an error of a script of several lines is, by its line", () => {
    const source = "gold += 1\ngold += oops";
    const a = analyze(source, "script", scope);
    expect(a.error?.code).toBe("unknown_identifier");
    expect(placeText(words.t, source, a.error ?? { code: "", start: 0, end: 0 })).toBe(
      "line 2, characters 9 to 12",
    );
  });

  it("checks a source against its board: a variable of another board is out of its scope", () => {
    expect(marked("lamp > 1")).toMatchObject({
      code: "board_variable_out_of_scope",
      text: "lamp",
    });
    expect(analyze("lamp > 1", "condition", harbourScope(ids.lighthouse)).ast).toBeDefined();
    // One in the trash is no variable.
    expect(marked("binned > 1")?.code).toBe("unknown_identifier");
    // Nor an attribute that is not runtime.
    expect(marked('elara.notes == "x"')?.code).toBe("attribute_not_runtime");
  });

  // Verifies: FR-VAR-02/AC1
  it("resolves a bare key that a board's variable and a global one share to the board's", () => {
    const a = analyze("trust >= 3", "condition", scope);
    expect(a.ast).toMatchObject({
      kind: "binary",
      left: { kind: "var", ref: ids.trustBoard },
    });
    expect(
      analyze("global.trust >= 3", "condition", scope).ast,
    ).toMatchObject({ left: { kind: "var", ref: ids.trustGlobal } });
  });

  it("looks the nodes of node references up in the document, and leaves one of a board that the page has not read to the server", () => {
    expect(analyze(`visited(node("${ids.quay}"))`, "condition", scope).ast).toBeDefined();
    expect(
      marked(`visited(node("${ids.onward}"))`)?.code,
    ).toBe("node_ref_jumper");
    const unread = "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BF9";
    expect(analyze(`visited(node("${unread}"))`, "condition", scope).ast).toBeDefined();
    // One that the document knows went is unknown, as the server finds it.
    const gone = harbourScope(ids.harbour, {
      gone: new Map([[unread, { rev: 2, seq: 3 }]]),
    });
    expect(analyze(`visited(node("${unread}"))`, "condition", gone).error?.code).toBe(
      "unknown_node",
    );
  });

  it("checks the syntax alone where the document holds what no project has", () => {
    const broken = harbourScope(ids.harbour, {
      components: new Map([
        ["cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BC1", { ...harbourScope().view.components.get(ids.elara)!, key: "trust" }],
        ["cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BC2", { ...harbourScope().view.components.get(ids.elara)!, id: "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BC2", key: "trust" }],
      ]),
    });
    expect(broken.project).toBeUndefined();
    const a = analyze("nobody > 1", "condition", broken);
    expect(a.checked).toBe(false);
    expect(a.error).toBeUndefined();
    expect(analyze("gold >", "condition", broken).error?.code).toBe("unexpected_end");
  });
});

describe("an expression's tints", () => {
  it("colours keywords, literals, strings, comments, the names that resolve, and the functions called", () => {
    const source = `met_elara and visits(node("${ids.quay}")) > 2 // note`;
    const a = analyze(source, "condition", scope);
    const tints = runsOf(a).map((r) => [source.slice(r.from, r.to), r.tint]);
    expect(tints).toContainEqual(["met_elara", "name"]);
    expect(tints).toContainEqual(["and", "keyword"]);
    expect(tints).toContainEqual(["visits", "function"]);
    expect(tints).toContainEqual(["2", "number"]);
    expect(tints).toContainEqual(["// note", "comment"]);
    // The runs cover the source, in order.
    expect(runsOf(a).map((r) => source.slice(r.from, r.to)).join("")).toBe(source);
  });

  it("leaves a name that resolves to nothing, and what follows a token that the lexer cannot read, plain", () => {
    const source = 'nobody == "open';
    const a = analyze(source, "condition", scope);
    const runs = runsOf(a);
    expect(runs.map((r) => source.slice(r.from, r.to)).join("")).toBe(source);
    expect(runs.find((r) => source.slice(r.from, r.to) === "nobody")?.tint).toBe(
      "plain",
    );
    expect(runs[runs.length - 1].tint).toBe("plain");
  });
});

describe("a built-in function's signature", () => {
  it("names its parameters, and an optional one in brackets", () => {
    expect(signatureOf("min")).toBe("min(a, b)");
    expect(signatureOf("signal")).toBe("signal(name[, value])");
    expect(signatureOf("visited")).toBe("visited(node)");
  });
});
