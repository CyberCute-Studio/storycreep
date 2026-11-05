// The appliers of the commands of nodes and of branches' outputs (PRD
// FR-EDIT-01 to FR-EDIT-05), as package node applies them, and what the
// appliers of the project's other content share with them: the canonical
// form that the server stores a rich text in, what a payload's source makes
// of an expression, the derivation that compiles a batch's expressions
// again, and the keys of order that the server picks.
//
// A node's position and size are kept to hundredths, rounded half away from
// zero; its text is stored in its canonical form; and an expression that a
// payload writes is compiled by the server, against what the project names,
// so a change of one is not sure. A branch's outputs live in its node's
// outputs, but a change of one changes the output's revision, not the
// node's, so their appliers record the output's change alone
// (Draft.putQuietly). A node or an output that goes to the trash takes the
// connections that it is an end of out of every read with it, without a
// revision of theirs, and brings them back when it comes back, to a board
// that the document cannot tell, so it has every board that it holds read
// again.

import type {
  BranchOutput,
  Connection,
  Expression,
  Node,
  NodePosition,
  NodeSize,
  NodeTarget,
  RichTextDoc,
} from "@storycreep/shared/api";
import {
  compareChildren,
  keyBetween,
  type Ordered,
} from "../../project/orderkey";
import { type Appliers, canonical } from "../apply";
import { type Draft, nodeOfOutput, placeOf } from "../doc";

type Types =
  | `node.${"create" | "update" | "move" | "duplicate" | "delete" | "delete_many" | "restore" | "restore_many"}`
  | `branch_output.${"create" | "update" | "reorder" | "delete" | "restore"}`;

/**
 * Returns a key after the last of keys, those of the children of a parent
 * that the document holds: the server picks one after those in the trash
 * too, which it does not hold, so it is not sure.
 */
export function keyAfter(keys: Iterable<string | undefined>): string {
  let last: string | undefined;
  for (const key of keys) {
    if (key !== undefined && (last === undefined || key > last)) {
      last = key;
    }
  }
  return keyBetween(last, undefined) ?? "V";
}

/**
 * Returns the key that a child moving among its siblings without a key is
 * placed at (board.Place), and whether it is sure: its own, if it comes
 * after every other sibling, as the document holds them all but those in
 * the trash, or else one after the last, which is not.
 */
export function placed(
  moving: Ordered,
  siblings: Iterable<Ordered>,
): { order: string; sure: boolean } {
  const others = [...siblings].filter((s) => s.id !== moving.id);
  if (others.every((s) => compareChildren(s, moving) < 0)) {
    return { order: moving.order, sure: true };
  }
  return { order: keyAfter(others.map((s) => s.order)), sure: false };
}

/** Returns entity with field set to value, or without it for undefined, as the API leaves out a field that holds nothing. */
export function withField<T extends object, F extends keyof T>(
  entity: T,
  field: F,
  value: T[F] | undefined,
): T {
  const next = { ...entity };
  if (value === undefined) {
    Reflect.deleteProperty(next, field);
  } else {
    next[field] = value;
  }
  return next;
}

/** Returns id as the server writes it, or undefined for the empty ID, which a payload sends for none. */
export function idOrNone(id: string | undefined): string | undefined {
  return id === undefined || id === "" ? undefined : canonical(id);
}

type Json = Record<string, unknown>;

/** The marks, in the order of the canonical form, which is alphabetical. */
const markOrder: readonly string[] = [
  "bold",
  "code",
  "italic",
  "link",
  "strike",
];

/** Returns a node of rich text as the canonical form writes it: its fields in alphabetical order, without one that holds its default. */
function wire(
  type: string,
  fields: { attrs?: Json; content?: Json[]; marks?: Json[]; text?: string },
): Json {
  return {
    ...(fields.attrs === undefined ? {} : { attrs: fields.attrs }),
    ...(fields.content?.length ? { content: fields.content } : {}),
    ...(fields.marks?.length ? { marks: fields.marks } : {}),
    ...(fields.text === undefined ? {} : { text: fields.text }),
    type,
  };
}

