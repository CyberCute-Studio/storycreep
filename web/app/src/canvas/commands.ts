// The commands of the canvas's edits of a board's nodes (PRD FR-EDIT-02,
// FR-EDIT-06): each edit is one batch, or, of more nodes than a batch of the
// API holds commands for, a batch for each thousand of them, each command
// expecting the revision that the page shows of each node that it changes or
// copies (FR-API-03), as every change of the project's content does. A move
// of the selection, and a deletion of it, are one command however many
// nodes they name, up to the 10,000 that one names (AC2).

import type {
  Node,
  NodePosition,
  NodeUpdatePayload,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import type { Doc } from "../document/doc";
import type { Command } from "../document/store";
import { onBoard } from "./edit";
import type { Point } from "./viewport";

/** The most commands that a batch sent through the API holds (FR-API-03). */
export const maxCommands = 1000;

/** The most nodes that one node.move, node.delete_many, or node.restore_many names. */
export const maxNodes = 10_000;

/** Returns items in runs of at most size. */
function chunks<T>(items: readonly T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    out.push(items.slice(i, i + size));
  }
  return out;
}

/** Returns what a command expects of the nodes of ids that view holds: the revisions that it shows. */
function revsOf(
  view: Pick<Doc, "nodes">,
  ids: Iterable<string>,
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const id of ids) {
    const node = view.nodes.get(id);
    if (node !== undefined) {
      out[id] = node.rev;
    }
  }
  return out;
}

/** Returns the batch that moves the nodes of places there: one node.move for each 10,000 of them. */
export function moveBatch(
  view: Pick<Doc, "nodes">,
  places: ReadonlyMap<string, Point>,
): Command[] {
  return chunks([...places], maxNodes).map((run) => ({
    type: "node.move" as const,
    payload: {
      nodes: run.map(([id, position]) => ({
        id,
        position: { x: position.x, y: position.y },
      })),
    },
    expected_rev: revsOf(
      view,
      run.map(([id]) => id),
    ),
  }));
}

/** Returns the command that creates a node of type on the board of boardId at position, of the ID id. */
export function createCommand(
  boardId: string,
  type: Node["type"],
  position: NodePosition,
  id: string,
): Command {
  return {
    type: "node.create",
    payload: { board_id: boardId, id, type, position },
  };
}

/** Returns the batch that deletes the nodes of ids to the trash: node.delete for one, and node.delete_many for more. */
export function deleteBatch(
  view: Pick<Doc, "nodes">,
  ids: readonly string[],
): Command[] {
  if (ids.length === 1) {
    return [
      {
        type: "node.delete",
        payload: { id: ids[0] },
        expected_rev: revsOf(view, ids),
      },
    ];
  }
  return chunks(ids, maxNodes).map((run) => ({
    type: "node.delete_many" as const,
    payload: { ids: run },
    expected_rev: revsOf(view, run),
  }));
}

/**
 * Returns the batches that duplicate the nodes of ids onto their board,
 * each moved by offset, with the IDs of the copies, in the order of ids:
 * a node.duplicate of each, a thousand to a batch.
 */
export function duplicateBatches(
  view: Pick<Doc, "nodes">,
  ids: readonly string[],
  offset: Point,
  makeId: () => string = () => newId("node"),
): { batches: Command[][]; copies: string[] } {
  const copies: string[] = [];
  const commands: Command[] = [];
  for (const id of ids) {
    const node = view.nodes.get(id);
    if (node === undefined) {
      continue;
    }
    const copy = makeId();
    copies.push(copy);
    commands.push({
      type: "node.duplicate",
      payload: {
        id: copy,
        source_id: id,
        position: onBoard({
          x: node.position.x + offset.x,
          y: node.position.y + offset.y,
        }),
      },
      expected_rev: { [id]: node.rev },
    });
  }
  return { batches: chunks(commands, maxCommands), copies };
}

/**
 * Returns the batches that change each node of ids as change says, a
 * node.update of each that it changes, a thousand to a batch: change
 * returns the fields to change, or undefined to leave the node as it is.
 */
export function updateBatches(
  view: Pick<Doc, "nodes">,
  ids: Iterable<string>,
  change: (node: Node) => Omit<NodeUpdatePayload, "id"> | undefined,
): Command[][] {
  const commands: Command[] = [];
  for (const id of ids) {
    const node = view.nodes.get(id);
    const fields = node === undefined ? undefined : change(node);
    if (node === undefined || fields === undefined) {
      continue;
    }
    commands.push({
      type: "node.update",
      payload: { id, ...fields },
      expected_rev: { [id]: node.rev },
    });
  }
  return chunks(commands, maxCommands);
}
