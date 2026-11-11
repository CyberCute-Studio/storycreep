// The canvas's renderer (PRD ADR-013, FR-EDIT-01, FR-EDIT-11): PixiJS v8,
// on WebGL, or WebGPU, or, on a device that has neither, or only a software
// one, on its Canvas 2D renderer, with reduced limits. It draws the part of
// a board that the canvas shows, and a margin around it, of display objects
// that it keeps and reuses (culling): panning within the margin moves them
// as one and draws again, without building anything, and a node that comes
// into the margin takes the objects of one that left it. Every node's card
// is of sprites of a few shared textures, and its text of bitmap fonts,
// whose glyphs share an atlas, so that they batch into a few draw calls.
// Below 40% zoom a node is a block of one sprite, without text (level of
// detail, FR-EDIT-01 AC4), and a connection is a straight line.

import "pixi.js/unsafe-eval";
import type { Node } from "@storycreep/shared/api";
import {
  autoDetectRenderer,
  type BitmapFont,
  BitmapFontManager,
  BitmapText,
  CanvasPool,
  Container,
  Graphics,
  isWebGLSupported,
  NineSliceSprite,
  type Renderer,
  Sprite,
  Texture,
} from "pixi.js";
import { type NodeLabels, sameLabels } from "./labels";
import { type Curve, outputOffset, portPoint, sameCurve } from "./layout";
import type { ConnectionItem, NodeItem, Scene } from "./scene";
import {
  bodyLine,
  bodySize,
  bodyTop,
  clip,
  columnsOf,
  glyphSize,
  graphemes,
  kindSize,
  type Measure,
  textTier,
  titleSize,
  wrapLines,
} from "./text";
import { nodeColor, type Theme } from "./theme";
import {
  contains,
  crowdedAt,
  detailZoom,
  grow,
  intersects,
  type Rect,
  type Size,
  union,
  type Viewport,
  visibleRect,
} from "./viewport";

/** The renderers that PixiJS has, in the order that the canvas tries them. */
export type RendererKind = "webgl" | "webgpu" | "canvas";

/** What draws a frame of the canvas. */
export interface Frame {
  viewport: Viewport;
  size: Size;
  scene: Scene;
  selection: ReadonlySet<string>;
  /** Counts the changes of the selection, of the nodes or of the connection. */
  selectionVersion: number;
  /** The connection selected, whose line is drawn in the selection's colour, or undefined for none. */
  connection?: string;
  /** The line of a link under way, from a port to its pointer or to the node that would take it, or undefined for none (plan note N69). */
  link?: Curve;
  labels: (node: NodeItem) => NodeLabels;
  /** Counts the changes of what labels returns, as once a speaker's name comes, or the locale changes. */
  labelsVersion: number;
  /** Which nodes are drawn faint, as the tag filter dims them (FR-EDIT-06 AC1), or undefined for none. */
  dimmed?: (node: Node) => boolean;
  /** Counts the changes of dimmed. */
  dimVersion?: number;
  theme: Theme;
}

/** What a frame drew, which the page's tests and the benchmark read. */
export interface FrameStats {
  /** Whether nodes were cards with text, or blocks. */
  lod: "detail" | "blocks";
  /** The nodes that the renderer holds objects of: those that the canvas shows, and its margin's. */
  nodes: number;
  /** The nodes of those within the canvas. */
  visible: number;
  /** The texts that it draws. */
  texts: number;
  /** The connections that it draws. */
  connections: number;
}

/** The canvas's renderer, once it has started. */
export interface Engine {
  readonly kind: RendererKind;
  /** Sizes the canvas to size, in CSS pixels, at resolution device pixels to one. */
  resize(size: Size, resolution: number): void;
  /** Draws f, and says what it drew. */
  render(f: Frame): FrameStats;
  /**
   * How many textures the GPU holds, which panning must not grow without
   * bound (FR-EDIT-11 AC2), or undefined for the Canvas 2D renderer, which
   * keeps none there.
   */
  textures(): number | undefined;
  /**
   * Calls listener each time that the browser gives back the renderer's
   * WebGL context, or its GPU's device, that it took, as from a tablet's
   * tab in the background, or as the GPU's process went, which leaves the
   * canvas blank until it draws again, and returns what stops it. The
   * Canvas 2D renderer's context is never taken.
   */
  onRestore(listener: () => void): () => void;
  /** Ends the renderer, and takes its canvas element out of the page, whatever the renderer throws as it ends. */
  destroy(): void;
}

/** How the canvas picks its renderer. */
export interface EngineOptions {
  /**
   * The renderer to try first, or undefined for WebGL, then WebGPU, then
   * Canvas 2D. A WebGL that the browser runs in software, with a major
   * performance caveat, counts as none, as does a WebGPU of a fallback
   * adapter, so that the Canvas 2D renderer, which is faster on such a
   * device, draws, unless forced asks for it.
   */
  preference?: RendererKind;
  /**
   * Whether the preference is asked for whatever the device runs it in, as
   * the page's `?renderer=` asks: of it alone, so that a renderer that the
   * canvas falls back to, once the one asked for failed to start, counts a
   * software one as none still, as without a preference.
   */
  forced?: boolean;
  /** The font of the page, which the cards' text is in. */
  fontFamily: string;
  /**
   * What tells that the page no longer wants the renderer, as once its
   * address asks for another while this one starts: the start then tries
   * no renderer after the one under way, and adds no canvas after it.
   */
  signal?: AbortSignal;
}

/** How far around the canvas the renderer builds objects, in canvases: half of one on each side. */
const margin = 0.5;

/**
 * The tiles of the grid over the margin, in each direction, that the Canvas
 * 2D renderer's lines are of (cull): eight, so that a tile is a quarter of
 * the canvas, which a view of the margin overlaps 16 to 25 of the 64 of.
 */
const lineGrid = 8;

/** How far the zoom may go from the one that the margin was built at before it is built anew, so that lines and text keep their width on the screen. */
const zoomSlack = 1.25;

/** The corner of a card, in units of the board. */
const cornerRadius = 10;

/** How opaque a node that the tag filter dims is drawn (FR-EDIT-06 AC1). */
const dimAlpha = 0.3;

/**
 * Of each field of a node, whether a node moved has it as it was (movedOnly):
 * every field that its card shows, but its position, and none that no card
 * shows, as its revision, which the answer to a move's batch changes, and
 * which, kept, would write every card that the move moved anew in the frame
 * of the answer, though each field that a card shows is compared here. The
 * compiler holds each field of the API's node to be named here, so that a
 * field that the API adds, which a card might show, is not left out unseen.
 */
const keptByMove = {
  board_id: false,
  color: true,
  cover_asset_id: true,
  created_at: false,
  deleted_at: false,
  id: true,
  is_ending: true,
  kind: true,
  on_enter: false,
  on_exit: false,
  outputs: true,
  position: false,
  project_id: false,
  rev: false,
  size: true,
  speaker_component_id: true,
  tags: true,
  target: true,
  text: true,
  title: true,
  type: true,
  updated_at: false,
} satisfies Record<keyof Node, boolean>;

