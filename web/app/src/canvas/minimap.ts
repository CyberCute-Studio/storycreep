// The canvas's minimap (PRD FR-EDIT-01): the board's nodes, small, with the
// rectangle that the canvas shows, on a Canvas 2D canvas of its own. It
// draws the nodes once for each change of the board or of the theme, into a
// canvas that it keeps, and copies it for each frame, with the rectangle.

import type { Scene } from "./scene";
import { nodeColor, type Theme } from "./theme";
import {
  grow,
  type Point,
  type Rect,
  type Size,
  type Viewport,
  visibleRect,
} from "./viewport";

/** The room around the board's content on the minimap, in CSS pixels. */
const inset = 6;

/** How the minimap maps the board's units to its CSS pixels. */
interface Mapping {
  bounds: Rect;
  scale: number;
  dx: number;
  dy: number;
}

/** Returns where on a minimap of size the board's bounds go, whole, centred. */
export function mappingOf(bounds: Rect, size: Size): Mapping {
  const scale = Math.min(
    (size.width - 2 * inset) / Math.max(1, bounds.width),
    (size.height - 2 * inset) / Math.max(1, bounds.height),
  );
  return {
    bounds,
    scale,
    dx: (size.width - bounds.width * scale) / 2,
    dy: (size.height - bounds.height * scale) / 2,
  };
}

/** Returns the point of the board that p of a minimap of mapping m shows. */
export function minimapToWorld(m: Mapping, p: Point): Point {
  return {
    x: m.bounds.x + (p.x - m.dx) / m.scale,
    y: m.bounds.y + (p.y - m.dy) / m.scale,
  };
}

/** Draws a board's minimap on a canvas element. */
export class Minimap {
  private readonly base = document.createElement("canvas");
  private drawn:
    | {
        version: number;
        theme: Theme;
        width: number;
        height: number;
        ratio: number;
      }
    | undefined;
  private mapping: Mapping | undefined;

  constructor(private readonly canvas: HTMLCanvasElement) {}

  /** Returns the point of the board at p of the minimap, in its CSS pixels, once it has drawn one. */
  toWorld(p: Point): Point | undefined {
    return this.mapping === undefined
      ? undefined
      : minimapToWorld(this.mapping, p);
  }

  /** Draws scene, with the rectangle that a canvas of size shows at v. */
  draw(scene: Scene, v: Viewport, size: Size, theme: Theme): void {
    const width = this.canvas.clientWidth;
    const height = this.canvas.clientHeight;
    const ratio = window.devicePixelRatio || 1;
    const g = this.canvas.getContext("2d");
    if (g === null || width === 0 || height === 0) {
      return;
    }
    const content = scene.contentBounds();
    const d = this.drawn;
    // A board without nodes maps the canvas's own rectangle, as it moves.
    if (content === undefined) {
      this.mapping = mappingOf(visibleRect(v, size), { width, height });
    }
    if (
      d === undefined ||
      d.version !== scene.version ||
      d.theme !== theme ||
      d.width !== width ||
      d.height !== height ||
      d.ratio !== ratio ||
      content === undefined
    ) {
      if (content !== undefined) {
        this.mapping = mappingOf(grow(content, 0.05), { width, height });
      }
      this.drawBase(scene, theme, width, height, ratio);
      this.drawn = { version: scene.version, theme, width, height, ratio };
    }
    if (this.canvas.width !== Math.round(width * ratio)) {
      this.canvas.width = Math.round(width * ratio);
    }
    if (this.canvas.height !== Math.round(height * ratio)) {
      this.canvas.height = Math.round(height * ratio);
    }
    g.setTransform(1, 0, 0, 1, 0, 0);
    g.clearRect(0, 0, this.canvas.width, this.canvas.height);
    g.drawImage(this.base, 0, 0);
    const m = this.mapping;
    if (m === undefined) {
      return;
    }
    g.setTransform(ratio, 0, 0, ratio, 0, 0);
    const r = visibleRect(v, size);
    const x = m.dx + (r.x - m.bounds.x) * m.scale;
    const y = m.dy + (r.y - m.bounds.y) * m.scale;
    // The rectangle, within the minimap, however far the canvas is from
    // the content.
    const x0 = Math.max(1, Math.min(width - 1, x));
    const y0 = Math.max(1, Math.min(height - 1, y));
    const x1 = Math.max(1, Math.min(width - 1, x + r.width * m.scale));
    const y1 = Math.max(1, Math.min(height - 1, y + r.height * m.scale));
    g.strokeStyle = hex(theme.accent);
    g.lineWidth = 2;
    g.strokeRect(x0, y0, Math.max(2, x1 - x0), Math.max(2, y1 - y0));
  }

  private drawBase(
    scene: Scene,
    theme: Theme,
    width: number,
    height: number,
    ratio: number,
  ): void {
    this.base.width = Math.round(width * ratio);
    this.base.height = Math.round(height * ratio);
    const g = this.base.getContext("2d");
    const m = this.mapping;
    if (g === null || m === undefined) {
      return;
    }
    g.setTransform(ratio, 0, 0, ratio, 0, 0);
    g.fillStyle = hex(theme.surface);
    g.fillRect(0, 0, width, height);
    // Each node, as a rectangle of at least a pixel, in its colour, the
    // nodes of one colour together, so that the context changes its fill
    // once for each.
    const byColor = new Map<number, Rect[]>();
    scene.nodes.forEach((_, item, rect) => {
      const color = nodeColor(item.node.color, theme);
      let list = byColor.get(color);
      if (list === undefined) {
        list = [];
        byColor.set(color, list);
      }
      list.push(rect);
    });
    for (const [color, rects] of byColor) {
      g.fillStyle = hex(color);
      for (const r of rects) {
        g.fillRect(
          m.dx + (r.x - m.bounds.x) * m.scale,
          m.dy + (r.y - m.bounds.y) * m.scale,
          Math.max(1, r.width * m.scale),
          Math.max(1, r.height * m.scale),
        );
      }
    }
  }
}

/** Returns color, 0xRRGGBB, as #rrggbb. */
function hex(color: number): string {
  return `#${color.toString(16).padStart(6, "0")}`;
}
