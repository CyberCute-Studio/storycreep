// Name resolution, type checking, purity, and the sandbox's limits (PRD
// §5.8.3–§5.8.6): the TypeScript twin of the Go package
// server/internal/expr's checker, which compiles every source alike, as
// schema/expr-corpus/compile.json checks.

import { isCanonicalId } from "../ids";
import {
  type AssignOp,
  type Attr,
  type Binary,
  type Call,
  type Context,
  MAX_ARGUMENTS,
  MAX_DEPTH,
  MAX_NODES,
  MAX_SIGNALS,
  MAX_STATEMENTS,
  type Node,
  type Range,
  type Var,
} from "./ast";
import {
  accepts,
  BUILTINS,
  TY_BOOL,
  TY_INT,
  TY_NODE,
  TY_NUMBER,
  TY_STRING,
  TY_VALUE,
  type TypeSet,
  takes,
  typeNameOf,
  typeNamesOf,
  typeOf,
  widened,
} from "./builtins";
import { literalProblem } from "./encode";
import { checkContext, ExprError, UsageError } from "./error";
import { parse } from "./parser";
import {
  type Attribute,
  type ComponentEntry,
  Project,
  type Variable,
} from "./project";
import { len } from "./semantics";
import {
  ASSIGN_OPS,
  BINARY_OPS,
  checkPlace,
  eachChild,
  type Place,
  UNARY_OPS,
} from "./shape";
import { near } from "./suggest";

/**
 * Reads source as the source of a condition or a script, as ctx says
 * (parse), and checks its AST against the project p, as an expression of the
 * board whose ID is board (check). It returns the AST, its names resolved,
 * or throws the first ExprError of the source: the parser's, or else the
 * checker's.
 */
export function compile(
  source: string,
  ctx: Context,
  p: Project | undefined,
  board: string,
): Node {
  const n = parse(source, ctx);
  check(n, ctx, p, board);
  return n;
}

/**
 * Checks n, the AST of a condition or a script, as ctx says, against the
 * project p, as an expression of the board whose ID is board, or of none for
 * "" (PRD §5.8.3–§5.8.6). It throws the first ExprError that it meets, or
 * returns once it has resolved every name of n: it sets each variable's ref,
 * and each attribute's ref and attribute, to their entities' IDs, and their
 * name to their keys, as the AST's JSON writes them (§5.8.8). It changes
 * nothing of n when it throws. An undefined p names nothing.
 *
 * It meets errors in two passes. The first reads n against the sandbox's
 * limits (§5.8.6), each node before its children, and the children in order:
 * at most MAX_NODES nodes, none deeper than MAX_DEPTH, the root 1 deep, at
 * most MAX_STATEMENTS statements of a script, MAX_ARGUMENTS arguments of a
 * call, and MAX_SIGNALS calls of signal in a script. The second resolves
 * names and checks types and purity in order from left to right.
 * schema/expr-corpus/README.md says which error each of them is, and which
 * comes first.
 *
 * It throws a UsageError for a ctx that is neither condition nor script, a
 * board that is not a board's ID, and an n that holds what no AST that parse
 * returns holds, which the first pass meets at its node, in its order, as it
 * meets the limits' errors.
 */
export function check(
  n: Node,
  ctx: Context,
  p: Project | undefined,
  board: string,
): void {
  checkAST(n, ctx, p, board, false);
}

/**
 * Checks n, an AST whose names are resolved, as decode returns a published
 * one, as check checks the AST that parse returns, but for how it reads a
 * name: by its refs, as a runtime's loader checks the ASTs of a snapshot
 * against the snapshot's declarations, rather than anew by its keys, which
 * may name another entity. A variable's ref names a variable of p, global or
 * of the board, or the error is unknown_identifier, or
 * board_variable_out_of_scope for another board's; an attribute's ref names
 * a component of p, or the error is unknown_identifier, and its attribute
 * one of the component's attributes, or the error is unknown_attribute,
 * which is runtime, or the error is attribute_not_runtime; and a node
 * reference's ref a node of p that is no jumper. It sets nothing of n.
 */
export function checkResolved(
  n: Node,
  ctx: Context,
  p: Project | undefined,
  board: string,
): void {
  checkAST(n, ctx, p, board, true);
}

