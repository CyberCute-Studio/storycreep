// The errors of a source, which parse and check report, and the errors of a
// caller's mistake, which no source makes.

import {
  MAX_ARGUMENTS,
  MAX_DEPTH,
  MAX_IDENTIFIER_LENGTH,
  MAX_NODES,
  MAX_SIGNALS,
  MAX_STATEMENTS,
  type Range,
} from "./ast";
import { MAX_STRING_LENGTH } from "./semantics";

/**
 * The stable code of an error of a source, which a client tells the error by,
 * and words it from in its user's language. schema/expr-corpus/README.md
 * lists each, with the range that it covers, in its tables of syntax errors,
 * which parse reports, and of compile errors, which check reports.
 */
export type Code =
  | "invalid_character"
  | "unterminated_string"
  | "invalid_escape"
  | "string_too_long"
  | "invalid_number"
  | "integer_out_of_range"
  | "float_out_of_range"
  | "identifier_too_long"
  | "unexpected_token"
  | "unexpected_end"
  | "reserved_word"
  | "invalid_name"
  | "invalid_node_ref"
  | "invalid_statement"
  | "invalid_assignment_target"
  | "condition_must_be_pure"
  | "ast_too_deep"
  | "ast_too_large"
  | "too_many_statements"
  | "too_many_arguments"
  | "too_many_signals"
  | "unknown_identifier"
  | "board_variable_out_of_scope"
  | "variable_not_component"
  | "component_not_value"
  | "unknown_attribute"
  | "attribute_not_runtime"
  | "unknown_node"
  | "node_ref_jumper"
  | "unknown_function"
  | "signal_not_statement"
  | "wrong_argument_count"
  | "invalid_signal_name"
  | "type_mismatch"
  | "invalid_enum_value";

/** The codes of parse's errors, in the order of the README's table of syntax errors. */
export const SYNTAX_CODES: readonly Code[] = [
  "invalid_character",
  "unterminated_string",
  "invalid_escape",
  "string_too_long",
  "invalid_number",
  "integer_out_of_range",
  "float_out_of_range",
  "identifier_too_long",
  "unexpected_token",
  "unexpected_end",
  "reserved_word",
  "invalid_name",
  "invalid_node_ref",
  "invalid_statement",
  "invalid_assignment_target",
  "condition_must_be_pure",
  "ast_too_deep",
  "ast_too_large",
];

/**
 * The codes of check's errors, in the order of the README's table of compile
 * errors: the limits' first, in the order that its first pass checks them at
 * a node, and then its second pass's.
 */
export const COMPILE_CODES: readonly Code[] = [
  "ast_too_large",
  "ast_too_deep",
  "too_many_statements",
  "too_many_arguments",
  "too_many_signals",
  "unknown_identifier",
  "board_variable_out_of_scope",
  "variable_not_component",
  "component_not_value",
  "unknown_attribute",
  "attribute_not_runtime",
  "unknown_node",
  "node_ref_jumper",
  "unknown_function",
  "condition_must_be_pure",
  "signal_not_statement",
  "invalid_statement",
  "wrong_argument_count",
  "invalid_signal_name",
  "type_mismatch",
  "invalid_enum_value",
];

// problems say what each error is, in English, for programs and logs, as the
// Go package's do. A client words an error from its code in its user's
// language.
const problems: Record<Code, string> = {
  invalid_character: "a character that starts no token",
  unterminated_string: "a string literal without its closing quote on its line",
  invalid_escape:
    'an escape that is not \\", \\\\, \\n, \\t, \\r, or \\u of a character other than NUL',
  string_too_long: `a string literal of more than ${MAX_STRING_LENGTH} characters`,
  invalid_number:
    "a number that is neither an integer literal nor a float literal",
  integer_out_of_range: "an integer literal beyond 9007199254740991",
  float_out_of_range: "a float literal beyond a double's range",
  identifier_too_long: `an identifier of more than ${MAX_IDENTIFIER_LENGTH} characters`,
  unexpected_token: "a token that cannot follow what is before it",
  unexpected_end: "the end of a source that is not complete",
  reserved_word: "a reserved word where a name is read",
  invalid_name:
    "a name of more parts than the forms of a variable's or an attribute's",
  invalid_node_ref:
    "a node(…) that does not hold one string literal of a node's ID",
  invalid_statement:
    "a statement that is neither an assignment nor a call of a function with an effect",
  invalid_assignment_target:
    "an assignment to something other than a variable or an attribute",
  condition_must_be_pure: "an assignment operator or a signal in a condition",
  ast_too_deep: `parentheses nested, or an AST, more than ${MAX_DEPTH} deep`,
  ast_too_large: `an AST of more than ${MAX_NODES} nodes`,
  too_many_statements: `a script of more than ${MAX_STATEMENTS} statements`,
  too_many_arguments: `a call of more than ${MAX_ARGUMENTS} arguments`,
  too_many_signals: `a script of more than ${MAX_SIGNALS} signals`,
  unknown_identifier: "a name that no variable or component at its place has",
  board_variable_out_of_scope: "a variable of another board",
  variable_not_component:
    "a variable's key before an attribute's, where a component's is read",
  component_not_value: "a component's key alone, where a value is read",
  unknown_attribute: "an attribute that its component does not have",
  attribute_not_runtime: "an attribute that is no part of the game's state",
  unknown_node: "a node reference to a node that the project does not have",
  node_ref_jumper: "a node reference to a jumper",
  unknown_function: "a call of a function that is not a built-in",
  signal_not_statement: "a signal where a value is read",
  wrong_argument_count:
    "a call of more or fewer arguments than its function takes",
  invalid_signal_name:
    "a signal's name that is not a string literal of a character or more",
  type_mismatch: "an operand of a type that its place does not take",
  invalid_enum_value:
    "an assignment to an enum attribute other than = of a string literal of one of its options",
};

