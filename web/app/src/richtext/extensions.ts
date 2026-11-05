// The editor's schema: TipTap's extensions of the nodes and marks of
// schema/rich-text/README.md, with only the attributes that it gives them,
// so that the editor writes the documents that the server stores (PRD
// FR-TEXT-01, ADR-020, plan note N40).

import { MAX_DECIMALS } from "@storycreep/shared/richtext";
import {
  type AnyExtension,
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
import type { Node as PMNode } from "@tiptap/pm/model";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { typedHref } from "./link";
import { normalizer } from "./normalize";
import { cleanPastedHTML } from "./paste";
import {
  isMentionElement,
  isValueRefElement,
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
 * A mention of a component, an inline atom that shows the component's name
 * (FR-TEXT-03), with its ID in attrs.id. Its element is a span of
 * data-type="mention", which the paste's cleaning keeps, so that a mention
 * copied from one text is one in the text that it is pasted into.
 */
const Mention = Node.create<{ labels: ChipLabels }>({
  name: "mention",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addOptions() {
    return { labels: { mention: (id: string) => id, value: (ref) => ref } };
  },

  addAttributes() {
    return {
      id: {
        isRequired: true,
        parseHTML: (element) => element.getAttribute("data-id"),
        renderHTML: (attributes) => ({ "data-id": attributes.id }),
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: 'span[data-type="mention"]',
        getAttrs: (element) => (isMentionElement(element) ? null : false),
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-type": "mention",
        class: "rich-text-chip",
      }),
      chipText(node, this.options.labels) ?? "",
    ];
  },

  renderText({ node }) {
    return chipText(node, this.options.labels) ?? "";
  },
});

/**
 * A reference to a value, an inline atom that shows the value (FR-TEXT-02):
 * a variable's, with its ID in attrs.ref, or a component's runtime
 * attribute's, with the component's ID in attrs.ref and the attribute
 * definition's in attrs.attribute, and attrs.decimals, the digits after the
 * point that a number is shown with, or null for none.
 */
const ValueRef = Node.create<{ labels: ChipLabels }>({
  name: "valueRef",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addOptions() {
    return { labels: { mention: (id: string) => id, value: (ref) => ref } };
  },

  addAttributes() {
    return {
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
    };
  },

  parseHTML() {
    return [
      {
        tag: 'span[data-type="value-ref"]',
        getAttrs: (element) => (isValueRefElement(element) ? null : false),
      },
    ];
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        "data-type": "value-ref",
        class: "rich-text-chip",
      }),
      chipText(node, this.options.labels) ?? "",
    ];
  },

  renderText({ node }) {
    return chipText(node, this.options.labels) ?? "";
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

/**
 * A link, with its URL in attrs.href and no other attribute: it opens in a
 * new tab, without the page's referrer or a handle on it, and a click in the
 * editor puts the caret in it rather than following it. A URL typed or
 * pasted is linked where a link may lead to it, as the link's field takes
 * it (typedHref), as example.com, which linkify links to https://example.com,
 * and an email address, which it links to a mailto URL.
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
}).configure({
  openOnClick: false,
  enableClickSelection: false,
  defaultProtocol: "https",
  // Autolinking and the paste's linking of a URL ask of linkify's text of
  // the URL, as typed, which the link leads to with linkify's scheme; the
  // normalizer writes the link's URL as a link stores it.
  isAllowedUri: (url) => typedHref(url) !== undefined,
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
 * A list's item, which Tab moves into a list of the item before it, as far
 * as lists nest (MAX_LIST_DEPTH), and otherwise leaves to move the focus on,
 * so that the keyboard leaves the editor from any item.
 */
const NestingListItem = ListItem.extend({
  addKeyboardShortcuts() {
    return {
      Enter: () => this.editor.commands.splitListItem(this.name),
      Tab: () =>
        sinkFits(this.editor.state.selection.$from) &&
        this.editor.commands.sinkListItem(this.name),
      "Shift-Tab": () => this.editor.commands.liftListItem(this.name),
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

/** The editor's props for what is pasted or dropped: HTML is cleaned to what the schema holds before it is read (FR-TEXT-01 AC2). */
export const pasteProps = {
  transformPastedHTML: (html: string) => cleanPastedHTML(html),
};
