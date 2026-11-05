// What the editor makes of HTML that is pasted or dropped into it (PRD
// FR-TEXT-01 AC2): HTML of only what the schema holds, which the editor's
// parser then reads. Whatever else the HTML has, from Word, Google Docs, or
// a web page, gives supported marks or plain text, and never HTML: its
// paragraphs, headings, and table cells become paragraphs; its lists, of
// list elements or of Word's paragraphs of a list (mso-list), lists, nested
// by their levels; its bold, italic, struck-through, and code text, by
// element or by style, the marks; its links that a link may lead to
// (storedHref) links; its line breaks hard breaks; and its white space,
// which a pre or a white-space style keeps, kept. What no text shows, as a
// script, a style sheet, an image, a comment, a hidden element, or the
// bullet or number of a list item that Word writes out, is left out.

import { MAX_LIST_DEPTH } from "@storycreep/shared/richtext";
import { storedHref } from "./link";
import { isMentionElement, isValueRefElement, startOf } from "./schema";

/** The marks of a run of text, which a link and the code mark limit (marksOf). */
interface Marks {
  bold: boolean;
  italic: boolean;
  strike: boolean;
  code: boolean;
  /** The URL of the link that the text is part of, as a link stores it. */
  href?: string;
}

/** What a paragraph holds: text, whose white space is kept where its HTML keeps it (Context), a line break, or a mention or a value reference that the editor itself wrote. */
type Run =
  | { kind: "text"; text: string; marks: Marks; keep: boolean }
  | { kind: "break" }
  | { kind: "chip"; attrs: [string, string][]; marks: Marks };

/** An item of a list, at a level, from 1. */
interface ItemOf {
  level: number;
  ordered: boolean;
  /**
   * The number of the item, where the HTML gives it: of the first item of an
   * ol that the walk reads, the ol's start, and of a paragraph of Word's
   * list, its number; or undefined, as of a later item of an ol, which
   * continues its list.
   */
  start?: number;
  /**
   * Whether the item is one of ProseMirror's HTML that no list element holds,
   * as of a copy within one list item, whose marker names the list, which
   * the cleaned HTML leaves it out of too.
   */
  loose?: boolean;
}

/** A paragraph of the HTML: one of the document, a list item's first, or one that continues the list item at a level. */
interface Para {
  runs: Run[];
  item?: ItemOf;
  continues?: number;
}

/** What the HTML's elements say of the text in them, as the walk reads them. */
interface Context {
  marks: Marks;
  /** The lists that hold the text. */
  lists: number;
  /** Whether the innermost list is numbered. */
  ordered: boolean;
  /** The start of the innermost list, as its element gives it. */
  start?: number;
  /** The innermost list's element, whose first item that the walk reads has its start. */
  list?: Element;
  /** Whether white space and line breaks are kept, as in a pre, or in an element whose white-space is one of KEPT_SPACE. */
  keep: boolean;
}

/** The values of the white-space style that keep white space and line breaks, as a pre does, and as Google Docs's and Word Online's text has them. */
const KEPT_SPACE = new Set(["pre", "pre-wrap", "break-spaces"]);

/** The elements whose content no text shows, or that hold no text of the document's. */
const SKIPPED = new Set([
  "applet",
  "area",
  "audio",
  "base",
  "button",
  "canvas",
  "datalist",
  "embed",
  "frame",
  "frameset",
  "head",
  "iframe",
  "img",
  "input",
  "link",
  "map",
  "math",
  "meta",
  "noembed",
  "noframes",
  "noscript",
  "object",
  "option",
  "param",
  "picture",
  "rp",
  "rt",
  "script",
  "select",
  "source",
  "style",
  "svg",
  "template",
  "textarea",
  "title",
  "track",
  "video",
]);

/** The elements that begin and end a block of their own, and so a paragraph. */
const BLOCKS = new Set([
  "address",
  "article",
  "aside",
  "blockquote",
  "body",
  "caption",
  "center",
  "dd",
  "details",
  "dialog",
  "dir",
  "div",
  "dl",
  "dt",
  "fieldset",
  "figcaption",
  "figure",
  "footer",
  "form",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "header",
  "hgroup",
  "hr",
  "legend",
  "li",
  "main",
  "menu",
  "nav",
  "ol",
  "p",
  "pre",
  "section",
  "summary",
  "table",
  "tbody",
  "td",
  "tfoot",
  "th",
  "thead",
  "tr",
  "ul",
]);

