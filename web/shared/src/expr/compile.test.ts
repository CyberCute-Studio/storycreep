import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { type Context, NO_RANGE, type Node } from "./ast";
import { versionOf } from "./builtins";
import { check, compile } from "./check";
import { encode } from "./encode";
import {
  COMPILE_CODES,
  type Code,
  ExprError,
  SYNTAX_CODES,
  UsageError,
} from "./error";
import {
  type BoardNode,
  type Component,
  Project,
  type Variable,
} from "./project";
import { len } from "./semantics";
import { walk } from "./shape";

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

// An operator that is none of the language's, as a malformed node's, counts
// for nothing, as the Go function's maps give, whatever its name: that of a
// member of Object.prototype too.
test("versionOf counts an operator that is none of the language's for nothing", () => {
  const lit: Node = { kind: "lit", type: "int", value: 1, src: NO_RANGE };
  const target = {
    kind: "var",
    name: "x",
    scope: "",
    key: "x",
    ref: "",
    src: NO_RANGE,
  };
  for (const op of ["constructor", "toString", "__proto__", "valueOf"]) {
    for (const n of [
      { kind: "unary", op, operand: lit, src: NO_RANGE },
      { kind: "binary", op, left: lit, right: lit, src: NO_RANGE },
      {
        kind: "script",
        statements: [{ kind: "assign", op, target, value: lit, src: NO_RANGE }],
        src: NO_RANGE,
      },
    ]) {
      expect(versionOf([n as unknown as Node]), `${n.kind} ${op}`).toBe(1);
    }
  }
});

// A malformed node's list that is no list, as a call's arguments that it
// does not have, holds no children, as the Go functions range over a nil
// slice, so that versionOf and walk read it without throwing.
test("versionOf and walk read a node's list that is no list as none", () => {
  for (const n of [
    { kind: "call", function: "min", src: NO_RANGE },
    { kind: "call", function: "min", args: 2, src: NO_RANGE },
    { kind: "script", src: NO_RANGE },
    { kind: "script", statements: "x", src: NO_RANGE },
  ]) {
    const node = n as unknown as Node;
    expect(versionOf([node]), JSON.stringify(n)).toBe(1);
    const seen: Node[] = [];
    walk(node, (m) => seen.push(m));
    expect(seen, JSON.stringify(n)).toEqual([node]);
  }
});

// What parses and checks an expression, as the editor's instant feedback,
// @storycreep/shared/expr/compile, imports no JSON, and so none of the
// tables of Unicode, which upper, lower, and trim alone read (unicode.ts),
// so that its bundle holds none of them.
test("the package's parse and check import none of the tables of Unicode", () => {
  const pkg = JSON.parse(
    readFileSync(new URL("../../package.json", import.meta.url), "utf8"),
  );
  expect(pkg.exports["./expr/compile"]).toBe("./src/expr/compile.ts");
  const seen = new Set<string>();
  const stack = [new URL("./compile.ts", import.meta.url).href];
  for (let file = stack.pop(); file !== undefined; file = stack.pop()) {
    if (seen.has(file)) {
      continue;
    }
    seen.add(file);
    const source = readFileSync(new URL(file), "utf8");
    for (const [, spec] of source.matchAll(
      /^(?:import|export)\s(?:[^;]*?\sfrom\s+)?"([^"]+)"/gm,
    )) {
      expect(spec, file).not.toMatch(/\.json$/);
      if (spec.startsWith(".")) {
        stack.push(new URL(`${spec}.ts`, file).href);
      }
    }
  }
  const names = [...seen].map((f) => f.slice(f.lastIndexOf("/") + 1));
  expect(names).toEqual(
    expect.arrayContaining(["parser.ts", "check.ts", "semantics.ts"]),
  );
  expect(names).not.toContain("unicode.ts");
  expect(names).not.toContain("eval.ts");
});

// A field that the checker reads as a string, or an attr's explicit, of
// another type, which no AST that parse or decode returns holds, is refused
// as one, with a UsageError, before the checker reads it.
test("check refuses an AST of a field of another type as a UsageError", () => {
  for (const n of [
    { kind: "call", function: 5, args: [], src: NO_RANGE },
    { kind: "var", name: "x", scope: "", key: 5, ref: "", src: NO_RANGE },
    {
      kind: "attr",
      name: "a.b",
      explicit: "no",
      componentKey: "a",
      attributeKey: "b",
      ref: "",
      attribute: "",
      src: NO_RANGE,
    },
    { kind: "node_ref", ref: null, src: NO_RANGE },
  ]) {
    let err: unknown;
    try {
      check(n as unknown as Node, "condition", undefined, "");
    } catch (e) {
      err = e;
    }
    expect(err, JSON.stringify(n)).toBeInstanceOf(UsageError);
    expect((err as UsageError).message).toMatch(/is not a (string|boolean)/);
  }
});
