// Where the canvas draws a node and the connections between nodes. A node's
// position is its top-left corner on its board (PRD FR-EDIT-02); a node
// without a size of its own is laid out by its type, as the canvas lays it
// out by itself: an element as a card of its title and text, a branch with
// a row of each of its outputs, each with its port, and a jumper as a
// smaller card of its target. A connection leaves its port on the node's
// right edge and enters the node's left edge, as a curve.

import type { Connection, Node } from "@storycreep/shared/api";
import type { Point, Rect } from "./viewport";

/** The width of a node that has no size of its own. */
export const nodeWidth = 240;

/** The height of an element that has no size of its own. */
export const elementHeight = 128;

/** The height of a jumper that has no size of its own. */
export const jumperHeight = 72;

/** The height of a node's head, its title and its type, above a branch's outputs. */
export const headHeight = 56;

/** The height of a branch's row of one output. */
export const outputRow = 28;

/** Returns the height of a branch of count outputs that has no size of its own. */
function branchHeight(count: number): number {
  return Math.max(96, headHeight + count * outputRow + 8);
}

/** Returns the rectangle that node takes on its board. */
export function nodeRect(node: Node): Rect {
  const { x, y } = node.position;
  if (node.size?.width !== undefined && node.size.height !== undefined) {
    return { x, y, width: node.size.width, height: node.size.height };
  }
  switch (node.type) {
    case "element":
      return { x, y, width: nodeWidth, height: elementHeight };
    case "jumper":
      return { x, y, width: nodeWidth, height: jumperHeight };
    case "branch":
      return {
        x,
        y,
        width: nodeWidth,
        height: branchHeight(node.outputs?.length ?? 0),
      };
    default:
      // A type that the canvas does not know, as a newer server's may send
      // to a page loaded before it, is laid out as an element is, rather
      // than as nothing, which the spatial index, and then each frame,
      // would throw at, leaving the board blank.
      return { x, y, width: nodeWidth, height: elementHeight };
  }
}

/**
 * Returns the distance from a branch's top to the middle of the row of its
 * output at index, of count, in a node of height: rows of outputRow under
 * its head, closer where a size of its own leaves them less room.
 */
export function outputOffset(
  index: number,
  count: number,
  height: number,
): number {
  const room = Math.max(0, height - headHeight - 8);
  const row = count === 0 ? outputRow : Math.min(outputRow, room / count);
  return headHeight + row * index + row / 2;
}

/**
 * Returns where a connection that leaves port of node, in its rectangle
 * rect, starts: the node's right edge, at the middle of an element, or of
 * the row of a branch's output; a port that the node does not have, as an
 * output in the trash, at its middle.
 */
export function portPoint(node: Node, rect: Rect, port: string): Point {
  const outputs = node.outputs ?? [];
  const index =
    node.type === "branch" ? outputs.findIndex((o) => o.id === port) : -1;
  return {
    x: rect.x + rect.width,
    y:
      rect.y +
      (index < 0
        ? rect.height / 2
        : outputOffset(index, outputs.length, rect.height)),
  };
}

/** Returns where a connection enters a node, in its rectangle rect: the middle of its left edge. */
export function entryPoint(rect: Rect): Point {
  return { x: rect.x, y: rect.y + rect.height / 2 };
}

/** A cubic Bézier curve of a connection: from p0 to p3, pulled towards c1 and c2. */
export interface Curve {
  p0: Point;
  c1: Point;
  c2: Point;
  p3: Point;
}

/**
 * Returns the curve of a connection from the port at from to the entry at
 * to, which leaves and enters horizontally, and loops above the nodes of a
 * connection that enters the node that it leaves (self).
 */
export function curveOf(from: Point, to: Point, self: boolean): Curve {
  if (self) {
    return {
      p0: from,
      c1: { x: from.x + 96, y: from.y - 160 },
      c2: { x: to.x - 96, y: to.y - 160 },
      p3: to,
    };
  }
  const dx = to.x - from.x;
  const pull =
    dx >= 0
      ? Math.min(400, Math.max(40, dx / 2))
      : Math.min(400, 80 + Math.abs(to.y - from.y) / 4 + Math.abs(dx) / 4);
  return {
    p0: from,
    c1: { x: from.x + pull, y: from.y },
    c2: { x: to.x - pull, y: to.y },
    p3: to,
  };
}

/**
 * Returns the smallest rectangle that holds curve whole: that of its ends
 * and of the points where it turns back, rather than that of its four
 * points, whose controls lie well beyond it, as a loop's 160 units above
 * its node, of which the curve reaches 120, so that fit to content, which
 * shows the board's connections whole, does not show room that they leave
 * empty.
 */
export function curveBounds(c: Curve): Rect {
  const [x, right] = extent(c.p0.x, c.c1.x, c.c2.x, c.p3.x);
  const [y, bottom] = extent(c.p0.y, c.c1.y, c.c2.y, c.p3.y);
  return { x, y, width: right - x, height: bottom - y };
}

/**
 * Returns the least and the most that a coordinate of a cubic Bézier curve
 * takes, of its ends, p0 and p3, and its controls, c1 and c2: those of its
 * ends, or of where its derivative, a quadratic, is nothing between them.
 */
function extent(
  p0: number,
  c1: number,
  c2: number,
  p3: number,
): [number, number] {
  let min = Math.min(p0, p3);
  let max = Math.max(p0, p3);
  // The derivative is 3 (a t² + b t + d0).
  const d0 = c1 - p0;
  const d1 = c2 - c1;
  const d2 = p3 - c2;
  const a = d0 - 2 * d1 + d2;
  const b = 2 * (d1 - d0);
  const roots: number[] = [];
  if (Math.abs(a) < 1e-12) {
    if (Math.abs(b) > 1e-12) {
      roots.push(-d0 / b);
    }
  } else {
    const discriminant = b * b - 4 * a * d0;
    if (discriminant >= 0) {
      const s = Math.sqrt(discriminant);
      roots.push((-b + s) / (2 * a), (-b - s) / (2 * a));
    }
  }
  for (const t of roots) {
    if (t > 0 && t < 1) {
      const u = 1 - t;
      const at =
        u * u * u * p0 +
        3 * u * u * t * c1 +
        3 * u * t * t * c2 +
        t * t * t * p3;
      min = Math.min(min, at);
      max = Math.max(max, at);
    }
  }
  return [min, max];
}

/** Returns the curve of connection, between its nodes' rectangles, as the nodes are. */
export function connectionCurve(
  connection: Connection,
  from: { node: Node; rect: Rect },
  to: { rect: Rect },
): Curve {
  return curveOf(
    portPoint(from.node, from.rect, connection.from_port),
    entryPoint(to.rect),
    connection.from_node_id === connection.to_node_id,
  );
}
