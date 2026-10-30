// The evaluator of the expression language (PRD §5.8.4–§5.8.7): the
// TypeScript twin of the Go package server/internal/expr's, which gives the
// same value and the same runtime errors for the same AST, as the cases of
// schema/expr-corpus and the conformance suite check.

import type { Assign, Binary, BinaryOp, Node, Script } from "./ast";
import { BUILTINS, takes } from "./builtins";
import {
  addFloat,
  addInt,
  ceil,
  compare,
  concat,
  contains,
  divFloat,
  divInt,
  endsWith,
  floor,
  len,
  modInt,
  mulFloat,
  mulInt,
  negFloat,
  negInt,
  parseFloatText,
  parseIntText,
  type Result,
  round,
  type SemanticsError,
  startsWith,
  subFloat,
  subInt,
  trunc,
} from "./semantics";
import { lower, trim, upper } from "./unicode";
import {
  asFloat,
  boolValue,
  floatValue,
  intValue,
  NONE,
  stringValue,
  type Value,
  valueString,
} from "./value";

/** The runtime errors that an evaluation reports (§5.8.7). */
export type EvaluationError = SemanticsError | "step_budget_exceeded";

/**
 * The state that an evaluation reads and changes (PRD §5.8.5): the values of
 * the variables and of the components' runtime attributes, by their IDs, of
 * their types, an enum's as a string, and how many times the flow has
 * entered each node, and what it reports to: the signals that a script
 * emits, and the runtime errors that an evaluation meets (§5.8.7). A
 * runtime implements it, as the flow's Runtime does.
 */
export interface Env {
  /** Returns the value of the variable whose ID is id. */
  variable(id: string): Value;
  /** Sets it to v, of its type. */
  setVariable(id: string, v: Value): void;
  /** Returns the value of the runtime attribute of the component. */
  attribute(component: string, attribute: string): Value;
  /** Sets it to v, of its type, an enum's one of its options. */
  setAttribute(component: string, attribute: string, v: Value): void;
  /** Returns how many times the flow has entered the node, 0 for none. */
  visits(node: string): number;
  /** Reports the signal that a script emits, with its value, or undefined for none. */
  signal(name: string, value: Value | undefined): void;
  /**
   * Reports the runtime error code, which the evaluation goes on after, but
   * for step_budget_exceeded.
   */
  error(code: EvaluationError): void;
}

/**
 * The evaluation step budget of an expression (§5.8.6): the most steps that
 * condition and run take of one AST, each node of it that they reach being
 * one, whether they evaluate it or, as an assignment's target, read it, but
 * for an operand of && or || that is not evaluated.
 */
export const MAX_STEPS = 10_000;

// exceeded is what an evaluation throws once it has taken MAX_STEPS steps,
// which condition and run catch.
const exceeded = Symbol("step budget exceeded");

/**
 * Evaluates n, the AST of a condition, against env, and returns its value
 * (§5.8.5): false when its evaluation exceeds MAX_STEPS, which it reports as
 * step_budget_exceeded. An operation that fails reports its runtime error,
 * as a division by zero, and yields the zero value of its result's type,
 * which the evaluation goes on with, so that 1 / 0 + 5 is 5 (§5.8.7). && and
 * || evaluate their right operand only when the left one does not decide the
 * result. n is an AST that check or checkResolved accepted as a condition's,
 * its names resolved; of any other, what condition returns is not defined,
 * but it returns once it has taken at most MAX_STEPS steps.
 */
export function condition(n: Node, env: Env): boolean {
  const e = new Evaluator(env);
  try {
    return e.eval(n).bool;
  } catch (x) {
    e.stop(x);
    return false;
  }
}

/**
 * Evaluates n, an expression's AST, against env, and returns its value, as
 * condition does, or NONE when its evaluation exceeds MAX_STEPS, which it
 * reports as step_budget_exceeded: the value of a text value reference, or
 * of a part of a condition, as play mode shows it (FR-PLAY-03).
 */
