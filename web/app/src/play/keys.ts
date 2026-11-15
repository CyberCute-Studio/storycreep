// What Escape does in the sections of play's panel that hold fields, its
// inspector and its saved play states (plan notes N79 and N80): none closes
// the sheet that the panel is on a narrow window, which would stop play and
// lose its history, and one in a field does what the field's own Escape
// does, but for one that cancels what an input method was composing, which
// is the input method's.

import type { KeyboardEvent } from "react";
import { inputMethodTook } from "../richtext/suggest";

/**
 * Keeps an Escape pressed in a section of play's panel from the sheet that
 * the panel is, which would close at it, stopping play, as one pressed once
 * more than there was to empty, to cancel, or to go back from would: in a
 * field, it empties the field, cancels, or goes back alone (escaped).
 */
export function keepSheetOpen(event: KeyboardEvent<HTMLElement>): void {
  if (event.key === "Escape") {
    event.stopPropagation();
  }
}

/**
 * Returns the handler of a key in a field of play's panel, or in a form of
 * its fields, which runs onEscape for an Escape, but for one that cancels
 * what an input method was composing, which is the input method's, and does
 * nothing else (inputMethodTook).
 */
export function escaped(onEscape: () => void) {
  return (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape" && !inputMethodTook(event.nativeEvent)) {
      onEscape();
    }
  };
}
