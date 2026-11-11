// What the canvas writes on a node's card at a zoom that shows text (PRD
// FR-EDIT-01 AC4): its title, a line of its type, and an element's text as
// plain text, a branch's outputs, or a jumper's target. The rich-text
// editor of item 61 shows a text whole, with its marks; the card shows its
// opening, as plain words.

import type {
  BranchOutput,
  Node,
  RichTextBlock,
  RichTextDoc,
  RichTextInline,
} from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import { type JumperTarget, type TargetLookup, targetOf } from "./targets";
import { excerpt, excerptLength } from "./text";

/** What a card shows of a node, as text. */
export interface NodeLabels {
  title: string;
  /** The node's type, an element's kind and speaker, and whether it is an ending. */
  kind: string;
  /** An element's text, a branch's outputs, one to a line, or a jumper's target. */
  body: string;
  /** Whether the node is a jumper whose target was deleted, which its card is drawn as broken for (FR-EDIT-05 AC1). */
  broken?: boolean;
}

/** What labels read from the page: its messages, and the names of what a node refers to. */
export interface LabelContext {
  t: (key: LabelKey, args?: Record<string, string>) => string;
  /** Returns the name of the component of id, if the project's document has it. */
  componentName: (id: string) => string | undefined;
  /** Returns the key of the variable of id, if the project's document has it. */
  variableKey: (id: string) => string | undefined;
  /** Returns what the target of node, a jumper, is (targets.ts). */
  target: (node: Node) => JumperTarget;
  /** Returns the name of the board of id, if the project's document has it. */
  boardName: (id: string) => string | undefined;
}

/** What a card's labels read of the project's document, beyond its node: the names of what the node refers to. */
export type LabelSources = Pick<
  Doc,
  "nodes" | "components" | "variables" | "boards"
>;

/**
 * Returns what labels read of view, the project's document, as it is when
 * they are read, with the page's messages, t: every name that a card
 * shows, of what its node refers to, each of which namesChanged follows,
 * so that what cards read and what writes them again, once it changes, are
 * the same names, here; and a jumper's target, as view and targets tell of
 * it (targetOf), whose change the scene and the target's reads tell of.
 */
export function labelContext(
  view: LabelSources,
  t: LabelContext["t"],
  targets?: TargetLookup,
): LabelContext {
  return {
    t,
    componentName: (id) => view.components.get(id)?.name,
    variableKey: (id) => view.variables.get(id)?.key,
    target: (node) => targetOf(node, view, targets),
    boardName: (id) => view.boards.get(id)?.name,
  };
}

/**
 * Reports whether a name that labelContext reads changed from before to
 * after, the project's document before and after a change, of a component,
 * a variable, or a board, rather than anything else of them, as a
 * component's description, whose change would write every card anew for
 * nothing; but the titles of nodes, which the board's jumpers alone show,
 * and which the scene follows for them (Scene.targetsVersion), rather than
 * the title of every node of the project, whose map changes with each
 * change of any of them.
 */
export function namesChanged(
  before: LabelSources,
  after: LabelSources,
): boolean {
  return (
    namesDiffer(before.components, after.components, (c) => c.name) ||
    namesDiffer(before.variables, after.variables, (v) => v.key) ||
    namesDiffer(before.boards, after.boards, (b) => b.name)
  );
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
  | "canvas.node.jumper_to_on"
  | "canvas.node.jumper_to_board"
  | "canvas.node.jumper_none"
  | "canvas.node.jumper_broken"
  | "canvas.node.output_if"
  | "canvas.node.output_elseif"
  | "canvas.node.output_else"
  | "canvas.node.no_outputs";

/**
 * Returns the title of node as its card shows it, and so as what else names
 * it on the canvas, as the inspector and the editor of its text: untitled,
 * for one without.
 */
export function titleOf(
  node: Pick<Node, "title">,
  t: LabelContext["t"],
): string {
  return node.title === "" ? t("canvas.node.untitled") : node.title;
}

/**
 * Returns the name of the type of node, an element's of its kind, as its
 * card's line of its type names it without a speaker, and the inspector's
 * subtitle names it: none, of a type that the canvas does not know.
 */
export function typeOf(
  node: Pick<Node, "type" | "kind">,
  t: LabelContext["t"],
): string {
  switch (node.type) {
    case "element":
      return node.kind === "narration"
        ? t("canvas.node.narration")
        : t("canvas.node.dialogue");
    case "branch":
      return t("canvas.node.branch");
    case "jumper":
      return t("canvas.node.jumper");
    default:
      return "";
  }
}

/** Returns what a card shows of node. */
export function labelsOf(node: Node, c: LabelContext): NodeLabels {
  const title = titleOf(node, c.t);
  switch (node.type) {
    case "element": {
      const speaker =
        node.speaker_component_id === undefined
          ? undefined
          : c.componentName(node.speaker_component_id);
      const kind =
        node.kind === "narration" || speaker === undefined
          ? typeOf(node, c.t)
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
        kind: typeOf(node, c.t),
        body:
          outputs.length === 0
            ? c.t("canvas.node.no_outputs")
            : outputs.map((o) => outputLabel(o, c.t)).join("\n"),
      };
    }
    case "jumper": {
      const target = c.target(node);
      return {
        title,
        kind: typeOf(node, c.t),
        body: targetLabel(target, c),
        ...(target.state === "gone" ? { broken: true } : {}),
      };
    }
    default:
      // A type that the canvas does not know, as a newer server's may send
      // to a page loaded before it, shows its title alone, rather than
      // labels that each frame would throw at, leaving the board blank.
      return { title, kind: "", body: "" };
  }
}

