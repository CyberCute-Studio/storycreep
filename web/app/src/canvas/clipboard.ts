// What the canvas's clipboard computes (PRD FR-EDIT-07, plan note N71): the
// command that pastes a clipboard onto a board, with a new ID of the client's
// for each of its nodes, outputs, and connections, which moves every node by
// one offset, so that they keep their places relative to each other, as the
// clipboard keeps its connections in their order (AC2); where a paste goes,
// its nodes' middle at a point of the board; and the commands that create
// what the server's report says that the board's project lacks (AC1), before
// the paste, in the batches that the API takes.

import type {
  Clipboard,
  ClipboardAttribute,
  ClipboardReport,
  MissingComponent,
  NodePosition,
} from "@storycreep/shared/api";
import { type IdKind, newId } from "@storycreep/shared/ids";
import type { Command } from "../document/store";
import { positionLimit, snapped } from "./edit";
import { nodeRect } from "./layout";
import type { Point, Rect } from "./viewport";

/** Makes a new ID of kind, as the client makes those of what it creates. */
export type MakeId = (kind: IdKind) => string;

/**
 * Returns the command that pastes clipboard onto the board of boardId, each
 * node moved by offset, with a new ID for each of its nodes, outputs, and
 * connections, and the new IDs of its nodes, in the clipboard's order.
 */
export function pasteCommand(
  boardId: string,
  clipboard: Clipboard,
  offset: NodePosition,
  makeId: MakeId = newId,
): { command: Command; nodes: string[] } {
  const ids: Record<string, string> = {};
  const nodes: string[] = [];
  for (const node of clipboard.nodes) {
    const id = makeId("node");
    ids[node.id] = id;
    nodes.push(id);
    for (const output of node.outputs ?? []) {
      ids[output.id] = makeId("branch_output");
    }
  }
  for (const connection of clipboard.connections) {
    ids[connection.id] = makeId("connection");
  }
  return {
    command: {
      type: "clipboard.paste",
      payload: {
        board_id: boardId,
        clipboard,
        ids,
        offset: { x: offset.x, y: offset.y },
      },
    },
    nodes,
  };
}

/** Returns the rectangle that the nodes of clipboard take where they were copied, as the canvas lays them out, or undefined for one of no node. */
export function boundsOf(clipboard: Clipboard): Rect | undefined {
  let bounds: Rect | undefined;
  for (const node of clipboard.nodes) {
    const r = nodeRect(node);
    if (bounds === undefined) {
      bounds = r;
      continue;
    }
    const left = Math.min(bounds.x, r.x);
    const top = Math.min(bounds.y, r.y);
    bounds = {
      x: left,
      y: top,
      width: Math.max(bounds.x + bounds.width, r.x + r.width) - left,
      height: Math.max(bounds.y + bounds.height, r.y + r.height) - top,
    };
  }
  return bounds;
}

/**
 * Returns offset as near as the board's limits let it be: so that no node of
 * clipboard goes beyond them once moved by it, which the server would refuse
 * the paste for, as of a paste at the board's edge, rounded to hundredths,
 * as the server keeps a position.
 */
export function withinLimits(clipboard: Clipboard, offset: Point): Point {
  if (clipboard.nodes.length === 0) {
    return offset;
  }
  const xs = clipboard.nodes.map((n) => n.position.x);
  const ys = clipboard.nodes.map((n) => n.position.y);
  const clamp = (d: number, least: number, most: number) =>
    Math.max(
      Math.ceil((-positionLimit - least) * 100) / 100,
      Math.min(Math.floor((positionLimit - most) * 100) / 100, d),
    );
  return {
    x: clamp(offset.x, Math.min(...xs), Math.max(...xs)),
    y: clamp(offset.y, Math.min(...ys), Math.max(...ys)),
  };
}

/** How far a paste moves on from where a node of the board has its corner already, in units of the board: four steps of the grid, as a new node does (freePlace). */
const step = 32;

/**
 * Returns the offset that pastes the nodes of clipboard with their middle at
 * at of the board, on the grid where snap is on, as a new node is placed
 * (freePlace): moved by four steps of the grid across and down as often as
 * it takes, up to 100 times, where a node of the board has its corner where
 * one of them would have its own, as taken tells, as of a paste back where
 * the nodes were copied from, so that the copies do not hide them; and then
 * within the board's limits (withinLimits).
 */
export function pasteOffset(
  clipboard: Clipboard,
  at: Point,
  snap: boolean,
  taken: (corner: Point) => boolean,
): Point {
  const bounds = boundsOf(clipboard);
  if (bounds === undefined) {
    return { x: 0, y: 0 };
  }
  let offset = {
    x: snapped(at.x - (bounds.x + bounds.width / 2), snap),
    y: snapped(at.y - (bounds.y + bounds.height / 2), snap),
  };
  const hides = (d: Point) =>
    clipboard.nodes.some((n) =>
      taken({ x: n.position.x + d.x, y: n.position.y + d.y }),
    );
  for (let i = 0; i < 100 && hides(offset); i++) {
    offset = { x: offset.x + step, y: offset.y + step };
  }
  return withinLimits(clipboard, offset);
}

/** Reports whether report lists anything: what the paste would resolve to nothing, or leave out. */
export function lacksAny(report: ClipboardReport): boolean {
  return (
    creatable(report) ||
    report.speakers.length > 0 ||
    report.nodes.length > 0 ||
    report.assets.length > 0
  );
}

/** Reports whether report lists what a client creates before the paste: variables, components, or attribute definitions. */
export function creatable(report: ClipboardReport): boolean {
  return (
    report.variables.length > 0 ||
    report.components.length > 0 ||
    report.attributes.length > 0
  );
}

