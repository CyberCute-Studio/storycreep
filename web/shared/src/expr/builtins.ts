// The types of the language, its built-in functions, and the ast_version of
// each construct (§5.8.3, §5.8.4, FR-EXPR-04).

import type { AssignOp, BinaryOp, Kind, Node, UnaryOp } from "./ast";
import type { TypeName } from "./error";
import { checkPlace, eachChild, type Place } from "./shape";

// A set of types: those of the language's values, bool, int, float, and
// string, and node, a node reference's. The type of an expression is a set of
// one.
export type TypeSet = number;

export const TY_BOOL = 1;
export const TY_INT = 2;
export const TY_FLOAT = 4;
export const TY_STRING = 8;
export const TY_NODE = 16;
/** The numbers' types, an int widened where a float is read. */
export const TY_NUMBER = TY_INT | TY_FLOAT;
/** The types of the language's values. */
export const TY_VALUE = TY_BOOL | TY_INT | TY_FLOAT | TY_STRING;

// typeNames name each type, in the order that an error's expected lists
// them.
const typeNames: readonly [TypeSet, TypeName][] = [
  [TY_BOOL, "bool"],
  [TY_INT, "int"],
  [TY_FLOAT, "float"],
  [TY_STRING, "string"],
  [TY_NODE, "node"],
];

/** Returns the names of the types of s, in the order bool, int, float, string, node. */
export function typeNamesOf(s: TypeSet): TypeName[] {
  return typeNames.filter(([t]) => (s & t) !== 0).map(([, name]) => name);
}

/** Returns the name of the one type of s. */
export function typeNameOf(s: TypeSet): TypeName | undefined {
  return typeNames.find(([t]) => s === t)?.[1];
}

/**
 * Returns the type that an expression reads a variable's or an attribute's
 * value of the type t as: an enum as a string, as its options are. A text,
 * which no expression reads, is a string too.
 */
export function typeOf(t: string): TypeSet {
  switch (t) {
    case "bool":
      return TY_BOOL;
    case "int":
      return TY_INT;
    case "float":
      return TY_FLOAT;
  }
  return TY_STRING;
}

/**
 * Returns the types that a place of the type t takes: its own, and an int
 * where a float is read, which is widened (§5.8.3).
 */
export function accepts(t: TypeSet): TypeSet {
  return t === TY_FLOAT ? TY_NUMBER : t;
}

/**
 * Returns the type of an arithmetic operation, or of min, max, abs, or clamp,
 * of numbers of the types ts: an int of ints, and otherwise a float.
 */
export function widened(...ts: TypeSet[]): TypeSet {
  return ts.every((t) => t === TY_INT) ? TY_INT : TY_FLOAT;
}

/** A built-in function (§5.8.4). */
export interface Builtin {
  /** The types that each parameter takes. */
  readonly params: readonly TypeSet[];
  /** How many of the last params a call may leave out. */
  readonly optional: number;
  /**
   * The type of the call's value, or 0 for the widened type of its
   * arguments', as min's.
   */
  readonly result: TypeSet;
  /**
   * Set for a function that has an effect, signal, which only a script's
   * statement calls; a call of any other is pure.
   */
  readonly effect: boolean;
  /**
   * The ast_version that introduced the function, or last changed what it
   * does (FR-EXPR-04): a change of the pinned Unicode tables changes
   * lower's, upper's, and trim's.
   */
  readonly version: number;
}

/**
 * Reports whether a call of b may have n arguments: as many as its params, or
 * fewer by at most its optional ones.
 */
export function takes(b: Builtin, n: number): boolean {
  return n >= b.params.length - b.optional && n <= b.params.length;
}

function builtin(
  params: TypeSet[],
  result: TypeSet,
  extra: { optional?: number; effect?: boolean } = {},
): Builtin {
  return {
    params,
    result,
    optional: extra.optional ?? 0,
    effect: extra.effect ?? false,
    version: 1,
  };
}

/**
 * The built-in functions, by their names (§5.8.4). A signal's first
 * parameter, its name, is a string literal of a character or more, which
 * check reads rather than its type.
 */
