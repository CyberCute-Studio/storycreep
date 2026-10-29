// The tokens of a source, by the lexical rules of §5.8.1, read one at a
// time as the parser asks for them: the TypeScript twin of the Go package
// server/internal/expr's lexer.

import { type Context, MAX_IDENTIFIER_LENGTH, type Range } from "./ast";
import { ExprError } from "./error";
import { MAX_STRING_LENGTH, parseFloatText, parseIntText } from "./semantics";

/** The kind of a token: a word's or a symbol's own, or what it is of. */
export type TokenKind =
  | "eof"
  /** A token that the lexer could not read, whose error is in err. */
  | "error"
  /** A line break that ends a statement of a script. */
  | "newline"
  | ";"
  | "ident"
  | "int"
  | "float"
  | "string"
  | "true"
  | "false"
  | "global"
  | "board"
  /** A reserved word that no rule of the grammar reads: null, if, else, or elseif. */
  | "reserved"
  | "("
  | ")"
  | ","
  | "."
  /** || or or. */
  | "or"
  /** && or and. */
  | "and"
  /** ! or not. */
  | "not"
  | "=="
  | "!="
  | "<"
  | "<="
  | ">"
  | ">="
  | "+"
  | "-"
  | "*"
  | "/"
  | "%"
  | "="
  | "+="
  | "-="
  | "*="
  | "/="
  | "%=";

/** A token of a source. */
export interface Token extends Range {
  kind: TokenKind;
  /** A word as written, or a string literal's value. */
  text: string;
  /**
   * Set for a reserved word, as true or and is, though not for the && that
   * and stands for.
   */
  keyword: boolean;
  /** A ('s count of the parentheses that are open once it opens, itself among them. */
  depth: number;
  /** An int or a float literal's value. */
  num: number;
  /** An error token's error. */
  err?: ExprError;
}

function token(kind: TokenKind, start: number, end: number): Token {
  return { kind, start, end, text: "", keyword: false, depth: 0, num: 0 };
}

function errorToken(err: ExprError): Token {
  return { ...token("error", err.start, err.end), err };
}

// The characters that may start an identifier, and be part of one.
function isStart(c: number): boolean {
  return (
    (c >= 0x41 && c <= 0x5a) || (c >= 0x61 && c <= 0x7a) || c === 0x5f // A-Z a-z _
  );
}

function isPart(c: number): boolean {
  return isStart(c) || (c >= 0x30 && c <= 0x39);
}

// The words that are reserved but that no rule of the grammar reads.
const unread = new Set(["null", "if", "else", "elseif"]);

// endsExpression reports whether an expression can end with a token of kind
// k: an identifier, a literal, or a ). A qualifier, global or board, is not
// one, since a dot and a key follow it.
function endsExpression(k: TokenKind): boolean {
  switch (k) {
    case "ident":
    case "int":
    case "float":
    case "string":
    case "true":
    case "false":
    case ")":
      return true;
  }
  return false;
}

// forbidden reports whether c, a code point that peek returned, is one that
// no text holds wherever it is: a NUL, or a lone surrogate, which is no
// character, and which UTF-8, as the Go lexer reads a source, cannot write.
function forbidden(c: number): boolean {
  return c === 0 || (c >= 0xd800 && c <= 0xdfff);
}

/**
 * Reads a source's tokens, one at a time, as the parser asks for them
 * (next), so that a source that the parser stops in early costs no more than
 * what it read of it. It counts the source's characters in code points.
 */
export class Lexer {
  private readonly src: string;
  private readonly ctx: Context;
  // i is the UTF-16 offset of the next character, and at its code point
  // offset.
  private i = 0;
  private at = 0;
  // prev is the kind of the last token that next returned, or eof before
  // the first.
  private prev: TokenKind = "eof";
  // depth is how many parentheses the tokens that next has returned leave
  // open, so that a line break is read by the parentheses before it in the
  // source, however far the parser has read.
  private depth = 0;
  // done is set once next has returned an error or the eof, after which it
  // returns an eof at eof.
  private done = false;
  private eof = 0;

  constructor(src: string, ctx: Context) {
    this.src = src;
    this.ctx = ctx;
  }

