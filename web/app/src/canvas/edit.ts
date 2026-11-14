// What the canvas's edits of a board's nodes compute (PRD FR-EDIT-02,
// FR-EDIT-06): where a drag moves the selection, snapped to the board's grid
// of 8 units unless the toolbar's toggle turns it off, what a resize makes
// of a node's size, where align and distribute put the selected nodes,
// where a new node and a duplicate go, and the tags of the board's nodes,
// compared as the server compares them, without regard to case, which the
// tag filter dims the nodes by (AC1). Each position is rounded as the server
// keeps it, to 2 decimal places, half away from zero (AC4), so that what the
// page shows at once is what the server stores.

import type { Node } from "@storycreep/shared/api";
import { foldCase } from "@storycreep/shared/fold";
import { isControlCode } from "@storycreep/shared/richtext";
import { hundredths } from "../document/appliers/node";
import { nodeRect } from "./layout";
import type { Point, Rect, Size } from "./viewport";

/** The grid that positions and sizes snap to, in units of the board (FR-EDIT-02). */
export const gridSize = 8;

/** The farthest that a node's x or y goes from the board's origin (FR-EDIT-02). */
export const positionLimit = 1_000_000_000;

/** The least and the most that a node's width or height is (FR-EDIT-02). */
export const sizeLimit = 100_000;

/**
 * The smallest size that a resize by the handle leaves a node, in units of
 * the board: room for a line of its title under its edge, as the server
 * would keep a node of 1 by 1 that no finger could find again. The
 * inspector's width and height, typed rather than dragged, take any size
 * that the server keeps.
 */
export const minSize: Readonly<Size> = Object.freeze({ width: 64, height: 40 });

/** How far a duplicate goes from its node, and a new node from one at the same place, in units of the board: four steps of the grid. */
export const offset = 4 * gridSize;

/** Returns n on the board's grid where snap is on, and rounded as the server keeps it either way. */
export function snapped(n: number, snap: boolean): number {
  return hundredths(snap ? Math.round(n / gridSize) * gridSize : n);
}

/** Returns p within the board's limits, rounded as the server keeps it. */
export function onBoard(p: Point): Point {
  const clamp = (n: number) =>
    hundredths(Math.max(-positionLimit, Math.min(positionLimit, n)));
  return { x: clamp(p.x), y: clamp(p.y) };
}

/**
 * Returns where the nodes of start go once a drag moved them by delta, in
 * units of the board: the node that the drag holds, at anchor, goes to its
 * place moved by delta, on the grid where snap is on, and the others move by
 * as much as it did, so that they keep their places relative to it, as one
 * batch moves them all (AC2). Only the nodes that move are in it, so that a
 * drag that comes back where it started moves none, as an axis that the drag
 * left as it was does not snap, of a node off the grid, as one that the
 * inspector placed.
 */
export function movedBy(
  start: ReadonlyMap<string, Point>,
  anchor: Point,
  delta: Point,
  snap: boolean,
): Map<string, Point> {
  const to = {
    x: delta.x === 0 ? anchor.x : snapped(anchor.x + delta.x, snap),
    y: delta.y === 0 ? anchor.y : snapped(anchor.y + delta.y, snap),
  };
  const dx = to.x - anchor.x;
  const dy = to.y - anchor.y;
  const out = new Map<string, Point>();
  for (const [id, p] of start) {
    const next = { x: shifted(p.x, dx), y: shifted(p.y, dy) };
    if (next.x !== p.x || next.y !== p.y) {
      out.set(id, next);
    }
  }
  return out;
}

/**
 * Returns n moved by d, within the board's limits, rounded as the server
 * keeps it, of n and d that are each in hundredths already, as a node's
 * place and a drag's snapped delta are: their sum is a number of
 * hundredths but for an error of the double far less than half of one,
 * which rounding its hundredths as a number takes away, as onBoard's
 * reading of its decimal would, without writing the decimal of each node
 * of a drag of thousands, in each of its moves, or of a paste's, each place
 * that it tries (pasteOffset).
 */
export function shifted(n: number, d: number): number {
  const sum = Math.max(-positionLimit, Math.min(positionLimit, n + d));
  const h = Math.round(sum * 100);
  return h === 0 ? 0 : h / 100;
}

/**
 * Returns the size that a resize of a node of size from gives it once its
 * corner moved by delta, in units of the board: on the grid where snap is
 * on, and no smaller than minSize, nor larger than the server takes, but for
 * a side that the drag left as it was, which keeps its size, as one off the
 * grid that the inspector gave it, so that a drag that comes back where it
 * started sizes nothing.
 */
export function resizedBy(from: Size, delta: Point, snap: boolean): Size {
  const side = (was: number, d: number, least: number) =>
    d === 0
      ? was
      : Math.max(least, Math.min(sizeLimit, snapped(was + d, snap)));
  return {
    width: side(from.width, delta.x, minSize.width),
    height: side(from.height, delta.y, minSize.height),
  };
}

/** How the selected nodes are aligned: their left edges, their centres, or their right edges, across; or their tops, middles, or bottoms, down. */
export type Alignment =
  | "left"
  | "center"
  | "right"
  | "top"
  | "middle"
  | "bottom";

