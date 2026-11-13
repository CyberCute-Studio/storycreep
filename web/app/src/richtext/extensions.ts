// The editor's schema: TipTap's extensions of the nodes and marks of
// schema/rich-text/README.md, with only the attributes that it gives them,
// so that the editor writes the documents that the server stores (PRD
// FR-TEXT-01, ADR-020, plan note N40).

import { checkHref, MAX_DECIMALS } from "@storycreep/shared/richtext";
import {
  type AnyExtension,
  type Attributes,
  type Editor,
  Extension,
  isMarkActive,
  markInputRule,
  markPasteRule,
  mergeAttributes,
  Node,
  PasteRule,
} from "@tiptap/core";
import { Bold } from "@tiptap/extension-bold";
import { Code } from "@tiptap/extension-code";
import { Document } from "@tiptap/extension-document";
import { HardBreak } from "@tiptap/extension-hard-break";
import { Italic } from "@tiptap/extension-italic";
import { Link } from "@tiptap/extension-link";
import { BulletList, ListItem, OrderedList } from "@tiptap/extension-list";
import { Paragraph } from "@tiptap/extension-paragraph";
import { Strike } from "@tiptap/extension-strike";
import { Text } from "@tiptap/extension-text";
import { Placeholder, UndoRedo } from "@tiptap/extensions";
import type { MarkType, Node as PMNode, Slice } from "@tiptap/pm/model";
import { Plugin, PluginKey, TextSelection } from "@tiptap/pm/state";
import type { EditorView } from "@tiptap/pm/view";
import { typedHref } from "./link";
import { normalizer } from "./normalize";
import { cleanPastedHTML } from "./paste";
import {
  isMentionElement,
  isValueRefElement,
  liftKeepsList,
  sinkFits,
  startOf,
} from "./schema";
import { type Suggestion, suggester } from "./suggest";

/** What the editor shows of the value references and the mentions of a text, which name what the project holds. */
export interface RichTextNames {
  /** The name of a component, which its mention shows, or undefined for one that the page does not know. */
  mention: (id: string) => string | undefined;
  /** The name of a variable, or of a component's attribute, which its value reference shows, or undefined for one that the page does not know. */
  value: (ref: string, attribute: string | undefined) => string | undefined;
}

/**
 * What a mention shows of its component, and a value reference of its value:
 * their names, or a word for what they are, as of an ID that is undefined,
 * where the node's is no string.
 */
export interface ChipLabels {
  mention: (id: string | undefined) => string;
  value: (ref: string | undefined, attribute: string | undefined) => string;
}

/**
 * Returns what a value reference shows of its name, the value's name in
 * braces, with the digits after the point that a number is shown with, where
 * it has them, after a colon, as {gold:2}, as the { of a text is typed to
 * insert it so (plan note N73); decimals is shown only where it is one that
 * a reference may have, as of a text that the editor cannot keep, which may
 * hold any JSON there (readOf).
 */
export function valueChipText(name: string, decimals: unknown): string {
  return typeof decimals === "number" &&
    Number.isInteger(decimals) &&
    decimals >= 0 &&
    decimals <= MAX_DECIMALS
    ? `{${name}:${decimals}}`
    : `{${name}}`;
}

/**
 * Returns what a node of the editor shows as its text, if it is a mention or
 * a value reference: @ before the component's name, or the value's name in
 * braces, with its format (valueChipText). The labels are asked of the
 * node's IDs only where they are strings, as the schema has them, and of
 * none otherwise, as of a text that the editor cannot keep, which may hold
 * any JSON there (readOf), so that a page's names are given only the
 * strings that they take.
 */
function chipText(node: PMNode, labels: ChipLabels): string | undefined {
  const { id, ref, attribute, decimals } = node.attrs;
  switch (node.type.name) {
    case "mention":
      return `@${labels.mention(typeof id === "string" ? id : undefined)}`;
    case "valueRef":
      return valueChipText(
        typeof ref === "string" &&
          (attribute === null || typeof attribute === "string")
          ? labels.value(ref, attribute ?? undefined)
          : labels.value(undefined, undefined),
        decimals,
      );
  }
  return undefined;
}

/**
 * Returns an inline atom of the editor's own, named name, which shows what
 * chipText makes of it with the labels of its options. Its element is a span
 * of data-type, with its attributes, which the paste's cleaning keeps where
 * accepts takes it, so that one copied from one text is one in the text that
 * it is pasted into.
 */
