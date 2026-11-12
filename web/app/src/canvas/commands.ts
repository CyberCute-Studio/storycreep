// The commands of the canvas's edits of a board's nodes (PRD FR-EDIT-02,
// FR-EDIT-06): each edit is one batch, undone as one step, of a command for
// each node that a colour or a tag changes, of at most
// maxEdited nodes, as many as a batch of the API holds commands for, each
// command expecting the revision that the page shows of each node that it
// changes (FR-API-03), as every change of the project's content does, or, of
// a drag, the one that it showed as the drag started, which the drag moved or
// sized the node from, so that a change of the same made elsewhere meanwhile
// is refused rather than written over (the document's conflicts). A move
// of the selection, and a deletion of it, are one command however many
// nodes they name, up to the 10,000 that one names (AC2).

import type {
  Node,
  NodePosition,
  NodeUpdatePayload,
} from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import type { Command } from "../document/store";
import type { Point } from "./viewport";

/** The most nodes that one node.move, node.delete_many, or node.restore_many names. */
export const maxNodes = 10_000;

/**
 * The most nodes that the canvas colours, tags, copies, cuts, or duplicates
 * at once (plan notes N68 and N71): a colour and a tag are a command for
 * each node, as many as a batch sent through the API holds (FR-API-03), so
 * that the edit is one batch, which undoes as one step, and a copy, and a
 * duplicate, which is a copy pasted, hold as many as a clipboard holds
 * (FR-EDIT-07).
 */
export const maxEdited = 1000;

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

/**
 * Returns the batch that moves the nodes of places there: one node.move for
 * each 10,000 of them, each expecting the revision of each node that
 * expected has, as a drag has it of the nodes as it started, which it moved
 * them from, and else the one that view shows.
 */
export function moveBatch(
  view: Pick<Doc, "nodes">,
  places: ReadonlyMap<string, Point>,
  expected?: ReadonlyMap<string, number>,
): Command[] {
  return chunks([...places], maxNodes).map((run) => {
    const revs = revsOf(
      view,
      run.map(([id]) => id),
    );
    for (const [id] of run) {
      const rev = expected?.get(id);
      if (rev !== undefined && id in revs) {
        revs[id] = rev;
      }
    }
    return {
      type: "node.move" as const,
      payload: {
        nodes: run.map(([id, position]) => ({
          id,
          position: { x: position.x, y: position.y },
        })),
      },
      expected_rev: revs,
    };
  });
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
 * Returns the batch that changes each node of ids as change says, a
 * node.update of each that it changes, of at most maxEdited nodes, which
 * the canvas holds a selection to: change returns the fields to change, or
 * undefined to leave the node as it is.
 * Each expects the revision of its node that expected has, as a resize has
 * it of the node as its drag started, and else the one that view shows.
 */
export function updateBatch(
  view: Pick<Doc, "nodes">,
  ids: Iterable<string>,
  change: (node: Node) => Omit<NodeUpdatePayload, "id"> | undefined,
  expected?: ReadonlyMap<string, number>,
): Command[] {
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
      expected_rev: { [id]: expected?.get(id) ?? node.rev },
    });
  }
  return commands;
}
