// The plain-language rendering of an expression (PRD FR-EXPR-01 AC4, plan
// note N74): what a compiled AST says, in the user's language, from the
// catalog's phrases, as "`met_elara` is true and `elara.trust` is at least
// 3", generated from the AST, not from the source, so that two sources of
// one AST say the same. A condition is one phrase, and a script a sentence
// for each of its statements. A name is shown as the board's expressions
// name it, and a node by its title. Each operand of an operator, each
// argument of a call, the condition that a not is of, and the value that a
// statement assigns is in parentheses, but a name, a literal, a negative
// number, or a node, and a comparison that is an operand of an and or an
// or, rather than grouped by a precedence, so that no two ASTs of different
// meanings say the same, whatever their phrases' words: a comparison's own
// operands are each a name, a literal, a negative number, a node, or in
// parentheses, so that "A is 1 and B is 2" is of one AST alone.

import {
  type Binary,
  BUILTINS,
  type Call,
  type Node,
  semantics,
  TY_BOOL,
  TY_STRING,
} from "@storycreep/shared/expr/compile";
import { nodeTitleText, valueNamer } from "../content/textNames";
import type { Words } from "./messages";
import type { ScopeSources } from "./scope";

/** A part of a rendering: words, or a name as the source writes it, which the editor shows as code. */
export type Part =
  | { readonly kind: "text"; readonly text: string }
  | { readonly kind: "code"; readonly text: string };

/** What marks a name within a phrase, which expand turns into a part of its own: two characters of Unicode's private use, which no message of the catalog holds. */
const open = "";
const close = "";
const marked = /(\d+)/g;

/** Returns text that the user wrote, as a string's or a node's title, with what marks a name taken for U+FFFD, so that it marks none. */
function clean(text: string): string {
  return text.replace(/[]/g, "�");
}

/** The type of an expression, as the rendering words it: a number of either kind. */
type Shape = "bool" | "number" | "string" | "node";

/** How a locale writes numbers: its decimal separator and its minus sign. */
interface Writing {
  readonly decimal: string;
  readonly minus: string;
}

/** The writing of each locale that a rendering was of, made once. */
const writings = new Map<string, Writing>();

/** Returns how locale writes numbers. */
function writingOf(locale: string): Writing {
  let w = writings.get(locale);
  if (w === undefined) {
    const parts = new Intl.NumberFormat(locale).formatToParts(-1.5);
    w = {
      decimal: parts.find((p) => p.type === "decimal")?.value ?? ".",
      minus: parts.find((p) => p.type === "minusSign")?.value ?? "-",
    };
    writings.set(locale, w);
  }
  return w;
}

/** Returns the number value, of a literal of type, as to_string writes it, an int as its digits and a float as 2.0 or 2.5 (formatInt, formatFloat), of Latin digits, ungrouped, with the locale's decimal separator and minus sign, so that a float that is whole, as of gold / 2.0, reads otherwise than the int, as of gold / 2, which divides otherwise, and an int's digits are written as a float's are, whatever the locale's own. */
function numberText(
  locale: string,
  value: number,
  type: "int" | "float",
): string {
  const w = writingOf(locale);
  return (
    type === "int" ? semantics.formatInt(value) : semantics.formatFloat(value)
  )
    .replace(".", w.decimal)
    .replace("-", w.minus);
}

/** Reports whether n is said as it is wherever it is, of no words that what is around it could run on into: a name, a literal, a negative number, or a node, which no part of a rendering groups. */
function atom(n: Node): boolean {
  return (
    n.kind === "var" ||
    n.kind === "attr" ||
    n.kind === "lit" ||
    n.kind === "node_ref" ||
    (n.kind === "unary" && n.op === "neg" && n.operand.kind === "lit")
  );
}

/** Reports whether n is a comparison, of ==, !=, <, <=, >, or >=. */
function isComparison(n: Node): n is Binary {
  return (
    n.kind === "binary" &&
    (n.op === "eq" ||
      n.op === "ne" ||
      n.op === "lt" ||
      n.op === "lte" ||
      n.op === "gt" ||
      n.op === "gte")
  );
}