/** Returns the command that creates the attribute definition attribute, as the clipboard declares it, of the component type of typeId. */
function attributeCreate(
  typeId: string,
  attribute: ClipboardAttribute,
  makeId: MakeId,
): Command {
  return {
    type: "attribute.create",
    payload: {
      id: makeId("attribute_def"),
      component_type_id: typeId,
      name: attribute.name,
      key: attribute.key,
      type: attribute.type,
      ...(attribute.default === undefined
        ? {}
        : { default: attribute.default }),
      ...(attribute.options == null ? {} : { options: attribute.options }),
      ...(attribute.runtime === undefined
        ? {}
        : { runtime: attribute.runtime }),
    },
  };
}

/**
 * Returns the commands that create what report lists that a paste onto the
 * board of boardId resolves to nothing, as the server's report gives it, so
 * that the paste after them resolves to them (FR-EDIT-07 AC1): each
 * variable, as the clipboard declares it, a board's of the board; each
 * component type that a component that resolves to none has no type of the
 * project for, once for its key, with the attribute definitions that the
 * clipboard declares of its components that resolve to none, once for each
 * key; each attribute definition that a type of the project lacks; and each
 * component, of the type that the report gives it, or else of the one
 * created for it. Each has an ID of its own, and the definitions and the
 * components come after the types that they are of.
 */
export function createCommands(
  report: ClipboardReport,
  boardId: string,
  makeId: MakeId = newId,
): Command[] {
  const out: Command[] = [];
  for (const v of report.variables) {
    out.push({
      type: "variable.create",
      payload: {
        id: makeId("variable"),
        key: v.key,
        type: v.type,
        scope: v.scope,
        ...(v.default === undefined ? {} : { default: v.default }),
        ...(v.description === undefined || v.description === ""
          ? {}
          : { description: v.description }),
        ...(v.scope === "board"
          ? { board_id: boardId, reset_on_enter: v.reset_on_enter ?? false }
          : {}),
      },
    });
  }
  // The types created, by their keys, and the type of each component.
  const types = new Map<
    string,
    { id: string; attributes: Map<string, ClipboardAttribute> }
  >();
  const components: { missing: MissingComponent; typeId: string }[] = [];
  for (const missing of report.components) {
    if (missing.component_type_id !== undefined) {
      components.push({ missing, typeId: missing.component_type_id });
      continue;
    }
    const type = missing.component.type;
    let made = types.get(type.key);
    if (made === undefined) {
      made = { id: makeId("component_type"), attributes: new Map() };
      types.set(type.key, made);
      out.push({
        type: "component_type.create",
        payload: { id: made.id, name: type.name, key: type.key },
      });
    }
    for (const attribute of missing.component.attributes) {
      if (!made.attributes.has(attribute.key)) {
        made.attributes.set(attribute.key, attribute);
      }
    }
    components.push({ missing, typeId: made.id });
  }
  for (const made of types.values()) {
    for (const attribute of made.attributes.values()) {
      out.push(attributeCreate(made.id, attribute, makeId));
    }
  }
  for (const missing of report.attributes) {
    out.push(
      attributeCreate(missing.component_type_id, missing.attribute, makeId),
    );
  }
  for (const { missing, typeId } of components) {
    out.push({
      type: "component.create",
      payload: {
        id: makeId("component"),
        component_type_id: typeId,
        name: missing.component.name,
        key: missing.component.key,
      },
    });
  }
  return out;
}

/** The most commands that a batch holds (FR-API-03). */
export const maxBatchCommands = 1000;

/**
 * The most bytes of a batch's commands, as JSON, that the canvas sends in one
 * request: the 5 MiB that the server takes of a request's body, less what
 * the request holds besides them, as each command's ID, which batchBytes
 * counts, so that the server never refuses a batch for its size.
 */
export const maxBatchBytes = (5 << 20) - (64 << 10);

/** What a command's ID, and its comma, add to the command's JSON in a batch. */
const commandIdBytes = 64;

const encoder = new TextEncoder();

/** Returns how many bytes of a batch's request command takes, as JSON in UTF-8, with its ID. */
export function commandBytes(command: Command): number {
  return encoder.encode(JSON.stringify(command)).length + commandIdBytes;
}

/**
 * Returns the batches that send creates and then paste, in their order: the
 * paste's with as many of the last of creates as fit in it, and before it,
 * batches of the others, each of at most maxBatchCommands commands and
 * maxBatchBytes bytes, as a batch of the API takes, so that what is created
 * is in the paste's batch where it fits, and the paste and what it resolves
 * to are one step of the history then.
 */
export function pasteBatches(
  creates: readonly Command[],
  paste: Command,
  limits: { commands: number; bytes: number } = {
    commands: maxBatchCommands,
    bytes: maxBatchBytes,
  },
): Command[][] {
  const batches: Command[][] = [];
  let run: Command[] = [];
  let bytes = 0;
  for (const command of creates) {
    const size = commandBytes(command);
    if (
      run.length > 0 &&
      (run.length >= limits.commands || bytes + size > limits.bytes)
    ) {
      batches.push(run);
      run = [];
      bytes = 0;
    }
    run.push(command);
    bytes += size;
  }
  if (
    run.length < limits.commands &&
    bytes + commandBytes(paste) <= limits.bytes
  ) {
    run.push(paste);
    batches.push(run);
  } else {
    if (run.length > 0) {
      batches.push(run);
    }
    batches.push([paste]);
  }
  return batches;
}
