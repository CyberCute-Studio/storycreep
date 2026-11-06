// What the editor makes of HTML that is pasted or dropped into it (PRD
// FR-TEXT-01 AC2): HTML of only what the schema holds, which the editor's
// parser then reads. Whatever else the HTML has, from Word, Google Docs, or
// a web page, gives supported marks or plain text, and never HTML: its
// paragraphs, headings, and table cells become paragraphs; its lists, of
// list elements or of Word's paragraphs of a list (mso-list), lists, nested
// by their levels; its bold, italic, struck-through, and code text, by
// element or by style, the marks; its links that a link may lead to
// (storedHref) links; its line breaks hard breaks; and its white space and
// the line breaks of its text, which a pre or a white-space style keeps,
// kept. What no text shows, as a script, a style sheet, an image, a comment,
// a hidden element, or the bullet or number of a list item that Word writes
// out, which the list shows, or a bullet of Word's anywhere, is left out; a
// heading of Word's numbered headings keeps its number as its text. Neither
// the pasted HTML, as it is parsed, nor the cleaned HTML has a style
// attribute or a style element, which the page's Content-Security-Policy
// refuses (style-src-attr, style-src-elem) in a document that DOMParser
// makes too, so that the browser reports none and leaves no style unread
// (unstyled).

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
   * The number of the item, where the HTML gives it: of an item of an ol,
   * its value, and otherwise, of the first item of an ol that the walk
   * reads, the ol's start, and of a paragraph of Word's list, its number; or
   * undefined, as of a later item of an ol, which continues its list.
   */
  start?: number;
  /**
   * Whether the item is one of ProseMirror's HTML that no list element holds,
   * as of a copy within one list item, whose marker names the list, which
   * the cleaned HTML leaves it out of too.
   */
  loose?: boolean;
  /** Whether the item is the first of its list element in ProseMirror's HTML, whose list is one of its own, as one of two lists side by side is. */
  opens?: boolean;
}

/** A paragraph of the HTML: one of the document, a list item's first, or one that continues the list item at a level. */
interface Para {
  runs: Run[];
  item?: ItemOf;
  continues?: number;
  /**
   * Whether the paragraph only begins its list item, which has no first
   * paragraph in ProseMirror's HTML, as where the slice begins within a
   * list within the item, whose open start ProseMirror's parser keeps.
   */
  bare?: boolean;
  /**
   * Whether no block of the HTML holds the paragraph, as of a copy from
   * within one paragraph of a page, which ProseMirror pastes into the
   * paragraph at the caret, so that, as the paste's last, the text after
   * the caret follows it.
   */
  open?: boolean;
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
  /** Whether line breaks are kept, as where white space is, or in an element whose white-space is pre-line, which collapses the rest of the white space. */
  lines: boolean;
  /** Whether the text is shown, as no visibility:hidden hides it, which visibility:visible undoes within it. */
  shown: boolean;
}

/** The values of the white-space style that keep white space and line breaks, as a pre does, and as Google Docs's and Word Online's text has them. */
const KEPT_SPACE = new Set(["pre", "pre-wrap", "break-spaces"]);

/** The value of the white-space style that keeps line breaks, but collapses the rest of the white space. */
const KEPT_LINES = "pre-line";

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
  "listing",
  "main",
  "menu",
  "nav",
  "ol",
  "p",
  "plaintext",
  "pre",
  "search",
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
  "xmp",
]);

/** The elements of preformatted text, whose white space is kept, and whose text is code: a pre, and the listing, the plaintext, and the xmp that HTML shows as one. */
const PREFORMATTED = new Set(["listing", "plaintext", "pre", "xmp"]);

/** The elements of a block of text, which ProseMirror writes of a paragraph, a heading, or a block of code. */
const TEXTBLOCKS = new Set(["h1", "h2", "h3", "h4", "h5", "h6", "p", "pre"]);

/** The white space of HTML, which a paragraph's text collapses. */
const HTML_SPACE = /^[ \t\n\f\r]*$/;

/** The attribute that a style attribute of the pasted HTML is renamed to before the HTML is parsed (unstyled), which the cleaning reads an element's style from. */
const STYLE = "data-pasted-style";

