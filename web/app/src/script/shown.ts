// Where a board's script view shows its entries (PRD FR-TEXT-04, plan note
// N75): each node's at its place in the order of order.ts, and its choices
// in the order of its ports; but the entry of a node whose form is open, or
// whose choice's is, in the part of the view where it showed it last, and
// such a choice in the entry where it showed it, whatever moved them
// meanwhile, as another session's deletion of the connections that reach
// the node, which moves it to the other part, or a node added after its
// own, which reconnects the choice to the new node. React keeps a form,
// and what the user typed in it, only while its entry stays in its own
// list, and its choice in its node's: one moved to another list would be a
// new form, of what the board has. A node or a choice that the board no
// longer has, as once another session deleted it, is shown, as a ghost,
// where the view showed it last, but for one that an addition under way
// created, which goes without a ghost once its batch is refused, as the
// page shows the board without it before it says so.

import type { Connection, Node } from "@storycreep/shared/api";
import type { ScriptOrder } from "./order";

/** The two parts of the view: the nodes that the entry node reaches, and the others. */
export type Section = "reached" | "unreached";

const sectionNames: readonly Section[] = ["reached", "unreached"];

/** Where the view shows an entry: its part, and its place in it. */
export interface Place {
  section: Section;
  index: number;
}

/** A node as the view showed it, and where. */
export interface ShownNode {
  node: Node;
  place: Place;
}

/** A choice as the view showed it: the connection, the node whose entry it was in, and its place among the node's. */
export interface ShownChoice {
  connection: Connection;
  nodeId: string;
  index: number;
}

/** What the view showed of its open forms, which it places them by as it shows them anew. */
export interface Shown {
  /** The node of each entry of an open form, of the node or of a choice of it. */
  nodes: ReadonlyMap<string, ShownNode>;
  /** The choice of each open form of a label. */
  choices: ReadonlyMap<string, ShownChoice>;
}

/** What a view that showed no open form showed of them. */
export const nothingShown: Shown = { nodes: new Map(), choices: new Map() };

/** What the view shows. */
export interface Placed {
  /** The nodes of each part, in the order that their entries are shown. */
  sections: Readonly<Record<Section, readonly Node[]>>;
  /** The connections that leave each node, in the order that its entry shows them. */
  leaving: ReadonlyMap<string, readonly Connection[]>;
  /** The IDs of the nodes and the connections that the board no longer has, which the view shows as ghosts. */
  ghosts: ReadonlySet<string>;
  /** What the view shows of its open forms, which it is placed by as it is shown anew. */
  shown: Shown;
}

const noGhosts: ReadonlySet<string> = new Set();

/**
 * Returns what the view shows of a board of order, whose nodes leaving
 * gives the connections that leave each, in their order, of connections,
 * the project's, with the forms of open, by the ID of the node or the
 * connection that each edits, open, of which last says what the view
 * showed last, and of which those that an addition under way created,
 * creating, show nothing once the board has them no longer.
 */
