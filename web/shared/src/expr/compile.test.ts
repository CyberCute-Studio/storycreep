import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { type Context, NO_RANGE, type Node } from "./ast";
import { versionOf } from "./builtins";
import { compile } from "./check";
import { encode } from "./encode";
import { COMPILE_CODES, type Code, ExprError, SYNTAX_CODES } from "./error";
import {
  type BoardNode,
  type Component,
  Project,
  type Variable,
} from "./project";
import { len } from "./semantics";

// The file is read rather than imported, since its deepest cases nest
// beyond what the bundler's JSON reader recurses to.
const corpus = JSON.parse(
  readFileSync(
    new URL("../../../../schema/expr-corpus/compile.json", import.meta.url),
    "utf8",
  ),
);

// schema/expr-corpus/compile.json, the checker's cases, with the project that
// they resolve names against, which the Go checker runs too
// (TestCompileCorpus). See schema/expr-corpus/README.md.
interface CompileCase {
  note: string;
  context: Context;
  board?: string;
  source: string;
  ast?: unknown;
  error?: { code: Code; start: number; end: number };
}

const project = corpus.project as {
  variables: Variable[];
  components: Component[];
  nodes: BoardNode[];
};
const cases = corpus.cases as CompileCase[];

// fullyResolved reports whether v, a case's AST, gives every var a ref, and
// every attr a ref and an attribute.
function fullyResolved(v: unknown): boolean {
  if (Array.isArray(v)) {
    return v.every(fullyResolved);
  }
  if (typeof v === "object" && v !== null) {
    const o = v as Record<string, unknown>;
    if (o.kind === "var" && !("ref" in o)) {
      return false;
    }
    if (o.kind === "attr" && !("ref" in o && "attribute" in o)) {
      return false;
    }
    return Object.values(o).every(fullyResolved);
  }
  return true;
}

// Each case's source compiles, on its board of the corpus's project, to the
// AST that the case holds, its names resolved, compared by value, or gives
// the error, by each of its keys, as the Go checker gives them, so that the
// two compile every source alike (FR-EXPR-02 AC1): the type and the
// expected types of a type_mismatch (AC4), the limits of the sandbox (AC2),
// a board's variable of another board (FR-VAR-02 AC2), an attribute that is
// not runtime (FR-COMP-03 AC2), and an unknown function's near matches and
// a signal in a condition (FR-EXPR-04 AC2, AC3).
// Verifies: FR-EXPR-02/AC1, FR-EXPR-02/AC2, FR-EXPR-02/AC4, FR-VAR-02/AC2, FR-COMP-03/AC2, FR-EXPR-04/AC2, FR-EXPR-04/AC3
test("the checker gives each source of the compile corpus its AST or its error", () => {
  const p = Project.of(project.variables, project.components, project.nodes);
  const notes = new Set<string>();
  const seen = new Set<Code>();
  const failures: string[] = [];
  for (const c of cases) {
    const where = `compile.json: ${c.note}`;
    expect(notes.has(c.note), `${where}: the note is another case's`).toBe(
      false,
    );
    notes.add(c.note);
    expect(["condition", "script"]).toContain(c.context);
    expect(
      c.ast === undefined,
      `${where}: an AST and an error, or neither`,
    ).toBe(c.error !== undefined);
    let got: unknown;
    try {
      got = JSON.parse(encode(compile(c.source, c.context, p, c.board ?? "")));
    } catch (e) {
      if (!(e instanceof ExprError)) {
        throw e;
      }
      got = e.toJSON();
    }
    if (c.error !== undefined) {
      seen.add(c.error.code);
      expect([...COMPILE_CODES, ...SYNTAX_CODES]).toContain(c.error.code);
      expect(c.error.start).toBeGreaterThanOrEqual(0);
      expect(c.error.start).toBeLessThanOrEqual(c.error.end);
      expect(c.error.end).toBeLessThanOrEqual(len(c.source));
    } else {
      expect(fullyResolved(c.ast), `${where}: the AST is not resolved`).toBe(
        true,
      );
    }
    try {
      expect(got).toEqual(c.error ?? c.ast);
    } catch {
      failures.push(
        `${where}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.error ?? c.ast)}`,
      );
    }
  }
  expect(failures).toEqual([]);
  expect(
    COMPILE_CODES.filter((code) => code !== "ast_too_large" && !seen.has(code)),
  ).toEqual([]);
});

// The corpus holds at least the 200 expressions, valid and invalid, that
// FR-EXPR-02 AC1 asks the Go and the TypeScript implementations to give
// alike: the parser's and the checker's cases together.
// Verifies: FR-EXPR-02/AC1
test("the corpus that both implementations run has at least 200 expressions", () => {
  expect(cases.length).toBeGreaterThanOrEqual(200);
});

// A snapshot's ASTs, however many, as more than an engine takes as a
// call's arguments, of which there are some hundred thousand at most.
test("versionOf reads any number of ASTs", () => {
  const lit: Node = { kind: "lit", type: "int", value: 1, src: NO_RANGE };
  const asts = Array.from({ length: 500_000 }, (): Node => ({ ...lit }));
  expect(versionOf(asts)).toBe(1);
  expect(versionOf([])).toBe(1);
});