function chipNode(
  name: "mention" | "valueRef",
  type: "mention" | "value-ref",
  accepts: (element: HTMLElement) => boolean,
  attributes: Attributes,
) {
  return Node.create<{ labels: ChipLabels }>({
    name,
    group: "inline",
    inline: true,
    atom: true,
    selectable: true,

    addOptions() {
      return {
        labels: { mention: (id) => id ?? "", value: (ref) => ref ?? "" },
      };
    },

    addAttributes: () => attributes,

    parseHTML() {
      return [
        {
          tag: `span[data-type="${type}"]`,
          getAttrs: (element) => (accepts(element) ? null : false),
        },
      ];
    },

    renderHTML({ node, HTMLAttributes }) {
      return [
        "span",
        mergeAttributes(HTMLAttributes, {
          "data-type": type,
          class: "rich-text-chip",
        }),
        chipText(node, this.options.labels) ?? "",
      ];
    },

    renderText({ node }) {
      return chipText(node, this.options.labels) ?? "";
    },
  });
}

/** A mention of a component, which shows the component's name (FR-TEXT-03), with its ID in attrs.id. */
const Mention = chipNode("mention", "mention", isMentionElement, {
  id: {
    isRequired: true,
    parseHTML: (element) => element.getAttribute("data-id"),
    renderHTML: (attributes) => ({ "data-id": attributes.id }),
  },
});

/**
 * A reference to a value, which shows the value (FR-TEXT-02): a variable's,
 * with its ID in attrs.ref, or a component's runtime attribute's, with the
 * component's ID in attrs.ref and the attribute definition's in
 * attrs.attribute, and attrs.decimals, the digits after the point that a
 * number is shown with, or null for none.
 */
const ValueRef = chipNode("valueRef", "value-ref", isValueRefElement, {
  ref: {
    isRequired: true,
    parseHTML: (element) => element.getAttribute("data-ref"),
    renderHTML: (attributes) => ({ "data-ref": attributes.ref }),
  },
  attribute: {
    default: null,
    parseHTML: (element) => element.getAttribute("data-attribute"),
    renderHTML: (attributes) =>
      attributes.attribute === null
        ? {}
        : { "data-attribute": attributes.attribute },
  },
  decimals: {
    default: null,
    parseHTML: (element) => {
      const decimals = element.getAttribute("data-decimals");
      return decimals !== null &&
        /^\d$/.test(decimals) &&
        Number(decimals) <= MAX_DECIMALS
        ? Number(decimals)
        : null;
    },
    renderHTML: (attributes) =>
      attributes.decimals === null
        ? {}
        : { "data-decimals": String(attributes.decimals) },
  },
});

/** The key of the plugin that shows the labels of the editor's mentions and value references again (relabel). */
const relabelKey = new PluginKey<number>("richTextRelabel");

/**
 * Returns the plugin that, when relabel asks, writes what each mention and
 * value reference shows as labels now give it: ProseMirror draws a node's
 * element once, and again only when the node changes, and the element holds
 * no content of the document, so ProseMirror leaves what it shows alone.
 */
function relabeller(labels: ChipLabels): Plugin {
  return new Plugin({
    key: relabelKey,
    state: {
      init: () => 0,
      apply: (tr, n) => (tr.getMeta(relabelKey) === true ? n + 1 : n),
    },
    view: () => ({
      update: (view, before) => {
        if (relabelKey.getState(view.state) === relabelKey.getState(before)) {
          return;
        }
        view.state.doc.descendants((node, pos) => {
          const text = chipText(node, labels);
          const element = text === undefined ? null : view.nodeDOM(pos);
          if (element !== null && element.textContent !== text) {
            element.textContent = text ?? "";
          }
        });
      },
    }),
  });
}

/**
 * Has the editor show again what its mentions, its value references, and
 * its placeholder show, after the names, the words, or the placeholder that
 * the editor's options give changed, as when the project's names have loaded
 * or another user renamed a component.
 */
export function relabel(editor: Editor) {
  if (!editor.isDestroyed) {
    editor.view.dispatch(
      editor.state.tr.setMeta(relabelKey, true).setMeta("addToHistory", false),
    );
  }
}

/** Whether TipTap links a URL that linkify finds as it is typed or pasted, of which StoredLink links only those that a link may lead to. */
const autoLinks = Link.options.shouldAutoLink;

/** Reports whether any of a slice's content has a mark of type. */
function holdsMark(slice: Slice, type: MarkType): boolean {
  let holds = false;
  slice.content.descendants((node) => {
    holds ||= type.isInSet(node.marks) !== undefined;
    return !holds;
  });
  return holds;
}