export function evaluate(n: Node, env: Env): Value {
  const e = new Evaluator(env);
  try {
    return e.eval(n);
  } catch (x) {
    e.stop(x);
    return NONE;
  }
}

/**
 * Runs s, the AST of a script, against env (§5.8.5): its statements in
 * order, each assignment of its value, as condition evaluates one, to its
 * target, and each signal reported. An assignment's value is of its target's
 * type: an int is widened for a float, and op= is op of the target's value
 * and the assignment's (§5.8.3). When the evaluation exceeds MAX_STEPS, run
 * reports step_budget_exceeded, and the statement that it exceeded them in,
 * and those after it, have no effect.
 */
export function run(s: Script, env: Env): void {
  const e = new Evaluator(env);
  try {
    if (s === null || s === undefined) {
      return;
    }
    e.step();
    for (const st of s.statements) {
      if (st?.kind === "assign") {
        e.assign(st);
      } else if (st?.kind === "call") {
        e.eval(st);
      }
    }
  } catch (x) {
    e.stop(x);
  }
}

// The binary operators of each op=, by it, as the Go evaluator's map holds
// them, so that an assignment of any other op, as a malformed node's, named
// after a member of Object.prototype, as constructor, too, assigns its value
// as = does.
const assignOperators: ReadonlyMap<string, BinaryOp> = new Map<
  Assign["op"],
  BinaryOp
>([
  ["+=", "add"],
  ["-=", "sub"],
  ["*=", "mul"],
  ["/=", "div"],
  ["%=", "mod"],
]);

// An evaluation of a built-in's call, of the arguments that check accepted
// of a call of it, their number among them.
type Evaluation = (e: Evaluator, args: Node[]) => Value;

// Evaluator evaluates an AST against its env, counting its steps.
class Evaluator {
  private steps = 0;

  constructor(readonly env: Env) {}

  // step counts a step of the evaluation, and stops it beyond MAX_STEPS.
  step(): void {
    if (++this.steps > MAX_STEPS) {
      throw exceeded;
    }
  }

  // stop reports an evaluation that exceeded its steps, and throws any
  // other error on.
  stop(x: unknown): void {
    if (x !== exceeded) {
      throw x;
    }
    this.env.error("step_budget_exceeded");
  }

  // report reports the runtime error code, if it is one.
  private report(code: SemanticsError | undefined): void {
    if (code !== undefined) {
      this.env.error(code);
    }
  }

  // intResult returns the value of an operation of an int result, having
  // reported its runtime error, and floatResult of a float result.
  intResult([n, code]: Result<number>): Value {
    this.report(code);
    return intValue(n);
  }

  floatResult([f, code]: Result<number>): Value {
    this.report(code);
    return floatValue(f);
  }

  // eval evaluates the expression n and returns its value, or none of a
  // node that is no expression.
  eval(n: Node): Value {
    if (typeof n !== "object" || n === null) {
      return NONE;
    }
    this.step();
    switch (n.kind) {
      case "lit":
        switch (n.type) {
          case "bool":
            return boolValue(n.value);
          case "int":
            return intValue(n.value);
          case "float":
            return floatValue(n.value);
          case "string":
            return stringValue(n.value);
        }
        return NONE;
      case "var":
        return this.env.variable(n.ref);
      case "attr":
        return this.env.attribute(n.ref, n.attribute);
      case "unary": {
        const v = this.eval(n.operand);
        if (n.op === "not") {
          return boolValue(!v.bool);
        }
        if (v.type === "int") {
          return this.intResult(negInt(v.int));
        }
        return this.floatResult(negFloat(asFloat(v)));
      }
      case "binary":
        return this.binary(n);
      case "call": {
        const b = BUILTINS.get(n.function);
        const f = evaluations.get(n.function);
        if (b === undefined || f === undefined || !takes(b, n.args.length)) {
          return NONE;
        }
        return f(this, n.args);
      }
    }
    return NONE;
  }