/** The elements of a block of text, which ProseMirror writes of a paragraph, a heading, or a block of code. */
const TEXTBLOCKS = new Set(["h1", "h2", "h3", "h4", "h5", "h6", "p", "pre"]);

/** The white space of HTML, which a paragraph's text collapses. */
const HTML_SPACE = /^[ \t\n\f\r]*$/;

/** Returns a run's marks as the schema combines them: a link with any but the code mark, and the code mark with no other. */
function marksOf(marks: Marks): Marks {
  if (marks.href !== undefined) {
    return { ...marks, code: false };
  }
  if (marks.code) {
    return { bold: false, italic: false, strike: false, code: true };
  }
  return marks;
}

/** Returns the value of a property of an element's inline style, lowercased, or "" for none. */
function styleOf(element: Element, property: string): string {
  return (
    (element as HTMLElement).style
      ?.getPropertyValue(property)
      .trim()
      .toLowerCase() ?? ""
  );
}

/** Returns what the element says of bold: true or false where it sets it, by its name or its style, and undefined where it does not. */
function boldOf(element: Element, name: string): boolean | undefined {
  const weight = styleOf(element, "font-weight");
  if (weight !== "") {
    if (weight === "bold" || weight === "bolder") {
      return true;
    }
    if (weight === "normal" || weight === "lighter") {
      return false;
    }
    const n = Number(weight);
    if (Number.isFinite(n)) {
      return n >= 600;
    }
  }
  return name === "b" || name === "strong" ? true : undefined;
}

function italicOf(element: Element, name: string): boolean | undefined {
  const style = styleOf(element, "font-style");
  if (
    style === "italic" ||
    style === "oblique" ||
    style.startsWith("oblique ")
  ) {
    return true;
  }
  if (style === "normal") {
    return false;
  }
  return name === "i" || name === "em" ? true : undefined;
}

/** Reports whether the element strikes its text through: a line through it is drawn over what it holds, and cannot be taken off within it. */
function strikeOf(element: Element, name: string): boolean {
  return (
    name === "s" ||
    name === "strike" ||
    name === "del" ||
    styleOf(element, "text-decoration").includes("line-through") ||
    styleOf(element, "text-decoration-line").includes("line-through")
  );
}

/** Reports whether the element is not shown, by its style or its hidden attribute, or as a dialog that is not open. */
function hidden(element: Element): boolean {
  return (
    element.hasAttribute("hidden") ||
    (element.localName.toLowerCase() === "dialog" &&
      !element.hasAttribute("open")) ||
    styleOf(element, "display") === "none" ||
    styleOf(element, "visibility") === "hidden" ||
    /mso-hide\s*:\s*all/i.test(element.getAttribute("style") ?? "")
  );
}

/**
 * Reports whether the cleaning leaves an element out, with what it holds: an
 * element that no text shows or that holds none of the document's
 * (SKIPPED), a hidden one, the bullet or number of a list item that Word
 * writes out, in a span of mso-list:Ignore, which the list shows, and the
 * line break that a browser adds where a selection that it copied ends at
 * the start of a line, which marks where the selection ended.
 */
function dropped(element: Element, name: string): boolean {
  return (
    SKIPPED.has(name) ||
    hidden(element) ||
    (name === "span" &&
      /mso-list\s*:\s*ignore/i.test(element.getAttribute("style") ?? "")) ||
    (name === "br" && element.classList.contains("Apple-interchange-newline"))
  );
}

/** Returns the level of a list item that its element gives, as Google Docs's aria-level and Word's data-aria-level do, or undefined for none. */
function levelOf(element: Element): number | undefined {
  const level = Number(
    element.getAttribute("aria-level") ??
      element.getAttribute("data-aria-level") ??
      Number.NaN,
  );
  return Number.isInteger(level) && level >= 1 ? level : undefined;
}

/**
 * Returns the list item that a paragraph of Word is, by its mso-list style,
 * as "mso-list:l0 level2 lfo1", with the bullet or the number that Word
 * writes out in a span of mso-list:Ignore, which tells whether the list is
 * numbered and where it starts; or undefined for a paragraph that is no
 * list's.
 */
