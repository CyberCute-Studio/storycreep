// The expression language (PRD §5.8): its parser, checker, encoder, decoder,
// and evaluator, the TypeScript twin of the Go package
// server/internal/expr, and its numeric and string semantics, of the Go
// package server/internal/semantics: semantics holds those that read no
// table of Unicode, and unicode upper, lower, and trim, which read
// schema/unicode/tables.json. Both run the cases of schema/expr-corpus, so
// that they parse, compile, and evaluate every source alike (FR-EXPR-02
// AC1, PRD §5.8.9), and the client's instant feedback is what the server,
// which is the authority, says of a source.
//
// parse reads a source into its AST, or throws its first ExprError; check
// resolves the AST's names against a Project, at a board, and checks its
// types, its purity, and the sandbox's limits; compile does both. encode
// writes an AST's canonical form, and decode reads a published one, which
// checkResolved checks by its refs. condition and run evaluate an AST
// against an Env, which the flow's Runtime is. What parses and checks a
// source alone, as the editor's instant feedback, imports what compile.ts
// exports, @storycreep/shared/expr/compile, which is all of this module but
// the evaluator and unicode, so that it imports none of their tables.

export * from "./compile";
export {
  condition,
  type Env,
  type EvaluationError,
  evaluate,
  MAX_STEPS,
  run,
} from "./eval";
export * as unicode from "./unicode";
