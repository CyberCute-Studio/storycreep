// The rich text of a project's content, the bodies of nodes, the labels of
// connections, and the descriptions of components (PRD FR-TEXT-01, ADR-020):
// a ProseMirror document of a constrained schema, stored as JSON.
// parseRichText checks a document against the schema and its limits
// (NFR-SEC-07) and returns it in its canonical form, as the server does, so
// that the editor knows, before it sends a document, whether the server
// takes it, and why not.
//
// This is the TypeScript twin of the Go package server/internal/richtext
// (Parse and CheckHref). schema/rich-text/README.md states the rules, and
// both run the vectors of schema/rich-text/vectors.json.

import type {
  RichTextBlock,
  RichTextDoc,
  RichTextInline,
  RichTextListItem,
  RichTextMarks,
} from "../api";
import { canonicalAnyId, decodeAnyId, type IdKind } from "../ids";

/**
 * The most bytes of a document's JSON, counted as richTextLength counts
 * them: without the white space between its tokens, and with each escape in
 * a string counted as the character that it stands for. The canonical form
 * that parseRichText returns is held to it, as the JSON that the server
 * reads is.
 */
export const MAX_BYTES = 1 << 20;
/** The most characters, counted as code points, that the texts of a document hold together. */
export const MAX_CHARS = 100_000;
/** How deeply lists nest: a list in no list is at depth 1. */
export const MAX_LIST_DEPTH = 5;
/** The longest URL of a link, in code points. */
export const MAX_HREF_CHARS = 2048;
/** The highest number that an ordered list starts at. */
export const MAX_START = 999_999;
/** The most digits after the point that a value reference shows a number with. */
export const MAX_DECIMALS = 6;

/** Returns the empty document, one empty paragraph, which the editor starts with and the server stores for a text that is not given. */
export function emptyRichText(): RichTextDoc {
  return { content: [{ type: "paragraph" }], type: "doc" };
}

/** What is wrong with a document that parseRichText refuses, as the vectors name it. */
export type RichTextReason =
  | "invalid_json"
  | "wrong_type"
  | "unknown_node"
  | "unknown_mark"
  | "unknown_field"
  | "missing_field"
  | "content"
  | "empty_text"
  | "control_character"
  | "duplicate_mark"
  | "excluded_mark"
  | "link"
  | "reference"
  | "out_of_range"
  | "too_deep"
  | "too_large";

/** What is wrong with a document that parseRichText refuses. */
export class RichTextError extends Error {
  override readonly name = "RichTextError";

  constructor(
    /** What is wrong. */
    readonly reason: RichTextReason,
    /**
     * The JSON Pointer (RFC 6901) of the value that is wrong, within the
     * document: "" for the document itself, or for a limit of the whole
     * document.
     */
    readonly pointer: string,
    message: string,
    /** Of a document that is too large, the limit that it is above. */
    readonly limit?: "bytes" | "chars",
  ) {
    super(
      `rich text: ${pointer === "" ? "the document" : pointer}: ${message}`,
    );
  }
}

/** Returns an object's key as a reference token of a JSON Pointer writes it, with ~ as ~0 and / as ~1. */
function escapeToken(key: string): string {
  return key.replaceAll("~", "~0").replaceAll("/", "~1");
}

/** The most characters of a name that a message writes: a field's name comes from a client. */
const MAX_TOKEN_CHARS = 40;

/** Returns name quoted for a message, cut to its first MAX_TOKEN_CHARS characters. */
function quoted(name: string): string {
  const chars = Array.from(name);
  return JSON.stringify(
    chars.length > MAX_TOKEN_CHARS
      ? `${chars.slice(0, MAX_TOKEN_CHARS).join("")}…`
      : name,
  );
}

type Json = Record<string, unknown>;

