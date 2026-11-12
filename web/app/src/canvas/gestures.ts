// How the canvas reads pointers and the wheel (PRD FR-EDIT-01, NFR-TOUCH-03,
// NFR-TOUCH-07): a mouse's drag, one finger's, or a pen's pans the board in
// the pan mode, and draws a rubber band that selects the nodes that it
// overlaps in the select mode, from the board's point where it started,
// however the board moves under it meanwhile, where a drag with the mouse's
// middle button, or with Space held, still pans; two fingers pinch, zooming
// about the point between them, which stays under them, and pan as they move
// together; a third finger is ignored, as is a finger while a pen is down,
// as a palm that rests on the screen as the pen draws, and Safari's pinch
// of such fingers, and a press of a mouse or a pen takes the canvas from
// any other pointer, as a finger's does from a mouse. A tap, or a click,
// selects the node under it, or adds it to the selection, or takes it out,
// with Shift, Ctrl, or Cmd, and a tap on the board's empty canvas clears
// the selection; a click of the middle button, which pans, selects nothing.
// A drag that starts on a selected node moves the selection instead, in
// either mode, on a board that may be changed, and one that starts on a node
// that is not selected selects it and moves it in the select mode, but pans
// in the pan mode, so that a finger that pans a crowded board moves no node
// (plan note N68); a second tap or click on the same node, right after the
// first and soon after it, opens it, as for its text. A press on the resize
// handle of the node selected, which the page places over the canvas,
// outside its element, is the canvas's as one on it is, by the same rules
// of pointers: its drag resizes the node, in either mode, from the board's
// point where it started, as a move does, but for one of the middle button
// or with Space held, which pans, and it taps nothing. A press on a link
// handle, of a port of the node selected, or of an end of the connection
// selected, which the page places over the canvas too, is the canvas's
// alike: its drag draws a connection from the port, or takes the
// connection's end, to the node under its pointer, and lands it there, or
// on the empty board, as it ends (plan note N69). A tap on a connection's
// line, on a board that may be changed, where no node is, selects the
// connection. A rubber band, a move, a resize, or a link under way ends,
// cancelled, as Escape asks, or as the board may no longer be changed
// (cancelDrag). The wheel pans, and Shift
// turns it sideways; with Ctrl or Cmd, as a trackpad's pinch sends it, it
// zooms about the pointer. A finger's press, or a pen's, held for 400 ms
// where it went down, opens the context menu of what is under it, the
// node, the connection, or the board there (FR-EDIT-09, NFR-TOUCH-02, plan
// note N70), and does nothing more until it goes up; the page opens the
// same menu for a mouse's right click (menu), and for the keys. Nothing
// needs hover or a right click, and a press of a mouse's right button, or
// of a pen's barrel button or its eraser, is not followed; a
// press ends, as cancelled, once its mouse or its pen moves, or leaves the
// canvas, without the button that pressed it, as a pen does that leaves
// the range where the screen sees it hover. A gesture under way when the
// canvas moves the board by itself, as with its fit to the board's content
// once it has been read, goes on from where the board is then. The canvas
// gives back the capture of a pointer as soon as it no longer follows it,
// however its gesture ended (GestureHost's release).

import { dragThreshold } from "../ui/drag";
import {
  anchorAt,
  type Point,
  panBy,
  type Rect,
  rectOf,
  type Size,
  toScreen,
  toWorld,
  type Viewport,
  zoomAt,
} from "./viewport";

/** What a drag of one pointer does: pan the board, or draw a rubber band. */
export type Mode = "pan" | "select";