// checkAST is check, which reads each name by its keys, or checkResolved, by
// its refs, as refs says.
function checkAST(
  n: Node,
  ctx: Context,
  p: Project | undefined,
  board: string,
  refs: boolean,
): void {
  checkContext(ctx);
  if (board !== "" && !isCanonicalId("board", board)) {
    throw new UsageError("board", `not a board's ID: ${JSON.stringify(board)}`);
  }
  const isScript =
    typeof n === "object" &&
    n !== null &&
    (n as { kind?: unknown }).kind === "script";
  if (isScript !== (ctx === "script")) {
    throw new UsageError(
      "ast",
      `not an AST of the language: a ${ctx}'s AST is a script exactly when its context is`,
    );
  }
  limits(n, ctx);
  const c = new Checker(ctx, p ?? Project.EMPTY, board, refs);
  c.root(n);
  for (const r of c.resolved) {
    r.n.name = r.name;
    r.n.ref = r.ref;
    if (r.n.kind === "attr") {
      r.n.attribute = r.attribute;
    }
  }
}

function astError(message: string): UsageError {
  return new UsageError("ast", `not an AST of the language: ${message}`);
}

// limits reads n against the sandbox's limits (check), and checks that n
// holds nothing that no AST holds, each node, at its place, as it reaches
// it.
function limits(n: Node, ctx: Context): void {
  const stack: [Node, Place, number][] = [[n, "root", 1]];
  const seen = new Set<Node>();
  let nodes = 0;
  let signals = 0;
  for (let v = stack.pop(); v !== undefined; v = stack.pop()) {
    const [node, at, depth] = v;
    const problem = checkPlace(node, at);
    if (problem !== undefined) {
      throw astError(problem);
    }
    if (seen.has(node)) {
      throw astError("a node is in the AST twice");
    }
    seen.add(node);
    if (++nodes > MAX_NODES) {
      throw new ExprError("ast_too_large", rangeOf(node));
    }
    if (depth > MAX_DEPTH) {
      throw new ExprError("ast_too_deep", rangeOf(node));
    }
    switch (node.kind) {
      case "lit": {
        const lit = literalProblem(node);
        if (lit !== undefined) {
          throw astError(lit);
        }
        break;
      }
      case "var":
        if (
          node.scope !== "" &&
          node.scope !== "global" &&
          node.scope !== "board"
        ) {
          throw astError(
            `${JSON.stringify(node.scope)} is no scope of a variable`,
          );
        }
        break;
      case "unary":
        if (!UNARY_OPS.has(node.op)) {
          throw astError(`${JSON.stringify(node.op)} is no unary operator`);
        }
        break;
      case "binary":
        if (!BINARY_OPS.has(node.op)) {
          throw astError(`${JSON.stringify(node.op)} is no binary operator`);
        }
        break;
      case "call":
        if (!Array.isArray(node.args)) {
          throw astError("a call's arguments are not a list");
        }
        if (node.args.length > MAX_ARGUMENTS) {
          beyond("too_many_arguments", node.args[MAX_ARGUMENTS], "operand");
        }
        if (node.function === "signal" && ctx === "script") {
          if (++signals > MAX_SIGNALS) {
            throw new ExprError("too_many_signals", rangeOf(node));
          }
        }
        break;
      case "assign":
        if (!ASSIGN_OPS.has(node.op)) {
          throw astError(
            `${JSON.stringify(node.op)} is no assignment operator`,
          );
        }
        break;
      case "script":
        if (!Array.isArray(node.statements)) {
          throw astError("a script's statements are not a list");
        }
        if (node.statements.length > MAX_STATEMENTS) {
          beyond(
            "too_many_statements",
            node.statements[MAX_STATEMENTS],
            "statement",
          );
        }
        break;
    }
    // The children are read in order, the first of them next.
    const from = stack.length;
    eachChild(node, (child, place) => stack.push([child, place, depth + 1]));
    const children = stack.splice(from).reverse();
    stack.push(...children);
  }
}

// beyond throws the error code over n, the statement or the argument beyond
// a limit, at the place at, or a UsageError if n cannot be there.
function beyond(
  code: "too_many_arguments" | "too_many_statements",
  n: Node,
  at: Place,
): never {
  const problem = checkPlace(n, at);
  if (problem !== undefined) {
    throw astError(problem);
  }
  throw new ExprError(code, rangeOf(n));
}