function isObject(v: unknown): v is Json {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Reports whether o has the field key, as JSON.stringify writes o: it leaves out a field whose value is undefined, which the server never sees. */
function has(o: Json, key: string): boolean {
  return Object.hasOwn(o, key) && o[key] !== undefined;
}

/** The names of the schema's marks, in alphabetical order, as the canonical form orders them. */
const MARK_ORDER = ["bold", "code", "italic", "link", "strike"] as const;
type MarkName = (typeof MARK_ORDER)[number];

/** Each node's fields. */
const NODE_FIELDS: Readonly<Record<string, readonly string[]>> = {
  doc: ["content", "type"],
  paragraph: ["content", "type"],
  bulletList: ["content", "type"],
  orderedList: ["attrs", "content", "type"],
  listItem: ["content", "type"],
  text: ["marks", "text", "type"],
  hardBreak: ["marks", "type"],
  mention: ["attrs", "marks", "type"],
  valueRef: ["attrs", "marks", "type"],
};

/** Where a node is read, which decides the types that it may have. */
type Where = "root" | "doc" | "itemFirst" | "item" | "list" | "paragraph";

function holds(where: Where, type: string): boolean {
  switch (where) {
    case "root":
      return type === "doc";
    case "doc":
    case "item":
      return (
        type === "paragraph" || type === "bulletList" || type === "orderedList"
      );
    case "itemFirst":
      return type === "paragraph";
    case "list":
      return type === "listItem";
    case "paragraph":
      return (
        type === "text" ||
        type === "hardBreak" ||
        type === "mention" ||
        type === "valueRef"
      );
  }
}

function holdsWhat(where: Where): string {
  switch (where) {
    case "root":
      return "a document is a doc node";
    case "doc":
      return "a document holds paragraphs and lists";
    case "itemFirst":
      return "a list item starts with a paragraph";
    case "item":
      return "a list item holds paragraphs and lists";
    case "list":
      return "a list holds list items";
    case "paragraph":
      return "a paragraph holds text, line breaks, mentions, and value references";
  }
}

/** Returns name after the indefinite article that it takes, as "an orderedList". */
function article(name: string): string {
  return /^[aeiou]/i.test(name) ? `an ${name}` : `a ${name}`;
}

/** Compares two strings in the order of their code points, as Go compares them, rather than of their UTF-16 code units, as sort does. */
function compareCodePoints(a: string, b: string): number {
  const x = Array.from(a);
  const y = Array.from(b);
  for (let i = 0; i < Math.min(x.length, y.length); i++) {
    const d = (x[i].codePointAt(0) ?? 0) - (y[i].codePointAt(0) ?? 0);
    if (d !== 0) {
      return d;
    }
  }
  return x.length - y.length;
}

/** The marks of an inline node. */
interface Marks {
  bold: boolean;
  code: boolean;
  italic: boolean;
  strike: boolean;
  /** The URL of the link that the node is part of, or "". */
  link: string;
}

function noMarks(): Marks {
  return { bold: false, code: false, italic: false, strike: false, link: "" };
}

function hasMark(m: Marks, name: MarkName): boolean {
  return name === "link" ? m.link !== "" : m[name];
}

function sameMarks(a: Marks, b: Marks): boolean {
  return (
    a.bold === b.bold &&
    a.code === b.code &&
    a.italic === b.italic &&
    a.strike === b.strike &&
    a.link === b.link
  );
}

/** Returns marks as the canonical form writes them, in alphabetical order, or undefined for none. */
function wireMarks(m: Marks): RichTextMarks | undefined {
  const out: RichTextMarks = [];
  for (const name of MARK_ORDER) {
    if (!hasMark(m, name)) {
      continue;
    }
    out.push(
      name === "link"
        ? { attrs: { href: m.link }, type: "link" }
        : { type: name },
    );
  }
  return out.length === 0 ? undefined : out;
}

/** An inline node as the parser reads it, before its paragraph's texts are joined. */
type Inline =
  | { kind: "text"; text: string; marks: Marks }
  | { kind: "break"; marks: Marks }
  | { kind: "mention"; id: string; marks: Marks }
  | {
      kind: "value";
      ref: string;
      attribute: string;
      decimals: number | undefined;
      marks: Marks;
    };

/** Returns inlines as the canonical form writes them: neighbouring texts with the same marks joined, as ProseMirror joins them, and each ID in its canonical spelling. */
function wireInlines(inlines: readonly Inline[]): RichTextInline[] {
  const out: RichTextInline[] = [];
  let last: Inline | undefined;
  for (const n of inlines) {
    const marks = wireMarks(n.marks);
    const withMarks = marks === undefined ? {} : { marks };
    switch (n.kind) {
      case "text": {
        const prev = out.at(-1);
        if (
          last?.kind === "text" &&
          prev?.type === "text" &&
          sameMarks(last.marks, n.marks)
        ) {
          out[out.length - 1] = {
            ...withMarks,
            text: prev.text + n.text,
            type: "text",
          };
        } else {
          out.push({ ...withMarks, text: n.text, type: "text" });
        }
        break;
      }
      case "break":
        out.push({ ...withMarks, type: "hardBreak" });
        break;
      case "mention":
        out.push({
          attrs: { id: canonicalAnyId(n.id) },
          ...withMarks,
          type: "mention",
        });
        break;
      case "value":
        out.push({
          attrs: {
            ...(n.attribute === ""
              ? {}
              : { attribute: canonicalAnyId(n.attribute) }),
            ...(n.decimals === undefined ? {} : { decimals: n.decimals }),
            ref: canonicalAnyId(n.ref),
          } as { ref: string },
          ...withMarks,
          type: "valueRef",
        });
        break;
    }
    last = n;
  }
  return out;
}

/**
 * Returns the kind of entity whose public ID id is, in either case, or
 * undefined for what is no ID: a mention's, a value reference's, and its
 * attribute's ID name an entity of the kinds that they may.
 */
export function idKindOf(id: string): IdKind | undefined {
  try {
    return decodeAnyId(id).kind;
  } catch {
    return undefined;
  }
}

/** Reads a document from a JSON value, keeping the pointer of the value that it reads for an error to name. */
class Parser {
  /** The reference tokens of the pointer to the value being read. Reading stops at the first error, which names them. */
  private path: string[] = [];
  /** The code points of the texts read so far. */
  private chars = 0;

  private push(key: string | number) {
    this.path.push(typeof key === "number" ? String(key) : escapeToken(key));
  }

  private pop() {
    this.path.pop();
  }

  private fail(
    reason: RichTextReason,
    message: string,
    limit?: "bytes" | "chars",
  ): RichTextError {
    return new RichTextError(
      reason,
      this.path.map((t) => `/${t}`).join(""),
      message,
      limit,
    );
  }

  /** Reads the object of a node at where, and returns it with its type, once the type may be there and the object has no field that the type does not. */
  private node(v: unknown, where: Where): { o: Json; type: string } {
    if (!isObject(v)) {
      throw this.fail("wrong_type", "a node is not an object");
    }
    if (!has(v, "type")) {
      throw this.fail("missing_field", "the node has no type");
    }
    const type = v.type;
    if (typeof type !== "string") {
      this.push("type");
      throw this.fail("wrong_type", "the node's type is not a string");
    }
    if (!Object.hasOwn(NODE_FIELDS, type)) {
      throw this.fail(
        "unknown_node",
        `${quoted(type)} is not a node of the schema`,
      );
    }
    if (!holds(where, type)) {
      throw this.fail("content", `${holdsWhat(where)}, not ${article(type)}`);
    }
    this.fields(v, `the ${type}`, NODE_FIELDS[type]);
    return { o: v, type };
  }

  /** Checks that o has no field but those allowed (has), naming the first in the order of code points of those that it may not have. */
  private fields(o: Json, what: string, allowed: readonly string[]) {
    let unknown: string | undefined;
    for (const key of Object.keys(o)) {
      if (
        has(o, key) &&
        !allowed.includes(key) &&
        (unknown === undefined || compareCodePoints(key, unknown) < 0)
      ) {
        unknown = key;
      }
    }
    if (unknown !== undefined) {
      this.push(unknown);
      throw this.fail(
        "unknown_field",
        `${what} has no field ${quoted(unknown)}`,
      );
    }
  }

  /** Returns the content of node o, which holds a node when required, and may be missing otherwise. It leaves "content" pushed for the caller to pop. */
  private content(o: Json, type: string, required: boolean): unknown[] {
    if (!has(o, "content")) {
      if (required) {
        throw this.fail("missing_field", `the ${type} has no content`);
      }
      this.push("content");
      return [];
    }
    this.push("content");
    const v = o.content;
    if (!Array.isArray(v)) {
      throw this.fail("wrong_type", `the ${type}'s content is not an array`);
    }
    if (required && v.length === 0) {
      throw this.fail("content", `the ${type} holds nothing`);
    }
    return v;
  }

  /** Returns the attributes of node or mark o, or undefined when it has none and they are not required. It leaves "attrs" pushed when it returns an object. */
  private attrs(o: Json, type: string, required: boolean): Json | undefined {
    if (!has(o, "attrs")) {
      if (required) {
        throw this.fail("missing_field", `the ${type} has no attrs`);
      }
      return undefined;
    }
    this.push("attrs");
    const v = o.attrs;
    if (!isObject(v)) {
      throw this.fail("wrong_type", `the ${type}'s attrs are not an object`);
    }
    return v;
  }

  doc(v: unknown): RichTextDoc {
    const { o, type } = this.node(v, "root");
    return { content: this.blocks(o, type, "doc", 0), type: "doc" };
  }

  /** Reads the blocks of a document, or of a list item when first is "itemFirst", at depth, the number of lists that hold them. */
  private blocks(
    o: Json,
    type: string,
    first: Where,
    depth: number,
  ): RichTextBlock[] {
    const content = this.content(o, type, true);
    const blocks: RichTextBlock[] = [];
    content.forEach((v, i) => {
      this.push(i);
      blocks.push(
        this.block(v, i > 0 && first === "itemFirst" ? "item" : first, depth),
      );
      this.pop();
    });
    this.pop();
    return blocks;
  }

  private block(v: unknown, where: Where, depth: number): RichTextBlock {
    const { o, type } = this.node(v, where);
    if (type === "paragraph") {
      return this.paragraph(o);
    }
    if (depth >= MAX_LIST_DEPTH) {
      throw this.fail(
        "too_deep",
        `lists nest more than ${MAX_LIST_DEPTH} deep`,
      );
    }
    let start = 1;
    if (type === "orderedList") {
      const attrs = this.attrs(o, type, false);
      if (attrs !== undefined) {
        this.fields(attrs, "the orderedList's attrs", ["start"]);
        if (has(attrs, "start")) {
          this.push("start");
          start = this.integer(
            attrs.start,
            "an ordered list's start",
            0,
            MAX_START,
          );
          this.pop();
        }
        this.pop();
      }
    }
    const content = this.content(o, type, true);
    const items: RichTextListItem[] = [];
    content.forEach((v, i) => {
      this.push(i);
      const item = this.node(v, "list");
      items.push({
        content: this.blocks(item.o, "listItem", "itemFirst", depth + 1),
        type: "listItem",
      });
      this.pop();
    });
    this.pop();
    return {
      ...(type === "orderedList" && start !== 1 ? { attrs: { start } } : {}),
      content: items,
      type: type as "bulletList" | "orderedList",
    };
  }

  /** Reads integer v, what, which is between lo and hi, as JSON.parse reads a number: one too large for a double, which it reads as Infinity, is out of range. */
  private integer(v: unknown, what: string, lo: number, hi: number): number {
    if (typeof v !== "number") {
      throw this.fail("wrong_type", `${what} is not a number`);
    }
    if (Number.isFinite(v) && !Number.isInteger(v)) {
      throw this.fail("wrong_type", `${what} is not an integer`);
    }
    if (!(v >= lo && v <= hi)) {
      throw this.fail("out_of_range", `${what} is not between ${lo} and ${hi}`);
    }
    // -0, which JSON.parse reads, is 0.
    return v + 0;
  }

  private paragraph(o: Json): RichTextBlock {
    const content = this.content(o, "paragraph", false);
    const inlines: Inline[] = [];
    content.forEach((v, i) => {
      this.push(i);
      inlines.push(this.inline(v));
      this.pop();
    });
    this.pop();
    const wired = wireInlines(inlines);
    return wired.length === 0
      ? { type: "paragraph" }
      : { content: wired, type: "paragraph" };
  }

  private inline(v: unknown): Inline {
    const { o, type } = this.node(v, "paragraph");
    let inline: Inline;
    switch (type) {
      case "text":
        inline = { kind: "text", text: this.text(o), marks: noMarks() };
        break;
      case "hardBreak":
        inline = { kind: "break", marks: noMarks() };
        break;
      case "mention":
        inline = this.mention(o);
        break;
      default:
        inline = this.value(o);
        break;
    }
    inline.marks = this.marks(o);
    return inline;
  }

  private text(o: Json): string {
    if (!has(o, "text")) {
      throw this.fail("missing_field", "a text has no text");
    }
    this.push("text");
    const s = o.text;
    if (typeof s !== "string") {
      throw this.fail("wrong_type", "a text's text is not a string");
    }
    if (s === "") {
      throw this.fail("empty_text", "a text's text is empty");
    }
    if (hasControl(s)) {
      throw this.fail(
        "control_character",
        "a text holds a control character, a bidirectional control, or a line or paragraph separator; a line break is a hardBreak node",
      );
    }
    this.chars += codePoints(s);
    if (this.chars > MAX_CHARS) {
      this.path = [];
      throw this.fail(
        "too_large",
        `the document's text is longer than ${MAX_CHARS} characters`,
        "chars",
      );
    }
    this.pop();
    return s;
  }

  private mention(o: Json): Inline {
    const attrs = this.attrs(o, "mention", true) as Json;
    this.fields(attrs, "the mention's attrs", ["id"]);
    const { id } = this.id(attrs, "id", "a mention", ["component"]);
    this.pop();
    return { kind: "mention", id, marks: noMarks() };
  }

  private value(o: Json): Inline {
    const attrs = this.attrs(o, "valueRef", true) as Json;
    this.fields(attrs, "the valueRef's attrs", [
      "attribute",
      "decimals",
      "ref",
    ]);
    const { kind, id: ref } = this.id(attrs, "ref", "a value reference", [
      "variable",
      "component",
    ]);
    const named = has(attrs, "attribute");
    let attribute = "";
    if (kind === "variable" && named) {
      this.push("attribute");
      if (typeof attrs.attribute !== "string") {
        throw this.fail(
          "wrong_type",
          "a value reference's attribute is not a string",
        );
      }
      throw this.fail(
        "reference",
        "a reference to a variable names no attribute",
      );
    }
    if (kind === "component" && !named) {
      throw this.fail(
        "reference",
        "a reference to a component names its attribute",
      );
    }
    if (named) {
      attribute = this.id(attrs, "attribute", "a value reference", [
        "attribute_def",
      ]).id;
    }
    let decimals: number | undefined;
    if (has(attrs, "decimals")) {
      this.push("decimals");
      decimals = this.integer(
        attrs.decimals,
        "a value reference's decimals",
        0,
        MAX_DECIMALS,
      );
      this.pop();
    }
    this.pop();
    return { kind: "value", ref, attribute, decimals, marks: noMarks() };
  }

  /** Reads the ID in field key of attrs, which is the ID of an entity of one of the kinds, and returns its kind and the ID as it is written. */
  private id(
    attrs: Json,
    key: string,
    what: string,
    kinds: readonly IdKind[],
  ): { kind: IdKind; id: string } {
    if (!has(attrs, key)) {
      throw this.fail("missing_field", `${what} has no ${key}`);
    }
    this.push(key);
    const s = attrs[key];
    if (typeof s !== "string") {
      throw this.fail("wrong_type", `${what}'s ${key} is not a string`);
    }
    const kind = idKindOf(s);
    if (kind === undefined || !kinds.includes(kind)) {
      const names = kinds.map((k) =>
        article(k === "attribute_def" ? "attribute definition" : k),
      );
      throw this.fail(
        "reference",
        `${what}'s ${key} is not the ID of ${names.join(" or ")}`,
      );
    }
    this.pop();
    return { kind, id: s };
  }

  private marks(o: Json): Marks {
    const m = noMarks();
    if (!has(o, "marks")) {
      return m;
    }
    this.push("marks");
    const a = o.marks;
    if (!Array.isArray(a)) {
      throw this.fail("wrong_type", "marks is not an array");
    }
    a.forEach((mo: unknown, i) => {
      this.push(i);
      if (!isObject(mo)) {
        throw this.fail("wrong_type", "a mark is not an object");
      }
      if (!has(mo, "type")) {
        throw this.fail("missing_field", "the mark has no type");
      }
      const type = mo.type;
      if (typeof type !== "string") {
        this.push("type");
        throw this.fail("wrong_type", "the mark's type is not a string");
      }
      if (!(MARK_ORDER as readonly string[]).includes(type)) {
        throw this.fail(
          "unknown_mark",
          `${quoted(type)} is not a mark of the schema`,
        );
      }
      const name = type as MarkName;
      this.fields(
        mo,
        `the ${name} mark`,
        name === "link" ? ["attrs", "type"] : ["type"],
      );
      if (hasMark(m, name)) {
        throw this.fail("duplicate_mark", `the ${name} mark is there twice`);
      }
      if (name === "link") {
        m.link = this.link(mo);
      } else {
        m[name] = true;
      }
      if (m.code && (m.bold || m.italic || m.strike || m.link !== "")) {
        throw this.fail(
          "excluded_mark",
          "the code mark is not combined with other marks",
        );
      }
      this.pop();
    });
    this.pop();
    return m;
  }

  /** Returns the URL of link mark mo. */
  private link(mo: Json): string {
    const attrs = this.attrs(mo, "link mark", true) as Json;
    this.fields(attrs, "the link's attrs", ["href"]);
    if (!has(attrs, "href")) {
      throw this.fail("missing_field", "a link has no href");
    }
    this.push("href");
    const href = attrs.href;
    if (typeof href !== "string") {
      throw this.fail("wrong_type", "a link's href is not a string");
    }
    const problem = checkHref(href);
    if (problem !== undefined) {
      throw this.fail("link", `a link's href ${problem}`);
    }
    this.pop();
    this.pop();
    return href;
  }
}

/** Returns the number of code points of s. */
function codePoints(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    // The first half of a pair counts the pair.
    if (
      c < 0xdc00 ||
      c > 0xdfff ||
      i === 0 ||
      !isHighSurrogate(s.charCodeAt(i - 1))
    ) {
      n++;
    }
  }
  return n;
}

