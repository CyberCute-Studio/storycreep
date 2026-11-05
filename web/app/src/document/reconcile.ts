// How a project's document follows the server (PRD §10.6: the store applies
// commands locally first, and reconciles with the server's operations by
// seq). An operation, the batch of commands that the server applied as its
// project's next seq, with the revisions that it left of every entity that it
// changed, is applied to the document through the shared appliers (apply.ts),
// and then held to its revisions: what the appliers surely changed takes the
// revision that the server gave it; what they changed that the server did
// not change is set back; and what the server changed that the appliers did
// not surely change is read from the server (toRead), as is a part whose
// entities came back without revisions of their own (reread). A document that
// holds an entity at a revision that the operation's is not past keeps it, as
// one read from the server since, which may have seen later operations.
//
// What the server is read for comes back later than the operations after it
// may: an entity, or a part's list, is merged by its revision, the higher
// winning, and the read's fields at the same one, which correct what the
// appliers were not sure of; one that the document saw go, as to the trash,
// is not brought back by an answer to a read that began before it went
// (Doc.gone).

import type {
  Attribute,
  BranchOutput,
  ComponentType,
  Node,
} from "@storycreep/shared/api";
import { type AnyCommand, applyCommands, canonical } from "./apply";
import {
  type Doc,
  Draft,
  type Entities,
  type Kind,
  nodeOfOutput,
  type Part,
  type Project,
  partOf,
  placeOf,
  typeOfAttribute,
} from "./doc";

/** A batch that the server applied, as an operation of the real-time channel or the answer to the page's own batch. */
export interface Operation {
  seq: number;
  commands: readonly AnyCommand[];
  revisions: readonly { id: string; rev: number }[];
  /** When it applied, as the server's timestamps say it, or as near as the page knows. */
  time: string;
}

/** What applying an operation leaves to do. */
export interface Reconciled {
  doc: Doc;
  /** The entities to read from the server, by their IDs. */
  toRead: Set<string>;
  /** The parts to read again. */
  reread: Set<Part>;
  /** The parts that the operation took out of the document whole, as a board's once it is in the trash. */
  dropped: ReadonlySet<Part>;
}

/** An entity of any place, with what all of them have. */
interface Revised {
  id: string;
  rev: number;
}

/** Returns the entity of id that doc holds, wherever it lives, or undefined. */
export function entityOf(doc: Doc, id: string): Revised | undefined {
  const place = placeOf(id);
  switch (place?.of) {
    case "project":
      return doc.project?.id === id ? doc.project : undefined;
    case "kind":
      return doc[place.kind].get(id);
    case "output":
      return nodeOfOutput(doc.nodes, id)?.outputs?.find((o) => o.id === id);
    case "attribute":
      return typeOfAttribute(doc.componentTypes, id)?.attributes.find(
        (a) => a.id === id,
      );
  }
  return undefined;
}

/** A document that is being written by ID, which copies each of its maps that it changes once. */
class Writer {
  private draft: Draft;

  constructor(doc: Doc, time: string) {
    this.draft = new Draft(doc, time);
  }

  /** Sets the entity of id to entity, or takes it out for undefined, without regard to what it held. */
  set(id: string, entity: Revised | undefined): void {
    const place = placeOf(id);
    switch (place?.of) {
      case "project":
        if (entity !== undefined) {
          this.draft.project = entity as Project;
        }
        return;
      case "kind":
        if (entity === undefined) {
          this.draft.remove(place.kind, id);
        } else {
          this.draft.putQuietly(place.kind, entity as Entities[Kind]);
        }
        return;
      case "output": {
        const output = entity as BranchOutput | undefined;
        const owner =
          nodeOfOutput(this.draft.all("nodes"), id) ??
          (output && this.draft.get("nodes", output.node_id));
        if (owner !== undefined) {
          this.draft.putQuietly(
            "nodes",
            withNested(owner, "outputs", id, output),
          );
        }
        return;
      }
      case "attribute": {
        const attribute = entity as Attribute | undefined;
        const owner =
          typeOfAttribute(this.draft.all("componentTypes"), id) ??
          (attribute &&
            this.draft.get("componentTypes", attribute.component_type_id));
        if (owner !== undefined) {
          this.draft.putQuietly(
            "componentTypes",
            withNested(owner, "attributes", id, attribute),
          );
        }
        return;
      }
    }
  }

