// What the canvas draws of a board (PRD FR-EDIT-01, FR-EDIT-11): its nodes
// and connections, as the project's document holds them, each with where it
// is on the board, in a spatial index, so that drawing the part of a board
// that the canvas shows takes the time of what it shows, not of the whole
// board. The scene follows the document: each change of it updates only the
// nodes and connections that changed, which the document's copy-on-write
// entities tell, as an entity that did not change is the same object.

import type { Connection, Node } from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import { type Curve, connectionCurve, curveBounds, nodeRect } from "./layout";
import { GridIndex } from "./spatial";
import { type Point, type Rect, union } from "./viewport";

/** A node of the scene. */
export interface NodeItem {
  node: Node;
  rect: Rect;
  /** Where the node is in the order that the canvas draws, and finds, nodes in: later is above. */
  order: number;
}

/** A connection of the scene, between two nodes that it has. */
export interface ConnectionItem {
  connection: Connection;
  curve: Curve;
}

/** What a rectangle of the board shows. */
export interface Shown {
  nodes: NodeItem[];
  connections: ConnectionItem[];
}

/** The nodes and connections of one board, as the project's document holds them. */
export class Scene {
  readonly nodes = new GridIndex<NodeItem>();
  readonly connections = new GridIndex<ConnectionItem>();
  /** The connections of each node, that leave it or enter it. */
  private readonly linked = new Map<string, Set<string>>();
  private nodesSeen: ReadonlyMap<string, Node> | undefined;
  private connectionsSeen: ReadonlyMap<string, Connection> | undefined;
  private nextOrder = 0;
  private bounds: Rect | undefined;
  private boundsStale = true;
  /** The node that each of the board's jumpers goes to, by the jumper's ID. */
  private readonly targets = new Map<string, string>();
  /** The titles of the nodes that the board's jumpers go to, by their IDs, as the scene saw them last. */
  private targetTitles: ReadonlyMap<string, string | undefined> = new Map();
  /** Counts the changes of the scene, so that what draws it tells that it changed. */
  version = 0;
  /**
   * Counts the changes of the titles of the nodes that the board's jumpers
   * go to, which their cards show, as of a node of another board, which
   * comes, or is renamed, without any node of this one changing.
   */
  targetsVersion = 0;

  constructor(readonly boardId: string) {}

  /**
   * Takes what doc holds of the board into the scene, and reports whether
   * the scene changed.
   */
  sync(doc: Pick<Doc, "nodes" | "connections">): boolean {
    if (
      doc.nodes === this.nodesSeen &&
      doc.connections === this.connectionsSeen
    ) {
      return false;
    }
    const before = this.version;
    const moved = new Set<string>();
    if (doc.nodes !== this.nodesSeen) {
      const seen = new Set<string>();
      for (const node of doc.nodes.values()) {
        if (node.board_id !== this.boardId || node.deleted_at !== undefined) {
          continue;
        }
        seen.add(node.id);
        const had = this.nodes.get(node.id);
        if (had?.node === node) {
          continue;
        }
        const item = {
          node,
          rect: nodeRect(node),
          order: had?.order ?? this.nextOrder++,
        };
        this.nodes.set(node.id, item.rect, item);
        if (node.type === "jumper" && node.target?.node_id !== undefined) {
          this.targets.set(node.id, node.target.node_id);
        } else {
          this.targets.delete(node.id);
        }
        moved.add(node.id);
        this.changed();
      }
      const gone: string[] = [];
      this.nodes.forEach((id) => {
        if (!seen.has(id)) {
          gone.push(id);
        }
      });
      for (const id of gone) {
        this.nodes.delete(id);
        this.targets.delete(id);
        moved.add(id);
        this.changed();
      }
      this.nodesSeen = doc.nodes;
      const titles = new Map<string, string | undefined>();
      for (const target of this.targets.values()) {
        titles.set(target, doc.nodes.get(target)?.title);
      }
      if (
        titles.size !== this.targetTitles.size ||
        [...titles].some(
          ([id, title]) =>
            !this.targetTitles.has(id) || this.targetTitles.get(id) !== title,
        )
      ) {
        this.targetsVersion++;
      }
      this.targetTitles = titles;
    }
    if (doc.connections !== this.connectionsSeen) {
      const seen = new Set<string>();
      for (const c of doc.connections.values()) {
        if (c.board_id !== this.boardId || c.deleted_at !== undefined) {
          continue;
        }
        seen.add(c.id);
        const had = this.connections.get(c.id);
        if (
          had?.connection === c &&
          !moved.has(c.from_node_id) &&
          !moved.has(c.to_node_id)
        ) {
          continue;
        }
        this.place(c);
      }
      const gone: string[] = [];
      this.connections.forEach((id) => {
        if (!seen.has(id)) {
          gone.push(id);
        }
      });
      // And those that waited for a node, which must not come back with it.
      for (const id of this.waiting.keys()) {
        if (!seen.has(id)) {
          gone.push(id);
        }
      }
      for (const id of gone) {
        this.unplace(id);
      }
      this.connectionsSeen = doc.connections;
    } else {
      // The connections are as they were, but those of a node that moved,
      // or went, follow it.
      for (const id of moved) {
        for (const cid of [...(this.linked.get(id) ?? [])]) {
          const item = this.connections.get(cid);
          if (item !== undefined) {
            this.place(item.connection);
          } else {
            this.placeLater(cid);
          }
        }
      }
    }
    return this.version !== before;
  }

