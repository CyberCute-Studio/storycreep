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
import { nodeRect } from "./layout";
import type { Point, Rect, Size } from "./viewport";

/** The grid that positions and sizes snap to, in units of the board (FR-EDIT-02). */
export const gridSize = 8;

/** The farthest that a node's x or y goes from the board's origin (FR-EDIT-02). */
export const positionLimit = 1_000_000_000;

/** The least and the most that a node's width or height is (FR-EDIT-02). */
export const sizeLimit = 100_000;

/**
 * The smallest size that a resize leaves a node, in units of the board: room
 * for a line of its title under its edge, as the server would keep a node of
 * 1 by 1 that no finger could find again.
 */
export const minSize: Readonly<Size> = Object.freeze({ width: 64, height: 40 });

/** How far a duplicate goes from its node, and a new node from one at the same place, in units of the board: four steps of the grid. */
export const offset = 4 * gridSize;

/** Returns n rounded to 2 decimal places, half away from zero, as the server keeps a node's position and size (FR-EDIT-02 AC4). */
export function round2(n: number): number {
  const r = Math.round(Math.abs(n) * 100) / 100;
  return r === 0 ? 0 : Math.sign(n) * r;
}

/** Returns n on the board's grid where snap is on, and rounded as the server keeps it either way. */
export function snapped(n: number, snap: boolean): number {
  return round2(snap ? Math.round(n / gridSize) * gridSize : n);
}

/** Returns p within the board's limits, rounded as the server keeps it. */
export function onBoard(p: Point): Point {
  const clamp = (n: number) =>
    round2(Math.max(-positionLimit, Math.min(positionLimit, n)));
  return { x: clamp(p.x), y: clamp(p.y) };
}

/**
 * Returns where the nodes of start go once a drag moved them by delta, in
 * units of the board: the node that the drag holds, at anchor, goes to its
 * place moved by delta, on the grid where snap is on, and the others move by
 * as much as it did, so that they keep their places relative to it, as one
 * batch moves them all (AC2). Only the nodes that move are in it, so that a
 * drag that comes back where it started moves none.
 */
export function movedBy(
  start: ReadonlyMap<string, Point>,
  anchor: Point,
  delta: Point,
  snap: boolean,
): Map<string, Point> {
  const to = {
    x: snapped(anchor.x + delta.x, snap),
    y: snapped(anchor.y + delta.y, snap),
  };
  const dx = to.x - anchor.x;
  const dy = to.y - anchor.y;
  const out = new Map<string, Point>();
  for (const [id, p] of start) {
    const next = onBoard({ x: p.x + dx, y: p.y + dy });
    if (next.x !== p.x || next.y !== p.y) {
      out.set(id, next);
    }
  }
  return out;
}

/**
 * Returns the size that a resize of a node of size from gives it once its
 * corner moved by delta, in units of the board: on the grid where snap is
 * on, and no smaller than minSize, nor larger than the server takes.
 */
export function resizedBy(from: Size, delta: Point, snap: boolean): Size {
  const side = (n: number, least: number) =>
    Math.max(least, Math.min(sizeLimit, snapped(n, snap)));
  return {
    width: side(from.width + delta.x, minSize.width),
    height: side(from.height + delta.y, minSize.height),
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
  const start = Math.min(...nodes.map((n) => (across ? n.rect.x : n.rect.y)));
  const end = Math.max(
    ...nodes.map((n) =>
      across ? n.rect.x + n.rect.width : n.rect.y + n.rect.height,
    ),
  );
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

/** Returns the rectangles of nodes, as the canvas lays them out. */
export function placedOf(nodes: Iterable<Node>): Placed[] {
  return [...nodes].map((node) => ({ id: node.id, rect: nodeRect(node) }));
}

/** Returns the key by which two tags are one tag, as the server compares them: without regard to case (FR-EDIT-06). */
export function tagKey(tag: string): string {
  return foldCase(tag);
}

/** Reports whether node has tag, whatever its case. */
export function hasTag(node: Pick<Node, "tags">, tag: string): boolean {
  const key = tagKey(tag);
  return node.tags.some((t) => tagKey(t) === key);
}

/**
 * Returns the tags of nodes, each once, whatever its case, in the spelling
 * that the most of them write, or, of spellings that as many write, the
 * first in the order of their code units, as the server suggests them, the
 * most used first, then in the order of their keys.
 */
export function tagsOf(nodes: Iterable<Pick<Node, "tags">>): string[] {
  const byKey = new Map<
    string,
    { spellings: Map<string, number>; uses: number }
  >();
  for (const node of nodes) {
    for (const tag of node.tags) {
      const key = tagKey(tag);
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
      if (uses > most || (uses === most && tag < out)) {
        out = tag;
        most = uses;
      }
    }
    return out;
  };
  return [...byKey]
    .sort(([a, x], [b, y]) => y.uses - x.uses || (a < b ? -1 : a > b ? 1 : 0))
    .map(([, entry]) => best(entry.spellings));
}

/** Returns tags with tag added, unless it has it already, whatever its case. */
export function withTag(tags: readonly string[], tag: string): string[] {
  const key = tagKey(tag);
  return tags.some((t) => tagKey(t) === key) ? [...tags] : [...tags, tag];
}

/** Returns tags without tag, whatever its case. */
export function withoutTag(tags: readonly string[], tag: string): string[] {
  const key = tagKey(tag);
  return tags.filter((t) => tagKey(t) !== key);
}
