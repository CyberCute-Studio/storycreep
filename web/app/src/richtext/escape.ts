// The Escape that closes a form that holds a rich text, as the canvas's
// editor of an element's text and the script view's forms, where it holds
// no change. ProseMirror takes every Escape in its text as its own
// (captureKeyDown, which calls preventDefault), so such a form cannot tell
// its own Escape by whether something took the key already, which every
// one in its text has: it tells it by where it went down, and leaves the
// keys that something within the text took for the text.

import type { KeyboardEvent } from "react";
import { inputMethodTook, suggestionTook } from "./suggest";

/**
 * Reports whether e, a key that went down in a form, is the Escape that
 * closes the form, where it holds no change: one of the form's own fields,
 * rather than of a dialog of its rich text, as the link's, which is in a
 * portal, whose keys React brings to the form too; not one that an input
 * method took, as one that composes, or the Escape that cancels what it
 * composed (inputMethodTook); and not one that the list of a { or an @ of
 * its rich text took, which closes the list alone (suggestionTook), as the
 * list that the caret opens, of no change, once it is moved after a { or an
 * @ that the text holds.
 */
export function leavesBy(e: KeyboardEvent<HTMLElement>): boolean {
  return (
    e.key === "Escape" &&
    !inputMethodTook(e.nativeEvent) &&
    !suggestionTook(e.nativeEvent) &&
    // The DOM's Node, rather than a type of the same name of the API's.
    e.target instanceof globalThis.Node &&
    e.currentTarget.contains(e.target)
  );
}
