// The AST of the expression language (PRD §5.8.8), as parse returns it and
// check resolves its names, the TypeScript twin of the Go package
// server/internal/expr's.

/**
 * What a source is the source of, which decides how it is read (§5.8.5): a
 * condition is one expression, of which line breaks are white space, and a
 * script is a sequence of statements, separated by ; or by line breaks.
 */
export type Context = "condition" | "script";

/**
 * Where in a source something is: the offsets of its first character and of
 * the character after its last, counted in code points from 0, which an
 * editor that counts in UTF-16 code units converts. An empty range is a
 * place between two characters, before the one at start.
 */
export interface Range {
  readonly start: number;
  readonly end: number;
}

/** The kind of an AST's node, as its JSON's kind names it (§5.8.8). */
export type Kind =
  | "lit"
  | "var"
  | "attr"
  | "node_ref"
  | "unary"
  | "binary"
  | "call"
  | "assign"
  | "script";

/** The types of a literal and of a value of the language (§5.8.3). */
export type ValueType = "bool" | "int" | "float" | "string";

/**
 * A literal: true or false, an int, a float, or a string, of its type. An int
 * is within ±(2^53 − 1), a float finite and never -0, and a string of whole
 * code points, of at most 4,096 of them (§5.8.1, §5.8.9).
 */
export type Lit = { kind: "lit"; src: Range } & (
  | { type: "bool"; value: boolean }
  | { type: "int"; value: number }
  | { type: "float"; value: number }
  | { type: "string"; value: string }
);

/** The qualifier that a source writes a variable's key after (§5.8.3). */
export type Scope = "" | "global" | "board";

/** A variable, by its key, as global.<key>, or as board.<key>. */
export interface Var {
  kind: "var";
  /**
   * The variable's name as the AST's JSON writes it: as the source wrote
   * it, with its qualifier, until name resolution writes the variable's key
   * there.
   */
  name: string;
  scope: Scope;
  key: string;
  /** The variable's ID, which name resolution sets, or "" until then. */
  ref: string;
  src: Range;
}

/**
 * A component's attribute, as <component>.<attribute> or as
 * cmp.<component>.<attribute>.
 */
export interface Attr {
  kind: "attr";
  /**
   * The attribute's name as the AST's JSON writes it: as the source wrote
   * it, cmp. and all, until name resolution writes
   * <component>.<attribute> there, of their keys.
   */
  name: string;
  /** Set for the form that starts with cmp. */
  explicit: boolean;
  componentKey: string;
  attributeKey: string;
  /** The component's ID, which name resolution sets, or "" until then. */
  ref: string;
  /** The attribute definition's ID, which name resolution sets, or "". */
  attribute: string;
  src: Range;
}

/** A node reference literal, node("nd_…") (§5.8.4). */
export interface NodeRef {
  kind: "node_ref";
  /** The node's ID, in its canonical spelling. */
  ref: string;
  src: Range;
}

/** The operator of a Unary: !, or not, and -. */
export type UnaryOp = "not" | "neg";

/** A unary operator applied to its operand. */
export interface Unary {
  kind: "unary";
  op: UnaryOp;
  operand: Node;
  src: Range;
}

/**
 * The operator of a Binary: ||, &&, ==, !=, <, <=, >, >=, +, -, *, /, and %,
 * and and or written as words too.
 */
export type BinaryOp =
  | "or"
  | "and"
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "add"
  | "sub"
  | "mul"
  | "div"
  | "mod";

/** A binary operator applied to its operands. */
export interface Binary {
  kind: "binary";
  op: BinaryOp;
  left: Node;
  right: Node;
  src: Range;
}

/** A call of a built-in function, by its name, with its arguments in order. */
export interface Call {
  kind: "call";
  function: string;
  args: Node[];
  src: Range;
}

/** The operator of an Assign, as the source writes it. */
export type AssignOp = "=" | "+=" | "-=" | "*=" | "/=" | "%=";

/** An assignment, a statement of a script, to a Var or an Attr. */
export interface Assign {
  kind: "assign";
  op: AssignOp;
  target: Node;
  value: Node;
  src: Range;
}

/** A script's statements, each an Assign or a Call, in order. */
export interface Script {
  kind: "script";
  statements: Node[];
  /** The whole source. */
  src: Range;
}

/**
 * A node of an AST. Its src is where it is in the source that parse read it
 * from: from its first token to its last, with the parentheses around its
 * operands, but not those around itself; a Script's is the whole source, the
 * white space and the comments around its statements included. A node that
 * decode reads from JSON has no source, and an empty range at 0.
 */
export type Node =
  | Lit
  | Var
  | Attr
  | NodeRef
  | Unary
  | Binary
  | Call
  | Assign
  | Script;

/** The range of a node that has no source. */
export const NO_RANGE: Range = Object.freeze({ start: 0, end: 0 });

/**
 * The limits of an AST (§5.8.1, §5.8.6), which parse checks of a source and
 * check of an AST.
 */
export const MAX_IDENTIFIER_LENGTH = 64;
/** The AST depth, which bounds how deeply parentheses nest too. */
export const MAX_DEPTH = 64;
/** The AST node count. */
export const MAX_NODES = 512;
/** The most statements of a script. */
export const MAX_STATEMENTS = 64;
/** The most arguments of a call. */
export const MAX_ARGUMENTS = 8;
/** The most calls of signal in a script. */
export const MAX_SIGNALS = 8;

/** The words of the language, which no identifier may be (§5.8.1). */
export const RESERVED_WORDS: readonly string[] = [
  "true",
  "false",
  "and",
  "or",
  "not",
  "global",
  "board",
  "null",
  "if",
  "else",
  "elseif",
];

/** Reports whether s is an identifier, [A-Za-z_][A-Za-z0-9_]*, of at most 64 characters. */
export function isIdentifier(s: string): boolean {
  return (
    s.length <= MAX_IDENTIFIER_LENGTH && /^[A-Za-z_][A-Za-z0-9_]*$/.test(s)
  );
}

/**
 * Reports whether s is a key: an identifier that is not a reserved word, as a
 * variable's, a component's, and an attribute's are.
 */
export function isKey(s: string): boolean {
  return isIdentifier(s) && !RESERVED_WORDS.includes(s);
}