/** The fields that a node moved has as it was (keptByMove). */
const movedFields = (Object.keys(keptByMove) as (keyof Node)[]).filter(
  (field) => keptByMove[field],
);

/**
 * Reports whether node b is node a moved, as a drag of the selection moves
 * it (controller.ts), with every field of a that its card shows but its
 * position (keptByMove): a card of a that b is drawn again where b is,
 * rather than anew, so that a drag of hundreds of nodes does not write their
 * text again in each of its frames.
 */
function movedOnly(a: NodeItem, b: NodeItem): boolean {
  const x = a.node;
  const y = b.node;
  return (
    a.rect.width === b.rect.width &&
    a.rect.height === b.rect.height &&
    movedFields.every((field) => x[field] === y[field])
  );
}

/** How far a card's ring, 4 units outside it, and its ports, dots of 10 on its edges, reach beyond its node, in units of the board. */
const cardReach = 8;

/**
 * The free cards that a renderer keeps however few are in use (trimCards):
 * as many as a margin of a sparse board, at a zoom of 1, takes up and
 * frees as a pan goes from its nodes to the empty board beside them and
 * back, so that it builds none anew for that.
 */
const freeCardFloor = 256;

/**
 * How long a card stays free before the renderer may destroy it
 * (trimCards), in milliseconds: longer than a pinch out and back in, as the
 * benchmark's swing of 6 s between 25% and 150% (bench.tsx), so that a zoom
 * out after a zoom in takes back the cards that the zoom in freed, rather
 * than build them anew, with their sprites and their text's geometry, a
 * moment after it destroyed them.
 */
const freeCardHold = 10_000;

/**
 * The free cards that the renderer destroys at most in one frame
 * (trimCards): each of them destroys its sprites and its text's geometry,
 * so that a frame destroys this many at most, rather than all of the
 * thousands that a zoom in from a margin of a large screen just above
 * detailZoom frees at once, which would hold that frame up on the reference
 * tablet.
 */
const freeCardTrim = 128;

/**
 * The bytes of the glyphs' atlases that a renderer's fonts may take before
 * it draws them anew, of the text of its margin alone. A page of an atlas
 * is 512 pixels square at its font's resolution, of 1, 4, or 16 MB at the
 * text's tiers 1, 2, and 4 (textTier), and about as much again on the GPU,
 * and a font keeps each glyph that it drew, in more pages as they fill. The
 * Latin alphabet takes a page of each of the two fonts at each tier, 42 MB
 * at most with all three, under this, so that a board of it never draws
 * its fonts anew; but a page holds about 150 of the glyphs of Chinese or
 * Japanese, so that a pan over a board of either would else keep a glyph
 * of each character that it passed, as many as the board has, rather than
 * as its view shows (FR-EDIT-11), on a tablet in a few hundred megabytes.
 */
const glyphFloor = 64 * 2 ** 20;

/**
 * Counts the sets of fonts that renderers drew anew (glyphFloor), each of
 * names of its own, so that one renderer's, which it gives back, are not
 * those that another one still draws with: the renderers that start share
 * the first set, of 0, as one that starts in place of another does.
 */
let fontSets = 0;

/** The renderers in the order that the canvas tries them, without a preference. */
const renderers: readonly RendererKind[] = ["webgl", "webgpu", "canvas"];

/**
 * Whether the canvas has had PixiJS tell whether the page has WebGPU, which
 * it does as it starts its WebGPU renderer, and keeps for the page.
 */
let webGPUTold = false;

/**
 * Starts the renderer on a canvas element of its own, which it adds to
 * surface, or rejects where the browser has none, or once options' signal
 * tells that the page no longer wants it. It tries each renderer in
 * turn, each on a canvas of its own, as a canvas keeps the kind of context
 * that it gave first, so that one that a renderer failed to start on, as
 * where the GPU's process is gone, cannot give the next renderer its own.
 */
export async function createEngine(
  surface: HTMLElement,
  options: EngineOptions,
): Promise<Engine> {
  // Whether a renderer of kind counts one that the browser runs in
  // software as none: each but the one that the page asked for.
  const caveat = (kind: RendererKind) =>
    options.forced !== true || kind !== options.preference;
  const order =
    options.preference === undefined
      ? renderers
      : [
          options.preference,
          ...renderers.filter((k) => k !== options.preference),
        ];
  // PixiJS tells whether the page has WebGL, and whether it has WebGPU,
  // once, for the page, and keeps the answer, with the caveat that its
  // first caller asked with, so that a board that forces WebGL after one
  // that did not would find none, and one that does not after one that did
  // would start a WebGL that the browser runs in software, and fail, and
  // keeps that the page has none as well, as from a start while the GPU's
  // process was gone, which would leave every board after it, until the
  // page is loaded again, without either. The canvas tells whether the
  // browser has one, and whether its GPU runs it, itself, each time that
  // it starts, as PixiJS tells it, of WebGPU by a device of its adapter,
  // and has PixiJS tell only once it found one, without the caveat, as
  // PixiJS's own default does, so that what PixiJS keeps is that the page
  // has it: a renderer that the browser no longer has, after it, fails to
  // start, and gives way to the next. Once PixiJS has told of WebGPU, it
  // keeps that answer, whatever the canvas finds after, so that the canvas
  // asks its adapter for a device of its own only before then.
  for (const kind of order) {
    // A start that the page no longer wants, as once its address asks for
    // another renderer, tries none after, and neither probes the GPU nor
    // adds a canvas to the surface beside the one that the page wants.
    options.signal?.throwIfAborted();
    if (kind === "webgl") {
      if (!hasWebGL(caveat(kind))) {
        continue;
      }
      isWebGLSupported(false);
    }
    if (kind === "webgpu") {
      if (!(await hasWebGPU(caveat(kind), !webGPUTold))) {
        continue;
      }
      options.signal?.throwIfAborted();
      webGPUTold = true;
    }
    // Each canvas lies over the surface, rather than under the one before
    // it, so that a canvas of a start that the page no longer wants, which
    // stays until its renderer has started, and ends, does not push the
    // one that the page wants out of the host's view meanwhile.
    const canvas = document.createElement("canvas");
    canvas.setAttribute("aria-hidden", "true");
    canvas.className = "absolute inset-0 size-full";
    surface.append(canvas);
    let renderer: Renderer | undefined;
    try {
      renderer = await autoDetectRenderer({
        canvas,
        width: Math.max(1, canvas.clientWidth),
        height: Math.max(1, canvas.clientHeight),
        resolution: window.devicePixelRatio || 1,
        autoDensity: true,
        antialias: true,
        backgroundAlpha: 1,
        // This renderer alone, so that one that fails to start gives way
        // to the next here, on a canvas of its own.
        preference: [kind],
        failIfMajorPerformanceCaveat: caveat(kind),
        // The canvas brings what it draws with its own imports: PixiJS's
        // accessibility layer, which would add a button of its own to the
        // page on a tablet, and its events, are not the canvas's (item 63
        // gives the canvas its accessibility tree).
        manageImports: false,
      });
      return new PixiEngine(kind, renderer, canvas, options.fontFamily);
    } catch {
      // A renderer that started, but whose engine did not, ends, rather
      // than keep its context, or its GPU's device, while the next one
      // starts. One whose own start failed is autoDetectRenderer's, which
      // returns none to end: nothing of the page holds it once its canvas
      // is out of the page, and the browser collects it with the canvas.
      try {
        renderer?.destroy();
      } catch {
        // A renderer that ends half started has nothing more to give back.
      }
      canvas.remove();
    }
  }
  throw new Error("No renderer can draw the board in this browser");
}