/** What renders the expressions of a board. */
class Renderer {
  private readonly names: string[] = [];
  private readonly name: ReturnType<typeof valueNamer>;

  constructor(
    private readonly w: Words,
    private readonly view: ScopeSources,
    private readonly boardId: string,
  ) {
    this.name = valueNamer(view, boardId);
  }

  /** Returns the parts of a phrase. */
  expand(phrase: string): Part[] {
    const parts: Part[] = [];
    let at = 0;
    for (const m of phrase.matchAll(marked)) {
      const index = m.index ?? 0;
      if (index > at) {
        parts.push({ kind: "text", text: phrase.slice(at, index) });
      }
      parts.push({ kind: "code", text: this.names[Number(m[1])] ?? "" });
      at = index + m[0].length;
    }
    if (at < phrase.length) {
      parts.push({ kind: "text", text: phrase.slice(at) });
    }
    return parts;
  }

  /** Returns the mark of a name, which expand turns into its part. */
  private code(text: string): string {
    this.names.push(text);
    return `${open}${this.names.length - 1}${close}`;
  }

  /** Returns the type of n, as the checker found it, of a compiled AST. */
  private shape(n: Node): Shape {
    switch (n.kind) {
      case "lit":
        return n.type === "bool"
          ? "bool"
          : n.type === "string"
            ? "string"
            : "number";
      case "var": {
        const type = this.view.variables.get(n.ref)?.type;
        return type === "bool"
          ? "bool"
          : type === "string"
            ? "string"
            : "number";
      }
      case "attr": {
        const component = this.view.components.get(n.ref);
        const type = component
          ? this.view.componentTypes
              .get(component.component_type_id)
              ?.attributes.find((a) => a.id === n.attribute)?.type
          : undefined;
        return type === "bool"
          ? "bool"
          : type === "int" || type === "float"
            ? "number"
            : "string";
      }
      case "node_ref":
        return "node";
      case "unary":
        return n.op === "not" ? "bool" : "number";
      case "binary":
        switch (n.op) {
          case "add":
            return this.shape(n.left) === "string" ? "string" : "number";
          case "sub":
          case "mul":
          case "div":
          case "mod":
            return "number";
        }
        return "bool";
      case "call": {
        // Of the result that the built-ins' table gives it, a number of
        // either kind, or of the widened type of its arguments, as min's.
        const result = BUILTINS.get(n.function)?.result;
        return result === TY_BOOL
          ? "bool"
          : result === TY_STRING
            ? "string"
            : "number";
      }
    }
    return "bool";
  }

  /** Returns phrase in parentheses. */
  private group(phrase: string): string {
    return this.w.t("expr.plain.group", { inner: phrase });
  }

  /** Returns the phrase of n where a condition is read, as the operand of and, or, or not. */
  condition(n: Node): string {
    const { t } = this.w;
    switch (n.kind) {
      case "var":
      case "attr":
        return t("expr.plain.is_true", { value: this.value(n) });
      case "unary":
        if (n.op === "not") {
          const o = n.operand;
          return o.kind === "var" || o.kind === "attr"
            ? t("expr.plain.is_false", { value: this.value(o) })
            : t("expr.plain.not", { condition: this.side(o) });
        }
        break;
      case "binary":
        if (n.op === "and" || n.op === "or") {
          const left = this.junct(n.left);
          const right = this.junct(n.right);
          return n.op === "and"
            ? t("expr.plain.and", { left, right })
            : t("expr.plain.or", { left, right });
        }
        return this.comparison(n);
      case "call":
        return this.call(n);
    }
    return this.value(n);
  }

  /** Returns the phrase of n as the condition that a not is of: grouped but of a name, or a literal, as "it is not so that (`gold` is more than 1)". */
  private side(n: Node): string {
    return atom(n) ? this.condition(n) : this.group(this.condition(n));
  }