/**
 * A link, with its URL in attrs.href and no other attribute: it opens in a
 * new tab, without the page's referrer or a handle on it, and a click in the
 * editor puts the caret in it rather than following it. A URL typed or
 * pasted is linked where a link may lead to it, as the link's field takes
 * it (typedHref), as example.com, which linkify links to https://example.com,
 * and an email address, which it links to a mailto URL. setLink and
 * toggleLink take a URL as the link's field does too, and set it as a link
 * stores it, so that a link that they set is not taken off again by the
 * normalizer, and refuse one that no link may lead to, as of no string, and
 * toggleLink without one only takes a link off the selection that has it. A
 * URL pasted over a selection links it, but a copied link pasted over it is
 * pasted as it is, keeping its link. Its element leads
 * only to a URL as a link stores it (checkHref), and to none otherwise, as
 * of a link without a URL, or with one that this version does not take, in
 * a text that the editor shows but cannot keep, rather than to a URL of the
 * app's own page that a relative one would be.
 */
const StoredLink = Link.extend({
  addAttributes() {
    return {
      href: {
        default: null,
        parseHTML: (element) => element.getAttribute("href"),
      },
    };
  },

  renderHTML({ HTMLAttributes }) {
    const { href } = HTMLAttributes;
    return [
      "a",
      mergeAttributes(this.options.HTMLAttributes, {
        ...HTMLAttributes,
        href:
          typeof href === "string" && checkHref(href) === undefined
            ? href
            : null,
      }),
      0,
    ];
  },

  // TipTap's rule of the URLs that linkify finds in pasted text, which
  // leads to an address without a scheme with linkify's own, http, and
  // links text that a link already holds, as a copied link whose text is a
  // URL, to that text: an address that it finds, only one that a link may
  // lead to (isAllowedUri), leads where autolinking leads it, as the link's
  // field takes it (typedHref), and pasted text that a link holds keeps
  // that link.
  addPasteRules() {
    const type = this.type;
    return (this.parent?.() ?? []).map((rule) => {
      const linking = markPasteRule({
        find: rule.find,
        type,
        getAttributes: (match) => ({ href: typedHref(match[0]) }),
      });
      // A match that it skips, as one that a link holds, or one in code,
      // which TipTap's rule answers with null, it answers with nothing,
      // rather than null, with which TipTap would drop what the rule did of
      // the whole paste, and so leave the paste's other URLs unlinked.
      return new PasteRule({
        find: rule.find,
        handler: (props) => {
          const { state, range } = props;
          if (state.doc.rangeHasMark(range.from, range.to, type)) {
            return undefined;
          }
          return linking.handler(props) ?? undefined;
        },
      });
    });
  },

  // TipTap's handler of a paste over a selection, which links the selection
  // where the pasted text is one URL, to the URL that linkify reads of that
  // text: pasted text that a link holds, as a copied link whose text is a
  // URL, as example.com of a link to https://example.com/docs, is pasted
  // over the selection as it is, as any copied link is, so that it keeps
  // its link, rather than the selection being linked to its text.
  addProseMirrorPlugins() {
    const type = this.type;
    return (this.parent?.() ?? []).map((plugin) => {
      const { handlePaste } = plugin.props;
      if (handlePaste === undefined) {
        return plugin;
      }
      return new Plugin({
        ...plugin.spec,
        props: {
          ...plugin.spec.props,
          handlePaste: (view, event, slice) =>
            !holdsMark(slice, type) &&
            handlePaste.call(plugin, view, event, slice),
        },
      });
    });
  },

  addCommands() {
    const parent = this.parent?.() ?? {};
    // The URL of a command's attributes, as the link's field takes it, or
    // undefined for none that a link may lead to, as of no string.
    const hrefOf = (attributes: { href?: unknown }) =>
      typeof attributes.href === "string"
        ? typedHref(attributes.href)
        : undefined;
    return {
      ...parent,
      setLink: (attributes) => (props) => {
        const href = hrefOf(attributes);
        if (href === undefined || parent.setLink === undefined) {
          return false;
        }
        return parent.setLink({ ...attributes, href })(props);
      },
      toggleLink: (attributes) => (props) => {
        if (parent.toggleLink === undefined) {
          return false;
        }
        // Without a URL, it only takes a link off, rather than set one
        // without a URL, which the normalizer would take off again.
        if (attributes === undefined) {
          return (
            isMarkActive(props.state, this.type) && parent.toggleLink()(props)
          );
        }
        const href = hrefOf(attributes);
        return (
          href !== undefined &&
          parent.toggleLink({ ...attributes, href })(props)
        );
      },
    };
  },
}).configure({
  openOnClick: false,
  enableClickSelection: false,
  defaultProtocol: "https",
  // Autolinking and the paste's linking of a URL ask of linkify's text of
  // the URL, as typed, which autolinking leads to with this scheme where it
  // has none, and the paste as the link's field takes it (addPasteRules),
  // which is the same URL; the normalizer writes the link's URL as a link
  // stores it. Pasting a URL over a selection, which links the selection,
  // asks only shouldAutoLink, so that a URL that no link may lead to is
  // pasted as text over it, rather than linking it with a link that the
  // normalizer then takes off.
  isAllowedUri: (url) =>
    typeof url === "string" && typedHref(url) !== undefined,
  shouldAutoLink: (url) => autoLinks(url) && typedHref(url) !== undefined,
  HTMLAttributes: { rel: "noopener noreferrer nofollow", target: "_blank" },
});