  gone(id: string, rev: number, seq: number): void {
    this.draft.markGone(id, rev, seq);
  }

  back(id: string): void {
    this.draft.unmarkGone(id);
  }

  finish(seq: number): Doc {
    return this.draft.finish(seq);
  }
}

/** Returns owner with its nested entity of id under field replaced by entity, added at the end, or taken out for undefined. */
function withNested<
  O extends Node | ComponentType,
  F extends O extends Node ? "outputs" : "attributes",
>(owner: O, field: F, id: string, entity: Revised | undefined): O {
  const list = ((owner as unknown as Record<string, unknown>)[field] ??
    []) as Revised[];
  const i = list.findIndex((e) => e.id === id);
  const next = [...list];
  if (entity === undefined) {
    if (i < 0) {
      return owner;
    }
    next.splice(i, 1);
  } else if (i < 0) {
    next.push(entity);
  } else {
    next[i] = entity;
  }
  return { ...owner, [field]: next };
}

/**
 * Returns doc with the entity of id set to entity, or taken out for
 * undefined, whatever revision it held, if doc holds its part.
 */
export function withEntity(
  doc: Doc,
  id: string,
  entity: Revised | undefined,
): Doc {
  const out = new Writer(doc, "");
  out.set(id, entity);
  return out.finish(doc.seq);
}

/**
 * Reports whether doc would hold the entity of id, which it does not, were
 * it read from the server: the project's, and one of a kind of a part that
 * it holds. A node or a connection, whose board the ID does not tell, is
 * not; an applier that adds those to a board that it cannot tell has the
 * board's part read again (Draft.reread).
 */
function wouldHold(doc: Doc, id: string): boolean {
  const place = placeOf(id);
  switch (place?.of) {
    case "project":
      return doc.project?.id === id;
    case "kind":
      switch (place.kind) {
        case "folders":
          return (
            doc.parts.has("tree") ||
            doc.parts.has("variables") ||
            doc.parts.has("components")
          );
        case "boards":
          return doc.parts.has("tree");
        case "variables":
          return doc.parts.has("variables");
        case "componentTypes":
        case "components":
          return doc.parts.has("components");
      }
  }
  return false;
}

/**
 * Returns ids with those of nested entities, a branch output or an attribute
 * definition, after the others: an owner, as a node, is set whole, with its
 * nested entities as they were before or as the appliers left them, so a
 * nested entity that changed is set after it, into it, rather than its
 * revision, or its change, be set back by its owner's, whatever the order of
 * the batch's commands.
 */
function ownersFirst(ids: Iterable<string>): string[] {
  const nested = (id: string) => {
    const of = placeOf(id)?.of;
    return of === "output" || of === "attribute";
  };
  const all = [...ids];
  return [...all.filter((id) => !nested(id)), ...all.filter(nested)];
}

/**
 * Applies op to doc, which must be as of the seq before op's, and returns the
 * document as of op's seq, with what it leaves to read from the server.
 */
export function applyOperation(doc: Doc, op: Operation): Reconciled {
  const draft = new Draft(doc, op.time);
  applyCommands(draft, op.commands);
  const applied = draft.finish(op.seq);
  const revs = new Map<string, number>();
  for (const r of op.revisions) {
    revs.set(canonical(r.id), r.rev);
  }
  const out = new Writer(applied, op.time);
  const toRead = new Set<string>();
  const reread = new Set(draft.rereads);
  for (const id of ownersFirst(draft.changes.keys())) {
    const change = draft.changes.get(id) as { sure: boolean };
    const before = entityOf(doc, id);
    const rev = revs.get(id);
    if (rev === undefined || (before !== undefined && before.rev >= rev)) {
      // The server did not change it, or the document has it as of a later
      // operation already, as read since.
      out.set(id, before);
      continue;
    }
    const after = entityOf(applied, id);
    if (!change.sure) {
      toRead.add(id);
      continue;
    }
    if (after === undefined) {
      // A nested entity is taken out of its owner, which may have been set
      // back with it.
      out.set(id, undefined);
      out.gone(id, rev, op.seq);
    } else {
      out.set(id, { ...after, rev });
      out.back(id);
    }
  }
  for (const [id, rev] of revs) {
    if (draft.changes.has(id)) {
      continue;
    }
    const held = entityOf(doc, id);
    if (held !== undefined ? held.rev < rev : wouldHold(applied, id)) {
      toRead.add(id);
    }
  }
  return { doc: out.finish(op.seq), toRead, reread, dropped: draft.dropped };
}

