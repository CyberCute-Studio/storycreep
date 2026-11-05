// The document of a project that a page holds (PRD §10.6): the entities that
// it has read of the project, by their IDs, each as the API answers it, and
// the seq of the latest operation that they are as of. A document changes
// only through commands, applied to it by the shared layer of apply.ts, as
// the server applies them (§10.4), whether a batch is the page's own, whose
// commands it applies at once, before the server answers (optimistic), or
// another session's, which the real-time channel brings, or, later, one
// that the page queues offline (FR-OFF-03 AC2).
//
// It holds what the pages read, a part at a time (Part): the tree of the
// project's boards, its variables, its components, and each board's nodes
// and connections that a page has opened. An entity of a part that it has
// not read is not in it, and a command's change of one is left out, as the
// lists that the part is read from will hold it once they are read.

import type {
  Board,
  BranchOutput,
  Component,
  Summary as ComponentSummary,
  ComponentType,
  Connection,
  Folder,
  Node,
  ProjectBody,
  Variable,
} from "@storycreep/shared/api";

/**
 * The project, as the document holds it: what the project is, without what
 * its reader is to it (role), nor its seq, which is the document's.
 */
export type Project = Omit<ProjectBody, "role" | "seq">;

/**
 * A component, as the document holds it: as the list of the project's
 * components answers it, without its description and its attributes' values,
 * which the component's own read has too, once it is read (getComponent).
 */
export type ComponentEntry = ComponentSummary & Partial<Component>;

/** The kinds of entity that the document holds by their IDs, each in a map of its own. */
export interface Entities {
  folders: Folder;
  boards: Board;
  nodes: Node;
  connections: Connection;
  variables: Variable;
  componentTypes: ComponentType;
  components: ComponentEntry;
}

export type Kind = keyof Entities;

/** Every kind, in the order that the document lists them. */
export const kinds: readonly Kind[] = [
  "folders",
  "boards",
  "nodes",
  "connections",
  "variables",
  "componentTypes",
  "components",
];

/**
 * A part of the project that the document reads and keeps current: the tree
 * of its boards (its folders of boards and its boards), its variables (with
 * their folders), its components (with their types and folders), or a
 * board's content, its nodes and its connections.
 */
export type Part = "tree" | "variables" | "components" | `board:${string}`;

/** Returns the part of a board's content. */
export function boardPart(boardId: string): Part {
  return `board:${boardId}`;
}

/** What the document holds of an entity that went from it. */
export interface Gone {
  /** The revision that it went at. */
  rev: number;
  /** The seq of the operation that took it, or that the read that found it gone began at. */
  seq: number;
}

export interface Doc {
  /** The seq of the project's latest operation that the document is as of. */
  readonly seq: number;
  /** The project, once read. */
  readonly project: Project | undefined;
  readonly folders: ReadonlyMap<string, Folder>;
  readonly boards: ReadonlyMap<string, Board>;
  readonly nodes: ReadonlyMap<string, Node>;
  readonly connections: ReadonlyMap<string, Connection>;
  readonly variables: ReadonlyMap<string, Variable>;
  readonly componentTypes: ReadonlyMap<string, ComponentType>;
  readonly components: ReadonlyMap<string, ComponentEntry>;
  /**
   * The parts that the document holds, which it has read or is reading: a
   * command's change of an entity of a part that it does not hold is left
   * out.
   */
  readonly parts: ReadonlySet<Part>;
  /**
   * The entities that went from the document, as to the trash, each with the
   * revision that it went at and the seq of the operation that took it, so
   * that an answer to a read that began before it went, which comes after, as
   * a list of a part read meanwhile, does not bring it back at that revision.
   */
  readonly gone: ReadonlyMap<string, Gone>;
}

/** Returns a document that holds nothing yet, as of seq. */
export function emptyDoc(seq = 0): Doc {
  return {
    seq,
    project: undefined,
    folders: new Map(),
    boards: new Map(),
    nodes: new Map(),
    connections: new Map(),
    variables: new Map(),
    componentTypes: new Map(),
    components: new Map(),
    parts: new Set(),
    gone: new Map(),
  };
}

/**
 * Where an entity lives in the document, by its ID's prefix: a kind's map,
 * the project, a branch output in its node's outputs, or an attribute
 * definition in its type's attributes. An ID of anything else, as a
 * workspace's, is none of the document's.
 */
export type Place =
  | { of: "project" }
  | { of: "kind"; kind: Kind }
  | { of: "output" }
  | { of: "attribute" };

const placesByPrefix: Readonly<Record<string, Place>> = {
  prj: { of: "project" },
  fld: { of: "kind", kind: "folders" },
  bd: { of: "kind", kind: "boards" },
  nd: { of: "kind", kind: "nodes" },
  cn: { of: "kind", kind: "connections" },
  var: { of: "kind", kind: "variables" },
  ct: { of: "kind", kind: "componentTypes" },
  cmp: { of: "kind", kind: "components" },
  bo: { of: "output" },
  atr: { of: "attribute" },
};