function marksOf(marks: unknown): Json[] | undefined {
  if (marks === undefined) {
    return [];
  }
  if (!Array.isArray(marks)) {
    return undefined;
  }
  const out: Json[] = [];
  for (const mark of marks as Json[]) {
    const type = mark?.type;
    if (typeof type !== "string" || !markOrder.includes(type)) {
      return undefined;
    }
    out.push(
      type === "link"
        ? { attrs: { href: (mark.attrs as Json | undefined)?.href }, type }
        : { type },
    );
  }
  return out.sort(
    (a, b) =>
      markOrder.indexOf(a.type as string) - markOrder.indexOf(b.type as string),
  );
}

function inlinesOf(content: unknown): Json[] | undefined {
  if (content === undefined) {
    return [];
  }
  if (!Array.isArray(content)) {
    return undefined;
  }
  const out: Json[] = [];
  for (const node of content as Json[]) {
    const marks = marksOf(node?.marks);
    const attrs = node?.attrs as Json | undefined;
    if (marks === undefined) {
      return undefined;
    }
    switch (node.type) {
      case "text": {
        if (typeof node.text !== "string") {
          return undefined;
        }
        // A paragraph's neighbouring texts with the same marks are one text.
        const last = out.at(-1);
        if (
          last?.type === "text" &&
          JSON.stringify(last.marks ?? []) === JSON.stringify(marks)
        ) {
          out[out.length - 1] = wire("text", {
            marks,
            text: `${last.text}${node.text}`,
          });
        } else {
          out.push(wire("text", { marks, text: node.text }));
        }
        break;
      }
      case "hardBreak":
        out.push(wire("hardBreak", { marks }));
        break;
      case "mention":
        if (typeof attrs?.id !== "string") {
          return undefined;
        }
        out.push(
          wire("mention", { attrs: { id: canonical(attrs.id) }, marks }),
        );
        break;
      case "valueRef": {
        const { attribute, decimals, ref } = attrs ?? {};
        if (typeof ref !== "string") {
          return undefined;
        }
        out.push(
          wire("valueRef", {
            attrs: {
              ...(typeof attribute === "string" && attribute !== ""
                ? { attribute: canonical(attribute) }
                : {}),
              ...(decimals == null ? {} : { decimals }),
              ref: canonical(ref),
            },
            marks,
          }),
        );
        break;
      }
      default:
        return undefined;
    }
  }
  return out;
}

function blocksOf(content: unknown): Json[] | undefined {
  if (!Array.isArray(content)) {
    return undefined;
  }
  const out: Json[] = [];
  for (const block of content as Json[]) {
    switch (block?.type) {
      case "paragraph": {
        const inlines = inlinesOf(block.content);
        if (inlines === undefined) {
          return undefined;
        }
        out.push(wire("paragraph", { content: inlines }));
        break;
      }
      case "bulletList":
      case "orderedList": {
        if (!Array.isArray(block.content)) {
          return undefined;
        }
        const items: Json[] = [];
        for (const item of block.content as Json[]) {
          const blocks = item?.type === "listItem" && blocksOf(item.content);
          if (!blocks) {
            return undefined;
          }
          items.push(wire("listItem", { content: blocks }));
        }
        // An ordered list that starts at 1, as one that says no start, has
        // no attrs.
        const start = (block.attrs as Json | undefined)?.start;
        out.push(
          wire(block.type, {
            attrs:
              block.type === "orderedList" && start != null && start !== 1
                ? { start }
                : undefined,
            content: items,
          }),
        );
        break;
      }
      default:
        return undefined;
    }
  }
  return out;
}

/** Returns the empty text, one empty paragraph, which the server stores for a text that a payload leaves out. */
export function emptyText(): RichTextDoc {
  return { content: [{ type: "paragraph" }], type: "doc" };
}

/**
 * Returns text in the canonical form that the server stores it in
 * (schema/rich-text/README.md): no field that holds its default, an inline
 * node's marks in alphabetical order, a paragraph's neighbouring texts with
 * the same marks joined, each ID in its canonical spelling, and each
 * object's fields in alphabetical order; the empty text for none; or
 * undefined for a text that holds what this does not know.
 */
export function canonicalText(
  text: RichTextDoc | undefined,
): RichTextDoc | undefined {
  if (text === undefined) {
    return emptyText();
  }
  const blocks = blocksOf(text.content);
  if (blocks === undefined) {
    return undefined;
  }
  return blocks.length === 0
    ? emptyText()
    : (wire("doc", { content: blocks }) as unknown as RichTextDoc);
}