/**
 * An ordered list, with the number of its first item in attrs.start and no
 * other attribute. Pasted plain text is ProseMirror's, a paragraph a line,
 * rather than the list that TipTap's plugin of an ordered list makes of
 * lines that each begin with a number, a Roman numeral, or one or two letters
 * before "." or ")", which would take "C." of "C. S. Lewis" for the number
 * 100, and leave it out of the text.
 */
const StoredOrderedList = OrderedList.extend({
  addAttributes() {
    return {
      start: {
        default: 1,
        parseHTML: (element) => startOf(element.getAttribute("start")),
      },
    };
  },
  addProseMirrorPlugins: () => [],
});

/**
 * Returns the pattern of an input rule of a mark of text typed between two
 * of delimiter, as "**", after white space or at the start, as TipTap's,
 * but only where no white space is just within either, as CommonMark's
 * emphasis has it, so that the asterisks of typed "2 * 3 * 4" stay text.
 * The text is the last group, which markInputRule marks.
 */
function emphasis(delimiter: string): RegExp {
  const d = delimiter.replace(/[*]/g, "\\*");
  const c = delimiter[0];
  return new RegExp(`(?:^|\\s)(${d}([^\\s${c}](?:[^${c}]*[^\\s${c}])?)${d})$`);
}

/**
 * The marks of the toolbar, whose Markdown-like input rules stay, as "**" or
 * "_" around typed text, those of bold, italic, and struck-through text only
 * where text follows and precedes the delimiters without white space
 * (emphasis), but not their paste rules, which would take the characters of
 * pasted text, as the asterisks of "2 * 3 * 4" or the underscores of
 * "__init__", for marks, and leave them out of it.
 */
const toolbarMarks = [
  Bold.extend({
    addInputRules() {
      return ["**", "__"].map((delimiter) =>
        markInputRule({ find: emphasis(delimiter), type: this.type }),
      );
    },
    addPasteRules: () => [],
  }),
  Italic.extend({
    addInputRules() {
      return ["*", "_"].map((delimiter) =>
        markInputRule({ find: emphasis(delimiter), type: this.type }),
      );
    },
    addPasteRules: () => [],
  }),
  Strike.extend({
    addInputRules() {
      return [markInputRule({ find: emphasis("~~"), type: this.type })];
    },
    addPasteRules: () => [],
  }),
  Code.extend({ addPasteRules: () => [] }),
];

/**
 * A list's item, which Tab moves, with the other items of the selection,
 * into a list of the item before them, as far as lists nest
 * (MAX_LIST_DEPTH), and Shift-Tab moves up into the list of the item that
 * holds their list. Where one cannot, its key moves the focus on, as to the
 * toolbar before it, rather than change the text: Tab from a paragraph, a
 * list's first item, which has no item before it, and an item that would
 * nest too deeply, and Shift-Tab from an item of a list that no list item
 * holds. Tab in a later item nests it, as TipTap's does, so that the
 * keyboard leaves the editor forwards from there only by changing the text,
 * or from another item.
 */
const NestingListItem = ListItem.extend({
  addKeyboardShortcuts() {
    return {
      Enter: () => this.editor.commands.splitListItem(this.name),
      Tab: () =>
        sinkFits(
          this.editor.state.selection.$from,
          this.editor.state.selection.$to,
        ) && this.editor.commands.sinkListItem(this.name),
      "Shift-Tab": () =>
        liftKeepsList(
          this.editor.state.selection.$from,
          this.editor.state.selection.$to,
        ) && this.editor.commands.liftListItem(this.name),
    };
  },
});