/**
 * Applies the commands of a batch of the page's own that the server has not
 * confirmed by seq yet to doc, as the page shows it, and, once the server has
 * answered it, gives what the appliers surely changed the revisions of the
 * answer, so that the next command expects them.
 */
export function applyPending(
  doc: Doc,
  commands: readonly AnyCommand[],
  time: string,
  revisions?: readonly { id: string; rev: number }[] | null,
): Doc {
  const draft = new Draft(doc, time);
  applyCommands(draft, commands);
  const applied = draft.finish(doc.seq);
  if (revisions == null) {
    return applied;
  }
  const out = new Writer(applied, time);
  const revs = new Map<string, number>();
  for (const r of revisions) {
    revs.set(canonical(r.id), r.rev);
  }
  for (const id of ownersFirst(revs.keys())) {
    const entity = entityOf(applied, id);
    if (draft.changes.get(id)?.sure && entity !== undefined) {
      out.set(id, { ...entity, rev: revs.get(id) as number });
    }
  }
  return out.finish(doc.seq);
}

/** Returns the field of the entity of id that holds its nested entities, each with a revision of its own: a node's branch outputs, or a type's attribute definitions, or undefined for none. */
function nestedField(id: string): "outputs" | "attributes" | undefined {
  const place = placeOf(id);
  if (place?.of !== "kind") {
    return undefined;
  }
  return place.kind === "nodes"
    ? "outputs"
    : place.kind === "componentTypes"
      ? "attributes"
      : undefined;
}

/** Returns the nested entities of owner under field, or none. */
function nestedOf(owner: Revised, field: "outputs" | "attributes"): Revised[] {
  const list = (owner as unknown as Record<string, unknown>)[field];
  return Array.isArray(list) ? (list as Revised[]) : [];
}

/**
 * Reports whether the entity of id went from doc at rev or later, by an
 * operation that a read that began once doc was as of seq since began
 * before, so that the read, which found it at rev, may have found it as it
 * was before.
 */
function wentSince(doc: Doc, id: string, rev: number, since: number): boolean {
  const gone = doc.gone.get(id);
  return gone !== undefined && since < gone.seq && rev <= gone.rev;
}

/**
 * Returns doc with entity, of id, as the server answered a read of it that
 * began once doc was as of seq since, or undefined if the server answered
 * that it has none (404): the higher revision wins, and the read's fields at
 * the same one, and one that went from the document since the read began
 * stays gone, unless the server has it at a later revision, as once it came
 * back. changedSince holds the entities that operations changed while the
 * read was under way.
 */
export function mergeEntity(
  doc: Doc,
  id: string,
  entity: Revised | undefined,
  since: number,
  changedSince: ReadonlySet<string> = new Set(),
): Doc {
  const held = entityOf(doc, id);
  let next = mergeOne(doc, id, entity, since);
  // A node's branch outputs and a type's attribute definitions have
  // revisions of their own, which a change of theirs does not give their
  // owner, so each is merged by its own, as one that came back from the
  // trash, whose owner's revision is the same.
  const field = nestedField(id);
  const owner = entityOf(next, id);
  if (field === undefined || entity === undefined || owner === undefined) {
    return next;
  }
  const nested = nestedOf(entity, field);
  if (owner !== held) {
    // The owner, of a later revision, was set as the read found it, with
    // its nested entities as they were then: each that the document holds
    // is set back to what it holds, which may be later, as an operation
    // that applied while the read was under way left it, before each is
    // merged by its own revision; one that went from the document since the
    // read began stays gone; and one that the read does not have, which an
    // operation put in since, is kept.
    const was = new Map(
      (held === undefined ? [] : nestedOf(held, field)).map((c) => [c.id, c]),
    );
    const read = new Set(nested.map((c) => c.id));
    const children = [
      ...nested.flatMap((c) => {
        const kept = was.get(c.id);
        if (kept !== undefined) {
          return [kept];
        }
        return wentSince(next, c.id, c.rev, since) ? [] : [c];
      }),
      ...[...was.values()].filter(
        (c) => !read.has(c.id) && changedSince.has(c.id),
      ),
    ];
    next = withEntity(next, id, { ...owner, [field]: children } as Revised);
  }
  for (const child of nested) {
    next = mergeOne(next, child.id, child, since);
  }
  return next;
}