/** Reports whether the UTF-16 code unit c is the first of a pair of surrogates, which a text holds only before the second (isWellFormed). */
export function isHighSurrogate(c: number): boolean {
  return c >= 0xd800 && c <= 0xdbff;
}

/** Reports whether the UTF-16 code unit c is the second of a pair of surrogates, which a text holds only after the first. */
export function isLowSurrogate(c: number): boolean {
  return c >= 0xdc00 && c <= 0xdfff;
}

/**
 * Reports whether text may not hold the UTF-16 code unit c: a control
 * character (Unicode's Cc) but a tab, a bidirectional control
 * (Bidi_Control), as U+202E RIGHT-TO-LEFT OVERRIDE, which reverses the text
 * after it, or a line or paragraph separator, which a hardBreak or a
 * paragraph stands for. Each of them is in the Basic Multilingual Plane, and
 * Unicode keeps these properties stable, so the ranges are written out
 * rather than read from the engine's tables (input.IsControl).
 */
export function isControlCode(c: number): boolean {
  return (
    (c <= 0x1f && c !== 0x09) ||
    (c >= 0x7f && c <= 0x9f) ||
    c === 0x061c ||
    c === 0x200e ||
    c === 0x200f ||
    (c >= 0x202a && c <= 0x202e) ||
    (c >= 0x2066 && c <= 0x2069) ||
    c === 0x2028 ||
    c === 0x2029
  );
}