  // binary evaluates the binary operation n, its left operand first, as the
  // types of its operands say (§5.8.3): an int beside a float is widened, +
  // of two strings concatenates them, and && and || short-circuit.
  private binary(n: Binary): Value {
    const l = this.eval(n.left);
    switch (n.op) {
      case "and":
        return boolValue(l.bool ? this.eval(n.right).bool : false);
      case "or":
        return boolValue(l.bool ? true : this.eval(n.right).bool);
    }
    const r = this.eval(n.right);
    switch (n.op) {
      case "eq":
        return boolValue(order(l, r) === 0);
      case "ne":
        return boolValue(order(l, r) !== 0);
      case "lt":
        return boolValue(order(l, r) < 0);
      case "lte":
        return boolValue(order(l, r) <= 0);
      case "gt":
        return boolValue(order(l, r) > 0);
      case "gte":
        return boolValue(order(l, r) >= 0);
    }
    return this.arithmetic(n.op, l, r);
  }

  // arithmetic applies the arithmetic operator op, +, -, *, /, or %, to l and
  // r: of two ints an int, of a string + a string their concatenation, and
  // otherwise a float, an int widened, each by its operation of the
  // semantics, which reports its runtime error and yields its zero value
  // (§5.8.9).
  private arithmetic(op: BinaryOp, l: Value, r: Value): Value {
    if (l.type === "string") {
      const [s, code] = concat(l.str, r.str);
      this.report(code);
      return stringValue(s);
    }
    if (l.type === "int" && r.type === "int") {
      switch (op) {
        case "add":
          return this.intResult(addInt(l.int, r.int));
        case "sub":
          return this.intResult(subInt(l.int, r.int));
        case "mul":
          return this.intResult(mulInt(l.int, r.int));
        case "div":
          return this.intResult(divInt(l.int, r.int));
      }
      return this.intResult(modInt(l.int, r.int));
    }
    const x = asFloat(l);
    const y = asFloat(r);
    switch (op) {
      case "add":
        return this.floatResult(addFloat(x, y));
      case "sub":
        return this.floatResult(subFloat(x, y));
      case "mul":
        return this.floatResult(mulFloat(x, y));
    }
    return this.floatResult(divFloat(x, y));
  }

  // assign runs the assignment a: it reads its target, evaluates its value,
  // and sets the target to the value, or, of op=, to op of the two, of the
  // target's type, an int widened for a float (§5.8.3).
  assign(a: Assign): void {
    this.step();
    const t = a.target;
    let target: Value;
    if (t?.kind === "var") {
      this.step();
      target = this.env.variable(t.ref);
    } else if (t?.kind === "attr") {
      this.step();
      target = this.env.attribute(t.ref, t.attribute);
    } else {
      return;
    }
    let v = this.eval(a.value);
    const op = assignOperators.get(a.op);
    if (op !== undefined) {
      v = this.arithmetic(op, target, v);
    }
    if (target.type === "float" && v.type === "int") {
      v = floatValue(v.int);
    }
    if (t.kind === "var") {
      this.env.setVariable(t.ref, v);
    } else {
      this.env.setAttribute(t.ref, t.attribute, v);
    }
  }

  // numbers returns the widened type's values of args, numbers: ints of
  // ints, and otherwise floats, each int widened, as min, max, and clamp
  // take them, and whether they are ints.
  numbers(args: Node[]): [Value[], boolean] {
    const vs = args.map((a) => this.eval(a));
    const ints = vs.every((v) => v.type === "int");
    return [ints ? vs : vs.map((v) => floatValue(asFloat(v))), ints];
  }

  // pick returns the value of min or max, of the widened type of its two
  // arguments.
  pick(args: Node[], f: (a: number, b: number) => number): Value {
    const [vs, ints] = this.numbers(args);
    return ints
      ? intValue(f(vs[0].int, vs[1].int))
      : floatValue(f(vs[0].float, vs[1].float));
  }

  // rounding returns the value of floor, ceil, or round of a number: an int
  // is itself, and a float rounded by f, which reports integer_overflow
  // beyond ±(2^53 − 1).
  rounding(args: Node[], f: (x: number) => Result<number>): Value {
    const v = this.eval(args[0]);
    return v.type === "int" ? v : this.intResult(f(v.float));
  }

