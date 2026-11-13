// The variable that an unknown name of a source would be (PRD FR-EXPR-01,
// FR-VAR-01, plan note N74): of a source whose first error is
// unknown_identifier at a variable's name, its key, the scope that the name
// is written in, if it says one, as global.met or board.met, and the type
// that its place in the source suggests, of the literals and the names of
// the board's scope that it is assigned, compared with, or added to, which
// the editor offers to create it with inline, as "create global bool
// met_elara".

import {
  type Attr,
  isKey,
  type Node,
  type Var,
  walk,
} from "@storycreep/shared/expr/compile";
import type { Analysis } from "./analyze";
import { boardVariables, runtimeAttributes, type Scope } from "./scope";

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

/** What tells the type of what a name of a source names, a variable or a component's attribute, of a type that a variable may have, or undefined of one that names none. */
type Known = (n: Var | Attr) => VariableType | undefined;

/** Returns what tells the types of the names of scope's board, as the checker resolves them (§5.8.3): a bare key the board's variable of it, or else the global one, and an attribute of a component's key that no variable has, or after cmp., its runtime attribute. */
function knownOf(scope: Scope | undefined): Known {
  if (scope === undefined) {
    return () => undefined;
  }
  const { view, boardId } = scope;
  const variables = boardVariables(view, boardId);
  const asType = (type: string): VariableType | undefined =>
    type === "bool" || type === "int" || type === "float" || type === "string"
      ? type
      : undefined;
  return (n) => {
    if (n.kind === "var") {
      const of = (where: "global" | "board") =>
        variables.find((v) => v.key === n.key && v.scope === where);
      const v = n.scope === "" ? (of("board") ?? of("global")) : of(n.scope);
      return v === undefined ? undefined : asType(v.type);
    }
    if (!n.explicit && variables.some((v) => v.key === n.componentKey)) {
      return undefined;
    }
    const component = [...view.components.values()].find(
      (c) => c.deleted_at === undefined && c.key === n.componentKey,
    );
    const attribute =
      component === undefined
        ? undefined
        : runtimeAttributes(view, component).find(
            (a) => a.key === n.attributeKey,
          );
    return attribute === undefined ? undefined : asType(attribute.type);
  };
}

/** Returns the type of a literal, or of what a node's place makes it, where the node says so alone: a not's a bool, and a minus's the number's that it is before, or an int; or of a name, what it names, as known tells. */
function valueType(n: Node, known: Known): VariableType | undefined {
  switch (n.kind) {
    case "lit":
      return n.type;
    case "unary":
      return n.op === "not"
        ? "bool"
        : valueType(n.operand, known) === "float"
          ? "float"
          : "int";
    case "var":
    case "attr":
      return known(n);
  }
  return undefined;
}

/** Returns the number that arithmetic with other is of: a float where other is a float, and else an int. */
function numberWith(other: Node, known: Known): VariableType {
  return valueType(other, known) === "float" ? "float" : "int";
}

/**
 * Returns the type that the place of target, in n, suggests: of the target
 * of an assignment, its value's, where that is a literal or a name of a
 * known type, or of one that updates it, a string that += adds to, or the
 * number of its value; of the value of an assignment, its target's, as
 * known tells it, or of one that updates it, the string that += adds it to,
 * or the target's number; of an operand of and, or, or not, and of a
 * condition alone, a bool; of one compared with, or added to, a literal or
 * a name of a known type, its type; of an operand of other arithmetic, a
 * float with a float, and else an int; of an argument of a function of
 * strings, a string; and otherwise an int, of a script's, or a bool, of a
 * condition's.
 */
function typeAt(root: Node, target: Var, known: Known): VariableType {
  let found: VariableType | undefined;
  const visit = (n: Node, parent: Node | undefined) => {
    if (found !== undefined) {
      return;
    }
    if (n === target) {
      found = placeType(n, parent, root, known);
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

/** Returns the type of n's place, of its parent, in root, the types of whose names known tells. */
function placeType(
  n: Node,
  parent: Node | undefined,
  root: Node,
  known: Known,
): VariableType {
  if (parent === undefined) {
    return root.kind === "script" ? "int" : "bool";
  }
  switch (parent.kind) {
    case "assign": {
      const [other, value] =
        parent.target === n
          ? [parent.value, valueType(parent.value, known)]
          : [parent.target, valueType(parent.target, known)];
      return parent.op === "="
        ? (value ?? "int")
        : parent.op === "+=" && value === "string"
          ? "string"
          : numberWith(other, known);
    }
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
          return valueType(other, known) ?? "int";
      }
      return numberWith(other, known);
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

/** Returns the variable that analysis's unknown name would be, of the board of scope, whose names' types its place's type may be of, or undefined unless its first error is an unknown name of a variable whose key may be a variable's. */
export function unknownOf(
  analysis: Analysis,
  scope?: Scope,
): Unknown | undefined {
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
    type: typeAt(root, target, knownOf(scope)),
  };
}
