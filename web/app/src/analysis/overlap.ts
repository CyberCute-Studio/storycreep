// The proofs that two conditions never hold together, which the rule of
// duplicate choices reads (validation.ts, PRD FR-DEP-03, plan note N55):
// the TypeScript twin of the server's prover (package validation,
// overlap.go), step for step, so that the two give up at the same place.

import type {
  Binary,
  Node as ExprNode,
  Lit,
} from "@storycreep/shared/expr/compile";

/**
 * The most nodes that a prover's proofs compare, so that the choices of
 * one element cost a bounded time, however many of them share a label and
 * however large their conditions are; a proof that needs more than the
 * steps left proves nothing, and the conditions may then overlap.
 */
export const MAX_STEPS = 10_000;

/**
 * Proves conditions exclusive within its steps, which its proofs share: the
 * rule of duplicate choices proves the pairs of one element's choices with
 * one prover.
 */
export class Prover {
  private steps = MAX_STEPS;

  /**
   * Reports whether the conditions a and b never hold together, as far as a
   * few patterns prove it within the prover's steps, each a condition's
   * AST, or undefined for none, which always holds. They are exclusive when
   * one is the literal false, which needs no step; when one is the negation
   * of the other; when they compare the same operand with literals, or
   * negative numbers, of values that no value meets both comparisons of,
   * with numbers compared as real numbers, and strings in the order of
   * their code points; when they compare the same two operands with
   * complementary operators; when one is a conjunction one of whose terms
   * is exclusive with the other; and when one is a disjunction both of
   * whose terms are.
   */
  exclusive(a: ExprNode | undefined, b: ExprNode | undefined): boolean {
    if (a === undefined || b === undefined) {
      return (a !== undefined && never(a)) || (b !== undefined && never(b));
    }
    return this.prove(a, b);
  }

  // spend takes a step, and reports whether there was one left.
  private spend(): boolean {
    this.steps--;
    return this.steps >= 0;
  }

  // prove reports whether the conditions a and b never hold together,
  // trying each pattern of the conditions whole, a disjunction's among
  // them, so that a disjunction whose terms are not each exclusive with the
  // other condition may still be its negation, or a term of its
  // conjunction.
  private prove(a: ExprNode, b: ExprNode): boolean {
    if (never(a) || never(b)) {
      return true;
    }
    if (!this.spend()) {
      return false;
    }
    if (a.kind === "binary") {
      if (a.op === "or" && this.prove(a.left, b) && this.prove(a.right, b)) {
        return true;
      }
      if (a.op === "and" && (this.prove(a.left, b) || this.prove(a.right, b))) {
        return true;
      }
    }
    if (b.kind === "binary") {
      if (b.op === "or" && this.prove(a, b.left) && this.prove(a, b.right)) {
        return true;
      }
      if (b.op === "and" && (this.prove(a, b.left) || this.prove(a, b.right))) {
        return true;
      }
    }
    return this.negation(a, b) || this.negation(b, a) || this.comparisons(a, b);
  }

  // negation reports whether a is the negation of b.
  private negation(a: ExprNode, b: ExprNode): boolean {
    return a.kind === "unary" && a.op === "not" && this.same(a.operand, b);
  }

  // comparisons reports whether a and b are comparisons that no value
  // meets both of: of the same two operands, by complementary operators,
  // or of the same operand with literals, whose sets of values do not meet.
  private comparisons(a: ExprNode, b: ExprNode): boolean {
    if (a.kind !== "binary" || b.kind !== "binary") {
      return false;
    }
    if (!isComparison(a.op) || !isComparison(b.op)) {
      return false;
    }
    if (this.same(a.left, b.left) && this.same(a.right, b.right)) {
      if (complements[a.op] === b.op) {
        return true;
      }
    } else if (this.same(a.left, b.right) && this.same(a.right, b.left)) {
      if (complements[a.op] === flipped[b.op]) {
        return true;
      }
    }
    const x = literalSide(a);
    const y = literalSide(b);
    if (
      x === undefined ||
      y === undefined ||
      !this.same(x.operand, y.operand)
    ) {
      return false;
    }
    return disjoint(x.op, x.lit, y.op, y.lit);
  }

