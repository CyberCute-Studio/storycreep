// What the expression editor says, in the user's language, of what the
// expression language tells by codes and names (PRD FR-EXPR-01, FR-EXPR-02,
// FR-EXPR-04): an error of a source, by its stable code, with the range of
// characters that it covers; a type; and a built-in function, by its
// signature and what it does (§5.8.4).

import { BUILTINS, typeNamesOf } from "@storycreep/shared/expr/compile";
import type { TFunction } from "i18next";

/** What the editor's words are made with: the catalog's messages, and the locale that they are of, which lists are joined in. */
export interface Words {
  readonly t: TFunction;
  readonly locale: string;
}

/** An error of a source as the editor tells of it: the parser's or the checker's, or the server's of a saved expression, whose code may be one that this client does not know yet. */
export interface SourceError {
  readonly code: string;
  /** The range that it covers, in code points of the source. */
  readonly start: number;
  readonly end: number;
  readonly type?: string;
  readonly expected?: readonly string[] | null;
  readonly suggestions?: readonly string[] | null;
}

/** Returns the name of a type of the language, as bool or node, in the user's language. */
export function typeText(t: TFunction, type: string): string {
  switch (type) {
    case "bool":
      return t("expr.type.bool");
    case "int":
      return t("expr.type.int");
    case "float":
      return t("expr.type.float");
    case "string":
      return t("expr.type.string");
    case "node":
      return t("expr.type.node");
    case "number":
      return t("expr.type.number");
    case "enum":
      return t("expr.type.enum");
  }
  return type;
}

/** Returns a list of alternatives, as either or, joined as the user's language joins one. */
export function orList(locale: string, items: readonly string[]): string {
  return new Intl.ListFormat(locale, { type: "disjunction" }).format(items);
}

/** Returns a list of the types, joined as the user's language joins a list of alternatives, of which a whole number and a number are a number. */
function typesText(w: Words, types: readonly string[]): string {
  const numbers = types.includes("int") && types.includes("float");
  return orList(
    w.locale,
    types
      .filter((type) => !numbers || type !== "float")
      .map((type) =>
        typeText(w.t, numbers && type === "int" ? "number" : type),
      ),
  );
}

/** Returns what an error says of what it is, by its code. */
function problemText(w: Words, e: SourceError): string {
  const { t } = w;
  switch (e.code) {
    case "invalid_character":
      return t("expr.error.invalid_character");
    case "unterminated_string":
      return t("expr.error.unterminated_string");
    case "invalid_escape":
      return t("expr.error.invalid_escape");
    case "string_too_long":
      return t("expr.error.string_too_long");
    case "invalid_number":
      return t("expr.error.invalid_number");
    case "integer_out_of_range":
      return t("expr.error.integer_out_of_range");
    case "float_out_of_range":
      return t("expr.error.float_out_of_range");
    case "identifier_too_long":
      return t("expr.error.identifier_too_long");
    case "unexpected_token":
      return t("expr.error.unexpected_token");
    case "unexpected_end":
      return t("expr.error.unexpected_end");
    case "reserved_word":
      return t("expr.error.reserved_word");
    case "invalid_name":
      return t("expr.error.invalid_name");
    case "invalid_node_ref":
      return t("expr.error.invalid_node_ref");
    case "invalid_statement":
      return t("expr.error.invalid_statement");
    case "invalid_assignment_target":
      return t("expr.error.invalid_assignment_target");
    case "condition_must_be_pure":
      return t("expr.error.condition_must_be_pure");
    case "ast_too_deep":
      return t("expr.error.ast_too_deep");
    case "ast_too_large":
      return t("expr.error.ast_too_large");
    case "too_many_statements":
      return t("expr.error.too_many_statements");
    case "too_many_arguments":
      return t("expr.error.too_many_arguments");
    case "too_many_signals":
      return t("expr.error.too_many_signals");
    case "unknown_identifier":
      return t("expr.error.unknown_identifier");
    case "board_variable_out_of_scope":
      return t("expr.error.board_variable_out_of_scope");
    case "variable_not_component":
      return t("expr.error.variable_not_component");
    case "component_not_value":
      return t("expr.error.component_not_value");
    case "unknown_attribute":
      return t("expr.error.unknown_attribute");
    case "attribute_not_runtime":
      return t("expr.error.attribute_not_runtime");
    case "unknown_node":
      return t("expr.error.unknown_node");
    case "node_ref_jumper":
      return t("expr.error.node_ref_jumper");
    case "unknown_function":
      return t("expr.error.unknown_function");
    case "signal_not_statement":
      return t("expr.error.signal_not_statement");
    case "wrong_argument_count":
      return t("expr.error.wrong_argument_count");
    case "invalid_signal_name":
      return t("expr.error.invalid_signal_name");
    case "type_mismatch":
      return e.type === undefined
        ? t("expr.error.type_mismatch_plain")
        : t("expr.error.type_mismatch", {
            type: typeText(t, e.type),
            expected: typesText(w, e.expected ?? []),
          });
    case "invalid_enum_value":
      return t("expr.error.invalid_enum_value");
  }
  return t("expr.error.unknown_code", { code: e.code });
}