export const BUILTINS: ReadonlyMap<string, Builtin> = new Map([
  ["min", builtin([TY_NUMBER, TY_NUMBER], 0)],
  ["max", builtin([TY_NUMBER, TY_NUMBER], 0)],
  ["abs", builtin([TY_NUMBER], 0)],
  ["floor", builtin([TY_NUMBER], TY_INT)],
  ["ceil", builtin([TY_NUMBER], TY_INT)],
  ["round", builtin([TY_NUMBER], TY_INT)],
  ["clamp", builtin([TY_NUMBER, TY_NUMBER, TY_NUMBER], 0)],
  ["to_int", builtin([TY_NUMBER | TY_STRING], TY_INT)],
  ["to_float", builtin([TY_NUMBER | TY_STRING], TY_FLOAT)],
  ["to_string", builtin([TY_VALUE], TY_STRING)],
  ["len", builtin([TY_STRING], TY_INT)],
  ["contains", builtin([TY_STRING, TY_STRING], TY_BOOL)],
  ["starts_with", builtin([TY_STRING, TY_STRING], TY_BOOL)],
  ["ends_with", builtin([TY_STRING, TY_STRING], TY_BOOL)],
  ["lower", builtin([TY_STRING], TY_STRING)],
  ["upper", builtin([TY_STRING], TY_STRING)],
  ["trim", builtin([TY_STRING], TY_STRING)],
  ["visited", builtin([TY_NODE], TY_BOOL)],
  ["visits", builtin([TY_NODE], TY_INT)],
  ["signal", builtin([TY_STRING, TY_VALUE], 0, { optional: 1, effect: true })],
]);

/** Returns the names of the built-in functions (§5.8.4), sorted. */
export function builtins(): string[] {
  return [...BUILTINS.keys()].sort();
}

/**
 * The ast_version of this module's language (FR-EXPR-04): the highest of its
 * constructs', the newest that check accepts and that a runtime must support
 * to evaluate every AST that it accepts.
 */
export const AST_VERSION = 1;

// The ast_version that introduced each node kind and operator, or last
// changed what it does (FR-EXPR-04).
const kindVersions: Record<Kind, number> = {
  lit: 1,
  var: 1,
  attr: 1,
  node_ref: 1,
  unary: 1,
  binary: 1,
  call: 1,
  assign: 1,
  script: 1,
};
const unaryVersions: Record<UnaryOp, number> = { not: 1, neg: 1 };
const binaryVersions: Record<BinaryOp, number> = {
  or: 1,
  and: 1,
  eq: 1,
  ne: 1,
  lt: 1,
  lte: 1,
  gt: 1,
  gte: 1,
  add: 1,
  sub: 1,
  mul: 1,
  div: 1,
  mod: 1,
};
const assignVersions: Record<AssignOp, number> = {
  "=": 1,
  "+=": 1,
  "-=": 1,
  "*=": 1,
  "/=": 1,
  "%=": 1,
};

/**
 * Returns the ast_version of the ASTs, as a snapshot's manifest declares it
 * of its expressions' ASTs (FR-EXPR-04, PRD §6.4): the highest of the node
 * kinds, the operators, and the built-ins that they use, or 1, the first, for
 * none. A node that is none of an AST's at its place counts for nothing, nor
 * do its children, a list of a node's that is no list holds none
 * (eachChild), and a node that it reaches twice counts once, so that it
 * never reads without end. It takes the ASTs as an array, as the Go
 * function takes a slice, so that a snapshot's, however many, are never a
 * call's arguments, of which an engine takes some hundred thousand at most.
 */
export function versionOf(asts: readonly Node[]): number {
  let v = 1;
  const seen = new Set<Node>();
  const stack: [Node, Place][] = asts.map((n) => [n, "root"]);
  for (let top = stack.pop(); top !== undefined; top = stack.pop()) {
    const [n, at] = top;
    if (checkPlace(n, at) !== undefined || seen.has(n)) {
      continue;
    }
    seen.add(n);
    v = Math.max(v, constructVersion(n));
    eachChild(n, (c, place) => stack.push([c, place]));
  }
  return v;
}

// constructVersion returns the ast_version of n's own construct: of its
// kind, and of its operator or its function, if it has one.
function constructVersion(n: Node): number {
  const v = versionIn(kindVersions, n.kind);
  switch (n.kind) {
    case "unary":
      return Math.max(v, versionIn(unaryVersions, n.op));
    case "binary":
      return Math.max(v, versionIn(binaryVersions, n.op));
    case "assign":
      return Math.max(v, versionIn(assignVersions, n.op));
    case "call":
      return Math.max(v, BUILTINS.get(n.function)?.version ?? 0);
  }
  return v;
}

// versionIn returns the version that versions gives the construct of the
// name, or 0 of a name that it does not give one, as the Go function's maps
// give: of an operator that is none of the language's, as a malformed node's,
// named after a member of Object.prototype, as constructor, too.
function versionIn(
  versions: Readonly<Record<string, number>>,
  name: string,
): number {
  return Object.hasOwn(versions, name) ? versions[name] : 0;
}