  /**
   * Returns the source's next token. Once it has returned a token that it
   * could not read, or the eof, it returns an eof at its end. In a script, a
   * line break outside parentheses after an identifier, a literal, or a ),
   * ends the statement before it, and next returns it as a newline, having
   * stepped past nothing after it; every other line break, and every line
   * break of a condition, is white space.
   */
  next(): Token {
    if (this.done) {
      return token("eof", this.eof, this.eof);
    }
    let t = this.space(
      this.ctx === "script" && this.depth === 0 && endsExpression(this.prev),
    );
    if (t === undefined) {
      t =
        this.i === this.src.length
          ? token("eof", this.at, this.at)
          : this.token();
    }
    return this.emit(t);
  }

  // emit returns t once it has noted what t changes of how the tokens after
  // it are read: the parentheses that are open, and whether a line break
  // after it ends a statement.
  private emit(t: Token): Token {
    switch (t.kind) {
      case "error":
      case "eof":
        this.done = true;
        this.eof = t.end;
        break;
      case "(":
        this.depth++;
        t.depth = this.depth;
        break;
      case ")":
        this.depth--;
        break;
    }
    this.prev = t.kind;
    return t;
  }

  // space steps past the white space and the comments at the next character,
  // and returns the token that it stops at among them, if any: the newline
  // of the first line break, when brk is set, having stepped past nothing
  // after it, or the error of a comment that holds a forbidden character.
  private space(brk: boolean): Token | undefined {
    for (;;) {
      const [c, n] = this.peek();
      if (c === 0x20 || c === 0x09) {
        this.step(n);
      } else if (c === 0x0a || c === 0x0d) {
        const at = this.at;
        this.step(n);
        if (brk) {
          return token("newline", at, at + 1);
        }
      } else if (c === 0x2f && this.src.startsWith("//", this.i)) {
        const err = this.comment();
        if (err !== undefined) {
          return err;
        }
      } else {
        return undefined;
      }
    }
  }

  // peek returns the next code point and its length in code units, or -1
  // and 0 at the end of the source. A lone surrogate is itself, of one.
  private peek(): [number, number] {
    if (this.i === this.src.length) {
      return [-1, 0];
    }
    const c = this.src.codePointAt(this.i) ?? 0;
    return [c, c > 0xffff ? 2 : 1];
  }

  // step steps past the next character, of n code units.
  private step(n: number): void {
    this.i += n;
    this.at++;
  }

  // comment steps past a comment, from its // to the line break or the end
  // of the source after it, or returns invalid_character at a forbidden
  // character in it.
  private comment(): Token | undefined {
    this.step(1);
    this.step(1);
    for (;;) {
      const [c, n] = this.peek();
      if (c === -1 || c === 0x0a || c === 0x0d) {
        return undefined;
      }
      if (forbidden(c)) {
        return errorToken(
          new ExprError("invalid_character", {
            start: this.at,
            end: this.at + 1,
          }),
        );
      }
      this.step(n);
    }
  }

  // token reads the token that starts at the next character, which is not
  // white space nor a comment's //.
  private token(): Token {
    const start = this.at;
    const [c, n] = this.peek();
    if (isStart(c)) {
      return this.word();
    }
    if (c >= 0x30 && c <= 0x39) {
      return this.number();
    }
    if (c === 0x22) {
      return this.string();
    }
    this.step(n);
    const one = (k: TokenKind) => token(k, start, this.at);
    // two returns a token of kind long if the next character is next, which
    // it steps past, and otherwise one of kind short.
    const two = (next: string, long: TokenKind, short: TokenKind) => {
      if (this.src[this.i] === next) {
        this.step(1);
        return one(long);
      }
      return one(short);
    };
    switch (String.fromCodePoint(c)) {
      case "(":
        return one("(");
      case ")":
        return one(")");
      case ",":
        return one(",");
      case ".":
        return one(".");
      case ";":
        return one(";");
      case "+":
        return two("=", "+=", "+");
      case "-":
        return two("=", "-=", "-");
      case "*":
        return two("=", "*=", "*");
      case "/":
        return two("=", "/=", "/");
      case "%":
        return two("=", "%=", "%");
      case "=":
        return two("=", "==", "=");
      case "!":
        return two("=", "!=", "not");
      case "<":
        return two("=", "<=", "<");
      case ">":
        return two("=", ">=", ">");
      case "&":
      case "|":
        // Only doubled: && and ||.
        if (this.src.codePointAt(this.i) === c) {
          this.step(1);
          return one(c === 0x26 ? "and" : "or");
        }
    }
    return errorToken(
      new ExprError("invalid_character", { start, end: start + 1 }),
    );
  }

