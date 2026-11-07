// What the canvas writes on a node's card at a zoom that shows text (PRD
// FR-EDIT-01 AC4): its title, a line of its type, and an element's text as
// plain text, a branch's outputs, or a jumper's target. The rich-text
// editor of item 61 shows a text whole, with its marks; the card shows its
// opening, as plain words.

import type {
  Node,
  RichTextBlock,
  RichTextDoc,
  RichTextInline,
} from "@storycreep/shared/api";
import { excerptLength, wholeStart } from "./text";

/** What a card shows of a node, as text. */
export interface NodeLabels {
  title: string;
  /** The node's type, an element's kind and speaker, and whether it is an ending. */
  kind: string;
  /** An element's text, a branch's outputs, one to a line, or a jumper's target. */
  body: string;
}

/** What labels read from the page: its messages, and the names of what a node refers to. */
export interface LabelContext {
  t: (key: LabelKey, args?: Record<string, string>) => string;
  /** Returns the name of the component of id, if the project's document has it. */
  componentName: (id: string) => string | undefined;
  /** Returns the key of the variable of id, if the project's document has it. */
  variableKey: (id: string) => string | undefined;
  /** Returns the title of the node of id, if the project's document has it. */
  nodeTitle: (id: string) => string | undefined;
  /** Returns the name of the board of id, if the project's document has it. */
  boardName: (id: string) => string | undefined;
}

/** The messages that a card's labels use. */
export type LabelKey =
  | "canvas.node.untitled"
  | "canvas.node.dialogue"
  | "canvas.node.dialogue_by"
  | "canvas.node.narration"
  | "canvas.node.branch"
  | "canvas.node.jumper"
  | "canvas.node.kind_ending"
  | "canvas.node.jumper_to"
  | "canvas.node.jumper_to_board"
  | "canvas.node.jumper_none"
  | "canvas.node.output_if"
  | "canvas.node.output_elseif"
  | "canvas.node.output_else"
  | "canvas.node.no_outputs";

/** Returns what a card shows of node. */
export function labelsOf(node: Node, c: LabelContext): NodeLabels {
  const title = node.title === "" ? c.t("canvas.node.untitled") : node.title;
  switch (node.type) {
    case "element": {
      const speaker =
        node.speaker_component_id === undefined
          ? undefined
          : c.componentName(node.speaker_component_id);
      const kind =
        node.kind === "narration"
          ? c.t("canvas.node.narration")
          : speaker === undefined
            ? c.t("canvas.node.dialogue")
            : c.t("canvas.node.dialogue_by", { speaker });
      return {
        title,
        kind: node.is_ending ? c.t("canvas.node.kind_ending", { kind }) : kind,
        body: plainText(node.text, c),
      };
    }
    case "branch": {
      const outputs = node.outputs ?? [];
      return {
        title,
        kind: c.t("canvas.node.branch"),
        body:
          outputs.length === 0
            ? c.t("canvas.node.no_outputs")
            : outputs
                .map((o) => {
                  const condition = oneLine(o.condition?.source ?? "");
                  if (o.kind === "if") {
                    return c.t("canvas.node.output_if", { condition });
                  }
                  if (o.kind === "elseif") {
                    return c.t("canvas.node.output_elseif", { condition });
                  }
                  return c.t("canvas.node.output_else");
                })
                .join("\n"),
      };
    }
    case "jumper": {
      const target = node.target?.node_id;
      return {
        title,
        kind: c.t("canvas.node.jumper"),
        body:
          target === undefined
            ? c.t("canvas.node.jumper_none")
            : jumperTarget(target, node.target?.board_id, c),
      };
    }
  }
}

/**
 * Returns what a jumper's card says of the node of ID target, on the board
 * of ID board, that it goes to: its title, as its own card shows it,
 * untitled as there; or, while the project's document does not have the
 * node, as of a board that the page has not read, which the canvas reads
 * nothing of, or a node gone to the trash, the name of its board, which
 * the project's tree, read with the board's page, has; or, of a board that
 * the tree does not have either, the node's ID.
 */
function jumperTarget(
  target: string,
  board: string | undefined,
  c: LabelContext,
): string {
  const name = c.nodeTitle(target);
  if (name !== undefined) {
    return c.t("canvas.node.jumper_to", {
      target: name === "" ? c.t("canvas.node.untitled") : name,
    });
  }
  const boardName = board === undefined ? undefined : c.boardName(board);
  return boardName === undefined
    ? c.t("canvas.node.jumper_to", { target })
    : c.t("canvas.node.jumper_to_board", { board: boardName });
}

/** Reports whether a, a card's labels, shows the same as b, those that it was drawn with, or none, of a card not drawn yet. */
export function sameLabels(a: NodeLabels, b: NodeLabels | undefined): boolean {
  return (
    b !== undefined &&
    a.title === b.title &&
    a.kind === b.kind &&
    a.body === b.body
  );
}

/**
 * Reports whether the name that cards show of an entity, as name reads it,
 * as a component's name or a variable's key, differs between the maps
 * before and after, or an entity came or went, rather than anything else of
 * them, as a component's description, which cards do not show: each entity
 * that did not change is the same object in both, as the document copies
 * an entity only as it writes it.
 */
export function namesDiffer<T>(
  before: ReadonlyMap<string, T>,
  after: ReadonlyMap<string, T>,
  name: (entity: T) => string,
): boolean {
  if (before === after) {
    return false;
  }
  if (before.size !== after.size) {
    return true;
  }
  for (const [id, entity] of after) {
    const was = before.get(id);
    if (was !== entity && (was === undefined || name(was) !== name(entity))) {
      return true;
    }
  }
  return false;
}

/** Returns source on one line, as a card's row shows a condition. */
function oneLine(source: string): string {
  return source.replace(/\s+/g, " ").trim();
}

/**
 * Returns the plain text of doc, its paragraphs one to a line, at most
 * excerptLength characters of it: a value's reference as its variable's key
 * in braces, and a mention as its component's name after an @.
 */
export function plainText(
  doc: RichTextDoc | undefined,
  c: Pick<LabelContext, "componentName" | "variableKey">,
): string {
  if (doc === undefined) {
    return "";
  }
  const lines: string[] = [];
  let length = 0;
  const blocks = (content: RichTextBlock[]) => {
    for (const block of content) {
      if (length >= excerptLength) {
        return;
      }
      if (block.type === "paragraph") {
        const line = (block.content ?? []).map((i) => inline(i, c)).join("");
        lines.push(line);
        length += line.length + 1;
      } else {
        for (const item of block.content) {
          blocks(item.content);
        }
      }
    }
  };
  blocks(doc.content);
  const text = lines.join("\n").trim();
  if (text.length <= excerptLength) {
    return text;
  }
  // Whole characters, as a reader sees them, as the card's lines cut it
  // (text.ts): a cut within one, as between the two code units of an emoji,
  // or a letter and its accent, would leave a part of it, which a font
  // draws as a broken glyph.
  return `${text.slice(0, wholeStart(text, excerptLength - 1))}…`;
}

function inline(
  i: RichTextInline,
  c: Pick<LabelContext, "componentName" | "variableKey">,
): string {
  switch (i.type) {
    case "text":
      return i.text;
    case "hardBreak":
      return "\n";
    case "mention":
      return `@${c.componentName(i.attrs.id) ?? "…"}`;
    case "valueRef":
      return "attribute" in i.attrs
        ? `{${c.componentName(i.attrs.ref) ?? "…"}.…}`
        : `{${c.variableKey(i.attrs.ref) ?? "…"}}`;
  }
}