/** A pointer's event, as the canvas reads it, at a point of the canvas. */
export interface PointerInput {
  id: number;
  x: number;
  y: number;
  kind: "mouse" | "touch" | "pen";
  /**
   * The button that went down: 0, the primary, as a finger's touch or a
   * pen's tip, 1, a mouse's middle, 2, a mouse's right or a pen's barrel
   * button, or 5, a pen's eraser.
   */
  button: number;
  /**
   * The buttons held as the event came, as the browser's `buttons` counts
   * them: 1, the primary, as a finger's touch or a pen's tip, 2, a mouse's
   * right or a pen's barrel button, 4, a mouse's middle, and 32, a pen's
   * eraser, added together.
   */
  buttons: number;
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

/**
 * What a link handle's drag holds (plan note N69): a connection to come,
 * from port of the node of node, or an end of the connection of
 * connection, the one that leaves its node (from) or the one that enters
 * its node (to), which the drag takes to another. Its handle tells its
 * side, in CSS pixels, the square that it covers about its port, or about
 * where its end is, where its drag has not left it.
 */
export type Link =
  | { kind: "new"; node: string; port: string; side?: number }
  | { kind: "from" | "to"; connection: string; side?: number };

/** Reports whether a and b hold the same end. */
export function sameLink(a: Link, b: Link): boolean {
  return a.kind === "new"
    ? b.kind === "new" && a.node === b.node && a.port === b.port
    : b.kind === a.kind && a.connection === b.connection;
}

/**
 * What a context menu is of (PRD FR-EDIT-09, plan note N70): the node, or
 * the connection, at its point, found as a tap finds one, or the board
 * itself there, each with the point of the board that it was asked for at.
 */
export type MenuTarget =
  | { kind: "node"; node: string; at: Point }
  | { kind: "connection"; connection: string; at: Point }
  | { kind: "board"; at: Point };

/**
 * A handle that the page places over the canvas, whose press is the
 * canvas's: the resize handle of the node of an ID, or a link handle, of
 * what its drag holds.
 */
export type Handle = string | Link;

/** What the gestures read of the canvas, and change. */
export interface GestureHost {
  viewport(): Viewport;
  size(): Size;
  /** Shows v at once, ending a zoom or a move that the canvas animates, unless v moves nothing. */
  setViewport(v: Viewport): void;
  mode(): Mode;
  /** Returns the ID of the topmost node at p of the board, within slop units. */
  nodeAt(p: Point, slop: number): string | undefined;
  /** Returns the IDs of the nodes that r of the board overlaps. */
  nodesIn(r: Rect): string[];
  select(ids: string[], how: SelectHow): void;
  /** Shows the rubber band at r of the canvas, or none. */
  band(r: Rect | undefined): void;
  /**
   * Gives back the pointer of id, which went down on the canvas, and which
   * the gestures no longer follow, however its gesture ended: its capture,
   * which the canvas took as it went down.
   */
  release(id: number): void;
  /** Reports whether the node of id is selected. */
  isSelected(id: string): boolean;
  /** Reports whether a drag may move nodes, as on a board that the user may change. */
  movable(): boolean;
  /**
   * Starts moving the selection, which holds the node of id, by its drag,
   * or, where pick says how, the selection that the node is first selected
   * into, and reports whether it did, as it does not once the node has
   * gone, which selects nothing.
   */
  moveStart(id: string, pick?: SelectHow): boolean;
  /** Moves the selection by delta of the board from where its move started. */
  moveTo(delta: Point): void;
  /** Ends the move of the selection, where it is, or, cancelled, where it started. */
  moveEnd(commit: boolean): void;
  /** Starts resizing the node of id by its handle's drag, and reports whether it did, as it does not once the node has gone. */
  resizeStart(id: string): boolean;
  /** Resizes the node by delta of the board, from where its resize started. */
  resizeTo(delta: Point): void;
  /** Ends the resize, at the size where it is, or, cancelled, at the size before. */
  resizeEnd(commit: boolean): void;
  /** Opens the node of id, as its second tap soon after the first does, as for its text. */
  open(id: string): void;
  /** Returns the ID of the connection whose line is within slop units of p of the board, where a tap selects one, as on a board that may be changed. */
  connectionAt(p: Point, slop: number): string | undefined;
  /** Selects the connection of id, in place of the selection. */
  selectConnection(id: string): void;
  /** Starts the drag of link, by its handle, and reports whether it did, as it does not once its node, or its connection, has gone. */
  linkStart(link: Link): boolean;
  /** Takes the end that the link's drag holds to at of the board, where a node within slop units takes it, but outside the canvas, and on the handle that the link left, where nothing does. */
  linkTo(at: Point, slop: number): void;
  /** Ends the link's drag, where it is, or, cancelled, as nothing. */
  linkEnd(commit: boolean): void;
  /** The time, in milliseconds, by which a double tap is told from two taps. */
  now(): number;
  /** Calls then once ms milliseconds have passed, unless the function that it returns is called first, as the time of a long press is counted. */
  after(ms: number, then: () => void): () => void;
  /** Opens the context menu of target, as a long press on it asks, where held says so, or a right click. */
  menu(target: MenuTarget, held: boolean): void;
}

/**
 * How far, in CSS pixels, a pointer moves before a press is a drag rather
 * than a tap (NFR-TOUCH-05): a finger's 8, as the app's every drag, and a
 * pen's, and a mouse's 4.
 */
const dragThresholds = {
  touch: dragThreshold,
  pen: dragThreshold,
  mouse: 4,
} as const;

/** How far from a node, in CSS pixels, a tap still finds it. */
const tapSlop = { touch: 12, pen: 6, mouse: 1 } as const;

/** How soon after a tap on a node another on it opens it, in milliseconds. */
export const doubleTapTime = 500;

/** How long a finger's press, or a pen's, is held where it went down before it opens the context menu, in milliseconds (FR-EDIT-09, NFR-TOUCH-02). */
export const longPressTime = 400;

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
      /** The point of the board that the pointer went down on, where a rubber band starts. */
      at: Point;
      /**
       * What its drag does: pans, draws a rubber band, moves the selection,
       * which holds the node that it went down on, selects that node and
       * then moves it (pick), resizes the node whose handle it went down
       * on, or links what the link handle that it went down on holds.
       */
      drag: "pan" | "band" | "move" | "pick" | "resize" | "link";
      /** The node that it went down on, which a move holds, or whose resize handle it went down on. */
      node?: string;
      /** What the link handle that it went down on holds. */
      link?: Link;
      /** Whether it went down on a handle, whose press taps nothing, whatever its drag does. */
      handle: boolean;
      /** The tap right before it, which a tap of it on the same node soon after opens the node of (tap). */
      lastTap: { node: string; time: number } | undefined;
      /** Which press it is, of those so far, by which the end of the time of its long press finds it (longPress). */
      serial: number;
      input: PointerInput;
    }
  | { kind: "pan"; id: number; last: Point }
  /** A press that does nothing until its pointer goes up, as a drag that would have moved a node that went meanwhile, or whose handle went, or a long press, which opened the context menu. */
  | { kind: "held"; id: number }
  /** A move of the selection, from the point of the board where it started, which stays there as the wheel or a key moves the board. */
  | { kind: "move"; id: number; anchor: Point }
  /** A resize of the node of node by its handle, from the point of the board where it started, as a move's. */
  | { kind: "resize"; id: number; node: string; anchor: Point }
  /** A drag of a link handle, whose end follows its pointer, wherever the board moves under it. */
  | { kind: "link"; id: number; link: Link }
  /** A rubber band, from the point of the board where it started, which stays there as the wheel or a key moves the board. */
  | { kind: "band"; id: number; anchor: Point; input: PointerInput }
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
  /** The node of the last tap, and when it was, which a second tap on it soon after opens, until the next press takes it (down). */
  private lastTap: { node: string; time: number } | undefined;
  /** How many presses there were, which numbers each (Gesture's serial). */
  private presses = 0;
  /** Ends the count of the time of the long press of the press under way, if it has one. */
  private unhold: (() => void) | undefined;

  constructor(private readonly host: GestureHost) {}

  /** Tells whether Space is held, with which a drag pans in the select mode. */
  setSpace(held: boolean): void {
    this.space = held;
  }

  /** Reports whether the canvas follows the pointer of id, which went down on it. */
  tracks(id: number): boolean {
    return this.pointers.has(id);
  }

  /**
   * Takes a pointer that went down, and reports whether the canvas follows
   * it: the primary button of each, a finger's touch or a pen's tip, and a
   * mouse's middle button, but no other button, as a mouse's right one, or a
   * pen's barrel button, which the system takes for a right click, or its
   * eraser, nor a third finger (NFR-TOUCH-07). A pointer that goes down
   * while the canvas still follows it went up where the canvas did not
   * hear of it, as a mouse's button that a menu of the system's, opened
   * over the page, took the release of: its gesture ends, as cancelled,
   * rather than refuse every press of it after. Only fingers share the
   * canvas, two of them, to pinch: a press of a mouse or a pen while
   * another pointer is down, or of a finger while a mouse is, takes the
   * canvas from it, whose gesture ends, as cancelled, rather than pinch
   * with it about a point that neither is on, as a mouse that is still,
   * whose release the canvas did not hear of, would with a finger that
   * touches the screen of a laptop. A finger that touches the screen while
   * a pen is down is ignored, as a palm that rests on it as the pen draws,
   * where the system rejects none, which would else end the pen's rubber
   * band, or its pan; a pen whose release the canvas did not hear of ends
   * as it hovers after it (move), as it leaves the range where the screen
   * sees it hover (leave), which it passes through as it is lifted, or as
   * it goes down again. A press of handle, the resize handle of a node, or
   * a link handle, is taken by the same rules, and its pointer followed as
   * any other.
   */
  down(p: PointerInput, handle?: Handle): boolean {
    if (this.pointers.has(p.id)) {
      this.end(p, true);
    }
    if (p.button !== 0 && !(p.kind === "mouse" && p.button === 1)) {
      return false;
    }
    if (
      p.kind === "touch" &&
      [...this.pointers.values()].some((other) => other.kind === "pen")
    ) {
      return false;
    }
    for (const other of [...this.pointers.values()]) {
      if (other.kind !== "touch" || p.kind !== "touch") {
        this.end(other, true);
      }
    }
    if (this.pointers.size >= 2) {
      return false;
    }
    this.pointers.set(p.id, p);
    // A double tap is two taps in a row: the press after a tap takes it,
    // so that whatever the press is but a tap, a drag, a pinch, a press of
    // the handle or of the middle button, or one cancelled, leaves none for
    // the tap after it.
    const lastTap = this.lastTap;
    this.lastTap = undefined;
    if (this.pointers.size === 2) {
      this.startPinch();
      return true;
    }
    const v = this.host.viewport();
    const at = toWorld(v, p);
    const pans =
      this.host.mode() === "pan" ||
      this.space ||
      (p.kind === "mouse" && p.button === 1);
    // A drag on a node moves it, on a board that may be changed, but for one
    // that pans whatever is under it, of the middle button or with Space
    // held: the selection, of a node that is selected, in either mode, and,
    // in the select mode, a node that is not, which it selects first. It is
    // on the node itself, without the slop that finds a tap's node beside
    // it, so that a rubber band started just beside a node, as at a low
    // zoom, where the slop is as wide as a node, draws rather than moves.
    // A drag of a node's resize handle resizes it, and one of a link handle
    // links what it holds, in either mode, but for one that pans whatever
    // is under it.
    const handles = handle !== undefined && p.button === 0 && !this.space;
    const link = handles && typeof handle !== "string" ? handle : undefined;
    const resizes = handles && typeof handle === "string";
    const node = resizes
      ? handle
      : link === undefined &&
          p.button === 0 &&
          !this.space &&
          this.host.movable()
        ? this.host.nodeAt(at, 0)
        : undefined;
    const drag =
      link !== undefined
        ? "link"
        : resizes
          ? "resize"
          : node !== undefined && this.host.isSelected(node)
            ? "move"
            : node !== undefined && this.host.mode() === "select"
              ? "pick"
              : pans
                ? "pan"
                : "band";
    const serial = ++this.presses;
    this.gesture = {
      kind: "press",
      id: p.id,
      start: { x: p.x, y: p.y },
      at,
      drag,
      node: drag === "pan" || drag === "band" ? undefined : node,
      link,
      handle: handle !== undefined,
      lastTap,
      serial,
      input: p,
    };
    // A finger's press, or a pen's tip's, on the canvas itself, held where
    // it went down, opens the context menu of what is under it (longPress):
    // not a mouse's, whose right button asks for it, nor a press of a
    // handle, which is a drag's alone.
    this.unhold?.();
    this.unhold =
      handle === undefined && p.kind !== "mouse"
        ? this.host.after(longPressTime, () => this.longPress(serial))
        : undefined;
    return true;
  }

  /**
   * Takes the end of the time of the long press of the press of serial:
   * where that press is still one, neither a drag, which it is from its
   * threshold on, nor a pinch, nor ended, it opens the context menu of what
   * is under its pointer, where the pointer is now, and does nothing more
   * until it goes up, so that it taps nothing, and drags nothing, after
   * (FR-EDIT-09).
   */
  private longPress(serial: number): void {
    this.unhold = undefined;
    const g = this.gesture;
    if (g.kind !== "press" || g.serial !== serial) {
      return;
    }
    const p = this.pointers.get(g.id);
    if (p === undefined) {
      return;
    }
    this.gesture = { kind: "held", id: g.id };
    this.menu(p, true);
  }

  /**
   * Opens the context menu of what is at p of the canvas, found as a tap of
   * a pointer of p's kind finds it: the node there, or else the connection
   * whose line is there, on a board that may be changed, or else the board
   * itself. A long press opens it so, which held tells of, and the page
   * for a mouse's right click, which the gestures do not follow.
   */
  menu(p: Pick<PointerInput, "x" | "y" | "kind">, held = false): void {
    const v = this.host.viewport();
    const at = toWorld(v, p);
    const slop = tapSlop[p.kind] / v.zoom;
    const node = this.host.nodeAt(at, slop);
    if (node !== undefined) {
      this.host.menu({ kind: "node", node, at }, held);
      return;
    }
    const connection = this.host.connectionAt(
      at,
      Math.max(slop, tapSlop.pen / v.zoom),
    );
    this.host.menu(
      connection === undefined
        ? { kind: "board", at }
        : { kind: "connection", connection, at },
      held,
    );
  }

  /** Reports whether the canvas follows a pointer, as one that is down on it. */
  following(): boolean {
    return this.pointers.size > 0;
  }

  /**
   * Takes a pointer that moved. A mouse or a pen that the canvas follows,
   * but that moves without the button that pressed it, went up where the
   * canvas did not hear of it, as once a menu of the system's took the
   * release, rather than the page, or as a pen that hovers after it, or
   * went on with another button alone, as a chord's right one, which the
   * canvas does not follow: its press ends there, as cancelled, rather than
   * pan the board, or draw a rubber band, under no button, or under one
   * that does nothing to the board. A finger is down as long as it moves.
   */
  move(p: PointerInput): void {
    const had = this.pointers.get(p.id);
    if (had === undefined) {
      return;
    }
    const pressed = had.button === 1 ? 4 : 1;
    if (had.kind !== "touch" && (p.buttons & pressed) === 0) {
      this.end(p, true);
      return;
    }
    this.follow(p);
  }

  /**
   * Takes a pointer that left the canvas. The canvas holds the capture of
   * each pointer that it follows, which the browser says has left it only
   * once the browser has lost the pointer, as a pen that leaves the range
   * where the screen sees it hover: a move to where it left, so that a pen
   * whose release the canvas did not hear of, and that leaves the range
   * without a move after it, as one lifted straight out of it, ends there,
   * as one that hovers does (move), rather than keep every finger from the
   * canvas after it, as a palm's (down).
   */
  leave(p: PointerInput): void {
    this.move(p);
  }

  /** Moves the pointer of p, which the canvas follows, to where p is. */
  private follow(p: PointerInput): void {
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
        if (moved < dragThresholds[g.input.kind]) {
          return;
        }
        if (g.drag === "resize") {
          // A drag of the handle of a node that went resizes nothing, as one
          // that cannot move its node moves nothing.
          const node = g.node as string;
          if (!this.host.resizeStart(node)) {
            this.gesture = { kind: "held", id: p.id };
            return;
          }
          this.gesture = { kind: "resize", id: p.id, node, anchor: g.at };
          this.refresh();
        } else if (g.drag === "link") {
          // A drag of the handle of what went links nothing, as one that
          // cannot move its node moves nothing.
          const link = g.link as Link;
          if (!this.host.linkStart(link)) {
            this.gesture = { kind: "held", id: p.id };
            return;
          }
          this.gesture = { kind: "link", id: p.id, link };
          this.refresh();
        } else if (g.drag === "move" || g.drag === "pick") {
          const node = g.node as string;
          // A drag that cannot move its node, as one whose node went, as
          // once another session deleted it, selects nothing, moves
          // nothing, pans nothing, and taps nothing as it ends: the host,
          // which refuses the move of a node that went, selects the node
          // that it picks only once it starts the move.
          const pick =
            g.drag === "pick"
              ? g.input.shift || g.input.additive
                ? "add"
                : "replace"
              : undefined;
          if (!this.host.moveStart(node, pick)) {
            this.gesture = { kind: "held", id: p.id };
            return;
          }
          this.gesture = { kind: "move", id: p.id, anchor: g.at };
          this.refresh();
        } else if (g.drag === "pan") {
          this.gesture = { kind: "pan", id: p.id, last: g.start };
          this.follow(p);
        } else {
          this.gesture = {
            kind: "band",
            id: p.id,
            anchor: g.at,
            input: g.input,
          };
          this.refresh();
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
      case "move":
      case "resize":
      case "link":
        if (g.id === p.id) {
          this.refresh();
        }
        return;
      case "held":
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

  /**
   * Takes a pointer that went up, where it went up, which a move may not
   * have told of, as a pen's or a finger's lift after moves that the
   * browser coalesced: the tap finds the node there, and a drag, a rubber
   * band, or a pinch ends there.
   */
  up(p: PointerInput): void {
    this.follow(p);
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
    this.host.release(p.id);
    const g = this.gesture;
    switch (g.kind) {
      case "press":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.unhold?.();
          this.unhold = undefined;
          // A click of the middle button is a pan that did not move, and a
          // tap of a resize handle, as one with Space held, which pans, a
          // drag of it that did not.
          const middle = g.input.kind === "mouse" && g.input.button === 1;
          if (!cancelled && !middle && !g.handle) {
            this.tap(input, g.input, g.lastTap);
          }
        }
        return;
      case "pan":
      case "held":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
        }
        return;
      case "move":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.host.moveEnd(!cancelled);
        }
        return;
      case "resize":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.host.resizeEnd(!cancelled);
        }
        return;
      case "link":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.host.linkEnd(!cancelled);
        }
        return;
      case "band":
        if (g.id === p.id) {
          this.gesture = { kind: "idle" };
          this.host.band(undefined);
          if (!cancelled) {
            const r = rectOf(g.anchor, toWorld(this.host.viewport(), input));
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

  /**
   * Draws the rubber band again, from the point of the board where it
   * started to its pointer, or moves the selection, resizes the node, or
   * takes the link's end, under its pointer again, as the pointer moves,
   * or the board under it, as once the wheel, a key, a pinch of the
   * trackpad, or the canvas's new size moved it.
   */
  refresh(): void {
    const g = this.gesture;
    if (
      g.kind !== "band" &&
      g.kind !== "move" &&
      g.kind !== "resize" &&
      g.kind !== "link"
    ) {
      return;
    }
    const p = this.pointers.get(g.id);
    if (p === undefined) {
      return;
    }
    const v = this.host.viewport();
    if (g.kind === "band") {
      this.host.band(rectOf(toScreen(v, g.anchor), p));
      return;
    }
    const at = toWorld(v, p);
    if (g.kind === "link") {
      // The node that takes the link's end is found as a tap's is, a
      // finger's within its slop.
      this.host.linkTo(at, tapSlop[p.kind] / v.zoom);
      return;
    }
    const delta = { x: at.x - g.anchor.x, y: at.y - g.anchor.y };
    if (g.kind === "move") {
      this.host.moveTo(delta);
    } else {
      this.host.resizeTo(delta);
    }
  }

  /**
   * Tells that the resize handle of the node of id went, as once the
   * selection changed, or the editor of the node's text opened over it: its
   * press, or its resize, ends, cancelled, and its pointer does nothing
   * more until it goes up, as a drag that cannot move its node.
   */
  handleGone(id: string): void {
    const g = this.gesture;
    if (g.kind === "resize" && g.node === id) {
      this.cancelDrag();
    } else if (g.kind === "press" && g.drag === "resize" && g.node === id) {
      this.gesture = { kind: "held", id: g.id };
    }
  }

  /**
   * Tells that the link handle of link went, as once the selection changed,
   * or what it holds went: its press, or its drag, ends, cancelled, as the
   * resize handle's does (handleGone).
   */
  linkGone(link: Link): void {
    const g = this.gesture;
    if (g.kind === "link" && sameLink(g.link, link)) {
      this.cancelDrag();
    } else if (
      g.kind === "press" &&
      g.link !== undefined &&
      sameLink(g.link, link)
    ) {
      this.gesture = { kind: "held", id: g.id };
    }
  }

  /**
   * Ends the rubber band, the move, the resize, or the link under way, as
   * cancelled, as Escape asks, or as the board may no longer be changed:
   * the band selects nothing, the nodes go back where they were, the link
   * connects nothing, and the pointer does nothing more until it goes up,
   * as a drag that cannot move its node. Reports whether there was one.
   */
  cancelDrag(): boolean {
    const g = this.gesture;
    if (
      g.kind !== "band" &&
      g.kind !== "move" &&
      g.kind !== "resize" &&
      g.kind !== "link"
    ) {
      return false;
    }
    this.gesture = { kind: "held", id: g.id };
    if (g.kind === "band") {
      this.host.band(undefined);
    } else if (g.kind === "move") {
      this.host.moveEnd(false);
    } else if (g.kind === "resize") {
      this.host.resizeEnd(false);
    } else {
      this.host.linkEnd(false);
    }
    return true;
  }

  /**
   * Tells that the canvas moved the board by itself, from the viewport
   * from, as with its fit to the board's content once it has been read,
   * which a gesture under way did not ask for: the gesture goes on from
   * where the board is now, rather than take it back to the point that it
   * held under the viewport before, as a pinch that began while the board
   * was read would undo the fit with its next move. A pinch starts again
   * from where its fingers are, and a press, a rubber band, a move, and a
   * resize, from where their start is on the canvas. A pan moves the board
   * by what its pointer moves, from wherever it is, and a link's end is
   * where its pointer is on the board now.
   */
  replaced(from: Viewport): void {
    const g = this.gesture;
    const v = this.host.viewport();
    const kept = (p: Point) => toWorld(v, toScreen(from, p));
    switch (g.kind) {
      case "pinch":
        this.startPinch();
        return;
      case "press":
        this.gesture = { ...g, at: kept(g.at) };
        return;
      case "band":
      case "move":
      case "resize":
        this.gesture = { ...g, anchor: kept(g.anchor) };
        this.refresh();
        return;
      case "link":
        this.refresh();
        return;
      case "pan":
      case "held":
      case "idle":
        return;
    }
  }

  private startPinch(): void {
    // A second finger takes the board from what the first did: a rubber
    // band, a move or a resize, which goes back where it started, or a
    // link, which connects nothing.
    if (this.gesture.kind === "band") {
      this.host.band(undefined);
    } else if (this.gesture.kind === "move") {
      this.host.moveEnd(false);
    } else if (this.gesture.kind === "resize") {
      this.host.resizeEnd(false);
    } else if (this.gesture.kind === "link") {
      this.host.linkEnd(false);
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

  /**
   * Selects the node at the tap, or adds it, or takes it out, with Shift,
   * Ctrl, or Cmd, or, where no node is, the connection whose line is there,
   * without them, or clears the selection on the empty board; a second tap
   * on the same node soon after the first, last, the tap right before it,
   * without them, opens it, as for its text, which nothing needs: the
   * inspector and Enter open it too (NFR-TOUCH-02).
   */
  private tap(
    at: PointerInput,
    pressed: PointerInput,
    last: { node: string; time: number } | undefined,
  ): void {
    const v = this.host.viewport();
    const where = toWorld(v, at);
    const slop = tapSlop[at.kind] / v.zoom;
    const id = this.host.nodeAt(where, slop);
    const additive = pressed.shift || pressed.additive;
    const now = this.host.now();
    this.lastTap =
      id === undefined || additive ? undefined : { node: id, time: now };
    if (id !== undefined) {
      this.host.select([id], additive ? "toggle" : "replace");
      if (
        !additive &&
        last !== undefined &&
        last.node === id &&
        now - last.time <= doubleTapTime
      ) {
        this.lastTap = undefined;
        this.host.open(id);
      }
    } else if (!additive) {
      // A line is thinner than what a mouse's slop finds a node by, so a
      // mouse finds one within a pen's.
      const connection = this.host.connectionAt(
        where,
        Math.max(slop, tapSlop.pen / v.zoom),
      );
      if (connection === undefined) {
        this.host.select([], "replace");
      } else {
        this.host.selectConnection(connection);
      }
    }
  }

  /** Takes a turn of the wheel, or a trackpad's scroll or pinch, which the canvas always handles. */
  wheel(w: WheelInput): void {
    const size = this.host.size();
    // A page is the canvas's width across and its height down.
    const unit = (page: number) =>
      w.deltaMode === 1 ? lineHeight : w.deltaMode === 2 ? page : 1;
    let dx = w.dx * unit(size.width);
    let dy = w.dy * unit(size.height);
    const v = this.host.viewport();
    if (w.zoom) {
      const step = Math.max(-wheelZoomStep, Math.min(wheelZoomStep, dy));
      const zoom = v.zoom * Math.exp(-step * wheelZoomRate);
      this.host.setViewport(zoomAt(v, { x: w.x, y: w.y }, zoom));
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
   * gesture's scale, from 1 at its start, rather than as a wheel. Safari on
   * an iPad sends a touch screen's pinch as a gesture too, as well as the
   * fingers' pointers, which pinch already: a gesture while a finger is on
   * the canvas is theirs, and is left to them, and one while a pen is down
   * is of fingers that the canvas ignores, as a palm's (down), which zoom
   * the board under the pen's rubber band, or its pan, no more than they
   * end it.
   */
  scale(at: Point, from: number, to: number): void {
    if (from <= 0 || to <= 0) {
      return;
    }
    for (const p of this.pointers.values()) {
      if (p.kind === "touch" || p.kind === "pen") {
        return;
      }
    }
    const v = this.host.viewport();
    this.host.setViewport(zoomAt(v, at, v.zoom * (to / from)));
  }
}

function midpoint(a: Point, b: Point): Point {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}