/**
 * Reports whether the browser has a WebGL, as PixiJS asks for it, or, with
 * caveat, one that its GPU runs, rather than one that it runs in software,
 * with a major performance caveat, without keeping the answer.
 */
function hasWebGL(caveat: boolean): boolean {
  try {
    const gl = document.createElement("canvas").getContext("webgl", {
      stencil: true,
      failIfMajorPerformanceCaveat: caveat,
    });
    if (gl === null) {
      return false;
    }
    const stencil = gl.getContextAttributes()?.stencil === true;
    gl.getExtension("WEBGL_lose_context")?.loseContext();
    return stencil;
  } catch {
    return false;
  }
}

/**
 * What the canvas reads of a WebGPU adapter: whether it is a fallback
 * adapter, in its info, or, of a browser that has no such info yet, of
 * itself, as the page's types no longer have it.
 */
type AdapterOf = GPUAdapter & { isFallbackAdapter?: boolean };

/**
 * Reports whether the browser has a WebGPU adapter, or, with caveat, one
 * that its GPU runs, rather than a fallback adapter, which the browser may
 * run in software, with the performance caveat that a WebGL of the kind
 * has, so that a device without a GPU gets the Canvas 2D renderer
 * (ADR-013) by either, and, with device, whether the adapter gives a
 * device, as PixiJS asks of it as it tells whether the page has WebGPU,
 * without keeping the answer.
 */
async function hasWebGPU(caveat: boolean, device: boolean): Promise<boolean> {
  // A browser without WebGPU, or a page that is not of a secure context,
  // has no gpu, whatever the page's types say.
  const gpu: GPU | undefined = navigator.gpu;
  if (gpu === undefined) {
    return false;
  }
  try {
    const adapter: AdapterOf | null = await gpu.requestAdapter();
    if (
      adapter === null ||
      (caveat &&
        (adapter.info?.isFallbackAdapter === true ||
          adapter.isFallbackAdapter === true))
    ) {
      return false;
    }
    // PixiJS keeps that the page has no WebGPU once an adapter that it
    // asked for gave no device, which the canvas finds first, so that it
    // has PixiJS tell only of an adapter that gave the canvas its own,
    // which it ends at once. Once PixiJS has told, a device of the canvas's
    // own changes nothing that it keeps, and the renderer's start, which
    // asks for one, gives way to the next renderer where it gets none.
    if (device) {
      (await adapter.requestDevice()).destroy();
    }
    return true;
  } catch {
    return false;
  }
}

/** The objects of a node's card. */
interface CardView {
  root: Container;
  card: NineSliceSprite;
  edge: NineSliceSprite;
  stripe: Sprite;
  ring: NineSliceSprite;
  title: BitmapText;
  kind: BitmapText;
  body: BitmapText;
  ports: Sprite[];
  /** What the card shows, so that a card of the same node, at the same versions, is not drawn anew. */
  item?: NodeItem;
  /** Whether it shows its node selected. */
  selected?: boolean;
  /** The labels that it shows, so that a change of the names that cards show writes again only the cards that it changes. */
  labels?: NodeLabels;
  /** The version of the names that cards show that its labels were read at (Frame's labelsVersion). */
  labelsVersion?: number;
  /** When it was last freed (releaseCards), in milliseconds of the page's clock, which trimCards waits freeCardHold after. */
  freedAt: number;
}

/** A tile of the connections' lines: its Graphics, and the rectangle of the board that its lines take, or none for a tile without any. */
interface LineTile {
  g: Graphics;
  bounds: Rect | undefined;
}

class PixiEngine implements Engine {
  private readonly stage = new Container();
  /** The board, at the zoom, positioned from the origin of the margin. */
  private readonly world = new Container();
  /**
   * The connections' lines: on a GPU's renderer, one Graphics of every
   * connection of the margin, which it batches into a draw call or a few;
   * on the Canvas 2D renderer, which strokes each connection on its own,
   * a Graphics for each tile of a grid over the margin (lineGrid), of the
   * connections whose curves' centres are in it, so that it strokes those
   * of the tiles whose lines reach into the canvas alone (cull).
   */
  private readonly lines = new Container();
  private readonly lineTiles: LineTile[] = [];
  private readonly blocks = new Container();
  private readonly cards = new Container();
  /** The line of the link under way (Frame's link), above the cards, and what it was drawn of. */
  private readonly linkLine = new Graphics();
  private linkDrawn:
    | { curve: Curve; x: number; y: number; zoom: number; theme: Theme }
    | undefined;
  /** The textures that the cards are drawn of, white, which a tint colours. */
  private readonly art: {
    card: Texture;
    edge: Texture;
    ring: Texture;
    dot: Texture;
  };
  /** The blocks' sprites, in use and free. */
  private readonly blockSprites: Sprite[] = [];
  /** The cards in use, by their nodes' IDs, and those free. */
  private readonly cardViews = new Map<string, CardView>();
  private readonly freeCards: CardView[] = [];
  private built:
    | {
        region: Rect;
        origin: { x: number; y: number };
        zoom: number;
        lod: FrameStats["lod"];
        tier: number;
        sceneVersion: number;
        connectionsVersion: number;
        selectionVersion: number;
        connection: string | undefined;
        labelsVersion: number;
        dimVersion: number;
        theme: Theme;
        stats: Omit<FrameStats, "visible">;
        shown: NodeItem[];
        connections: ConnectionItem[];
      }
    | undefined;
  /** The fonts that this renderer installed, which it releases as it ends, or as their atlases outgrow glyphBudget. */
  private readonly fonts = new Set<string>();
  /** The set of fonts that this renderer draws with (fontSets). */
  private fontSet = 0;
  /**
   * The bytes of the fonts' atlases past which the next build of cards
   * draws the fonts anew: glyphFloor, or, once it has, twice what the
   * glyphs of its margin took then, so that a margin whose text needs more
   * than the floor draws its glyphs anew at most once for as many as it
   * drew since, rather than in each build.
   */
  private glyphBudget = glyphFloor;
  /** Whether the Canvas 2D renderer shows blocks for the nodes in view, which it does until fewer are than bring its cards back (crowdedAt). */
  private crowded = false;
  /** What onRestore calls. */
  private readonly restoreListeners = new Set<() => void>();
  /**
   * What PixiJS tells of a context, or a device, that the browser gave
   * back, once it has told its own systems, which give the GPU again what
   * it lost, as the textures and the lines' geometry, as they draw next.
   */
  private readonly restored = {
    contextChange: () => {
      for (const listener of this.restoreListeners) {
        listener();
      }
    },
  };