// rangeOf returns where n is, or the empty range at 0 of a node without one,
// as one that decode returns.
function rangeOf(n: Node): Range {
  return n.src ?? { start: 0, end: 0 };
}

// mismatch returns the type_mismatch of the operand n, of the type got, at a
// place that takes the types want.
function mismatch(n: Node, got: TypeSet, want: TypeSet): ExprError {
  return new ExprError("type_mismatch", rangeOf(n), {
    type: typeNameOf(got),
    expected: typeNamesOf(want),
  });
}

// comparedWith returns the types that a value of the type t is compared
// with: numbers with numbers, after widening, and any other with its own
// type.
function comparedWith(t: TypeSet): TypeSet {
  return (t & TY_NUMBER) !== 0 ? TY_NUMBER : t;
}

// targets are the types of the targets that each assignment operator takes:
// any value's of =, those that its operator takes of op=.
const targets: Record<AssignOp, TypeSet> = {
  "=": TY_VALUE,
  "+=": TY_NUMBER | TY_STRING,
  "-=": TY_NUMBER,
  "*=": TY_NUMBER,
  "/=": TY_NUMBER,
  "%=": TY_INT,
};

// written returns the keys as names written with the qualifier scope.
function written(scope: Var["scope"], keys: string[]): string[] {
  return scope === "" ? keys : keys.map((k) => `${scope}.${k}`);
}

// runtimeAttribute returns the runtime attribute of the key of c, or
// undefined, as of an undefined c.
function runtimeAttribute(
  c: ComponentEntry | undefined,
  key: string,
): Attribute | undefined {
  const a = c?.byKey.get(key);
  return a?.runtime ? a : undefined;
}

// A resolution is what check sets of n, a Var or an Attr: its name, its
// ref, and an Attr's attribute, "" of a Var.
interface Resolution {
  n: Var | Attr;
  name: string;
  ref: string;
  attribute: string;
}

// Checker is check's second pass, which resolves names and checks types and
// purity, of an AST that the first pass, limits, has read, so that it is a
// tree of at most MAX_NODES nodes, at most MAX_DEPTH deep, of the language's
// kinds at their places.
class Checker {
  readonly resolved: Resolution[] = [];

  constructor(
    private readonly ctx: Context,
    private readonly p: Project,
    private readonly board: string,
    // refs is set for checkResolved, which reads each name by its refs.
    private readonly refs: boolean,
  ) {}

  // root checks the AST n: a script's statements, or a condition's
  // expression, which is a bool.
  root(n: Node): void {
    if (n.kind === "script") {
      for (const st of n.statements) {
        if (st.kind === "assign") {
          this.assign(st);
        } else if (st.kind === "call") {
          this.call(st, true);
        }
      }
      return;
    }
    this.operand(n, TY_BOOL);
  }

  // operand checks n, and then that its type is one of want, and returns
  // it.
  private operand(n: Node, want: TypeSet): TypeSet {
    const t = this.expression(n);
    if ((t & want) === 0) {
      throw mismatch(n, t, want);
    }
    return t;
  }

  // expression checks the expression n, and returns its type.
  private expression(n: Node): TypeSet {
    switch (n.kind) {
      case "lit":
        return typeOf(n.type);
      case "var":
        return typeOf(this.variable(n).type);
      case "attr":
        return typeOf(this.attribute(n).type);
      case "node_ref":
        this.nodeRef(n.ref, rangeOf(n));
        return TY_NODE;
      case "unary":
        return this.operand(n.operand, n.op === "not" ? TY_BOOL : TY_NUMBER);
      case "binary":
        return this.binary(n);
      case "call":
        return this.call(n, false);
    }
    return 0;
  }

