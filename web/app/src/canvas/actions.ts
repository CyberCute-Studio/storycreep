// What a board's canvas changes of the board (PRD FR-EDIT-02, FR-EDIT-06),
// as its toolbar, its inspector, its keys, and its drags ask: each a batch
// of commands, whose undo is one step, of a colour, a tag, or a duplicate of
// at most maxEdited nodes, which the canvas's controls hold a selection to
// (plan note N68), sent through the project's document, which shows it at once (optimistic,
// NFR-PERF-06), and the server's refusal of which the canvas's toolbar says
// (error), while the document shows the board again as it was: the latest
// refusal of any of them, though another was sent after it, until the next
// is sent.

import type { CommandResultBody, Node } from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { useMutation } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useProjectDocument } from "../document/react";
import type { PaletteColor } from "../project/colors";
import type { Command } from "../project/queries";
import {
  createCommand,
  deleteBatch,
  duplicateBatch,
  maxEdited,
  moveBatch,
  updateBatch,
} from "./commands";
import type { CanvasController } from "./controller";
import {
  type Alignment,
  aligned,
  distributed,
  freePlace,
  hasTagOf,
  maxTags,
  offset,
  placedOf,
  withoutTagOf,
} from "./edit";
import { newNodeSize } from "./layout";
import { centerOf, type Point, type Size } from "./viewport";

/** What the canvas's controls change of the board. */
export type CanvasActions = ReturnType<typeof useCanvasActions>;

/**
 * Returns what the canvas of the board boardId, of the project projectId,
 * driven by controller, changes of it, each as a batch of the page's,
 * through commands, and the refusal that the toolbar says.
 */