/** HTML's white space, which ends a tag's name and an attribute's, as \s, which holds more, as the no-break space, does not. */
const SPACE = "[\\t\\n\\f\\r ]";

/** What ends the name of a tag: white space, "/", or ">". */
const NAME_END = "(?=[\\t\\n\\f\\r />])";

/** The name of an attribute, which may begin with "=", and holds quotes and "<", as HTML's tokenizer reads it. */
const ATTRIBUTE_NAME = "[^\\t\\n\\f\\r />][^\\t\\n\\f\\r /=>]*";

/**
 * The value that follows the name of an attribute: one in quotes, up to the
 * quote that ends it or the end of the HTML, or one without, up to white
 * space or ">", which holds what else follows, as the "/style" of
 * href=https://example.com/style.
 */
const ATTRIBUTE_VALUE = `${SPACE}*=${SPACE}*(?:"[^"]*"?|'[^']*'?|[^\\t\\n\\f\\r >]*)`;

/**
 * The attributes of a tag, up to its ">" or the end of the HTML, as HTML's
 * tokenizer reads them: white space and "/" between them, and each a name,
 * with or without a value, as one name follows a quoted value with nothing
 * between them; a quote that begins no value begins nothing.
 */
const ATTRIBUTES = `(?:[\\t\\n\\f\\r /]+|${ATTRIBUTE_NAME}(?:${ATTRIBUTE_VALUE})?)*`;

/** An attribute of ATTRIBUTES: what goes before it, its name, and its value. */
const ATTRIBUTE = new RegExp(
  `([\\t\\n\\f\\r /]*)(${ATTRIBUTE_NAME})((?:${ATTRIBUTE_VALUE})?)`,
  "g",
);

/** The name of a tag, after its first letter, as HTML's tokenizer reads it. */
const TAG_NAME = "[^\\t\\n\\f\\r />]*";

/**
 * The parts of HTML that unstyled reads, each where HTML's tokenizer reads
 * it, as it reads HTML outside SVG and MathML: a comment, which "-->" or
 * "--!>" ends, or ">" or "->" right after its "<!--"; a DOCTYPE, a
 * processing instruction, CDATA, and "</" before what is no letter, which it
 * reads up to the first ">", as a comment; an end tag; an element of text,
 * whose content is text up to its end tag, as a style sheet, a script, or an
 * xmp, of which the cleaning shows only xmp's, and leaves the others out
 * (SKIPPED); a plaintext, whose content is text to the end of the HTML; and a
 * start tag. What nothing ends goes on to the end of the HTML, as in HTML.
 */
const PARTS = new RegExp(
  [
    "<!--(?:-?>|[\\s\\S]*?(?:--!?>|$))",
    "<[!?][^>]*>?",
    "</(?![a-z])[^>]*>?",
    `</[a-z]${TAG_NAME}${ATTRIBUTES}>?`,
    `<(?<raw>script|style|textarea|title|xmp|iframe|noembed|noframes)${NAME_END}(?<rawAttributes>${ATTRIBUTES})(?<rawClose>>?)(?<rawText>[\\s\\S]*?)(?<rawEnd></\\k<raw>${NAME_END}${ATTRIBUTES}>?|$)`,
    `<(?<plain>plaintext)${NAME_END}(?<plainAttributes>${ATTRIBUTES})(?<plainClose>>?)(?<plainText>[\\s\\S]*)`,
    `<(?<start>[a-z]${TAG_NAME})(?<attributes>${ATTRIBUTES})(?<close>>?)`,
  ].join("|"),
  "gi",
);

/**
 * The start tags after which HTML may read what follows an xmp's or a
 * plaintext's start tag as HTML, rather than as their text: SVG's and
 * MathML's, within which they are elements of no text, and a select's and a
 * frameset's, within which HTML may leave their start tags out.
 */
const MARKUP_AFTER = /^(?:svg|math|select|frameset)$/i;

/** Returns the attributes of a tag (ATTRIBUTES) with the name of its style attributes STYLE. */
function renamed(attributes = ""): string {
  return attributes.replace(
    ATTRIBUTE,
    (attribute, before: string, name: string, value: string) =>
      /^style$/i.test(name) ? `${before}${STYLE}${value}` : attribute,
  );
}

