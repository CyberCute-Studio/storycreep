import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import type { Context, Node } from "./ast";
import { encode } from "./encode";
import { type Code, ExprError, SYNTAX_CODES } from "./error";
import { parse } from "./parser";
import { len } from "./semantics";
import { eachChild, walk } from "./shape";

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

// ranges returns the kind and the range of each node of n, as walk gives
// them: n first, each node before its children.
function ranges(n: Node): string[] {
  const out: string[] = [];
  walk(n, (m) => {
    out.push(`${m.kind} ${m.src.start}-${m.src.end}`);
  });
  return out;
}

// Where each node is in its source, in code points, as the Go parser has it
// (TestRanges, of the same sources): from its first token to its last, with
// the parentheses around its operands but not those around itself, so that
// an error that names an operand can cover it as it is written, and a
// name's range is the name's alone, which a rename rewrites.
// Verifies: FR-EXPR-02/AC1
test("the parser gives each node its range in the source, as the Go parser does", () => {
  for (const [ctx, source, want] of [
    [
      "condition",
      "(a + b) * -c",
      [
        "binary 0-12",
        "binary 1-6",
        "var 1-2",
        "var 5-6",
        "unary 10-12",
        "var 11-12",
      ],
    ],
    [
      "condition",
      "elara.trust >= cmp.mira.trust",
      ["binary 0-29", "attr 0-11", "attr 15-29"],
    ],
    [
      "condition",
      '"é" == f(x)',
      ["binary 0-11", "lit 0-3", "call 7-11", "var 9-10"],
    ],
    ["condition", "(x)", ["var 1-2"]],
    [
      "condition",
      'visited(node("nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF"))',
      ["call 0-46", "node_ref 8-45"],
    ],
    ["condition", "global . gold", ["var 0-13"]],
    [
      "script",
      'x += 1\n signal("a")',
      [
        "script 0-19",
        "assign 0-6",
        "var 0-1",
        "lit 5-6",
        "call 8-19",
        "lit 15-18",
      ],
    ],
    ["script", "  // none\n", ["script 0-10"]],
  ] as [Context, string, string[]][]) {
    expect(ranges(parse(source, ctx)), source).toEqual(want);
  }
});

// Of each source of the syntax corpus that parses, each node is within its
// parent, after its siblings before it, and a script is its whole source,
// so that a parse's ranges, which the corpus's ASTs do not hold, are at
// least in the order of the source.
test("the parser's ranges of the corpus's sources nest in the order of the source", () => {
  const failures: string[] = [];
  for (const c of cases) {
    if (c.ast === undefined) {
      continue;
    }
    const n = parse(c.source, c.context);
    const size = len(c.source);
    const outer = n.kind === "script" ? { start: 0, end: size } : n.src;
    if (
      n.src.start !== outer.start ||
      n.src.end !== outer.end ||
      n.src.start > n.src.end ||
      n.src.end > size
    ) {
      failures.push(`${c.note}: ${n.kind} ${n.src.start}-${n.src.end}`);
    }
    walk(n, (m) => {
      let after = m.src.start;
      eachChild(m, (child) => {
        if (
          child.src.start < after ||
          child.src.start > child.src.end ||
          child.src.end > m.src.end
        ) {
          failures.push(
            `${c.note}: ${child.kind} ${child.src.start}-${child.src.end} in ${m.kind} ${m.src.start}-${m.src.end}`,
          );
        }
        after = child.src.end;
      });
    });
  }
  expect(failures).toEqual([]);
});

// errorOf returns the code and the range of the error that parse throws of
// the source, or undefined if it throws none.
function errorOf(source: string, ctx: Context) {
  try {
    parse(source, ctx);
  } catch (e) {
    if (e instanceof ExprError) {
      return { code: e.code, start: e.start, end: e.end };
    }
    throw e;
  }
  return undefined;
}

// A lone surrogate, which is no character, is read as U+FFFD, as the server
// reads the escape of one that the editor's JSON.stringify writes in a
// command's payload, so that a source that holds one gives what the server
// gives of it.
test("the parser reads a lone surrogate as U+FFFD, as the server reads one", () => {
  const lone = String.fromCharCode(0xd800);
  const replacement = String.fromCharCode(0xfffd);
  expect(parse(`"a${lone}"`, "condition")).toEqual(
    parse(`"a${replacement}"`, "condition"),
  );
  expect(errorOf(`x = 1 // ${lone}`, "script")).toBeUndefined();
  expect(errorOf(`x == ${lone}`, "condition")).toEqual({
    code: "invalid_character",
    start: 5,
    end: 6,
  });
  expect(errorOf(`x == ${replacement}`, "condition")).toEqual(
    errorOf(`x == ${lone}`, "condition"),
  );
});
