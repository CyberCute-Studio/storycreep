// The expression language (PRD §5.8): its parser, checker, encoder, decoder,
// and evaluator, the TypeScript twin of the Go package
// server/internal/expr, and its numeric and string semantics, of the Go
// package server/internal/semantics. Both run the cases of
// schema/expr-corpus, so that they parse, compile, and evaluate every source
// alike (FR-EXPR-02 AC1, PRD §5.8.9), and the client's instant feedback is
// what the server, which is the authority, says of a source.
//
// parse reads a source into its AST, or throws its first ExprError; check
// resolves the AST's names against a Project, at a board, and checks its
// types, its purity, and the sandbox's limits; compile does both. encode
// writes an AST's canonical form, and decode reads a published one, which
// checkResolved checks by its refs. condition and run evaluate an AST
// against an Env, which the flow's Runtime is.

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
  condition,
  type Env,
  type EvaluationError,
  evaluate,
  MAX_STEPS,
  run,
} from "./eval";
export {
  compareNumbers,
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
  newProject,
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