  // same reports whether a and b are the same expression: of the same
  // kinds, operators, functions, literals, and the IDs of what they name,
  // which hold the same value in one state, as the conditions are pure.
  private same(a: ExprNode, b: ExprNode): boolean {
    if (!this.spend()) {
      return false;
    }
    switch (a.kind) {
      case "lit":
        return b.kind === "lit" && a.type === b.type && a.value === b.value;
      case "var":
        return b.kind === "var" && a.ref !== "" && a.ref === b.ref;
      case "attr":
        return (
          b.kind === "attr" &&
          a.ref !== "" &&
          a.ref === b.ref &&
          a.attribute === b.attribute
        );
      case "node_ref":
        return b.kind === "node_ref" && a.ref === b.ref;
      case "unary":
        return (
          b.kind === "unary" && a.op === b.op && this.same(a.operand, b.operand)
        );
      case "binary":
        return (
          b.kind === "binary" &&
          a.op === b.op &&
          this.same(a.left, b.left) &&
          this.same(a.right, b.right)
        );
      case "call": {
        if (
          b.kind !== "call" ||
          a.function !== b.function ||
          a.args.length !== b.args.length
        ) {
          return false;
        }
        for (let i = 0; i < a.args.length; i++) {
          if (!this.same(a.args[i], b.args[i])) {
            return false;
          }
        }
        return true;
      }
    }
    return false;
  }
}

// never reports whether the condition n never holds: the literal false.
function never(n: ExprNode): boolean {
  return n.kind === "lit" && n.type === "bool" && !n.value;
}

/** The comparisons, which hold exactly when their complement does not. */
type Comparison = "eq" | "ne" | "lt" | "lte" | "gt" | "gte";

function isComparison(op: string): op is Comparison {
  return Object.hasOwn(complements, op);
}

const complements: Readonly<Record<Comparison, Comparison>> = {
  eq: "ne",
  ne: "eq",
  lt: "gte",
  gte: "lt",
  lte: "gt",
  gt: "lte",
};

// The comparisons as they read with their operands swapped, as x < 3 is
// 3 > x.
const flipped: Readonly<Record<Comparison, Comparison>> = {
  eq: "eq",
  ne: "ne",
  lt: "gt",
  gt: "lt",
  lte: "gte",
  gte: "lte",
};

// literalSide returns a comparison c as its other operand, its operator as
// it reads with the operand first, and its literal, if one of its operands
// is a literal or a negative number (literal), or undefined.
function literalSide(
  c: Binary,
): { operand: ExprNode; op: Comparison; lit: Lit } | undefined {
  const op = c.op as Comparison;
  const right = literal(c.right);
  if (right !== undefined) {
    return { operand: c.left, op, lit: right };
  }
  const left = literal(c.left);
  if (left !== undefined) {
    return { operand: c.right, op: flipped[op], lit: left };
  }
  return undefined;
}

// literal returns n as a literal: n itself, if it is one, or, if it is a
// negative number, the unary - of a number's literal (§5.8.1), the literal
// of its value; otherwise undefined.
function literal(n: ExprNode): Lit | undefined {
  if (n.kind === "lit") {
    return n;
  }
  if (
    n.kind === "unary" &&
    n.op === "neg" &&
    n.operand.kind === "lit" &&
    (n.operand.type === "int" || n.operand.type === "float")
  ) {
    return { ...n.operand, value: -n.operand.value };
  }
  return undefined;
}

// disjoint reports whether no value v meets both v op1 l1 and v op2 l2: of
// numbers, of either type, as real numbers; of strings, in the order of
// their code points (§5.8.3); and of booleans, of which there are two.
// Literals of different types may meet.
function disjoint(op1: Comparison, l1: Lit, op2: Comparison, l2: Lit): boolean {
  const num1 = l1.type === "int" || l1.type === "float";
  const num2 = l2.type === "int" || l2.type === "float";
  if (num1 && num2) {
    return apart(
      op1,
      l1.value as number,
      op2,
      l2.value as number,
      Number.NEGATIVE_INFINITY,
      compareNumbers,
    );
  }
  if (l1.type !== l2.type) {
    return false;
  }
  if (l1.type === "string" && l2.type === "string") {
    return apart(op1, l1.value, op2, l2.value, "", compareCodePoints);
  }
  if (l1.type === "bool" && l2.type === "bool") {
    for (const v of [false, true]) {
      if (meets(op1, v, l1.value) && meets(op2, v, l2.value)) {
        return false;
      }
    }
    return true;
  }
  return false;
}

