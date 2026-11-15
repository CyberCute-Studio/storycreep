import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import type { Segment } from "../published";
import {
  type Names,
  PREVIEW_CHARS,
  plainTextOf,
  previewOf,
  segmentsOf,
} from "./derive";
import { parseRichText } from "./parse";

// schema/rich-text/vectors.json, which the Go package's derivations run too
// (TestVectors): each valid document's segments, plain text, and preview,
// and the previews of plain texts.
const vectors = JSON.parse(
  readFileSync(
    new URL("../../../../schema/rich-text/vectors.json", import.meta.url),
    "utf8",
  ),
) as {
  names: {
    components: Record<string, string>;
    values: { ref: string; attribute?: string; name: string }[];
  };
  valid: {
    note: string;
    doc: unknown;
    segments: unknown[];
    text: string;
    preview: string;
  }[];
  previews: { note: string; text: string; preview: string }[];
};

const names: Names = {
  value: (ref, attribute) =>
    vectors.names.values.find((v) => v.ref === ref && v.attribute === attribute)
      ?.name ?? "",
  component: (id) => vectors.names.components[id] ?? "",
};

// sorted returns v with each object's keys in the order of their code
// points, as the server writes the segments, so that its JSON compares with
// theirs.
function sorted(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(sorted);
  }
  if (typeof v === "object" && v !== null) {
    return Object.fromEntries(
      Object.keys(v)
        .sort()
        .map((k) => [k, sorted((v as Record<string, unknown>)[k])]),
    );
  }
  return v;
}

describe("the vectors", () => {
  test.each(vectors.valid)(
    "derives the segments, the text, and the preview of $note",
    ({ doc, segments, text, preview }) => {
      const segs = segmentsOf(parseRichText(doc), names);
      expect(JSON.stringify(sorted(segs))).toBe(JSON.stringify(segments));
      expect(plainTextOf(segs)).toBe(text);
      expect(previewOf(text)).toBe(preview);
    },
  );

  test.each(vectors.previews)("previews $note", ({ text, preview }) => {
    expect(previewOf(text)).toBe(preview);
  });
});

test("an empty document has no segments", () => {
  expect(
    segmentsOf({ type: "doc", content: [{ type: "paragraph" }] }, names),
  ).toEqual([]);
});

test("a link's segment has its href, and the link among its marks", () => {
  const segs: Segment[] = segmentsOf(
    {
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            {
              type: "text",
              text: "map",
              marks: [
                { type: "bold" },
                { type: "link", attrs: { href: "https://example.com/" } },
              ],
            },
          ],
        },
      ],
    },
    names,
  );
  expect(segs).toEqual([
    {
      kind: "text",
      marks: ["bold", "link"],
      href: "https://example.com/",
      value: "map",
    },
  ]);
});

test("a preview cuts a long word at its character's start", () => {
  // An accent after the 100th code point stays with its letter.
  const word = `${"a".repeat(PREVIEW_CHARS - 1)}e\u0301z`;
  expect(previewOf(word)).toBe(`${"a".repeat(PREVIEW_CHARS - 1)}…`);
});