function wordItemOf(element: Element): ItemOf | undefined {
  const match = /mso-list\s*:\s*l\d+\s+level(\d+)/i.exec(
    element.getAttribute("style") ?? "",
  );
  if (match === null) {
    return undefined;
  }
  const marker =
    Array.from(element.querySelectorAll("span"))
      .find((span) =>
        /mso-list\s*:\s*ignore/i.test(span.getAttribute("style") ?? ""),
      )
      ?.textContent?.trim() ?? "";
  // The item's number is the last of those that the marker holds, as 2 of
  // 1.2., where the numbers of the items that hold it go before it.
  const numbered = /^\(?(?:\d+\.)*([0-9]+|[a-z]{1,4})[.)]/i.exec(marker);
  return {
    level: Number(match[1]),
    ordered: numbered !== null,
    start:
      numbered !== null && /^\d+$/.test(numbered[1])
        ? startOf(numbered[1])
        : undefined,
  };
}

/** Reads the paragraphs of HTML's elements, in order. */
class Reader {
  readonly paras: Para[] = [];
  private current: Para | undefined;
  /** The list item whose first paragraph has not begun yet. */
  private pendingItem: ItemOf | undefined;
  /** The level of the list item whose paragraphs a paragraph continues, or undefined outside any. */
  private itemLevel: number | undefined;
  /**
   * Whether the HTML is ProseMirror's, as the editor copies it, whose every
   * block of text is a paragraph, as one that holds nothing or only white
   * space, which a paragraph of another app's HTML that shows nothing is
   * not.
   */
  private readonly faithful: boolean;
  /** The list elements of which the walk has read an item. */
  private readonly begun = new Set<Element>();

  constructor(faithful: boolean) {
    this.faithful = faithful;
  }

  /** Ends the paragraph that inline content went to. */
  private end() {
    this.current = undefined;
  }

  /** Returns the paragraph that inline content goes to, which begins with the first. */
  private paragraph(): Para {
    if (this.current === undefined) {
      const para: Para = { runs: [] };
      if (this.pendingItem !== undefined) {
        para.item = this.pendingItem;
        this.pendingItem = undefined;
      } else if (this.itemLevel !== undefined) {
        para.continues = this.itemLevel;
      }
      this.paras.push(para);
      this.current = para;
    }
    return this.current;
  }

  read(node: Node, ctx: Context) {
    if (node.nodeType === Node.TEXT_NODE) {
      this.text(node.nodeValue ?? "", ctx);
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) {
      // A comment, as Word's conditional ones, or a processing instruction.
      return;
    }
    const element = node as Element;
    const name = element.localName.toLowerCase();
    if (dropped(element, name)) {
      return;
    }
    if (name === "br") {
      this.paragraph().runs.push({ kind: "break" });
      return;
    }
    const chip = chipOf(element);
    if (chip !== undefined) {
      this.paragraph().runs.push({
        kind: "chip",
        attrs: chip,
        marks: marksOf(ctx.marks),
      });
      return;
    }
    const inner = this.contextOf(element, name, ctx);
    if (!BLOCKS.has(name)) {
      this.children(element, inner);
      return;
    }
    // A list item that holds a list before any text of its own has an
    // empty first paragraph, before the list's items.
    if (
      this.pendingItem !== undefined &&
      (name === "li" || inner.lists > ctx.lists)
    ) {
      this.paragraph();
    }
    this.end();
    const outerItem = this.pendingItem;
    const outerLevel = this.itemLevel;
    // A list item, of a list's element or a paragraph of Word's list, whose
    // first paragraph its content begins, and which its later paragraphs
    // continue.
    let item: ItemOf | undefined;
    if (name === "li") {
      const first = ctx.list !== undefined && !this.begun.has(ctx.list);
      if (ctx.list !== undefined) {
        this.begun.add(ctx.list);
      }
      item = {
        level: levelOf(element) ?? Math.max(ctx.lists, 1),
        ordered: ctx.ordered,
        start: ctx.ordered && first ? (ctx.start ?? 1) : undefined,
        loose: this.faithful && ctx.lists === 0,
      };
    } else if (name === "p" || name === "div") {
      item = wordItemOf(element);
    }
    if (item !== undefined) {
      item.level = Math.min(item.level, MAX_LIST_DEPTH);
      this.pendingItem = item;
      this.itemLevel = name === "li" ? item.level : undefined;
    }
    if (this.faithful && TEXTBLOCKS.has(name)) {
      this.paragraph();
    }
    this.children(element, inner);
    if (item !== undefined) {
      if (this.pendingItem === item) {
        // An empty item is an empty paragraph of its list.
        this.paragraph();
      }
      this.pendingItem = outerItem;
      this.itemLevel = outerLevel;
    }
    this.end();
  }

