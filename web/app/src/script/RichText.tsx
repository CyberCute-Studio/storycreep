// A rich text as the script view reads it (PRD FR-TEXT-04, FR-TEXT-01,
// plan note N75): its paragraphs, lists, line breaks, and marks, as HTML
// of the page's own, rather than an editor for each, which a board of
// hundreds of texts would take seconds to start; a mention shows its
// component's name, and a value reference its value's, as an expression of
// the text's board names it, as the cards and the editor show them
// (labels.ts's referenceNames). A block, an inline, or a mark of a type
// that the page does not know, as a newer server's may send to a page
// loaded before it, shows its text alone, or nothing, as a card does, but
// a block or an inline of one is no sign of an empty text (isEmptyText).

import type {
  RichTextBlock,
  RichTextDoc,
  RichTextInline,
  RichTextMarks,
} from "@storycreep/shared/api";
import type { ReactNode } from "react";
import type { LabelContext } from "../canvas/labels";

/** What a text names of what it refers to. */
export type TextNames = Pick<LabelContext, "componentName" | "valueName">;

/** Returns what wraps marks around content, the link outermost, and code innermost, as the editor nests them. */
function marked(content: ReactNode, marks: RichTextMarks | undefined) {
  if (marks === undefined || marks.length === 0) {
    return content;
  }
  const has = (type: string) => marks.some((m) => m.type === type);
  let out = content;
  if (has("code")) {
    out = <code className="rounded bg-surface px-1 font-mono">{out}</code>;
  }
  if (has("strike")) {
    out = <s>{out}</s>;
  }
  if (has("italic")) {
    out = <em>{out}</em>;
  }
  if (has("bold")) {
    out = <strong>{out}</strong>;
  }
  const link = marks.find((m) => m.type === "link");
  if (link !== undefined && link.type === "link") {
    // A link of the text leaves the app in a tab of its own, so that what
    // a form of the view holds is not lost; the schema allows http, https,
    // and mailto alone (FR-TEXT-01).
    out = (
      <a
        href={link.attrs.href}
        target="_blank"
        rel="noopener noreferrer"
        className="text-accent underline"
      >
        {out}
      </a>
    );
  }
  return out;
}

/** Returns what inline shows, of the key key among its paragraph's. */
function inlineOf(i: RichTextInline, key: number, names: TextNames) {
  switch (i.type) {
    case "text":
      return <span key={key}>{marked(i.text, i.marks)}</span>;
    case "hardBreak":
      return <br key={key} />;
    case "mention":
      return (
        <span key={key} className="font-medium" data-mention="">
          {marked(`@${names.componentName(i.attrs.id) ?? "…"}`, i.marks)}
        </span>
      );
    case "valueRef":
      return (
        <span key={key} className="font-mono" data-value-ref="">
          {marked(
            `{${names.valueName(i.attrs.ref, "attribute" in i.attrs ? i.attrs.attribute : undefined) ?? "…"}}`,
            i.marks,
          )}
        </span>
      );
    default:
      return null;
  }
}

/** Returns what blocks show, each keyed by its place. */
function blocksOf(blocks: readonly RichTextBlock[], names: TextNames) {
  return blocks.map((block, key) => {
    switch (block.type) {
      case "paragraph":
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: a text's blocks, in their order, which change with it.
          <p key={key} className="break-words whitespace-pre-wrap">
            {(block.content ?? []).map((i, n) => inlineOf(i, n, names))}
          </p>
        );
      case "bulletList":
      case "orderedList": {
        const items = block.content.map((item, n) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: as above.
          <li key={n}>{blocksOf(item.content ?? [], names)}</li>
        ));
        const start = block.attrs?.start;
        return block.type === "orderedList" ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: as above.
          <ol key={key} start={start} className="list-decimal ps-6">
            {items}
          </ol>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: as above.
          <ul key={key} className="list-disc ps-6">
            {items}
          </ul>
        );
      }
      default:
        return null;
    }
  });
}

/**
 * Reports whether doc holds no text: no inline in any of its blocks, and no
 * block of a type that the page does not know, which may hold text that it
 * cannot show, as an inline of such a type may, rather than say that the
 * text has none.
 */
export function isEmptyText(doc: RichTextDoc | undefined): boolean {
  const empty = (blocks: readonly RichTextBlock[]): boolean =>
    blocks.every((b) =>
      b.type === "paragraph"
        ? (b.content ?? []).length === 0
        : b.type === "bulletList" || b.type === "orderedList"
          ? b.content.every((item) => empty(item.content ?? []))
          : false,
    );
  return doc === undefined || empty(doc.content);
}

/** Shows doc, naming what it refers to by names. */
export function RichText({
  doc,
  names,
}: {
  doc: RichTextDoc;
  names: TextNames;
}) {
  return <div className="space-y-2">{blocksOf(doc.content, names)}</div>;
}
