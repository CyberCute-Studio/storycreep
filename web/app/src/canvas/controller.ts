// What drives a board's canvas (PRD FR-EDIT-01, FR-EDIT-11): the viewport,
// which the gestures, the keys, and the toolbar change, the selection, the
// scene of the board, and the loop that draws a frame once something
// changed, and only then, so that a canvas that nothing moves draws
// nothing. A zoom or a move that the toolbar or a key asks for is animated,
// briefly, unless the system asks for reduced motion (NFR-A11Y-07), as the
// canvas's only animation; a gesture follows the fingers at once. A drag
// that moves the selection, or resizes a node by its handle, shows it at
// once, as the scene of the document with the nodes where the drag has them,
// without a command, and tells the page where it left them as it ends, which
// sends one batch for it (FR-EDIT-02 AC2, plan note N68), whose nodes the
// document shows there at once, in the same frame as the drag's end.

import type { Node } from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import { movedBy, resizedBy } from "./edit";
import type { Engine, FrameStats, RendererKind } from "./engine";
import {
  type GestureHost,
  Gestures,
  type Mode,
  type PointerInput,
  type SelectHow,
} from "./gestures";
import { type KeyInput, keyAction } from "./keyboard";
import type { NodeLabels } from "./labels";
import type { Minimap } from "./minimap";
import { type NodeItem, Scene } from "./scene";
import { sameTheme, type Theme } from "./theme";
import {
  anchorAt,
  between,
  centerOf,
  clampZoom,
  emptyView,
  fitTo,
  type Point,
  panBy,
  type Rect,
  type Size,
  sameViewport,
  stepZoom,
  toScreen,
  type Viewport,
  zoomAt,
} from "./viewport";

/** A scripted run of the canvas that the benchmark harness measures (bench.tsx). */
export interface BenchOptions {
  scenario: "pan" | "panzoom" | "load" | "edit";
  /** How long the run records, after its warm-up. */
  seconds: number;
  /** The rectangle of the board that the run's path crosses, or the board's content. */
  area?: Rect;
  /**
   * How long the run warms up before it records, in seconds, or for a pass
   * of its path across the area and the 2 s after, as a run that measures
   * memory does; 2 s without it.
   */
  warm?: number | "pass";
}

/** The name of the performance mark of the first frame that draws a board's content (NFR-PERF-04, NFR-PERF-05). */
export const renderedMark = "storycreep:board-rendered";

/** How long an animated zoom or move takes, in milliseconds. */
export const animationTime = 220;

/** The most that fit to content and zoom to selection zoom in to. */
const fitZoom = 1;

/** A frame that the canvas drew, which the benchmark records. */
export interface FrameRecord {
  /** When the frame began, as requestAnimationFrame gives it, or, of one that the page drew of its own work (scheduled), as the clock read then. */
  time: number;
  /**
   * Whether the browser's frame drew it, rather than the page's own work,
   * as the observer's callback of the canvas's size, which draws at once,
   * between two of the browser's frames, at a time of its own, rather
   * than the one that the browser's next frame begins at: the benchmark
   * counts the time between the browser's frames alone.
   */
  scheduled: boolean;
  /** How long building and drawing it took the page, in milliseconds. */
  work: number;
  stats: FrameStats;
}

/**
 * What the canvas shows of its board: the viewport, the canvas's size that
 * the viewport is of, and the selected nodes, which the board's page keeps
 * for the next time that it opens the board (PRD FR-PROJ-04).
 */
export interface CanvasView {
  viewport: Viewport;
  size: Size;
  selection: ReadonlySet<string>;
}

/**
 * What the canvas showed of its board before, which it opens with again in
 * place of the fit to the board's content (restore): its viewport, the size
 * of the canvas then, and the IDs of the nodes selected.
 */
export interface SavedView {
  viewport: Viewport;
  size: Size;
  selection: readonly string[];
}

/** What the canvas tells the page of. */
export interface CanvasEvents {
  /** The selection changed. */
  selection(ids: ReadonlySet<string>): void;
  /** The zoom changed, by a percent or more. */
  zoom(zoom: number): void;
  /** The first frame of the board's content was drawn. */
  rendered(): void;
  /**
   * What the canvas shows changed, its viewport, its size, or its
   * selection, from the frame that placed it on the board's content on,
   * once in each frame at most, as each frame of a pan's or an animation's.
   */
  view(view: CanvasView): void;
}

/**
 * What the canvas asks the page to change of the board, which the page
 * sends as commands (plan note N68): it has none on a board that the user
 * may not change, where a drag on a node pans or draws a rubber band, as on
 * the empty board, and the keys that change it do nothing.
 */
export interface CanvasEdits {
  /**
   * Moves the nodes of places there, as a drag of the selection left them,
   * in one batch (FR-EDIT-02 AC2), expecting the revisions of expected, of
   * the nodes as the drag started, which it moved them from.
   */
  move(
    places: ReadonlyMap<string, Point>,
    expected: ReadonlyMap<string, number>,
  ): void;
  /** Gives the node of id size, as its handle's drag left it, expecting rev, its revision as the drag started. */
  resize(id: string, size: Size, rev: number): void;
  /** Deletes the nodes of ids to the trash, as Delete asks. */
  remove(ids: readonly string[]): void;
  /** Opens the node of id, as for its text, as Enter or a double tap asks. */
  open(id: string): void;
}