  /** Reads the children of element that show: of a details that is not open, only its summary. */
  private children(element: Element, ctx: Context) {
    const closed =
      element.localName.toLowerCase() === "details" &&
      !element.hasAttribute("open");
    const shown = closed
      ? Array.from(element.children)
          .filter((child) => child.localName.toLowerCase() === "summary")
          .slice(0, 1)
      : Array.from(element.childNodes);
    for (const child of shown) {
      this.read(child, ctx);
    }
  }

  /** Returns what an element says of the text in it, over what holds it. */
  private contextOf(element: Element, name: string, ctx: Context): Context {
    const marks = { ...ctx.marks };
    marks.bold = boldOf(element, name) ?? marks.bold;
    marks.italic = italicOf(element, name) ?? marks.italic;
    marks.strike = marks.strike || strikeOf(element, name);
    marks.code =
      marks.code || ["code", "kbd", "samp", "tt", "pre"].includes(name);
    if (name === "a") {
      const href = element.getAttribute("href");
      // A link within a link is the inner one, as a browser follows it.
      marks.href =
        href === null ? marks.href : (storedHref(href) ?? marks.href);
    }
    // White space is kept in a pre, and as an element's white-space, which
    // what it holds takes, says.
    const space = styleOf(element, "white-space");
    const inner: Context = {
      ...ctx,
      marks,
      keep: space === "" ? ctx.keep || name === "pre" : KEPT_SPACE.has(space),
    };
    if (name === "ul" || name === "ol" || name === "menu" || name === "dir") {
      inner.lists = ctx.lists + 1;
      inner.ordered = name === "ol";
      inner.start =
        name === "ol" && element.hasAttribute("start")
          ? startOf(element.getAttribute("start"))
          : undefined;
      inner.list = element;
    } else if (name === "li") {
      // The start of a list is its first item's. An item that no list holds
      // is one of a list too, which the lists within it nest in.
      inner.start = undefined;
      inner.list = undefined;
      inner.lists = Math.max(ctx.lists, 1);
    }
    return inner;
  }

  private text(value: string, ctx: Context) {
    // White space between blocks is no paragraph, but where it is kept.
    if (this.current === undefined && !ctx.keep && HTML_SPACE.test(value)) {
      return;
    }
    const marks = marksOf(ctx.marks);
    if (!ctx.keep) {
      this.paragraph().runs.push({
        kind: "text",
        text: value,
        marks,
        keep: false,
      });
      return;
    }
    // Kept text keeps its lines, each after a line break, and their white
    // space.
    value.split(/\r\n?|\n/).forEach((line, i) => {
      const para = this.paragraph();
      if (i > 0) {
        para.runs.push({ kind: "break" });
      }
      if (line !== "") {
        para.runs.push({ kind: "text", text: line, marks, keep: true });
      }
    });
  }
}

/**
 * Returns the attributes of a mention or a value reference that the editor
 * wrote, which a paste keeps, or undefined for another element: one of
 * another app's, as a mention of a user, which names no component, is read
 * as the other inline elements are, so that its text stays.
 */
function chipOf(element: Element): [string, string][] | undefined {
  if (element.localName !== "span") {
    return undefined;
  }
  const type = element.getAttribute("data-type");
  const names =
    type === "mention" && isMentionElement(element)
      ? ["data-id"]
      : type === "value-ref" && isValueRefElement(element)
        ? ["data-ref", "data-attribute", "data-decimals"]
        : undefined;
  if (names === undefined) {
    return undefined;
  }
  const attrs: [string, string][] = [["data-type", type as string]];
  for (const name of names) {
    const value = element.getAttribute(name);
    if (value !== null) {
      attrs.push([name, value]);
    }
  }
  return attrs;
}