// meets reports whether the boolean v may meet v op l: by == and !=, as
// they read, and by any other operator, which a boolean is never compared
// by, as far as the prover knows.
function meets(op: Comparison, v: boolean, l: boolean): boolean {
  switch (op) {
    case "eq":
      return v === l;
    case "ne":
      return v !== l;
  }
  return true;
}

type Compare<T> = (a: T, b: T) => number;

function compareNumbers(a: number, b: number): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Compares two strings in the order of their code points, as Go compares their UTF-8 bytes. */
export function compareCodePoints(a: string, b: string): number {
  const x = a[Symbol.iterator]();
  const y = b[Symbol.iterator]();
  for (;;) {
    const p = x.next();
    const q = y.next();
    if (p.done || q.done) {
      return p.done && q.done ? 0 : p.done ? -1 : 1;
    }
    const d = (p.value.codePointAt(0) ?? 0) - (q.value.codePointAt(0) ?? 0);
    if (d !== 0) {
      return d < 0 ? -1 : 1;
    }
  }
}

// apart reports whether no value v of an order, the real numbers or the
// strings, whose values are least or above it, meets both v op1 x and v op2
// y: two comparisons by != always meet, as the order has more than two
// values; one by != and another meet unless the other holds no value but
// the one that != leaves out; and two others meet unless their intervals
// do not.
function apart<T>(
  op1: Comparison,
  x: T,
  op2: Comparison,
  y: T,
  least: T,
  cmp: Compare<T>,
): boolean {
  if (op1 === "ne" && op2 === "ne") {
    return false;
  }
  if (op1 === "ne") {
    return only(interval(op2, y, least), x, cmp);
  }
  if (op2 === "ne") {
    return only(interval(op1, x, least), y, cmp);
  }
  return empty(
    meet(interval(op1, x, least), interval(op2, y, least), cmp),
    cmp,
  );
}

// A span is an interval of an order: from lo to hi, each bound open or
// closed, or from lo on, without end, where above.
interface Span<T> {
  lo: T;
  hi: T;
  loOpen: boolean;
  hiOpen: boolean;
  above: boolean;
}

// interval returns the interval of the values v that meet v op n, of an
// order whose values are least or above it. An interval without end keeps
// least as its hi, which nothing reads, as Go keeps the zero value.
function interval<T>(op: Comparison, n: T, least: T): Span<T> {
  const none = { loOpen: false, hiOpen: false, above: false };
  switch (op) {
    case "lt":
      return { ...none, lo: least, hi: n, hiOpen: true };
    case "lte":
      return { ...none, lo: least, hi: n };
    case "gt":
      return { ...none, lo: n, hi: least, loOpen: true, above: true };
    case "gte":
      return { ...none, lo: n, hi: least, above: true };
  }
  return { ...none, lo: n, hi: n };
}

// meet returns the interval of the values that both s and t hold.
function meet<T>(s: Span<T>, t: Span<T>, cmp: Compare<T>): Span<T> {
  const out = { ...s };
  const lo = cmp(t.lo, out.lo);
  if (lo > 0 || (lo === 0 && t.loOpen)) {
    out.lo = t.lo;
    out.loOpen = t.loOpen;
  }
  const hi = cmp(t.hi, out.hi);
  if (out.above || (!t.above && (hi < 0 || (hi === 0 && t.hiOpen)))) {
    out.hi = t.hi;
    out.hiOpen = t.hiOpen;
    out.above = t.above;
  }
  return out;
}

// empty reports whether s holds no value.
function empty<T>(s: Span<T>, cmp: Compare<T>): boolean {
  const d = cmp(s.lo, s.hi);
  return !s.above && (d > 0 || (d === 0 && (s.loOpen || s.hiOpen)));
}

// only reports whether s holds no value but v, if it holds any.
function only<T>(s: Span<T>, v: T, cmp: Compare<T>): boolean {
  return (
    empty(s, cmp) || (!s.above && cmp(s.lo, v) === 0 && cmp(s.hi, v) === 0)
  );
}
