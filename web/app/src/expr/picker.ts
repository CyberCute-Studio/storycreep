// The guided picker's model of a condition (PRD FR-EXPR-01 AC3, plan note
// N74): a value that the board reads, a test of it, and what it is tested
// against, which the picker reads of a condition's compiled AST, where the
// condition is of that shape, and writes back as the source of the same
// AST: the picker and the text editor are two views of one source, which a
// switch between them leaves as it is, and which the picker writes anew
// only as the user changes what it shows.

import { Lexer, type Node, type Range } from "@storycreep/shared/expr/compile";
import { type Analysis, unitRange } from "./analyze";

/** The tests that the picker offers: of a bool, whether it is true or false, and of any value, how it compares with another. */
export type Test =
  | "is_true"
  | "is_false"
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte";

/** What the picker tests: a variable, by its ID, or a component's attribute, by the component's ID and the attribute definition's. */
export interface Target {
  readonly ref: string;
  readonly attribute?: string;
}

/** What the picker shows of a condition. */
export interface Picked {
  /** What it tests, or none yet. */
  readonly target?: Target;
  readonly test: Test;
  /** The source of the literal that it tests against, as 3, -2.5, true, or "north", or "" for none, as of a bool's is_true. */
  readonly value: string;
}

/** The type of a target, as the picker reads it: an enum's options, which are its values. */
export type TargetType =
  | { readonly kind: "bool" | "int" | "float" | "string" }
  | { readonly kind: "enum"; readonly options: readonly string[] };

/** The operators of the tests that compare, as the source writes them. */
const symbols: Record<Exclude<Test, "is_true" | "is_false">, string> = {
  eq: "==",
  ne: "!=",
  lt: "<",
  lte: "<=",
  gt: ">",
  gte: ">=",
};

/** Returns the tests that the picker offers of a value of type: of a bool, is true and is false, and is and is not of a literal; of an enum, is and is not one of its options; and of a number or a string, every comparison. */
export function testsOf(type: TargetType): readonly Test[] {
  switch (type.kind) {
    case "bool":
      return ["is_true", "is_false", "eq", "ne"];
    case "enum":
      return ["eq", "ne"];
  }
  return ["eq", "ne", "lt", "lte", "gt", "gte"];
}

/** Returns whether a test compares the value with a literal, which the picker then asks for. */
export function takesValue(test: Test): boolean {
  return test !== "is_true" && test !== "is_false";
}

/** Returns the source of the string literal of text, as the lexer reads one (§5.8.1): ", \, and the line breaks escaped, and a NUL, which no source holds, left out. */
export function stringLiteral(text: string): string {
  return `"${text
    .replace(/\0/g, "")
    .replace(/\\/g, "\\\\")
    .replace(/"/g, '\\"')
    .replace(/\n/g, "\\n")
    .replace(/\r/g, "\\r")
    .replace(/\t/g, "\\t")}"`;
}

/** Returns the text of the string literal of source, as the lexer reads it, or undefined if source is no string literal alone. */
export function stringText(source: string): string | undefined {
  const lexer = new Lexer(source, "condition");
  const tok = lexer.next();
  return tok.kind === "string" && lexer.next().kind === "eof"
    ? tok.text
    : undefined;
}

/** Reports whether text is the source of a number, as 3, 2.5, or -1e3: an int or a float literal, with a minus before it or none. */
export function isNumber(text: string): boolean {
  const lexer = new Lexer(text, "condition");
  let tok = lexer.next();
  if (tok.kind === "-") {
    tok = lexer.next();
  }
  return (
    (tok.kind === "int" || tok.kind === "float") && lexer.next().kind === "eof"
  );
}

/** Returns the default literal of a value of type, which the picker starts a test of it with: true, so that a bool's "is true" changed to "is" tests what it did, 0, the empty string, or an enum's first option. */
export function defaultValue(type: TargetType): string {
  switch (type.kind) {
    case "bool":
      return "true";
    case "int":
    case "float":
      return "0";
    case "string":
      return stringLiteral("");
    case "enum":
      return stringLiteral(type.options[0] ?? "");
  }
}

/** Reports whether n is a literal that the picker tests against: a bool, a number, or a string, or a number with a minus before it. */
function isLiteral(n: Node): boolean {
  return (
    n.kind === "lit" ||
    (n.kind === "unary" &&
      n.op === "neg" &&
      n.operand.kind === "lit" &&
      (n.operand.type === "int" || n.operand.type === "float"))
  );
}

/** Returns the target that n names, of a resolved AST, or undefined if n names none. */
function targetOf(n: Node): Target | undefined {
  if (n.kind === "var" && n.ref !== "") {
    return { ref: n.ref };
  }
  if (n.kind === "attr" && n.ref !== "") {
    return { ref: n.ref, attribute: n.attribute };
  }
  return undefined;
}

/** Returns the source of the range r, of code points, of analysis's source. */
function sourceOf(analysis: Analysis, r: Range): string {
  const { from, to } = unitRange(analysis.units, r);
  return analysis.source.slice(from, to);
}

/**
 * Returns what the picker shows of analysis, a condition's: nothing yet
 * tested of one whose source is empty, or only white space and comments, as
 * no condition; a test of a name of a bool, as met_elara, of its negation,
 * as not met_elara, or of a comparison of a name with a literal, as
 * elara.trust >= 3, of one that compiles; and undefined of any other, which
 * the picker cannot show, and which the text editor edits.
 */
export function readPicked(analysis: Analysis): Picked | undefined {
  if (
    analysis.error?.code === "unexpected_end" &&
    analysis.parsed === undefined
  ) {
    // Nothing but white space and comments, which name nothing.
    const lexer = new Lexer(analysis.source, "condition");
    if (lexer.next().kind === "eof") {
      return { test: "eq", value: "" };
    }
  }
  const ast = analysis.ast;
  if (ast === undefined) {
    return undefined;
  }
  const named = targetOf(ast);
  if (named !== undefined) {
    return { target: named, test: "is_true", value: "" };
  }
  if (ast.kind === "unary" && ast.op === "not") {
    const negated = targetOf(ast.operand);
    return negated === undefined
      ? undefined
      : { target: negated, test: "is_false", value: "" };
  }
  if (ast.kind !== "binary") {
    return undefined;
  }
  const test = (Object.keys(symbols) as (keyof typeof symbols)[]).find(
    (k) => k === ast.op,
  );
  const target = targetOf(ast.left);
  if (test === undefined || target === undefined || !isLiteral(ast.right)) {
    return undefined;
  }
  return { target, test, value: sourceOf(analysis, ast.right.src) };
}

/** Returns the source of the condition that picked shows, writing its target as name names it: the empty source of none. */
export function writePicked(
  picked: Picked,
  name: (target: Target) => string,
): string {
  if (picked.target === undefined) {
    return "";
  }
  const written = name(picked.target);
  switch (picked.test) {
    case "is_true":
      return written;
    case "is_false":
      return `not ${written}`;
  }
  return `${written} ${symbols[picked.test]} ${picked.value}`;
}
