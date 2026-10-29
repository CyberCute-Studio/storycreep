import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import type { Context } from "./ast";
import { encode } from "./encode";
import { type Code, ExprError, SYNTAX_CODES } from "./error";
import { parse } from "./parser";
import { len } from "./semantics";

// The file is read rather than imported, since its deepest cases nest
// beyond what the bundler's JSON reader recurses to.
const corpus = JSON.parse(
  readFileSync(
    new URL("../../../../schema/expr-corpus/syntax.json", import.meta.url),
    "utf8",
  ),
);

// schema/expr-corpus/syntax.json, the parser's cases, which the Go parser
// runs too (TestSyntaxCorpus). See schema/expr-corpus/README.md.
interface SyntaxCase {
  note: string;
  context: Context;
  source: string;
  ast?: unknown;
  error?: { code: Code; start: number; end: number };
}

const cases = corpus.cases as SyntaxCase[];

// resolved reports whether v, a case's AST, gives a variable or an attribute
// a ref or an attribute, which name resolution adds and no case of the
// parser's has.
function resolved(v: unknown): boolean {
  if (Array.isArray(v)) {
    return v.some(resolved);
  }
  if (typeof v === "object" && v !== null) {
    const o = v as Record<string, unknown>;
    if (
      (o.kind === "var" || o.kind === "attr") &&
      ("ref" in o || "attribute" in o)
    ) {
      return true;
    }
    return Object.values(o).some(resolved);
  }
  return false;
}

// Each case's source gives the AST that the case holds, compared by value,
// each number the double nearest it, or the error, of its code and over its
// characters, counted in code points, as the Go parser gives them, so that
// the two read every source alike (FR-EXPR-02 AC1).
// Verifies: FR-EXPR-02/AC1, FR-EXPR-02/AC3
test("the parser gives each source of the syntax corpus its AST or its error", () => {
  const notes = new Set<string>();
  const seen = new Set<Code>();
  const failures: string[] = [];
  for (const c of cases) {
    const where = `syntax.json: ${c.note}`;
    expect(notes.has(c.note), `${where}: the note is another case's`).toBe(
      false,
    );
    notes.add(c.note);
    expect(["condition", "script"]).toContain(c.context);
    expect(
      c.ast === undefined,
      `${where}: an AST and an error, or neither`,
    ).toBe(c.error !== undefined);
    expect(/\p{Surrogate}/u.test(c.source), `${where}: a lone surrogate`).toBe(
      false,
    );
    let got: unknown;
    try {
      got = JSON.parse(encode(parse(c.source, c.context)));
    } catch (e) {
      if (!(e instanceof ExprError)) {
        throw e;
      }
      got = { code: e.code, start: e.start, end: e.end };
    }
    if (c.error !== undefined) {
      seen.add(c.error.code);
      expect(SYNTAX_CODES).toContain(c.error.code);
      expect(c.error.start).toBeGreaterThanOrEqual(0);
      expect(c.error.start).toBeLessThanOrEqual(c.error.end);
      expect(c.error.end).toBeLessThanOrEqual(len(c.source));
      if (JSON.stringify(got) !== JSON.stringify(c.error)) {
        failures.push(
          `${where}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.error)}`,
        );
      }
      continue;
    }
    expect(resolved(c.ast), `${where}: the AST is resolved`).toBe(false);
    try {
      expect(got).toEqual(c.ast);
    } catch {
      failures.push(
        `${where}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.ast)}`,
      );
    }
  }
  expect(failures).toEqual([]);
  expect([...SYNTAX_CODES].filter((code) => !seen.has(code))).toEqual([]);
});
