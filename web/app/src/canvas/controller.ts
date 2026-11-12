// What drives a board's canvas (PRD FR-EDIT-01, FR-EDIT-11): the viewport,
// which the gestures, the keys, and the toolbar change, the selection, the
// scene of the board, and the loop that draws a frame once something
// changed, and only then, so that a canvas that nothing moves draws
// nothing. A zoom or a move that the toolbar or a key asks for is animated,
// briefly, unless the system asks for reduced motion (NFR-A11Y-07), as the
// canvas's only animation; a gesture follows the fingers at once. A drag
// that moves the selection, or resizes a node by its handle, one at a time,
// as the gestures make them, shows it at once, as the scene of the document
// with the nodes where the drag has them, without a command, and tells the
// page where it left them as it ends, which sends one batch for it
// (FR-EDIT-02 AC2, plan note N68), whose nodes the document shows there at
// once, in the same frame as the drag's end. A drag of a link handle, of a
// port of the node selected, or of an end of the connection selected,
// shows the connection that it would make as a line to its pointer, or to
// the node under it that would take it, and tells the page where it
// landed as it ends (FR-EDIT-03, plan note N69); a tap on a connection's
// line selects the connection, in place of the nodes selected, as a node
// selected takes its place.

import type { Node } from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import { movedBy, resizedBy } from "./edit";
import type { Engine, FrameStats, RendererKind } from "./engine";
import {
  type GestureHost,
  Gestures,
  type Handle,
  type Link,
  type MenuTarget,
  type Mode,
  type PointerInput,
  type SelectHow,
} from "./gestures";
import { type KeyInput, keyAction } from "./keyboard";
import type { NodeLabels } from "./labels";
import { type Curve, curveOf, entryPoint, portPoint, portsOf } from "./layout";
import type { Minimap } from "./minimap";
import { type NodeItem, Scene } from "./scene";
import { sameTheme, type Theme } from "./theme";
import {
  anchorAt,
  between,
  centerOf,
  clampZoom,
  contains,
  emptyView,
  fitTo,
  middleOf,
  type Point,
  panBy,
  type Rect,
  type Size,
  sameViewport,
  stepZoom,
  toScreen,
  type Viewport,
  visibleRect,
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
  /** The connection selected changed, to the one of id, or to none. */
  connection(id: string | undefined): void;
  /** The zoom changed, by a percent or more. */
  zoom(zoom: number): void;
  /**
   * What the canvas offers changed: to create a node at at of the board and
   * connect from to it, as a drag of a port's handle that landed on the
   * empty board asks (FR-EDIT-03 AC3), whose line the canvas shows until
   * the page says what became of the offer (CanvasController's endOffer),
   * or nothing, once the offer has ended, as the page, the next link, a
   * board that may no longer be changed, or one that no longer has the port
   * that the offer would connect, ends it.
   */
  offer(offer: { from: PortOf; at: Point } | undefined): void;
  /**
   * The context menu of target was asked for (PRD FR-EDIT-09, plan note
   * N70), by a long press where held says so, and else by a right click or
   * a key: of the nodes selected, which hold the node that it was asked for
   * on, of the connection selected, or of the board at its point, each of
   * which the canvas selected first where it was not.
   */
  menu(target: MenuTarget, held: boolean): void;
  /** The first frame of the board's content was drawn. */
  rendered(): void;
  /**
   * What the canvas shows changed, its viewport, its size, or its
   * selection, from the frame that placed it on the board's content on,
   * once in each frame at most, as each frame of a pan's or an animation's.
   */
  view(view: CanvasView): void;
}

/** A port that connections leave: port of the node of node, out of an element, or the ID of a branch's output. */
export interface PortOf {
  node: string;
  port: string;
}

/**
 * What the canvas asks the page to change of the board, which the page
 * sends as commands (plan notes N68 and N69): it has none on a board that
 * the user may not change, where a drag on a node pans or draws a rubber
 * band, as on the empty board, a tap selects no connection, and the keys
 * that change it do nothing.
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
  /** Connects from to the node of to, as a drag of a port's handle that landed on it asks. */
  connect(from: PortOf, to: string): void;
  /**
   * Reconnects the connection of id, so that it leaves from, or enters the
   * node of to, as a drag of one of its ends' handles left it, expecting
   * rev, its revision as the drag started.
   */
  reconnect(
    id: string,
    ends: { from?: PortOf; to?: string },
    rev: number,
  ): void;
  /** Deletes the connection of id to the trash, as Delete asks. */
  removeConnection(id: string): void;
  /** Opens the connection of id, in the inspector, as Enter asks. */
  openConnection(id: string): void;
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