/**
 * Returns HTML with the style attributes of its start tags renamed (STYLE),
 * and without its style elements, which the cleaning leaves out, so that a
 * document that DOMParser makes of it has neither: a browser applies the
 * page's Content-Security-Policy to such a document too, which refuses both
 * (style-src-attr and style-src-elem), reports them, and leaves the
 * element's style empty. A style element leaves an empty comment in its
 * place, so that a "<" before it begins no tag with what follows it. The
 * other elements of text whose text the cleaning leaves out, as a script or
 * a title, keep their tags but not their text: HTML reads that text as HTML
 * within SVG, MathML, a select, or a frameset, and a script's up to a later
 * end tag than its first where it holds "<!--" and "<script", which unstyled
 * cannot tell apart, and an element without text is the same either way. So
 * are an xmp and a plaintext, whose text the cleaning shows, after a start
 * tag of SVG, MathML, a select, or a frameset (MARKUP_AFTER). So HTML reads
 * all that is left as unstyled reads it.
 */
function unstyled(html: string): string {
  // Whether a start tag has been read after which HTML may read the text of
  // an xmp or a plaintext as HTML.
  let markup = false;
  return html.replace(PARTS, (part, ...args) => {
    const g = args.at(-1) as Partial<Record<string, string>>;
    if (g.raw !== undefined) {
      const name = g.raw.toLowerCase();
      if (name === "style") {
        return "<!---->";
      }
      const text = name === "xmp" && !markup ? g.rawText : "";
      return `<${g.raw}${renamed(g.rawAttributes)}${g.rawClose}${text}${g.rawEnd}`;
    }
    if (g.plain !== undefined) {
      return `<${g.plain}${renamed(g.plainAttributes)}${g.plainClose}${markup ? "" : g.plainText}`;
    }
    if (g.start !== undefined) {
      markup ||= MARKUP_AFTER.test(g.start);
      return `<${g.start}${renamed(g.attributes)}${g.close}`;
    }
    return part;
  });
}

/**
 * Gives each element of the parsed HTML the style that its attribute (STYLE)
 * writes, through the CSSOM, which the page's policy allows, so that styleOf
 * reads it; a style attribute that unstyled did not rename, should HTML have
 * read one where unstyled reads none, which the browser may have refused, is
 * read the same.
 */
function applyStyles(source: Document) {
  for (const element of source.querySelectorAll(`[${STYLE}], [style]`)) {
    if (!element.hasAttribute(STYLE)) {
      element.setAttribute(STYLE, element.getAttribute("style") ?? "");
    }
    const { style } = element as Partial<ElementCSSInlineStyle>;
    if (style !== undefined) {
      style.cssText = element.getAttribute(STYLE) ?? "";
    }
  }
}

/** Returns the text of an element's style as the pasted HTML writes it, as the mso- properties of Word, which the CSSOM does not hold. */
function rawStyleOf(element: Element): string {
  return element.getAttribute(STYLE) ?? "";
}

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

/** Reports whether the element is not shown, with what it holds, by its style or its hidden attribute, or as a dialog that is not open. */
function hidden(element: Element): boolean {
  return (
    element.hasAttribute("hidden") ||
    (element.localName.toLowerCase() === "dialog" &&
      !element.hasAttribute("open")) ||
    styleOf(element, "display") === "none" ||
    /mso-hide\s*:\s*all/i.test(rawStyleOf(element))
  );
}

/** Returns whether an element's text is shown by its visibility, of which what holds it says shown: hidden hides it, and visible shows it within what is hidden. */
function shownOf(element: Element, shown: boolean): boolean {
  const visibility = styleOf(element, "visibility");
  return visibility === "hidden"
    ? false
    : visibility === "visible"
      ? true
      : shown;
}

/** Returns the elements of source that hold an element of visibility:visible, which shows what it holds within one of visibility:hidden. */
function visibleWithin(source: Document): Set<Element> {
  const holders = new Set<Element>();
  for (const element of source.querySelectorAll(`[${STYLE}]`)) {
    if (styleOf(element, "visibility") !== "visible") {
      continue;
    }
    for (
      let e = element.parentElement;
      e !== null && !holders.has(e);
      e = e.parentElement
    ) {
      holders.add(e);
    }
  }
  return holders;
}

