// What part of a board the canvas shows (PRD FR-EDIT-01): the point of the
// board at the canvas's top-left corner and the zoom, as the screen shows
// the board's units, in CSS pixels. A board's units are a node's position's
// and size's (FR-EDIT-02); at a zoom of 1, one of them is one CSS pixel.

/** A point, of the board or of the screen. */
export interface Point {
  x: number;
  y: number;
}

/** A rectangle, by its top-left corner, its width, and its height. */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A width and a height, in CSS pixels. */
export interface Size {
  width: number;
  height: number;
}

/** The part of a board that the canvas shows. */
export interface Viewport {
  /** The board's point at the canvas's top-left corner. */
  x: number;
  y: number;
  /** How many CSS pixels a unit of the board is: 0.1 to 4 (10%–400%). */
  zoom: number;
}

/** The least zoom, 10%. */
export const minZoom = 0.1;

/** The greatest zoom, 400%. */
export const maxZoom = 4;

/**
 * The zoom below which nodes are simplified blocks, without text (PRD
 * FR-EDIT-01 AC4).
 */
export const detailZoom = 0.4;

/**
 * The Canvas 2D renderer's reduced limits (ADR-013): it draws each sprite
 * on its own, so it shows cards with text only while at most this many
 * nodes are in the canvas, and blocks beyond, and a card's text in fewer
 * lines.
 */
export const canvasDetailLimit = 300;

/**
 * How few nodes in the canvas bring the Canvas 2D renderer's cards back,
 * once it showed blocks for more than canvasDetailLimit: a view that holds
 * about the limit, as one that pans across a board of about as many nodes
 * to a canvas, would else switch between cards and blocks, and build every
 * object of its margin anew, as each node came into it or left it.
 */
export const canvasDetailReturn = 240;

/**
 * Reports whether the Canvas 2D renderer shows blocks, for being crowded,
 * at zoom, with inView nodes in the canvas, as it did before (was): from
 * more than canvasDetailLimit, until canvasDetailReturn or fewer are again.
 * Below detailZoom, where the zoom shows blocks of itself, it is not, so
 * that a zoom in past detailZoom shows cards for as many nodes as the
 * limit, rather than for as few as bring them back.
 */
export function crowdedAt(zoom: number, inView: number, was: boolean): boolean {
  return (
    zoom >= detailZoom &&
    inView > (was ? canvasDetailReturn : canvasDetailLimit)
  );
}

/**
 * How far a point of the board may be from its origin, as a node's position
 * may (FR-EDIT-02), with the room that a node and a margin take beyond it.
 */
const extent = 1_000_200_000;

/**
 * What the canvas shows of a board without nodes: its origin, at 100%, 48
 * CSS pixels from the canvas's corner, as fit to content leaves 48 around
 * what it shows. It is frozen, as the canvas shows it as it is, and hands
 * it to the benchmark as its viewport: one that a caller changed would be
 * every later empty board's, and its minimap's.
 */
export const emptyView: Readonly<Viewport> = Object.freeze({
  x: -48,
  y: -48,
  zoom: 1,
});

/** The zooms that the zoom buttons and keys step through. */
const stops = [
  0.1, 0.15, 0.2, 0.25, 0.33, 0.4, 0.5, 0.67, 0.75, 1, 1.25, 1.5, 2, 3, 4,
] as const;

/** Returns zoom within the canvas's range. */
export function clampZoom(zoom: number): number {
  if (!Number.isFinite(zoom)) {
    return 1;
  }
  return Math.min(maxZoom, Math.max(minZoom, zoom));
}

/** Returns the point of the screen, relative to the canvas, that shows p of the board. */
export function toScreen(v: Viewport, p: Point): Point {
  return { x: (p.x - v.x) * v.zoom, y: (p.y - v.y) * v.zoom };
}

/** Returns the point of the board that p of the screen, relative to the canvas, shows. */
export function toWorld(v: Viewport, p: Point): Point {
  return { x: v.x + p.x / v.zoom, y: v.y + p.y / v.zoom };
}

/** Returns v kept within the board's extent. */
function bounded(v: Viewport): Viewport {
  return {
    x: Math.min(extent, Math.max(-extent, v.x)),
    y: Math.min(extent, Math.max(-extent, v.y)),
    zoom: v.zoom,
  };
}

/**
 * Returns v zoomed to zoom, within the canvas's range, so that the point of
 * the board that the screen's point at shows stays where it is.
 */
export function zoomAt(v: Viewport, at: Point, zoom: number): Viewport {
  return anchorAt(toWorld(v, at), at, zoom);
}

/**
 * Returns v moved so that the point of the board anchor shows at the
 * screen's point at, with the zoom zoom: as two fingers that pinch hold the
 * point between them (FR-EDIT-01 AC1).
 */