  constructor(
    /** The renderer that started, the only one that autoDetectRenderer was asked for. */
    readonly kind: RendererKind,
    private readonly renderer: Renderer,
    /** The canvas element that the renderer draws on, which it takes out of the page as it ends. */
    private readonly canvas: HTMLCanvasElement,
    private readonly fontFamily: string,
  ) {
    this.world.addChild(this.lines, this.blocks, this.cards, this.linkLine);
    this.linkLine.visible = false;
    this.stage.addChild(this.world);
    this.art = {
      card: canvasTexture(64, (g) => {
        g.beginPath();
        g.roundRect(0.5, 0.5, 63, 63, 16);
        g.fill();
      }),
      edge: canvasTexture(64, (g) => {
        g.lineWidth = 3;
        g.beginPath();
        g.roundRect(1.5, 1.5, 61, 61, 15);
        g.stroke();
      }),
      ring: canvasTexture(64, (g) => {
        g.lineWidth = 6;
        g.beginPath();
        g.roundRect(3, 3, 58, 58, 17);
        g.stroke();
      }),
      dot: canvasTexture(32, (g) => {
        g.beginPath();
        g.arc(16, 16, 14, 0, Math.PI * 2);
        g.fill();
      }),
    };
    renderer.runners.contextChange.add(this.restored);
  }

  onRestore(listener: () => void): () => void {
    this.restoreListeners.add(listener);
    return () => {
      this.restoreListeners.delete(listener);
    };
  }

  resize(size: Size, resolution: number): void {
    this.renderer.resize(
      Math.max(1, size.width),
      Math.max(1, size.height),
      resolution,
    );
  }

  textures(): number | undefined {
    // The textures that the GPU holds: the cards' four, the glyphs'
    // atlases, and the canvas's own. The Canvas 2D renderer keeps none.
    // PixiJS keeps the slot of each texture that it gave the GPU, and
    // empties it, rather than take it out, once the texture is destroyed,
    // as an atlas's page of a font drawn anew, or its GPU's copy is
    // collected, as of one unused for a while: only the slots that still
    // hold a texture count, so that a texture that is gone is not one that
    // the GPU holds, which would grow the count, as a leak would, with each
    // font drawn anew.
    const system = (
      this.renderer as unknown as {
        texture?: { managedTextures?: readonly unknown[] };
      }
    ).texture;
    const slots = system?.managedTextures;
    if (slots === undefined) {
      return undefined;
    }
    let held = 0;
    for (const t of slots) {
      if (t !== null && t !== undefined) {
        held++;
      }
    }
    return held;
  }

  render(f: Frame): FrameStats {
    const { viewport: v, size } = f;
    const visible = visibleRect(v, size);
    const b = this.built;
    // The glyphs' resolution, which stays a little as the zoom falls below
    // its tier (textTier), as every card of the margin is written anew
    // where it changes.
    const tier = textTier(v.zoom * this.renderer.resolution, b?.tier);
    // The nodes in view: of those that the renderer holds, while they are
    // the scene's as it is and its margin holds the canvas, as in each
    // frame that moves within it, and else of the scene itself, where the
    // Canvas 2D renderer needs them to choose its level of detail before
    // it builds the margin anew, rather than the nodes of the margin that
    // it built last, as of a board that was not read yet.
    let inView =
      b !== undefined &&
      b.sceneVersion === f.scene.version &&
      contains(b.region, visible)
        ? countIn(b.shown, visible)
        : undefined;
    if (inView === undefined && this.kind === "canvas") {
      inView = f.scene.countIn(visible);
    }
    if (this.kind === "canvas") {
      this.crowded = crowdedAt(v.zoom, inView ?? 0, this.crowded);
    }
    const lod: FrameStats["lod"] =
      v.zoom < detailZoom || this.crowded ? "blocks" : "detail";
    // A change of the text's resolution builds the margin anew where it
    // shows text, and not where it shows blocks, of none, as the Canvas 2D
    // renderer's of a crowded view, at a zoom that may cross a tier's edge.
    const moved =
      b === undefined ||
      b.lod !== lod ||
      (lod === "detail" && b.tier !== tier) ||
      v.zoom > b.zoom * zoomSlack ||
      v.zoom < b.zoom / zoomSlack ||
      !contains(b.region, visible);
    const stale =
      moved ||
      b.sceneVersion !== f.scene.version ||
      b.selectionVersion !== f.selectionVersion ||
      b.labelsVersion !== f.labelsVersion ||
      b.dimVersion !== (f.dimVersion ?? 0) ||
      b.theme !== f.theme;
    if (stale) {
      // A change of what the margin shows, but not of where it is, as of
      // the selection, builds it again where it was, so that its lines,
      // which the change left as they were, are not drawn anew.
      if (moved) {
        this.build(f, grow(visible, margin), v.zoom, lod, tier);
      } else {
        this.build(f, b.region, b.zoom, lod, tier);
      }
    }
    const built = this.built as NonNullable<typeof this.built>;
    this.trimCards(performance.now());
    this.world.scale.set(v.zoom);
    this.world.position.set(
      (built.origin.x - v.x) * v.zoom,
      (built.origin.y - v.y) * v.zoom,
    );
    if (this.kind === "canvas") {
      this.cull(visible);
    }
    this.drawLink(f.link, built.origin, v.zoom, f.theme);
    this.renderer.background.color = f.theme.canvas;
    this.renderer.render(this.stage);
    return {
      ...built.stats,
      visible: inView ?? countIn(built.shown, visible),
    };
  }