/** A list as the cleaned HTML writes it. */
interface List {
  ordered: boolean;
  start: number;
  /** Whether the list's items are loose ones (ItemOf), which it writes without the list's element. */
  loose: boolean;
  items: Block[][];
}

type Block = { kind: "p"; runs: Run[] } | { kind: "list"; list: List };

/**
 * Returns the blocks of the paragraphs, each list item's in its list, nested
 * by their levels: an item one level deeper than the item before it begins
 * a list within that item, one at the same level of another kind of list, a
 * loose one at the top level, or one whose number (ItemOf) is not the one
 * after the list's last item, a list of its own, and one at a level that
 * skips a level is at the next. So Word Online's lists of one item each,
 * each ol numbered from where the one before it ends, are one list. A
 * paragraph of the editor's own HTML (faithful) keeps its white space, as
 * ProseMirror reads it.
 */
function blocksOf(paras: readonly Para[], faithful: boolean): Block[] {
  const top: Block[] = [];
  const open: List[] = [];
  const lastItem = (list: List) => list.items[list.items.length - 1];
  for (const para of paras) {
    const block = {
      kind: "p" as const,
      runs: faithful ? para.runs : tidy(para.runs),
    };
    if (para.item !== undefined) {
      const level = Math.min(para.item.level, open.length + 1);
      const loose = para.item.loose === true && level === 1;
      open.length = Math.min(open.length, level);
      const at = open[level - 1];
      if (
        open.length === level &&
        (at.ordered !== para.item.ordered ||
          at.loose !== loose ||
          (para.item.start !== undefined &&
            para.item.start !== at.start + at.items.length))
      ) {
        open.length = level - 1;
      }
      if (open.length < level) {
        const list: List = {
          ordered: para.item.ordered,
          start: para.item.start ?? 1,
          loose,
          items: [],
        };
        (open.length === 0 ? top : lastItem(open[open.length - 1])).push({
          kind: "list",
          list,
        });
        open.push(list);
      }
      open[open.length - 1].items.push([block]);
    } else if (para.continues !== undefined && open.length > 0) {
      open.length = Math.min(open.length, para.continues);
      lastItem(open[open.length - 1]).push(block);
    } else {
      open.length = 0;
      top.push(block);
    }
  }
  return top;
}

/**
 * Returns a paragraph's runs as a block shows them: without the line break
 * that ends it, which a browser shows nothing of, and without the white
 * space at its end, as the no-break spaces of Word's empty paragraphs and of
 * Word Online's ends of paragraphs.
 */
function tidy(runs: readonly Run[]): Run[] {
  const out = runs.at(-1)?.kind === "break" ? runs.slice(0, -1) : [...runs];
  while (out.length > 0) {
    const last = out[out.length - 1];
    if (last.kind !== "text") {
      break;
    }
    const text = last.text.replace(/[\s ]+$/u, "");
    if (text !== "") {
      out[out.length - 1] = { ...last, text };
      break;
    }
    out.pop();
  }
  return out;
}

/**
 * Writes a run into parent, within the elements of its marks: text whose
 * white space is kept within a span of white-space pre-wrap, which the
 * editor's parser reads without collapsing its white space, as a line's
 * indentation.
 */
function writeRun(doc: Document, parent: Element, run: Run) {
  if (run.kind === "break") {
    parent.append(doc.createElement("br"));
    return;
  }
  let node: Node;
  if (run.kind === "text") {
    node = doc.createTextNode(run.text);
    if (run.keep) {
      const span = doc.createElement("span");
      span.setAttribute("style", "white-space: pre-wrap");
      span.append(node);
      node = span;
    }
  } else {
    const span = doc.createElement("span");
    for (const [name, value] of run.attrs) {
      span.setAttribute(name, value);
    }
    node = span;
  }
  const { marks } = run;
  for (const [on, tag] of [
    [marks.code, "code"],
    [marks.strike, "s"],
    [marks.italic, "em"],
    [marks.bold, "strong"],
  ] as const) {
    if (on) {
      const element = doc.createElement(tag);
      element.append(node);
      node = element;
    }
  }
  if (marks.href !== undefined) {
    const a = doc.createElement("a");
    a.setAttribute("href", marks.href);
    a.append(node);
    node = a;
  }
  parent.append(node);
}

