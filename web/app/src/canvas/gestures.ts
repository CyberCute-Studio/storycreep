// How the canvas reads pointers and the wheel (PRD FR-EDIT-01, NFR-TOUCH-03,
// NFR-TOUCH-07): a mouse's drag, one finger's, or a pen's pans the board in
// the pan mode, and draws a rubber band that selects the nodes that it
// overlaps in the select mode, where a drag with the mouse's middle button,
// or with Space held, still pans; two fingers pinch, zooming about the point
// between them, which stays under them, and pan as they move together; a
// third finger is ignored. A tap, or a click, selects the node under it, or
// adds it to the selection, or takes it out, with Shift, Ctrl, or Cmd, and
// a tap on the board's empty canvas clears the selection. The wheel pans, and
// Shift turns it sideways; with Ctrl or Cmd, as a trackpad's pinch sends it,
// it zooms about the pointer. Nothing needs hover or a right click.

import {
  anchorAt,
  type Point,
  panBy,
  type Rect,
  rectOf,
  type Size,
  toWorld,
  type Viewport,
} from "./viewport";

/** What a drag of one pointer does: pan the board, or draw a rubber band. */
export type Mode = "pan" | "select";

/** A pointer's event, as the canvas reads it, at a point of the canvas. */
export interface PointerInput {
  id: number;
  x: number;
  y: number;
  kind: "mouse" | "touch" | "pen";
  /** The button that went down, of a mouse: 0, the primary, or 1, the middle. */
  button: number;
  shift: boolean;
  /** Whether Ctrl or Cmd is held, which adds to the selection, as Shift does. */
  additive: boolean;
}

/** A wheel's event, as the canvas reads it, at a point of the canvas. */
export interface WheelInput {
  x: number;
  y: number;
  dx: number;
  dy: number;
  /** In what the deltas count: 0 pixels, 1 lines, 2 pages. */
  deltaMode: number;
  /** Whether Ctrl or Cmd is held, as a trackpad's pinch sends it. */
  zoom: boolean;
  shift: boolean;
}

/** How a selection changes: to the nodes given, with them added, or each added or taken out. */
export type SelectHow = "replace" | "add" | "toggle";

/** What the gestures read of the canvas, and change. */
export interface GestureHost {
  viewport(): Viewport;
  size(): Size;
  /** Shows v at once, ending a zoom or a move that the canvas animates. */
  setViewport(v: Viewport): void;
  mode(): Mode;
  /** Returns the ID of the topmost node at p of the board, within slop units. */
  nodeAt(p: Point, slop: number): string | undefined;
  /** Returns the IDs of the nodes that r of the board overlaps. */
  nodesIn(r: Rect): string[];
  select(ids: string[], how: SelectHow): void;
  /** Shows the rubber band at r of the canvas, or none. */
  band(r: Rect | undefined): void;
}

/**
 * How far, in CSS pixels, a pointer moves before a press is a drag rather
 * than a tap (NFR-TOUCH-05): a finger's 8, and a mouse's 4.
 */
export const dragThreshold = { touch: 8, pen: 8, mouse: 4 } as const;

/** How far from a node, in CSS pixels, a tap still finds it. */
const tapSlop = { touch: 12, pen: 6, mouse: 1 } as const;

/** How much a pixel of a zooming wheel's delta zooms: e to its negative, so that zooming in and out by the same delta come back. */
const wheelZoomRate = 0.01;

/** The most of a zooming wheel's delta that one event counts, so that a mouse's notch zooms by a step, not off the range. */
const wheelZoomStep = 25;

/** The pixels of a line of a wheel that counts in lines. */
const lineHeight = 16;

type Gesture =
  | { kind: "idle" }
  /** A pointer is down, and has not moved past the threshold: a tap, or a drag to come. */
  | {
      kind: "press";
      id: number;
      start: Point;
      drag: "pan" | "band";
      input: PointerInput;
    }
  | { kind: "pan"; id: number; last: Point }
  | { kind: "band"; id: number; start: Point; input: PointerInput }
  | {
      kind: "pinch";
      ids: [number, number];
      /** The point of the board that the point between the fingers holds. */
      anchor: Point;
      distance: number;
      zoom: number;
    };

/** The state of the pointers on the canvas, and what they do to it. */
export class Gestures {
  private readonly pointers = new Map<number, PointerInput>();
  private gesture: Gesture = { kind: "idle" };
  private space = false;

  constructor(private readonly host: GestureHost) {}

  /** Tells whether Space is held, with which a drag pans in the select mode. */
  setSpace(held: boolean): void {
    this.space = held;
  }

  /** Reports whether a gesture is under way. */
  get active(): boolean {
    return this.gesture.kind !== "idle";
  }

  /**
   * Takes a pointer that went down, and reports whether the canvas follows
   * it: not a mouse's other buttons, nor a third pointer (NFR-TOUCH-07).
   */
  down(p: PointerInput): boolean {
    if (p.kind === "mouse" && p.button !== 0 && p.button !== 1) {
      return false;
    }
    if (this.pointers.size >= 2 || this.pointers.has(p.id)) {
      return false;
    }
    this.pointers.set(p.id, p);
    if (this.pointers.size === 2) {
      this.startPinch();
      return true;
    }
    const pans =
      this.host.mode() === "pan" ||
      this.space ||
      (p.kind === "mouse" && p.button === 1);
    this.gesture = {
      kind: "press",
      id: p.id,
      start: { x: p.x, y: p.y },
      drag: pans ? "pan" : "band",
      input: p,
    };
    return true;
  }