  /**
   * Draws, of the nodes of the margin, those in r alone, and of its lines,
   * those of the tiles whose lines reach into r, as the Canvas 2D renderer
   * draws each object that it is given on its own, each glyph of a card's
   * text and each connection's curve too, whether the canvas shows it or
   * not, since PixiJS culls nothing by itself: so the margin, which keeps
   * the canvas from building anything as it pans, costs it little to draw,
   * and it draws as many cards as there are nodes in view, which its limit
   * counts (canvasDetailLimit), rather than the four times as many that the
   * margin holds. A GPU's renderer draws the margin whole, in a few batches.
   */
  private cull(r: Rect): void {
    const built = this.built;
    if (built === undefined) {
      return;
    }
    // A card's ring and its ports reach a few units beyond its node, and a
    // line half its width beyond its curve, which is less than they do at
    // any zoom from 10%.
    const around = {
      x: r.x - cardReach,
      y: r.y - cardReach,
      width: r.width + 2 * cardReach,
      height: r.height + 2 * cardReach,
    };
    for (const tile of this.lineTiles) {
      tile.g.renderable =
        tile.bounds !== undefined && intersects(tile.bounds, around);
    }
    if (built.lod === "blocks") {
      built.shown.forEach((item, i) => {
        this.blockSprites[i].renderable = intersects(item.rect, r);
      });
      return;
    }
    for (const item of built.shown) {
      const view = this.cardViews.get(item.node.id);
      if (view !== undefined) {
        view.root.renderable = intersects(item.rect, around);
      }
    }
  }

  /** Builds the objects of what region of the board shows, at zoom, from its corner. */
  private build(
    f: Frame,
    region: Rect,
    zoom: number,
    lod: FrameStats["lod"],
    tier: number,
  ): void {
    const { scene, theme } = f;
    const was = this.built;
    // What the renderer built is told only once this build has finished:
    // one that throws partway, having freed the cards of the margin before
    // and taken them out of the stage, leaves the next frame to build the
    // margin whole, rather than draw what was built before without its
    // cards, where the view went back into it.
    this.built = undefined;
    const same =
      was !== undefined &&
      was.region === region &&
      was.sceneVersion === scene.version;
    const shown = same
      ? { nodes: was.shown, connections: was.connections }
      : scene.shown(region);
    const origin = { x: Math.floor(region.x), y: Math.floor(region.y) };
    const fresh =
      was === undefined ||
      was.tier !== tier ||
      was.theme !== theme ||
      was.lod !== lod;
    // A change of the names that cards show, as of the title of a node that
    // a jumper goes to, or of a component, reads each card's labels again,
    // and writes again only the cards whose labels it changed, as the few
    // jumpers to the node, rather than every card of the margin.
    const relabel = was !== undefined && was.labelsVersion !== f.labelsVersion;
    const now = performance.now();
    let texts = 0;
    if (lod === "blocks") {
      this.releaseCards(new Set(), now);
      // Every card is free, and out of the cards' list (orderCards).
      this.cards.removeChildren();
      this.cards.visible = false;
      this.blocks.visible = true;
      let i = 0;
      for (const item of shown.nodes) {
        let s = this.blockSprites[i];
        if (s === undefined) {
          s = new Sprite(Texture.WHITE);
          this.blockSprites.push(s);
          this.blocks.addChild(s);
        }
        s.visible = true;
        s.position.set(item.rect.x - origin.x, item.rect.y - origin.y);
        s.width = item.rect.width;
        s.height = item.rect.height;
        s.tint = f.selection.has(item.node.id)
          ? theme.accent
          : nodeColor(item.node.color, theme);
        s.alpha = f.dimmed?.(item.node) === true ? dimAlpha : 1;
        i++;
      }
      for (; i < this.blockSprites.length; i++) {
        this.blockSprites[i].visible = false;
      }
    } else {
      this.blocks.visible = false;
      this.cards.visible = true;
      const keep = new Set(shown.nodes.map((n) => n.node.id));
      this.releaseCards(keep, now);
      // Fonts whose atlases outgrew their budget, as of the glyphs of every
      // character of a board of Chinese that a pan passed, are given back,
      // of every tier, and drawn anew, as a set of their own, of the glyphs
      // of the margin's text alone, as every card of it is written anew.
      // The cards that are free keep the names of the fonts before, but are
      // out of the stage, and drawn with the new ones as they are used
      // again. PixiJS warns in the console, of each page of an atlas that it
      // destroys, that a batch that it keeps still held it, which is all
      // that it does: a batch of a page that is gone is never drawn again,
      // and no board of the Latin alphabet comes here.
      const anew = this.glyphBytes() > this.glyphBudget;
      if (anew) {
        this.releaseFonts();
        this.fontSet = ++fontSets;
      }
      const fonts = this.fontsOf(tier);
      const lines = this.kind === "canvas" ? 2 : 4;
      for (const item of shown.nodes) {
        let view = this.cardViews.get(item.node.id);
        if (view === undefined) {
          view = this.freeCards.pop() ?? this.newCard(fonts);
          this.cardViews.set(item.node.id, view);
        }
        const selected = f.selection.has(item.node.id);
        // A card of a node that only moved, as in each frame of a drag, is
        // where its node is now, as it was otherwise.
        if (
          !fresh &&
          !anew &&
          view.item !== undefined &&
          view.item !== item &&
          view.selected === selected &&
          movedOnly(view.item, item)
        ) {
          view.item = item;
        }
        const stale =
          fresh || anew || view.item !== item || view.selected !== selected;
        if (stale || relabel) {
          // A card's labels follow its node and the names that cards show
          // alone, so that a card drawn anew for the text's resolution, the
          // level of detail, the theme, or its selection, as every card of
          // the margin once a pinch crosses a resolution, keeps those that
          // it has, rather than read its node's text again.
          const labels =
            view.labels !== undefined &&
            view.item === item &&
            view.labelsVersion === f.labelsVersion
              ? view.labels
              : f.labels(item);
          if (stale || !sameLabels(labels, view.labels)) {
            this.drawCard(view, item, labels, f, fonts, selected, lines);
            view.item = item;
            view.selected = selected;
          }
          view.labels = labels;
          view.labelsVersion = f.labelsVersion;
        }
        view.root.position.set(item.rect.x - origin.x, item.rect.y - origin.y);
        view.root.alpha = f.dimmed?.(item.node) === true ? dimAlpha : 1;
        texts += view.body.visible ? 3 : 2;
      }
      this.orderCards(shown.nodes);
      if (anew) {
        this.glyphBudget = Math.max(glyphFloor, 2 * this.glyphBytes());
      }
    }
    // The lines are one stroke of every connection of the margin, or of a
    // tile's, drawn anew only once a connection, the margin, the zoom that
    // their width follows, the level of detail, or the theme changed, and
    // not for a change of a node alone, as of its title, which leaves its
    // connections' curves as they were.
    if (
      was === undefined ||
      was.region !== region ||
      was.connectionsVersion !== scene.connectionsVersion ||
      was.connection !== f.connection ||
      was.zoom !== zoom ||
      was.lod !== lod ||
      was.theme !== theme
    ) {
      this.drawLines(
        shown.connections,
        region,
        origin,
        zoom,
        lod,
        theme,
        f.connection,
      );
    }
    this.built = {
      region,
      origin,
      zoom,
      lod,
      tier,
      sceneVersion: scene.version,
      connectionsVersion: scene.connectionsVersion,
      selectionVersion: f.selectionVersion,
      connection: f.connection,
      labelsVersion: f.labelsVersion,
      dimVersion: f.dimVersion ?? 0,
      theme,
      stats: {
        lod,
        nodes: shown.nodes.length,
        texts,
        connections: shown.connections.length,
      },
      shown: shown.nodes,
      connections: shown.connections,
    };
  }