  // binary checks the binary operation n, its left operand and then its
  // right, and returns its type (§5.8.3). The left operand's type decides
  // what the right one may be, so that the right one is the operand that a
  // type_mismatch names when the two do not go together.
  private binary(n: Binary): TypeSet {
    switch (n.op) {
      case "or":
      case "and":
        this.operand(n.left, TY_BOOL);
        this.operand(n.right, TY_BOOL);
        return TY_BOOL;
      case "add": {
        const l = this.operand(n.left, TY_NUMBER | TY_STRING);
        if (l === TY_STRING) {
          this.operand(n.right, TY_STRING);
          return TY_STRING;
        }
        return widened(l, this.operand(n.right, TY_NUMBER));
      }
      case "sub":
      case "mul":
      case "div": {
        const l = this.operand(n.left, TY_NUMBER);
        return widened(l, this.operand(n.right, TY_NUMBER));
      }
      case "mod":
        this.operand(n.left, TY_INT);
        this.operand(n.right, TY_INT);
        return TY_INT;
      case "eq":
      case "ne": {
        const l = this.operand(n.left, TY_VALUE);
        this.operand(n.right, comparedWith(l));
        return TY_BOOL;
      }
    }
    // <, <=, >, >=.
    const l = this.operand(n.left, TY_NUMBER | TY_STRING);
    this.operand(n.right, comparedWith(l));
    return TY_BOOL;
  }

  // call checks the call n, a script's statement if statement is set, and
  // otherwise an operand, and returns its type: its function first, the
  // function's purity at its place, and its number of arguments, and then
  // each argument in order.
  private call(n: Call, statement: boolean): TypeSet {
    const b = BUILTINS.get(n.function);
    const src = rangeOf(n);
    if (b === undefined) {
      const name = { start: src.start, end: src.start + len(n.function) };
      throw new ExprError("unknown_function", name, {
        suggestions: near(n.function, this.functions(statement)),
      });
    }
    if (b.effect && this.ctx === "condition") {
      throw new ExprError("condition_must_be_pure", src);
    }
    if (b.effect && !statement) {
      throw new ExprError("signal_not_statement", src);
    }
    if (!b.effect && statement) {
      throw new ExprError("invalid_statement", src);
    }
    if (!takes(b, n.args.length)) {
      throw new ExprError("wrong_argument_count", src);
    }
    const types: TypeSet[] = [];
    n.args.forEach((a, i) => {
      if (b.effect && i === 0) {
        // A signal's name, which is known before the script runs.
        if (a.kind !== "lit" || a.type !== "string" || a.value === "") {
          throw new ExprError("invalid_signal_name", rangeOf(a));
        }
        return;
      }
      types.push(this.operand(a, b.params[i]));
    });
    if (b.effect) {
      // A statement, which has no value.
      return 0;
    }
    return b.result === 0 ? widened(...types) : b.result;
  }

  // functions returns the names of the built-ins that a call can be of at
  // its place, which an unknown_function suggests: one that has an effect as
  // a script's statement, and one that has none anywhere else.
  private functions(statement: boolean): string[] {
    const names: string[] = [];
    for (const [name, b] of BUILTINS) {
      if (b.effect === (statement && this.ctx === "script")) {
        names.push(name);
      }
    }
    return names;
  }

  // assign checks the assignment n, its target and then its value (§5.8.3):
  // the value of = is of the target's type, or an int for a float, and op=
  // is checked as op of the target and the value is, its result of the
  // target's type. An enum attribute is assigned only with = and a string
  // literal of one of its options.
  private assign(n: Node & { kind: "assign" }): void {
    let target = 0;
    let enumAttribute: Attribute | undefined;
    if (n.target.kind === "var") {
      target = typeOf(this.variable(n.target).type);
    } else if (n.target.kind === "attr") {
      const a = this.attribute(n.target);
      target = typeOf(a.type);
      if (a.type === "enum") {
        enumAttribute = a;
      }
    }
    if (enumAttribute !== undefined) {
      this.expression(n.value);
      const v = n.value;
      if (
        n.op !== "=" ||
        v.kind !== "lit" ||
        v.type !== "string" ||
        !(enumAttribute.options ?? []).includes(v.value)
      ) {
        throw new ExprError("invalid_enum_value", rangeOf(n.value));
      }
      return;
    }
    const takesTypes = targets[n.op];
    if ((target & takesTypes) === 0) {
      throw mismatch(n.target, target, takesTypes);
    }
    this.operand(n.value, accepts(target));
  }

  // resolve has check set the name of n to the entity of the IDs ref and
  // attribute, of the name name, once it has met no error.
  private resolve(r: Resolution): void {
    this.resolved.push(r);
  }

