// The commands of the canvas's edits of a board's connections and of a
// branch's outputs (PRD FR-EDIT-03, FR-EDIT-04, plan note N69): each edit is
// one batch, undone as one step, whose command expects the revision that
// the page shows of the connection, or of the output, that it changes, or,
// of a drag of a connection's end, the one that it showed as the drag
// started. A connection that a batch creates expects nothing, as nothing
// has a revision of it yet: the server refuses one whose node went
// meanwhile. A reorder gives what it moves a key between those of its
// neighbours, so that it changes that one alone (FR-EDIT-03 AC2), as the
// tree of boards moves a child (project/orderkey.ts).

import type {
  BranchOutput,
  Connection,
  Node,
  NodePosition,
} from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import type { Command } from "../document/store";
import {
  compareChildren,
  keyAfterSibling,
  keyBeforeSibling,
} from "../project/orderkey";
import { createCommand } from "./commands";
import type { PortOf } from "./controller";

/**
 * Returns the connections that leave port of the node of nodeId, as view
 * holds them, in their order, which is the order of the choices that the
 * port offers (FR-EDIT-03 AC1).
 */
export function choicesOf(
  view: Pick<Doc, "connections">,
  nodeId: string,
  port: string,
): Connection[] {
  const out: Connection[] = [];
  for (const c of view.connections.values()) {
    if (
      c.from_node_id === nodeId &&
      c.from_port === port &&
      c.deleted_at === undefined
    ) {
      out.push(c);
    }
  }
  return out.sort(compareChildren);
}

/** A conditional output, whose key places it among its branch's. */
type Conditional = BranchOutput & { order: string };

/** Returns the conditional outputs of node, a branch, in their order, without its else, which has no place among them (FR-EDIT-04). */
export function conditionalsOf(node: Pick<Node, "outputs">): Conditional[] {
  return (node.outputs ?? []).filter(
    (o): o is Conditional => o.kind !== "else" && o.order !== undefined,
  );
}

/**
 * Returns the key that moves the child at from of siblings, the children
 * of one parent in their order, to to, its place among them once it has
 * moved, or undefined where it is there already, or where no key goes
 * between its neighbours there, as between two of the same key: before the
 * one that is at to now, of a child that moves up, and after it, of one
 * that moves down.
 */
export function keyAt(
  siblings: readonly { order: string }[],
  from: number,
  to: number,
): string | undefined {
  if (
    to === from ||
    from < 0 ||
    from >= siblings.length ||
    to < 0 ||
    to >= siblings.length
  ) {
    return undefined;
  }
  const rest = siblings.filter((_, i) => i !== from);
  return to < from ? keyBeforeSibling(rest, to) : keyAfterSibling(rest, to - 1);
}

/** Returns the command that connects from to the node of to, by a connection of the ID id, after the port's last. */
export function connectCommand(from: PortOf, to: string, id: string): Command {
  return {
    type: "connection.create",
    payload: {
      id,
      from_node_id: from.node,
      from_port: from.port,
      to_node_id: to,
    },
  };
}

/**
 * Returns the batch that creates a node of type on the board of boardId at
 * position and connects from to it, of the IDs ids, as one step
 * (FR-EDIT-03 AC3).
 */
export function connectNewBatch(
  boardId: string,
  from: PortOf,
  type: Node["type"],
  position: NodePosition,
  ids: { node: string; connection: string },
): Command[] {
  return [
    createCommand(boardId, type, position, ids.node),
    connectCommand(from, ids.node, ids.connection),
  ];
}

/**
 * Returns the command that reconnects the connection of id, so that it
 * leaves from, or enters the node of to, or both, expecting rev.
 */
export function reconnectCommand(
  id: string,
  ends: { from?: PortOf; to?: string },
  rev: number,
): Command {
  return {
    type: "connection.reconnect",
    payload: {
      id,
      ...(ends.from === undefined
        ? {}
        : { from_node_id: ends.from.node, from_port: ends.from.port }),
      ...(ends.to === undefined ? {} : { to_node_id: ends.to }),
    },
    expected_rev: { [id]: rev },
  };
}

/** Returns the command that gives connection the key order among its port's (FR-EDIT-03 AC2). */
export function reorderConnectionCommand(
  connection: Pick<Connection, "id" | "rev">,
  order: string,
): Command {
  return {
    type: "connection.reorder",
    payload: { id: connection.id, order },
    expected_rev: { [connection.id]: connection.rev },
  };
}

/** Returns the command that deletes connection to the trash. */
export function deleteConnectionCommand(
  connection: Pick<Connection, "id" | "rev">,
): Command {
  return {
    type: "connection.delete",
    payload: { id: connection.id },
    expected_rev: { [connection.id]: connection.rev },
  };
}

/** Returns the command that gives the branch of nodeId an output of the ID id, after its conditional outputs: one of them, or its else. */
export function createOutputCommand(
  nodeId: string,
  id: string,
  isElse: boolean,
): Command {
  return {
    type: "branch_output.create",
    payload: { id, node_id: nodeId, ...(isElse ? { else: true } : {}) },
  };
}

/** Returns the command that gives output the key order among its branch's conditional outputs. */
export function reorderOutputCommand(
  output: Pick<BranchOutput, "id" | "rev">,
  order: string,
): Command {
  return {
    type: "branch_output.reorder",
    payload: { id: output.id, order },
    expected_rev: { [output.id]: output.rev },
  };
}

/**
 * Returns the command that deletes output to the trash, with the
 * connections that leave it where confirm says that the user confirmed
 * that (FR-EDIT-04 AC3), without which the server refuses the deletion of
 * an output that connections leave.
 */
export function deleteOutputCommand(
  output: Pick<BranchOutput, "id" | "rev">,
  confirm: boolean,
): Command {
  return {
    type: "branch_output.delete",
    payload: { id: output.id, ...(confirm ? { confirm: true } : {}) },
    expected_rev: { [output.id]: output.rev },
  };
}
