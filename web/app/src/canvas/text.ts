// How a card's text fits it (PRD FR-EDIT-01): its sizes, the resolution
// that its glyphs are drawn at, which follows the zoom, and its lines, cut
// to the card's width and height, which a bitmap font's text, of no lines
// of its own, does not do.

import { excerptLength } from "./labels";

/** The text's sizes, in units of the board. */
export const titleSize = 15;
export const kindSize = 12;
export const bodySize = 13;
export const bodyLine = 18;

/**
 * Where an element's text, or a jumper's target, starts on its card, under
 * its title and its type: high enough that a jumper of 72 units, as one
 * without a size of its own is, has the room of a line for its target.
 */
export const bodyTop = 50;

/** The size that the fonts' glyphs are drawn at, in pixels, at their resolution 1. */
export const glyphSize = 28;

/**
 * Returns the resolution that the fonts' glyphs are drawn at for a zoom of
 * the board's units to device pixels of scale: 1, 2, or 4, so that text
 * stays sharp to 400% on a screen of two device pixels to one.
 */
export function textTier(scale: number): number {
  const needed = (scale * titleSize) / glyphSize;
  return needed <= 1 ? 1 : needed <= 2 ? 2 : 4;
}

/** Returns value cut to at most max characters, with an ellipsis where it was cut. */
export function clip(value: string, max: number): string {
  const line = value.split("\n", 1)[0];
  if (line.length <= max && line === value) {
    return value;
  }
  return line.length <= max ? `${line}…` : `${line.slice(0, max - 1)}…`;
}

/**
 * Returns value in at most lines lines of at most perLine characters, broken
 * at spaces where it can be, with an ellipsis where it was cut; rows keeps
 * each line of value on its own row, cut to one, as a branch's outputs.
 */
export function wrapLines(
  value: string,
  perLine: number,
  lines: number,
  rows: boolean,
): string {
  if (lines <= 0) {
    return "";
  }
  const source = value.slice(0, excerptLength + 1).split("\n");
  if (rows) {
    return source
      .slice(0, lines)
      .map((line) => clip(line, perLine))
      .join("\n");
  }
  const out: string[] = [];
  for (const paragraph of source) {
    let rest = paragraph;
    do {
      if (out.length === lines) {
        out[lines - 1] = clip(`${out[lines - 1]}\n`, perLine);
        return out.join("\n");
      }
      if (rest.length <= perLine) {
        out.push(rest);
        rest = "";
      } else {
        const space = rest.lastIndexOf(" ", perLine);
        const cut = space > perLine / 3 ? space : perLine;
        out.push(rest.slice(0, cut).trimEnd());
        rest = rest.slice(cut).trimStart();
      }
    } while (rest !== "");
  }
  return out.join("\n");
}
