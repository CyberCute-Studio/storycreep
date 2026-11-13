// What the expression editor makes of a source as it is typed (PRD
// FR-EXPR-01, FR-EXPR-02): its AST, as the shared parser reads it, checked
// against what the expressions of its board can name (scope.ts), as the
// server will check it once it is saved, or its first error, at the range
// that the parser or the checker gives it; and where each of its tokens is,
// which the editor colours. The language counts a source's characters in
// code points, and the editor's text area in UTF-16 code units, which
// unitsOf converts between.

import {
  type Context,
  check,
  ExprError,
  Lexer,
  type Node,
  parse,
  type Range,
  UsageError,
  walk,
} from "@storycreep/shared/expr/compile";
import { nodesOf, type Scope } from "./scope";

/** What the editor makes of a source. */
export interface Analysis {
  readonly source: string;
  readonly context: Context;
  /** The UTF-16 offset of each code point of the source, and of its end, by the code point's offset. */
  readonly units: readonly number[];
  /** The AST of a source that parses, its names not resolved where it does not compile. */
  readonly parsed?: Node;
  /** The AST of a source that compiles, its names resolved to the IDs of what they name. */
  readonly ast?: Node;
  /** The source's first error, as the parser or the checker meets it, at its range in code points. */
  readonly error?: ExprError;
  /** Whether the source was checked against its board's names, as it is not while the document holds what no project has (Scope's project). */
  readonly checked: boolean;
}

/**
 * Returns the UTF-16 offset of each code point of source, by its offset in
 * code points, and of the source's end after them, as the expression
 * language counts a source's characters (§5.8.1): a lone surrogate, which
 * the lexer reads as U+FFFD, is one code point, of one code unit.
 */
export function unitsOf(source: string): number[] {
  const units: number[] = [];
  for (let i = 0; i < source.length; ) {
    units.push(i);
    const c = source.codePointAt(i) ?? 0;
    i += c > 0xffff ? 2 : 1;
  }
  units.push(source.length);
  return units;
}

/** Returns the range r, of code points, in UTF-16 code units of the source of units, clamped to the source. */
export function unitRange(
  units: readonly number[],
  r: Range,
): { from: number; to: number } {
  const last = units.length - 1;
  const at = (i: number) => units[Math.max(0, Math.min(i, last))];
  return { from: at(r.start), to: at(r.end) };
}

/** Returns the IDs of the nodes that the node references of n name. */
function nodeRefsOf(n: Node): string[] {
  const ids: string[] = [];
  walk(n, (m) => {
    if (m.kind === "node_ref") {
      ids.push(m.ref);
    }
  });
  return ids;
}

/**
 * Returns what the editor makes of source, a condition's or a script's, as
 * context says, of the board of scope: its AST, resolved where it compiles
 * against scope, or its first error. Where scope has no project, the source
 * is parsed alone, so that its syntax errors show, and the rest is the
 * server's to say.
 */
export function analyze(
  source: string,
  context: Context,
  scope: Scope,
): Analysis {
  const units = unitsOf(source);
  let parsed: Node;
  try {
    parsed = parse(source, context);
  } catch (e) {
    if (e instanceof ExprError) {
      return { source, context, units, error: e, checked: false };
    }
    throw e;
  }
  if (scope.project === undefined) {
    return { source, context, units, parsed, checked: false };
  }
  try {
    const refs = nodeRefsOf(parsed);
    const project =
      refs.length === 0
        ? scope.project
        : scope.project.with(
            scope.boardId,
            [],
            nodesOf(scope.view, refs).filter(
              (n) => scope.project?.node(n.id) === undefined,
            ),
          );
    // The checker resolves the names of the AST that it is given, which it
    // leaves as it was where it throws.
    check(parsed, context, project, scope.boardId);
    return { source, context, units, parsed, ast: parsed, checked: true };
  } catch (e) {
    if (e instanceof ExprError) {
      return { source, context, units, parsed, error: e, checked: true };
    }
    // A board's ID that is not one of a board, which no page holds, or a
    // project that the document holds no more of as a whole.
    if (e instanceof UsageError) {
      return { source, context, units, parsed, checked: false };
    }
    throw e;
  }
}