  /** The connections that wait for one of their nodes, by ID, to be drawn once it comes. */
  private readonly waiting = new Map<string, Connection>();

  private placeLater(id: string): void {
    const c = this.waiting.get(id);
    if (c !== undefined) {
      this.place(c);
    }
  }

  /** Puts connection c into the scene, between its nodes, or keeps it until both are there. */
  private place(c: Connection): void {
    for (const end of [c.from_node_id, c.to_node_id]) {
      let set = this.linked.get(end);
      if (set === undefined) {
        set = new Set();
        this.linked.set(end, set);
      }
      set.add(c.id);
    }
    const from = this.nodes.get(c.from_node_id);
    const to = this.nodes.get(c.to_node_id);
    if (from === undefined || to === undefined) {
      if (this.connections.get(c.id) !== undefined) {
        this.connections.delete(c.id);
        this.changed();
      }
      this.waiting.set(c.id, c);
      return;
    }
    this.waiting.delete(c.id);
    const curve = connectionCurve(c, from, to);
    this.connections.set(c.id, curveBounds(curve), { connection: c, curve });
    this.changed();
  }

  /** Takes the connection of id out of the scene. */
  private unplace(id: string): void {
    const item = this.connections.get(id);
    const c = item?.connection ?? this.waiting.get(id);
    if (c !== undefined) {
      for (const end of [c.from_node_id, c.to_node_id]) {
        const set = this.linked.get(end);
        set?.delete(id);
        if (set?.size === 0) {
          this.linked.delete(end);
        }
      }
    }
    this.waiting.delete(id);
    if (item !== undefined) {
      this.connections.delete(id);
      this.changed();
    }
  }

  private changed(): void {
    this.version++;
    this.boundsStale = true;
  }

  /** Returns the rectangle of the board that holds every node, or undefined for a board without one. */
  contentBounds(): Rect | undefined {
    if (this.boundsStale) {
      const rects: Rect[] = [];
      this.nodes.forEach((_, __, rect) => {
        rects.push(rect);
      });
      this.bounds = union(rects);
      this.boundsStale = false;
    }
    return this.bounds;
  }

  /** Returns the rectangle of the board that holds the nodes of ids that the scene has. */
  boundsOf(ids: Iterable<string>): Rect | undefined {
    const rects: Rect[] = [];
    for (const id of ids) {
      const r = this.nodes.rect(id);
      if (r !== undefined) {
        rects.push(r);
      }
    }
    return union(rects);
  }

  /** Returns what r of the board shows: its nodes, in the order that they are drawn, and its connections. */
  shown(r: Rect): Shown {
    const nodes: NodeItem[] = [];
    this.nodes.query(r, (_, item) => {
      nodes.push(item);
    });
    nodes.sort((a, b) => a.order - b.order);
    const connections: ConnectionItem[] = [];
    this.connections.query(r, (_, item) => {
      connections.push(item);
    });
    return { nodes, connections };
  }

  /**
   * Returns the ID of the topmost node at p of the board, within slop units
   * of it, as a finger's tap is less precise than a mouse's click.
   */
  nodeAt(p: Point, slop = 0): string | undefined {
    let found: NodeItem | undefined;
    this.nodes.query(
      { x: p.x - slop, y: p.y - slop, width: 2 * slop, height: 2 * slop },
      (_, item) => {
        if (found === undefined || item.order > found.order) {
          found = item;
        }
      },
    );
    return found?.node.id;
  }

  /** Returns the IDs of the nodes that r of the board overlaps. */
  nodesIn(r: Rect): string[] {
    const ids: string[] = [];
    this.nodes.query(r, (id) => {
      ids.push(id);
    });
    return ids;
  }
}