/** A node with the rectangle that it takes on its board. */
export interface Placed {
  id: string;
  rect: Rect;
}

/**
 * Returns where nodes go once aligned by how: each to the left edge of the
 * leftmost, the centre of all of them, or the right edge of the rightmost,
 * across, or alike down, which only the nodes that move are in.
 */
export function aligned(
  nodes: readonly Placed[],
  how: Alignment,
): Map<string, Point> {
  const out = new Map<string, Point>();
  if (nodes.length < 2) {
    return out;
  }
  const across = how === "left" || how === "center" || how === "right";
  // The bounds by a loop, rather than by Math.min of the nodes spread as
  // arguments, which a browser refuses past as many as it takes.
  let start = Number.POSITIVE_INFINITY;
  let end = Number.NEGATIVE_INFINITY;
  for (const { rect } of nodes) {
    start = Math.min(start, across ? rect.x : rect.y);
    end = Math.max(end, across ? rect.x + rect.width : rect.y + rect.height);
  }
  for (const { id, rect } of nodes) {
    const length = across ? rect.width : rect.height;
    const at =
      how === "left" || how === "top"
        ? start
        : how === "right" || how === "bottom"
          ? end - length
          : (start + end) / 2 - length / 2;
    const next = onBoard(across ? { x: at, y: rect.y } : { x: rect.x, y: at });
    if (next.x !== rect.x || next.y !== rect.y) {
      out.set(id, next);
    }
  }
  return out;
}

/**
 * Returns where nodes go once distributed across, or down: the first and
 * the last, in the order of their left edges, or their tops, stay, and those
 * between them go so that the gaps between each node and the next are the
 * same, which only the nodes that move are in. It takes three nodes or more.
 */
