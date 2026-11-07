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
import {
  autoDetectRenderer,
  type BitmapFont,
  BitmapFontManager,
  BitmapText,
  Container,
  Graphics,
  isWebGLSupported,
  NineSliceSprite,
  type Renderer,
  Sprite,
  Texture,
} from "pixi.js";
import { type NodeLabels, sameLabels } from "./labels";
import { outputOffset, portPoint } from "./layout";
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
  /** Counts the changes of the selection. */
  selectionVersion: number;
  labels: (node: NodeItem) => NodeLabels;
  /** Counts the changes of what labels returns, as once a speaker's name comes, or the locale changes. */
  labelsVersion: number;
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
  /** Ends the renderer, and takes its canvas element out of the page. */
  destroy(): void;
}

/** How the canvas picks its renderer. */
export interface EngineOptions {
  /**
   * The renderer to try first, or undefined for WebGL, then WebGPU, then
   * Canvas 2D. A WebGL that the browser runs in software, with a major
   * performance caveat, counts as none, so that the Canvas 2D renderer,
   * which is faster on such a device, draws, unless forced asks for it.
   */
  preference?: RendererKind;
  forced?: boolean;
  /** The font of the page, which the cards' text is in. */
  fontFamily: string;
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

/** How far a card's ring, 4 units outside it, and its ports, dots of 10 on its edges, reach beyond its node, in units of the board. */
const cardReach = 8;

/** The renderers in the order that the canvas tries them, without a preference. */
const renderers: readonly RendererKind[] = ["webgl", "webgpu", "canvas"];

/**
 * Starts the renderer on a canvas element of its own, which it adds to
 * surface, or rejects where the browser has none. It tries each renderer in
 * turn, each on a canvas of its own, as a canvas keeps the kind of context
 * that it gave first, so that one that a renderer failed to start on, as
 * where the GPU's process is gone, cannot give the next renderer its own.
 */
export async function createEngine(
  surface: HTMLElement,
  options: EngineOptions,
): Promise<Engine> {
  const caveat = options.forced !== true;
  const order =
    options.preference === undefined
      ? renderers
      : [
          options.preference,
          ...renderers.filter((k) => k !== options.preference),
        ];
  // PixiJS tells whether the page has WebGL once, for the page, with the
  // caveat that its first caller asked with, so that a board that forces
  // WebGL after one that did not would find none, and one that does not
  // after one that did would start a WebGL that the browser runs in
  // software, and fail: the canvas has PixiJS tell whether the browser has
  // any, as its own default does, and tells whether it is the GPU's itself,
  // each time that it starts.
  isWebGLSupported(false);
  for (const kind of order) {
    if (kind === "webgl" && caveat && !hardwareWebGL()) {
      continue;
    }
    if (kind === "webgpu" && caveat && !(await hardwareWebGPU())) {
      continue;
    }
    const canvas = document.createElement("canvas");
    canvas.setAttribute("aria-hidden", "true");
    canvas.className = "block size-full";
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
        failIfMajorPerformanceCaveat: caveat,
        // The canvas brings what it draws with its own imports: PixiJS's
        // accessibility layer, which would add a button of its own to the
        // page on a tablet, and its events, are not the canvas's (item 63
        // gives the canvas its accessibility tree).
        manageImports: false,
      });
      return new PixiEngine(renderer, canvas, options.fontFamily);
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
 * Reports whether the browser has a WebGL that its GPU runs, rather than one
 * that it runs in software, with a major performance caveat, as PixiJS asks
 * for it, without keeping the answer.
 */
function hardwareWebGL(): boolean {
  try {
    const gl = document.createElement("canvas").getContext("webgl", {
      stencil: true,
      failIfMajorPerformanceCaveat: true,
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
 * Reports whether the browser has a WebGPU that its GPU runs, rather than
 * none, or a fallback adapter, which the browser may run in software, with
 * the performance caveat that a WebGL of the kind has, so that a device
 * without a GPU gets the Canvas 2D renderer (ADR-013) by either.
 */
async function hardwareWebGPU(): Promise<boolean> {
  // A browser without WebGPU, or a page that is not of a secure context,
  // has no gpu, whatever the page's types say.
  const gpu: GPU | undefined = navigator.gpu;
  if (gpu === undefined) {
    return false;
  }
  try {
    const adapter: AdapterOf | null = await gpu.requestAdapter();
    return (
      adapter !== null &&
      adapter.info?.isFallbackAdapter !== true &&
      adapter.isFallbackAdapter !== true
    );
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
  key?: string;
  /** The labels that it shows, so that a change of the names that cards show writes again only the cards that it changes. */
  labels?: NodeLabels;
}

/** A tile of the connections' lines: its Graphics, and the rectangle of the board that its lines take, or none for a tile without any. */
interface LineTile {
  g: Graphics;
  bounds: Rect | undefined;
}

class PixiEngine implements Engine {
  readonly kind: RendererKind;
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
        selectionVersion: number;
        labelsVersion: number;
        theme: Theme;
        stats: Omit<FrameStats, "visible">;
        shown: NodeItem[];
        connections: ConnectionItem[];
      }
    | undefined;
  /** The fonts that this renderer installed, which it releases as it ends. */
  private readonly fonts = new Set<string>();
  /** Whether the Canvas 2D renderer shows blocks for the nodes in view, which it does until fewer are than bring its cards back (crowdedAt). */
  private crowded = false;

  constructor(
    private readonly renderer: Renderer,
    /** The canvas element that the renderer draws on, which it takes out of the page as it ends. */
    private readonly canvas: HTMLCanvasElement,
    private readonly fontFamily: string,
  ) {
    this.kind = kindOf(renderer);
    this.world.addChild(this.lines, this.blocks, this.cards);
    this.stage.addChild(this.world);
    this.art = {
      card: canvasTexture(64, (g) => {
        roundRect(g, 0.5, 0.5, 63, 63, 16);
        g.fill();
      }),
      edge: canvasTexture(64, (g) => {
        g.lineWidth = 3;
        roundRect(g, 1.5, 1.5, 61, 61, 15);
        g.stroke();
      }),
      ring: canvasTexture(64, (g) => {
        g.lineWidth = 6;
        roundRect(g, 3, 3, 58, 58, 17);
        g.stroke();
      }),
      dot: canvasTexture(32, (g) => {
        g.beginPath();
        g.arc(16, 16, 14, 0, Math.PI * 2);
        g.fill();
      }),
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
    const system = (
      this.renderer as unknown as {
        texture?: { managedTextures?: readonly unknown[] };
      }
    ).texture;
    return system?.managedTextures?.length;
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
      inView = f.scene.nodesIn(visible).length;
    }
    if (this.kind === "canvas") {
      this.crowded = crowdedAt(v.zoom, inView ?? 0, this.crowded);
    }
    const lod: FrameStats["lod"] =
      v.zoom < detailZoom || this.crowded ? "blocks" : "detail";
    const moved =
      b === undefined ||
      b.lod !== lod ||
      b.tier !== tier ||
      v.zoom > b.zoom * zoomSlack ||
      v.zoom < b.zoom / zoomSlack ||
      !contains(b.region, visible);
    const stale =
      moved ||
      b.sceneVersion !== f.scene.version ||
      b.selectionVersion !== f.selectionVersion ||
      b.labelsVersion !== f.labelsVersion ||
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
    this.world.scale.set(v.zoom);
    this.world.position.set(
      (built.origin.x - v.x) * v.zoom,
      (built.origin.y - v.y) * v.zoom,
    );
    if (this.kind === "canvas") {
      this.cull(visible);
    }
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
    let texts = 0;
    if (lod === "blocks") {
      this.releaseCards(new Set());
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
        i++;
      }
      for (; i < this.blockSprites.length; i++) {
        this.blockSprites[i].visible = false;
      }
    } else {
      this.blocks.visible = false;
      this.cards.visible = true;
      const keep = new Set(shown.nodes.map((n) => n.node.id));
      this.releaseCards(keep);
      const fonts = this.fontsOf(tier);
      const lines = this.kind === "canvas" ? 2 : 4;
      for (const item of shown.nodes) {
        let view = this.cardViews.get(item.node.id);
        if (view === undefined) {
          view = this.freeCards.pop() ?? this.newCard(fonts);
          this.cardViews.set(item.node.id, view);
        }
        const selected = f.selection.has(item.node.id);
        const key = `${selected}`;
        const stale = fresh || view.item !== item || view.key !== key;
        if (stale || relabel) {
          const labels = f.labels(item);
          if (stale || !sameLabels(labels, view.labels)) {
            this.drawCard(view, item, labels, f, fonts, selected, lines);
            view.item = item;
            view.key = key;
            view.labels = labels;
          }
        }
        view.root.position.set(item.rect.x - origin.x, item.rect.y - origin.y);
        texts += view.body.visible ? 3 : 2;
      }
      this.orderCards(shown.nodes);
    }
    // The lines are one stroke of every connection of the margin, or of a
    // tile's, drawn anew only once a connection, the margin, the zoom that
    // their width follows, the level of detail, or the theme changed.
    if (!same || was.zoom !== zoom || was.lod !== lod || was.theme !== theme) {
      this.drawLines(shown.connections, region, origin, zoom, lod, theme);
    }
    this.built = {
      region,
      origin,
      zoom,
      lod,
      tier,
      sceneVersion: scene.version,
      selectionVersion: f.selectionVersion,
      labelsVersion: f.labelsVersion,
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
   * Puts the cards in use in the order of nodes, the scene's, which they
   * are drawn in, in one pass, where they are not in it already, as once a
   * free card, wherever it was among them, is that of a node that came into
   * the margin: rather than move each card to its place on its own, which
   * would look for it, and shift those after it, among every card, in use
   * and free, for each of them.
   */
  private orderCards(nodes: readonly NodeItem[]): void {
    const roots = nodes.flatMap((item) => {
      const view = this.cardViews.get(item.node.id);
      return view === undefined ? [] : [view.root];
    });
    const rank = new Map(roots.map((root, i) => [root, i]));
    let last = -1;
    let ordered = true;
    for (const child of this.cards.children) {
      const r = rank.get(child as Container);
      if (r === undefined) {
        continue;
      }
      if (r < last) {
        ordered = false;
        break;
      }
      last = r;
    }
    if (ordered) {
      return;
    }
    this.cards.removeChildren();
    this.cards.addChild(...roots, ...this.freeCards.map((view) => view.root));
  }

  /** Frees the cards of the nodes that keep does not hold, for nodes that come into the margin. */
  private releaseCards(keep: ReadonlySet<string>): void {
    for (const [id, view] of this.cardViews) {
      if (!keep.has(id)) {
        this.cardViews.delete(id);
        view.root.visible = false;
        view.item = undefined;
        view.labels = undefined;
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
    view.edge.tint =
      node.color === "none" ? theme.border : nodeColor(node.color, theme);
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
        branch ? theme.muted : theme.fg,
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
   * which cull draws the tile or not.
   */
  private drawLines(
    connections: readonly ConnectionItem[],
    region: Rect,
    origin: { x: number; y: number },
    zoom: number,
    lod: FrameStats["lod"],
    theme: Theme,
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
      for (const { curve: c } of items) {
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
      }
      g.stroke({ width, color: theme.border, alpha: 0.9 });
      if (lod === "detail" && this.kind !== "canvas") {
        // An arrowhead where each connection enters its node.
        for (const { curve: c } of items) {
          const x = c.p3.x - origin.x;
          const y = c.p3.y - origin.y;
          g.poly([x, y, x - 9, y - 5, x - 9, y + 5]);
        }
        g.fill({ color: theme.border });
      }
    });
  }

  /** Returns the fonts of tier, installing them once. */
  private fontsOf(tier: number): Fonts {
    const fonts = {
      regular: `storycreep-regular-${tier}`,
      bold: `storycreep-bold-${tier}`,
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

  destroy(): void {
    this.stage.destroy({ children: true });
    for (const t of Object.values(this.art)) {
      t.destroy(true);
    }
    for (const name of this.fonts) {
      const had = installed.get(name);
      if (had !== undefined && had.users > 1) {
        had.users--;
      } else {
        installed.delete(name);
        BitmapFontManager.uninstall(name);
      }
    }
    this.renderer.destroy();
    this.canvas.remove();
  }
}

/**
 * What the canvas reads of a bitmap font that it installed, and asks of it:
 * the width that it draws each character that it has at, in the units of
 * its size of measurement, and the glyphs of more.
 */
type CardFont = Pick<BitmapFont, "chars" | "baseMeasurementFontSize"> & {
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

function kindOf(renderer: Renderer): RendererKind {
  switch (renderer.name) {
    case "webgpu":
      return "webgpu";
    case "canvas":
      return "canvas";
    default:
      return "webgl";
  }
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

function roundRect(
  g: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
): void {
  g.beginPath();
  g.roundRect(x, y, w, h, r);
}