/**
 * Returns what a branch's card says of output on its row, and so what else
 * names it, as the inspector: its kind, and, of a conditional output, its
 * condition, on one line.
 */
export function outputLabel(
  output: Pick<BranchOutput, "kind" | "condition">,
  t: LabelContext["t"],
): string {
  const condition = oneLine(output.condition?.source ?? "");
  if (output.kind === "if") {
    return t("canvas.node.output_if", { condition });
  }
  if (output.kind === "elseif") {
    return t("canvas.node.output_elseif", { condition });
  }
  return t("canvas.node.output_else");
}

/**
 * Returns what a jumper's card, and what else names its target, as its
 * inspector, says of target (PRD FR-EDIT-05): that it has none; the title
 * of the node that it goes to, as that node's own card shows it, untitled
 * as there, with the name of its board where that is another than the
 * jumper's; that its target was deleted; or, of a node that the page has
 * not read yet, as of a board that it has not opened, the name of its
 * board, which the project's tree, read with the board's page, has, or, of
 * a board that the tree does not have either, the node's ID.
 */
export function targetLabel(
  target: JumperTarget,
  c: Pick<LabelContext, "t" | "boardName">,
): string {
  switch (target.state) {
    case "none":
      return c.t("canvas.node.jumper_none");
    case "gone":
      return c.t("canvas.node.jumper_broken");
    case "found": {
      const title = titleOf(target, c.t);
      const board = target.elsewhere ? c.boardName(target.boardId) : undefined;
      return board === undefined
        ? c.t("canvas.node.jumper_to", { target: title })
        : c.t("canvas.node.jumper_to_on", { target: title, board });
    }
    case "unknown": {
      const board =
        target.boardId === undefined ? undefined : c.boardName(target.boardId);
      return board === undefined
        ? c.t("canvas.node.jumper_to", { target: target.nodeId })
        : c.t("canvas.node.jumper_to_board", { board });
    }
  }
}

/** Reports whether a, a card's labels, shows the same as b, those that it was drawn with, or none, of a card not drawn yet. */
export function sameLabels(a: NodeLabels, b: NodeLabels | undefined): boolean {
  return (
    b !== undefined &&
    a.title === b.title &&
    a.kind === b.kind &&
    a.body === b.body &&
    (a.broken ?? false) === (b.broken ?? false)
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
      } else if (block.type === "bulletList" || block.type === "orderedList") {
        for (const item of block.content) {
          blocks(item.content);
        }
      }
      // A block of a type that the canvas does not know, as a newer
      // server's may send to a page loaded before it, shows nothing, rather
      // than content that it may not have, which labels, and then each
      // frame, would throw at, leaving the board blank, as a node's type
      // that it does not know shows its title alone (labelsOf).
    }
  };
  blocks(doc.content);
  // Whole characters, as a reader sees them, as the card's lines cut it
  // (text.ts): a cut within one, as between the two code units of an emoji,
  // or a letter and its accent, would leave a part of it, which a font
  // draws as a broken glyph.
  return excerpt(lines.join("\n").trim());
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
