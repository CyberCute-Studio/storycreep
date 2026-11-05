import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import {
  checkHref,
  MAX_BYTES,
  MAX_CHARS,
  parseRichText,
  RichTextError,
  type RichTextReason,
  richTextLength,
  stringifyRichText,
} from "./parse";

// schema/rich-text/vectors.json, which the Go package runs too
// (TestVectors). JSON.parse reads each document as the client would get it,
// so a number too large for a double is Infinity, and a string that escapes
// a lone surrogate keeps it. See schema/rich-text/README.md.
const vectors = JSON.parse(
  readFileSync(
    new URL("../../../../schema/rich-text/vectors.json", import.meta.url),
    "utf8",
  ),
) as {
  valid: { note: string; doc: unknown; canonical?: unknown }[];
  invalid: {
    note: string;
    doc: unknown;
    reason: RichTextReason;
    pointer: string;
  }[];
  lengths: { note: string; json: string; length: number }[];
};

/** Returns the error that parseRichText throws of doc, or undefined. */
function refusal(doc: unknown): RichTextError | undefined {
  try {
    parseRichText(doc);
  } catch (error) {
    if (error instanceof RichTextError) {
      return error;
    }
    throw error;
  }
  return undefined;
}

describe("the vectors", () => {
  test.each(vectors.valid)("accepts $note", ({ doc, canonical }) => {
    const parsed = parseRichText(doc);
    expect(stringifyRichText(parsed)).toBe(JSON.stringify(canonical ?? doc));
    // The canonical form is its own.
    expect(stringifyRichText(parseRichText(parsed))).toBe(
      stringifyRichText(parsed),
    );
  });

  test.each(vectors.invalid)("refuses $note", ({ doc, reason, pointer }) => {
    const error = refusal(doc);
    expect(error?.reason).toBe(reason);
    expect(error?.pointer).toBe(pointer);
  });

  test.each(vectors.lengths)(
    "counts the length of $note",
    ({ json, length }) => {
      expect(richTextLength(json)).toBe(length);
    },
  );

  test("run without String.prototype.isWellFormed, which Safari has only since 16.4", () => {
    const isWellFormed = Object.getOwnPropertyDescriptor(
      String.prototype,
      "isWellFormed",
    );
    Reflect.deleteProperty(String.prototype, "isWellFormed");
    try {
      for (const { doc, canonical } of vectors.valid) {
        expect(stringifyRichText(parseRichText(doc))).toBe(
          JSON.stringify(canonical ?? doc),
        );
      }
      for (const { doc, reason, pointer } of vectors.invalid) {
        expect(refusal(doc)).toMatchObject({ reason, pointer });
      }
    } finally {
      if (isWellFormed !== undefined) {
        Object.defineProperty(String.prototype, "isWellFormed", isWellFormed);
      }
    }
  });
});

/** Returns a document of one paragraph of texts. */
function paragraphOf(...texts: object[]) {
  return { type: "doc", content: [{ type: "paragraph", content: texts }] };
}

describe("the limits", () => {
  test("a document of 100,000 characters of text is taken, and one more is too large", () => {
    // Characters beyond the Basic Multilingual Plane count once each.
    const text = "\u{1F434}".repeat(MAX_CHARS / 2) + "a".repeat(MAX_CHARS / 2);
    expect(() =>
      parseRichText(paragraphOf({ type: "text", text })),
    ).not.toThrow();
    const error = refusal(
      paragraphOf(
        { type: "text", text },
        { type: "text", text: "b", marks: [{ type: "bold" }] },
      ),
    );
    expect(error?.reason).toBe("too_large");
    expect(error?.pointer).toBe("");
    expect(error?.limit).toBe("chars");
  });

  test("a canonical form of more than 1 MiB is too large, with its escapes counted as the characters that they stand for", () => {
    // Empty paragraphs hold no text, and each is 20 bytes of JSON.
    const paragraphs = (n: number) => ({
      type: "doc",
      content: Array.from({ length: n }, () => ({ type: "paragraph" })),
    });
    const fits = Math.floor((MAX_BYTES - 26) / 21);
    const doc = parseRichText(paragraphs(fits));
    expect(richTextLength(stringifyRichText(doc))).toBeLessThanOrEqual(
      MAX_BYTES,
    );
    const error = refusal(paragraphs(fits + 1));
    expect(error?.reason).toBe("too_large");
    expect(error?.limit).toBe("bytes");
    // A document of more than 1 MiB of JSON is too large, as the server
    // reads it, though its canonical form, which joins its texts, is not.
    const texts = (n: number) =>
      paragraphOf(
        ...Array.from({ length: n }, () => ({ type: "text", text: "a" })),
      );
    expect(parseRichText(texts(30_000))).toEqual(
      paragraphOf({ text: "a".repeat(30_000), type: "text" }),
    );
    expect(refusal(texts(60_000))).toMatchObject({
      reason: "too_large",
      pointer: "",
      limit: "bytes",
    });
    // A quote in a link is two bytes of JSON and counts as one.
    const href = `https://example.com/${'"'.repeat(2000)}`;
    expect(richTextLength(JSON.stringify({ href }))).toBe(
      JSON.stringify({ href }).length - 2000,
    );
  });
});

describe("a document that a page builds", () => {
  test("has no field whose value is undefined, as JSON.stringify sends it", () => {
    expect(
      parseRichText({
        attrs: undefined,
        content: [
          { content: undefined, type: "paragraph" },
          {
            content: [{ marks: undefined, text: "a", type: "text" }],
            type: "paragraph",
          },
        ],
        type: "doc",
      }),
    ).toEqual({
      content: [
        { type: "paragraph" },
        { content: [{ text: "a", type: "text" }], type: "paragraph" },
      ],
      type: "doc",
    });
    expect(
      refusal(paragraphOf({ text: undefined, type: "text" })),
    ).toMatchObject({
      reason: "missing_field",
      pointer: "/content/0/content/0",
    });
  });
});

describe("links", () => {
  test.each([
    "https://example.com/",
    "HTTP://Example.COM:8080/a?b#c",
    "https://xn--caf-dma.example/caf%C3%A9",
    "https://[2001:db8::1]:443/",
    "https://[::ffff:192.0.2.1]/",
    "https://192.0.2.1/",
    "https://example.com./",
    "mailto:keeper@example.com",
    "mailto:first.last+tag@xn--caf-dma.example",
  ])("takes %s", (href) => {
    expect(checkHref(href)).toBeUndefined();
  });

  test.each([
    "",
    "javascript:alert(1)",
    "data:text/html,x",
    "/relative",
    "https://",
    "https://example.com@evil.example/",
    "https://café.example/",
    "https://999/",
    "https://127.1/",
    "https://example.0x/",
    "https://example..com/",
    "https://[1:2:3:4:5:6:7:8::]/",
    "https://[::1]x/",
    "https://example.com:65536/",
    "https://example.com/a b",
    "https://example.com/​",
    "https://example.com/%zz",
    "mailto:a@b.com?subject=x",
    "mailto:a@b.com,c@d.com",
    "mailto:a..b@example.com",
    "mailto:a@example.123",
    `https://example.com/${"a".repeat(2048)}`,
  ])("refuses %s", (href) => {
    expect(checkHref(href)).toBeTypeOf("string");
  });
});
