// What a form of an expression's owner keeps of the source that its editor
// gives (ExpressionEditor.tsx), as the inspector's forms of a connection, a
// branch's output, and a node's scripts do (plan note N74), of each field
// (useTypedSource): the source last typed, of the form shown by its key, and
// whether the guided picker holds what it cannot write yet, which the
// form's Save is refused for; the owner's own source, of none the empty one,
// which the form saves where nothing was typed in the form shown; what the
// form holds of the field over the owner as it is now, once a save is
// refused for a change made elsewhere; and what a notice of such a refusal
// shows of it.

import type { Expression } from "@storycreep/shared/api";
import { useRef } from "react";
import type { Held } from "../ui/held";

/** What a form holds of an expression's source that its editor gave last, of the form shown by its key, and whether the guided picker holds what it cannot write yet, as a number that is not one. */
type TypedSource = { key: string; source: string; unfinished: boolean };

/** Returns the source of the expression that an owner holds, or the empty one of none. */
export function sourceOf(e: Expression | undefined): string {
  return e?.source ?? "";
}

/** What a form keeps of an expression's field, of the form that held shows (useTypedSource). */
export interface TypedField {
  /** What the field's editor tells of each change: it keeps the source, and whether the picker holds what it cannot write yet, and tells the form of the change, as a field of it does, so that the form holds what it shows, as of the picker's button that clears a condition, which no field of the form tells of. */
  readonly onChange: (source: string, unfinished: boolean) => void;
  /** Returns the source that the form saves of the field: what its editor gave last, of the form shown, or else the source of shown, the expression that the form shows. */
  readonly source: (shown: Expression | undefined) => string;
  /** Reports whether the guided picker holds what it cannot write yet, of the form shown, which the form's Save is refused for, as the picker says under its field. */
  readonly unfinished: () => boolean;
}

/** Returns what a form keeps of an expression's field, of the form that held shows, whose key what it keeps is of. */
export function useTypedSource(
  held: Pick<Held<unknown>, "key" | "props">,
): TypedField {
  const typed = useRef<TypedSource>(undefined);
  return {
    onChange: (source, unfinished) => {
      typed.current = { key: held.key, source, unfinished };
      held.props.onChange();
    },
    source: (shown) =>
      typed.current?.key === held.key ? typed.current.source : sourceOf(shown),
    unfinished: () =>
      typed.current?.key === held.key && typed.current.unfinished,
  };
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

/** Returns what a notice of a conflict shows of an expression's value: its source, of one line, or undefined of several, which does not read as a line of text, or of none, as of a condition removed, which no quotes would say is gone. */
export function expressionShown(value: unknown): string | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  const source = sourceOf(value as Expression);
  return /[\n\r]/.test(source) ? undefined : source;
}
