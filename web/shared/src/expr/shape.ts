// What an AST's nodes are, and where each kind can be: the one switch over
// the kinds that check, versionOf, encode, and walk read an AST by.

import type { AssignOp, BinaryOp, Node, UnaryOp } from "./ast";

/** Where a node is in an AST, which decides the kinds of node that it can be of. */
export type Place =
  /** The AST itself: an expression or a script. */
  | "root"
  /** An expression's place: an operand, a call's argument, or an assignment's value. */
  | "operand"
  /** A script's statement: an assignment or a call. */
  | "statement"
  /** An assignment's target: a variable or an attribute. */
  | "target";

const placeNames: Record<Place, string> = {
  root: "an AST",
  operand: "an operand, an argument, or an assignment's value",
  statement: "a script's statement",
  target: "an assignment's target",
};

/** The unary operators of the language. */
export const UNARY_OPS: ReadonlySet<string> = new Set<UnaryOp>(["not", "neg"]);

/** The binary operators of the language. */
export const BINARY_OPS: ReadonlySet<string> = new Set<BinaryOp>([
  "or",
  "and",
  "eq",
  "ne",
  "lt",
  "lte",
  "gt",
  "gte",
  "add",
  "sub",
  "mul",
  "div",
  "mod",
]);

/** The assignment operators of the language. */
export const ASSIGN_OPS: ReadonlySet<string> = new Set<AssignOp>([
  "=",
  "+=",
  "-=",
  "*=",
  "/=",
  "%=",
]);

/** The error of a node that is no node, or nil. */
export const NOT_A_NODE = "a node is nil, or of no kind of an AST";

/**
 * Returns undefined if n is a node of one of the kinds that can be at the
 * place at, and otherwise why it is not: NOT_A_NODE for what is no object of
 * a kind, as null, undefined, or an object of another kind, and the kind
 * that the place cannot be of. It tells where each kind can be in one switch
 * over the kinds, as eachChild tells each kind's children in one.
 */
export function checkPlace(n: unknown, at: Place): string | undefined {
  if (typeof n !== "object" || n === null || !("kind" in n)) {
    return NOT_A_NODE;
  }
  const expression = at === "root" || at === "operand";
  let fits: boolean;
  switch ((n as { kind: unknown }).kind) {
    case "lit":
    case "node_ref":
    case "unary":
    case "binary":
      fits = expression;
      break;
    case "var":
    case "attr":
      fits = at !== "statement";
      break;
    case "call":
      fits = at !== "target";
      break;
    case "assign":
      fits = at === "statement";
      break;
    case "script":
      fits = at === "root";
      break;
    default:
      return NOT_A_NODE;
  }
  if (!fits) {
    return `a node of the kind ${(n as Node).kind} cannot be ${placeNames[at]}`;
  }
  return undefined;
}

/**
 * Calls f with each of n's children, at its place, in the order of the
 * source: a script's statements, a call's arguments, a unary operator's
 * operand, a binary operator's left operand and then its right, and an
 * assignment's target and then its value. A leaf has none, nor has a
 * malformed node's list that is no list, as a call's arguments that it does
 * not have, of which it calls f with none, as the Go function ranges over a
 * nil slice.
 */
export function eachChild(n: Node, f: (child: Node, at: Place) => void): void {
  switch (n.kind) {
    case "unary":
      f(n.operand, "operand");
      break;
    case "binary":
      f(n.left, "operand");
      f(n.right, "operand");
      break;
    case "call":
      for (const a of listOf(n.args)) {
        f(a, "operand");
      }
      break;
    case "assign":
      f(n.target, "target");
      f(n.value, "operand");
      break;
    case "script":
      for (const s of listOf(n.statements)) {
        f(s, "statement");
      }
      break;
  }
}

// listOf returns the list of a node's children, or none of a list that is
// no list.
function listOf(list: readonly Node[]): readonly Node[] {
  return Array.isArray(list) ? list : [];
}

/**
 * Calls f with n and with each node under it, each node before its children,
 * and the children in the order of the source, as eachChild tells them, so
 * that what reads every node of an AST, as the names and the node references
 * of a source, finds the children of each kind by this module's one switch.
 * It recurses as deep as the AST is, which parse and check bound by
 * MAX_DEPTH.
 */
export function walk(n: Node, f: (n: Node) => void): void {
  f(n);
  eachChild(n, (c) => walk(c, f));
}
