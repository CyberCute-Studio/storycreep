// The parser of the expression language (§5.8.2): the TypeScript twin of
// the Go package server/internal/expr's, which reads every source alike, as
// schema/expr-corpus/syntax.json checks.

import { decodeId, encodeId } from "../ids";
import {
  type AssignOp,
  type BinaryOp,
  type Context,
  MAX_DEPTH,
  MAX_NODES,
  type Node,
  type Range,
  type Script,
  type UnaryOp,
} from "./ast";
import { checkContext, ExprError } from "./error";
import { Lexer, type Token, type TokenKind } from "./lexer";

/**
 * Reads source as the source of a condition or a script, as ctx says, and
 * returns its AST: a condition's expression, or a Script. It throws an
 * ExprError of the first problem that it meets reading the source from left
 * to right, or, if ctx is neither condition nor script, a UsageError.
 *
 * parse names a variable or a component's attribute as its source writes it,
 * and leaves which entity that is to name resolution (check). It checks the
 * lengths of identifiers and string literals, how deeply parentheses nest,
 * and how many nodes the AST has (§5.8.6), and never recurses deeper than
 * parentheses nest, though the AST may be deeper than MAX_DEPTH, as a chain
 * of binary operators makes it, until check checks the AST depth.
 */
export function parse(source: string, ctx: Context): Node {
  checkContext(ctx);
  return new Parser(source, ctx).parse();
}

// The assignment operators, by their tokens.
const assignOps: Partial<Record<TokenKind, AssignOp>> = {
  "=": "=",
  "+=": "+=",
  "-=": "-=",
  "*=": "*=",
  "/=": "/=",
  "%=": "%=",
};

// levels are the binary operators by their tokens, by precedence, from the
// lowest (§5.8.2). Each level's operators are left-associative.
const levels: Partial<Record<TokenKind, BinaryOp>>[] = [
  { or: "or" },
  { and: "and" },
  { "==": "eq", "!=": "ne" },
  { "<": "lt", "<=": "lte", ">": "gt", ">=": "gte" },
  { "+": "add", "-": "sub" },
  { "*": "mul", "/": "div", "%": "mod" },
];

// What the parser keeps of a qualified name that it has read: how many parts
// it has, its second and its third, the only ones after its first that a
// name's forms hold, and where its last ends.
interface Qualified {
  parts: number;
  second: string;
  third: string;
  end: number;
}

// sameRange reports whether a and b are one range.
function sameRange(a: Range, b: Range): boolean {
  return a.start === b.start && a.end === b.end;
}

// Parser reads a source's tokens by the grammar of §5.8.2, by recursive
// descent, and stops at the first token that it cannot read, with the error
// that it throws (fail). It reads the token after the current one only as it
// steps past the current one (next), but for the token after a node
// reference's string, which nodeRef reads ahead (peek), so that the error of
// a token that it has not read never comes first.
class Parser {
  private readonly ctx: Context;
  private readonly lex: Lexer;
  private tok!: Token;
  private ahead: Token | undefined;
  // nodes is how many nodes the AST has so far (count).
  private nodes = 0;

  constructor(source: string, ctx: Context) {
    this.ctx = ctx;
    this.lex = new Lexer(source, ctx);
  }

  parse(): Node {
    this.next();
    if (this.ctx === "script") {
      return this.script();
    }
    const [n] = this.expression();
    if (this.tok.kind !== "eof") {
      this.unexpected();
    }
    return n;
  }

  // fail stops the parser with the error code over r.
  private fail(code: ExprError["code"], r: Range): never {
    throw new ExprError(code, r);
  }

  // next steps past the current token, to the one after it, which is an eof
  // again once the current one is the eof.
  private next(): void {
    if (this.ahead !== undefined) {
      this.tok = this.ahead;
      this.ahead = undefined;
      return;
    }
    this.tok = this.lex.next();
  }

  // peek returns the kind of the token after the current one.
  private peek(): TokenKind {
    if (this.ahead === undefined) {
      this.ahead = this.lex.next();
    }
    return this.ahead.kind;
  }

  // unexpected fails at the current token, which nothing that the parser has
  // read can be followed by: with its own error, if it is a token that the
  // lexer could not read; unexpected_end at the end of the source;
  // condition_must_be_pure for an assignment operator in a condition,
  // wherever it is; and otherwise unexpected_token. A token that the lexer
  // could not read is its error only here, where the parser stops at it.
  private unexpected(): never {
    const t = this.tok;
    if (t.kind === "error" && t.err !== undefined) {
      throw t.err;
    }
    if (t.kind === "eof") {
      this.fail("unexpected_end", t);
    }
    if (this.ctx === "condition" && assignOps[t.kind] !== undefined) {
      this.fail("condition_must_be_pure", t);
    }
    this.fail("unexpected_token", t);
  }