/** The kinds of what the editor colours of a source. */
export type Tint =
  | "plain"
  | "keyword"
  | "literal"
  | "number"
  | "string"
  | "comment"
  | "name"
  | "function";

/** A run of a source's text, in UTF-16 code units, of one tint. */
export interface Run {
  from: number;
  to: number;
  tint: Tint;
}

/**
 * Returns the runs of analysis's source, from its start to its end, each of
 * the tint of what it is: a keyword, a literal, a number or a string, a
 * comment, a name that the source resolves, a function that it calls, or
 * plain, as an operator, white space, a name that resolves to nothing, and
 * whatever follows a token that the lexer cannot read, where it stops.
 */
export function runsOf(analysis: Analysis): Run[] {
  const { source, context, units } = analysis;
  // The ranges of the names that resolve, in code points.
  const names: Range[] = [];
  if (analysis.ast !== undefined) {
    walk(analysis.ast, (n) => {
      if (n.kind === "var" || n.kind === "attr") {
        names.push(n.src);
      }
    });
  }
  const resolved = (start: number) =>
    names.some((r) => r.start <= start && start < r.end);
  const runs: Run[] = [];
  // The runs of identifiers, which a ( after one retints, and which nothing
  // after them joins.
  const idents = new Set<Run>();
  const push = (start: number, end: number, tint: Tint) => {
    if (end <= start) {
      return;
    }
    const { from, to } = unitRange(units, { start, end });
    const last = runs[runs.length - 1];
    if (
      last !== undefined &&
      !idents.has(last) &&
      last.tint === tint &&
      last.to === from
    ) {
      last.to = to;
    } else {
      runs.push({ from, to, tint });
    }
  };
  // What lies between two tokens is white space and comments.
  const gap = (start: number, end: number) => {
    const { from, to } = unitRange(units, { start, end });
    const text = source.slice(from, to);
    let at = start;
    for (const m of text.matchAll(/\/\/[^\n\r]*/g)) {
      const index = m.index ?? 0;
      const begin = start + unitsOf(text.slice(0, index)).length - 1;
      const finish = begin + unitsOf(m[0]).length - 1;
      push(at, begin, "plain");
      push(begin, finish, "comment");
      at = finish;
    }
    push(at, end, "plain");
  };
  const lexer = new Lexer(source, context);
  let at = 0;
  // The run of the last identifier, which a ( after it makes a function's.
  let ident: Run | undefined;
  const total = units.length - 1;
  for (;;) {
    const tok = lexer.next();
    gap(at, tok.start);
    at = tok.start;
    if (tok.kind === "eof") {
      break;
    }
    if (tok.kind === "error") {
      push(tok.start, total, "plain");
      at = total;
      break;
    }
    if (tok.kind === "(" && ident !== undefined) {
      ident.tint = "function";
    }
    ident = undefined;
    let tint: Tint = "plain";
    switch (tok.kind) {
      case "ident":
        tint = resolved(tok.start) ? "name" : "plain";
        break;
      case "int":
      case "float":
        tint = "number";
        break;
      case "string":
        tint = "string";
        break;
      case "true":
      case "false":
        tint = "literal";
        break;
      default:
        // A reserved word, as global, and, or null, though not the && that
        // and stands for, nor an operator.
        tint = tok.keyword ? "keyword" : "plain";
    }
    const { from, to } = unitRange(units, tok);
    if (tok.kind === "ident") {
      const run: Run = { from, to, tint };
      runs.push(run);
      idents.add(run);
      ident = run;
    } else {
      push(tok.start, tok.end, tint);
    }
    at = tok.end;
  }
  push(at, total, "plain");
  return runs;
}