/** Returns the text that a payload's text makes, as the server stores it, and whether the document can tell it. */
export function textOf(text: RichTextDoc | undefined): {
  text: RichTextDoc;
  sure: boolean;
} {
  const stored = canonicalText(text);
  return stored === undefined
    ? { text: text as RichTextDoc, sure: false }
    : { text: stored, sure: true };
}

/**
 * Returns what a payload's source makes of an expression that is held, and
 * whether the document can tell it: the held one for no source, or for its
 * own, which the server does not write again; none for the empty source,
 * where it removes the expression; and otherwise the source, not compiled,
 * since the server compiles it against what the project names.
 */
export function scripted(
  held: Expression | undefined,
  source: string | undefined,
  emptyRemoves = true,
): { expression: Expression | undefined; sure: boolean } {
  if (source === undefined || held?.source === source) {
    return { expression: held, sure: true };
  }
  if (source === "" && emptyRemoves) {
    return { expression: undefined, sure: true };
  }
  return { expression: { source, is_valid: false }, sure: false };
}

/** An entity that owns expressions. */
type Owner = Node | Connection | BranchOutput;

/** The fields of an owner that hold its expressions. */
const expressionFields = [
  "on_enter",
  "on_exit",
  "condition",
  "on_traverse",
] as const;

/**
 * What each batch, by its draft, changes of what expressions name, for
 * which the server compiles again, once every command of the batch has
 * applied, each expression of the project that names it, without a revision
 * of an owner that the batch changed already (package expression's
 * derivation): every name, for a change of a variable, a component, a
 * component type, or an attribute definition, which an expression may name
 * or suggest as a near match; or the nodes that it creates, deletes, or
 * restores, which node references name by their IDs.
 */
const renamings = new WeakMap<Draft, { names: boolean; nodes: string[] }>();

/** Reports whether the server may compile an expression of owner otherwise, once draft's batch has applied, than as the document holds it. */
function recompiles(draft: Draft, owner: Owner): boolean {
  const renaming = renamings.get(draft);
  if (renaming === undefined) {
    return false;
  }
  return expressionFields.some((field) => {
    const source = (owner as unknown as Partial<Record<string, Expression>>)[
      field
    ]?.source.toLowerCase();
    return (
      source !== undefined &&
      (renaming.names ||
        renaming.nodes.some((id) => source.includes(id.toLowerCase())))
    );
  });
}

/** Returns sure, unless draft's batch changes what an expression of owner names, which the server then compiles again (renames). */
export function settled(draft: Draft, owner: Owner, sure = true): boolean {
  return sure && !recompiles(draft, owner);
}

/** Returns the owner of expressions of id that draft holds, or undefined. */
function ownerOf(draft: Draft, id: string): Owner | undefined {
  const place = placeOf(id);
  if (place?.of === "output") {
    return nodeOfOutput(draft.all("nodes"), id)?.outputs?.find(
      (o) => o.id === id,
    );
  }
  if (
    place?.of === "kind" &&
    (place.kind === "nodes" || place.kind === "connections")
  ) {
    return draft.get(place.kind, id);
  }
  return undefined;
}

/**
 * Records that draft's batch changes what expressions name: every name, or,
 * with nodeIds, the nodes of those IDs alone. A change of an owner whose
 * expressions may then compile otherwise is not sure, whichever command of
 * the batch made it, before this one or after.
 */
export function renames(draft: Draft, nodeIds?: readonly string[]): void {
  const renaming = renamings.get(draft) ?? { names: false, nodes: [] };
  if (nodeIds === undefined) {
    renaming.names = true;
  } else {
    renaming.nodes.push(...nodeIds);
  }
  renamings.set(draft, renaming);
  for (const id of draft.changes.keys()) {
    const owner = ownerOf(draft, id);
    if (owner !== undefined && recompiles(draft, owner)) {
      draft.changed(id, false);
    }
  }
}

/**
 * Returns x as the server keeps a position's or a size's number: in
 * hundredths, rounded half away from zero, read as the decimal that it is
 * written as (node/geometry.go), here as JavaScript writes it, which is how
 * a payload writes it, unless with more digits than a double holds.
 */