/** Returns where the node of id is on the canvas, in its CSS pixels, or undefined for one that the board does not have. */
export type ScreenRectOf = (id: string) => Rect | undefined;

/** What the benchmark reads of a canvas, and drives (bench.tsx). */
export interface CanvasHandle {
  viewport(): Viewport;
  setViewport(v: Viewport): void;
  size(): Size;
  scene: Scene;
  /** Whether the board's content has been read and drawn. */
  ready(): boolean;
  kind(): RendererKind | undefined;
  textures(): number | undefined;
  /** Calls listener with each frame drawn, until the returned function is called. */
  onFrame(listener: (frame: FrameRecord) => void): () => void;
}

/** The elements of a canvas that the controller writes to. */
export interface CanvasElements {
  /** The element that the gestures and keys are on, whose data attributes tell what the canvas shows. */
  host: HTMLElement;
  /** The rubber band's element, over the canvas. */
  band: HTMLElement;
}

/** The browser's frame scheduling, which the tests replace. */
export interface Clock {
  request(frame: (time: number) => void): number;
  cancel(handle: number): void;
  now(): number;
}

/** Reports whether a and b put the same nodes at the same places. */
function samePlaces(
  a: ReadonlyMap<string, Point>,
  b: ReadonlyMap<string, Point>,
): boolean {
  if (a.size !== b.size) {
    return false;
  }
  for (const [id, p] of a) {
    const q = b.get(id);
    if (q === undefined || q.x !== p.x || q.y !== p.y) {
      return false;
    }
  }
  return true;
}

const browserClock: Clock = {
  request: (frame) => requestAnimationFrame(frame),
  cancel: (handle) => cancelAnimationFrame(handle),
  now: () => performance.now(),
};

/** A board's canvas, apart from what draws it and the page around it. */
export class CanvasController implements GestureHost {
  readonly scene: Scene;
  readonly gestures: Gestures;
  /** The handle of the canvas that the benchmark drives. */
  readonly handle: CanvasHandle;
  /** Returns what a card shows of a node. */
  labels: (item: NodeItem) => NodeLabels = (item) => ({
    title: item.node.title,
    kind: item.node.type,
    body: "",
  });
  /** Whether the system asks for reduced motion, which makes every move instant. */
  reducedMotion = false;
  /** What the page changes of the board as the canvas asks, or undefined where it may not be changed. */
  edits: CanvasEdits | undefined;
  /** Whether a move or a resize snaps to the board's grid (FR-EDIT-02). */
  snap = true;
  private v: Viewport = { x: 0, y: 0, zoom: 1 };
  /**
   * The canvas's size, in CSS pixels, that its viewport is of, and that
   * every move is about the centre of, as the toolbar's, the keys', or the
   * minimap's: or, while it has none, as of a host that the page does not
   * display, the last that had an area, whose centre the size after keeps
   * (setSize), so that a move meanwhile is about the centre that the canvas
   * shows again, rather than about the top-left corner of none. It is of
   * none until the canvas has had an area.
   */
  private sz: Size = { width: 0, height: 0 };
  /** Whether the canvas has an area now, which it draws in, and is fitted to the content in. */
  private displayed = false;
  private resolution = 1;
  private engine: Engine | undefined;
  /**
   * Whether the renderer has the canvas's size and resolution, which the
   * frame that draws next gives it where it has not, rather than setSize
   * or setEngine as they take them: a renderer that throws as it is sized,
   * as one whose GPU's device is lost, throws in that frame, as it would as
   * it draws, which is tried again in the next frame (threw), and once the
   * browser gives its context back, rather than into the page's own work,
   * as React's commit, or the start's handler, which would say that the
   * browser has no renderer at all.
   */
  private sized = false;
  /**
   * Whether the renderer threw in the last frame, as it was sized or as it
   * drew. A frame whose renderer throws asks for the next frame, which tries
   * again, so that a renderer that throws once, as in the frame that
   * setEngine asked for, draws in the next, rather than once something
   * moves the canvas or changes: but not after a frame whose renderer threw
   * too, so that a renderer that throws in each frame, as one whose context
   * the browser never gives back, does not spin the loop at the display's
   * rate, reporting each, but throws again once something asks for a frame,
   * or the browser gives its context back (onRestore).
   */
  private threw = false;
  /** Stops the call of the engine's onRestore. */
  private stopRestore: (() => void) | undefined;
  private minimap: Minimap | undefined;
  private elements: CanvasElements | undefined;
  private selected: ReadonlySet<string> = new Set();
  private selectionVersion = 0;
  /** What the project's document holds of the board, as the page last gave it (sync). */
  private docSeen: Pick<Doc, "nodes" | "connections"> = {
    nodes: new Map(),
    connections: new Map(),
  };
  /**
   * The nodes where a drag has them, which the scene shows in place of where
   * the document has them until the drag ends: the selection's positions as
   * a move has them, or a node's size as its resize has it.
   */
  private preview:
    | {
        positions?: ReadonlyMap<string, Point>;
        size?: { id: string; size: Size };
      }
    | undefined;
  /** The nodes that the scene shows of the preview, of the document's nodes and the preview that they were made of (shownNodes). */
  private shown:
    | {
        nodes: ReadonlyMap<string, Node>;
        preview: NonNullable<CanvasController["preview"]>;
        shown: ReadonlyMap<string, Node>;
      }
    | undefined;
  /**
   * The nodes that the page's batches create, which the selection keeps
   * though the scene has them not yet, until those batches have settled
   * (selectComing).
   */
  private coming: ReadonlySet<string> = new Set();
  /**
   * The move of the selection under way: where its nodes were as it
   * started, and their revisions then, which its batch expects, of the node
   * that it holds at anchor, and where they are now.
   */
  private moving:
    | {
        anchor: Point;
        start: ReadonlyMap<string, Point>;
        revs: ReadonlyMap<string, number>;
        places: ReadonlyMap<string, Point>;
      }
    | undefined;
  /** The resize under way: of the node of id, at revision rev, from its size as it started, to its size now. */
  private resizing:
    | { id: string; rev: number; from: Size; size?: Size }
    | undefined;
  /** Which nodes the canvas dims, as the tag filter asks (FR-EDIT-06 AC1), and how often that changed. */
  private dims: ((node: Node) => boolean) | undefined;
  private dimVersion = 0;
  /**
   * How many of the board's nodes are dimmed, of the document's nodes and
   * the dims' version that it was counted at, rather than of the scene's,
   * which each move of a drag changes, and none of their tags.
   */
  private dimmedCount:
    | { nodes: ReadonlyMap<string, Node>; dims: number; count: number }
    | undefined;
  /** What the canvas places over its nodes in each frame, as the resize handle and the text's editor (follow). */
  private readonly followers = new Set<(rectOf: ScreenRectOf) => void>();
  /** The node that the canvas opens on, in place of the fit or the view that it restores, as a link to the node asks (focus). */
  private focusId: string | undefined;
  private labelsVersion = 0;
  private theme: Theme | undefined;
  private modeValue: Mode = "pan";
  private animation:
    | { from: Viewport; to: Viewport; start: number }
    | undefined;
  private pending = 0;
  /** Whether the loop stopped, as the canvas went from the page, so that nothing asks for a frame after. */
  private stopped = false;
  /**
   * Whether the board's content has been read, and whether the canvas has
   * been placed on it once, as the first frame that it draws after fits it
   * to the content, whatever moved it before (frame).
   */
  private ready = false;
  private placed = false;
  /** What the canvas places itself at in place of the fit, until it is placed (restore). */
  private saved: SavedView | undefined;
  /** What the canvas last told the page that it shows (report). */
  private reported:
    | { viewport: Viewport; size: Size; selection: number }
    | undefined;
  private renderedOnce = false;
  private shownZoom = 0;
  private lastData: Record<string, string> = {};
  private readonly listeners = new Set<(frame: FrameRecord) => void>();