  /**
   * Makes the cards in use, in the order of nodes, the scene's, which they
   * are drawn in, the cards' list, in one pass, where they are not it
   * already, as once a free card is that of a node that came into the
   * margin, or once the margin freed some: rather than move each card to
   * its place on its own, which would look for it, and shift those after
   * it, among every card, for each of them. The free cards are out of the
   * list, so that neither a reorder nor the destruction of one of them
   * (trimCards) goes through those that the margin no longer shows.
   */
  private orderCards(nodes: readonly NodeItem[]): void {
    const roots = nodes.flatMap((item) => {
      const view = this.cardViews.get(item.node.id);
      return view === undefined ? [] : [view.root];
    });
    const children = this.cards.children;
    if (
      children.length === roots.length &&
      roots.every((root, i) => children[i] === root)
    ) {
      return;
    }
    this.cards.removeChildren();
    // PixiJS's addChild reads its first child without asking whether it
    // has one, and throws given none, as of a margin that holds no node
    // after one that held some, as past the board's content.
    if (roots.length > 0) {
      this.cards.addChild(...roots);
    }
  }

  /**
   * Destroys free cards beyond as many as are in use, or freeCardFloor, in
   * each frame, once the margin has taken those that it needs: so that the
   * cards that the renderer keeps come to follow what its margin shows in
   * detail, at most twice as many, rather than the most that it ever did,
   * until the board is left, as of the thousands of nodes of a margin of a
   * large screen at a zoom just above detailZoom, after a zoom in that
   * shows a few hundred, or after a zoom out to the blocks, each card with
   * its sprites and its text's geometry. It destroys only the cards that
   * have been free for freeCardHold, so that a zoom out or in again soon
   * after, as a pinch's swing, takes back those that the zoom before freed
   * rather than build them anew, and at most freeCardTrim of them in a
   * frame, so that the trim of thousands spreads over the frames after it
   * rather than hold one up. A canvas that draws no frame, as one that
   * nothing moves or changes, keeps them until it draws again, and does
   * nothing with them meanwhile. The cards free longest come first: the
   * free cards are a stack, which the margin takes the latest freed of,
   * and frees onto, so that they stay in the order that they were freed
   * in. A pan, which frees about as many cards as it takes, keeps those
   * that it frees.
   */
  private trimCards(now: number): void {
    const excess =
      this.freeCards.length - Math.max(freeCardFloor, this.cardViews.size);
    let n = 0;
    while (
      n < excess &&
      n < freeCardTrim &&
      now - this.freeCards[n].freedAt >= freeCardHold
    ) {
      n++;
    }
    for (const view of this.freeCards.splice(0, n)) {
      view.root.destroy({ children: true });
    }
  }

  /** Frees, at now, the cards of the nodes that keep does not hold, for nodes that come into the margin, which orderCards takes out of the cards' list. */
  private releaseCards(keep: ReadonlySet<string>, now: number): void {
    for (const [id, view] of this.cardViews) {
      if (!keep.has(id)) {
        this.cardViews.delete(id);
        view.item = undefined;
        view.labels = undefined;
        view.freedAt = now;
        this.freeCards.push(view);
      }
    }
  }

  private newCard(fonts: Fonts): CardView {
    const nine = (texture: Texture, inset: number) =>
      new NineSliceSprite({
        texture,
        leftWidth: inset,
        rightWidth: inset,
        topHeight: inset,
        bottomHeight: inset,
      });
    const text = (fontFamily: string, fontSize: number) =>
      new BitmapText({ text: "", style: { fontFamily, fontSize } });
    const view: CardView = {
      root: new Container(),
      card: nine(this.art.card, 20),
      edge: nine(this.art.edge, 20),
      stripe: new Sprite(Texture.WHITE),
      ring: nine(this.art.ring, 22),
      title: text(fonts.bold, titleSize),
      kind: text(fonts.regular, kindSize),
      body: text(fonts.regular, bodySize),
      ports: [],
      freedAt: 0,
    };
    view.root.addChild(
      view.card,
      view.edge,
      view.stripe,
      view.title,
      view.kind,
      view.body,
      view.ring,
    );
    this.cards.addChild(view.root);
    return view;
  }

  private drawCard(
    view: CardView,
    item: NodeItem,
    labels: NodeLabels,
    f: Frame,
    fonts: Fonts,
    selected: boolean,
    lines: number,
  ): void {
    const { theme } = f;
    const { width: w, height: h } = item.rect;
    const node = item.node;
    view.root.visible = true;
    // The textures' corners are drawn at 16 of 64 pixels; a card's are
    // cornerRadius of the board's units.
    const corners = (s: NineSliceSprite, inset: number) => {
      const scale = cornerRadius / 16;
      s.scale.set(scale);
      s.width = (w + 2 * inset) / scale;
      s.height = (h + 2 * inset) / scale;
      s.position.set(-inset, -inset);
    };
    corners(view.card, 0);
    view.card.tint = theme.surface;
    corners(view.edge, 0);
    // A broken jumper's edge, and its line that says so, are of the colour
    // of what is wrong (FR-EDIT-05 AC1).
    view.edge.tint = labels.broken
      ? theme.danger
      : nodeColor(node.color, theme);
    view.stripe.visible = node.color !== "none";
    view.stripe.tint = nodeColor(node.color, theme);
    view.stripe.position.set(cornerRadius, 0);
    view.stripe.width = Math.max(0, w - 2 * cornerRadius);
    view.stripe.height = 4;
    corners(view.ring, 4);
    view.ring.visible = selected;
    view.ring.tint = theme.accent;
    // The room of a line, within the card's padding, which each character
    // takes the width of that the card's font draws it at (fitted), but at
    // least that of four average Latin letters.
    const inner = Math.max(8, w - 24);
    const cut = (
      font: string,
      size: number,
      text: (room: number, measure: Measure) => string,
    ) =>
      fitted(installed.get(font)?.font, size, (measure) =>
        text(Math.max(inner, 4 * averageLetter * size), measure),
      );
    const setText = (
      t: BitmapText,
      fontFamily: string,
      value: string,
      color: number,
      x: number,
      y: number,
    ) => {
      if (t.style.fontFamily !== fontFamily) {
        t.style.fontFamily = fontFamily;
      }
      if (t.text !== value) {
        t.text = value;
      }
      t.tint = color;
      t.position.set(x, y);
    };
    setText(
      view.title,
      fonts.bold,
      cut(fonts.bold, titleSize, (room, m) => clip(labels.title, room, m)),
      theme.fg,
      12,
      12,
    );
    setText(
      view.kind,
      fonts.regular,
      cut(fonts.regular, kindSize, (room, m) => clip(labels.kind, room, m)),
      theme.muted,
      12,
      32,
    );
    const branch = node.type === "branch";
    const outputs = node.outputs ?? [];
    // A branch's outputs, each on its row, or, on the row of a first one,
    // the line that says that it has none; an element's text, or a
    // jumper's target, in the lines that the card has room for under its
    // title and its type, which is one in a jumper's 72 units.
    const rows = Math.max(1, outputs.length);
    const bodyLines = branch
      ? rows
      : Math.min(lines, Math.floor((h - bodyTop - 4) / bodyLine));
    view.body.visible = labels.body !== "" && bodyLines > 0;
    if (view.body.visible) {
      const text = cut(fonts.regular, bodySize, (room, m) =>
        wrapLines(labels.body, room, bodyLines, branch, m),
      );
      const lineHeight = branch
        ? outputOffset(1, rows, h) - outputOffset(0, rows, h)
        : bodyLine;
      if (view.body.style.lineHeight !== lineHeight) {
        view.body.style.lineHeight = lineHeight;
      }
      setText(
        view.body,
        fonts.regular,
        text,
        labels.broken ? theme.danger : branch ? theme.muted : theme.fg,
        12,
        branch ? outputOffset(0, rows, h) - lineHeight / 2 : bodyTop,
      );
    }
    // A port is a dot on the card's edge: the entry, on the left, and each
    // port that connections leave, on the right.
    const ports = [{ x: 0, y: h / 2 }];
    if (node.type === "element") {
      ports.push({ x: w, y: h / 2 });
    } else if (branch) {
      for (const o of outputs) {
        const p = portPoint(node, { x: 0, y: 0, width: w, height: h }, o.id);
        ports.push(p);
      }
    }
    for (let i = 0; i < Math.max(ports.length, view.ports.length); i++) {
      let dot = view.ports[i];
      if (i >= ports.length) {
        dot.visible = false;
        continue;
      }
      if (dot === undefined) {
        dot = new Sprite(this.art.dot);
        dot.anchor.set(0.5);
        dot.width = 10;
        dot.height = 10;
        view.ports.push(dot);
        view.root.addChild(dot);
      }
      dot.visible = true;
      dot.tint = theme.border;
      dot.position.set(ports[i].x, ports[i].y);
    }
  }