function writeBlocks(doc: Document, parent: Element, blocks: readonly Block[]) {
  for (const block of blocks) {
    if (block.kind === "p") {
      const p = doc.createElement("p");
      for (const run of block.runs) {
        writeRun(doc, p, run);
      }
      parent.append(p);
      continue;
    }
    const items = block.list.items.map((item) => {
      const li = doc.createElement("li");
      writeBlocks(doc, li, item);
      return li;
    });
    if (block.list.loose) {
      // Loose items are written as they were, so that ProseMirror puts them
      // in the list that the marker of the slice names.
      parent.append(...items);
      continue;
    }
    const list = doc.createElement(block.list.ordered ? "ol" : "ul");
    if (block.list.ordered && block.list.start !== 1) {
      list.setAttribute("start", String(block.list.start));
    }
    list.append(...items);
    parent.append(list);
  }
}

/**
 * Returns the marker of a slice that ProseMirror wrote on the first element
 * of what it copied (data-pm-slice: how deep the slice is open at its start
 * and at its end, and the nodes around it) as it holds of the cleaned HTML,
 * or undefined for HTML without one, or with one that no editor of this
 * schema writes: one that elements wrap that are none of its nodes, as a
 * table's around its rows (-N), whose cells the cleaning makes paragraphs
 * of. The cleaning puts a slice of inline content, as of a selected
 * mention, in a paragraph, and so the slice is open one deeper at both ends;
 * one whose first element the cleaning leaves out, as another editor's
 * image, begins with what follows it, as ProseMirror reads it too; and
 * loose list items stay loose (ItemOf).
 */
function sliceOf(source: Document): string | undefined {
  const element = source.querySelector("[data-pm-slice]");
  const match =
    element === null
      ? null
      : /^(\d+) (\d+)(?: -(\d+))? (.*)/.exec(
          element.getAttribute("data-pm-slice") ?? "",
        );
  if (element === null || match === null || match[3] !== undefined) {
    return undefined;
  }
  const wrapped = wrapsInline(element) ? 1 : 0;
  return `${Number(match[1]) + wrapped} ${Number(match[2]) + wrapped} ${match[4]}`;
}

/** Reports whether the cleaning puts what element holds in a paragraph of its own making: the element is kept, and neither it nor what holds it is a block. */
function wrapsInline(element: Element): boolean {
  for (
    let e: Element | null = element;
    e !== null && e !== element.ownerDocument.body;
    e = e.parentElement
  ) {
    const name = e.localName.toLowerCase();
    if (BLOCKS.has(name) || dropped(e, name)) {
      return false;
    }
  }
  return true;
}

/**
 * Returns pasted HTML as HTML of only what the schema holds: paragraphs,
 * line breaks, lists and their items, the elements of the marks, links of
 * URLs that a link may lead to, the spans of the editor's mentions and value
 * references, and spans of text whose white space is kept (writeRun). The
 * HTML is read in a document of its own, without a window, so that none of
 * it runs, loads, or applies a style; and the marker of a slice that
 * ProseMirror wrote, data-pm-slice, is kept on the first block, as it holds
 * of the cleaned HTML (sliceOf), with every block of text a paragraph and
 * its white space kept, and list items that no list element holds kept so,
 * so that what is copied from the editor is pasted as it was cut from its
 * blocks.
 */
export function cleanPastedHTML(html: string): string {
  const source = new DOMParser().parseFromString(html, "text/html");
  const slice = sliceOf(source);
  const reader = new Reader(slice !== undefined);
  reader.read(source.body, {
    marks: { bold: false, italic: false, strike: false, code: false },
    lists: 0,
    ordered: false,
    keep: false,
  });
  const out = document.implementation.createHTMLDocument("");
  writeBlocks(out, out.body, blocksOf(reader.paras, slice !== undefined));
  if (slice !== undefined) {
    out.body.firstElementChild?.setAttribute("data-pm-slice", slice);
  }
  return out.body.innerHTML;
}
