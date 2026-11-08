// What a board's canvas changes of the board (PRD FR-EDIT-02, FR-EDIT-06),
// as its toolbar, its inspector, its keys, and its drags ask: each a batch
// of commands, sent through the project's document, which shows it at once
// (optimistic, NFR-PERF-06), and the server's refusal of which the canvas's
// toolbar says (useCommands' error), while the document shows the board
// again as it was.

import type { Node } from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { useMemo } from "react";
import { useProjectDocument } from "../document/react";
import type { PaletteColor } from "../project/colors";
import { type Command, useCommands } from "../project/queries";
import {
  createCommand,
  deleteBatch,
  duplicateBatches,
  moveBatch,
  updateBatches,
} from "./commands";
import type { CanvasController } from "./controller";
import {
  type Alignment,
  aligned,
  distributed,
  freePlace,
  hasTag,
  offset,
  placedOf,
  withoutTag,
  withTag,
} from "./edit";
import { newNodeSize } from "./layout";
import { centerOf, type Point, type Size } from "./viewport";

/** What the canvas's controls change of the board. */
export type CanvasActions = ReturnType<typeof useCanvasActions>;

/**
 * Returns what the canvas of the board boardId, of the project projectId,
 * driven by controller, changes of it, each as a batch of the page's,
 * through commands, a mutation whose error the toolbar says.
 */
export function useCanvasActions(
  projectId: string,
  boardId: string,
  controller: CanvasController,
) {
  const doc = useProjectDocument(projectId);
  const commands = useCommands(projectId);
  const { mutateAsync } = commands;
  const ops = useMemo(() => {
    const view = () => doc.store.getState().view;
    /** The nodes of ids that the board has, as the page shows them. */
    const nodesOf = (ids: Iterable<string>): Node[] =>
      [...ids].flatMap((id) => {
        const node = view().nodes.get(id);
        return node === undefined ||
          node.board_id !== boardId ||
          node.deleted_at !== undefined
          ? []
          : [node];
      });
    /**
     * Sends batches in turn, each once the one before has applied, and
     * resolves with whether all of them did; a refusal, which the toolbar
     * says, sends no more of them.
     */
    const send = async (batches: Command[][]): Promise<boolean> => {
      for (const batch of batches) {
        if (batch.length === 0) {
          continue;
        }
        try {
          await mutateAsync(batch);
        } catch {
          return false;
        }
      }
      return true;
    };
    const place = (places: ReadonlyMap<string, Point>) =>
      places.size === 0
        ? Promise.resolve(true)
        : send([moveBatch(view(), places)]);
    return {
      /**
       * Creates a node of type at the centre of the canvas, on the grid
       * where it snaps, beside a node that is there already, and selects it.
       */
      create(type: Node["type"]): Promise<boolean> {
        const size = newNodeSize(type);
        const centre = centerOf(controller.viewport(), controller.size());
        const corners = new Set<string>();
        controller.scene.nodes.forEach((_, item) => {
          corners.add(`${item.node.position.x},${item.node.position.y}`);
        });
        const at = freePlace(
          centre,
          size,
          (p) => corners.has(`${p.x},${p.y}`),
          controller.snap,
        );
        const id = newId("node");
        const sent = send([[createCommand(boardId, type, at, id)]]);
        controller.selectComing([id], sent);
        return sent;
      },
      /** Moves the nodes of places there, as a drag left them, in one batch (FR-EDIT-02 AC2). */
      move: place,
      /** Gives the node of id size. */
      resize(id: string, size: Size): Promise<boolean> {
        return send(
          updateBatches(view(), [id], () => ({
            size: { width: size.width, height: size.height },
          })),
        );
      },
      /** Deletes the nodes of ids to the trash, as one command (FR-EDIT-02). */
      remove(ids: readonly string[]): Promise<boolean> {
        const nodes = nodesOf(ids).map((n) => n.id);
        return nodes.length === 0
          ? Promise.resolve(true)
          : send([deleteBatch(view(), nodes)]);
      },
      /**
       * Duplicates the nodes of ids onto the board, beside them, and selects
       * the copies, every one of them, though the batches after the first
       * show theirs later (selectComing).
       */
      duplicate(ids: readonly string[]): Promise<boolean> {
        const { batches, copies } = duplicateBatches(
          view(),
          nodesOf(ids).map((n) => n.id),
          { x: offset, y: offset },
        );
        const sent = send(batches);
        if (copies.length > 0) {
          controller.selectComing(copies, sent);
        }
        return sent;
      },
      /** Aligns the nodes of ids as how says, in one batch. */
      align(ids: readonly string[], how: Alignment): Promise<boolean> {
        return place(aligned(placedOf(nodesOf(ids)), how));
      },
      /** Distributes the nodes of ids across or down, in one batch. */
      distribute(
        ids: readonly string[],
        axis: "across" | "down",
      ): Promise<boolean> {
        return place(distributed(placedOf(nodesOf(ids)), axis));
      },
      /** Gives the nodes of ids color, each that has another. */
      color(ids: readonly string[], color: PaletteColor): Promise<boolean> {
        return send(
          updateBatches(
            view(),
            nodesOf(ids).map((n) => n.id),
            (node) => (node.color === color ? undefined : { color }),
          ),
        );
      },
      /** Adds tag to the nodes of ids, each that does not have it, whatever its case (FR-EDIT-06). */
      addTag(ids: readonly string[], tag: string): Promise<boolean> {
        return send(
          updateBatches(
            view(),
            nodesOf(ids).map((n) => n.id),
            (node) =>
              hasTag(node, tag) ? undefined : { tags: withTag(node.tags, tag) },
          ),
        );
      },
      /** Takes tag from the nodes of ids, each that has it, whatever its case. */
      removeTag(ids: readonly string[], tag: string): Promise<boolean> {
        return send(
          updateBatches(
            view(),
            nodesOf(ids).map((n) => n.id),
            (node) =>
              hasTag(node, tag)
                ? { tags: withoutTag(node.tags, tag) }
                : undefined,
          ),
        );
      },
      /** The nodes of ids that the board has, as the page shows them. */
      nodesOf,
    };
  }, [doc, boardId, controller, mutateAsync]);
  // The mutation of every batch of theirs, whose error the toolbar says.
  return { ...ops, commands };
}
