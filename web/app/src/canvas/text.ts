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

/**
 * The characters that are about an em wide, twice an average letter of the
 * Latin alphabet: those of Chinese, Japanese, and Korean, their punctuation
 * and fullwidth forms, and emoji, as a terminal counts them wide. A card
 * cuts its text by the room that it takes, rather than by how many
 * characters it has, as a bitmap font's text is neither clipped nor masked,
 * so that a title in Japanese does not run on past its card's edge, over
 * the nodes beside it.
 */
const wide =
  /[\u1100-\u115f\u2e80-\u303e\u3041-\u33ff\u3400-\u4dbf\u4e00-\u9fff\ua000-\ua4cf\ua960-\ua97f\uac00-\ud7a3\uf900-\ufaff\ufe30-\ufe4f\uff00-\uff60\uffe0-\uffe6\u{1f300}-\u{1faff}\u{20000}-\u{3fffd}]/u;

/** Returns how many of a card's columns, each of about an average Latin letter's width, the character ch takes. */
function columnsOf(ch: string): number {
  return wide.test(ch) ? 2 : 1;
}

/**
 * Returns how long, in UTF-16 code units, the start of value is that fits
 * in max columns, whole characters of it, so that a cut never splits a
 * character of two code units, as an emoji.
 */
function fitting(value: string, max: number): number {
  let used = 0;
  let end = 0;
  for (const ch of value) {
    used += columnsOf(ch);
    if (used > max) {
      break;
    }
    end += ch.length;
  }
  return end;
}

/** Returns value cut to at most max columns, with an ellipsis where it was cut. */
export function clip(value: string, max: number): string {
  const line = value.split("\n", 1)[0];
  if (fitting(line, max) === line.length) {
    return line === value ? value : `${line}…`;
  }
  return `${line.slice(0, fitting(line, max - 1))}…`;
}

/**
 * Returns value in at most lines lines of at most perLine columns, broken
 * at spaces where it can be, with an ellipsis where it was cut; rows keeps
 * each line of value on its own row, cut to one, as a branch's outputs,
 * each of which a card shows, however long the rows before it are.
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
  if (rows) {
    return value
      .split("\n", lines)
      .map((line) => clip(line, perLine))
      .join("\n");
  }
  const source = value.slice(0, excerptLength + 1).split("\n");
  const out: string[] = [];
  for (const paragraph of source) {
    let rest = paragraph;
    do {
      if (out.length === lines) {
        out[lines - 1] = clip(`${out[lines - 1]}\n`, perLine);
        return out.join("\n");
      }
      const end = fitting(rest, perLine);
      if (end === rest.length) {
        out.push(rest);
        rest = "";
      } else {
        // A line takes at least a character, as of a card narrower than a
        // wide one, which is of one code unit or two.
        const fit = end > 0 ? end : (rest.codePointAt(0) ?? 0) > 0xffff ? 2 : 1;
        const space = rest.lastIndexOf(" ", fit);
        const cut = space > fit / 3 ? space : fit;
        out.push(rest.slice(0, cut).trimEnd());
        rest = rest.slice(cut).trimStart();
      }
    } while (rest !== "");
  }
  return out.join("\n");
}
