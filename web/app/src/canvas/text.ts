// How a card's text fits it (PRD FR-EDIT-01): its sizes, the resolution
// that its glyphs are drawn at, which follows the zoom, and its lines, cut
// to the card's width and height, which a bitmap font's text, of no lines
// of its own, does not do, by the width that each character takes, as the
// card's font measures it (engine.ts), or as a column, an average Latin
// letter's, or two for a wide one, each a character as a reader sees it,
// of one code point or more, as the font draws it.

/** The most characters of a text that a card shows, which its lines cut shorter. */
export const excerptLength = 240;

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
 * How far below the least resolution that a tier is needed at a zoom goes
 * before the glyphs are drawn at the tier below, which writes every card of
 * the renderer's margin anew, as a pinch that rests about a tier's edge,
 * its fingers' tremor across it, would else do in each frame that crosses
 * it: a tenth, over which a glyph is drawn at a little more than twice the
 * size that it shows at, rather than twice at most, as sharp.
 */
const tierSlack = 1.1;

/**
 * Returns the resolution that the fonts' glyphs are drawn at for a zoom of
 * the board's units to device pixels of scale: 1, 2, or 4, so that text
 * stays sharp to 400% on a screen of two device pixels to one; with the
 * tier that they are drawn at now, current, a higher one stays as the zoom
 * falls, until it falls by a tenth below the tier's (tierSlack), and a
 * lower one goes up as soon as the zoom needs it, as the glyphs would else
 * be blurred.
 */
export function textTier(scale: number, current?: number): number {
  const needed = (scale * titleSize) / glyphSize;
  const tier = needed <= 1 ? 1 : needed <= 2 ? 2 : 4;
  return current !== undefined &&
    tier < current &&
    needed * tierSlack > current / 2
    ? current
    : tier;
}

/**
 * The characters that are about an em wide, twice an average letter of the
 * Latin alphabet: those of Chinese, Japanese, and Korean, their punctuation
 * and fullwidth forms, and emoji, and the regional indicators, two of which
 * are a flag, as a terminal counts them wide; a character of more than a
 * code point is wide where any of its code points is. A card cuts its text
 * by the room that it takes, as its font measures it, or, for a character
 * that the font has not drawn yet, by these columns, rather than by how
 * many characters it has, as a bitmap font's text is neither clipped nor
 * masked, so that a title in capitals, or in Japanese, does not run on past
 * its card's edge, over the nodes beside it.
 */
const wide =
  /[\u1100-\u115f\u2e80-\u303e\u3041-\u33ff\u3400-\u4dbf\u4e00-\u9fff\ua000-\ua4cf\ua960-\ua97f\uac00-\ud7a3\uf900-\ufaff\ufe30-\ufe4f\uff00-\uff60\uffe0-\uffe6\u{1f1e6}-\u{1f1ff}\u{1f300}-\u{1faff}\u{20000}-\u{3fffd}]/u;

/**
 * The code points that may join another into one character, as a reader
 * sees it, by each rule of Unicode's (UAX #29) that keeps two code points
 * together, whatever the version of it that the browser's segmenter has:
 * those that are no character of their own (not Grapheme_Base), as the
 * joiners within an emoji of a family, an emoji's tags, the controls, of
 * which a carriage return joins the line feed after it, the format marks
 * that number signs of Arabic are, which join the digits after them, and
 * the halfwidth katakana's voiced marks, which join the letter before;
 * combining marks, as of an accent written after its letter, or a vowel of
 * Devanagari or Thai; the letters that join as such a mark does, or as a
 * mark before a letter does, as Thai's and Lao's vowel AM, after its
 * consonant, Kirat Rai's vowels, and Malayalam's dot reph, before one; the
 * conjoining jamo of Hangul, which a syllable is written of, or ends in;
 * the regional indicators, two of which are a flag; and an emoji's skin
 * tones. The line feed alone is none, as the cut lines that the canvas
 * checks (engine.ts) have it between them. A text of none of them is of
 * characters of a code point each, as the segmenter, which PixiJS keys its
 * glyphs by, finds them too, and the canvas reads it without segmenting
 * it, as segmenting takes ten times as long; a text of any of them, which
 * may not join, is segmented, as is any of a code point that Unicode has
 * not given a character yet, which a newer segmenter may join.
 */
const joining =
  /[^\p{Grapheme_Base}\n]|\p{M}|[\u0d4e\u0e33\u0eb3\u1100-\u11ff\ua960-\ua97f\ud7b0-\ud7ff\u{111c2}\u{111c3}\u{113d1}\u{1193f}\u{11941}\u{11a3a}\u{11a84}-\u{11a89}\u{11d46}\u{11f02}\u{16d63}\u{16d67}-\u{16d6a}\u{1f1e6}-\u{1f1ff}\u{1f3fb}-\u{1f3ff}]/u;