/** Returns where the entity of id lives in the document, or undefined for none. */
export function placeOf(id: string): Place | undefined {
  const prefix = id.slice(0, id.indexOf("_"));
  return Object.hasOwn(placesByPrefix, prefix)
    ? placesByPrefix[prefix]
    : undefined;
}

/** Returns the part that holds an entity of kind, as it is. */
export function partOf<K extends Kind>(
  kind: K,
  entity: Entities[K],
): Part | undefined {
  switch (kind) {
    case "folders": {
      const folder = entity as Folder;
      return folder.kind === "board"
        ? "tree"
        : folder.kind === "variable"
          ? "variables"
          : "components";
    }
    case "boards":
      return "tree";
    case "nodes":
      return boardPart((entity as Node).board_id);
    case "connections":
      return boardPart((entity as Connection).board_id);
    case "variables":
      return "variables";
    case "componentTypes":
    case "components":
      return "components";
  }
  return undefined;
}

/** Returns the node of doc whose outputs hold the branch output of id, or undefined. */
export function nodeOfOutput(
  nodes: ReadonlyMap<string, Node>,
  id: string,
): Node | undefined {
  for (const node of nodes.values()) {
    if (node.outputs?.some((output) => output.id === id)) {
      return node;
    }
  }
  return undefined;
}

/** Returns the component type of doc whose attributes hold the attribute definition of id, or undefined. */
export function typeOfAttribute(
  types: ReadonlyMap<string, ComponentType>,
  id: string,
): ComponentType | undefined {
  for (const type of types.values()) {
    if (type.attributes.some((attribute) => attribute.id === id)) {
      return type;
    }
  }
  return undefined;
}

/** Returns the revision that doc holds of the entity of id, or undefined if it holds none. */
export function revOf(doc: Doc, id: string): number | undefined {
  const place = placeOf(id);
  switch (place?.of) {
    case "project":
      return doc.project?.id === id ? doc.project.rev : undefined;
    case "kind":
      return doc[place.kind].get(id)?.rev;
    case "output":
      return nodeOfOutput(doc.nodes, id)?.outputs?.find((o) => o.id === id)
        ?.rev;
    case "attribute":
      return typeOfAttribute(doc.componentTypes, id)?.attributes.find(
        (a) => a.id === id,
      )?.rev;
  }
  return undefined;
}

/** What a Draft records of an entity that a command changed. */
export interface Change {
  /**
   * Whether the change is the one that the server makes, as a rename's,
   * rather than one that the draft cannot know, as where the server picks a
   * value, or a part that it does not hold changes with it: the store reads
   * such an entity from the server once its operation has applied.
   */
  sure: boolean;
}

/**
 * A document being changed by a batch's commands, which copies each map of
 * the document that it changes once, so that what it leaves unchanged is
 * shared with the document that it started from. It records each entity
 * that it changed (changes), and what it went from, so that the store can
 * tell what a batch changed, and set back what it changed that the server
 * did not.
 */
export class Draft {
  readonly base: Doc;
  /** When the batch applied, as the server's timestamps say it. */
  readonly time: string;
  project: Project | undefined;
  private readonly maps = new Map<Kind, Map<string, Entities[Kind]>>();
  private gone: Map<string, Gone> | undefined;
  private parts: Set<Part> | undefined;
  /** The entities that the batch changed, by ID: the project, one of a kind's map, a branch output, or an attribute definition. */
  readonly changes = new Map<string, Change>();
  /** The parts that a command takes out of the document whole, as a board's once it is in the trash. */
  readonly dropped = new Set<Part>();
  /**
   * The parts that the store reads again once the batch has applied, as a
   * board's whose node came back from the trash with connections that went
   * with it, which come back without a revision of their own.
   */
  readonly rereads = new Set<Part>();

  constructor(base: Doc, time: string) {
    this.base = base;
    this.time = time;
    this.project = base.project;
  }

  /** Returns the entity of kind and id, as the batch has left it so far. */
  get<K extends Kind>(kind: K, id: string): Entities[K] | undefined {
    return (this.maps.get(kind) ?? this.base[kind]).get(id) as
      | Entities[K]
      | undefined;
  }

  /** Returns the entities of kind, as the batch has left them so far. */
  all<K extends Kind>(kind: K): ReadonlyMap<string, Entities[K]> {
    return (this.maps.get(kind) ?? this.base[kind]) as ReadonlyMap<
      string,
      Entities[K]
    >;
  }

  /** Reports whether the document holds part, so that a change of its entities is kept. */
  holds(part: Part | undefined): boolean {
    return part !== undefined && (this.parts ?? this.base.parts).has(part);
  }