/** The types that a type_mismatch names: the values' and a node reference's. */
export type TypeName = "bool" | "int" | "float" | "string" | "node";

/**
 * An error of a source: its code, and the characters that it covers, which
 * are those of a token, of an expression, or, for unexpected_end, none, at
 * the end of the source. A type_mismatch names its operand's type, and the
 * types that its place takes, and an unknown name or function the near
 * matches that it might be.
 */
export class ExprError extends Error {
  readonly code: Code;
  readonly start: number;
  readonly end: number;
  /** A type_mismatch's operand's type, or undefined for any other error. */
  readonly type: TypeName | undefined;
  /**
   * The types that a type_mismatch's operand's place takes, in the order
   * bool, int, float, string, node, or undefined for any other error.
   */
  readonly expected: readonly TypeName[] | undefined;
  /**
   * What might be written in place of the error's characters, nearest
   * first, of an unknown_identifier, an unknown_attribute, an
   * unknown_function, or a variable_not_component, if anything near is.
   */
  readonly suggestions: readonly string[] | undefined;

  constructor(
    code: Code,
    range: Range,
    details: {
      type?: TypeName;
      expected?: readonly TypeName[];
      suggestions?: readonly string[];
    } = {},
  ) {
    let message = `expr: ${code}, ${problems[code]}, at characters ${range.start} to ${range.end}`;
    if (details.type !== undefined) {
      message += `: of the type ${details.type}, where ${(details.expected ?? []).join(" or ")} is expected`;
    }
    const suggestions =
      details.suggestions !== undefined && details.suggestions.length > 0
        ? details.suggestions
        : undefined;
    if (suggestions !== undefined) {
      message += `; perhaps ${suggestions.join(" or ")}`;
    }
    super(message);
    this.name = "ExprError";
    this.code = code;
    this.start = range.start;
    this.end = range.end;
    this.type = details.type;
    this.expected = details.type === undefined ? undefined : details.expected;
    this.suggestions = suggestions;
  }

  /**
   * Returns the error as the corpus writes one: its code, its range, and
   * its type, expected types, and suggestions, each left out when it has
   * none.
   */
  toJSON(): {
    code: Code;
    start: number;
    end: number;
    type?: TypeName;
    expected?: TypeName[];
    suggestions?: string[];
  } {
    return {
      code: this.code,
      start: this.start,
      end: this.end,
      ...(this.type === undefined ? {} : { type: this.type }),
      ...(this.expected === undefined || this.expected.length === 0
        ? {}
        : { expected: [...this.expected] }),
      ...(this.suggestions === undefined
        ? {}
        : { suggestions: [...this.suggestions] }),
    };
  }
}

/**
 * A caller's mistake, which no source makes: a context that is neither
 * condition nor script, a board that is not a board's ID, a node that no AST
 * that parse returns holds, or what is not a project's. Its kind says which.
 */
export class UsageError extends Error {
  readonly kind: "context" | "board" | "ast" | "project";

  constructor(kind: "context" | "board" | "ast" | "project", message: string) {
    super(`expr: ${message}`);
    this.name = "UsageError";
    this.kind = kind;
  }
}

/** Throws a UsageError of the kind context unless ctx is a context. */
export function checkContext(ctx: string): void {
  if (ctx !== "condition" && ctx !== "script") {
    throw new UsageError(
      "context",
      `no context of a source: ${JSON.stringify(ctx)}`,
    );
  }
}
