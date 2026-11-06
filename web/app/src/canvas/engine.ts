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
  BitmapFont,
  BitmapText,
  Container,
  Graphics,
  isWebGLSupported,
  NineSliceSprite,
  type Renderer,
  Sprite,
  Texture,
} from "pixi.js";
import type { NodeLabels } from "./labels";
import { type Curve, outputOffset, portPoint } from "./layout";
import type { ConnectionItem, NodeItem, Scene } from "./scene";
import {
  bodyLine,
  bodySize,
  bodyTop,
  clip,
  glyphSize,
  kindSize,
  textTier,
  titleSize,
  wrapLines,
} from "./text";
import { nodeColor, type Theme } from "./theme";
import {
  contains,
  detailZoom,
  grow,
  intersects,
  type Rect,
  type Size,
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

/**
 * The Canvas 2D renderer's reduced limits (ADR-013): it draws each sprite
 * on its own, so it shows cards with text only while at most this many
 * nodes are in the canvas, and blocks beyond, and a card's text in fewer
 * lines.
 */
export const canvasDetailLimit = 300;

/** How far around the canvas the renderer builds objects, in canvases: half of one on each side. */
const margin = 0.5;

/** How far the zoom may go from the one that the margin was built at before it is built anew, so that lines and text keep their width on the screen. */
const zoomSlack = 1.25;

/** The corner of a card, in units of the board. */
const cornerRadius = 10;

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
    const canvas = document.createElement("canvas");
    canvas.setAttribute("aria-hidden", "true");
    canvas.className = "block size-full";
    surface.append(canvas);
    try {
      const renderer = await autoDetectRenderer({
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
}

class PixiEngine implements Engine {
  readonly kind: RendererKind;
  private readonly stage = new Container();
  /** The board, at the zoom, positioned from the origin of the margin. */
  private readonly world = new Container();
  private readonly lines = new Graphics();
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
    const tier = textTier(v.zoom * this.renderer.resolution);
    const b = this.built;
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
    const lod: FrameStats["lod"] =
      v.zoom < detailZoom ||
      (this.kind === "canvas" && (inView ?? 0) > canvasDetailLimit)
        ? "blocks"
        : "detail";
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
    this.renderer.background.color = f.theme.canvas;
    this.renderer.render(this.stage);
    return {
      ...built.stats,
      visible: inView ?? countIn(built.shown, visible),
    };
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
      was.labelsVersion !== f.labelsVersion ||
      was.theme !== theme ||
      was.lod !== lod;
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
        if (fresh || view.item !== item || view.key !== key) {
          this.drawCard(view, item, f, fonts, selected, lines);
          view.item = item;
          view.key = key;
        }
        view.root.position.set(item.rect.x - origin.x, item.rect.y - origin.y);
        texts += view.body.visible ? 3 : 2;
      }
      // The order that the cards are drawn in is the scene's.
      shown.nodes.forEach((item, i) => {
        const view = this.cardViews.get(item.node.id);
        if (view !== undefined && this.cards.getChildIndex(view.root) !== i) {
          this.cards.setChildIndex(view.root, i);
        }
      });
    }
    // The lines are one stroke of every connection of the margin, drawn
    // anew only once a connection, the margin, the zoom that their width
    // follows, the level of detail, or the theme changed.
    if (!same || was.zoom !== zoom || was.lod !== lod || was.theme !== theme) {
      this.drawLines(shown.connections, origin, zoom, lod, theme);
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

  /** Frees the cards of the nodes that keep does not hold, for nodes that come into the margin. */
  private releaseCards(keep: ReadonlySet<string>): void {
    for (const [id, view] of this.cardViews) {
      if (!keep.has(id)) {
        this.cardViews.delete(id);
        view.root.visible = false;
        view.item = undefined;
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
    f: Frame,
    fonts: Fonts,
    selected: boolean,
    lines: number,
  ): void {
    const { theme } = f;
    const { width: w, height: h } = item.rect;
    const node = item.node;
    const labels = f.labels(item);
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
    // The columns of a line, each of an average Latin letter's width, 0.56
    // of the font's size, of which a wide character, as of Japanese, takes
    // two (text.ts).
    const inner = Math.max(8, w - 24);
    const perLine = (size: number) =>
      Math.max(4, Math.floor(inner / (size * 0.56)));
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
      clip(labels.title, perLine(titleSize)),
      theme.fg,
      12,
      12,
    );
    setText(
      view.kind,
      fonts.regular,
      clip(labels.kind, perLine(kindSize)),
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
      const text = wrapLines(labels.body, perLine(bodySize), bodyLines, branch);
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

  private drawLines(
    connections: readonly { curve: Curve }[],
    origin: { x: number; y: number },
    zoom: number,
    lod: FrameStats["lod"],
    theme: Theme,
  ): void {
    const g = this.lines;
    g.clear();
    if (connections.length === 0) {
      return;
    }
    const width = 1.5 / zoom;
    for (const { curve: c } of connections) {
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
      for (const { curve: c } of connections) {
        const x = c.p3.x - origin.x;
        const y = c.p3.y - origin.y;
        g.poly([x, y, x - 9, y - 5, x - 9, y + 5]);
      }
      g.fill({ color: theme.border });
    }
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
        const users = fontUsers.get(name) ?? 0;
        fontUsers.set(name, users + 1);
        if (users > 0) {
          continue;
        }
        BitmapFont.install({
          name,
          style: {
            fontFamily: this.fontFamily,
            fontSize: glyphSize,
            fontWeight: weight,
            fill: 0xffffff,
          },
          resolution: tier,
          // The glyphs of the Latin alphabet at once, and those of any
          // other script as the first text that has them comes.
          chars: [[" ", "~"]],
        });
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
      const users = (fontUsers.get(name) ?? 1) - 1;
      if (users > 0) {
        fontUsers.set(name, users);
      } else {
        fontUsers.delete(name);
        BitmapFont.uninstall(name);
      }
    }
    this.renderer.destroy();
    this.canvas.remove();
  }
}

/**
 * How many renderers use each bitmap font, which PixiJS keeps for the page,
 * by its name: the last to end uninstalls it.
 */
const fontUsers = new Map<string, number>();

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