  // word reads an identifier, [A-Za-z_][A-Za-z0-9_]*, or the reserved word
  // of that form.
  private word(): Token {
    const start = this.at;
    const from = this.i;
    while (this.i < this.src.length && isPart(this.src.charCodeAt(this.i))) {
      this.step(1);
    }
    const text = this.src.slice(from, this.i);
    if (text.length > MAX_IDENTIFIER_LENGTH) {
      return errorToken(
        new ExprError("identifier_too_long", { start, end: this.at }),
      );
    }
    const t: Token = { ...token("ident", start, this.at), text, keyword: true };
    switch (text) {
      case "true":
      case "false":
      case "and":
      case "or":
      case "not":
      case "global":
      case "board":
        t.kind = text;
        break;
      default:
        if (unread.has(text)) {
          t.kind = "reserved";
        } else {
          t.keyword = false;
        }
    }
    return t;
  }

  // number reads a number: the run of ASCII letters, digits, underscores,
  // and points that starts with a digit, with a sign after an e or an E of
  // it, which is an integer literal, [0-9]+, or a float literal,
  // [0-9]+\.[0-9]+([eE][+-]?[0-9]+)?, or else invalid_number, as 1., 1e5,
  // 3abc, and 1_000 are, whole. A letter that is not ASCII ends the run, as
  // any character that starts no token does, so that 3é is 3 and
  // invalid_character.
  private number(): Token {
    const start = this.at;
    const from = this.i;
    while (this.i < this.src.length) {
      const c = this.src.charCodeAt(this.i);
      const before = this.src[this.i - 1];
      const sign =
        (c === 0x2b || c === 0x2d) && (before === "e" || before === "E");
      if (!isPart(c) && c !== 0x2e && !sign) {
        break;
      }
      this.step(1);
    }
    const text = this.src.slice(from, this.i);
    const r = { start, end: this.at };
    if (/^[0-9]+$/.test(text)) {
      // parseIntText reads every run of digits, and reports only one beyond
      // ±MAX_INT.
      const [n, problem] = parseIntText(text);
      if (problem !== undefined) {
        return errorToken(new ExprError("integer_out_of_range", r));
      }
      return { ...token("int", r.start, r.end), num: n };
    }
    if (/^[0-9]+\.[0-9]+([eE][+-]?[0-9]+)?$/.test(text)) {
      // parseFloatText reads every float literal, as to_float reads one, and
      // reports only one beyond a double's range.
      const [f, problem] = parseFloatText(text);
      if (problem !== undefined) {
        return errorToken(new ExprError("float_out_of_range", r));
      }
      return { ...token("float", r.start, r.end), num: f };
    }
    return errorToken(new ExprError("invalid_number", r));
  }

  // string reads a string literal, from its opening quote to its closing
  // one, and its value: its characters, its escapes the characters that they
  // stand for. A string that its line or the source ends in is
  // unterminated_string, up to that end; a bad escape is invalid_escape; a
  // forbidden character is invalid_character, each at the first that the
  // string has, from left to right. A string read whole whose value is
  // longer than MAX_STRING_LENGTH code points is string_too_long, whole.
  private string(): Token {
    const start = this.at;
    const from = this.i;
    const open = this.at;
    this.step(1);
    // The value's parts, of a string with an escape, and its length in code
    // points.
    const parts: string[] = [];
    let escaped = false;
    let length = 0;
    let run = this.i;
    for (;;) {
      const [c, n] = this.peek();
      if (c === -1 || c === 0x0a || c === 0x0d) {
        return errorToken(
          new ExprError("unterminated_string", { start: open, end: this.at }),
        );
      }
      if (c === 0x22) {
        break;
      }
      if (c === 0x5c) {
        parts.push(this.src.slice(run, this.i));
        const [ch, err] = this.escape(open);
        if (err !== undefined) {
          return errorToken(err);
        }
        parts.push(ch);
        run = this.i;
        escaped = true;
      } else if (forbidden(c)) {
        return errorToken(
          new ExprError("invalid_character", {
            start: this.at,
            end: this.at + 1,
          }),
        );
      } else {
        this.step(n);
      }
      length++;
    }
    const value = escaped
      ? parts.join("") + this.src.slice(run, this.i)
      : this.src.slice(from + 1, this.i);
    this.step(1);
    if (length > MAX_STRING_LENGTH) {
      return errorToken(
        new ExprError("string_too_long", { start, end: this.at }),
      );
    }
    return { ...token("string", start, this.at), text: value };
  }

