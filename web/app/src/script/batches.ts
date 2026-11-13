// The structural edits of a board's script view (PRD FR-TEXT-04, plan note
// N75), in the constrained form that it offers them: "add node after" an
// element, which inserts a new element between it and what followed it, and
// "add choice", which gives an element a new choice, to a new element. Each
// is one batch of the commands that the canvas sends for the same changes
// (canvas/commands.ts, canvas/flow.ts), whose undo is one step; the new
// node goes on the canvas to the right of the element, on the grid, where
// no node of the board has its corner already (freePlace).

import type { Connection, Node } from "@storycreep/shared/api";
import { createCommand } from "../canvas/commands";
import { freePlace, offset } from "../canvas/edit";
import {
  choicesOfPort,
  connectCommand,
  reconnectCommand,
} from "../canvas/flow";
import { newNodeSize, nodeRect } from "../canvas/layout";
import type { Point } from "../canvas/viewport";
import type { Command } from "../document/store";

/** The room between an element and a node that the view adds after it, across, in units of the board. */
const across = 80;

/** The IDs that a structural edit gives what it creates. */
export interface NewIds {
  node: string;
  connection: string;
}

/**
 * Returns where a new element goes on the canvas, as the view adds it after
 * node, the place-th of those that it adds there, 0 for the first, below the
 * one before: to the right of node, where none of the corners of others,
 * the board's nodes, is, on the grid.
 */
export function placeAfter(
  node: Node,
  others: Iterable<Node>,
  place = 0,
): Point {
  const size = newNodeSize("element");
  const rect = nodeRect(node);
  const corners = new Set<string>();
  for (const n of others) {
    corners.add(`${n.position.x},${n.position.y}`);
  }
  return freePlace(
    {
      x: rect.x + rect.width + across + size.width / 2,
      y: rect.y + rect.height / 2 + place * (size.height + offset),
    },
    size,
    (p) => corners.has(`${p.x},${p.y}`),
    true,
  );
}

/**
 * Returns the batch that inserts a new element, of ids, at position, after
 * node, an element: the connections that leave its port, its choices, in
 * their order, leave the new element instead, each expecting the revision
 * that leaving, those that leave node, show of it, and node is connected to
 * the new element, which so follows it, and goes before what followed it.
 */
export function addAfterBatch(
  node: Node,
  leaving: readonly Connection[],
  position: Point,
  ids: NewIds,
): Command[] {
  return [
    createCommand(node.board_id, "element", position, ids.node),
    ...choicesOfPort(leaving, "out").map((c) =>
      reconnectCommand(c.id, { from: { node: ids.node, port: "out" } }, c.rev),
    ),
    connectCommand({ node: node.id, port: "out" }, ids.node, ids.connection),
  ];
}

/**
 * Returns the batch that gives node, an element, a new choice, of ids: a
 * connection after its port's others, to a new element at position.
 */
export function addChoiceBatch(
  node: Node,
  position: Point,
  ids: NewIds,
): Command[] {
  return [
    createCommand(node.board_id, "element", position, ids.node),
    connectCommand({ node: node.id, port: "out" }, ids.node, ids.connection),
  ];
}