  constructor(
    boardId: string,
    private readonly events: CanvasEvents,
    private readonly clock: Clock = browserClock,
  ) {
    this.scene = new Scene(boardId);
    this.gestures = new Gestures(this);
    this.handle = {
      viewport: () => this.v,
      setViewport: (v) => this.setViewport(v),
      size: () => this.sz,
      scene: this.scene,
      ready: () => this.renderedOnce,
      kind: () => this.engine?.kind,
      textures: () => this.engine?.textures(),
      onFrame: (listener) => {
        this.listeners.add(listener);
        return () => {
          this.listeners.delete(listener);
        };
      },
    };
  }

  /** Takes the elements that the canvas writes to, which starts the loop again, or none once it is gone. */
  attach(elements: CanvasElements | undefined): void {
    this.elements = elements;
    this.lastData = {};
    if (elements !== undefined) {
      this.stopped = false;
    }
    this.invalidate();
  }

  /** Takes the renderer, once it has started, or none once it ends. */
  setEngine(engine: Engine | undefined): void {
    this.stopRestore?.();
    this.engine = engine;
    // The renderer's context, or its GPU's device, that the browser gives
    // back after it took it, as from a tablet's tab in the background,
    // leaves the canvas blank: the canvas draws again then, as it would
    // else only once something moved it or changed.
    this.stopRestore = engine?.onRestore(() => this.invalidate());
    this.sized = false;
    this.threw = false;
    this.lastData = {};
    this.invalidate();
  }

  setMinimap(minimap: Minimap | undefined): void {
    this.minimap = minimap;
    this.invalidate();
  }

  setTheme(theme: Theme): void {
    // The same colours, read again, as once the window gets the focus back
    // in forced colours, draw nothing anew.
    if (this.theme !== undefined && sameTheme(this.theme, theme)) {
      return;
    }
    this.theme = theme;
    this.invalidate();
  }

  /**
   * Takes the canvas's size, in CSS pixels, keeping the board's point at its
   * centre where it is, and those of where an animation in progress goes
   * from and to, so that it goes on from where the canvas is now, rather
   * than from and to views of the size before, as a fit that the window's
   * turn interrupts would. A size of none, as of a host that the page does
   * not display, moves nothing, and stops the canvas drawing: the canvas,
   * and the renderer's canvas, keep the last size that had an area (sz),
   * whose centre the size after keeps.
   */
  setSize(size: Size, resolution: number): void {
    const displayed = size.width > 0 && size.height > 0;
    if (
      displayed === this.displayed &&
      (!displayed ||
        (size.width === this.sz.width && size.height === this.sz.height)) &&
      resolution === this.resolution
    ) {
      return;
    }
    if (displayed) {
      const was = this.sz;
      if (this.placed && was.width > 0 && was.height > 0) {
        const kept = (v: Viewport) =>
          anchorAt(
            centerOf(v, was),
            { x: size.width / 2, y: size.height / 2 },
            v.zoom,
          );
        this.v = kept(this.v);
        const a = this.animation;
        if (a !== undefined) {
          this.animation = { ...a, from: kept(a.from), to: kept(a.to) };
        }
      }
      this.sz = size;
    }
    this.displayed = displayed;
    this.resolution = resolution;
    this.sized = false;
    if (this.engine === undefined) {
      this.invalidate();
      return;
    }
    // Resizing the renderer clears its canvas, which the browser would show
    // so, as a frame of nothing, before the next frame drew it: the canvas
    // sizes the renderer, and draws, at once, as the browser tells of its
    // new size before it paints.
    this.drawNow();
  }