  private writable<K extends Kind>(kind: K): Map<string, Entities[K]> {
    let map = this.maps.get(kind);
    if (map === undefined) {
      map = new Map(this.base[kind] as ReadonlyMap<string, Entities[Kind]>);
      this.maps.set(kind, map);
    }
    return map as Map<string, Entities[K]>;
  }

  /** Records that the batch changed the entity of id, surely or not. */
  changed(id: string, sure = true): void {
    const before = this.changes.get(id);
    this.changes.set(id, { sure: sure && (before?.sure ?? true) });
  }

  /**
   * Puts entity, of kind, in the document, as the batch changed it, if the
   * document holds its part, and records the change. It returns whether it
   * put it.
   */
  put<K extends Kind>(kind: K, entity: Entities[K], sure = true): boolean {
    if (!this.holds(partOf(kind, entity))) {
      return false;
    }
    this.writable(kind).set(entity.id, entity);
    this.changed(entity.id, sure);
    return true;
  }

  /**
   * Puts entity, of kind, in the document as put does, without recording a
   * change of it: for an entity whose nested entity changed, as a node whose
   * branch output did, which records that entity's change itself (changed),
   * since the server changes the nested entity's revision, not its owner's.
   */
  putQuietly<K extends Kind>(kind: K, entity: Entities[K]): boolean {
    if (!this.holds(partOf(kind, entity))) {
      return false;
    }
    this.writable(kind).set(entity.id, entity);
    return true;
  }

  /**
   * Takes the entity of kind and id out of the document as remove does,
   * without recording a change of it: for an entity that goes with another,
   * without a revision of its own, as a connection with its node to the
   * trash, which the store would otherwise set back, as one that the server
   * did not change.
   */
  removeQuietly<K extends Kind>(kind: K, id: string): void {
    if (this.get(kind, id) !== undefined) {
      this.writable(kind).delete(id);
    }
  }

  /** Has the store read part again once the batch has applied, if the document holds it. */
  reread(part: Part): void {
    if (this.holds(part)) {
      this.rereads.add(part);
    }
  }

  /**
   * Takes the entity of kind and id out of the document, as to the trash,
   * and records the change, if the document holds it. It returns the entity
   * that it took out.
   */
  remove<K extends Kind>(
    kind: K,
    id: string,
    sure = true,
  ): Entities[K] | undefined {
    const entity = this.get(kind, id);
    if (entity === undefined) {
      return undefined;
    }
    this.writable(kind).delete(id);
    this.changed(id, sure);
    return entity;
  }

  /** Sets the project, as the batch changed it, and records the change. */
  setProject(project: Project, sure = true): void {
    if (this.project === undefined) {
      return;
    }
    this.project = project;
    this.changed(project.id, sure);
  }

  /** Takes part out of the document, with the entities of it, as a board's content once the board is in the trash. */
  drop(part: Part): void {
    if (!this.holds(part)) {
      return;
    }
    this.parts ??= new Set(this.base.parts);
    this.parts.delete(part);
    this.dropped.add(part);
    for (const kind of ["nodes", "connections"] as const) {
      for (const [id, entity] of this.all(kind)) {
        if (partOf(kind, entity) === part) {
          this.writable(kind).delete(id);
        }
      }
    }
  }

  /** Records that the entity of id went from the document at rev, by the operation of seq, as the server answered. */
  markGone(id: string, rev: number, seq: number): void {
    this.gone ??= new Map(this.base.gone);
    const before = this.gone.get(id);
    this.gone.set(id, {
      rev: Math.max(rev, before?.rev ?? 0),
      seq: Math.max(seq, before?.seq ?? 0),
    });
  }

  /** Forgets that the entity of id went, as once it has come back. */
  unmarkGone(id: string): void {
    if ((this.gone ?? this.base.gone).has(id)) {
      this.gone ??= new Map(this.base.gone);
      this.gone.delete(id);
    }
  }

  /** Returns the document that the batch leaves, as of seq. */
  finish(seq: number): Doc {
    const doc: { -readonly [K in keyof Doc]: Doc[K] } = { ...this.base, seq };
    for (const [kind, map] of this.maps) {
      (doc as Record<Kind, ReadonlyMap<string, unknown>>)[kind] = map;
    }
    doc.project = this.project;
    doc.parts = this.parts ?? this.base.parts;
    doc.gone = this.gone ?? this.base.gone;
    return doc;
  }
}

/** Returns node with its branch output of id replaced by what change returns of it, or node if it has none. */
export function withOutput(
  node: Node,
  id: string,
  change: (output: BranchOutput) => BranchOutput | undefined,
): Node {
  const outputs = node.outputs ?? [];
  const i = outputs.findIndex((output) => output.id === id);
  if (i < 0) {
    return node;
  }
  const changed = change(outputs[i]);
  const next = [...outputs];
  if (changed === undefined) {
    next.splice(i, 1);
  } else {
    next[i] = changed;
  }
  return { ...node, outputs: next };
}