  /**
   * Draws the lines of connections, of the margin that is region, from its
   * origin: on a GPU's renderer, into one Graphics; on the Canvas 2D
   * renderer, each into the tile of the grid over region that the centre of
   * its curve is in, with the rectangle that the tile's lines take, by
   * which cull draws the tile or not. The connection of selected is drawn
   * again over the rest, wider, in the selection's colour.
   */
  private drawLines(
    connections: readonly ConnectionItem[],
    region: Rect,
    origin: { x: number; y: number },
    zoom: number,
    lod: FrameStats["lod"],
    theme: Theme,
    selected: string | undefined,
  ): void {
    const grid = this.kind === "canvas" ? lineGrid : 1;
    while (this.lineTiles.length < grid * grid) {
      const g = new Graphics();
      this.lines.addChild(g);
      this.lineTiles.push({ g, bounds: undefined });
    }
    const tiles = this.lineTiles.map((): ConnectionItem[] => []);
    // The tile's column or row of a centre at p, of the region from start
    // that is length long.
    const cell = (p: number, start: number, length: number) =>
      Math.min(
        grid - 1,
        Math.max(0, Math.floor(((p - start) / Math.max(1, length)) * grid)),
      );
    for (const item of connections) {
      const b = item.bounds;
      const i =
        grid === 1
          ? 0
          : cell(b.y + b.height / 2, region.y, region.height) * grid +
            cell(b.x + b.width / 2, region.x, region.width);
      tiles[i].push(item);
    }
    const width = 1.5 / zoom;
    this.lineTiles.forEach((tile, i) => {
      const g = tile.g;
      g.clear();
      const items = tiles[i];
      tile.bounds = union(items.map((item) => item.bounds));
      g.visible = items.length > 0;
      if (items.length === 0) {
        return;
      }
      const trace = ({ curve: c }: ConnectionItem) => {
        g.moveTo(c.p0.x - origin.x, c.p0.y - origin.y);
        if (lod === "blocks") {
          g.lineTo(c.p3.x - origin.x, c.p3.y - origin.y);
        } else {
          g.bezierCurveTo(
            c.c1.x - origin.x,
            c.c1.y - origin.y,
            c.c2.x - origin.x,
            c.c2.y - origin.y,
            c.p3.x - origin.x,
            c.p3.y - origin.y,
          );
        }
      };
      // An arrowhead where a connection enters its node.
      const head = ({ curve: c }: ConnectionItem) => {
        const x = c.p3.x - origin.x;
        const y = c.p3.y - origin.y;
        g.poly([x, y, x - 9, y - 5, x - 9, y + 5]);
      };
      const heads = lod === "detail" && this.kind !== "canvas";
      for (const item of items) {
        trace(item);
      }
      g.stroke({ width, color: theme.border, alpha: 0.9 });
      if (heads) {
        for (const item of items) {
          head(item);
        }
        g.fill({ color: theme.border });
      }
      const chosen =
        selected === undefined
          ? undefined
          : items.find((item) => item.connection.id === selected);
      if (chosen !== undefined) {
        trace(chosen);
        g.stroke({ width: 2 * width, color: theme.accent });
        if (heads) {
          head(chosen);
          g.fill({ color: theme.accent });
        }
      }
    });
  }

  /**
   * Draws the line of the link under way, curve, from origin, the margin's,
   * at zoom, or none: anew only once it, or what it is drawn from, changed.
   */
  private drawLink(
    curve: Curve | undefined,
    origin: { x: number; y: number },
    zoom: number,
    theme: Theme,
  ): void {
    const g = this.linkLine;
    const was = this.linkDrawn;
    if (curve === undefined) {
      if (was !== undefined) {
        g.clear();
        g.visible = false;
        this.linkDrawn = undefined;
      }
      return;
    }
    if (
      was !== undefined &&
      sameCurve(was.curve, curve) &&
      was.x === origin.x &&
      was.y === origin.y &&
      was.zoom === zoom &&
      was.theme === theme
    ) {
      return;
    }
    g.clear();
    g.visible = true;
    g.moveTo(curve.p0.x - origin.x, curve.p0.y - origin.y);
    g.bezierCurveTo(
      curve.c1.x - origin.x,
      curve.c1.y - origin.y,
      curve.c2.x - origin.x,
      curve.c2.y - origin.y,
      curve.p3.x - origin.x,
      curve.p3.y - origin.y,
    );
    g.stroke({ width: 3 / zoom, color: theme.accent });
    this.linkDrawn = { curve, x: origin.x, y: origin.y, zoom, theme };
  }

