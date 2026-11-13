// What a form of an expression's owner keeps of the source that its editor
// gives (ExpressionEditor.tsx), as the inspector's forms of a connection, a
// branch's output, and a node's scripts do (plan note N74): the source last
// typed, of the form shown by its key; the owner's own source, of none the
// empty one; what the form holds of the field over the owner as it is now,
// once a save is refused for a change made elsewhere; and what a notice of
// such a refusal shows of it.

import type { Expression } from "@storycreep/shared/api";

/** What a form holds of an expression's source that its editor gave last, of the form shown by its key. */
export type TypedSource = { key: string; source: string } | undefined;

/** Returns the source of the expression that an owner holds, or the empty one of none. */
export function sourceOf(e: Expression | undefined): string {
  return e?.source ?? "";
}

/**
 * Returns what a form keeps of the source that the user typed, as its
 * owner would hold it before the server compiles it: none of the empty
 * source of an optional expression, which the empty source removes.
 */
export function typedExpression(
  source: string,
  optional: boolean,
): Expression | undefined {
  return optional && source === "" ? undefined : { source, is_valid: false };
}

/** Returns what a notice of a conflict shows of an expression's value: its source, of one line, or undefined of several, which does not read as a line of text. */
export function expressionShown(value: unknown): string | undefined {
  const source = sourceOf(value as Expression | undefined);
  return /[\n\r]/.test(source) ? undefined : source;
}