export function useCanvasActions(
  projectId: string,
  boardId: string,
  controller: CanvasController,
) {
  const doc = useProjectDocument(projectId);
  // A mutation, as useCommands' is, so that a refusal reaches what the app
  // does of every mutation's (createQueryClient).
  const { mutateAsync } = useMutation({
    mutationFn: (batch: Command[]) => doc.send(batch),
  });
  // The latest refusal of a batch, with the error that it was refused with:
  // the mutation's own error is of its latest batch alone, as its observer
  // no longer follows one sent before that is still under way, as a drag's
  // move while the user duplicates, whose refusal it would not say.
  const [refusal, setRefusal] = useState<{ error: unknown }>();
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
     * Sends batch, and resolves with its answer, or with null for a batch
     * of no command, which it does not send, or with undefined once it was
     * refused, which the toolbar says.
     */
    const sendOne = async (
      batch: Command[],
    ): Promise<CommandResultBody | null | undefined> => {
      if (batch.length === 0) {
        return null;
      }
      setRefusal(undefined);
      try {
        return await mutateAsync(batch);
      } catch (error) {
        setRefusal({ error });
        return undefined;
      }
    };
    /** Sends batch as sendOne does, and resolves with whether it applied. */
    const send = async (batch: Command[]): Promise<boolean> =>
      (await sendOne(batch)) !== undefined;
    /**
     * Edits the nodes of ids that the board has, as nodesOf returns them,
     * by edit, and resolves with whether it applied, but for more than a
     * colour, a tag, or a duplicate changes at once (maxEdited), which the
     * canvas's controls do not offer, of which it sends nothing, and
     * resolves with false.
     */
    const editing = (
      ids: Iterable<string>,
      edit: (nodes: string[]) => Promise<boolean>,
    ): Promise<boolean> => {
      const nodes = nodesOf(ids).map((n) => n.id);
      return nodes.length > maxEdited ? Promise.resolve(false) : edit(nodes);
    };
    /**
     * Moves the nodes of places there, in one batch, expecting the
     * revisions that expected has of them, and else those that the page
     * shows, and resolves with whether it applied, and its answer, of none
     * where it moved nothing.
     */
    const place = async (
      places: ReadonlyMap<string, Point>,
      expected?: ReadonlyMap<string, number>,
    ): Promise<{ applied: boolean; answer?: CommandResultBody }> => {
      if (places.size === 0) {
        return { applied: true };
      }
      const answer = await sendOne(moveBatch(view(), places, expected));
      return {
        applied: answer !== undefined,
        answer: answer ?? undefined,
      };
    };
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
        const sent = send([createCommand(boardId, type, at, id)]);
        controller.selectComing([id], sent);
        return sent;
      },
      /**
       * Moves the nodes of places there, as a drag left them, in one batch
       * (FR-EDIT-02 AC2), expecting the revisions that they had as it
       * started, and resolves with whether it applied, and its answer,
       * which tells whether its undo joined the history.
       */
      move: place,
      /** Gives the node of id size, expecting rev, its revision as its handle's drag started, where it is given. */
      resize(id: string, size: Size, rev?: number): Promise<boolean> {
        return send(
          updateBatch(
            view(),
            [id],
            () => ({ size: { width: size.width, height: size.height } }),
            rev === undefined ? undefined : new Map([[id, rev]]),
          ),
        );
      },
      /** Deletes the nodes of ids to the trash, as one command (FR-EDIT-02). */
      remove(ids: readonly string[]): Promise<boolean> {
        return send(
          deleteBatch(
            view(),
            nodesOf(ids).map((n) => n.id),
          ),
        );
      },
      /**
       * Duplicates the nodes of ids onto the board, beside them, at most
       * maxEdited of them, and selects the copies, which the selection
       * keeps until their batch has settled, though the scene has them not
       * yet (selectComing).
       */
      duplicate(ids: readonly string[]): Promise<boolean> {
        return editing(ids, (nodes) => {
          const { batch, copies } = duplicateBatch(view(), nodes, {
            x: offset,
            y: offset,
          });
          const sent = send(batch);
          if (copies.length > 0) {
            controller.selectComing(copies, sent);
          }
          return sent;
        });
      },
      /** Aligns the nodes of ids as how says, in one batch. */
      async align(ids: readonly string[], how: Alignment): Promise<boolean> {
        return (await place(aligned(placedOf(nodesOf(ids)), how))).applied;
      },
      /** Distributes the nodes of ids across or down, in one batch. */
      async distribute(
        ids: readonly string[],
        axis: "across" | "down",
      ): Promise<boolean> {
        return (await place(distributed(placedOf(nodesOf(ids)), axis))).applied;
      },
      /** Gives the nodes of ids color, each that has another, of at most maxEdited of them. */
      color(ids: readonly string[], color: PaletteColor): Promise<boolean> {
        return editing(ids, (nodes) =>
          send(
            updateBatch(view(), nodes, (node) =>
              node.color === color ? undefined : { color },
            ),
          ),
        );
      },
      /**
       * Adds tag to the nodes of ids, each that does not have it, whatever
       * its case, but for one that has the most tags that a node has, which
       * the server would refuse the batch for, and with it the tag of every
       * other node (FR-EDIT-06).
       */
      addTag(ids: readonly string[], tag: string): Promise<boolean> {
        // The tag folded once, rather than for each node.
        const has = hasTagOf(tag);
        return editing(ids, (nodes) =>
          send(
            updateBatch(view(), nodes, (node) =>
              has(node) || node.tags.length >= maxTags
                ? undefined
                : { tags: [...node.tags, tag] },
            ),
          ),
        );
      },
      /** Takes tag from the nodes of ids, each that has it, whatever its case. */
      removeTag(ids: readonly string[], tag: string): Promise<boolean> {
        const has = hasTagOf(tag);
        const without = withoutTagOf(tag);
        return editing(ids, (nodes) =>
          send(
            updateBatch(view(), nodes, (node) =>
              has(node) ? { tags: without(node.tags) } : undefined,
            ),
          ),
        );
      },
      /** The nodes of ids that the board has, as the page shows them. */
      nodesOf,
    };
  }, [doc, boardId, controller, mutateAsync]);
  // The latest refusal of any batch of theirs, which the toolbar says, or
  // null for none: the same object until either changes.
  return useMemo(
    () => ({ ...ops, error: refusal === undefined ? null : refusal.error }),
    [ops, refusal],
  );
}
