// What drives a board's canvas (PRD FR-EDIT-01, FR-EDIT-11): the viewport,
// which the gestures, the keys, and the toolbar change, the selection, the
// scene of the board, and the loop that draws a frame once something
// changed, and only then, so that a canvas that nothing moves draws
// nothing. A zoom or a move that the toolbar or a key asks for is animated,
// briefly, unless the system asks for reduced motion (NFR-A11Y-07), as the
// canvas's only animation; a gesture follows the fingers at once.

import type { Doc } from "../document/doc";
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
import type { Theme } from "./theme";
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
  type Viewport,
  zoomAt,
} from "./viewport";

/** A scripted run of the canvas that the benchmark harness measures (bench.tsx). */
export interface BenchOptions {
  scenario: "pan" | "panzoom" | "load";
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
  /** When the frame began, as requestAnimationFrame gives it. */
  time: number;
  /** How long building and drawing it took the page, in milliseconds. */
  work: number;
  stats: FrameStats;
}

/** What the canvas tells the page of. */
export interface CanvasEvents {
  /** The selection changed. */
  selection(ids: ReadonlySet<string>): void;
  /** The zoom changed, by a percent or more. */
  zoom(zoom: number): void;
  /** The first frame of the board's content was drawn. */
  rendered(): void;
}

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
  private v: Viewport = { x: 0, y: 0, zoom: 1 };
  private sz: Size = { width: 0, height: 0 };
  private resolution = 1;
  private engine: Engine | undefined;
  private minimap: Minimap | undefined;
  private elements: CanvasElements | undefined;
  private selected: ReadonlySet<string> = new Set();
  private selectionVersion = 0;
  private labelsVersion = 0;
  private theme: Theme | undefined;
  private modeValue: Mode = "pan";
  private animation:
    | { from: Viewport; to: Viewport; start: number }
    | undefined;
  private pending = 0;
  /** Whether the loop stopped, as the canvas went from the page, so that nothing asks for a frame after. */
  private stopped = false;
  /** Whether the board's content has been read, and whether the canvas has been placed on it once. */
  private ready = false;
  private placed = false;
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
    this.engine = engine;
    engine?.resize(this.sz, this.resolution);
    this.lastData = {};
    this.invalidate();
  }

  setMinimap(minimap: Minimap | undefined): void {
    this.minimap = minimap;
    this.invalidate();
  }

  setTheme(theme: Theme): void {
    this.theme = theme;
    this.invalidate();
  }

  /** Takes the canvas's size, in CSS pixels, keeping the board's point at its centre where it is. */
  setSize(size: Size, resolution: number): void {
    if (
      size.width === this.sz.width &&
      size.height === this.sz.height &&
      resolution === this.resolution
    ) {
      return;
    }
    if (this.placed && this.sz.width > 0 && this.sz.height > 0) {
      const center = centerOf(this.v, this.sz);
      this.v = anchorAt(
        center,
        { x: size.width / 2, y: size.height / 2 },
        this.v.zoom,
      );
    }
    this.sz = size;
    this.resolution = resolution;
    const engine = this.engine;
    if (engine === undefined) {
      this.invalidate();
      return;
    }
    // Resizing the renderer clears its canvas, which the browser would show
    // so, as a frame of nothing, before the next frame drew it: the canvas
    // draws at once, as the browser tells of its new size before it paints.
    engine.resize(size, resolution);
    this.drawNow();
  }

  /**
   * Takes what the project's document holds of the board, and whether the
   * names of its components or variables, which cards show, changed.
   */
  sync(doc: Doc, namesChanged: boolean): void {
    const targets = this.scene.targetsVersion;
    const changed = this.scene.sync(doc);
    const relabel = namesChanged || this.scene.targetsVersion !== targets;
    if (relabel) {
      // A card names components, variables, and, a jumper's, the node that
      // it goes to, which may have changed without it: every card is
      // written anew. A card of a node that changed itself is written anew
      // alone.
      this.labelsVersion++;
    }
    if (changed) {
      const kept = [...this.selected].filter(
        (id) => this.scene.nodes.get(id) !== undefined,
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

  // The gestures' host.

  viewport(): Viewport {
    return this.v;
  }

  size(): Size {
    return this.sz;
  }

  setViewport(v: Viewport): void {
    this.animation = undefined;
    this.placed = true;
    if (!sameViewport(v, this.v)) {
      this.v = v;
      this.invalidate();
    }
  }

  mode(): Mode {
    return this.modeValue;
  }

  setMode(mode: Mode): void {
    this.modeValue = mode;
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

  private setSelection(ids: ReadonlySet<string>): void {
    this.selected = ids;
    this.selectionVersion++;
    this.events.selection(ids);
    this.invalidate();
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

  /** Moves to v, animated unless the system asks for reduced motion. */
  animateTo(v: Viewport): void {
    this.placed = true;
    if (this.reducedMotion || this.sz.width === 0) {
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
    this.animateTo(
      zoomAt(
        v,
        { x: this.sz.width / 2, y: this.sz.height / 2 },
        stepZoom(v.zoom, direction),
      ),
    );
  }

  /** Zooms to zoom about the canvas's centre. */
  zoomTo(zoom: number): void {
    const v = this.target();
    this.animateTo(
      zoomAt(
        v,
        { x: this.sz.width / 2, y: this.sz.height / 2 },
        clampZoom(zoom),
      ),
    );
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

  /** Centres the canvas on p of the board, at once, as the minimap does. */
  centerOn(p: Point): void {
    this.setViewport(
      anchorAt(p, { x: this.sz.width / 2, y: this.sz.height / 2 }, this.v.zoom),
    );
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
      case "deselect":
        this.select([], "replace");
        return true;
    }
  }

  /** Takes a pointer's event of the browser's, at a point of the canvas. */
  pointer(
    type: "down" | "move" | "up" | "cancel",
    input: PointerInput,
  ): boolean {
    switch (type) {
      case "down":
        this.animation = undefined;
        return this.gestures.down(input);
      case "move":
        this.gestures.move(input);
        return true;
      case "up":
        this.gestures.up(input);
        return true;
      case "cancel":
        this.gestures.cancel(input);
        return true;
    }
  }

  // The loop.

  /** Asks for a frame, once. */
  invalidate(): void {
    if (this.pending === 0 && !this.stopped) {
      this.pending = this.clock.request(this.frame);
    }
  }

  /** Draws a frame at once, in place of the one asked for. */
  private drawNow(): void {
    if (this.stopped) {
      return;
    }
    if (this.pending !== 0) {
      this.clock.cancel(this.pending);
      this.pending = 0;
    }
    this.frame(this.clock.now());
  }

  private readonly frame = (time: number): void => {
    this.pending = 0;
    const size = this.sz;
    if (this.ready && !this.placed && size.width > 0 && size.height > 0) {
      this.v = this.fitted();
      this.placed = true;
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
    this.showZoom();
    const engine = this.engine;
    const theme = this.theme;
    if (
      engine === undefined ||
      theme === undefined ||
      size.width === 0 ||
      size.height === 0
    ) {
      return;
    }
    const start = this.clock.now();
    const stats = engine.render({
      viewport: this.v,
      size,
      scene: this.scene,
      selection: this.selected,
      selectionVersion: this.selectionVersion,
      labels: this.labels,
      labelsVersion: this.labelsVersion,
      theme,
    });
    const work = this.clock.now() - start;
    this.minimap?.draw(this.scene, this.v, size, theme);
    this.writeData(engine.kind, stats);
    if (this.ready && !this.renderedOnce) {
      this.renderedOnce = true;
      this.events.rendered();
    }
    for (const listener of this.listeners) {
      listener({ time, work, stats });
    }
  };

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
  }
}