  /**
   * Takes what the project's document holds of the board, and whether the
   * names of its components or variables, which cards show, changed.
   */
  sync(doc: Pick<Doc, "nodes" | "connections">, namesChanged: boolean): void {
    this.docSeen = doc;
    this.syncScene(namesChanged);
  }

  /**
   * Returns the nodes that the scene shows in place of the document's: none,
   * or, while a drag moves nodes or resizes one, those that the drag has,
   * where it has them, as nodes of their own, made anew for each move, and
   * for each change of the document meanwhile, which the scene builds anew,
   * as it does any node that changed, without reading the rest of the
   * document's nodes for a move (Scene.sync).
   */
  private shownNodes(): ReadonlyMap<string, Node> | undefined {
    const p = this.preview;
    if (p === undefined) {
      this.shown = undefined;
      return undefined;
    }
    const nodes = this.docSeen.nodes;
    const s = this.shown;
    if (s !== undefined && s.nodes === nodes && s.preview === p) {
      return s.shown;
    }
    const shown = new Map<string, Node>();
    for (const [id, position] of p.positions ?? []) {
      const node = nodes.get(id);
      if (node !== undefined) {
        shown.set(id, { ...node, position });
      }
    }
    if (p.size !== undefined) {
      const node = nodes.get(p.size.id);
      if (node !== undefined) {
        shown.set(p.size.id, { ...node, size: p.size.size });
      }
    }
    this.shown = { nodes, preview: p, shown };
    return shown;
  }

  /** Takes what the scene shows of the document into it (shownNodes). */
  private syncScene(namesChanged = false): void {
    const targets = this.scene.targetsVersion;
    const changed = this.scene.sync(this.docSeen, this.shownNodes());
    const relabel = namesChanged || this.scene.targetsVersion !== targets;
    if (relabel) {
      // A card names components, variables, and, a jumper's, the node that
      // it goes to, which may have changed without it: the renderer reads
      // every card's labels again, and writes again those that they
      // changed. A card of a node that changed itself is written anew
      // alone.
      this.labelsVersion++;
    }
    if (changed) {
      // A selected node that the scene does not have, as one that another
      // session deleted, leaves the selection, but for one that the page's
      // batches create, which is coming (selectComing).
      const kept = [...this.selected].filter(
        (id) => this.scene.nodes.get(id) !== undefined || this.coming.has(id),
      );
      if (kept.length !== this.selected.size) {
        this.setSelection(new Set(kept));
      }
    }
    if (changed || relabel) {
      this.invalidate();
    }
  }

  /** Tells that the messages that labels returns changed, as with the locale. */
  relabel(): void {
    this.labelsVersion++;
    this.invalidate();
  }

  /** Tells that the board's content has been read, so that the canvas is placed on it. */
  setReady(): void {
    this.ready = true;
    this.invalidate();
  }

  /**
   * Has the canvas placed on the board's content as view says it was, once
   * the content has been read, rather than fitted to it (PRD FR-PROJ-04):
   * the board's point at the centre of the canvas then at the centre of the
   * canvas now, at the zoom then, so that a canvas of the same size shows
   * the same part of the board, and one of another, as a tablet turned
   * since, the part about the same centre, as a resize keeps it; and the
   * nodes that it selected that the board still has, selected again, unless
   * the user has selected others since. It does nothing once the canvas has
   * been placed. It asks for no frame, so that a page may call it as it
   * renders, as the frame that places the canvas comes once the content has
   * been read (setReady) and the canvas has a size (setSize), which each ask
   * for one.
   */
  restore(view: SavedView): void {
    if (!this.placed) {
      this.saved = view;
    }
  }

  // The gestures' host.

  viewport(): Viewport {
    return this.v;
  }

  size(): Size {
    return this.sz;
  }

  /** The centre of the canvas, which a move of the toolbar's, the keys', or the minimap's is about (sz). */
  private centre(): Point {
    return { x: this.sz.width / 2, y: this.sz.height / 2 };
  }

  /**
   * Shows v at once, ending a zoom or a move that the canvas animates. A v
   * that is the viewport moves nothing, so it does not end an animation, as
   * a wheel's event without a delta, or Safari's gesture as it starts, does
   * not. A move does not place the canvas on the board's content: the
   * frame that fits the canvas to it, the first after it has been read,
   * takes the place of every move before, as of a wheel turned over the
   * canvas while it reads it, and as of a pinch's move that comes after the
   * read and before that frame, as a browser sends the moves of a frame
   * before its callbacks, once a finger moves in each.
   */
  setViewport(v: Viewport): void {
    if (sameViewport(v, this.v)) {
      return;
    }
    this.animation = undefined;
    this.v = v;
    this.invalidate();
  }

  mode(): Mode {
    return this.modeValue;
  }