  /** Returns the phrase of n as an operand of an and or an or: a comparison as it is, whose own operands are each grouped but a name, a literal, a negative number, or a node, so that nothing after it is of them, as "`met_elara` is true and `elara.trust` is at least 3", and else as a not's condition, grouped but a name or a literal, as an and or an or within one, "(`a` is 1 and `b` is 2) or `c` is true". */
  private junct(n: Node): string {
    return isComparison(n) ? this.comparison(n) : this.side(n);
  }

  /** Returns the phrase of a comparison, or of arithmetic, which is no condition of its own. */
  private comparison(n: Binary): string {
    const { t } = this.w;
    const left = this.operand(n.left);
    const right = this.operand(n.right);
    switch (n.op) {
      case "eq":
        return t("expr.plain.eq", { left, right });
      case "ne":
        return t("expr.plain.ne", { left, right });
      case "lt":
        return t("expr.plain.lt", { left, right });
      case "lte":
        return t("expr.plain.lte", { left, right });
      case "gt":
        return t("expr.plain.gt", { left, right });
      case "gte":
        return t("expr.plain.gte", { left, right });
    }
    return this.value(n);
  }

  /** Returns the phrase of an operand of a comparison, or the value of an assignment: a name, a literal, a negative number, or a node as it is, whether a condition holds, grouped, as "whether (`gold` is more than 3)", or else a value, grouped. */
  private operand(n: Node): string {
    if (atom(n)) {
      return this.value(n);
    }
    return this.shape(n) === "bool"
      ? this.w.t("expr.plain.whether", {
          condition: this.group(this.condition(n)),
        })
      : this.group(this.value(n));
  }

  /** Returns the phrase of n as an operand of arithmetic, of a minus, or an argument of a call: grouped but of a name, a literal, a negative number, or a node, as "(`gold` minus 3) without its sign". */
  private grouped(n: Node): string {
    return atom(n) ? this.value(n) : this.group(this.value(n));
  }

  /** Returns the phrase of n where a value is read. */
  value(n: Node): string {
    const { t, locale } = this.w;
    switch (n.kind) {
      case "lit":
        switch (n.type) {
          case "bool":
            return n.value ? t("expr.plain.true") : t("expr.plain.false");
          case "string":
            return t("expr.plain.string", { value: clean(n.value) });
        }
        return numberText(locale, n.value, n.type);
      case "var":
        return this.code(this.name(n.ref, undefined) ?? n.key);
      case "attr":
        return this.code(
          this.name(n.ref, n.attribute) ??
            `${n.componentKey}.${n.attributeKey}`,
        );
      case "node_ref": {
        const node = this.view.nodes.get(n.ref);
        if (node === undefined) {
          return t("expr.plain.node_unread");
        }
        const title = clean(nodeTitleText(t, node.title));
        return node.board_id === this.boardId
          ? t("expr.plain.node", { title })
          : t("expr.plain.node_on", {
              title,
              board: clean(
                this.view.boards.get(node.board_id)?.name ??
                  t("boards.unknown_board"),
              ),
            });
      }
      case "unary":
        if (n.op !== "neg") {
          return this.condition(n);
        }
        // A number with a minus before it is a negative number, as -2.
        return n.operand.kind === "lit" &&
          n.operand.type !== "bool" &&
          n.operand.type !== "string"
          ? numberText(locale, -n.operand.value, n.operand.type)
          : t("expr.plain.neg", { value: this.grouped(n.operand) });
      case "binary": {
        if (
          n.op !== "add" &&
          n.op !== "sub" &&
          n.op !== "mul" &&
          n.op !== "div" &&
          n.op !== "mod"
        ) {
          return this.condition(n);
        }
        const left = this.grouped(n.left);
        const right = this.grouped(n.right);
        switch (n.op) {
          case "add":
            return this.shape(n) === "string"
              ? t("expr.plain.concat", { left, right })
              : t("expr.plain.add", { left, right });
          case "sub":
            return t("expr.plain.sub", { left, right });
          case "mul":
            return t("expr.plain.mul", { left, right });
          case "div":
            return t("expr.plain.div", { left, right });
        }
        return t("expr.plain.mod", { left, right });
      }
      case "call":
        return this.call(n);
    }
    return "";
  }

