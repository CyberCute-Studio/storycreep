// The variable that an unknown name of a source would be (PRD FR-EXPR-01,
// FR-VAR-01, plan note N74): of a source whose first error is
// unknown_identifier at a variable's name, its key, the scope that the name
// is written in, if it says one, as global.met or board.met, and the type
// that its place in the source suggests, of the literals, the names of the
// board's scope, and what is made of them, that it is assigned, compared
// with, or added to, which the editor offers to create it with inline, as
// "create global bool met_elara", but at a place where no variable may be,
// as visited's argument, where no variable that it creates would compile.

import {
  type Attr,
  BUILTINS,
  isKey,
  type Node,
  TY_BOOL,
  TY_FLOAT,
  TY_INT,
  TY_STRING,
  type Var,
  walk,
} from "@storycreep/shared/expr/compile";
import type { Analysis } from "./analyze";
import type { Scope } from "./scope";

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

/** What tells the type of what a name of a source names, a variable or a component's attribute, of a type that a variable may have, or an enum, or undefined of one that names none. */
type Known = (n: Var | Attr) => VariableType | "enum" | undefined;

/** Returns what tells the types of the names of scope's board, of its Project's variables and components, as the checker resolves them (§5.8.3): a bare key the board's variable of it, or else the global one, and an attribute of a component's key that no variable has, or after cmp., its runtime attribute. */
function knownOf(scope: Scope | undefined): Known {
  const project = scope?.project;
  if (scope === undefined || project === undefined) {
    return () => undefined;
  }
  const { boardId } = scope;
  const variable = (key: string, where: Var["scope"]) =>
    where === "global"
      ? project.global(key)
      : where === "board"
        ? project.ofBoard(boardId, key)
        : (project.ofBoard(boardId, key) ?? project.global(key));
  return (n) => {
    if (n.kind === "var") {
      return variable(n.key, n.scope)?.type;
    }
    if (!n.explicit && variable(n.componentKey, "") !== undefined) {
      return undefined;
    }
    const a = project.component(n.componentKey)?.byKey.get(n.attributeKey);
    if (a === undefined || a.runtime !== true) {
      return undefined;
    }
    return a.type === "text" ? undefined : a.type;
  };
}

/** Returns the number that arithmetic of the operands is of: a float where one is a float, and else an int. */
function numberOf(operands: readonly Node[], known: Known): VariableType {
  return operands.some((o) => valueType(o, known) === "float")
    ? "float"
    : "int";
}

/**
 * Returns the type of a literal, or of what a node's place makes it, where
 * the node says so alone: a not's, a comparison's, an and's, and an or's a
 * bool; a minus's the number's that it is before; a + of a string a string;
 * other arithmetic's a float of a float, or else an int; a function's its
 * result's, or of one of numbers, as min's, the number of its arguments;
 * or of a name, what it names, as known tells, of an enum a string, as an
 * expression reads it.
 */
function valueType(n: Node, known: Known): VariableType | undefined {
  switch (n.kind) {
    case "lit":
      return n.type;
    case "unary":
      return n.op === "not" ? "bool" : numberOf([n.operand], known);
    case "var":
    case "attr": {
      const type = known(n);
      return type === "enum" ? "string" : type;
    }
    case "binary":
      switch (n.op) {
        case "add":
          return valueType(n.left, known) === "string" ||
            valueType(n.right, known) === "string"
            ? "string"
            : numberOf([n.left, n.right], known);
        case "sub":
        case "mul":
        case "div":
          return numberOf([n.left, n.right], known);
        case "mod":
          return "int";
      }
      return "bool";
    case "call": {
      const b = BUILTINS.get(n.function);
      switch (b?.result) {
        case TY_BOOL:
          return "bool";
        case TY_INT:
          return "int";
        case TY_FLOAT:
          return "float";
        case TY_STRING:
          return "string";
        case 0:
          // The widened type of its arguments', but of signal, which has
          // no value.
          return b?.effect === false ? numberOf(n.args, known) : undefined;
      }
      return undefined;
    }
  }
  return undefined;
}