export function placeShown(
  order: ScriptOrder,
  leaving: ReadonlyMap<string, readonly Connection[]>,
  connections: ReadonlyMap<string, Connection>,
  open: ReadonlyMap<string, { kind: "node" | "choice" }>,
  last: Shown,
  creating: ReadonlySet<string>,
): Placed {
  if (open.size === 0) {
    return { sections: order, leaving, ghosts: noGhosts, shown: nothingShown };
  }
  const onBoard = new Map<string, ShownNode>();
  for (const section of sectionNames) {
    order[section].forEach((node, index) => {
      onBoard.set(node.id, { node, place: { section, index } });
    });
  }
  // The connection of id as the board has it, of a node of the board's.
  const connectionOf = (id: string): Connection | undefined => {
    const c = connections.get(id);
    return c !== undefined &&
      leaving.get(c.from_node_id)?.some((l) => l.id === id)
      ? c
      : undefined;
  };

  // The entries that stay where the view showed them, rather than where
  // the order puts them, and the choices likewise, by the node whose entry
  // they stay in.
  const ghosts = new Set<string>();
  const kept = new Map<string, ShownNode>();
  const keptChoices = new Map<string, ShownChoice>();
  // Reports whether the entry of the node of id is shown, and keeps it
  // where the view showed it, where the order moved it to the other part,
  // or the board no longer has it.
  const keep = (id: string): boolean => {
    if (kept.has(id)) {
      return true;
    }
    const was = last.nodes.get(id);
    const now = onBoard.get(id);
    if (now !== undefined) {
      if (was !== undefined && was.place.section !== now.place.section) {
        kept.set(id, { node: now.node, place: was.place });
      }
      return true;
    }
    if (was === undefined || creating.has(id)) {
      return false;
    }
    kept.set(id, was);
    ghosts.add(id);
    return true;
  };
  for (const [id, e] of open) {
    if (e.kind === "node") {
      keep(id);
      continue;
    }
    const now = connectionOf(id);
    const was = last.choices.get(id);
    if (was === undefined) {
      // A choice that the view has not shown yet is where the board has it.
      continue;
    }
    if (now?.from_node_id === was.nodeId) {
      keep(was.nodeId);
      continue;
    }
    if ((now === undefined && creating.has(id)) || !keep(was.nodeId)) {
      continue;
    }
    if (now === undefined) {
      ghosts.add(id);
    }
    keptChoices.set(id, { ...was, connection: now ?? was.connection });
  }

  // The parts, without the entries kept elsewhere, which then go back in,
  // each at its place, in the order of their places, so that two of one
  // part are as they were among themselves.
  let sections: Record<Section, readonly Node[]> = order;
  if (kept.size > 0) {
    const moved: Record<Section, Node[]> = {
      reached: order.reached.filter((n) => !kept.has(n.id)),
      unreached: order.unreached.filter((n) => !kept.has(n.id)),
    };
    for (const { node, place } of byIndex(
      [...kept.values()],
      (k) => k.place.index,
    )) {
      const list = moved[place.section];
      list.splice(Math.min(place.index, list.length), 0, node);
    }
    sections = moved;
  }
  let shownLeaving = leaving;
  if (keptChoices.size > 0) {
    const lists = new Map(leaving);
    for (const [id] of keptChoices) {
      const from = connectionOf(id)?.from_node_id;
      const list = from === undefined ? undefined : lists.get(from);
      if (from !== undefined && list !== undefined) {
        lists.set(
          from,
          list.filter((c) => c.id !== id),
        );
      }
    }
    for (const choice of byIndex([...keptChoices.values()], (c) => c.index)) {
      const list = [...(lists.get(choice.nodeId) ?? [])];
      list.splice(Math.min(choice.index, list.length), 0, choice.connection);
      lists.set(choice.nodeId, list);
    }
    shownLeaving = lists;
  }

  // What the view shows now of its open forms, which it is placed by as it
  // is shown anew.
  const shownAt = new Map<string, ShownNode>();
  for (const section of sectionNames) {
    sections[section].forEach((node, index) => {
      shownAt.set(node.id, { node, place: { section, index } });
    });
  }
  const nodes = new Map<string, ShownNode>();
  const choices = new Map<string, ShownChoice>();
  for (const [id, e] of open) {
    if (e.kind === "node") {
      const at = shownAt.get(id);
      if (at !== undefined) {
        nodes.set(id, at);
      }
      continue;
    }
    const nodeId =
      keptChoices.get(id)?.nodeId ?? connectionOf(id)?.from_node_id;
    const at = nodeId === undefined ? undefined : shownAt.get(nodeId);
    const list = nodeId === undefined ? undefined : shownLeaving.get(nodeId);
    const index = list?.findIndex((c) => c.id === id) ?? -1;
    if (
      nodeId !== undefined &&
      at !== undefined &&
      list !== undefined &&
      index >= 0
    ) {
      choices.set(id, { connection: list[index], nodeId, index });
      nodes.set(nodeId, at);
    }
  }
  return {
    sections,
    leaving: shownLeaving,
    ghosts,
    shown: { nodes, choices },
  };
}

/** Returns items in the order of their places, of place. */
function byIndex<T>(items: T[], place: (item: T) => number): T[] {
  return items.sort((a, b) => place(a) - place(b));
}