/**
 * Reports whether the cleaning leaves an element out, with what it holds: an
 * element that no text shows or that holds none of the document's
 * (SKIPPED), a hidden one, and the line break that a browser adds where a
 * selection that it copied ends at the start of a line, which marks where
 * the selection ended.
 */
function dropped(element: Element, name: string): boolean {
  return (
    SKIPPED.has(name) ||
    hidden(element) ||
    (name === "br" && element.classList.contains("Apple-interchange-newline"))
  );
}

/** Reports whether an element is the bullet or the number that Word writes out of a paragraph of its list, a span of mso-list:Ignore. */
function isWordMarker(element: Element, name: string): boolean {
  return name === "span" && /mso-list\s*:\s*ignore/i.test(rawStyleOf(element));
}

/**
 * Reports whether the text of a marker of Word's list (isWordMarker) is a
 * bullet, or nothing: one that holds no letter or digit, as Symbol's "·", a
 * character of the private use area, or Wingdings's "§", or one character
 * that is no digit, as Courier New's "o" or Wingdings's "Ø", which Word
 * draws in its font as a bullet, and the editor would show as the letter.
 */
function isBullet(marker: string): boolean {
  return (
    !/[\p{L}\p{N}]/u.test(marker) ||
    ([...marker].length === 1 && !/\p{Nd}/u.test(marker))
  );
}

/** Returns the level of a list item that its element gives, as Google Docs's aria-level and Word Online's data-aria-level do, the first of them that is a level, or undefined for none. */
function levelOf(element: Element): number | undefined {
  for (const name of ["aria-level", "data-aria-level"]) {
    const value = element.getAttribute(name);
    const level = value === null || value.trim() === "" ? 0 : Number(value);
    if (Number.isInteger(level) && level >= 1) {
      return level;
    }
  }
  return undefined;
}

/**
 * Returns the list item that a paragraph of Word is, by its mso-list style,
 * as "mso-list:l0 level2 lfo1", with the bullet or the number that Word
 * writes out in a span of mso-list:Ignore, which tells whether the list is
 * numbered and where it starts; or undefined for a paragraph that is no
 * list's.
 */