  // escape reads the escape at the next character, a backslash in the string
  // literal that opens at open, and returns the character that it stands
  // for, or its error. The range of an invalid_escape is the backslash and
  // the character after it, or, after \u, the hexadecimal digits that
  // follow, up to four, whatever ends them. A line break or the end of the
  // source right after the backslash is unterminated_string, and a
  // forbidden character there invalid_character, at it.
  private escape(open: number): [string, ExprError | undefined] {
    const at = this.at;
    this.step(1);
    const [c, n] = this.peek();
    if (c !== -1 && forbidden(c)) {
      return [
        "",
        new ExprError("invalid_character", {
          start: this.at,
          end: this.at + 1,
        }),
      ];
    }
    const invalid = (): [string, ExprError] => [
      "",
      new ExprError("invalid_escape", { start: at, end: this.at }),
    ];
    switch (c) {
      case -1:
      case 0x0a:
      case 0x0d:
        return [
          "",
          new ExprError("unterminated_string", { start: open, end: this.at }),
        ];
      case 0x22:
      case 0x5c:
        this.step(n);
        return [String.fromCodePoint(c), undefined];
      case 0x6e: // n
        this.step(n);
        return ["\n", undefined];
      case 0x74: // t
        this.step(n);
        return ["\t", undefined];
      case 0x72: // r
        this.step(n);
        return ["\r", undefined];
      case 0x75: {
        // u
        this.step(n);
        const u = this.hex4();
        if (u === undefined || u === 0 || (u >= 0xdc00 && u <= 0xdfff)) {
          return invalid();
        }
        if (u >= 0xd800 && u <= 0xdbff) {
          const low = this.lowSurrogate();
          if (low === undefined) {
            return invalid();
          }
          return [String.fromCharCode(u, low), undefined];
        }
        return [String.fromCharCode(u), undefined];
      }
    }
    this.step(n);
    return invalid();
  }

  // hex4 steps past the hexadecimal digits at the next character, up to
  // four, and returns their value, or undefined when there are fewer.
  private hex4(): number | undefined {
    let u = 0;
    for (let k = 0; k < 4; k++) {
      const v = hexDigit(this.src, this.i);
      if (v === undefined) {
        return undefined;
      }
      u = (u << 4) | v;
      this.step(1);
    }
    return u;
  }

  // lowSurrogate steps past a \uXXXX of a low surrogate at the next
  // character, and returns the surrogate, or returns undefined, stepping
  // past nothing, if there is none.
  private lowSurrogate(): number | undefined {
    if (!this.src.startsWith("\\u", this.i)) {
      return undefined;
    }
    let u = 0;
    for (let k = 0; k < 4; k++) {
      const v = hexDigit(this.src, this.i + 2 + k);
      if (v === undefined) {
        return undefined;
      }
      u = (u << 4) | v;
    }
    if (u < 0xdc00 || u >= 0xe000) {
      return undefined;
    }
    for (let k = 0; k < 6; k++) {
      this.step(1);
    }
    return u;
  }
}

// hexDigit returns the value of the hexadecimal digit, in either case, at
// code unit i of s, or undefined if there is none.
function hexDigit(s: string, i: number): number | undefined {
  if (i >= s.length) {
    return undefined;
  }
  const c = s.charCodeAt(i);
  if (c >= 0x30 && c <= 0x39) {
    return c - 0x30;
  }
  if (c >= 0x61 && c <= 0x66) {
    return c - 0x61 + 10;
  }
  if (c >= 0x41 && c <= 0x46) {
    return c - 0x41 + 10;
  }
  return undefined;
}