  /** Returns the fonts of tier, of this renderer's set (fontSet), installing them once. */
  private fontsOf(tier: number): Fonts {
    const fonts = {
      regular: `storycreep-regular-${tier}-${this.fontSet}`,
      bold: `storycreep-bold-${tier}-${this.fontSet}`,
    };
    for (const [name, weight] of [
      [fonts.regular, "400"],
      [fonts.bold, "600"],
    ] as const) {
      if (!this.fonts.has(name)) {
        this.fonts.add(name);
        const had = installed.get(name);
        if (had !== undefined) {
          had.users++;
          continue;
        }
        const font = BitmapFontManager.install({
          name,
          style: {
            fontFamily: this.fontFamily,
            fontSize: glyphSize,
            fontWeight: weight,
            fill: 0xffffff,
          },
          resolution: tier,
          // The glyphs of the Latin alphabet at once, and the ellipsis
          // that a cut line ends with, and those of any other script as
          // the first text that has them comes.
          chars: [[" ", "~"], "…"],
        });
        installed.set(name, { users: 1, font });
      }
    }
    return fonts;
  }

  /** The bytes of the atlases of the fonts that this renderer installed, of four to a pixel. */
  private glyphBytes(): number {
    let bytes = 0;
    for (const name of this.fonts) {
      for (const page of installed.get(name)?.font.pages ?? []) {
        const source = page.texture.source;
        bytes += source.pixelWidth * source.pixelHeight * 4;
      }
    }
    return bytes;
  }

  /**
   * Gives back the fonts that this renderer installed: the last renderer
   * that drew with one uninstalls it, with its atlases.
   */
  private releaseFonts(): void {
    let uninstalled = false;
    for (const name of this.fonts) {
      const had = installed.get(name);
      if (had !== undefined && had.users > 1) {
        had.users--;
      } else {
        installed.delete(name);
        BitmapFontManager.uninstall(name);
        uninstalled = true;
      }
    }
    this.fonts.clear();
    // A font that PixiJS uninstalls gives the GPU back its pages, but puts
    // the canvas of each, of 1, 4, or 16 MB, back into PixiJS's pool of
    // canvases for the page, CanvasPool, which keeps every one until the
    // page is loaded again: of each tier, as many pages as its fonts ever
    // had at once, which, of a board of Chinese, come to more than
    // glyphFloor together, and, of any board, after the canvas has ended.
    // The pool is emptied of those idle canvases, which the browser then
    // collects, so that the fonts drawn anew take as many as the margin's
    // text needs, and that a page without the canvas keeps none. A canvas
    // in use, as of another renderer's font, is not in the pool, and stays
    // its own.
    if (uninstalled) {
      CanvasPool.clear();
    }
  }

  destroy(): void {
    this.renderer.runners.contextChange.remove(this.restored);
    this.restoreListeners.clear();
    this.stage.destroy({ children: true });
    // The free cards, which are out of the stage (orderCards).
    for (const view of this.freeCards.splice(0)) {
      view.root.destroy({ children: true });
    }
    for (const t of Object.values(this.art)) {
      t.destroy(true);
    }
    this.releaseFonts();
    // A renderer whose context, or whose GPU's device, is lost may throw as
    // it gives back what it held, which the browser collects with its
    // canvas: the canvas leaves the page all the same, and the page, which
    // ends the renderer from a promise's handler, or as React unmounts the
    // canvas, hears nothing of it.
    try {
      this.renderer.destroy();
    } catch {
      // A renderer that ends without its context has nothing more to give back.
    }
    this.canvas.remove();
  }
}

/**
 * What the canvas reads of a bitmap font that it installed, and asks of it:
 * the width that it draws each character that it has at, in the units of
 * its size of measurement, the pages of its atlas, whose bytes it counts
 * (glyphFloor), and the glyphs of more.
 */
type CardFont = Pick<
  BitmapFont,
  "chars" | "baseMeasurementFontSize" | "pages"
> & {
  ensureCharacters?: (chars: string) => void;
};

/**
 * The bitmap fonts that the renderers installed, which PixiJS keeps for the
 * page, by their names, each with how many renderers use it: the last to
 * end uninstalls it.
 */
const installed = new Map<string, { users: number; font: CardFont }>();

/** An average Latin letter's width, of the font's size, of which text.ts's columns are. */
const averageLetter = 0.56;

/**
 * Returns the text that cut makes of a card's label, at size, by the width
 * that font draws each of its characters at, or, without the font, or for a
 * character that it has not drawn yet, by text.ts's columns, so that a
 * line of capitals, or of a bold font's wide letters, stays within its
 * card. The font draws the glyphs of the characters that the cut shows
 * then, as the text's layout would as it shows it, and the label is cut
 * again by their widths, at most twice, so that a character of a script
 * beyond the Latin alphabet's, which the font draws once a text first has
 * it, is cut by its width too.
 */
function fitted(
  font: CardFont | undefined,
  size: number,
  cut: (measure: Measure) => string,
): string {
  const column = averageLetter * size;
  if (font === undefined) {
    return cut((ch) => columnsOf(ch) * column);
  }
  const scale = size / font.baseMeasurementFontSize;
  const measure: Measure = (ch) => {
    const glyph = font.chars[ch];
    return glyph === undefined
      ? columnsOf(ch) * column
      : glyph.xAdvance * scale;
  };
  let text = cut(measure);
  for (let pass = 0; pass < 2 && !drawnIn(font, text); pass++) {
    font.ensureCharacters?.(text);
    text = cut(measure);
  }
  return text;
}

/**
 * Reports whether font has drawn each character of text, but its line
 * breaks, as a reader sees them, of a code point or more, as the font
 * keeps its glyphs (graphemes): a letter and its accent, or an emoji of a
 * family, is one glyph of the font, never a glyph of each of its code
 * points, which the font would never have, and the label would be cut
 * again, twice, each time that its card is drawn.
 */
function drawnIn(font: CardFont, text: string): boolean {
  for (const ch of graphemes(text)) {
    if (ch !== "\n" && font.chars[ch] === undefined) {
      return false;
    }
  }
  return true;
}

/** The fonts of a tier: a card's regular text, and its title's. */
interface Fonts {
  regular: string;
  bold: string;
}

/** Returns how many of nodes are in r. */
function countIn(nodes: readonly NodeItem[], r: Rect): number {
  let n = 0;
  for (const item of nodes) {
    if (intersects(item.rect, r)) {
      n++;
    }
  }
  return n;
}

/** Returns a white texture of size pixels square, drawn by draw on a Canvas 2D context. */
function canvasTexture(
  size: number,
  draw: (g: CanvasRenderingContext2D) => void,
): Texture {
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const g = canvas.getContext("2d");
  if (g !== null) {
    g.fillStyle = "#ffffff";
    g.strokeStyle = "#ffffff";
    draw(g);
  }
  return Texture.from(canvas);
}
