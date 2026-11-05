// The appliers of the commands of connections (PRD FR-EDIT-03), as package
// connection applies them: a connection's label is stored in its canonical
// form, and its expressions as their sources, which the server compiles; a
// key of order that the payload leaves out is the server's to pick, after
// every connection of the port, those in the trash too; and a connection
// that comes back from the trash is none that the document holds, which the
// store reads.

import type { Connection } from "@storycreep/shared/api";
import { type Appliers, canonical } from "../apply";
import type { Draft } from "../doc";
import {
  idOrNone,
  keyAfter,
  placed,
  scripted,
  settled,
  textOf,
  withField,
} from "./node";

type Types =
  `connection.${"create" | "update" | "reconnect" | "reorder" | "delete" | "restore"}`;

/** Returns the connections that draft holds that leave the port of the node of nodeId. */
function ofPort(draft: Draft, nodeId: string, port: string): Connection[] {
  return [...draft.all("connections").values()].filter(
    (c) => c.from_node_id === nodeId && c.from_port === port,
  );
}

/** Returns a payload's port as the server writes it: an output's ID, or out, an element's one port, for none. */
function portOf(port: string | undefined): string {
  return idOrNone(port) ?? "out";
}

export const connectionAppliers: Appliers<Types> = {
  "connection.create": (draft, p) => {
    const from = draft.get("nodes", canonical(p.from_node_id));
    if (from === undefined) {
      return;
    }
    const port = portOf(p.from_port);
    const label = textOf(p.label);
    const condition = scripted(undefined, p.condition);
    const onTraverse = scripted(undefined, p.on_traverse);
    draft.put(
      "connections",
      {
        id: canonical(p.id),
        project_id: from.project_id,
        board_id: from.board_id,
        from_node_id: from.id,
        from_port: port,
        to_node_id: canonical(p.to_node_id),
        order:
          p.order ?? keyAfter(ofPort(draft, from.id, port).map((c) => c.order)),
        label: label.text,
        ...(condition.expression === undefined
          ? {}
          : { condition: condition.expression }),
        ...(onTraverse.expression === undefined
          ? {}
          : { on_traverse: onTraverse.expression }),
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.order !== undefined && label.sure && condition.sure && onTraverse.sure,
    );
  },
  "connection.update": (draft, p) => {
    const connection = draft.get("connections", canonical(p.id));
    if (connection === undefined) {
      return;
    }
    const label = p.label == null ? undefined : textOf(p.label);
    const condition = scripted(connection.condition, p.condition ?? undefined);
    const onTraverse = scripted(
      connection.on_traverse,
      p.on_traverse ?? undefined,
    );
    let next: Connection = {
      ...connection,
      ...(label === undefined ? {} : { label: label.text }),
      updated_at: draft.time,
    };
    next = withField(next, "condition", condition.expression);
    next = withField(next, "on_traverse", onTraverse.expression);
    draft.put(
      "connections",
      next,
      settled(
        draft,
        next,
        (label?.sure ?? true) && condition.sure && onTraverse.sure,
      ),
    );
  },
  "connection.reconnect": (draft, p) => {
    const connection = draft.get("connections", canonical(p.id));
    if (connection === undefined) {
      return;
    }
    const fromId = idOrNone(p.from_node_id) ?? connection.from_node_id;
    // The port stays with the node that it is of, unless the payload names
    // one: a node that the connection moves to without one is an element,
    // whose one port is out, since a branch's is refused.
    const port =
      p.from_port === undefined || p.from_port === ""
        ? fromId === connection.from_node_id
          ? connection.from_port
          : "out"
        : portOf(p.from_port);
    const moves =
      fromId !== connection.from_node_id || port !== connection.from_port;
    const next: Connection = {
      ...connection,
      from_node_id: fromId,
      from_port: port,
      to_node_id: idOrNone(p.to_node_id) ?? connection.to_node_id,
      order:
        p.order ??
        (moves
          ? keyAfter(ofPort(draft, fromId, port).map((c) => c.order))
          : connection.order),
      updated_at: draft.time,
    };
    draft.put(
      "connections",
      next,
      settled(draft, next, p.order !== undefined || !moves),
    );
  },
  "connection.reorder": (draft, p) => {
    const connection = draft.get("connections", canonical(p.id));
    if (connection === undefined) {
      return;
    }
    const { order, sure } =
      p.order === undefined
        ? placed(
            connection,
            ofPort(draft, connection.from_node_id, connection.from_port),
          )
        : { order: p.order, sure: true };
    if (order !== connection.order) {
      const next = { ...connection, order, updated_at: draft.time };
      draft.put("connections", next, settled(draft, next, sure));
    }
  },
  "connection.delete": (draft, p) => {
    draft.remove("connections", canonical(p.id));
  },
  // The connection in the trash is none that the document holds, and its
  // ID does not tell its board: the store reads it, and keeps it if it holds
  // the board.
  "connection.restore": (draft, p) => {
    const id = canonical(p.id);
    if (draft.get("connections", id) === undefined) {
      draft.changed(id, false);
    }
  },
};
