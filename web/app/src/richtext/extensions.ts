// The editor's schema: TipTap's extensions of the nodes and marks of
// schema/rich-text/README.md, with only the attributes that it gives them,
// so that the editor writes the documents that the server stores (PRD
// FR-TEXT-01, ADR-020, plan note N40).

import { decodeAnyId, type IdKind } from "@storycreep/shared/ids";
import { MAX_DECIMALS } from "@storycreep/shared/richtext";
import {
  type AnyExtension,
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
import { storedHref } from "./link";
import { normalizer } from "./normalize";
import { cleanPastedHTML } from "./paste";
import { sinkFits, startOf } from "./schema";

/** What the editor shows of the value references and the mentions of a text, which name what the project holds. */
export interface RichTextNames {
  /** The name of a component, which its mention shows, or undefined for one that the page does not know. */
  mention: (id: string) => string | undefined;
  /** The name of a variable, or of a component's attribute, which its value reference shows, or undefined for one that the page does not know. */
  value: (ref: string, attribute: string | undefined) => string | undefined;
}

/** Reports whether id is the public ID of an entity of one of the kinds. */
function isIdOf(id: string | null, kinds: readonly IdKind[]): id is string {
  if (id === null) {
    return false;
  }
  try {
    return kinds.includes(decodeAnyId(id).kind);
  } catch {
    return false;
  }
}

/**
 * A mention of a component, an inline atom that shows the component's name
 * (FR-TEXT-03), with its ID in attrs.id. Its element is a span of
 * data-type="mention", which the paste's cleaning keeps, so that a mention
 * copied from one text is one in the text that it is pasted into.
 */
const Mention = Node.create<{ label: (id: string) => string }>({
  name: "mention",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addOptions() {
    return { label: (id: string) => id };
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
        getAttrs: (element) =>
          isIdOf(element.getAttribute("data-id"), ["component"]) ? null : false,
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
      `@${this.options.label(node.attrs.id)}`,
    ];
  },

  renderText({ node }) {
    return `@${this.options.label(node.attrs.id)}`;
  },
});

/**
 * A reference to a value, an inline atom that shows the value (FR-TEXT-02):
 * a variable's, with its ID in attrs.ref, or a component's runtime
 * attribute's, with the component's ID in attrs.ref and the attribute
 * definition's in attrs.attribute, and attrs.decimals, the digits after the
 * point that a number is shown with, or null for none.
 */
const ValueRef = Node.create<{
  label: (ref: string, attribute: string | undefined) => string;
}>({
  name: "valueRef",
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,

  addOptions() {
    return { label: (ref: string) => ref };
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
        getAttrs: (element) => {
          const ref = element.getAttribute("data-ref");
          const attribute = element.getAttribute("data-attribute");
          // A variable's value names no attribute, and a component's names
          // one of its attribute definitions.
          return (isIdOf(ref, ["variable"]) && attribute === null) ||
            (isIdOf(ref, ["component"]) && isIdOf(attribute, ["attribute_def"]))
            ? null
            : false;
        },
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
      `{${this.options.label(node.attrs.ref, node.attrs.attribute ?? undefined)}}`,
    ];
  },

  renderText({ node }) {
    return `{${this.options.label(node.attrs.ref, node.attrs.attribute ?? undefined)}}`;
  },
});

/**
 * A link, with its URL in attrs.href and no other attribute: it opens in a
 * new tab, without the page's referrer or a handle on it, and a click in the
 * editor puts the caret in it rather than following it. A URL typed or
 * pasted is linked where a link may lead to it (storedHref), as one that the
 * link's field takes.
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
  isAllowedUri: (url) => storedHref(url) !== undefined,
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
  /** What the editor shows of the references and mentions that a text holds. */
  names?: RichTextNames;
  /** What a mention or a value reference shows when names does not know what it refers to. */
  unknown: { mention: string; value: string };
  /** What the empty editor shows, or undefined for nothing. */
  placeholder?: string;
  /** Called when the user asks to edit a link with the keyboard (Mod-K). */
  onLinkShortcut?: () => void;
}

/** The editor's extensions, which make the schema of schema/rich-text/README.md and keep the document within it. */
export function richTextExtensions(options: ExtensionOptions): AnyExtension[] {
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
    Mention.configure({
      label: (id) => options.names?.mention(id) ?? options.unknown.mention,
    }),
    ValueRef.configure({
      label: (ref, attribute) =>
        options.names?.value(ref, attribute) ?? options.unknown.value,
    }),
    UndoRedo,
    ...(options.placeholder === undefined
      ? []
      : [Placeholder.configure({ placeholder: options.placeholder })]),
    Extension.create({
      name: "richTextRules",
      addProseMirrorPlugins: () => [normalizer()],
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