  // variable resolves the variable n (§5.8.3): global.<key> names a global
  // variable, board.<key> one of the board, and a bare key the board's, a
  // global one, or else a component, which is no value.
  private variable(n: Var): Variable {
    const src = rangeOf(n);
    if (this.refs) {
      const v = this.p.variable(n.ref);
      if (v === undefined) {
        throw new ExprError("unknown_identifier", src);
      }
      if ((v.board ?? "") !== "" && v.board !== this.board) {
        throw new ExprError("board_variable_out_of_scope", src);
      }
      return v;
    }
    let v: Variable | undefined;
    switch (n.scope) {
      case "global":
        v = this.p.global(n.key);
        break;
      case "board":
        v = this.p.ofBoard(this.board, n.key);
        break;
      default:
        v = this.p.ofBoard(this.board, n.key) ?? this.p.global(n.key);
        if (v === undefined && this.p.component(n.key) !== undefined) {
          throw new ExprError("component_not_value", src);
        }
    }
    if (v !== undefined) {
      this.resolve({ n, name: v.key, ref: v.id, attribute: "" });
      return v;
    }
    if (n.scope !== "global" && this.p.elsewhere(this.board, n.key)) {
      throw new ExprError("board_variable_out_of_scope", src);
    }
    const candidates: string[] = [];
    if (n.scope !== "global") {
      candidates.push(...this.p.boardKeys(this.board));
    }
    if (n.scope !== "board") {
      candidates.push(...this.p.globalKeys());
    }
    throw new ExprError("unknown_identifier", src, {
      suggestions: written(n.scope, near(n.key, candidates)),
    });
  }

  // shadowed reports whether a variable of the board or a global one has the
  // key, which a bare key, and <key>.<attribute>, names rather than a
  // component of the key.
  private shadowed(key: string): boolean {
    return (
      this.p.ofBoard(this.board, key) !== undefined ||
      this.p.global(key) !== undefined
    );
  }

  // attribute resolves the attribute n, which is a runtime one (§5.8.3,
  // FR-COMP-03): <component>.<attribute> names the attribute of the
  // component unless a variable has the component's key, as
  // cmp.<component>.<attribute> names it whatever has.
  private attribute(n: Attr): Attribute {
    const src = rangeOf(n);
    if (this.refs) {
      const comp = this.p.componentById(n.ref);
      if (comp === undefined) {
        throw new ExprError("unknown_identifier", src);
      }
      const a = comp.byId.get(n.attribute);
      if (a === undefined) {
        throw new ExprError("unknown_attribute", src);
      }
      if (!a.runtime) {
        throw new ExprError("attribute_not_runtime", src);
      }
      return a;
    }
    // qualifier is how the name is written before the component's key.
    const qualifier = n.explicit ? "cmp." : "";
    if (!n.explicit && this.shadowed(n.componentKey)) {
      const suggestions =
        runtimeAttribute(this.p.component(n.componentKey), n.attributeKey) !==
        undefined
          ? [`cmp.${n.componentKey}.${n.attributeKey}`]
          : [];
      throw new ExprError("variable_not_component", src, { suggestions });
    }
    const comp = this.p.component(n.componentKey);
    if (comp === undefined) {
      const candidates = this.p
        .componentKeys()
        .filter((key) => n.explicit || !this.shadowed(key));
      throw new ExprError("unknown_identifier", src, {
        suggestions: near(n.componentKey, candidates).map(
          (k) => `${qualifier}${k}.${n.attributeKey}`,
        ),
      });
    }
    const a = comp.byKey.get(n.attributeKey);
    if (a === undefined) {
      const candidates = comp.attributes
        .filter((x) => x.runtime)
        .map((x) => x.key);
      throw new ExprError("unknown_attribute", src, {
        suggestions: near(n.attributeKey, candidates).map(
          (k) => `${qualifier}${n.componentKey}.${k}`,
        ),
      });
    }
    if (!a.runtime) {
      throw new ExprError("attribute_not_runtime", src);
    }
    this.resolve({
      n,
      name: `${comp.key}.${a.key}`,
      ref: comp.id,
      attribute: a.id,
    });
    return a;
  }

  // nodeRef checks the node reference to ref: to a node that the project
  // has, which is not a jumper (§5.8.5).
  private nodeRef(ref: string, src: Range): void {
    const jumper = this.p.node(ref);
    if (jumper === undefined) {
      throw new ExprError("unknown_node", src);
    }
    if (jumper) {
      throw new ExprError("node_ref_jumper", src);
    }
  }
}
