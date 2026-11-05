// The editor's document as the server stores it, and back (PRD FR-TEXT-01,
// schema/rich-text/README.md).

import type { RichTextDoc } from "@storycreep/shared/api";
import { parseRichText, RichTextError } from "@storycreep/shared/richtext";
import type { Mark, Node as PMNode } from "@tiptap/pm/model";

type Json = Record<string, unknown>;

function marksOf(marks: readonly Mark[]): Json {
  return marks.length === 0
    ? {}
    : {
        marks: marks.map((mark) =>
          mark.type.name === "link"
            ? { attrs: { href: mark.attrs.href }, type: "link" }
            : { type: mark.type.name },
        ),
      };
}

function contentOf(node: PMNode): Json {
  const content: Json[] = [];
  node.forEach((child) => {
    content.push(jsonOf(child));
  });
  return content.length === 0 ? {} : { content };
}

/**
 * Returns a node of the editor as the document's JSON holds it: without the
 * attributes that ProseMirror writes as null where they are not set, which
 * the server refuses, and without an ordered list's start of 1.
 */
function jsonOf(node: PMNode): Json {
  const type = node.type.name;
  switch (type) {
    case "text":
      return { ...marksOf(node.marks), text: node.text, type };
    case "hardBreak":
      return { ...marksOf(node.marks), type };
    case "mention":
      return { attrs: { id: node.attrs.id }, ...marksOf(node.marks), type };
    case "valueRef":
      return {
        attrs: {
          ...(node.attrs.attribute === null
            ? {}
            : { attribute: node.attrs.attribute }),
          ...(node.attrs.decimals === null
            ? {}
            : { decimals: node.attrs.decimals }),
          ref: node.attrs.ref,
        },
        ...marksOf(node.marks),
        type,
      };
    case "orderedList":
      return {
        ...(node.attrs.start === 1
          ? {}
          : { attrs: { start: node.attrs.start } }),
        ...contentOf(node),
        type,
      };
    default:
      return { ...contentOf(node), type };
  }
}

/** What the editor holds: its document as the server stores it, or why the server would refuse it. */
export type RichTextValue =
  | { doc: RichTextDoc; error?: undefined }
  | { doc?: undefined; error: RichTextError };

/** Returns the editor's document in its canonical form, as the server stores it, or why the server would refuse it, as a document that is too large. */
export function storedOf(doc: PMNode): RichTextValue {
  try {
    return { doc: parseRichText(jsonOf(doc)) };
  } catch (error) {
    if (error instanceof RichTextError) {
      return { error };
    }
    throw error;
  }
}