  setMode(mode: Mode): void {
    this.modeValue = mode;
  }

  isSelected(id: string): boolean {
    return this.selected.has(id);
  }

  /** The selected nodes. */
  selection(): ReadonlySet<string> {
    return this.selected;
  }

  movable(): boolean {
    return this.edits !== undefined;
  }

  now(): number {
    return this.clock.now();
  }

  open(id: string): void {
    this.edits?.open(id);
  }

  /** Reports whether the scene has the node of id, as it has not once another session deleted it. */
  has(id: string): boolean {
    return this.scene.nodes.get(id) !== undefined;
  }

  /**
   * Starts moving the selection, which holds the node of id, or, where pick
   * says how, the selection that the node is first selected into, from
   * where the scene has its nodes, and reports whether it did: not where the
   * board may not be changed, nor once the node has gone, nor while a
   * node's handle resizes it, as under another finger, whose preview a
   * move's would else take the place of, nor of a node that is not
   * selected without pick. A move that does not start selects nothing.
   */
  moveStart(id: string, pick?: SelectHow): boolean {
    const held = this.scene.nodes.get(id);
    if (
      this.edits === undefined ||
      held === undefined ||
      this.resizing !== undefined
    ) {
      return false;
    }
    if (pick !== undefined) {
      this.select([id], pick);
    }
    if (!this.selected.has(id)) {
      return false;
    }
    const start = new Map<string, Point>();
    const revs = new Map<string, number>();
    for (const selected of this.selected) {
      const item = this.scene.nodes.get(selected);
      if (item !== undefined) {
        start.set(selected, item.node.position);
        revs.set(selected, item.node.rev);
      }
    }
    this.moving = {
      anchor: held.node.position,
      start,
      revs,
      places: new Map(),
    };
    return true;
  }

  /**
   * Moves the selection by delta of the board from where its move started:
   * a move that leaves each node where it is, as in each frame that the
   * gestures move the selection under a pointer that is still, or one that
   * the grid keeps in place, changes nothing, and asks for no frame.
   */
  moveTo(delta: Point): void {
    const m = this.moving;
    if (m === undefined) {
      return;
    }
    const places = movedBy(m.start, m.anchor, delta, this.snap);
    if (samePlaces(places, m.places)) {
      return;
    }
    this.moving = { ...m, places };
    this.preview = { positions: places };
    this.syncScene();
  }

  /**
   * Ends the move of the selection: where it left the nodes, which the page
   * sends as one batch, expecting the revisions that the nodes had as it
   * started, which the document shows at once, before the next frame, so
   * that the nodes stay where the drag left them; or, cancelled, where they
   * were, as of a move that a second finger ended.
   */
  moveEnd(commit: boolean): void {
    const m = this.moving;
    this.moving = undefined;
    this.preview = undefined;
    if (m !== undefined && commit) {
      // A node that went while it moved, as one that another session
      // deleted, does not move, which the server would refuse whole.
      const places = new Map(
        [...m.places].filter(([id]) => {
          const node = this.docSeen.nodes.get(id);
          return node !== undefined && node.deleted_at === undefined;
        }),
      );
      if (places.size > 0) {
        this.edits?.move(places, m.revs);
      }
    }
    this.syncScene();
    // A frame of the move hid the resize handle, which a frame shows again,
    // though the move left the scene as it was, as one that came back where
    // it started, or never left it.
    if (m !== undefined) {
      this.invalidate();
    }
  }

  /** Reports whether the node of id is being moved, which its resize handle hides for. */
  moves(): boolean {
    return this.moving !== undefined;
  }

  /**
   * Starts resizing the node of id by its handle, from its size as the
   * scene has it, and reports whether it did: not where the board may not
   * be changed, nor once the node has gone, nor while a move, or another
   * resize, is under way, whose preview its own would take the place of.
   */
  resizeStart(id: string): boolean {
    const item = this.scene.nodes.get(id);
    if (
      this.edits === undefined ||
      item === undefined ||
      this.moving !== undefined ||
      this.resizing !== undefined
    ) {
      return false;
    }
    this.resizing = {
      id,
      rev: item.node.rev,
      from: { width: item.rect.width, height: item.rect.height },
    };
    return true;
  }

  /** Resizes the node that the handle holds by delta, in CSS pixels of the canvas, from its size as its resize started. */
  resizeTo(delta: Point): void {
    const r = this.resizing;
    if (r === undefined) {
      return;
    }
    const zoom = this.v.zoom;
    const size = resizedBy(
      r.from,
      { x: delta.x / zoom, y: delta.y / zoom },
      this.snap,
    );
    if (size.width === r.size?.width && size.height === r.size.height) {
      return;
    }
    this.resizing = { ...r, size };
    this.preview = { size: { id: r.id, size } };
    this.syncScene();
  }

  /** Ends the resize: the size that it left the node, which the page sends, expecting the node's revision as it started, or, cancelled, the size before. */
  resizeEnd(commit: boolean): void {
    const r = this.resizing;
    this.resizing = undefined;
    this.preview = undefined;
    if (
      r !== undefined &&
      commit &&
      r.size !== undefined &&
      (r.size.width !== r.from.width || r.size.height !== r.from.height) &&
      this.docSeen.nodes.get(r.id)?.deleted_at === undefined &&
      this.docSeen.nodes.has(r.id)
    ) {
      this.edits?.resize(r.id, r.size, r.rev);
    }
    this.syncScene();
  }

