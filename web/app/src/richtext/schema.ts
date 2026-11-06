// What the editor reads of its documents' lists, by the limits of
// schema/rich-text/README.md, and of the elements of its mentions and value
// references.

import type { IdKind } from "@storycreep/shared/ids";
import {
  idKindOf,
  MAX_LIST_DEPTH,
  MAX_START,
} from "@storycreep/shared/richtext";
import type { Node as PMNode, ResolvedPos } from "@tiptap/pm/model";

/** Reports whether id is the public ID of an entity of one of the kinds. */
function isIdOf(id: string | null, kinds: readonly IdKind[]): id is string {
  const kind = id === null ? undefined : idKindOf(id);
  return kind !== undefined && kinds.includes(kind);
}

/** Reports whether the element of a mention names a component, as the editor's mention does. */
export function isMentionElement(element: Element): boolean {
  return isIdOf(element.getAttribute("data-id"), ["component"]);
}

/** Reports whether the element of a value reference names a variable's value, without an attribute, or a component's attribute's, as the editor's value reference does. */
export function isValueRefElement(element: Element): boolean {
  const ref = element.getAttribute("data-ref");
  const attribute = element.getAttribute("data-attribute");
  return (
    (isIdOf(ref, ["variable"]) && attribute === null) ||
    (isIdOf(ref, ["component"]) && isIdOf(attribute, ["attribute_def"]))
  );
}

/** Reports whether node is a list, bulleted or numbered. */
export function isList(node: PMNode): boolean {
  return node.type.name === "bulletList" || node.type.name === "orderedList";
}

/** Returns the most lists that nest within node, below it: 0 for none, and 1 for lists that hold no list. */
export function listsWithin(node: PMNode): number {
  let most = 0;
  node.forEach((child) => {
    most = Math.max(most, (isList(child) ? 1 : 0) + listsWithin(child));
  });
  return most;
}

/**
 * Reports whether the list items from $from to $to, those that Tab moves as
 * ProseMirror's sinkListItem finds them, can move into a list of the item
 * before them, with no list that one of them holds nested deeper than lists
 * nest (MAX_LIST_DEPTH): the lists that hold them, one more, and those that
 * the item that holds the most holds.
 */
export function sinkFits(
  $from: ResolvedPos,
  $to: ResolvedPos = $from,
): boolean {
  const range = $from.blockRange(
    $to,
    (node) => node.firstChild?.type.name === "listItem",
  );
  if (range === null) {
    return false;
  }
  let holding = 0;
  for (let d = range.depth; d > 0; d--) {
    if (isList($from.node(d))) {
      holding++;
    }
  }
  let most = 0;
  for (let i = range.startIndex; i < range.endIndex; i++) {
    most = Math.max(most, listsWithin(range.parent.child(i)));
  }
  return holding + 1 + most <= MAX_LIST_DEPTH;
}

/**
 * Returns the start of an ordered list as an attribute writes it, as its
 * HTML's start does, by HTML's rules for parsing integers, which read the
 * digits that it begins with, after white space and a sign, as 5 of "5a" or
 * "5.0": an integer within the range that a document holds, the nearest one
 * to it for one beyond it, and 1 for none, or for what does not begin with
 * an integer.
 */
export function startOf(start: unknown): number {
  const digits =
    typeof start === "string" ? /^[\t\n\f\r ]*([+-]?\d+)/.exec(start) : null;
  const n =
    typeof start === "number"
      ? start
      : digits !== null
        ? Number(digits[1])
        : Number.NaN;
  if (!Number.isFinite(n)) {
    return 1;
  }
  return Math.min(MAX_START, Math.max(0, Math.round(n)));
}