function hasControl(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    if (isControlCode(s.charCodeAt(i))) {
      return true;
    }
  }
  return false;
}

/**
 * Reports whether s holds no surrogate that is not one of a pair, as
 * s.isWellFormed() does, which Safari has only since 16.4, so that it runs
 * on iPadOS 16, which PRD §9.9 supports: a regular expression of Unicode
 * reads a pair as the one code point that it is, and no pair as a surrogate.
 */
function isWellFormed(s: string): boolean {
  return !/\p{Surrogate}/u.test(s);
}

/**
 * Reports whether a string of v, a key or a value, at any depth, escapes a
 * surrogate that is not one of a pair, which no UTF-8 holds and which the
 * server reads as U+FFFD, where JSON.parse keeps it. It walks v without
 * recursion, so that no document nests deeply enough to overflow the stack.
 */
function holdsLoneSurrogate(v: unknown): boolean {
  const stack: unknown[] = [v];
  while (stack.length > 0) {
    const x = stack.pop();
    if (typeof x === "string") {
      if (!isWellFormed(x)) {
        return true;
      }
    } else if (Array.isArray(x)) {
      for (const item of x) {
        stack.push(item);
      }
    } else if (isObject(x)) {
      for (const [key, value] of Object.entries(x)) {
        // A field of undefined is none (has).
        if (value === undefined) {
          continue;
        }
        if (!isWellFormed(key)) {
          return true;
        }
        stack.push(value);
      }
    }
  }
  return false;
}

