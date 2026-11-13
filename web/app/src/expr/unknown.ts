// The variable that an unknown name of a source would be (PRD FR-EXPR-01,
// FR-VAR-01, plan note N74): of a source whose first error is
// unknown_identifier at a variable's name, its key, the scope that the name
// is written in, if it says one, as global.met or board.met, and the type
// that its place in the source suggests, which the editor offers to create
// it with inline, as "create global bool met_elara".

import {
  isKey,
  type Node,
  type Var,
  walk,
} from "@storycreep/shared/expr/compile";
import type { Analysis } from "./analyze";

/** The type of a variable. */
export type VariableType = "bool" | "int" | "float" | "string";

/** What a name that names nothing would be, as a variable. */
export interface Unknown {
  readonly key: string;
  /** The scope that the name is written in, or undefined for a bare key, which either names. */
  readonly scope?: "global" | "board";
  /** The type that its place suggests. */
  readonly type: VariableType;
}

/** Returns the type of a literal, or of what a node's place makes it, where the node says so alone. */
function literalType(n: Node): VariableType | undefined {
  if (n.kind === "lit") {
    return n.type;
  }
  if (n.kind === "unary") {
    return n.op === "not" ? "bool" : "int";
  }
  return undefined;
}

/**
 * Returns the type that the place of target, in n, suggests: of the target
 * of an assignment, its value's, where that is a literal, or a number for
 * one that updates it; of an operand of and, or, or not, and of a condition
 * alone, a bool; of one compared with, or added to, a literal, the
 * literal's; of an operand of arithmetic, an int; of an argument of a
 * function of strings, a string; and otherwise an int, of a script's, or a
 * bool, of a condition's.
 */
function typeAt(root: Node, target: Var): VariableType {
  let found: VariableType | undefined;
  const visit = (n: Node, parent: Node | undefined) => {
    if (found !== undefined) {
      return;
    }
    if (n === target) {
      found = placeType(n, parent, root);
      return;
    }
    switch (n.kind) {
      case "unary":
        visit(n.operand, n);
        break;
      case "binary":
        visit(n.left, n);
        visit(n.right, n);
        break;
      case "call":
        for (const a of n.args) {
          visit(a, n);
        }
        break;
      case "assign":
        visit(n.target, n);
        visit(n.value, n);
        break;
      case "script":
        for (const s of n.statements) {
          visit(s, n);
        }
        break;
    }
  };
  visit(root, undefined);
  return found ?? (root.kind === "script" ? "int" : "bool");
}

/** Returns the type of n's place, of its parent, in root. */
function placeType(
  n: Node,
  parent: Node | undefined,
  root: Node,
): VariableType {
  if (parent === undefined) {
    return root.kind === "script" ? "int" : "bool";
  }
  switch (parent.kind) {
    case "assign":
      if (parent.target === n) {
        return parent.op === "="
          ? (literalType(parent.value) ?? "int")
          : parent.op === "+=" && literalType(parent.value) === "string"
            ? "string"
            : "int";
      }
      return "int";
    case "unary":
      return parent.op === "not" ? "bool" : "int";
    case "binary": {
      const other = parent.left === n ? parent.right : parent.left;
      switch (parent.op) {
        case "and":
        case "or":
          return "bool";
        case "eq":
        case "ne":
        case "lt":
        case "lte":
        case "gt":
        case "gte":
        case "add":
          return literalType(other) ?? "int";
      }
      return "int";
    }
    case "call":
      switch (parent.function) {
        case "len":
        case "contains":
        case "starts_with":
        case "ends_with":
        case "lower":
        case "upper":
        case "trim":
          return "string";
      }
      return "int";
  }
  return "int";
}

/** Returns the variable that analysis's unknown name would be, or undefined unless its first error is an unknown name of a variable whose key may be a variable's. */
export function unknownOf(analysis: Analysis): Unknown | undefined {
  const e = analysis.error;
  const root = analysis.parsed;
  if (e?.code !== "unknown_identifier" || root === undefined) {
    return undefined;
  }
  let target: Var | undefined;
  walk(root, (n) => {
    if (
      target === undefined &&
      n.kind === "var" &&
      n.src.start === e.start &&
      n.src.end === e.end
    ) {
      target = n;
    }
  });
  if (target === undefined || !isKey(target.key)) {
    return undefined;
  }
  return {
    key: target.key,
    ...(target.scope === "" ? {} : { scope: target.scope }),
    type: typeAt(root, target),
  };
}