/** The browser's segmenter of a text's characters, as a reader sees them. */
const segmenter =
  typeof Intl.Segmenter === "function" ? new Intl.Segmenter() : undefined;

/**
 * Returns the characters of value, as a reader sees them and as PixiJS's
 * bitmap fonts draw them, a glyph for each, of one code point or more, as
 * a letter with its accent, a syllable of Devanagari, an emoji of a family,
 * or a flag, so that a cut never splits one, which a font would draw as a
 * broken glyph or a stray letter; without the browser's segmenter, of a
 * code point each, as PixiJS reads them then.
 */
export function graphemes(value: string): Iterable<string> {
  if (segmenter === undefined || !joining.test(value)) {
    return value;
  }
  return Array.from(segmenter.segment(value), (s) => s.segment);
}

/** Returns how wide a character, as a reader sees it, of a code point or more, is, in the units of the room that a text is cut to. */
export type Measure = (ch: string) => number;

/** Returns how many of a card's columns, each of about an average Latin letter's width, the character ch takes. */
export function columnsOf(ch: string): number {
  return wide.test(ch) ? 2 : 1;
}

/**
 * Returns how long, in UTF-16 code units, the start of value is that fits
 * in max, as measure measures its characters, whole characters of it, as a
 * reader sees them (graphemes), so that a cut never splits one, as an
 * emoji of two code units, or a letter and its accent.
 */
function fitting(value: string, max: number, measure: Measure): number {
  let used = 0;
  let end = 0;
  for (const ch of graphemes(value)) {
    used += measure(ch);
    if (used > max) {
      break;
    }
    end += ch.length;
  }
  return end;
}

/**
 * Returns the first line of value, cut to at most max, as measure measures
 * its characters, or in columns, with an ellipsis where it was cut, or
 * where more lines follow it, which the line makes room for within max.
 */
export function clip(
  value: string,
  max: number,
  measure: Measure = columnsOf,
): string {
  const line = value.split("\n", 1)[0];
  if (line === value && fitting(line, max, measure) === line.length) {
    return value;
  }
  return `${line.slice(0, fitting(line, max - measure("…"), measure))}…`;
}

/**
 * Returns value in at most lines lines, each at most perLine, as measure
 * measures its characters, or in columns, broken at spaces where it can
 * be, with an ellipsis where it was cut; rows keeps each line of value on
 * its own row, cut to one, as a branch's outputs, each of which a card
 * shows, however long the rows before it are.
 */
export function wrapLines(
  value: string,
  perLine: number,
  lines: number,
  rows: boolean,
  measure: Measure = columnsOf,
): string {
  if (lines <= 0) {
    return "";
  }
  if (rows) {
    return value
      .split("\n", lines)
      .map((line) => clip(line, perLine, measure))
      .join("\n");
  }
  const source = value.slice(0, excerptLength + 1).split("\n");
  const out: string[] = [];
  for (const paragraph of source) {
    let rest = paragraph;
    do {
      if (out.length === lines) {
        out[lines - 1] = clip(`${out[lines - 1]}\n`, perLine, measure);
        return out.join("\n");
      }
      const end = fitting(rest, perLine, measure);
      if (end === rest.length) {
        out.push(rest);
        rest = "";
      } else {
        // A line takes at least a character, as of a card narrower than a
        // wide one, which is of a code unit or more.
        const fit = end > 0 ? end : firstLength(rest);
        const space = rest.lastIndexOf(" ", fit);
        const cut = space > fit / 3 ? space : fit;
        out.push(rest.slice(0, cut).trimEnd());
        rest = rest.slice(cut).trimStart();
      }
    } while (rest !== "");
  }
  return out.join("\n");
}

/** Returns how long, in UTF-16 code units, the first character of value, as a reader sees it, is. */
function firstLength(value: string): number {
  for (const ch of graphemes(value)) {
    return ch.length;
  }
  return 0;
}

/**
 * Returns how long, in UTF-16 code units, the longest start of value is of
 * at most max of them that ends between two characters, as a reader sees
 * them, as a text's excerpt is cut (labels.ts), so that it never ends
 * within one, as an emoji, which a font would draw as a broken glyph.
 */
export function wholeStart(value: string, max: number): number {
  if (value.length <= max) {
    return value.length;
  }
  // Of the code units up to a little after max alone, as a text may be
  // long: a character that the cut would end within ends after max either
  // way, as one longer than the little does.
  let end = 0;
  for (const ch of graphemes(value.slice(0, max + 16))) {
    if (end + ch.length > max) {
      break;
    }
    end += ch.length;
  }
  return end;
}
