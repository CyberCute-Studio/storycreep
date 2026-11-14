// The order of a board's script view (PRD FR-TEXT-04 AC2, plan note N75):
// breadth-first from the board's entry node, along the connections that
// leave each node, in the order of its ports, an element's one or a
// branch's outputs, and of each port's choices (FR-EDIT-03 AC1), and then
// the nodes that no connection from the entry node reaches, in the order
// that they were created. A jumper's target is not followed, as it is no
// connection: the view links it instead. The same board gives the same
// order, whatever the order that the page read its entities in.

import type { Connection, Node } from "@storycreep/shared/api";
import { choicesOfPort } from "../canvas/flow";
import { portsOf } from "../canvas/layout";

/** The nodes of a board, in the order of its script view. */
export interface ScriptOrder {
  /** The nodes that the entry node reaches, itself first, breadth-first. */
  reached: Node[];
  /** The others, in the order that they were created. */
  unreached: Node[];
}

/** Compares two IDs by their bytes, as the server orders them, of one prefix. */
function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Returns when node was created, in milliseconds, or 0 for a time that does not parse. */
function createdOf(node: Node): number {
  const at = Date.parse(node.created_at);
  return Number.isNaN(at) ? 0 : at;
}

/**
 * Returns the connections of leaving, which leave node, in the order that
 * the flow offers them: each of its ports' in turn, in their order, and
 * after them, of the ports that the node does not have, as an output in
 * the trash, in the order of their IDs, as the canvas's tree lists them.
 */
export function leavingInOrder(
  node: Node,
  leaving: readonly Connection[],
): Connection[] {
  const ports = portsOf(node);
  const out = ports.flatMap((port) => choicesOfPort(leaving, port));
  const others = [
    ...new Set(
      leaving.map((c) => c.from_port).filter((port) => !ports.includes(port)),
    ),
  ].sort(compareIds);
  for (const port of others) {
    out.push(...choicesOfPort(leaving, port));
  }
  return out;
}

/**
 * Returns the connections that leave each of nodes, of connections, by the
 * node's ID, in the order that the flow offers them (leavingInOrder), of
 * each node that one leaves.
 */
export function leavingByNode(
  nodes: readonly Node[],
  connections: Iterable<Connection>,
): Map<string, Connection[]> {
  const byNode = new Map<string, Connection[]>();
  for (const c of connections) {
    const list = byNode.get(c.from_node_id);
    if (list === undefined) {
      byNode.set(c.from_node_id, [c]);
    } else {
      list.push(c);
    }
  }
  const out = new Map<string, Connection[]>();
  for (const node of nodes) {
    const list = byNode.get(node.id);
    if (list !== undefined) {
      out.set(node.id, leavingInOrder(node, list));
    }
  }
  return out;
}

/**
 * Returns the order of the script view of a board of nodes, those of the
 * board that are not in the trash, of which leaving gives the connections
 * that leave each, in the order that the flow offers them (leavingByNode),
 * of the entry node of entryId, if it has one: the nodes that it reaches by
 * connections, breadth-first, and the rest by when they were created, and
 * of one time, by their IDs.
 */
export function scriptOrder(
  nodes: readonly Node[],
  leaving: ReadonlyMap<string, readonly Connection[]>,
  entryId: string | undefined,
): ScriptOrder {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const reached: Node[] = [];
  const seen = new Set<string>();
  const entry = entryId === undefined ? undefined : byId.get(entryId);
  if (entry !== undefined) {
    seen.add(entry.id);
    reached.push(entry);
    // The list is the queue: each node is read once, in the order reached.
    for (let i = 0; i < reached.length; i++) {
      const node = reached[i];
      for (const c of leaving.get(node.id) ?? []) {
        const next = byId.get(c.to_node_id);
        if (next !== undefined && !seen.has(next.id)) {
          seen.add(next.id);
          reached.push(next);
        }
      }
    }
  }
  const unreached = nodes
    .filter((n) => !seen.has(n.id))
    .map((n) => ({ n, at: createdOf(n) }))
    .sort((a, b) => a.at - b.at || compareIds(a.n.id, b.n.id))
    .map(({ n }) => n);
  return { reached, unreached };
}
