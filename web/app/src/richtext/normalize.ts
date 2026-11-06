// What keeps the editor's document one that the server stores, whatever made
// the change, typing, a paste or a drop, an input rule, or a command (PRD
// FR-TEXT-01, schema/rich-text/README.md): after each change, a text loses
// the characters that a text may not hold, a link that may not lead where it
// does loses its link, an ordered list's start comes within its range, and
// a list nested deeper than lists nest has its items moved up a level.

import {
  isControlCode,
  isHighSurrogate,
  isLowSurrogate,
  MAX_LIST_DEPTH,
} from "@storycreep/shared/richtext";
import { Fragment, type Mark, type Node as PMNode } from "@tiptap/pm/model";
import { Plugin, PluginKey, type Transaction } from "@tiptap/pm/state";
import { storedHref } from "./link";
import { isList, startOf } from "./schema";

/** Returns the plugin that keeps the document within the schema. */
export function normalizer(): Plugin {
  return new Plugin({
    key: new PluginKey("richTextNormalizer"),
    appendTransaction(transactions, before, state) {
      if (!transactions.some((tr) => tr.docChanged)) {
        return null;
      }
      const changed = changedRange(before.doc, state.doc);
      if (changed === undefined) {
        return null;
      }
      const tr = state.tr;
      cleanTexts(tr, changed.from, changed.to);
      cleanLinks(
        tr,
        tr.mapping.map(changed.from, -1),
        tr.mapping.map(changed.to, 1),
      );
      cleanStarts(tr);
      liftTooDeep(tr);
      if (!tr.docChanged) {
        return null;
      }
      // What a change that the undo history leaves out made, as a document
      // set from the server's, it leaves out too.
      if (transactions.some((t) => t.getMeta("addToHistory") === false)) {
        tr.setMeta("addToHistory", false);
      }
      return tr;
    },
  });
}

/**
 * Returns the range of after that differs from before, as ProseMirror
 * compares them, by the nodes that they share and then by their content,
 * whatever step changed it, with the node on each side of it, so that a
 * text that a deletion joined is in it; or undefined for none. Where the
 * change repeats what is next to it, the range is one of the places where
 * it could be, which holds the same content.
 */
function changedRange(
  before: PMNode,
  after: PMNode,
): { from: number; to: number } | undefined {
  const start = before.content.findDiffStart(after.content);
  if (start === null) {
    return undefined;
  }
  const { a, b } = before.content.findDiffEnd(after.content) ?? {
    a: before.content.size,
    b: after.content.size,
  };
  const end = Math.max(start, b + Math.max(0, start - a));
  return {
    from: Math.max(0, start - 1),
    to: Math.min(after.content.size, end + 1),
  };
}

/** Reports whether the UTF-16 code unit c stands for a line break, which a hardBreak takes the place of: a line feed, a carriage return, a line tabulation, a form feed, a next line, or a line or paragraph separator. */
function isLineBreak(c: number): boolean {
  return (
    c === 0x0a ||
    c === 0x0b ||
    c === 0x0c ||
    c === 0x0d ||
    c === 0x85 ||
    c === 0x2028 ||
    c === 0x2029
  );
}

/** Returns the UTF-16 code unit at i of the text of node, counted from its end for an i below 0, or NaN for none, as of a node that is no text. */
function unitOf(node: PMNode | null | undefined, i: number): number {
  const text = node?.isText ? (node.text ?? "") : "";
  return text.charCodeAt(i < 0 ? text.length + i : i);
}

/**
 * Takes out of each text the characters that a text may not hold
 * (isControlCode), as a U+200F RIGHT-TO-LEFT MARK that a keyboard of Hebrew
 * types, and a surrogate that is not one of a pair, and puts a hardBreak in
 * place of a line break, a carriage return and a line feed together being
 * one; a tab, which is white space, stays. A pair of surrogates, or a
 * carriage return and a line feed, that a mark's boundary splits between
 * two texts is one, which the first text takes, with its marks. It reads the
 * texts between from and to, where the document changed, since the rest
 * holds none of them.
 */
