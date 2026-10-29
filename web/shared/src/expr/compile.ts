// What the editor's instant feedback needs of the expression language (PRD
// §5.8), exported as @storycreep/shared/expr/compile: everything that
// @storycreep/shared/expr exports, which re-exports it, but the evaluator,
// and with it unicode upper, lower, and trim, so that it imports none of the
// tables of Unicode that they alone read (unicode.ts,
// schema/unicode/tables.json), and a bundle of what parses and checks a
// source holds none of them, whatever its bundler makes of a module's
// top-level reads of a JSON file.

export type {
  Assign,
  AssignOp,
  Attr,
  Binary,
  BinaryOp,
  Call,
  Context,
  Kind,
  Lit,
  Node,
  NodeRef,
  Range,
  Scope,
  Script,
  Unary,
  UnaryOp,
  ValueType,
  Var,
} from "./ast";
export {
  isIdentifier,
  isKey,
  MAX_ARGUMENTS,
  MAX_DEPTH,
  MAX_IDENTIFIER_LENGTH,
  MAX_NODES,
  MAX_SIGNALS,
  MAX_STATEMENTS,
  NO_RANGE,
  RESERVED_WORDS,
} from "./ast";
export { AST_VERSION, builtins, versionOf } from "./builtins";
export { check, checkResolved, compile } from "./check";
export { DecodeError, decode } from "./decode";
export { encode } from "./encode";
export {
  COMPILE_CODES,
  type Code,
  ExprError,
  SYNTAX_CODES,
  type TypeName,
  UsageError,
} from "./error";
export {
  compareNumbers,
  fixedView,
  isIntegral,
  isJsonObject,
  JsonError,
  JsonNumber,
  type JsonValue,
  plain,
  readJSON,
  readJSONBytes,
} from "./json";
export { parse } from "./parser";
export {
  type Attribute,
  type AttributeType,
  type BoardNode,
  type Component,
  Project,
  type Variable,
} from "./project";
export * as semantics from "./semantics";
export { walk } from "./shape";
export { MAX_SUGGESTIONS } from "./suggest";
export {
  asFloat,
  boolValue,
  type DeclaredType,
  equalValue,
  floatValue,
  intValue,
  jsonOf,
  MAX_TEXT_LENGTH,
  NONE,
  readValue,
  sameValue,
  stringValue,
  type Value,
  valueString,
} from "./value";