  /** Returns the phrase of a call of a built-in function, each argument grouped but a name, a literal, a negative number, and a node (grouped). */
  private call(n: Call): string {
    const { t } = this.w;
    const [a, b, c] = n.args.map((arg) => this.grouped(arg));
    switch (n.function) {
      case "min":
        return t("expr.plain.call.min", { a, b });
      case "max":
        return t("expr.plain.call.max", { a, b });
      case "abs":
        return t("expr.plain.call.abs", { x: a });
      case "floor":
        return t("expr.plain.call.floor", { x: a });
      case "ceil":
        return t("expr.plain.call.ceil", { x: a });
      case "round":
        return t("expr.plain.call.round", { x: a });
      case "clamp":
        return t("expr.plain.call.clamp", { x: a, lo: b, hi: c });
      case "to_int":
        return t("expr.plain.call.to_int", { x: a });
      case "to_float":
        return t("expr.plain.call.to_float", { x: a });
      case "to_string":
        return t("expr.plain.call.to_string", { x: a });
      case "len":
        return t("expr.plain.call.len", { s: a });
      case "contains":
        return t("expr.plain.call.contains", { s: a, sub: b });
      case "starts_with":
        return t("expr.plain.call.starts_with", { s: a, prefix: b });
      case "ends_with":
        return t("expr.plain.call.ends_with", { s: a, suffix: b });
      case "lower":
        return t("expr.plain.call.lower", { s: a });
      case "upper":
        return t("expr.plain.call.upper", { s: a });
      case "trim":
        return t("expr.plain.call.trim", { s: a });
      case "visited":
        return t("expr.plain.call.visited", { node: a });
      case "visits":
        return t("expr.plain.call.visits", { node: a });
      case "signal":
        return b === undefined
          ? t("expr.plain.signal", { name: a })
          : t("expr.plain.signal_value", { name: a, value: b });
    }
    return "";
  }

  /** Returns the sentence of a script's statement. */
  statement(n: Node): string {
    const { t } = this.w;
    if (n.kind === "call") {
      return this.call(n);
    }
    if (n.kind !== "assign") {
      return this.value(n);
    }
    const target = this.value(n.target);
    // As an operand of a comparison is: met_elara = gold > 3 is "Set
    // `met_elara` to whether (`gold` is more than 3).", and gold += trust * 2
    // "Add (`trust` times 2) to `gold`.", whose words no update's run on
    // into.
    const value = this.operand(n.value);
    switch (n.op) {
      case "+=":
        return this.shape(n.target) === "string"
          ? t("expr.plain.append", { target, value })
          : t("expr.plain.increase", { target, value });
      case "-=":
        return t("expr.plain.decrease", { target, value });
      case "*=":
        return t("expr.plain.multiply", { target, value });
      case "/=":
        return t("expr.plain.divide", { target, value });
      case "%=":
        return t("expr.plain.remainder", { target, value });
    }
    return t("expr.plain.set", { target, value });
  }
}

/**
 * Returns the plain-language rendering of ast, a compiled condition's or
 * script's, of the board of boardId, as view names what it names: a
 * condition's one phrase, or a sentence for each of a script's statements,
 * each as its parts.
 */
export function plainOf(
  ast: Node,
  w: Words,
  view: ScopeSources,
  boardId: string,
): Part[][] {
  const r = new Renderer(w, view, boardId);
  if (ast.kind === "script") {
    return ast.statements.map((s) => r.expand(r.statement(s)));
  }
  return [r.expand(r.condition(ast))];
}
