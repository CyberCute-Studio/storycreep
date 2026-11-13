import { encode } from "@storycreep/shared/expr/compile";
import { describe, expect, it } from "vitest";
import { valueNamer } from "../content/textNames";
import { analyze } from "./analyze";
import {
  isNumber,
  readPicked,
  stringLiteral,
  stringText,
  type Target,
  writePicked,
} from "./picker";
import { harbourScope, ids } from "./testdata";

// The guided picker's model of a condition (PRD FR-EXPR-01 AC3, plan note
// N74): it reads a condition of its shape, and writes back the source of
// the same AST.

const scope = harbourScope();
const name = valueNamer(scope.view, scope.boardId);
const nameOf = (t: Target) => name(t.ref, t.attribute) ?? "";

/** Returns the canonical AST of a condition that compiles. */
function astOf(source: string): string {
  const a = analyze(source, "condition", scope);
  if (a.ast === undefined) {
    throw new Error(`${source} does not compile: ${a.error?.code}`);
  }
  return encode(a.ast);
}

describe("the guided picker's model of a condition", () => {
  // Verifies: FR-EXPR-01/AC3
  it("reads each condition of its shape, and writes back a source of the same AST", () => {
    for (const source of [
      "met_elara",
      "not met_elara",
      "!met_elara",
      "met_elara == false",
      "gold >= 3",
      "gold<-2",
      "gold != - 2",
      "global.trust > 2.5",
      "trust <= 1.5e2",
      'keeper == "Ada \\"the\\" keeper"',
      'elara.mood != "angry"',
      'cmp.elara.mood == "calm" // the default',
      "(elara.trust) > 0",
    ]) {
      const picked = readPicked(analyze(source, "condition", scope));
      expect(picked, source).toBeDefined();
      const written = writePicked(picked!, nameOf);
      expect(astOf(written), `${source} as ${written}`).toBe(astOf(source));
    }
  });

  it("reads an empty condition as nothing tested yet, which it writes as the empty source", () => {
    for (const source of ["", "  ", "// nothing yet"]) {
      expect(readPicked(analyze(source, "condition", scope))).toEqual({
        test: "eq",
        value: "",
      });
    }
    expect(writePicked({ test: "eq", value: "" }, nameOf)).toBe("");
  });

  it("reads no condition of another shape, nor one that does not compile", () => {
    for (const source of [
      "met_elara and gold > 1",
      "gold > trust",
      "3 < gold",
      "gold + 1 > 2",
      "visited(node(\"" + ids.quay + "\"))",
      "nobody > 1",
      "gold >",
    ]) {
      expect(readPicked(analyze(source, "condition", scope)), source).toBeUndefined();
    }
  });

  it("writes the names as the board's expressions name them", () => {
    expect(
      writePicked({ target: { ref: ids.trustGlobal }, test: "gt", value: "1" }, nameOf),
    ).toBe("global.trust > 1");
    expect(
      writePicked({ target: { ref: ids.trustBoard }, test: "is_false", value: "" }, nameOf),
    ).toBe("not trust");
  });

  it("writes a string literal that reads back as the text", () => {
    for (const text of ['say "hi"', "a\\b", "two\nlines\tand\rmore", "😀", ""]) {
      expect(stringText(stringLiteral(text))).toBe(text);
    }
    expect(stringLiteral("a\0b")).toBe('"ab"');
  });

  it("tells a number from what is not one", () => {
    for (const ok of ["3", "-2", "2.5", "1.5e3", "- 4"]) {
      expect(isNumber(ok), ok).toBe(true);
    }
    for (const bad of ["", "abc", "3 4", "--1", "1.", "1e3", "0x1f"]) {
      expect(isNumber(bad), bad).toBe(false);
    }
  });
});