export function distributed(
  nodes: readonly Placed[],
  axis: "across" | "down",
): Map<string, Point> {
  const out = new Map<string, Point>();
  if (nodes.length < 3) {
    return out;
  }
  const across = axis === "across";
  const startOf = (r: Rect) => (across ? r.x : r.y);
  const lengthOf = (r: Rect) => (across ? r.width : r.height);
  const order = [...nodes].sort(
    (a, b) =>
      startOf(a.rect) - startOf(b.rect) ||
      (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
  const first = order[0].rect;
  const last = order[order.length - 1].rect;
  const span = startOf(last) + lengthOf(last) - startOf(first);
  const taken = order.reduce((sum, n) => sum + lengthOf(n.rect), 0);
  const gap = (span - taken) / (order.length - 1);
  let at = startOf(first);
  for (const { id, rect } of order) {
    const next = onBoard(across ? { x: at, y: rect.y } : { x: rect.x, y: at });
    if (next.x !== rect.x || next.y !== rect.y) {
      out.set(id, next);
    }
    at += lengthOf(rect) + gap;
  }
  return out;
}

/**
 * Returns where a new node of size goes about centre of the board: centred
 * on it, on the grid where snap is on, and moved by offset across and down,
 * as often as it takes, where a node of the board has its corner there
 * already, as once Add was pressed twice without moving the canvas, so that
 * the new one does not hide the one before.
 */
export function freePlace(
  centre: Point,
  size: Size,
  taken: (p: Point) => boolean,
  snap: boolean,
): Point {
  let p = onBoard({
    x: snapped(centre.x - size.width / 2, snap),
    y: snapped(centre.y - size.height / 2, snap),
  });
  for (let i = 0; i < 100 && taken(p); i++) {
    p = onBoard({ x: p.x + offset, y: p.y + offset });
  }
  return p;
}

/**
 * Returns what tells whether one of nodes has its corner at a point, as a
 * node that the canvas adds, or pastes, or that the script view adds, is
 * placed beside it rather than over it (freePlace, pasteOffset).
 */
export function cornerTakenOf(
  nodes: Iterable<Pick<Node, "position">>,
): (corner: Point) => boolean {
  const corners = new Set<string>();
  for (const n of nodes) {
    corners.add(`${n.position.x},${n.position.y}`);
  }
  return (p) => corners.has(`${p.x},${p.y}`);
}

/** Returns the rectangles of nodes, as the canvas lays them out. */
export function placedOf(nodes: Iterable<Node>): Placed[] {
  return [...nodes].map((node) => ({ id: node.id, rect: nodeRect(node) }));
}

/** The most tags that a node has (FR-EDIT-06), as the server keeps them. */
export const maxTags = 32;

/** Returns the key by which two tags are one tag, as the server compares them: without regard to case (FR-EDIT-06). */
export function tagKey(tag: string): string {
  return foldCase(tag);
}

/**
 * Reports whether tag holds a character that the server refuses in a tag,
 * as in every name (input.IsControl): a control character, a tab among
 * them, a bidirectional control, as U+202E RIGHT-TO-LEFT OVERRIDE, or a
 * line or paragraph separator, for which it would refuse the tag of every
 * node that it is added to.
 */
export function hasControlIn(tag: string): boolean {
  for (let i = 0; i < tag.length; i++) {
    const c = tag.charCodeAt(i);
    if (c === 0x09 || isControlCode(c)) {
      return true;
    }
  }
  return false;
}

/**
 * A character that a name shows, as the server tells it (input.Printed): a
 * letter, a mark, a number, a punctuation mark, or a symbol, rather than a
 * space, a format character, as U+200B ZERO WIDTH SPACE, or another that
 * shows nothing, as a variation selector, U+3164 HANGUL FILLER, or U+2800
 * BRAILLE PATTERN BLANK, and the server's blanks of the planes beyond.
 */
const printedChar =
  /(?![\p{White_Space}\p{Default_Ignorable_Code_Point}\u2800\u{13441}\u{13442}\u{16FE4}\u{1D159}])[\p{L}\p{M}\p{N}\p{P}\p{S}]/u;

/**
 * Reports whether tag shows a character (printedChar), without which the
 * server refuses it as empty, as a tag of a zero width space alone, for
 * which it would refuse the tag of every node that it is added to.
 */
export function hasPrintedIn(tag: string): boolean {
  return printedChar.test(tag);
}

/**
 * The keys of each list of tags that tagKeys has read, as a node keeps its
 * list, the same object, until its tags change, so that each tag of it is
 * folded once, rather than in each frame that the tag filter dims it in.
 */
const keysOfTags = new WeakMap<readonly string[], readonly string[]>();

/** Returns the keys of tags (tagKey). */
function tagKeys(tags: readonly string[]): readonly string[] {
  let keys = keysOfTags.get(tags);
  if (keys === undefined) {
    keys = tags.map(tagKey);
    keysOfTags.set(tags, keys);
  }
  return keys;
}

/**
 * Returns what reports whether a node has tag, whatever its case, which
 * folds tag once, and each node's tags once (tagKeys), as the tag filter
 * asks of each node that the canvas draws, in each frame.
 */
export function hasTagOf(tag: string): (node: Pick<Node, "tags">) => boolean {
  const key = tagKey(tag);
  return (node) => tagKeys(node.tags).includes(key);
}

/**
 * Returns how many of nodes have each tag, by its key (tagKey), whatever
 * its case, of each node's tags folded once (tagKeys), rather than each
 * node's tags folded again for each tag counted.
 */
export function tagCounts(
  nodes: Iterable<Pick<Node, "tags">>,
): Map<string, number> {
  const counts = new Map<string, number>();
  for (const node of nodes) {
    for (const key of new Set(tagKeys(node.tags))) {
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
  }
  return counts;
}

/**
 * Compares a and b by their code points, as the server compares strings, by
 * their bytes of UTF-8, rather than by their UTF-16 code units, as < does,
 * which puts a character beyond U+FFFF, of a pair of surrogates, before one
 * from U+E000 to U+FFFF, as an emoji before a fullwidth letter.
 */
export function byCodePoints(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const x = a.charCodeAt(i);
    const y = b.charCodeAt(i);
    if (x !== y) {
      return codePointRank(x) - codePointRank(y);
    }
  }
  return a.length - b.length;
}

/** Returns a code unit's place in the order of code points: a surrogate's after U+FFFF, and those from U+E000 before it. */
function codePointRank(c: number): number {
  return c < 0xd800 ? c : c < 0xe000 ? c + 0x2000 : c - 0x800;
}

/**
 * Returns the tags of nodes, each once, whatever its case, in the spelling
 * that the most of them write, or, of spellings that as many write, the
 * first in the order of their code points, as the server suggests them, the
 * most used first, then in the order of their keys (byCodePoints).
 */
export function tagsOf(nodes: Iterable<Pick<Node, "tags">>): string[] {
  const byKey = new Map<
    string,
    { spellings: Map<string, number>; uses: number }
  >();
  for (const node of nodes) {
    const keys = tagKeys(node.tags);
    for (const [i, tag] of node.tags.entries()) {
      const key = keys[i];
      let entry = byKey.get(key);
      if (entry === undefined) {
        entry = { spellings: new Map(), uses: 0 };
        byKey.set(key, entry);
      }
      entry.uses++;
      entry.spellings.set(tag, (entry.spellings.get(tag) ?? 0) + 1);
    }
  }
  const best = (spellings: Map<string, number>) => {
    let out = "";
    let most = -1;
    for (const [tag, uses] of spellings) {
      if (uses > most || (uses === most && byCodePoints(tag, out) < 0)) {
        out = tag;
        most = uses;
      }
    }
    return out;
  };
  return [...byKey]
    .sort(([a, x], [b, y]) => y.uses - x.uses || byCodePoints(a, b))
    .map(([, entry]) => best(entry.spellings));
}

/**
 * Returns what returns tags without tag, whatever its case, which folds tag
 * once, and each list of tags once (tagKeys), as a removal of it from each
 * of thousands of nodes asks.
 */
export function withoutTagOf(
  tag: string,
): (tags: readonly string[]) => string[] {
  const key = tagKey(tag);
  return (tags) => {
    const keys = tagKeys(tags);
    return tags.filter((_, i) => keys[i] !== key);
  };
}