export function hundredths(x: number): number {
  const [mantissa, exponent = "0"] = String(Math.abs(x)).split("e");
  const [whole, fraction = ""] = mantissa.split(".");
  const digits = (whole + fraction).replace(/^0+/, "");
  if (digits === "") {
    return 0;
  }
  const shift = Number(exponent) - fraction.length + 2;
  let n: number;
  if (shift >= 0) {
    n = Number(digits + "0".repeat(shift));
  } else {
    // The first digit cut off rounds the rest up when it is 5 or more,
    // since what follows it can only add to it.
    const keep = digits.length + shift;
    n = keep > 0 ? Number(digits.slice(0, keep)) : 0;
    if (keep >= 0 && digits[keep] >= "5") {
      n++;
    }
  }
  return n === 0 ? 0 : (x < 0 ? -n : n) / 100;
}

function positionOf(position: NodePosition): NodePosition {
  return { x: hundredths(position.x), y: hundredths(position.y) };
}

/** Returns a payload's size as the server keeps it, or none, for {}. */
function sizeOf(size: NodeSize | undefined): NodeSize | undefined {
  return size?.width === undefined || size.height === undefined
    ? undefined
    : { width: hundredths(size.width), height: hundredths(size.height) };
}

/** Returns a payload's target as the server keeps it, or none, for {}. */
function targetOf(target: NodeTarget | undefined): NodeTarget | undefined {
  return target?.node_id === undefined || target.node_id === ""
    ? undefined
    : {
        board_id: canonical(target.board_id ?? ""),
        node_id: canonical(target.node_id),
      };
}

function nodeOf(draft: Draft, id: string): Node | undefined {
  return draft.get("nodes", canonical(id));
}

/**
 * Takes out of draft each connection that is gone, which went to the trash
 * with what goesWith returns of it, one of its ends or both, or the output
 * that it leaves, without a revision of its own, and which every read leaves
 * out; one that it goes with nothing of stays.
 */
function dropConnections(
  draft: Draft,
  goesWith: (connection: Connection) => string[],
): void {
  for (const connection of [...draft.all("connections").values()]) {
    const owners = goesWith(connection);
    if (owners.length > 0) {
      draft.removeQuietly("connections", connection.id, owners);
    }
  }
}

/**
 * Has the store read again every board's content that draft holds, as one
 * that a node or an output came back to, whose board the document cannot
 * tell, which brings back its connections without revisions of their own.
 */
export function rereadBoards(draft: Draft): void {
  for (const part of draft.base.parts) {
    if (part.startsWith("board:")) {
      draft.reread(part);
    }
  }
}

function deleteNodes(draft: Draft, ids: readonly string[]): void {
  const gone = new Set(ids.map((id) => canonical(id)));
  for (const id of gone) {
    draft.remove("nodes", id);
  }
  // A node's outputs go with it, in its outputs, and so do the connections
  // that leave or enter it, without a revision of their own.
  dropConnections(draft, (c) =>
    [c.from_node_id, c.to_node_id].filter((end) => gone.has(end)),
  );
  renames(draft, [...gone]);
}

/**
 * A node in the trash is none that the document holds: the store reads the
 * boards that it holds again, which brings back the node, should it be on
 * one, with its outputs and the connections that come back with it.
 */
function restoreNodes(draft: Draft, ids: readonly string[]): void {
  const back = ids
    .map((id) => canonical(id))
    .filter((id) => draft.get("nodes", id) === undefined);
  if (back.length > 0) {
    rereadBoards(draft);
    renames(draft, back);
  }
}

/** Returns outputs in their order: the conditional ones by their keys, then by their IDs, and the else after them. */
function inOrder(outputs: readonly BranchOutput[]): BranchOutput[] {
  const key = (o: BranchOutput): Ordered => ({
    id: o.id,
    order: o.order ?? "",
  });
  return [...outputs].sort((a, b) =>
    a.kind === "else"
      ? 1
      : b.kind === "else"
        ? -1
        : compareChildren(key(a), key(b)),
  );
}

/**
 * Puts node in draft with outputs, in their order, each conditional one of
 * the kind that its place gives it, the first an if and the others elseifs,
 * as the server rekinds them, and records the change of each output of
 * changed, with whether it is sure, and of each whose kind that changes.
 */
