// The editor's schema: TipTap's extensions of the nodes and marks of
// schema/rich-text/README.md, with only the attributes that it gives them,
// so that the editor writes the documents that the server stores (PRD
// FR-TEXT-01, ADR-020, plan note N40).

import { MAX_DECIMALS } from "@storycreep/shared/richtext";
import {
  type AnyExtension,
  type Attributes,
  type Editor,
  Extension,
  mergeAttributes,
  Node,
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
import type { Node as PMNode, Slice } from "@tiptap/pm/model";
import { Plugin, PluginKey } from "@tiptap/pm/state";
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

/** What the editor shows of the value references and the mentions of a text, which name what the project holds. */
export interface RichTextNames {
  /** The name of a component, which its mention shows, or undefined for one that the page does not know. */
  mention: (id: string) => string | undefined;
  /** The name of a variable, or of a component's attribute, which its value reference shows, or undefined for one that the page does not know. */
  value: (ref: string, attribute: string | undefined) => string | undefined;
}

/** What a mention shows of its component, and a value reference of its value: their names, or a word for what they are. */
export interface ChipLabels {
  mention: (id: string) => string;
  value: (ref: string, attribute: string | undefined) => string;
}

/** Returns what a node of the editor shows as its text, if it is a mention or a value reference: @ before the component's name, or the value's name in braces. */
function chipText(node: PMNode, labels: ChipLabels): string | undefined {
  switch (node.type.name) {
    case "mention":
      return `@${labels.mention(node.attrs.id)}`;
    case "valueRef":
      return `{${labels.value(node.attrs.ref, node.attrs.attribute ?? undefined)}}`;
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
      return { labels: { mention: (id: string) => id, value: (ref) => ref } };
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

/**
 * A link, with its URL in attrs.href and no other attribute: it opens in a
 * new tab, without the page's referrer or a handle on it, and a click in the
 * editor puts the caret in it rather than following it. A URL typed or
 * pasted is linked where a link may lead to it, as the link's field takes
 * it (typedHref), as example.com, which linkify links to https://example.com,
 * and an email address, which it links to a mailto URL. setLink and
 * toggleLink take a URL as the link's field does too, and set it as a link
 * stores it, so that a link that they set is not taken off again by the
 * normalizer, and refuse one that no link may lead to.
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

  addCommands() {
    const parent = this.parent?.() ?? {};
    return {
      ...parent,
      setLink: (attributes) => (props) => {
        const href = typedHref(attributes.href);
        if (href === undefined || parent.setLink === undefined) {
          return false;
        }
        return parent.setLink({ ...attributes, href })(props);
      },
      toggleLink: (attributes) => (props) => {
        if (parent.toggleLink === undefined) {
          return false;
        }
        if (attributes === undefined) {
          return parent.toggleLink()(props);
        }
        const href = typedHref(attributes.href);
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
  // the URL, as typed, which the link leads to with linkify's scheme; the
  // normalizer writes the link's URL as a link stores it. Pasting a URL over
  // a selection, which links the selection, asks only shouldAutoLink, so
  // that a URL that no link may lead to is pasted as text over it, rather
  // than linking it with a link that the normalizer then takes off.
  isAllowedUri: (url) => typedHref(url) !== undefined,
  shouldAutoLink: (url) => autoLinks(url) && typedHref(url) !== undefined,
  HTMLAttributes: { rel: "noopener noreferrer nofollow", target: "_blank" },
});

/** An ordered list, with the number of its first item in attrs.start and no other attribute. */
const StoredOrderedList = OrderedList.extend({
  addAttributes() {
    return {
      start: {
        default: 1,
        parseHTML: (element) => startOf(element.getAttribute("start")),
      },
    };
  },
});

/**
 * A list's item, which Tab moves, with the other items of the selection,
 * into a list of the item before them, as far as lists nest
 * (MAX_LIST_DEPTH), and Shift-Tab moves up into the list of the item that
 * holds their list. Where one cannot, its key moves the focus on, so that
 * the keyboard leaves the editor from any item, forwards or back, as to the
 * toolbar before it, without changing the text.
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
    Bold,
    Italic,
    Strike,
    Code,
    StoredLink,
    Mention.configure({ labels }),
    ValueRef.configure({ labels }),
    UndoRedo,
    ...(placeholder === undefined
      ? []
      : [Placeholder.configure({ placeholder: () => placeholder() })]),
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
 * which the cleaning keeps nothing, as of only a button or a formula, is
 * pasted as the clipboard's plain text, which ProseMirror reads only where
 * the clipboard has no HTML.
 */
export const pasteProps = {
  transformPastedHTML: (html: string) => cleanPastedHTML(html),
  handlePaste: (view: EditorView, event: ClipboardEvent, slice: Slice) => {
    const text = event.clipboardData?.getData("text/plain") ?? "";
    return slice.content.size === 0 && text.trim() !== ""
      ? view.pasteText(text)
      : false;
  },
};