  /** Takes a pointer that moved. */
  move(p: PointerInput): void {
    const had = this.pointers.get(p.id);
    if (had === undefined) {
      return;
    }
    this.pointers.set(p.id, { ...had, x: p.x, y: p.y });
    const g = this.gesture;
    switch (g.kind) {
      case "press": {
        if (g.id !== p.id) {
          return;
        }
        const moved = Math.hypot(p.x - g.start.x, p.y - g.start.y);
        if (moved < dragThreshold[g.input.kind]) {
          return;
        }
        if (g.drag === "pan") {
          this.gesture = { kind: "pan", id: p.id, last: g.start };
          this.move(p);
        } else {
          this.gesture = {
            kind: "band",
            id: p.id,
            start: g.start,
            input: g.input,
          };
          this.host.band(rectOf(g.start, p));
        }
        return;
      }
      case "pan": {
        if (g.id !== p.id) {
          return;
        }
        this.host.setViewport(
          panBy(this.host.viewport(), p.x - g.last.x, p.y - g.last.y),
        );
        this.gesture = { ...g, last: { x: p.x, y: p.y } };
        return;
      }
      case "band":
        if (g.id === p.id) {
          this.host.band(rectOf(g.start, p));
        }
        return;
      case "pinch": {
        const [a, b] = g.ids.map((id) => this.pointers.get(id));
        if (a === undefined || b === undefined) {
          return;
        }
        const distance = Math.max(1, Math.hypot(a.x - b.x, a.y - b.y));
        this.host.setViewport(
          anchorAt(g.anchor, midpoint(a, b), g.zoom * (distance / g.distance)),
        );
        return;
      }
    }
  }

  /** Takes a pointer that went up. */
  up(p: PointerInput): void {
    this.end(p, false);
  }

  /** Takes a pointer that the browser took, as for a gesture of its own, or that the canvas lost. */
  cancel(p: Pick<PointerInput, "id">): void {
    this.end(p, true);
  }

  /** Ends every gesture, as once the canvas loses the focus of the window. */
  cancelAll(): void {
    for (const id of [...this.pointers.keys()]) {
      this.end({ id }, true);
    }
  }

  private end(p: Pick<PointerInput, "id">, cancelled: boolean): void {
    if (!this.pointers.has(p.id)) {
      return;
    }
    const input = this.pointers.get(p.id) as PointerInput;
    this.pointers.delete(p.id);
    const g = this.gesture;
    switch (g.kind) {
      case "press":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          if (!cancelled) {
            this.tap(input, g.input);
          }
        }
        return;
      case "pan":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
        }
        return;
      case "band":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.host.band(undefined);
          if (!cancelled) {
            const v = this.host.viewport();
            const r = rectOf(toWorld(v, g.start), toWorld(v, input));
            const additive = g.input.shift || g.input.additive;
            this.host.select(
              this.host.nodesIn(r),
              additive ? "add" : "replace",
            );
          }
        }
        return;
      case "pinch": {
        // The finger that stays pans on, from where it is.
        const rest = g.ids.find((id) => id !== p.id);
        const other = rest === undefined ? undefined : this.pointers.get(rest);
        this.gesture =
          other === undefined
            ? { kind: "idle" }
            : { kind: "pan", id: other.id, last: { x: other.x, y: other.y } };
        return;
      }
      case "idle":
        return;
    }
  }

  private startPinch(): void {
    if (this.gesture.kind === "band") {
      this.host.band(undefined);
    }
    const [a, b] = [...this.pointers.values()];
    const v = this.host.viewport();
    this.gesture = {
      kind: "pinch",
      ids: [a.id, b.id],
      anchor: toWorld(v, midpoint(a, b)),
      distance: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)),
      zoom: v.zoom,
    };
  }

  private tap(at: PointerInput, pressed: PointerInput): void {
    const v = this.host.viewport();
    const id = this.host.nodeAt(toWorld(v, at), tapSlop[at.kind] / v.zoom);
    const additive = pressed.shift || pressed.additive;
    if (id !== undefined) {
      this.host.select([id], additive ? "toggle" : "replace");
    } else if (!additive) {
      this.host.select([], "replace");
    }
  }

  /** Takes a turn of the wheel, or a trackpad's scroll or pinch, which the canvas always handles. */
  wheel(w: WheelInput): void {
    const size = this.host.size();
    const unit =
      w.deltaMode === 1 ? lineHeight : w.deltaMode === 2 ? size.height : 1;
    let dx = w.dx * unit;
    let dy = w.dy * unit;
    const v = this.host.viewport();
    if (w.zoom) {
      const step = Math.max(-wheelZoomStep, Math.min(wheelZoomStep, dy));
      const zoom = v.zoom * Math.exp(-step * wheelZoomRate);
      const anchor = toWorld(v, w);
      this.host.setViewport(anchorAt(anchor, w, zoom));
      return;
    }
    if (w.shift && dx === 0) {
      dx = dy;
      dy = 0;
    }
    this.host.setViewport(panBy(v, -dx, -dy));
  }

  /**
   * Takes a step of Safari's pinch of a trackpad, which it sends as a
   * gesture's scale, from 1 at its start, rather than as a wheel.
   */
  scale(at: Point, from: number, to: number): void {
    if (from <= 0 || to <= 0) {
      return;
    }
    const v = this.host.viewport();
    this.host.setViewport(anchorAt(toWorld(v, at), at, v.zoom * (to / from)));
  }
}

function midpoint(a: Point, b: Point): Point {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}