/**
 * Returns the type that the place of target, in n, suggests: of the target
 * of an assignment, its value's, where that says its type (valueType), or
 * of one that updates it, a string that += adds to, or the number of its
 * value; of the value of an assignment, its target's, as known tells it, or
 * of one that updates it, the string that += adds it to, or the target's
 * number; of an operand of and, or, or not, and of a condition alone, a
 * bool; of one compared with, or added to a string, what says its type,
 * that type; of an operand of %, an int; of an operand of other
 * arithmetic, or of a minus, or an argument of min, max, abs, or clamp, a
 * float with a float among the others, and else the number that the place
 * of what it is within suggests, as of -x > 2.5 or x * 2 == 3.5 a float; of
 * an argument of floor, ceil, or round, a float, which they round; of an
 * argument of a function of strings, a string; and otherwise an int, of a
 * script's, or a bool, of a condition's. It is null of a place that no
 * variable may be at, whatever its type: an argument of visited or visits,
 * which takes a node, the value that an enum attribute is assigned, which
 * is one of its options, written as a string, and an operand of what a
 * node is the other of.
 */
function typeAt(root: Node, target: Var, known: Known): VariableType | null {
  let found: VariableType | null | undefined;
  const visit = (n: Node, parents: readonly Node[]) => {
    if (found !== undefined) {
      return;
    }
    if (n === target) {
      found = placeType(n, parents, root, known);
      return;
    }
    const within = [...parents, n];
    switch (n.kind) {
      case "unary":
        visit(n.operand, within);
        break;
      case "binary":
        visit(n.left, within);
        visit(n.right, within);
        break;
      case "call":
        for (const a of n.args) {
          visit(a, within);
        }
        break;
      case "assign":
        visit(n.target, within);
        visit(n.value, within);
        break;
      case "script":
        for (const s of n.statements) {
          visit(s, within);
        }
        break;
    }
  };
  visit(root, []);
  return found === undefined
    ? root.kind === "script"
      ? "int"
      : "bool"
    : found;
}

/** Returns the type of n's place, of its parents, the last its own, in root, the types of whose names known tells, or null of one that no variable may be at (typeAt). */
function placeType(
  n: Node,
  parents: readonly Node[],
  root: Node,
  known: Known,
): VariableType | null {
  const parent = parents[parents.length - 1];
  if (parent === undefined) {
    return root.kind === "script" ? "int" : "bool";
  }
  // The number that the place of the parent suggests, of arithmetic's
  // operand, a minus's, or an argument of a function of the widened type of
  // its own.
  const outer = (): VariableType =>
    placeType(parent, parents.slice(0, -1), root, known) === "float"
      ? "float"
      : "int";
  // A float with a float among the others, and else the number of outer.
  const widenedWith = (others: readonly Node[]): VariableType =>
    numberOf(others, known) === "float" ? "float" : outer();
  switch (parent.kind) {
    case "assign": {
      if (
        parent.value === n &&
        parent.target.kind === "attr" &&
        known(parent.target) === "enum"
      ) {
        return null;
      }
      const [other, value] =
        parent.target === n
          ? [parent.value, valueType(parent.value, known)]
          : [parent.target, valueType(parent.target, known)];
      return parent.op === "="
        ? (value ?? "int")
        : parent.op === "+=" && value === "string"
          ? "string"
          : numberOf([other], known);
    }
    case "unary":
      return parent.op === "not" ? "bool" : outer();
    case "binary": {
      const other = parent.left === n ? parent.right : parent.left;
      if (other.kind === "node_ref") {
        return null;
      }
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
          return valueType(other, known) ?? "int";
        case "mod":
          return "int";
        case "add":
          if (valueType(other, known) === "string") {
            return "string";
          }
      }
      return widenedWith([other]);
    }
    case "call":
      switch (parent.function) {
        case "visited":
        case "visits":
          return null;
        case "len":
        case "contains":
        case "starts_with":
        case "ends_with":
        case "lower":
        case "upper":
        case "trim":
          return "string";
        case "floor":
        case "ceil":
        case "round":
          return "float";
        case "min":
        case "max":
        case "abs":
        case "clamp":
          return widenedWith(parent.args.filter((a) => a !== n));
      }
      return "int";
  }
  return "int";
}

/** Returns the variable that analysis's unknown name would be, of the board of scope, whose names' types its place's type may be of, or undefined unless its first error is an unknown name of a variable whose key may be a variable's, at a place that a variable may be at (typeAt). */
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
  const type = typeAt(root, target, knownOf(scope));
  if (type === null) {
    return undefined;
  }
  return {
    key: target.key,
    ...(target.scope === "" ? {} : { scope: target.scope }),
    type,
  };
}