function cleanTexts(tr: Transaction, from: number, to: number) {
  /** What takes the place of what is between from and to: nothing, a hardBreak, or a text, as the pair of surrogates that a mark's boundary split, with the marks of the text that it is in. */
  const edits: ({ from: number; to: number } & (
    | { kind: "delete" }
    | { kind: "break"; marks: readonly Mark[] }
    | { kind: "text"; text: string; marks: readonly Mark[] }
  ))[] = [];
  tr.doc.nodesBetween(from, to, (node, pos, parent, index) => {
    const text = node.text;
    if (!node.isText || text === undefined) {
      return;
    }
    // The last code unit of the text before, and the first of the text
    // after, which are of the same characters where a mark's boundary
    // splits one.
    const before = unitOf(parent?.maybeChild(index - 1), -1);
    const after = unitOf(parent?.maybeChild(index + 1), 0);
    const unit = (i: number) =>
      i < 0 ? before : i < text.length ? text.charCodeAt(i) : after;
    for (let i = 0; i < text.length; i++) {
      const c = text.charCodeAt(i);
      if (
        i === 0 &&
        ((c === 0x0a && before === 0x0d) ||
          (isLowSurrogate(c) && isHighSurrogate(before)))
      ) {
        // The text before takes it.
        continue;
      }
      if (isLineBreak(c)) {
        const length = c === 0x0d && unit(i + 1) === 0x0a ? 2 : 1;
        edits.push({
          from: pos + i,
          to: pos + i + length,
          kind: "break",
          marks: node.marks,
        });
        i += length - 1;
      } else if (isHighSurrogate(c) && isLowSurrogate(unit(i + 1))) {
        if (i === text.length - 1) {
          edits.push({
            from: pos + i,
            to: pos + i + 2,
            kind: "text",
            text: String.fromCharCode(c, after),
            marks: node.marks,
          });
        }
        i++;
      } else if (
        isControlCode(c) ||
        // A surrogate that no pair took above is lone.
        isHighSurrogate(c) ||
        isLowSurrogate(c)
      ) {
        edits.push({ from: pos + i, to: pos + i + 1, kind: "delete" });
      }
    }
  });
  const schema = tr.doc.type.schema;
  for (const edit of edits.reverse()) {
    switch (edit.kind) {
      case "delete":
        tr.delete(edit.from, edit.to);
        break;
      case "break":
        tr.replaceWith(
          edit.from,
          edit.to,
          schema.nodes.hardBreak.create(null, null, edit.marks),
        );
        break;
      case "text":
        tr.replaceWith(edit.from, edit.to, schema.text(edit.text, edit.marks));
        break;
    }
  }
}

/** Writes the URL of each link between from and to, where the document changed, as a link stores it (storedHref), and takes the link off what leads where no link may. */
function cleanLinks(tr: Transaction, from: number, to: number) {
  const link = tr.doc.type.schema.marks.link;
  const edits: { from: number; to: number; mark: Mark; href?: string }[] = [];
  tr.doc.nodesBetween(from, to, (node, pos) => {
    if (!node.isInline) {
      return;
    }
    for (const mark of node.marks) {
      if (mark.type !== link) {
        continue;
      }
      const href =
        typeof mark.attrs.href === "string"
          ? storedHref(mark.attrs.href)
          : undefined;
      if (href !== mark.attrs.href) {
        edits.push({ from: pos, to: pos + node.nodeSize, mark, href });
      }
    }
  });
  for (const edit of edits) {
    tr.removeMark(edit.from, edit.to, edit.mark);
    if (edit.href !== undefined) {
      tr.addMark(edit.from, edit.to, link.create({ href: edit.href }));
    }
  }
}

/** Brings each ordered list's start within its range (startOf). */
function cleanStarts(tr: Transaction) {
  const edits: { pos: number; start: number }[] = [];
  tr.doc.descendants((node, pos) => {
    if (node.type.name === "orderedList") {
      const start = startOf(node.attrs.start);
      if (start !== node.attrs.start) {
        edits.push({ pos, start });
      }
    }
    return !node.isTextblock;
  });
  for (const edit of edits) {
    tr.setNodeMarkup(edit.pos, undefined, { start: edit.start });
  }
}

/**
 * Moves the items of each list nested deeper than lists nest
 * (MAX_LIST_DEPTH) up a level, into the list of the item that holds it,
 * after that item, so that their text keeps its order: what that item held
 * after the list goes to the end of the last of them. It moves one list at a
 * time, the first that it finds, until none is too deep.
 */
function liftTooDeep(tr: Transaction) {
  for (;;) {
    const found = tooDeep(tr.doc);
    if (found === undefined) {
      return;
    }
    const { pos, item, index } = found;
    const before: PMNode[] = [];
    const after: PMNode[] = [];
    item.forEach((child, _offset, i) => {
      if (i < index) {
        before.push(child);
      } else if (i > index) {
        after.push(child);
      }
    });
    const lifted: PMNode[] = [];
    item.child(index).forEach((child) => {
      lifted.push(child);
    });
    const last = lifted.length - 1;
    lifted[last] = lifted[last].copy(
      lifted[last].content.append(Fragment.from(after)),
    );
    tr.replaceWith(pos, pos + item.nodeSize, [
      item.copy(Fragment.from(before)),
      ...lifted,
    ]);
  }
}

/** Returns the first list item that holds a list nested deeper than lists nest, with its position and the list's index within it. */
function tooDeep(
  doc: PMNode,
): { pos: number; item: PMNode; index: number } | undefined {
  let found: { pos: number; item: PMNode; index: number } | undefined;
  // node's content starts at start, and lists hold it.
  const visit = (node: PMNode, start: number, lists: number) => {
    node.forEach((child, offset, index) => {
      if (found !== undefined) {
        return;
      }
      if (isList(child)) {
        if (lists + 1 > MAX_LIST_DEPTH && node.type.name === "listItem") {
          found = { pos: start - 1, item: node, index };
          return;
        }
        visit(child, start + offset + 1, lists + 1);
      } else if (child.type.name === "listItem") {
        visit(child, start + offset + 1, lists);
      }
    });
  };
  visit(doc, 0, 0);
  return found;
}