/**
 * Reports whether json is longer than MAX_BYTES as richTextLength counts it,
 * which it reads only of JSON of more than a third of MAX_BYTES in UTF-16
 * code units, since none counts more than 3 bytes.
 */
function overMaxBytes(json: string): boolean {
  return json.length * 3 > MAX_BYTES && richTextLength(json) > MAX_BYTES;
}

/**
 * Returns a document, a JSON value as JSON.parse reads it, or as
 * JSON.stringify writes it, which leaves out a field of undefined, in its
 * canonical form, as the server stores it (schema/rich-text/README.md): no
 * field that holds its default, an inline node's marks in alphabetical
 * order, a paragraph's neighbouring texts with the same marks joined, each
 * ID in its canonical spelling, and every object's fields in alphabetical
 * order, so that stringifyRichText writes what the server writes. The
 * document's JSON, as JSON.stringify writes it, and its canonical form are
 * each held to MAX_BYTES, as the server holds the JSON that it reads and
 * the canonical form.
 *
 * @throws {RichTextError} when the server would refuse the document, with
 * the reason and the pointer that the server gives, or, for one with more
 * than one thing wrong, of one of them.
 */
export function parseRichText(value: unknown): RichTextDoc {
  // The server reads the length first, whatever else is wrong.
  const json = JSON.stringify(value);
  if (json !== undefined && overMaxBytes(json)) {
    throw new RichTextError(
      "too_large",
      "",
      `the document is longer than ${MAX_BYTES} bytes without the white space between its tokens, with each escape counted as the character that it stands for`,
      "bytes",
    );
  }
  if (holdsLoneSurrogate(value)) {
    throw new RichTextError(
      "invalid_json",
      "",
      "the document escapes a lone surrogate, which is not a character",
    );
  }
  const doc = new Parser().doc(value);
  if (overMaxBytes(stringifyRichText(doc))) {
    throw new RichTextError(
      "too_large",
      "",
      `the document's canonical form is longer than ${MAX_BYTES} bytes, with each escape counted as the character that it stands for`,
      "bytes",
    );
  }
  return doc;
}