/** The browser's frame scheduling, and its timers, which the tests replace. */
export interface Clock {
  request(frame: (time: number) => void): number;
  cancel(handle: number): void;
  now(): number;
  /** Calls then once ms milliseconds have passed, unless the function that it returns is called first. */
  after(ms: number, then: () => void): () => void;
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
  after: (ms, then) => {
    const timer = setTimeout(then, ms);
    return () => clearTimeout(timer);
  },
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
  /**
   * Returns what the canvas's accessibility tree says of a node (CanvasTree.tsx):
   * what its card shows, of what the canvas knows of a jumper's target
   * already, rather than what labels returns, which reads a target that it
   * does not know as its card is first drawn, and would read every
   * jumper's of the board at once for a tree of all of its nodes.
   */
  described: (item: NodeItem) => NodeLabels = (item) => this.labels(item);
  /** Whether the system asks for reduced motion, which makes every move instant. */
  reducedMotion = false;
  private editsOf: CanvasEdits | undefined;
  /** What the page changes of the board as the canvas asks, or undefined where it may not be changed. */
  get edits(): CanvasEdits | undefined {
    return this.editsOf;
  }
  /**
   * Takes what the page changes of the board, or none once the board may no
   * longer be changed, as once the user's role or the project's archive
   * changes, which ends a move, a resize, or a link under way, cancelled,
   * rather than go on moving the nodes of a board that it can no longer
   * change, and leaves no connection selected, which only a board that may
   * be changed selects.
   */
  set edits(edits: CanvasEdits | undefined) {
    this.editsOf = edits;
    if (edits === undefined) {
      if (this.drag !== undefined) {
        this.gestures.cancelDrag();
      }
      this.endOffer();
      this.setConnection(undefined);
    }
  }
  /** Reports whether the canvas draws the jumper node as broken, as one whose target was deleted (FR-EDIT-05 AC1), which the page tells. */
  broken: (node: Node) => boolean = () => false;
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
  /** The connection selected, of which there is one at most, and none while a node is selected. */
  private connection: string | undefined;
  /** Counts the changes of the selection, of the nodes or of the connection. */
  private selectionVersion = 0;
  /** The level of detail that the renderer last drew, by which a tap finds a connection's line as it is drawn. */
  private lod: FrameStats["lod"] = "detail";
  /** What the project's document holds of the board, as the page last gave it (sync). */
  private docSeen: Pick<Doc, "nodes" | "connections"> = {
    nodes: new Map(),
    connections: new Map(),
  };
  /**
   * The drag under way, which the scene shows in place of where the document
   * has its nodes until it ends (shownNodes), and of which there is one at
   * most, a move's or a resize's: of a move of the selection, where its
   * nodes were as it started, and their revisions then, which its batch
   * expects, of the node that it holds at anchor, and where they are now; of
   * a resize by a node's handle, the node of id, at revision rev, its size
   * as it started, and its size now; of a link, what its handle holds, the
   * revision of its connection as it started, where its pointer is on the
   * board, the port, or the node, under it that would take its end, and
   * whether nothing would, where it is (idle), as with its pointer outside
   * the canvas, or on the handle that it left. Each change of it is another
   * object.
   */
  private drag:
    | {
        kind: "move";
        anchor: Point;
        start: ReadonlyMap<string, Point>;
        revs: ReadonlyMap<string, number>;
        places: ReadonlyMap<string, Point>;
      }
    | { kind: "resize"; id: string; rev: number; from: Size; size?: Size }
    | {
        kind: "link";
        link: Link;
        rev: number;
        at?: Point;
        over?: { node: string; port?: string };
        idle?: boolean;
      }
    | undefined;
  /** What the canvas offers, to create a node and connect it, whose line it shows from a port to a point of the board, until the offer ends (endOffer): the page shows what it is told of it (CanvasEvents' offer), and holds none of its own. */
  private offered: { from: PortOf; at: Point } | undefined;
  /** How many of the board's jumpers are broken, of the document's nodes and the labels' version that it was counted at. */
  private brokenCount:
    | { nodes: ReadonlyMap<string, Node>; labels: number; count: number }
    | undefined;
  /**
   * The delta of the board that the move under way was last moved by, and
   * whether the grid snapped it, which the frame's refresh of the gestures
   * moves it by again in each frame while its pointer and the board are
   * still: a move by the same is passed over, rather than its places found
   * anew, and compared, for each node of a drag of thousands (moveTo).
   */
  private lastMove: { delta: Point; snap: boolean } | undefined;
  /** The nodes that the scene shows of the drag, of the document's nodes and the drag that they were made of (shownNodes). */
  private shown:
    | {
        nodes: ReadonlyMap<string, Node>;
        drag: NonNullable<CanvasController["drag"]>;
        shown: ReadonlyMap<string, Node>;
      }
    | undefined;
  /** The document's connections that the connection selected was last kept to (syncScene). */
  private connectionKeptTo: Doc["connections"] | undefined;
  /** The document's nodes that the selection was last kept to, as the scene has them (syncScene). */
  private keptTo: ReadonlyMap<string, Node> | undefined;
  /**
   * The nodes that the page's batches create, which the selection keeps
   * though the scene has them not yet, until those batches have settled
   * (selectComing).
   */
  private coming: ReadonlySet<string> = new Set();
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
  /** What is told of each change of the board's content (watch). */
  private readonly watchers = new Set<() => void>();
  /** Whether the board's content changed in the middle of a drag, which told nothing of it, and whose end tells of it (tellWatchers). */
  private untold = false;
  /** The node that the canvas opens on, in place of the fit or the view that it restores, as a link to the node asks, and what it calls once it has (focus). */
  private focusing: { id: string; found?: () => void } | undefined;
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
    const d = this.drag;
    if (d === undefined) {
      this.shown = undefined;
      return undefined;
    }
    const nodes = this.docSeen.nodes;
    const s = this.shown;
    if (s !== undefined && s.nodes === nodes && s.drag === d) {
      return s.shown;
    }
    const shown = new Map<string, Node>();
    if (d.kind === "move") {
      for (const [id, position] of d.places) {
        const node = nodes.get(id);
        if (node !== undefined) {
          shown.set(id, { ...node, position });
        }
      }
    } else if (d.kind === "resize" && d.size !== undefined) {
      const node = nodes.get(d.id);
      if (node !== undefined) {
        shown.set(d.id, { ...node, size: d.size });
      }
    }
    this.shown = { nodes, drag: d, shown };
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
    if (changed && this.docSeen.nodes !== this.keptTo) {
      // A selected node that the scene does not have, as one that another
      // session deleted, leaves the selection, but for one that the page's
      // batches create, which is coming (selectComing): once for each
      // change of the document's nodes, as only it takes a node out of the
      // scene, rather than in each move of a drag of the selection, which
      // changes where nodes are alone.
      this.keptTo = this.docSeen.nodes;
      const kept = [...this.selected].filter(
        (id) => this.has(id) || this.coming.has(id),
      );
      if (kept.length !== this.selected.size) {
        this.setSelection(new Set(kept));
      }
    }
    if (
      this.connection !== undefined &&
      this.docSeen.connections !== this.connectionKeptTo
    ) {
      // A connection selected that the board no longer has, as one deleted,
      // here or by another session, or gone to the trash with its node, is
      // selected no longer.
      this.connectionKeptTo = this.docSeen.connections;
      if (!this.aliveConnection(this.connection)) {
        this.setConnection(undefined);
      }
    }
    if (changed && this.offered !== undefined) {
      // What the canvas offers ends once the board no longer has the port
      // that it would connect, as of a node, or of a branch's output, that
      // another session deleted, whose line the canvas can no longer draw,
      // and which the server would refuse the new node's batch for.
      const from = this.scene.nodes.get(this.offered.from.node);
      if (
        from === undefined ||
        !portsOf(from.node).includes(this.offered.from.port)
      ) {
        this.endOffer();
      }
    }
    if (changed || relabel) {
      this.invalidate();
      this.tellWatchers();
    }
  }

  /** Tells that the messages that labels returns changed, as with the locale. */
  relabel(): void {
    this.labelsVersion++;
    this.invalidate();
    this.tellWatchers();
  }

  /**
   * Calls changed each time that the board's content, or what its cards
   * say, changed, but in the middle of a drag, whose every move changes
   * where its nodes are in the scene, and whose end tells of them where it
   * left them, and of what else changed meanwhile, as what another session
   * did, though the drag itself left the board as it was (tellUntold),
   * until the returned function is called: what the page shows of the whole
   * board beside the canvas's pixels, as its accessibility tree, is made
   * again then, rather than for each move.
   */
  watch(changed: () => void): () => void {
    this.watchers.add(changed);
    return () => {
      this.watchers.delete(changed);
    };
  }

  private tellWatchers(): void {
    if (this.drag !== undefined) {
      this.untold = true;
      return;
    }
    this.untold = false;
    for (const changed of [...this.watchers]) {
      changed();
    }
  }

  /**
   * Tells of what changed in the middle of the drag that has just ended,
   * where its end told of nothing, as that of a link, which changes nothing
   * of the scene, or of a move that left its nodes where they were.
   */
  private tellUntold(): void {
    if (this.untold) {
      this.tellWatchers();
    }
  }

  /** Counts the changes of what cards say that no change of a node tells of, as of the name of a speaker, or of the page's locale (relabel). */
  labelling(): number {
    return this.labelsVersion;
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

  after(ms: number, then: () => void): () => void {
    return this.clock.after(ms, then);
  }

  open(id: string): void {
    this.edits?.open(id);
  }

  /**
   * Has the page show the context menu of target (PRD FR-EDIT-09, plan
   * note N70), which a long press asked for where held says so: of the
   * selection, where target is a node of it, and else of that node alone,
   * which it selects first, as a tap does, so that the menu is of what the
   * canvas shows selected; of a connection, which it selects, on a board
   * that may be changed, which alone selects one; or of the board at
   * target's point. It shows none of a node, or a connection, that the
   * board no longer has.
   */
  menu(target: MenuTarget, held = false): void {
    if (target.kind === "node") {
      if (!this.has(target.node)) {
        return;
      }
      if (!this.selected.has(target.node)) {
        this.select([target.node], "replace");
      }
    } else if (target.kind === "connection") {
      this.selectConnection(target.connection);
      if (this.connection !== target.connection) {
        return;
      }
    }
    this.events.menu(target, held);
  }

  /**
   * Has the page show the context menu of what is selected, as the keys
   * ask for it on the canvas (NFR-A11Y-04): of the connection selected, at
   * the middle of its line, of the nodes selected, at the middle of them,
   * or, of no selection, of the board, each at the canvas's centre where
   * its own point is not in view, so that the menu opens over the canvas.
   */
  menuOfSelection(): void {
    const view = visibleRect(this.v, this.sz);
    const shown = (p: Point): Point =>
      contains(view, { ...p, width: 0, height: 0 })
        ? p
        : centerOf(this.v, this.sz);
    if (this.connection !== undefined) {
      const item = this.scene.connections.get(this.connection);
      if (item !== undefined) {
        this.menu({
          kind: "connection",
          connection: this.connection,
          at: shown(middleOf(item.bounds)),
        });
        return;
      }
    }
    const bounds = this.scene.boundsOf(this.selected);
    const first = [...this.selected].find((id) => this.has(id));
    if (bounds !== undefined && first !== undefined) {
      this.menu({ kind: "node", node: first, at: shown(middleOf(bounds)) });
      return;
    }
    this.menu({ kind: "board", at: centerOf(this.v, this.sz) });
  }

  /**
   * Shows r of the board, as the rectangle of a node, or of a connection's
   * line, that the accessibility tree's focus is on, so that what marks it
   * on the canvas is in view: where the canvas does not show it whole, with
   * 24 px around it, it moves to have r's middle at its centre, at the zoom
   * that it is at, animated as a move that a key asks for is.
   */
  reveal(r: Rect): void {
    const v = this.target();
    const around = 24 / v.zoom;
    if (
      contains(visibleRect(v, this.sz), {
        x: r.x - around,
        y: r.y - around,
        width: r.width + 2 * around,
        height: r.height + 2 * around,
      })
    ) {
      return;
    }
    this.animateTo(anchorAt(middleOf(r), this.centre(), v.zoom));
  }

  /** Returns where r of the board is on the canvas, in its CSS pixels. */
  screenRectOf(r: Rect): Rect {
    const at = toScreen(this.v, r);
    return {
      x: at.x,
      y: at.y,
      width: r.width * this.v.zoom,
      height: r.height * this.v.zoom,
    };
  }

  /** Reports whether the scene has the node of id, as it has not once another session deleted it. */
  has(id: string): boolean {
    return this.scene.nodes.get(id) !== undefined;
  }

  /**
   * Reports whether the document has the node of id on the board, rather
   * than in the trash, as a drag that ends sends it, as it does not of one
   * that went meanwhile, as one that another session deleted, which the
   * server would refuse.
   */
  private alive(id: string): boolean {
    const node = this.docSeen.nodes.get(id);
    return node !== undefined && node.deleted_at === undefined;
  }

  /** Returns the connection of id that the document has on the board, rather than in the trash, or undefined. */
  private connectionOf(id: string) {
    const c = this.docSeen.connections.get(id);
    return c !== undefined &&
      c.deleted_at === undefined &&
      c.board_id === this.scene.boardId
      ? c
      : undefined;
  }

  /** Reports whether the document has the connection of id on the board, rather than in the trash. */
  private aliveConnection(id: string): boolean {
    return this.connectionOf(id) !== undefined;
  }

  /**
   * Starts moving the selection, which holds the node of id, or, where pick
   * says how, the selection that the node is first selected into, from
   * where the scene has its nodes, and reports whether it did: not where the
   * board may not be changed, nor once the node has gone, nor while another
   * drag is under way, whose nodes the move's would else take the place of,
   * nor of a node that is not selected without pick. A move that does not
   * start selects nothing.
   */
  moveStart(id: string, pick?: SelectHow): boolean {
    const held = this.scene.nodes.get(id);
    if (
      this.edits === undefined ||
      held === undefined ||
      this.drag !== undefined
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
    this.drag = {
      kind: "move",
      anchor: held.node.position,
      start,
      revs,
      places: new Map(),
    };
    this.lastMove = undefined;
    // A frame hides the resize handle while the move is under way, though
    // its first moves leave the nodes where they are, as on the grid.
    this.invalidate();
    return true;
  }

  /**
   * Moves the selection by delta of the board from where its move started:
   * a move that leaves each node where it is, as in each frame that the
   * gestures move the selection under a pointer that is still, by the delta
   * of the move before (lastMove), or one that the grid keeps in place,
   * changes nothing, and asks for no frame.
   */
  moveTo(delta: Point): void {
    const m = this.drag;
    if (m?.kind !== "move") {
      return;
    }
    const last = this.lastMove;
    if (
      last !== undefined &&
      last.delta.x === delta.x &&
      last.delta.y === delta.y &&
      last.snap === this.snap
    ) {
      return;
    }
    this.lastMove = { delta, snap: this.snap };
    const places = movedBy(m.start, m.anchor, delta, this.snap);
    if (samePlaces(places, m.places)) {
      return;
    }
    this.drag = { ...m, places };
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
    const m = this.drag;
    if (m?.kind !== "move") {
      return;
    }
    this.drag = undefined;
    if (commit) {
      // A node that went while it moved, as one that another session
      // deleted, does not move, which the server would refuse whole.
      const places = new Map([...m.places].filter(([id]) => this.alive(id)));
      if (places.size > 0) {
        this.edits?.move(places, m.revs);
      }
    }
    this.syncScene();
    this.tellUntold();
    // A frame of the move hid the resize handle, which a frame shows again,
    // though the move left the scene as it was, as one that came back where
    // it started, or never left it.
    this.invalidate();
  }

  /** Reports whether the selection is being moved, which the resize handle hides for. */
  moves(): boolean {
    return this.drag?.kind === "move";
  }

  /** Reports whether the node of id is being resized, whose handle stays shown, however small the resize draws the node. */
  resizes(id: string): boolean {
    return this.drag?.kind === "resize" && this.drag.id === id;
  }

  /**
   * Starts resizing the node of id by its handle, from its size as the
   * scene has it, and reports whether it did: not where the board may not
   * be changed, nor once the node has gone, nor while another drag is under
   * way, whose nodes its own would take the place of, nor once the node is
   * no longer the one selected, which alone has a handle, though the page
   * has yet to take the handle away, which ends its press (Gestures'
   * handleGone).
   */
  resizeStart(id: string): boolean {
    const item = this.scene.nodes.get(id);
    if (
      this.edits === undefined ||
      item === undefined ||
      this.drag !== undefined ||
      this.selected.size !== 1 ||
      !this.selected.has(id)
    ) {
      return false;
    }
    this.drag = {
      kind: "resize",
      id,
      rev: item.node.rev,
      from: { width: item.rect.width, height: item.rect.height },
    };
    return true;
  }

  /**
   * Resizes the node that the handle holds by delta of the board, the move
   * of the handle's pointer from the board's point where it went down, from
   * its size as its resize started, so that its corner stays under the
   * pointer as the board moves under it, as by the wheel, a key, or a pinch
   * of the trackpad.
   */
  resizeTo(delta: Point): void {
    const r = this.drag;
    if (r?.kind !== "resize") {
      return;
    }
    const size = resizedBy(r.from, delta, this.snap);
    if (size.width === r.size?.width && size.height === r.size.height) {
      return;
    }
    this.drag = { ...r, size };
    this.syncScene();
  }

  /** Ends the resize: the size that it left the node, which the page sends, expecting the node's revision as it started, or, cancelled, the size before. */
  resizeEnd(commit: boolean): void {
    const r = this.drag;
    if (r?.kind !== "resize") {
      return;
    }
    this.drag = undefined;
    if (
      commit &&
      r.size !== undefined &&
      (r.size.width !== r.from.width || r.size.height !== r.from.height) &&
      this.alive(r.id)
    ) {
      this.edits?.resize(r.id, r.size, r.rev);
    }
    this.syncScene();
    this.tellUntold();
  }

  /**
   * Starts the drag of link, by its handle, and reports whether it did: not
   * where the board may not be changed, nor while another drag is under
   * way, nor of a port that its node no longer has, as of an output deleted
   * meanwhile, or of a node that has gone, nor of a connection that has. It
   * ends the offer of the link before it.
   */
  linkStart(link: Link): boolean {
    if (this.edits === undefined || this.drag !== undefined) {
      return false;
    }
    let rev = 0;
    if (link.kind === "new") {
      const from = this.scene.nodes.get(link.node);
      if (from === undefined || !portsOf(from.node).includes(link.port)) {
        return false;
      }
    } else {
      const c = this.connectionOf(link.connection);
      if (c === undefined) {
        return false;
      }
      rev = c.rev;
    }
    this.endOffer();
    this.drag = { kind: "link", link, rev };
    this.invalidate();
    return true;
  }

  /**
   * Takes the end that the link's drag holds to at of the board: the node
   * there, the topmost, or the nearest within slop units, takes it, any
   * node the end that enters one, and an element or a branch the end that
   * leaves one, by its one port, or by the output of a branch whose row is
   * nearest, while a jumper, which no connection leaves, and a branch
   * without outputs, take none. Nothing takes it outside the canvas, as
   * over the inspector beside it, where what is under the pointer is not
   * shown, nor on the handle that it left (onOwnHandle), where it is let go
   * for nothing.
   */
  linkTo(at: Point, slop: number): void {
    const d = this.drag;
    if (d?.kind !== "link") {
      return;
    }
    const on = toScreen(this.v, at);
    const size = this.size();
    const idle =
      on.x < 0 ||
      on.y < 0 ||
      on.x > size.width ||
      on.y > size.height ||
      this.onOwnHandle(d.link, at);
    const id = idle ? undefined : this.scene.nodeAt(at, slop);
    const item = id === undefined ? undefined : this.scene.nodes.get(id);
    let over: { node: string; port?: string } | undefined;
    if (item !== undefined) {
      if (d.link.kind !== "from") {
        over = { node: item.node.id };
      } else {
        let nearest = Number.POSITIVE_INFINITY;
        for (const port of portsOf(item.node)) {
          const distance = Math.abs(
            portPoint(item.node, item.rect, port).y - at.y,
          );
          if (distance < nearest) {
            nearest = distance;
            over = { node: item.node.id, port };
          }
        }
      }
    }
    if (
      d.at?.x === at.x &&
      d.at.y === at.y &&
      d.over?.node === over?.node &&
      d.over?.port === over?.port &&
      (d.idle ?? false) === idle
    ) {
      return;
    }
    this.drag = { ...d, at, over, idle };
    this.invalidate();
  }

  /**
   * Reports whether at of the board is on the handle that link's drag left,
   * of a handle that told its side: within the square of that side, as the
   * canvas shows it, about the port of a connection to come, or about where
   * the end of a connection leaves its node, or enters it, where the drag
   * has not left the handle, or has come back to it. A finger's handle is
   * wider than the rows of a branch's outputs are apart, below 158%, so
   * that an end that a finger slipped on as it pressed its handle, and that
   * the nearest row then took, would else leave by the output beside its
   * own.
   */
  private onOwnHandle(link: Link, at: Point): boolean {
    if (link.side === undefined) {
      return false;
    }
    let point: Point | undefined;
    if (link.kind === "new") {
      const from = this.scene.nodes.get(link.node);
      point =
        from === undefined
          ? undefined
          : portPoint(from.node, from.rect, link.port);
    } else {
      const c = this.connectionOf(link.connection);
      if (c !== undefined && link.kind === "from") {
        const from = this.scene.nodes.get(c.from_node_id);
        point =
          from === undefined
            ? undefined
            : portPoint(from.node, from.rect, c.from_port);
      } else if (c !== undefined) {
        const to = this.scene.nodes.rect(c.to_node_id);
        point = to === undefined ? undefined : entryPoint(to);
      }
    }
    if (point === undefined) {
      return false;
    }
    const reach = link.side / 2 / this.v.zoom;
    return (
      Math.abs(at.x - point.x) <= reach && Math.abs(at.y - point.y) <= reach
    );
  }

  /**
   * Ends the link's drag: where it landed, which the page sends as a batch,
   * or, cancelled, as nothing. A connection to come that landed on a node
   * connects to it, and one that landed on the empty board has the page
   * offer to create a node there, whose line stays until the offer ends
   * (FR-EDIT-03 AC3); an end of a connection that landed on a node, or on a
   * port, other than its own is reconnected to it, expecting the
   * connection's revision as the drag started, and one that landed
   * elsewhere stays as it was. What went meanwhile, as a node that another
   * session deleted, connects nothing, and neither does a link let go where
   * nothing takes it (linkTo): outside the canvas, or on the handle that it
   * left, which offers nothing either.
   */
  linkEnd(commit: boolean): void {
    const d = this.drag;
    if (d?.kind !== "link") {
      return;
    }
    this.drag = undefined;
    this.invalidate();
    this.tellUntold();
    const edits = this.edits;
    if (!commit || edits === undefined || d.at === undefined || d.idle) {
      return;
    }
    const over =
      d.over !== undefined && this.alive(d.over.node) ? d.over : undefined;
    if (d.link.kind === "new") {
      const from = this.scene.nodes.get(d.link.node);
      if (
        from === undefined ||
        !this.alive(d.link.node) ||
        !portsOf(from.node).includes(d.link.port)
      ) {
        return;
      }
      const port = { node: d.link.node, port: d.link.port };
      if (over !== undefined) {
        edits.connect(port, over.node);
      } else if (d.over === undefined) {
        this.offered = { from: port, at: d.at };
        this.events.offer(this.offered);
      }
      return;
    }
    const c = this.connectionOf(d.link.connection);
    if (c === undefined || over === undefined) {
      return;
    }
    if (d.link.kind === "to") {
      if (over.node !== c.to_node_id) {
        edits.reconnect(c.id, { to: over.node }, d.rev);
      }
    } else if (
      over.port !== undefined &&
      (over.node !== c.from_node_id || over.port !== c.from_port)
    ) {
      edits.reconnect(
        c.id,
        { from: { node: over.node, port: over.port } },
        d.rev,
      );
    }
  }

  /** Reports whether a link's drag is under way, which the canvas's element says (data-linking). */
  links(): boolean {
    return this.drag?.kind === "link";
  }

  /** Ends the offer to create a node and connect it, whose line the canvas shows no longer, and tells the page, as once the user chose a node's type, or cancelled. */
  endOffer(): void {
    if (this.offered !== undefined) {
      this.offered = undefined;
      this.events.offer(undefined);
      this.invalidate();
    }
  }

  /**
   * Returns the line that the canvas draws of the link under way, or of the
   * page's offer: from the port that the connection leaves, or would, to
   * the node that it enters, or would, as a connection's curve is, with the
   * end that the drag holds at its pointer while nothing under it takes it.
   */
  private linkCurve(): Curve | undefined {
    const d = this.drag;
    const portOf = (p: PortOf): Point | undefined => {
      const item = this.scene.nodes.get(p.node);
      return item === undefined
        ? undefined
        : portPoint(item.node, item.rect, p.port);
    };
    const entryOf = (id: string): Point | undefined => {
      const rect = this.scene.nodes.rect(id);
      return rect === undefined ? undefined : entryPoint(rect);
    };
    let from: Point | undefined;
    let to: Point | undefined;
    let self = false;
    if (d?.kind === "link") {
      if (d.at === undefined) {
        return undefined;
      }
      const over = d.over;
      if (d.link.kind === "from") {
        const c = this.connectionOf(d.link.connection);
        if (c === undefined) {
          return undefined;
        }
        from =
          over?.port === undefined
            ? d.at
            : portOf({ node: over.node, port: over.port });
        to = entryOf(c.to_node_id);
        self = over?.node === c.to_node_id;
      } else {
        const c =
          d.link.kind === "to"
            ? this.connectionOf(d.link.connection)
            : undefined;
        const port =
          d.link.kind === "new"
            ? { node: d.link.node, port: d.link.port }
            : c === undefined
              ? undefined
              : { node: c.from_node_id, port: c.from_port };
        if (port === undefined) {
          return undefined;
        }
        from = portOf(port);
        to = over === undefined ? d.at : entryOf(over.node);
        self = over?.node === port.node;
      }
    } else if (this.offered !== undefined) {
      from = portOf(this.offered.from);
      to = this.offered.at;
    }
    return from === undefined || to === undefined
      ? undefined
      : curveOf(from, to, self);
  }

  /** Returns where p of the board is on the canvas, in its CSS pixels, as what the page places over a port reads it (follow). */
  screenPoint(p: Point): Point {
    return toScreen(this.v, p);
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
   * drag, which changes where nodes are, and none of their tags.
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
   * How many of the board's jumpers the canvas draws as broken (broken),
   * counted once for each change of the document's nodes or of what cards
   * show, rather than in each frame.
   */
  private brokenJumpers(): number {
    const c = this.brokenCount;
    const nodes = this.docSeen.nodes;
    if (
      c !== undefined &&
      c.nodes === nodes &&
      c.labels === this.labelsVersion
    ) {
      return c.count;
    }
    let count = 0;
    this.scene.nodes.forEach((_, item) => {
      if (item.node.type === "jumper" && this.broken(item.node)) {
        count++;
      }
    });
    this.brokenCount = { nodes, labels: this.labelsVersion, count };
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
    return r === undefined ? undefined : this.screenRectOf(r);
  };

  /**
   * Selects the node of id and shows it whole, as a link to it asks: once
   * the board's content has been read, in place of the fit or of the view
   * that the canvas restores, or at once, animated, on a canvas placed
   * already; and then calls found, as the page opens the node's inspector
   * then. It calls nothing for a node that the board does not have, which
   * the canvas then opens as it would without it.
   */
  focus(id: string, found?: () => void): void {
    if (!this.placed) {
      this.focusing = { id, found };
      this.invalidate();
      return;
    }
    if (this.has(id)) {
      this.select([id], "replace");
      this.zoomToSelection();
      found?.();
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
    // A selection of nodes takes the place of the connection selected, as
    // does the selection of none, of a tap on the empty board.
    if (how === "replace" || next.size > 0) {
      this.setConnection(undefined);
    }
    if (
      next.size !== this.selected.size ||
      [...next].some((id) => !this.selected.has(id))
    ) {
      this.setSelection(next);
    }
  }

  /**
   * Returns the ID of the connection whose line is within slop units of p
   * of the board, as the canvas draws it, a curve, or a straight line below
   * the zoom that shows text, on a board that may be changed, where a tap
   * selects one.
   */
  connectionAt(p: Point, slop: number): string | undefined {
    return this.edits === undefined
      ? undefined
      : this.scene.connectionAt(p, slop, this.lod === "blocks");
  }

  /**
   * Selects the connection of id, in place of the nodes selected, on a
   * board that may be changed and has it, or none.
   */
  selectConnection(id: string | undefined): void {
    if (
      id !== undefined &&
      (this.edits === undefined || !this.aliveConnection(id))
    ) {
      return;
    }
    if (id !== undefined && this.selected.size > 0) {
      this.setSelection(new Set());
    }
    this.setConnection(id);
  }

  /** The connection selected, or undefined for none. */
  selectedConnection(): string | undefined {
    return this.connection;
  }

  private setConnection(id: string | undefined): void {
    if (id === this.connection) {
      return;
    }
    this.connection = id;
    this.connectionKeptTo = this.docSeen.connections;
    this.selectionVersion++;
    this.events.connection(id);
    this.invalidate();
  }

  /**
   * Selects the nodes of ids, in place of the selection, as the page's
   * batch that creates them shows them, which may be after the scene next
   * changes, as the batch's mutation hands it to the document a moment
   * after it is asked for: the selection keeps them, though the scene has
   * them not yet, until done settles, rather than drop them as the scene
   * changes before the batch shows them (syncScene), and then drops those
   * that the scene has not, as of a batch refused, or of one that applied
   * but whose nodes went meanwhile, as once the user deleted them before
   * its answer came. Once done resolves with false, of a batch refused, a
   * selection of none but them is the one before them again, of the nodes
   * that the scene has, rather than none, while one that the user changed
   * meanwhile keeps what they selected; once it applied, it is never the
   * one before, which the user did not select again, and whose nodes a
   * Delete after it would else delete.
   */
  selectComing(ids: readonly string[], done: Promise<boolean>): void {
    const coming = new Set(ids);
    const before = this.selected;
    this.coming = coming;
    this.select([...ids], "replace");
    const settled = (applied: boolean) => {
      if (this.coming === coming) {
        this.coming = new Set();
      }
      const kept = [...this.selected].filter(
        (id) => !coming.has(id) || this.has(id) || this.coming.has(id),
      );
      if (!applied && kept.length === 0 && this.selected.size > 0) {
        this.setSelection(
          new Set(
            [...before].filter((id) => this.has(id) || this.coming.has(id)),
          ),
        );
      } else if (kept.length !== this.selected.size) {
        this.setSelection(new Set(kept));
      }
    };
    done.then(
      (applied) => settled(applied),
      () => settled(false),
    );
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
        // Escape ends a drag under way, cancelled, as a move that it would
        // else leave to land the nodes that it took out of the selection,
        // and leaves the selection as it is; else it is the canvas's while
        // there is a selection to clear, and else the page's, as of what
        // around the canvas closes with it.
        if (this.gestures.cancelDrag()) {
          return true;
        }
        const had = this.selected.size > 0 || this.connection !== undefined;
        this.select([], "replace");
        return had;
      }
      case "delete":
        if (this.edits === undefined) {
          return false;
        }
        if (this.connection !== undefined) {
          this.edits.removeConnection(this.connection);
          return true;
        }
        if (this.selected.size === 0) {
          return false;
        }
        this.edits.remove([...this.selected]);
        return true;
      case "open":
        if (this.edits === undefined) {
          return false;
        }
        if (this.connection !== undefined) {
          this.edits.openConnection(this.connection);
          return true;
        }
        if (this.selected.size !== 1) {
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
   * leaves, goes up, or is cancelled, goes to the gestures as it comes. A
   * pointer that went down on handle, the resize handle of a node, or a
   * link handle, is the canvas's too, and resizes the node, or links what
   * the handle holds, by its drag.
   */
  down(input: PointerInput, handle?: Handle): boolean {
    const followed = this.gestures.down(input, handle);
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
      const focusing = this.focusing;
      this.focusing = undefined;
      const focus =
        focusing === undefined ? undefined : this.scene.nodes.rect(focusing.id);
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
      if (focus !== undefined && focusing !== undefined) {
        this.select([focusing.id], "replace");
        focusing.found?.();
      } else if (saved !== undefined && this.selected.size === 0) {
        this.select(
          saved.selection.filter((id) => this.has(id)),
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
    // A rubber band, a move, or a resize under way stays on the board's
    // point where it started, and a link's end under its pointer.
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
        connection: this.connection,
        link: this.linkCurve(),
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
    this.lod = stats.lod;
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
      // The connection selected, or none, and whether a link handle's drag
      // is under way (plan note N69).
      connection: this.connection ?? "",
      linking: String(this.links()),
      // The nodes of the board that the tag filter dims (FR-EDIT-06 AC1).
      dimmed: String(this.dimmed()),
      // The board's jumpers that are drawn as broken (FR-EDIT-05 AC1).
      broken: String(this.brokenJumpers()),
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

  /** Stops the loop, until the canvas's elements are attached again, and ends every gesture, and so every drag, as cancelled. */
  stop(): void {
    this.stopped = true;
    if (this.pending !== 0) {
      this.clock.cancel(this.pending);
      this.pending = 0;
    }
    this.gestures.cancelAll();
  }
}