  // script reads a script: statements, each ended by a ;, a line break that
  // ends it, or the end of the source, with as many ; and line breaks as a
  // writer likes before, between, and after them.
  private script(): Script {
    // The script's own node, which no token makes, counts before its first
    // token.
    this.nodes = 1;
    const statements: Node[] = [];
    for (;;) {
      while (this.tok.kind === ";" || this.tok.kind === "newline") {
        this.next();
      }
      if (this.tok.kind === "eof") {
        return {
          kind: "script",
          statements,
          src: { start: 0, end: this.tok.end },
        };
      }
      statements.push(this.statement());
    }
  }

  // statement reads a statement of a script, to the token that ends it: an
  // assignment to a variable or an attribute, or a call. It reads an
  // expression first, so that an assignment to anything but a variable or
  // an attribute is invalid_assignment_target, and a statement that is
  // neither an assignment nor a call invalid_statement, over the whole
  // expression as written, parentheses and all, once the token after it
  // ends it; any other token after it is that token's error.
  private statement(): Node {
    const [e, written] = this.expression();
    const op = assignOps[this.tok.kind];
    if (op !== undefined) {
      this.count();
      // A name in parentheses is written over more than its range.
      if (
        (e.kind === "var" || e.kind === "attr") &&
        sameRange(written, e.src)
      ) {
        this.next();
        const [v, rest] = this.expression();
        this.end();
        return {
          kind: "assign",
          op,
          target: e,
          value: v,
          src: { start: written.start, end: rest.end },
        };
      }
      this.fail("invalid_assignment_target", written);
    }
    this.end();
    if (e.kind === "call" && sameRange(written, e.src)) {
      return e;
    }
    this.fail("invalid_statement", written);
  }

  // count counts the node that the current token makes, which the parser
  // has reached and not yet stepped past: a literal, the identifier or the
  // qualifier that a name, a call, or a node reference starts with, a unary
  // or a binary operator, or a statement's assignment operator. It fails
  // with ast_too_large at the token if the AST has MAX_NODES nodes already,
  // before anything else is decided at it.
  private count(): void {
    if (this.nodes === MAX_NODES) {
      this.fail("ast_too_large", this.tok);
    }
    this.nodes++;
  }

  // end fails unless the current token ends a statement: a ;, a line break
  // that ends it, or the end of the source.
  private end(): void {
    const k = this.tok.kind;
    if (k !== ";" && k !== "newline" && k !== "eof") {
      this.unexpected();
    }
  }

  // expression reads an expression, and returns its node and the range of
  // the expression as written: the node's, and the parentheses around it, if
  // any.
  private expression(): [Node, Range] {
    return this.binary(0);
  }

  // binary reads an expression of the operators of levels[level] and those
  // above it, its operands joined from the left.
  private binary(level: number): [Node, Range] {
    if (level === levels.length) {
      return this.unary();
    }
    let [left, written] = this.binary(level + 1);
    for (;;) {
      const op = levels[level][this.tok.kind];
      if (op === undefined) {
        return [left, written];
      }
      this.count();
      this.next();
      const [right, rest] = this.binary(level + 1);
      written = { start: written.start, end: rest.end };
      left = { kind: "binary", op, left, right, src: written };
    }
  }

  // unary reads a primary, with a unary operator before it or none. The
  // operator applies to a primary, so that --x is unexpected_token at its
  // second -, and -(-x) is read.
  private unary(): [Node, Range] {
    let op: UnaryOp;
    switch (this.tok.kind) {
      case "not":
        op = "not";
        break;
      case "-":
        op = "neg";
        break;
      default:
        return this.primary();
    }
    const start = this.tok.start;
    this.count();
    this.next();
    const [operand, written] = this.primary();
    const src = { start, end: written.end };
    return [{ kind: "unary", op, operand, src }, src];
  }

  // primary reads a literal, a name, a call, a node reference, or an
  // expression in parentheses.
  private primary(): [Node, Range] {
    const t = this.tok;
    const src = { start: t.start, end: t.end };
    let n: Node;
    switch (t.kind) {
      case "int":
        n = { kind: "lit", type: "int", value: t.num, src };
        break;
      case "float":
        n = { kind: "lit", type: "float", value: t.num, src };
        break;
      case "string":
        n = { kind: "lit", type: "string", value: t.text, src };
        break;
      case "true":
      case "false":
        n = { kind: "lit", type: "bool", value: t.kind === "true", src };
        break;
      case "ident":
        return this.nameOrCall();
      case "global":
      case "board":
        return this.scoped();
      case "reserved":
        return this.fail("reserved_word", t);
      case "(": {
        const start = t.start;
        this.open();
        const [e] = this.expression();
        const end = this.close();
        return [e, { start, end }];
      }
      default:
        this.unexpected();
    }
    // A literal.
    this.count();
    this.next();
    return [n, n.src];
  }

  // open steps past the opening parenthesis that is the current token, a
  // group's, a call's, or a node reference's, which is ast_too_deep if it
  // opens one more than MAX_DEPTH of them, as the lexer counts them. A ( that
  // nothing before it can be followed by, as one after a literal, opens no
  // level, since the parser stops at it before it would reach open.
  private open(): void {
    if (this.tok.depth > MAX_DEPTH) {
      this.fail("ast_too_deep", this.tok);
    }
    this.next();
  }