/** What tryParseRichText makes of a document: its canonical form, or why the server would refuse it. */
export type RichTextResult =
  | { doc: RichTextDoc; error?: undefined }
  | { doc?: undefined; error: RichTextError };

/** Returns what parseRichText returns of value, or the RichTextError that it throws, as a value. */
export function tryParseRichText(value: unknown): RichTextResult {
  try {
    return { doc: parseRichText(value) };
  } catch (error) {
    if (error instanceof RichTextError) {
      return { error };
    }
    throw error;
  }
}

/**
 * Returns a document that parseRichText returned as the JSON that the
 * server writes of it: JSON.stringify writes its fields in the order that
 * parseRichText gave them, which is alphabetical, and escapes no HTML.
 */
export function stringifyRichText(doc: RichTextDoc): string {
  return JSON.stringify(doc);
}

/** Returns the bytes of the UTF-8 of code point cp. */
function utf8Length(cp: number): number {
  return cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
}

/**
 * Returns the length of a document's JSON as MAX_BYTES counts it: in bytes
 * of UTF-8, without the white space that JSON allows between its tokens,
 * and with each escape in a string, as é or \", counted as the bytes of
 * the character that it stands for, an escaped pair of surrogates as the one
 * character that it stands for, and a lone surrogate as U+FFFD. So a
 * document's length depends neither on how its JSON is laid out nor on which
 * characters its encoder escapes. It stops counting once the length is above
 * MAX_BYTES.
 */
export function richTextLength(json: string): number {
  let n = 0;
  let inString = false;
  for (let i = 0; i < json.length && n <= MAX_BYTES; i++) {
    const cp = json.codePointAt(i) ?? 0;
    const width = cp > 0xffff ? 2 : 1;
    if (!inString) {
      if (cp !== 0x20 && cp !== 0x09 && cp !== 0x0a && cp !== 0x0d) {
        n += utf8Length(cp);
        inString = cp === 0x22;
      }
    } else if (cp === 0x22) {
      n++;
      inString = false;
    } else if (cp !== 0x5c) {
      n += utf8Length(cp);
    } else if (i + 5 < json.length && json[i + 1] === "u") {
      const r = hex4(json, i + 2);
      i += 5;
      if (
        r >= 0xd800 &&
        r < 0xdc00 &&
        i + 6 < json.length &&
        json[i + 1] === "\\" &&
        json[i + 2] === "u"
      ) {
        const low = hex4(json, i + 3);
        if (low >= 0xdc00 && low < 0xe000) {
          n += 4;
          i += 6;
          continue;
        }
      }
      // A surrogate that is not one of a pair counts as U+FFFD.
      n += r >= 0xd800 && r < 0xe000 ? 3 : utf8Length(r);
    } else {
      // An escape of one character, as \" or \n.
      n++;
      i++;
    }
    i += width - 1;
  }
  return n;
}