/** Merges entity, of id, into doc, as mergeEntity does, without its nested entities. */
function mergeOne(
  doc: Doc,
  id: string,
  entity: Revised | undefined,
  since: number,
): Doc {
  const held = entityOf(doc, id);
  const out = new Writer(doc, "");
  if (entity === undefined) {
    if (held !== undefined) {
      out.set(id, undefined);
      out.gone(id, held.rev, since);
    }
    return out.finish(doc.seq);
  }
  if (held !== undefined && held.rev > entity.rev) {
    return doc;
  }
  let merged = entity;
  if (held !== undefined && held.rev === entity.rev) {
    // The server has the same state of it at the same revision, which the
    // read answers: what the document holds otherwise, as what the appliers
    // predicted of a change that they were not sure of, as the key of order
    // that a create left to the server, takes the read's, and what the read
    // leaves out, as the description and the values that the list of the
    // components does not answer, is kept.
    merged = { ...held, ...entity };
    if (equal(merged, held)) {
      return doc;
    }
  }
  if (held === undefined && wentSince(doc, id, entity.rev, since)) {
    // It went at that revision or later, by an operation that the read
    // began before, so the read may have found it as it was before.
    return doc;
  }
  const place = placeOf(id);
  if (
    place?.of === "kind" &&
    !doc.parts.has(partOf(place.kind, entity as Entities[Kind]) as Part)
  ) {
    return doc;
  }
  out.set(id, merged);
  out.back(id);
  return out.finish(doc.seq);
}

/** Reports whether a and b are the same JSON value, whatever the order of their objects' fields. */
function equal(a: unknown, b: unknown): boolean {
  if (a === b) {
    return true;
  }
  if (
    typeof a !== "object" ||
    typeof b !== "object" ||
    a === null ||
    b === null ||
    Array.isArray(a) !== Array.isArray(b)
  ) {
    return false;
  }
  if (Array.isArray(a)) {
    const other = b as unknown[];
    return a.length === other.length && a.every((v, i) => equal(v, other[i]));
  }
  const x = a as Record<string, unknown>;
  const y = b as Record<string, unknown>;
  const keys = Object.keys(x);
  return (
    keys.length === Object.keys(y).length &&
    keys.every((k) => Object.hasOwn(y, k) && equal(x[k], y[k]))
  );
}

/**
 * Returns doc with the entities of kind that a list of part answered, read
 * once doc was as of seq since, merged as mergeEntity merges each, and
 * without those of part that it holds that the list did not, which went
 * from the part before the list was read, unless the document has them as
 * of an operation after since, as one created since.
 */
export function mergeList<K extends Kind>(
  doc: Doc,
  kind: K,
  part: Part,
  items: readonly Entities[K][],
  since: number,
  changedSince: ReadonlySet<string>,
): Doc {
  let next = doc;
  const listed = new Set<string>();
  for (const item of items) {
    listed.add(item.id);
    next = mergeEntity(next, item.id, item, since, changedSince);
  }
  const out = new Writer(next, "");
  for (const [id, entity] of next[kind]) {
    if (
      !listed.has(id) &&
      partOf(kind, entity as Entities[K]) === part &&
      !changedSince.has(id)
    ) {
      out.set(id, undefined);
    }
  }
  return out.finish(next.seq);
}
