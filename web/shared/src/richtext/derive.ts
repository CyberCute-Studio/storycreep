// What the published format and play mode derive of a rich text (PRD §6.1,
// FR-TEXT-02): its segments, which a runtime walks instead of the
// document's tree, its plain text, and its preview, as
// schema/rich-text/README.md says. This is the TypeScript twin of the
// derivations of the Go package server/internal/richtext (Doc.Segments,
// PlainText, and Preview), and both run the vectors of
// schema/rich-text/vectors.json, so that play mode's projection of a draft
// (plan item 71) holds the segments that the server's serializer writes.

import type { RichTextBlock, RichTextDoc, RichTextInline } from "../api";
import { render } from "../flow/text";
import type { Segment, SegmentMarks } from "../published";

/**
 * The names that a document's derivations show of what it refers to, which
 * the caller chooses for what it does not find.
 */
export interface Names {
  /**
   * Returns the name of the value that a reference reads, as an expression
   * names it: a variable's key, as met_elara, or, when attribute is given,
   * a component's key and the attribute's key joined by a dot, as
   * elara.trust.
   */
  value(ref: string, attribute: string | undefined): string;
  /** Returns the name of a component, as a mention shows it. */
  component(id: string): string;
}

/**
 * Returns the segments of doc, a document in its canonical form, as
 * parseRichText returns it and the server stores it, with the names of what
 * it refers to: its inline content in order, texts, values, and mentions,
 * each with its marks, and segments that mark where its paragraphs, list
 * items, and line breaks begin. A paragraph segment begins each paragraph
 * but one that is the first block of the document, which the document's
 * start begins, or of a list item, which the item's segment begins, so that
 * a paragraph after a list has one, even when the list is the document's
 * first block. A document whose only block is an empty paragraph has none.
 */
export function segmentsOf(doc: RichTextDoc, names: Names): Segment[] {
  const out: Segment[] = [];
  blocks(doc.content, 0, names, out);
  return out;
}

// blocks adds the segments of the blocks of a document, at depth 0, or of a
// list item at depth, the item's, to out.
function blocks(
  of: readonly RichTextBlock[],
  depth: number,
  names: Names,
  out: Segment[],
): void {
  let first = true;
  for (const b of of) {
    if (b.type === "paragraph") {
      if (!first) {
        out.push(
          depth === 0 ? { kind: "paragraph" } : { kind: "paragraph", depth },
        );
      }
      for (const inline of b.content ?? []) {
        out.push(segmentOf(inline, names));
      }
    } else {
      const start = b.type === "orderedList" ? (b.attrs?.start ?? 1) : 0;
      b.content.forEach((item, j) => {
        out.push(
          b.type === "orderedList"
            ? {
                kind: "item",
                depth: depth + 1,
                list: "ordered",
                number: start + j,
              }
            : { kind: "item", depth: depth + 1, list: "bullet" },
        );
        blocks(item.content, depth + 1, names, out);
      });
    }
    first = false;
  }
}

// segmentOf returns the segment of an inline node of a paragraph in its
// canonical form.
function segmentOf(inline: RichTextInline, names: Names): Segment {
  if (inline.type === "hardBreak") {
    return { kind: "break" };
  }
  const marks: SegmentMarks = [];
  let href: string | undefined;
  for (const m of inline.marks ?? []) {
    marks.push(m.type);
    if (m.type === "link") {
      href = m.attrs.href;
    }
  }
  const linked = href === undefined ? {} : { href };
  switch (inline.type) {
    case "text":
      return { kind: "text", marks, ...linked, value: inline.text };
    case "mention":
      return {
        kind: "mention",
        marks,
        ...linked,
        ref: inline.attrs.id,
        value: names.component(inline.attrs.id),
      };
    case "valueRef": {
      const a = inline.attrs;
      const attribute = "attribute" in a ? a.attribute : undefined;
      const decimals = a.decimals === undefined ? {} : { decimals: a.decimals };
      return attribute === undefined
        ? {
            kind: "value",
            marks,
            ...linked,
            ref: a.ref,
            ...decimals,
            name: names.value(a.ref, undefined),
          }
        : {
            kind: "value",
            marks,
            ...linked,
            ref: a.ref,
            attribute,
            ...decimals,
            name: names.value(a.ref, attribute),
          };
    }
  }
}