function putOutputs(
  draft: Draft,
  node: Node,
  outputs: readonly BranchOutput[],
  changed: ReadonlyMap<string, boolean>,
): void {
  const rekinded = new Set<string>();
  let first = true;
  const next = inOrder(outputs).map((output) => {
    if (output.kind === "else") {
      return output;
    }
    const kind: BranchOutput["kind"] = first ? "if" : "elseif";
    first = false;
    if (output.kind === kind) {
      return output;
    }
    rekinded.add(output.id);
    return { ...output, kind, updated_at: draft.time };
  });
  if (
    !draft.putQuietly(
      "nodes",
      withField(node, "outputs", next.length === 0 ? undefined : next),
    )
  ) {
    return;
  }
  for (const output of next) {
    const sure = changed.get(output.id);
    if (sure !== undefined || rekinded.has(output.id)) {
      draft.changed(output.id, settled(draft, output, sure ?? true));
    }
  }
}

/** Returns the output of id that draft holds, with its node, or undefined. */
function outputOf(
  draft: Draft,
  id: string,
): { node: Node; output: BranchOutput } | undefined {
  const outputId = canonical(id);
  const node = nodeOfOutput(draft.all("nodes"), outputId);
  const output = node?.outputs?.find((o) => o.id === outputId);
  return node && output && { node, output };
}