  // text returns the string of args[i].
  text(args: Node[], i: number): string {
    return this.eval(args[i]).str;
  }

  // node returns the ID of the node that args[0] references, a node
  // reference, which it counts as a step, or "" of anything else.
  node(args: Node[]): string {
    this.step();
    const r = args[0];
    return r?.kind === "node_ref" ? r.ref : "";
  }
}

// order returns -1, 0, or +1 as l is less than, equal to, or greater than r,
// two values that the language compares: numbers by their values, an int
// beside a float widened, strings by their code points, and bools, which only
// == and != compare, false before true.
function order(l: Value, r: Value): number {
  if (l.type === "int" && r.type === "int") {
    return Math.sign(l.int - r.int);
  }
  if (l.type === "string") {
    return compare(l.str, r.str);
  }
  if (l.type === "bool") {
    return l.bool === r.bool ? 0 : r.bool ? -1 : 1;
  }
  const x = asFloat(l);
  const y = asFloat(r);
  return x < y ? -1 : x > y ? 1 : 0;
}

const text =
  (f: (s: string) => string): Evaluation =>
  (e, args) =>
    stringValue(f(e.text(args, 0)));

const predicate =
  (f: (s: string, t: string) => boolean): Evaluation =>
  (e, args) => {
    const s = e.text(args, 0);
    return boolValue(f(s, e.text(args, 1)));
  };

// evaluations are the built-ins' evaluations (§5.8.4), by their names, one
// of each of BUILTINS, which a test holds them to.
export const evaluations: ReadonlyMap<string, Evaluation> = new Map<
  string,
  Evaluation
>([
  ["min", (e, args) => e.pick(args, Math.min)],
  ["max", (e, args) => e.pick(args, Math.max)],
  [
    "abs",
    (e, args) => {
      const v = e.eval(args[0]);
      return v.type === "int"
        ? intValue(Math.abs(v.int))
        : floatValue(Math.abs(asFloat(v)));
    },
  ],
  ["floor", (e, args) => e.rounding(args, floor)],
  ["ceil", (e, args) => e.rounding(args, ceil)],
  ["round", (e, args) => e.rounding(args, round)],
  // clamp is min(max(x, lo), hi), so that it is hi when lo is greater than
  // hi.
  [
    "clamp",
    (e, args) => {
      const [vs, ints] = e.numbers(args);
      return ints
        ? intValue(Math.min(Math.max(vs[0].int, vs[1].int), vs[2].int))
        : floatValue(Math.min(Math.max(vs[0].float, vs[1].float), vs[2].float));
    },
  ],
  [
    "to_int",
    (e, args) => {
      const v = e.eval(args[0]);
      switch (v.type) {
        case "int":
          return v;
        case "string":
          return e.intResult(parseIntText(v.str));
      }
      return e.intResult(trunc(v.float));
    },
  ],
  [
    "to_float",
    (e, args) => {
      const v = e.eval(args[0]);
      return v.type === "string"
        ? e.floatResult(parseFloatText(v.str))
        : floatValue(asFloat(v));
    },
  ],
  ["to_string", (e, args) => stringValue(valueString(e.eval(args[0])))],
  ["len", (e, args) => intValue(len(e.text(args, 0)))],
  ["contains", predicate(contains)],
  ["starts_with", predicate(startsWith)],
  ["ends_with", predicate(endsWith)],
  ["lower", text(lower)],
  ["upper", text(upper)],
  ["trim", text(trim)],
  ["visited", (e, args) => boolValue(e.env.visits(e.node(args)) > 0)],
  ["visits", (e, args) => intValue(e.env.visits(e.node(args)))],
  // signal reports the signal of its first argument's name, a string
  // literal, with its second argument's value, if it has one, and is no
  // value itself.
  [
    "signal",
    (e, args) => {
      const name = e.text(args, 0);
      if (args.length === 1) {
        e.env.signal(name, undefined);
        return NONE;
      }
      const v = e.eval(args[1]);
      e.env.signal(name, v);
      return NONE;
    },
  ],
]);