/**
 * Returns where in source an error is, as its range in code points says,
 * counted from 1: the characters that it covers, of the line that they are
 * on where the source has several, or from a line's to another's, of a
 * range that runs across a line break, or the place between two
 * characters, or the end of the source, of one that covers none.
 */
export function placeText(
  t: TFunction,
  source: string,
  e: SourceError,
): string {
  const chars = [...source];
  // The line of the character at index, counted from 1, and its place on
  // that line, of a CR LF the LF ending the line.
  const at = (index: number) => {
    let line = 1;
    let lineStart = 0;
    for (let i = 0; i < index && i < chars.length; i++) {
      const c = chars[i];
      if (c === "\n" || (c === "\r" && chars[i + 1] !== "\n")) {
        line++;
        lineStart = i + 1;
      }
    }
    return { line, char: index - lineStart + 1 };
  };
  const several = chars.some((c) => c === "\n" || c === "\r");
  const start = at(e.start);
  if (e.end <= e.start) {
    if (e.start >= chars.length) {
      return t("expr.place.end");
    }
    return several
      ? t("expr.place.line_before", { line: start.line, char: start.char })
      : t("expr.place.before", { char: start.char });
  }
  // The last character that it covers.
  const end = at(e.end - 1);
  if (end.line !== start.line) {
    return t("expr.place.lines", {
      line: start.line,
      first: start.char,
      end: end.line,
      last: end.char,
    });
  }
  if (several) {
    return start.char === end.char
      ? t("expr.place.line_char", { line: start.line, char: start.char })
      : t("expr.place.line_chars", {
          line: start.line,
          first: start.char,
          last: end.char,
        });
  }
  return start.char === end.char
    ? t("expr.place.char", { char: start.char })
    : t("expr.place.chars", { first: start.char, last: end.char });
}

/** Returns what an error of source says: what it is, where it is, and what might be meant, if anything near is. */
export function errorText(w: Words, source: string, e: SourceError): string {
  const { t } = w;
  const problem = problemText(w, e);
  const place = placeText(t, source, e);
  const suggestions = e.suggestions ?? [];
  return suggestions.length === 0
    ? t("expr.error.at", { problem, place })
    : t("expr.error.at_suggest", {
        problem,
        place,
        suggestions: orList(w.locale, suggestions),
      });
}

/** The names of each built-in function's parameters, as its signature shows them. */
const parameters: Readonly<Record<string, readonly string[]>> = {
  min: ["a", "b"],
  max: ["a", "b"],
  abs: ["x"],
  floor: ["x"],
  ceil: ["x"],
  round: ["x"],
  clamp: ["x", "lo", "hi"],
  to_int: ["x"],
  to_float: ["x"],
  to_string: ["x"],
  len: ["s"],
  contains: ["s", "sub"],
  starts_with: ["s", "prefix"],
  ends_with: ["s", "suffix"],
  lower: ["s"],
  upper: ["s"],
  trim: ["s"],
  visited: ["node"],
  visits: ["node"],
  signal: ["name", "value"],
};

/** Returns the signature of the built-in function name, as min(a, b) or signal(name[, value]). */
export function signatureOf(name: string): string {
  const b = BUILTINS.get(name);
  const names = parameters[name] ?? [];
  if (b === undefined) {
    return `${name}()`;
  }
  const required = b.params.length - b.optional;
  const shown = b.params.map((_, i) => names[i] ?? `p${i + 1}`);
  const head = shown.slice(0, required).join(", ");
  const tail = shown.slice(required);
  return `${name}(${head}${tail.map((p, i) => (i === 0 && required === 0 ? `[${p}` : `[, ${p}`)).join("")}${"]".repeat(tail.length)})`;
}

/** Returns the type of a call of the built-in function name, in the user's language: its result's, or a number of its arguments' type, or none of signal, which is a statement. */
export function resultText(w: Words, name: string): string {
  const b = BUILTINS.get(name);
  if (b === undefined || b.effect) {
    return w.t("expr.builtin.statement");
  }
  return b.result === 0
    ? typeText(w.t, "number")
    : typesText(w, typeNamesOf(b.result));
}

/** Returns what the built-in function name does, in the user's language. */
export function builtinText(t: TFunction, name: string): string {
  switch (name) {
    case "min":
      return t("expr.builtin.min");
    case "max":
      return t("expr.builtin.max");
    case "abs":
      return t("expr.builtin.abs");
    case "floor":
      return t("expr.builtin.floor");
    case "ceil":
      return t("expr.builtin.ceil");
    case "round":
      return t("expr.builtin.round");
    case "clamp":
      return t("expr.builtin.clamp");
    case "to_int":
      return t("expr.builtin.to_int");
    case "to_float":
      return t("expr.builtin.to_float");
    case "to_string":
      return t("expr.builtin.to_string");
    case "len":
      return t("expr.builtin.len");
    case "contains":
      return t("expr.builtin.contains");
    case "starts_with":
      return t("expr.builtin.starts_with");
    case "ends_with":
      return t("expr.builtin.ends_with");
    case "lower":
      return t("expr.builtin.lower");
    case "upper":
      return t("expr.builtin.upper");
    case "trim":
      return t("expr.builtin.trim");
    case "visited":
      return t("expr.builtin.visited");
    case "visits":
      return t("expr.builtin.visits");
    case "signal":
      return t("expr.builtin.signal");
  }
  return "";
}