/**
 * Returns the plain text of a document's segments, as the text export shows
 * it: its texts and the names of its mentions, each value as its name in
 * braces, as {met_elara}, laid out as render lays it out.
 */
export function plainTextOf(segments: readonly Segment[]): string {
  return render(segments, (s) => `{${s.name}}`);
}

/** The most characters of a preview, before its ellipsis. */
export const PREVIEW_CHARS = 100;

const whiteSpace = /^\p{White_Space}$/u;
// A code point that continues the character before it rather than starting
// one, as a reader sees it: a mark, a character that Unicode extends a
// grapheme with, a join control, or an emoji modifier, a skin tone.
const continuing =
  /^[\p{M}\p{Grapheme_Extend}\p{Join_Control}\p{Emoji_Modifier}]$/u;
const regionalIndicator = /^\p{Regional_Indicator}$/u;
const zeroWidthJoiner = "\u200d";

/**
 * Returns the preview of a plain text: the text with each run of white space
 * written as one space and none around it, and, when that is longer than
 * PREVIEW_CHARS code points, its first PREVIEW_CHARS, cut back when the cut
 * falls within a word (previewCut), without the spaces at its end, and an
 * ellipsis (…). It reads the text only as far as the code point after the
 * first PREVIEW_CHARS.
 */
export function previewOf(plain: string): string {
  // The text collapsed, as code points, as far as the code point after the
  // first PREVIEW_CHARS, which says whether the cut falls within a word. A
  // run of white space is written once a code point that is not white
  // space follows it.
  const collapsed: string[] = [];
  let space = false;
  for (const c of plain) {
    if (whiteSpace.test(c)) {
      space = collapsed.length > 0;
      continue;
    }
    if (space) {
      collapsed.push(" ");
      if (collapsed.length > PREVIEW_CHARS) {
        break;
      }
      space = false;
    }
    collapsed.push(c);
    if (collapsed.length > PREVIEW_CHARS) {
      break;
    }
  }
  if (collapsed.length <= PREVIEW_CHARS) {
    return collapsed.join("");
  }
  let cut = PREVIEW_CHARS;
  if (collapsed[cut] !== " ") {
    cut = previewCut(collapsed, cut);
  }
  return `${collapsed.slice(0, cut).join("").replace(/ +$/, "")}…`;
}

// previewCut returns where the preview of collapsed, a plain text with its
// white space collapsed, as code points, is cut when its first
// PREVIEW_CHARS, which end at cut, end within a word: at the last space
// among them, when at least PREVIEW_CHARS/2 code points come before it;
// and otherwise at cut, moved back to the start of the character that it
// falls within (splits). A cut within a character that is all of those
// code points stays where it is, so that the preview is never empty.
function previewCut(collapsed: readonly string[], cut: number): number {
  const i = collapsed.lastIndexOf(" ", cut - 1);
  if (i >= PREVIEW_CHARS / 2) {
    return i;
  }
  for (let at = cut; at > 0; at--) {
    if (!splits(collapsed, at)) {
      return at;
    }
  }
  return cut;
}

// splits reports whether a cut of s, as code points, at at, which is
// neither its start nor its end, falls within a character as a reader sees
// it: before a code point that continues the one before it, after U+200D
// ZERO WIDTH JOINER, or between two regional indicators of which the first
// is the first of a pair, a flag, as after an odd number of them in a row.
function splits(s: readonly string[], at: number): boolean {
  const next = s[at];
  if (continuing.test(next) || s[at - 1] === zeroWidthJoiner) {
    return true;
  }
  if (regionalIndicator.test(next)) {
    let n = 0;
    while (at - n - 1 >= 0 && regionalIndicator.test(s[at - n - 1])) {
      n++;
    }
    return n % 2 === 1;
  }
  return false;
}