  // close steps past the closing parenthesis that the current token must
  // be, and returns where it ends.
  private close(): number {
    if (this.tok.kind !== ")") {
      this.unexpected();
    }
    const end = this.tok.end;
    this.next();
    return end;
  }

  // nameOrCall reads what starts with an identifier: a call or a node
  // reference, when a parenthesis follows it, and otherwise a variable, by
  // its key, or an attribute, as <component>.<attribute> or
  // cmp.<component>.<attribute>.
  private nameOrCall(): [Node, Range] {
    const first = this.tok;
    this.count();
    this.next();
    if (this.tok.kind === "(") {
      if (first.text === "node") {
        return this.nodeRef(first);
      }
      return this.call(first);
    }
    const name = this.parts(first);
    const r = { start: first.start, end: name.end };
    let n: Node;
    if (name.parts === 1) {
      n = {
        kind: "var",
        name: first.text,
        scope: "",
        key: first.text,
        ref: "",
        src: r,
      };
    } else if (name.parts === 2) {
      n = {
        kind: "attr",
        name: `${first.text}.${name.second}`,
        explicit: false,
        componentKey: first.text,
        attributeKey: name.second,
        ref: "",
        attribute: "",
        src: r,
      };
    } else if (name.parts === 3 && first.text === "cmp") {
      n = {
        kind: "attr",
        name: `cmp.${name.second}.${name.third}`,
        explicit: true,
        componentKey: name.second,
        attributeKey: name.third,
        ref: "",
        attribute: "",
        src: r,
      };
    } else {
      this.fail("invalid_name", r);
    }
    return [n, r];
  }

  // scoped reads a variable written after its qualifier: global.<key> or
  // board.<key>. The qualifier without a dot after it is reserved_word,
  // whatever the token after it is, one that the lexer could not read too,
  // and with more than one part after it, invalid_name.
  private scoped(): [Node, Range] {
    const q = this.tok;
    this.count();
    this.next();
    if (this.tok.kind !== ".") {
      this.fail("reserved_word", q);
    }
    const name = this.parts(q);
    const r = { start: q.start, end: name.end };
    if (name.parts !== 2) {
      this.fail("invalid_name", r);
    }
    const scope = q.kind === "board" ? "board" : "global";
    return [
      {
        kind: "var",
        name: `${q.text}.${name.second}`,
        scope,
        key: name.second,
        ref: "",
        src: r,
      },
      r,
    ];
  }

  // parts reads the parts of a qualified name: first, which the parser has
  // stepped past, and the identifier after each dot that follows. A reserved
  // word after a dot is reserved_word. It counts the parts rather than
  // keeping them, since neither a dot nor the part after it makes a node
  // that count would stop at.
  private parts(first: Token): Qualified {
    const q: Qualified = { parts: 1, second: "", third: "", end: first.end };
    while (this.tok.kind === ".") {
      this.next();
      const t = this.tok;
      if (t.kind === "ident") {
        q.parts++;
        if (q.parts === 2) {
          q.second = t.text;
        } else if (q.parts === 3) {
          q.third = t.text;
        }
        q.end = t.end;
        this.next();
      } else if (t.keyword) {
        this.fail("reserved_word", t);
      } else {
        this.unexpected();
      }
    }
    return q;
  }

  // call reads a call of the function name, which the parser has stepped
  // past, from its opening parenthesis.
  private call(name: Token): [Node, Range] {
    this.open();
    const args = this.arguments();
    const end = this.close();
    const src = { start: name.start, end };
    return [{ kind: "call", function: name.text, args, src }, src];
  }

  // arguments reads a call's arguments, expressions separated by commas, up
  // to its closing parenthesis, which it leaves.
  private arguments(): Node[] {
    const args: Node[] = [];
    if (this.tok.kind === ")") {
      return args;
    }
    for (;;) {
      const [a] = this.expression();
      args.push(a);
      if (this.tok.kind !== ",") {
        return args;
      }
      this.next();
    }
  }

  // nodeRef reads a node reference, node("nd_…"), from its opening
  // parenthesis: one string literal that holds a node's ID, in any case,
  // which is part of the reference's node, or else invalid_node_ref, over
  // the string, or over the whole node(…) when it holds anything else, once
  // the parser has read it to its closing parenthesis, as a call's
  // arguments, each node of which counts, and stepped past that
  // parenthesis, so that its error comes before that of the token after it.
  private nodeRef(name: Token): [Node, Range] {
    this.open();
    if (this.tok.kind === "string" && this.peek() === ")") {
      const arg = this.tok;
      this.next();
      const end = this.close();
      let ref: string;
      try {
        ref = encodeId("node", decodeId("node", arg.text));
      } catch {
        this.fail("invalid_node_ref", arg);
      }
      const src = { start: name.start, end };
      return [{ kind: "node_ref", ref, src }, src];
    }
    this.arguments();
    const end = this.close();
    this.fail("invalid_node_ref", { start: name.start, end });
  }
}
