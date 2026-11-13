// The plain-language rendering of an expression (PRD FR-EXPR-01 AC4, plan
// note N74): what a compiled AST says, in the user's language, from the
// catalog's phrases, as "`met_elara` is true and `elara.trust` is at least
// 3", generated from the AST, not from the source, so that two sources of
// one AST say the same. A condition is one phrase, and a script a sentence
// for each of its statements. A name is shown as the board's expressions
// name it, and a node by its title.

import type {
  Binary,
  BinaryOp,
  Call,
  Node,
} from "@storycreep/shared/expr/compile";
import { valueNamer } from "../content/textNames";
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

/** The precedence of an arithmetic operator, which says where a part of a rendering is grouped. */
const precedence: Partial<Record<BinaryOp, number>> = {
  add: 1,
  sub: 1,
  mul: 2,
  div: 2,
  mod: 2,
};

/** Returns the number value in locale's writing of numbers: an integer with its digits grouped, and a float as its shortest decimal, with the locale's decimal separator and minus sign. */
function numberText(locale: string, value: number): string {
  if (Number.isInteger(value) && Math.abs(value) < 1e21) {
    return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(
      value,
    );
  }
  const parts = new Intl.NumberFormat(locale).formatToParts(-1.5);
  const decimal = parts.find((p) => p.type === "decimal")?.value ?? ".";
  const minus = parts.find((p) => p.type === "minusSign")?.value ?? "-";
  return String(value).replace(".", decimal).replace("-", minus);
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
      case "call":
        switch (n.function) {
          case "to_string":
          case "lower":
          case "upper":
          case "trim":
            return "string";
          case "contains":
          case "starts_with":
          case "ends_with":
          case "visited":
            return "bool";
        }
        return "number";
    }
    return "bool";
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
            : t("expr.plain.not", { condition: this.condition(o) });
        }
        break;
      case "binary":
        if (n.op === "and" || n.op === "or") {
          const side = (s: Node) =>
            s.kind === "binary" &&
            (s.op === "and" || s.op === "or") &&
            s.op !== n.op
              ? t("expr.plain.group", { inner: this.condition(s) })
              : this.condition(s);
          return n.op === "and"
            ? t("expr.plain.and", { left: side(n.left), right: side(n.right) })
            : t("expr.plain.or", { left: side(n.left), right: side(n.right) });
        }
        return this.comparison(n);
      case "call":
        return this.call(n);
    }
    return this.value(n);
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

  /** Returns the phrase of an operand of a comparison: a value, or whether a condition holds. */
  private operand(n: Node): string {
    const plainValue =
      n.kind === "lit" ||
      n.kind === "var" ||
      n.kind === "attr" ||
      this.shape(n) !== "bool";
    return plainValue
      ? this.value(n)
      : this.w.t("expr.plain.whether", { condition: this.condition(n) });
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
        return numberText(locale, n.value);
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
        const title = clean(
          node.title === "" ? t("boards.untitled_node") : node.title,
        );
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
        // A number with a minus before it is a negative number, as −2.
        return n.operand.kind === "lit" &&
          n.operand.type !== "bool" &&
          n.operand.type !== "string"
          ? numberText(locale, -n.operand.value)
          : t("expr.plain.neg", { value: this.grouped(n.operand, 3) });
      case "binary": {
        const p = precedence[n.op];
        if (p === undefined) {
          return this.condition(n);
        }
        const left = this.grouped(n.left, p);
        // The right operand of the same precedence is grouped, as a - (b -
        // c) is not a - b - c.
        const right = this.grouped(n.right, p + 1);
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

  /** Returns the phrase of n as an operand of an arithmetic operator of the precedence least, grouped where its own is lower. */
  private grouped(n: Node, least: number): string {
    const p =
      n.kind === "binary"
        ? (precedence[n.op] ?? 0)
        : n.kind === "unary" && n.op === "neg"
          ? 3
          : 4;
    const phrase = this.value(n);
    return p < least ? this.w.t("expr.plain.group", { inner: phrase }) : phrase;
  }

  /** Returns the phrase of a call of a built-in function. */
  private call(n: Call): string {
    const { t } = this.w;
    const [a, b, c] = n.args.map((arg) => this.value(arg));
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
    const value = this.value(n.value);
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
