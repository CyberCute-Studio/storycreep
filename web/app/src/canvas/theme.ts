// The colours that the canvas draws with: the theme's tokens (NFR-A11Y-06),
// as the page resolves them in the system's light or dark mode, or, in
// forced colours, the system's own, and the palette of nodes' colours (PRD
// FR-EDIT-06), which is the boards' (FR-PROJ-02).

import type { Node } from "@storycreep/shared/api";

/** The colours of the canvas, each as 0xRRGGBB. */
export interface Theme {
  /** The board behind the nodes. */
  canvas: number;
  /** A node's card. */
  surface: number;
  /** A node's edge, and a connection. */
  border: number;
  /** A node's title. */
  fg: number;
  /** A node's type and text. */
  muted: number;
  /** The selection's outline, and the rubber band. */
  accent: number;
  /** Whether the theme is dark, which picks the palette's colours. */
  dark: boolean;
}

/** The tokens of the theme that the canvas reads, as CSS custom properties (web/shared/src/tokens). */
const tokens = {
  canvas: "--color-canvas",
  surface: "--color-surface",
  border: "--color-border",
  fg: "--color-fg",
  muted: "--color-fg-muted",
  accent: "--color-accent",
} as const;

/**
 * The system's colours that the browser draws a page in, in forced colours,
 * as under Windows's contrast themes, for each of the tokens, as it draws
 * the page's own elements: the board and the cards on Canvas, their edges
 * and text in CanvasText, and the selection in Highlight. The browser
 * forces the colour of the probe that reads a token as well, so that each
 * token would read as the text's colour, and the board be drawn in one.
 */
const systemColors: Record<keyof typeof tokens, string> = {
  canvas: "Canvas",
  surface: "Canvas",
  border: "CanvasText",
  fg: "CanvasText",
  muted: "CanvasText",
  accent: "Highlight",
};

/** The light theme's colours, for a page that has none of the tokens, as the tests' jsdom. */
const fallback: Theme = {
  canvas: 0xffffff,
  surface: 0xf3f4f6,
  border: 0x7c8494,
  fg: 0x15171c,
  muted: 0x4b5260,
  accent: 0x2b50c8,
  dark: false,
};

/** Returns 0xRRGGBB of a computed colour, as rgb(…), or undefined for one that it cannot read. */
export function parseColor(value: string): number | undefined {
  const m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/.exec(value);
  if (m === null) {
    return undefined;
  }
  const [r, g, b] = [m[1], m[2], m[3]].map((c) =>
    Math.max(0, Math.min(255, Math.round(Number(c)))),
  );
  return (r << 16) | (g << 8) | b;
}

/**
 * Returns the theme as the page resolves its tokens at element, in the
 * system's mode, or, where forced, as the system's forced colours are on,
 * the system's colours that the browser draws the page in instead.
 */
export function readTheme(element: HTMLElement, forced = false): Theme {
  const probe = document.createElement("span");
  probe.style.display = "none";
  element.append(probe);
  try {
    const read = (key: keyof typeof tokens) => {
      probe.style.color = forced ? systemColors[key] : `var(${tokens[key]})`;
      return parseColor(getComputedStyle(probe).color) ?? fallback[key];
    };
    const canvas = read("canvas");
    return {
      canvas,
      surface: read("surface"),
      border: read("border"),
      fg: read("fg"),
      muted: read("muted"),
      accent: read("accent"),
      dark: luminance(canvas) < 0.5,
    };
  } finally {
    probe.remove();
  }
}

function luminance(color: number): number {
  const r = (color >> 16) & 0xff;
  const g = (color >> 8) & 0xff;
  const b = color & 0xff;
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
}

/**
 * The palette of a node's colour, which marks its card's edge, in the light
 * and the dark theme: each 3:1 or more against the card (NFR-A11Y-06).
 */
const palette: Record<Exclude<Node["color"], "none">, [number, number]> = {
  red: [0xc62828, 0xff7b72],
  orange: [0xc25e00, 0xffa657],
  yellow: [0x8f6a00, 0xe3b341],
  green: [0x2e7d32, 0x56d364],
  teal: [0x00796b, 0x39c5bb],
  blue: [0x1f5fd1, 0x79b8ff],
  purple: [0x7b3fc4, 0xd2a8ff],
  pink: [0xb4235f, 0xff7eb6],
  gray: [0x5f6672, 0xa8afbc],
};

/**
 * Returns the colour that marks a node of color, in theme: none's is the
 * theme's edge, as is that of a colour that the palette does not have, as
 * one that a newer server's palette has, of a page loaded before it, rather
 * than a frame that throws, and a board drawn as nothing.
 */
export function nodeColor(color: Node["color"], theme: Theme): number {
  if (color === "none") {
    return theme.border;
  }
  return palette[color]?.[theme.dark ? 1 : 0] ?? theme.border;
}