export function anchorAt(anchor: Point, at: Point, zoom: number): Viewport {
  const z = clampZoom(zoom);
  return bounded({ x: anchor.x - at.x / z, y: anchor.y - at.y / z, zoom: z });
}

/** Returns v moved by dx and dy CSS pixels of the screen: the board moves with them. */
export function panBy(v: Viewport, dx: number, dy: number): Viewport {
  return bounded({ x: v.x - dx / v.zoom, y: v.y - dy / v.zoom, zoom: v.zoom });
}

/** Returns the rectangle of the board that a canvas of size shows at v. */
export function visibleRect(v: Viewport, size: Size): Rect {
  return {
    x: v.x,
    y: v.y,
    width: size.width / v.zoom,
    height: size.height / v.zoom,
  };
}

/** Returns the point of the board at the centre of a canvas of size at v. */
export function centerOf(v: Viewport, size: Size): Point {
  return toWorld(v, { x: size.width / 2, y: size.height / 2 });
}

/**
 * Returns the viewport that shows bounds whole, centred in a canvas of size,
 * with padding CSS pixels around it, at the greatest zoom that does, up to
 * max: fit to content, and zoom to selection.
 */
export function fitTo(
  bounds: Rect,
  size: Size,
  padding = 48,
  max = 1,
): Viewport {
  const room = {
    width: Math.max(1, size.width - 2 * padding),
    height: Math.max(1, size.height - 2 * padding),
  };
  const zoom = clampZoom(
    Math.min(
      max,
      room.width / Math.max(1, bounds.width),
      room.height / Math.max(1, bounds.height),
    ),
  );
  return anchorAt(
    { x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height / 2 },
    { x: size.width / 2, y: size.height / 2 },
    zoom,
  );
}

/**
 * Returns the next zoom from zoom of the stops, up or down, so that zooming
 * in and out by steps lands on round percentages.
 */
export function stepZoom(zoom: number, direction: 1 | -1): number {
  // A zoom within a percent of a stop counts as at it.
  const near = 1.01;
  if (direction > 0) {
    return stops.find((s) => s > zoom * near) ?? maxZoom;
  }
  return [...stops].reverse().find((s) => s < zoom / near) ?? minZoom;
}

/**
 * Returns the viewport at t, from 0 to 1, of the way from a to b, eased, at
 * a canvas of size: the zoom changes by the same ratio in each moment, and
 * the centre moves with it, so that a zoom to a far node does not race past
 * it first.
 */
export function between(
  a: Viewport,
  b: Viewport,
  t: number,
  size: Size,
): Viewport {
  const e = t >= 1 ? 1 : 1 - (1 - t) ** 3;
  const zoom = a.zoom * (b.zoom / a.zoom) ** e;
  const ca = centerOf(a, size);
  const cb = centerOf(b, size);
  return anchorAt(
    { x: ca.x + (cb.x - ca.x) * e, y: ca.y + (cb.y - ca.y) * e },
    { x: size.width / 2, y: size.height / 2 },
    zoom,
  );
}

/** Reports whether a and b show the same part of a board, within a thousandth of a pixel. */
export function sameViewport(a: Viewport, b: Viewport): boolean {
  return (
    Math.abs(a.zoom - b.zoom) < 1e-9 &&
    Math.abs((a.x - b.x) * a.zoom) < 1e-3 &&
    Math.abs((a.y - b.y) * a.zoom) < 1e-3
  );
}

/** Returns the smallest rectangle that holds every one of rects, or undefined for none. */
export function union(rects: Iterable<Rect>): Rect | undefined {
  let minX = Number.POSITIVE_INFINITY;
  let minY = Number.POSITIVE_INFINITY;
  let maxX = Number.NEGATIVE_INFINITY;
  let maxY = Number.NEGATIVE_INFINITY;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.width);
    maxY = Math.max(maxY, r.y + r.height);
  }
  if (minX > maxX) {
    return undefined;
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

/** Reports whether a and b overlap, or touch. */
export function intersects(a: Rect, b: Rect): boolean {
  return (
    a.x <= b.x + b.width &&
    b.x <= a.x + a.width &&
    a.y <= b.y + b.height &&
    b.y <= a.y + a.height
  );
}

/** Reports whether outer holds inner whole. */
export function contains(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}

/** Returns r grown by its own width and height times by on each side. */
export function grow(r: Rect, by: number): Rect {
  return {
    x: r.x - r.width * by,
    y: r.y - r.height * by,
    width: r.width * (1 + 2 * by),
    height: r.height * (1 + 2 * by),
  };
}

/** Returns the rectangle of the two corners a and b, in any order. */
export function rectOf(a: Point, b: Point): Rect {
  return {
    x: Math.min(a.x, b.x),
    y: Math.min(a.y, b.y),
    width: Math.abs(a.x - b.x),
    height: Math.abs(a.y - b.y),
  };
}