  /**
   * Dims the nodes that dim tells, or none, as the tag filter asks (FR-EDIT-06
   * AC1): the canvas draws them faint, and changes nothing of the board.
   */
  setDimmed(dim: ((node: Node) => boolean) | undefined): void {
    if (dim === this.dims) {
      return;
    }
    this.dims = dim;
    this.dimVersion++;
    this.invalidate();
  }

  /**
   * How many of the board's nodes are dimmed, counted once for each change
   * of the document's nodes or of the dims, rather than in each frame of a
   * drag, whose preview changes where nodes are, and none of their tags.
   */
  private dimmed(): number {
    const dim = this.dims;
    if (dim === undefined) {
      return 0;
    }
    const c = this.dimmedCount;
    const nodes = this.docSeen.nodes;
    if (c !== undefined && c.nodes === nodes && c.dims === this.dimVersion) {
      return c.count;
    }
    let count = 0;
    this.scene.nodes.forEach((_, item) => {
      if (dim(item.node)) {
        count++;
      }
    });
    this.dimmedCount = { nodes, dims: this.dimVersion, count };
    return count;
  }

  /**
   * Calls place at once, and in each frame that the canvas draws, with where
   * its nodes are on it, so that what the page places over a node, as its
   * resize handle or its text's editor, is over it from the first, rather
   * than shown once where it was not yet placed, and follows it as the
   * canvas pans, zooms, or moves it, in the same frame, until the returned
   * function is called.
   */
  follow(place: (rectOf: ScreenRectOf) => void): () => void {
    this.followers.add(place);
    place(this.screenRect);
    this.invalidate();
    return () => {
      this.followers.delete(place);
    };
  }

  /** Returns where the node of id is on the canvas, in its CSS pixels. */
  private screenRect: ScreenRectOf = (id) => {
    const r = this.scene.nodes.rect(id);
    if (r === undefined) {
      return undefined;
    }
    const at = toScreen(this.v, r);
    return {
      x: at.x,
      y: at.y,
      width: r.width * this.v.zoom,
      height: r.height * this.v.zoom,
    };
  };

  /**
   * Selects the node of id and shows it whole, as a link to it asks: once
   * the board's content has been read, in place of the fit or of the view
   * that the canvas restores, or at once, animated, on a canvas placed
   * already. It reports nothing of a node that the board does not have,
   * which the canvas then opens as it would without it.
   */
  focus(id: string): void {
    if (!this.placed) {
      this.focusId = id;
      this.invalidate();
      return;
    }
    if (this.scene.nodes.get(id) !== undefined) {
      this.select([id], "replace");
      this.zoomToSelection();
    }
  }

  nodeAt(p: Point, slop: number): string | undefined {
    return this.scene.nodeAt(p, slop);
  }

  nodesIn(r: Rect): string[] {
    return this.scene.nodesIn(r);
  }