export const nodeAppliers: Appliers<Types> = {
  "node.create": (draft, p) => {
    const id = canonical(p.id);
    const text = p.type === "element" ? textOf(p.text) : undefined;
    const onEnter = scripted(undefined, p.on_enter);
    const onExit = scripted(undefined, p.on_exit);
    const size = sizeOf(p.size);
    const target = targetOf(p.target);
    const speaker = idOrNone(p.speaker_component_id);
    const cover = idOrNone(p.cover_asset_id);
    draft.put(
      "nodes",
      {
        id,
        project_id: draft.project?.id ?? "",
        board_id: canonical(p.board_id),
        type: p.type,
        ...(text === undefined
          ? {}
          : { kind: p.kind ?? "dialogue", text: text.text }),
        title: p.title ?? "",
        ...(speaker === undefined ? {} : { speaker_component_id: speaker }),
        ...(cover === undefined ? {} : { cover_asset_id: cover }),
        color: p.color ?? "none",
        tags: p.tags ?? [],
        position: positionOf(p.position),
        ...(size === undefined ? {} : { size }),
        is_ending: p.is_ending ?? false,
        ...(target === undefined ? {} : { target }),
        ...(onEnter.expression === undefined
          ? {}
          : { on_enter: onEnter.expression }),
        ...(onExit.expression === undefined
          ? {}
          : { on_exit: onExit.expression }),
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      (text?.sure ?? true) && onEnter.sure && onExit.sure,
    );
    renames(draft, [id]);
  },
  "node.update": (draft, p) => {
    const node = nodeOf(draft, p.id);
    if (node === undefined) {
      return;
    }
    const text = p.text === undefined ? undefined : textOf(p.text);
    const onEnter = scripted(node.on_enter, p.on_enter);
    const onExit = scripted(node.on_exit, p.on_exit);
    let next: Node = {
      ...node,
      ...(p.kind === undefined ? {} : { kind: p.kind }),
      ...(p.title === undefined ? {} : { title: p.title }),
      ...(text === undefined ? {} : { text: text.text }),
      ...(p.color === undefined ? {} : { color: p.color }),
      ...(p.tags === undefined ? {} : { tags: p.tags }),
      ...(p.is_ending === undefined ? {} : { is_ending: p.is_ending }),
      updated_at: draft.time,
    };
    if (p.speaker_component_id !== undefined) {
      next = withField(
        next,
        "speaker_component_id",
        idOrNone(p.speaker_component_id),
      );
    }
    if (p.cover_asset_id !== undefined) {
      next = withField(next, "cover_asset_id", idOrNone(p.cover_asset_id));
    }
    if (p.size !== undefined) {
      next = withField(next, "size", sizeOf(p.size));
    }
    if (p.target !== undefined) {
      next = withField(next, "target", targetOf(p.target));
    }
    next = withField(next, "on_enter", onEnter.expression);
    next = withField(next, "on_exit", onExit.expression);
    draft.put(
      "nodes",
      next,
      settled(draft, next, (text?.sure ?? true) && onEnter.sure && onExit.sure),
    );
  },
  "node.move": (draft, p) => {
    for (const place of p.nodes) {
      const node = nodeOf(draft, place.id);
      const position = positionOf(place.position);
      // The server changes only the nodes whose positions it changes.
      if (
        node !== undefined &&
        (node.position.x !== position.x || node.position.y !== position.y)
      ) {
        const next = { ...node, position, updated_at: draft.time };
        draft.put("nodes", next, settled(draft, next));
      }
    }
  },
  "node.duplicate": (draft, p) => {
    const source = nodeOf(draft, p.source_id);
    if (source === undefined) {
      return;
    }
    const id = canonical(p.id);
    // The copy has the source's scripts, which the server compiles at the
    // same board, to what the source's compile to, but neither its outputs
    // nor its connections.
    const node: Node = {
      ...withField(source, "outputs", undefined),
      id,
      position: positionOf(p.position),
      rev: 1,
      created_at: draft.time,
      updated_at: draft.time,
    };
    draft.put("nodes", node, settled(draft, node));
    renames(draft, [id]);
  },
  "node.delete": (draft, p) => deleteNodes(draft, [p.id]),
  "node.delete_many": (draft, p) => deleteNodes(draft, p.ids),
  "node.restore": (draft, p) => restoreNodes(draft, [p.id]),
  "node.restore_many": (draft, p) => restoreNodes(draft, p.ids),
  "branch_output.create": (draft, p) => {
    const node = nodeOf(draft, p.node_id);
    if (node === undefined) {
      return;
    }
    const outputs = node.outputs ?? [];
    const id = canonical(p.id);
    const times = { rev: 1, created_at: draft.time, updated_at: draft.time };
    if (p.else) {
      putOutputs(
        draft,
        node,
        [...outputs, { id, node_id: node.id, kind: "else", ...times }],
        new Map([[id, true]]),
      );
      return;
    }
    // A conditional output always has a condition, which the server
    // compiles, even of the empty source. Without a key, it goes after every
    // conditional output, those in the trash too, so it is the last of
    // those that the node holds, as kinds go, whatever its key.
    putOutputs(
      draft,
      node,
      [
        ...outputs,
        {
          id,
          node_id: node.id,
          kind: "elseif",
          order:
            p.order ??
            keyAfter(
              outputs.filter((o) => o.kind !== "else").map((o) => o.order),
            ),
          condition: { source: p.condition ?? "", is_valid: false },
          ...times,
        },
      ],
      new Map([[id, false]]),
    );
  },
  "branch_output.update": (draft, p) => {
    const found = outputOf(draft, p.id);
    if (found === undefined || p.condition === undefined) {
      return;
    }
    const { node, output } = found;
    // The empty source is kept, as a condition that does not compile.
    const condition = scripted(output.condition, p.condition, false);
    if (condition.expression === output.condition) {
      return;
    }
    putOutputs(
      draft,
      node,
      (node.outputs ?? []).map((o) =>
        o.id === output.id
          ? { ...o, condition: condition.expression, updated_at: draft.time }
          : o,
      ),
      new Map([[output.id, condition.sure]]),
    );
  },
  "branch_output.reorder": (draft, p) => {
    const found = outputOf(draft, p.id);
    if (found === undefined) {
      return;
    }
    const { node, output } = found;
    const outputs = node.outputs ?? [];
    const conditionals = outputs
      .filter((o) => o.kind !== "else")
      .map((o) => ({ id: o.id, order: o.order ?? "" }));
    const { order, sure } =
      p.order === undefined
        ? placed({ id: output.id, order: output.order ?? "" }, conditionals)
        : { order: p.order, sure: true };
    putOutputs(
      draft,
      node,
      outputs.map((o) =>
        o.id === output.id && order !== o.order
          ? { ...o, order, updated_at: draft.time }
          : o,
      ),
      new Map(order === output.order ? [] : [[output.id, sure]]),
    );
  },
  "branch_output.delete": (draft, p) => {
    const found = outputOf(draft, p.id);
    if (found === undefined) {
      return;
    }
    const { node, output } = found;
    putOutputs(
      draft,
      node,
      (node.outputs ?? []).filter((o) => o.id !== output.id),
      new Map(),
    );
    draft.changed(output.id);
    // The connections that leave it go to the trash with it.
    dropConnections(draft, (c) =>
      c.from_port === output.id ? [output.id] : [],
    );
  },
  // The output in the trash is none that the document holds, and the
  // payload does not tell its node: the store reads the boards that it
  // holds again, which brings back the connections that leave it.
  "branch_output.restore": (draft) => rereadBoards(draft),
};