function wordItemOf(element: Element): ItemOf | undefined {
  const match = /mso-list\s*:\s*l\d+\s+level(\d+)/i.exec(rawStyleOf(element));
  if (match === null) {
    return undefined;
  }
  const marker =
    Array.from(element.querySelectorAll("span"))
      .find((span) => isWordMarker(span, "span"))
      ?.textContent?.trim() ?? "";
  // The item's number is the last of those that the marker holds, as 2 of
  // 1.2., where the numbers of the items that hold it go before it: digits,
  // a Roman numeral, as xviii, or up to four letters, as aa.
  const numbered = /^\(?(?:\d+\.)*([0-9]+|[ivxlcdm]+|[a-z]{1,4})[.)]/i.exec(
    marker,
  );
  return {
    // Word's levels are from 1, as a list item's are; level0 is the first.
    level: Math.max(1, Number(match[1])),
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
  /** How many paragraphs of Word's list (wordItemOf) hold what the walk reads. */
  private wordItems = 0;
  /** How many blocks of the HTML, but its body, hold what the walk reads. */
  private blocks = 0;
  /**
   * Whether the HTML is ProseMirror's, as the editor copies it, whose every
   * block of text is a paragraph, as one that holds nothing or only white
   * space, which a paragraph of another app's HTML that shows nothing is
   * not.
   */
  private readonly faithful: boolean;
  /** The list elements of which the walk has read an item. */
  private readonly begun = new Set<Element>();
  /** The elements that hold one of visibility:visible (visibleWithin), which the walk reads though they are not shown. */
  private readonly holdsVisible: Set<Element>;

  constructor(faithful: boolean, holdsVisible: Set<Element>) {
    this.faithful = faithful;
    this.holdsVisible = holdsVisible;
  }

  /** Ends the paragraph that inline content went to. */
  private end() {
    this.current = undefined;
  }

  /** Returns the paragraph that inline content goes to, which begins with the first. */
  private paragraph(): Para {
    if (this.current === undefined) {
      const para: Para = { runs: [] };
      if (this.blocks === 0) {
        para.open = true;
      }
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
    // What is not shown is left out with what it holds, but for what shows
    // within it.
    const shown = shownOf(element, ctx.shown);
    if (dropped(element, name) || (!shown && !this.holdsVisible.has(element))) {
      return;
    }
    if (name === "br") {
      this.paragraph().runs.push({ kind: "break" });
      return;
    }
    if (isWordMarker(element, name)) {
      // The bullet or the number of a paragraph of Word's list, or within a
      // list's item, is left out, as its list shows it, and so is a bullet
      // anywhere (isBullet). The number of a heading of Word's list, as of
      // its numbered headings, which is a paragraph of no list here, is its
      // text, as Word shows it, with a space in place of the white space
      // that Word writes after it.
      const marker = element.textContent?.trim() ?? "";
      if (this.wordItems === 0 && ctx.lists === 0 && !isBullet(marker)) {
        this.text(
          `${marker} `,
          this.contextOf(element, name, { ...ctx, shown }),
        );
      }
      return;
    }
    const chip = chipOf(element);
    if (chip !== undefined) {
      if (shown) {
        this.paragraph().runs.push({
          kind: "chip",
          attrs: chip,
          marks: marksOf(ctx.marks),
        });
      }
      return;
    }
    const inner = this.contextOf(element, name, { ...ctx, shown });
    if (!BLOCKS.has(name)) {
      this.children(element, inner);
      return;
    }
    // A list item that holds a list before any text of its own has an
    // empty first paragraph, before the list's items; in ProseMirror's HTML
    // it has none, as where the slice begins within the list, which
    // ProseMirror's parser keeps open (bare).
    if (
      this.pendingItem !== undefined &&
      (name === "li" || inner.lists > ctx.lists)
    ) {
      this.paragraph().bare = this.faithful;
    }
    this.end();
    const block = name === "body" ? 0 : 1;
    this.blocks += block;
    const outerItem = this.pendingItem;
    const outerLevel = this.itemLevel;
    // A list item, of a list's element or a paragraph of Word's list, whose
    // first paragraph its content begins, and which its later paragraphs
    // continue.
    let item: ItemOf | undefined;
    let word = false;
    if (name === "li") {
      const first = ctx.list !== undefined && !this.begun.has(ctx.list);
      if (ctx.list !== undefined) {
        this.begun.add(ctx.list);
      }
      // An item's number is its value, where HTML reads one, as 9 of
      // "9th", and the first item's otherwise its list's start.
      const value = element.getAttribute("value");
      const numbered =
        value !== null && /^[\t\n\f\r ]*[+-]?\d/.test(value)
          ? startOf(value)
          : undefined;
      item = {
        level: levelOf(element) ?? Math.max(ctx.lists, 1),
        ordered: ctx.ordered,
        start: ctx.ordered
          ? (numbered ?? (first ? (ctx.start ?? 1) : undefined))
          : undefined,
        loose: this.faithful && ctx.lists === 0,
        opens: this.faithful && first,
      };
    } else if (name === "p" || name === "div") {
      item = wordItemOf(element);
      word = item !== undefined;
    }
    if (item !== undefined) {
      item.level = Math.min(item.level, MAX_LIST_DEPTH);
      this.pendingItem = item;
      this.itemLevel = name === "li" ? item.level : undefined;
    }
    if (this.faithful && TEXTBLOCKS.has(name) && inner.shown) {
      this.paragraph();
    }
    this.wordItems += word ? 1 : 0;
    this.children(element, inner);
    this.wordItems -= word ? 1 : 0;
    if (item !== undefined) {
      if (this.pendingItem === item && inner.shown) {
        // An empty item is an empty paragraph of its list.
        this.paragraph();
      }
      this.pendingItem = outerItem;
      this.itemLevel = outerLevel;
    }
    this.blocks -= block;
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
      marks.code ||
      ["code", "kbd", "samp", "tt"].includes(name) ||
      PREFORMATTED.has(name);
    if (name === "a") {
      const href = element.getAttribute("href");
      // A link within a link is the inner one, as a browser follows it.
      marks.href =
        href === null ? marks.href : (storedHref(href) ?? marks.href);
    }
    // White space is kept in a pre (PREFORMATTED), and as an element's
    // white-space, which what it holds takes, says, and so are line breaks,
    // and they alone as its pre-line says.
    const space = styleOf(element, "white-space");
    const keep =
      space === "" ? ctx.keep || PREFORMATTED.has(name) : KEPT_SPACE.has(space);
    const inner: Context = {
      ...ctx,
      marks,
      keep,
      lines: keep || (space === "" ? ctx.lines : space === KEPT_LINES),
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
    if (
      !ctx.shown ||
      (this.current === undefined && !ctx.keep && HTML_SPACE.test(value))
    ) {
      return;
    }
    const marks = marksOf(ctx.marks);
    if (!ctx.lines) {
      this.paragraph().runs.push({
        kind: "text",
        text: value,
        marks,
        keep: false,
      });
      return;
    }
    // Text whose line breaks are kept keeps its lines, each after a line
    // break, and their white space where it is kept too; otherwise the white
    // space around a line break goes, as pre-line has it, and the rest
    // collapses (collapsed).
    const lines = value.split(/\r\n?|\n/);
    lines.forEach((line, i) => {
      const para = this.paragraph();
      if (i > 0) {
        para.runs.push({ kind: "break" });
      }
      const text = ctx.keep
        ? line
        : line
            .replace(i > 0 ? /^[ \t\f]+/ : /^/, "")
            .replace(i < lines.length - 1 ? /[ \t\f]+$/ : /$/, "");
      if (text !== "") {
        para.runs.push({ kind: "text", text, marks, keep: ctx.keep });
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
 * each ol numbered from where the one before it ends, are one list. In the
 * editor's own HTML (faithful), the first item of a list element begins a
 * list of its own, as ProseMirror reads it, and a paragraph keeps its white
 * space; in another app's, a paragraph that holds text whose white space is
 * kept has the rest of its white space collapsed here (collapsed), since
 * ProseMirror keeps all of the paragraph's (writeBlocks), and a paragraph
 * loses the white space at its end (tidy), but for the paste's last where no
 * block holds it (Para.open).
 */
function blocksOf(paras: readonly Para[], faithful: boolean): Block[] {
  const top: Block[] = [];
  const open: List[] = [];
  const lastItem = (list: List) => list.items[list.items.length - 1];
  for (const [i, para] of paras.entries()) {
    // The paste's last paragraph, where no block holds it, ends where the
    // text after the caret begins, which shows the white space at its end.
    const joined = para.open === true && i === paras.length - 1;
    const runs = holdsKept(para.runs) ? collapsed(para.runs) : para.runs;
    const block = {
      kind: "p" as const,
      runs: faithful ? para.runs : joined ? spacedEnd(runs) : tidy(runs),
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
          para.item.opens === true ||
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
      open[open.length - 1].items.push(para.bare === true ? [] : [block]);
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
 * Returns a paragraph's runs as a block shows them: without the white space
 * at its end, as the no-break spaces of Word's empty paragraphs and of Word
 * Online's ends of paragraphs, or before the line break that ends it, which
 * a browser shows nothing of, and which ProseMirror's parser of what is
 * pasted leaves out of the cleaned HTML as it is the last of its paragraph,
 * so that a paragraph that ends in two line breaks, which shows the first,
 * keeps it.
 */
function tidy(runs: readonly Run[]): Run[] {
  const out = [...runs];
  trimEnd(out);
  const last = out.at(-1);
  if (last?.kind === "break") {
    out.pop();
    trimEnd(out);
    out.push(last);
  }
  return out;
}

/**
 * Returns the runs of the paste's last paragraph where no block holds it,
 * which ProseMirror pastes into the paragraph at the caret, with the spaces
 * at the end of its text whose white space is kept, as Google Docs's, as
 * no-break spaces, as a browser's copy writes the space at its end:
 * ProseMirror's parser takes the spaces at the end of what is pasted off
 * it, though a pre keeps them, and keeps no-break spaces, which show as the
 * space between the pasted text and the text after the caret.
 */
function spacedEnd(runs: readonly Run[]): Run[] {
  const out = [...runs];
  const last = out.at(-1);
  if (last?.kind === "text" && last.keep) {
    out[out.length - 1] = {
      ...last,
      text: last.text.replace(/ +$/, (spaces) =>
        "\u00a0".repeat(spaces.length),
      ),
    };
  }
  return out;
}

/** Takes the white space at the end of runs off them, up to what is no text. */
function trimEnd(runs: Run[]) {
  while (runs.length > 0) {
    const last = runs[runs.length - 1];
    if (last.kind !== "text") {
      return;
    }
    // \s holds the no-break space too.
    const text = last.text.replace(/\s+$/u, "");
    if (text !== "") {
      runs[runs.length - 1] = { ...last, text };
      return;
    }
    runs.pop();
  }
}

/** Reports whether runs hold text whose white space is kept. */
function holdsKept(runs: readonly Run[]): boolean {
  return runs.some((run) => run.kind === "text" && run.keep);
}

/**
 * Returns the runs with the white space of their text that is not kept
 * collapsed, as a browser shows it and ProseMirror reads it: each run of it
 * one space, and none at the paragraph's start, after a line break, or after
 * text that ends with white space.
 */
function collapsed(runs: readonly Run[]): Run[] {
  const out: Run[] = [];
  let spaceBefore = true;
  for (const run of runs) {
    if (run.kind !== "text" || run.keep) {
      out.push(run);
      spaceBefore =
        run.kind === "break" ||
        (run.kind === "text" && /[ \t\n\f\r]$/.test(run.text));
      continue;
    }
    let text = run.text.replace(/[ \t\n\f\r]+/g, " ");
    if (spaceBefore && text.startsWith(" ")) {
      text = text.slice(1);
    }
    if (text !== "") {
      out.push({ ...run, text });
      spaceBefore = text.endsWith(" ");
    }
  }
  return out;
}

/** Writes a run into parent, within the elements of its marks. */
function writeRun(doc: Document, parent: Element, run: Run) {
  if (run.kind === "break") {
    parent.append(doc.createElement("br"));
    return;
  }
  let node: Node;
  if (run.kind === "text") {
    node = doc.createTextNode(run.text);
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

/**
 * Writes the blocks into parent: a paragraph that holds text whose white
 * space is kept within a pre, whose white space ProseMirror's parser keeps,
 * as a line's indentation, rather than in an element of a white-space style,
 * which the page's policy refuses.
 */
function writeBlocks(doc: Document, parent: Element, blocks: readonly Block[]) {
  for (const block of blocks) {
    if (block.kind === "p") {
      const p = doc.createElement("p");
      for (const run of block.runs) {
        writeRun(doc, p, run);
      }
      if (holdsKept(block.runs)) {
        const pre = doc.createElement("pre");
        pre.append(p);
        parent.append(pre);
      } else {
        parent.append(p);
      }
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
 * references, and pre elements around paragraphs whose white space is kept
 * (writeBlocks). The HTML is read in a document of its own, without a
 * window, so that none of it runs, loads, or applies a style, with its style
 * attributes renamed and its style elements left out (unstyled), and its
 * styles read through the CSSOM (applyStyles); and the marker of a slice
 * that ProseMirror wrote, data-pm-slice, is kept on the first block, as it
 * holds of the cleaned HTML (sliceOf), with every block of text a paragraph
 * and its white space kept, list items that no list element holds kept so,
 * a list item's missing first paragraph missing, and each list element a
 * list, so that what is copied from the editor is pasted as it was cut from
 * its blocks.
 */
export function cleanPastedHTML(html: string): string {
  const source = new DOMParser().parseFromString(unstyled(html), "text/html");
  applyStyles(source);
  const slice = sliceOf(source);
  const reader = new Reader(slice !== undefined, visibleWithin(source));
  reader.read(source.body, {
    marks: { bold: false, italic: false, strike: false, code: false },
    lists: 0,
    ordered: false,
    keep: false,
    lines: false,
    shown: true,
  });
  const out = document.implementation.createHTMLDocument("");
  writeBlocks(out, out.body, blocksOf(reader.paras, slice !== undefined));
  if (slice !== undefined) {
    out.body.firstElementChild?.setAttribute("data-pm-slice", slice);
  }
  return out.body.innerHTML;
}