/** Returns the value of the four hexadecimal digits of s at i, each that is not one read as 0. */
function hex4(s: string, i: number): number {
  let r = 0;
  for (let j = i; j < i + 4; j++) {
    const d = Number.parseInt(s[j], 16);
    r = (r << 4) | (Number.isNaN(d) ? 0 : d);
  }
  return r;
}

// Links (PRD NFR-SEC-08). checkHref reads a link by the rules that
// schema/rich-text/README.md states, as CheckHref does, rather than as a URL
// parser does, since parsers differ on what is not a URL.

/**
 * The characters that a link may not hold, which would hide or reorder what
 * a reader sees of it: white space, a control character, a format
 * character, or another character that a renderer shows nothing of. These
 * are the same in Unicode 15.0, the server's tables, as in Unicode 17.0.
 */
const HIDDEN = /[\p{White_Space}\p{Cc}\p{Cf}\p{Default_Ignorable_Code_Point}]/u;

/**
 * Says what is wrong with the URL of a link, or returns undefined: so that
 * no link runs a script or reads a file when it is followed, or hides where
 * it leads. A link has at most MAX_HREF_CHARS characters, none hidden, and
 * each % in it starts an escape; it is a mailto URL of one address and
 * nothing else, or an http or https URL whose authority is a host of ASCII
 * and a port. A scheme is read regardless of ASCII case.
 */