/** The options of the editor's extensions. */
export interface ExtensionOptions {
  /** What the editor shows of the references and mentions that a text holds, which it asks again as relabel says. */
  labels: ChipLabels;
  /** What the empty editor shows, which it asks again as relabel says, or undefined for nothing. */
  placeholder?: () => string;
  /** Called when the user asks to edit a link with the keyboard (Mod-K). */
  onLinkShortcut?: () => void;
  /**
   * Called with each key that goes down while a suggestion of { or @ is
   * open (suggest.ts), which reports whether the list that the page shows
   * of it took the key; none takes one without it.
   */
  onSuggestionKey?: (
    event: KeyboardEvent,
    open: Suggestion,
    view: EditorView,
  ) => boolean;
}

/** The editor's extensions, which make the schema of schema/rich-text/README.md and keep the document within it. */
export function richTextExtensions(options: ExtensionOptions): AnyExtension[] {
  const { labels, placeholder } = options;
  return [
    Document,
    Paragraph,
    Text,
    HardBreak,
    BulletList,
    StoredOrderedList,
    NestingListItem,
    ...toolbarMarks,
    StoredLink,
    Mention.configure({ labels }),
    ValueRef.configure({ labels }),
    UndoRedo,
    ...(placeholder === undefined
      ? []
      : [Placeholder.configure({ placeholder: () => placeholder() })]),
    // The list of a suggestion takes its keys, as Enter, before a paragraph
    // or a list item would split at it.
    Extension.create({
      name: "richTextSuggest",
      priority: 1000,
      addProseMirrorPlugins: () => [
        suggester(
          (event, open, view) =>
            options.onSuggestionKey?.(event, open, view) ?? false,
        ),
      ],
    }),
    Extension.create({
      name: "richTextRules",
      addProseMirrorPlugins: () => [normalizer(), relabeller(labels)],
      addKeyboardShortcuts() {
        return {
          // Code holds no link, as the toolbar's button says.
          "Mod-k": () => {
            if (
              options.onLinkShortcut === undefined ||
              !this.editor.isEditable ||
              this.editor.isActive("code")
            ) {
              return false;
            }
            options.onLinkShortcut();
            return true;
          },
        };
      },
    }),
  ];
}

/**
 * The editor's props for what is pasted or dropped: HTML is cleaned to what
 * the schema holds before it is read (FR-TEXT-01 AC2), and pasted HTML of
 * which the cleaning keeps nothing, as of only a button or a formula, or
 * only an empty paragraph, as of Word's, which ProseMirror would paste
 * nothing of (an open slice of size 0), is pasted as the clipboard's plain
 * text (pastePlain), which ProseMirror reads only where the clipboard has no
 * HTML, though it is only white space, as of a copied space, and the
 * editor's copy of a line break alone as a line break. A paste of neither,
 * as of only an image, changes nothing, rather
 * than take out the selection, which ProseMirror replaces with what is
 * pasted. So is a drop from outside the editor, at the place that it is
 * dropped at, of which ProseMirror would insert nothing.
 */
export const pasteProps = {
  transformPastedHTML: (html: string) => cleanPastedHTML(html),
  handlePaste: (view: EditorView, event: ClipboardEvent, slice: Slice) =>
    slice.size === 0 && pastePlain(view, event.clipboardData),
  handleDrop: (
    view: EditorView,
    event: DragEvent,
    slice: Slice,
    moved: boolean,
  ) => {
    if (moved || slice.size > 0) {
      return false;
    }
    const text = event.dataTransfer?.getData("text/plain") ?? "";
    const at = view.posAtCoords({ left: event.clientX, top: event.clientY });
    if (text === "" || at === null) {
      return false;
    }
    view.dispatch(
      view.state.tr.setSelection(
        TextSelection.near(view.state.doc.resolve(at.pos)),
      ),
    );
    view.focus();
    return pastePlain(view, event.dataTransfer);
  },
};

/**
 * Pastes the plain text of data, whose HTML gave nothing, over the selection,
 * or nothing where it has none. ProseMirror's copy of a line break alone,
 * whose plain text is the line break, and whose HTML's br ProseMirror's
 * parser leaves out as the last of its paragraph, is a line break, as was
 * copied, rather than the end of a paragraph that its plain text would be.
 */
function pastePlain(view: EditorView, data: DataTransfer | null): boolean {
  const text = data?.getData("text/plain") ?? "";
  if (text === "") {
    return true;
  }
  if (
    /^(?:\r\n?|\n)$/.test(text) &&
    /\sdata-pm-slice=/.test(data?.getData("text/html") ?? "")
  ) {
    const { hardBreak } = view.state.schema.nodes;
    view.dispatch(
      view.state.tr.replaceSelectionWith(hardBreak.create()).scrollIntoView(),
    );
    return true;
  }
  return view.pasteText(text);
}
