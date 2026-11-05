// What the editor reads of its documents' lists, by the limits of
// schema/rich-text/README.md.

import { MAX_LIST_DEPTH, MAX_START } from "@storycreep/shared/richtext";
import type { Node as PMNode, ResolvedPos } from "@tiptap/pm/model";

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
 * Reports whether the list item at $pos can move into a list of the item
 * before it, as Tab moves it, with no list that it holds nested deeper than
 * lists nest (MAX_LIST_DEPTH): the lists that hold it, one more, and those
 * that it holds.
 */
export function sinkFits($pos: ResolvedPos): boolean {
  for (let depth = $pos.depth; depth > 0; depth--) {
    const item = $pos.node(depth);
    if (item.type.name !== "listItem") {
      continue;
    }
    let holding = 0;
    for (let d = depth - 1; d > 0; d--) {
      if (isList($pos.node(d))) {
        holding++;
      }
    }
    return holding + 1 + listsWithin(item) <= MAX_LIST_DEPTH;
  }
  return false;
}

/**
 * Returns the start of an ordered list as an attribute writes it, as its
 * HTML's start does: an integer within the range that a document holds, the
 * nearest one to it for one beyond it, and 1 for none, or for what is not an
 * integer.
 */
export function startOf(start: unknown): number {
  const n =
    typeof start === "number"
      ? start
      : typeof start === "string" && /^\s*[+-]?\d+\s*$/.test(start)
        ? Number(start)
        : Number.NaN;
  if (!Number.isFinite(n)) {
    return 1;
  }
  return Math.min(MAX_START, Math.max(0, Math.round(n)));
}