export function checkHref(href: string): string | undefined {
  if (href === "") {
    return "is empty";
  }
  if (!isWellFormed(href)) {
    return "is not UTF-8";
  }
  if (codePoints(href) > MAX_HREF_CHARS) {
    return `is longer than ${MAX_HREF_CHARS} characters`;
  }
  if (HIDDEN.test(href)) {
    return "holds white space, a control character, a format character, or another character that is not shown";
  }
  if (!validEscapes(href)) {
    return "holds a % that starts no escape of two hexadecimal digits";
  }
  const colon = href.indexOf(":");
  const scheme = colon < 0 ? href : href.slice(0, colon);
  const rest = colon < 0 ? "" : href.slice(colon + 1);
  switch (scheme.replace(/[A-Z]/g, (c) => c.toLowerCase())) {
    case "http":
    case "https": {
      if (!rest.startsWith("//")) {
        return "has no host";
      }
      const authority = rest.slice(2);
      const end = authority.search(/[/?#]/);
      return authorityProblem(end < 0 ? authority : authority.slice(0, end));
    }
    case "mailto":
      return mailtoProblem(rest);
  }
  return "is not an http, https, or mailto URL";
}

function validEscapes(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    if (
      s[i] === "%" &&
      (i + 2 >= s.length || !isHexDigit(s[i + 1]) || !isHexDigit(s[i + 2]))
    ) {
      return false;
    }
  }
  return true;
}

const isDigit = (c: string) => c >= "0" && c <= "9";
const isHexDigit = (c: string) =>
  isDigit(c) || (c >= "a" && c <= "f") || (c >= "A" && c <= "F");
const isLetter = (c: string) =>
  (c >= "a" && c <= "z") || (c >= "A" && c <= "Z");

/** Reports whether every character of s, by its code points, is one that ok takes. */
function only(s: string, ok: (c: string) => boolean): boolean {
  for (const c of s) {
    if (!ok(c)) {
      return false;
    }
  }
  return true;
}

/** Says what is wrong with what follows the colon of a mailto link: one plain ASCII address and nothing else. */
function mailtoProblem(rest: string): string | undefined {
  const at = rest.indexOf("@");
  if (
    !only(rest, (c) => isLetter(c) || isDigit(c) || "_+-.@".includes(c)) ||
    rest.split("@").length > 2
  ) {
    return "is a mailto URL with more than an address: a mailto link names one address and nothing else, of ASCII letters, digits, _, +, -, and . around one @, without a query (?), an escape (%), a fragment (#), a second address, quotes, brackets, or a character beyond ASCII";
  }
  if (at < 0) {
    return "is a mailto URL without an address: a local part, an @, and a domain";
  }
  if (!isLocalPart(rest.slice(0, at))) {
    return "is a mailto URL whose local part, before the @, is not atoms of ASCII letters, digits, _, +, and - that single dots separate";
  }
  if (!isDomain(rest.slice(at + 1))) {
    return "is a mailto URL whose domain, after the @, is not labels of ASCII letters, digits, and - that start and end with a letter or a digit, which single dots separate, the last not only digits";
  }
  return undefined;
}

function isLocalPart(s: string): boolean {
  return s
    .split(".")
    .every(
      (atom) =>
        atom !== "" &&
        only(atom, (c) => isLetter(c) || isDigit(c) || "_+-".includes(c)),
    );
}

function isDomain(domain: string): boolean {
  const labels = domain.split(".");
  return (
    labels.every(
      (label) =>
        label !== "" &&
        !label.startsWith("-") &&
        !label.endsWith("-") &&
        only(label, (c) => isLetter(c) || isDigit(c) || c === "-"),
    ) && !only(labels[labels.length - 1], isDigit)
  );
}

/** The highest port: the highest number of 16 bits. */
const MAX_PORT = 65_535;

/** Says what is wrong with the authority of an http or https link, a host and a port, or returns undefined. */
function authorityProblem(authority: string): string | undefined {
  if (authority.includes("@")) {
    return "has a userinfo, before an @, which could show another host than the one that it leads to";
  }
  let port = "";
  if (authority.startsWith("[")) {
    const end = authority.indexOf("]");
    if (end < 0) {
      return "has an IPv6 address without its closing bracket";
    }
    const after = authority.slice(end + 1);
    if (after !== "") {
      if (!after.startsWith(":")) {
        return "has a host followed by more than a port";
      }
      port = after.slice(1);
    }
    const problem = ipv6Problem(authority.slice(1, end));
    if (problem !== undefined) {
      return problem;
    }
  } else {
    let host = authority;
    const colon = authority.lastIndexOf(":");
    if (colon >= 0) {
      host = authority.slice(0, colon);
      port = authority.slice(colon + 1);
    }
    const problem = nameProblem(host);
    if (problem !== undefined) {
      return problem;
    }
  }
  if (port === "") {
    return undefined;
  }
  if (!only(port, isDigit)) {
    return "has a port that is not a number";
  }
  if (Number(port) > MAX_PORT) {
    return `has a port above ${MAX_PORT}`;
  }
  return undefined;
}

function ipv6Problem(address: string): string | undefined {
  if (address === "") {
    return "has no host";
  }
  if (!only(address, (c) => c === ":" || c === "." || isHexDigit(c))) {
    return "has an IPv6 address in brackets that holds what no address does";
  }
  if (!isIPv6(address)) {
    return "has in brackets what is not an IPv6 address";
  }
  return undefined;
}

/** Reports whether s is an IPv4 address as Go's netip reads one: four numbers from 0 to 255 in decimal, without leading zeros, separated by dots. */
function isIPv4(s: string): boolean {
  const parts = s.split(".");
  return (
    parts.length === 4 &&
    parts.every(
      (p) =>
        p.length >= 1 &&
        p.length <= 3 &&
        only(p, isDigit) &&
        (p === "0" || !p.startsWith("0")) &&
        Number(p) <= 255,
    )
  );
}

/**
 * Reports whether s is an IPv6 address as RFC 4291 (section 2.2) writes it,
 * as Go's netip reads one: eight groups of one to four hexadecimal digits,
 * separated by colons, of which one run of one or more groups of zeros may
 * be written as ::, and the last two of which may be written as an IPv4
 * address.
 */
function isIPv6(s: string): boolean {
  let groups = 8;
  let text = s;
  const lastColon = s.lastIndexOf(":");
  if (lastColon < 0) {
    return false;
  }
  if (s.slice(lastColon + 1).includes(".")) {
    if (!isIPv4(s.slice(lastColon + 1))) {
      return false;
    }
    groups = 6;
    // The IPv4 address stands in for the last two groups; what is before it
    // ends with its colon, or with the colons of ::.
    text = s.slice(0, lastColon + 1);
    text = text.endsWith("::") ? text : text.slice(0, -1);
    if (text === "") {
      return false;
    }
  }
  const halves = text.split("::");
  if (halves.length > 2) {
    return false;
  }
  const groupsOf = (half: string) => (half === "" ? [] : half.split(":"));
  const valid = (g: string) =>
    g.length >= 1 && g.length <= 4 && only(g, isHexDigit);
  if (halves.length === 1) {
    const all = groupsOf(halves[0]);
    return all.length === groups && all.every(valid);
  }
  const head = groupsOf(halves[0]);
  const tail = groupsOf(halves[1]);
  // :: stands for at least one group of zeros.
  return (
    head.length + tail.length < groups && head.every(valid) && tail.every(valid)
  );
}

/** Says what is wrong with a host's name, or returns undefined: ASCII, labels that are not empty, and an IPv4 address where a browser would read one. */
function nameProblem(name: string): string | undefined {
  if (name === "") {
    return "has no host";
  }
  if (
    !only(
      name,
      (c) => isLetter(c) || isDigit(c) || "-._~!$&'()*+,;=".includes(c),
    )
  ) {
    return "has a host with a character that a host may not hold, as one beyond ASCII: an internationalized host is written as IDNA writes it in ASCII (xn--)";
  }
  const trimmed = name.endsWith(".") ? name.slice(0, -1) : name;
  const labels = trimmed.split(".");
  if (labels.includes("")) {
    return "has a host with an empty label, before a dot that does not end it";
  }
  const last = labels[labels.length - 1];
  if ((only(last, isDigit) || hexNumber(last)) && !isIPv4(trimmed)) {
    return "has a host that a browser reads as an IPv4 address, which is not four numbers from 0 to 255 in decimal without leading zeros";
  }
  return undefined;
}

/** Reports whether a label is 0x or 0X followed by hexadecimal digits or by none, which a browser reads as a number. */
function hexNumber(label: string): boolean {
  return (
    (label.startsWith("0x") || label.startsWith("0X")) &&
    only(label.slice(2), isHexDigit)
  );
}