  select(ids: string[], how: SelectHow): void {
    const next = new Set(how === "replace" ? [] : this.selected);
    for (const id of ids) {
      if (how === "toggle" && next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
    }
    if (
      next.size !== this.selected.size ||
      [...next].some((id) => !this.selected.has(id))
    ) {
      this.setSelection(next);
    }
  }

  /**
   * Selects the nodes of ids, in place of the selection, as the page's
   * batches that create them show them, which may be after the scene next
   * changes, as with the copies of a duplicate of thousands, whose batches
   * the document shows in turn: the selection keeps them, though the scene
   * has them not yet, until done settles, rather than drop those that a
   * batch after the first creates as the first shows its own (syncScene),
   * and then drops those that the scene has not, as of a batch refused.
   */
  selectComing(ids: readonly string[], done: Promise<unknown>): void {
    const coming = new Set(ids);
    this.coming = coming;
    this.select([...ids], "replace");
    const settled = () => {
      if (this.coming === coming) {
        this.coming = new Set();
      }
      const kept = [...this.selected].filter(
        (id) => !coming.has(id) || this.has(id) || this.coming.has(id),
      );
      if (kept.length !== this.selected.size) {
        this.setSelection(new Set(kept));
      }
    };
    done.then(settled, settled);
  }

  private setSelection(ids: ReadonlySet<string>): void {
    this.selected = ids;
    this.selectionVersion++;
    this.events.selection(ids);
    this.invalidate();
  }

  /**
   * Gives back the capture of the pointer of id, which the host took as it
   * went down, once the gestures no longer follow it, as once a press whose
   * release the canvas did not hear of ended, or another pointer's press,
   * or the window's blur, ended it: the browser, which did not hear of the
   * release either, might else send the pointer's next press, as of a
   * button of the toolbar, to the host rather than to the button.
   */
  release(id: number): void {
    const host = this.elements?.host;
    try {
      if (host?.hasPointerCapture?.(id)) {
        host.releasePointerCapture(id);
      }
    } catch {
      // A pointer that the browser no longer has keeps no capture.
    }
  }

  band(r: Rect | undefined): void {
    const el = this.elements?.band;
    if (el === undefined) {
      return;
    }
    if (r === undefined) {
      el.hidden = true;
      return;
    }
    el.hidden = false;
    el.style.transform = `translate(${r.x}px, ${r.y}px)`;
    el.style.width = `${r.width}px`;
    el.style.height = `${r.height}px`;
  }

  // What the toolbar and the keys do.

  /**
   * Moves to v, animated unless the system asks for reduced motion, which
   * the fit to the board's content, once it has been read, takes the place
   * of, as it does of a move of setViewport's.
   */
  animateTo(v: Viewport): void {
    if (this.reducedMotion || !this.displayed) {
      this.setViewport(v);
      return;
    }
    this.animation = { from: this.v, to: v, start: this.clock.now() };
    this.invalidate();
  }

  /** Where the animation in progress goes, or the viewport. */
  private target(): Viewport {
    return this.animation?.to ?? this.v;
  }

  /** Zooms in or out by a step, about the canvas's centre. */
  zoomStep(direction: 1 | -1): void {
    const v = this.target();
    this.animateTo(zoomAt(v, this.centre(), stepZoom(v.zoom, direction)));
  }

  /** Zooms to zoom about the canvas's centre. */
  zoomTo(zoom: number): void {
    const v = this.target();
    this.animateTo(zoomAt(v, this.centre(), clampZoom(zoom)));
  }

  /** Shows the board's content whole, or the board's origin at 100% for a board without any. */
  fit(): void {
    this.animateTo(this.fitted());
  }

  private fitted(): Viewport {
    const bounds = this.scene.contentBounds();
    if (bounds === undefined) {
      return emptyView;
    }
    return fitTo(bounds, this.sz, 48, fitZoom);
  }

  /** Shows the selected nodes whole, and reports whether there were any. */
  zoomToSelection(): boolean {
    const bounds = this.scene.boundsOf(this.selected);
    if (bounds === undefined) {
      return false;
    }
    this.animateTo(fitTo(bounds, this.sz, 48, fitZoom));
    return true;
  }

  /**
   * Centres the canvas on p of the board, at once, as the minimap does, at
   * the zoom that an animation in progress goes to, which this ends, rather
   * than at the one that it was passing through, which no step of the zoom
   * gives.
   */
  centerOn(p: Point): void {
    this.setViewport(anchorAt(p, this.centre(), this.target().zoom));
  }

  /** Centres the canvas on the board's point at p of the minimap, in its CSS pixels. */
  centerOnMinimap(p: Point): void {
    const world = this.minimap?.toWorld(p);
    if (world !== undefined) {
      this.centerOn(world);
    }
  }

  /** Takes a key that went down on the canvas, and reports whether it was the canvas's. */
  key(e: KeyInput): boolean {
    const action = keyAction(e);
    if (action === undefined) {
      return false;
    }
    switch (action.kind) {
      case "pan": {
        const v = this.target();
        this.animateTo(
          panBy(v, action.dx * this.sz.width, action.dy * this.sz.height),
        );
        return true;
      }
      case "zoom":
        this.zoomStep(action.direction);
        return true;
      case "actual":
        this.zoomTo(1);
        return true;
      case "fit":
        this.fit();
        return true;
      case "selection":
        this.zoomToSelection();
        return true;
      case "deselect": {
        // Escape is the canvas's while there is a selection to clear, and
        // else the page's, as of what around the canvas closes with it.
        const had = this.selected.size > 0;
        this.select([], "replace");
        return had;
      }
      case "delete":
        if (this.edits === undefined || this.selected.size === 0) {
          return false;
        }
        this.edits.remove([...this.selected]);
        return true;
      case "open":
        if (this.edits === undefined || this.selected.size !== 1) {
          return false;
        }
        this.edits.open([...this.selected][0]);
        return true;
    }
  }

  /**
   * Takes a pointer that went down on the canvas, at a point of it, and
   * reports whether the canvas follows it (Gestures' down): one that it
   * does stops an animation in progress where it is, as a finger that takes
   * the board does, and one that it ignores, as a right click, or a third
   * finger, leaves it to go on. What the pointer does after, as it moves,
   * leaves, goes up, or is cancelled, goes to the gestures as it comes.
   */
  down(input: PointerInput): boolean {
    const followed = this.gestures.down(input);
    if (followed) {
      this.animation = undefined;
    }
    return followed;
  }

  // The loop.

  /** Asks for a frame, once. */
  invalidate(): void {
    if (this.pending === 0 && !this.stopped) {
      this.pending = this.clock.request(this.frame);
    }
  }

  /**
   * Draws a frame at once, in place of the one asked for. It is drawn from
   * within the page's own work, as React's commit that sets the canvas's
   * height, or the observer's callback of its size, rather than in a frame
   * of the browser's: a renderer that throws, as one whose GPU's device is
   * lost, throws in the loop's frame that it asks for instead, which tries
   * again, and where the browser reports what still throws, as of any
   * frame, rather than into React's commit, which would take the board's
   * page down with it.
   */
  private drawNow(): void {
    if (this.stopped) {
      return;
    }
    if (this.pending !== 0) {
      this.clock.cancel(this.pending);
      this.pending = 0;
    }
    try {
      this.frame(this.clock.now(), false);
    } catch {
      this.invalidate();
    }
  }

  private readonly frame = (time: number, scheduled = true): void => {
    this.pending = 0;
    const size = this.sz;
    if (this.ready && !this.placed && this.displayed) {
      // The fit, or the view that the canvas restores, takes the place of
      // whatever moved the canvas before it, as while the content was read,
      // or since, before this frame: a zoom of the toolbar's, which would
      // else go on after it, or a pinch's move, which the browser sent
      // before this frame's callback. A gesture under way, as the pinch,
      // goes on from it, rather than take the board back with its next
      // move. A restored view is the first that the board's content is
      // drawn at, with its selection, rather than a fit that it moves from,
      // but for a selection that the user made before it, as of a board
      // whose nodes the document had, which the canvas showed while it read
      // them anew, which stays.
      const from = this.v;
      const saved = this.saved;
      this.saved = undefined;
      // A link to a node opens the canvas on it, selected, in place of
      // either, where the board has it (focus).
      const focus =
        this.focusId === undefined
          ? undefined
          : this.scene.nodes.rect(this.focusId);
      this.v =
        focus !== undefined
          ? fitTo(focus, this.sz, 48, fitZoom)
          : saved === undefined
            ? this.fitted()
            : anchorAt(
                centerOf(saved.viewport, saved.size),
                this.centre(),
                saved.viewport.zoom,
              );
      this.animation = undefined;
      this.placed = true;
      this.gestures.replaced(from);
      const focused = this.focusId;
      this.focusId = undefined;
      if (focus !== undefined && focused !== undefined) {
        this.select([focused], "replace");
      } else if (saved !== undefined && this.selected.size === 0) {
        this.select(
          saved.selection.filter(
            (id) => this.scene.nodes.get(id) !== undefined,
          ),
          "replace",
        );
      }
    }
    const a = this.animation;
    if (a !== undefined) {
      const t = (this.clock.now() - a.start) / animationTime;
      if (t >= 1) {
        this.v = a.to;
        this.animation = undefined;
      } else {
        this.v = between(a.from, a.to, Math.max(0, t), size);
        this.invalidate();
      }
    }
    // A rubber band under way stays on the board's point where it started.
    this.gestures.refresh();
    this.showZoom();
    for (const place of this.followers) {
      place(this.screenRect);
    }
    if (this.placed) {
      this.report();
    }
    const theme = this.theme;
    if (theme === undefined || !this.displayed) {
      return;
    }
    // The minimap draws on a 2D canvas of its own, which needs nothing of
    // the renderer's, so that it maps the board, and takes a tap, while the
    // renderer starts too.
    this.minimap?.draw(this.scene, this.v, size, theme);
    const engine = this.engine;
    if (engine === undefined) {
      return;
    }
    let start: number;
    let stats: FrameStats;
    try {
      if (!this.sized) {
        engine.resize(size, this.resolution);
        this.sized = true;
      }
      start = this.clock.now();
      stats = engine.render({
        viewport: this.v,
        size,
        scene: this.scene,
        selection: this.selected,
        selectionVersion: this.selectionVersion,
        labels: this.labels,
        labelsVersion: this.labelsVersion,
        dimmed: this.dims,
        dimVersion: this.dimVersion,
        theme,
      });
    } catch (error) {
      if (!this.threw) {
        this.threw = true;
        this.invalidate();
      }
      throw error;
    }
    this.threw = false;
    const work = this.clock.now() - start;
    this.writeData(engine.kind, stats);
    if (this.ready && !this.renderedOnce) {
      this.renderedOnce = true;
      this.events.rendered();
    }
    for (const listener of this.listeners) {
      listener({ time, work, stats, scheduled });
    }
  };

  /**
   * Tells the page what the canvas shows, where its viewport, its size, or
   * its selection is another than it last told: each is replaced rather
   * than changed, so the same objects are the same view.
   */
  private report(): void {
    const r = this.reported;
    if (
      r !== undefined &&
      r.viewport === this.v &&
      r.size === this.sz &&
      r.selection === this.selectionVersion
    ) {
      return;
    }
    this.reported = {
      viewport: this.v,
      size: this.sz,
      selection: this.selectionVersion,
    };
    this.events.view({
      viewport: this.v,
      size: this.sz,
      selection: this.selected,
    });
  }

  private showZoom(): void {
    const percent = Math.round(this.v.zoom * 100);
    if (percent !== this.shownZoom) {
      this.shownZoom = percent;
      this.events.zoom(this.v.zoom);
    }
  }

  /**
   * Writes what the canvas shows to its host's data attributes, which the
   * end-to-end tests read, since what PixiJS draws is pixels: the renderer,
   * the viewport, the level of detail, and what it holds and draws.
   */
  private writeData(kind: RendererKind, stats: FrameStats): void {
    const host = this.elements?.host;
    if (host === undefined) {
      return;
    }
    const data: Record<string, string> = {
      renderer: kind,
      zoom: String(Math.round(this.v.zoom * 1e6) / 1e6),
      x: String(Math.round(this.v.x * 1000) / 1000),
      y: String(Math.round(this.v.y * 1000) / 1000),
      lod: stats.lod,
      nodes: String(stats.nodes),
      visible: String(stats.visible),
      texts: String(stats.texts),
      connections: String(stats.connections),
      selected: String(this.selected.size),
      // The nodes of the board that the tag filter dims (FR-EDIT-06 AC1).
      dimmed: String(this.dimmed()),
      // Whether the frame drew the board's content, once it was read.
      ready: String(this.ready),
    };
    for (const [key, value] of Object.entries(data)) {
      if (this.lastData[key] !== value) {
        host.dataset[key] = value;
      }
    }
    this.lastData = data;
  }

  /** Stops the loop, until the canvas's elements are attached again. */
  stop(): void {
    this.stopped = true;
    if (this.pending !== 0) {
      this.clock.cancel(this.pending);
      this.pending = 0;
    }
    this.gestures.cancelAll();
    this.resizeEnd(false);
  }
}
