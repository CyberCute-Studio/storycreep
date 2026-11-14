// A project's play session (PRD FR-PLAY-01, plan item 71, note N78): the
// flow of the draft as play mode walks it, on the shared flow's Runtime, in
// strict mode, as play mode always runs (§5.8.7), against a snapshot that
// projection.ts makes of the project's document, so that no step makes a
// round trip to the server (AC1). Starting reads what the document does not
// hold yet, every board's content and every component whole, a few reads at
// a time; from then on the session keeps the snapshot that it started with,
// and says once the draft has changed since, which Restart projects anew
// (N78). A session is the page's, one for each document of a project, kept
// as long as the document is, so that the flow follows a jumper to another
// board's page and keeps its state there (AC3).

import type {
  Choice,
  Event,
  ExpressionField,
  Phase,
  Reason,
  Runtime,
  RuntimeError,
  Snapshot,
} from "@storycreep/shared/flow";
import { createStore, type StoreApi } from "zustand/vanilla";
import { boardPart, type Doc } from "../document/doc";
import type { ProjectDocument } from "../document/store";
import type { Projection } from "./projection";

// What play runs a draft with, the projection, the shared flow's loader,
// and its runtime, with the JSON Schemas that the loader checks a snapshot
// against, read once play starts, rather than with the board's page, whose
// first frame they would else make later.
const engine = () =>
  Promise.all([import("./projection"), import("@storycreep/shared/flow")]);

/** Where play starts: a node of a board, or the board's entry node where node is "". */
export interface PlayStart {
  readonly board: string;
  readonly node: string;
}

/** A runtime error that the flow reported (§5.8.7), with where it is, as the error panel shows it. */
export interface PlayError {
  readonly code: RuntimeError;
  /** The entity whose expression it is, or the jumper or the branch, or "" for a call's own. */
  readonly at: string;
  readonly expression: ExpressionField | "";
  /** The board and the node that a link opens it by, absent for a call's own. */
  readonly board?: string;
  readonly node?: string;
  /** The expression's source, as the snapshot holds it. */
  readonly source?: string;
  /** The step that reported it, from 0, the start's. */
  readonly step: number;
}

/** What the flow shows where it is at rest. */
export interface PlayFlow {
  /** The current node and its board, or "" before the flow has a node, as after a start that did not move it. */
  readonly board: string;
  readonly node: string;
  readonly phase: Phase | "";
  /** The current node's text, its values resolved, as a game shows it. */
  readonly text: string;
  /** The current element's speaker's name, or "" for none. */
  readonly speaker: string;
  /** The current node's title, or "" for none. */
  readonly title: string;
  /** Its choices, in their author's order, whose conditions held (FR-EDIT-03 AC1). */
  readonly choices: readonly Choice[];
  /** Why the flow ended, once it has (§5.8.5). */
  readonly ended: Reason | undefined;
  /** Whether strict mode halted the flow at a runtime error. */
  readonly halted: boolean;
  /** The nodes that the flow entered, in order. */
  readonly path: readonly string[];
  /** The nodes of path, and the connections that the flow took, which the canvas highlights. */
  readonly visited: ReadonlySet<string>;
  readonly traversed: ReadonlySet<string>;
  /** The runtime errors of every step, in order. */
  readonly errors: readonly PlayError[];
  /** How many steps the flow has taken: the start's, and one for each choice. */
  readonly steps: number;
}

/** A project's play session, as its store holds it. */
export type PlayState =
  | { readonly status: "idle" }
  | {
      readonly status: "loading";
      readonly start: PlayStart;
      /** How many of the reads that play waits for have been answered, of how many. */
      readonly done: number;
      readonly total: number;
    }
  | {
      readonly status: "failed";
      readonly start: PlayStart;
      readonly error: unknown;
    }
  | {
      readonly status: "playing";
      readonly start: PlayStart;
      readonly projection: Projection;
      readonly flow: PlayFlow;
      /** Whether the draft has changed since play projected it (N78). */
      readonly changed: boolean;
      /**
       * Counts the session's calls of the flow, its starts and its choices,
       * across its restarts, after each of which the board's page shows the
       * current node's board, as once the flow followed a jumper (AC3).
       */
      readonly turn: number;
    };

/** How many reads play makes at once as it starts, of boards and components, within the account's limit of reads. */
export const parallelReads = 4;

/** A project's play session, and what moves it. */
export class PlaySession {
  readonly store: StoreApi<PlayState> = createStore<PlayState>(() => ({
    status: "idle",
  }));
  private readonly doc: ProjectDocument;
  // generation counts the starts and stops, so that a start that one after
  // it replaced does nothing more once its reads are answered.
  private generation = 0;
  private runtime: Runtime | undefined;
  private snapshot: Snapshot | undefined;
  private unwatch: (() => void) | undefined;
  private turns = 0;

  constructor(doc: ProjectDocument) {
    this.doc = doc;
  }

  /** The session's state. */
  get state(): PlayState {
    return this.store.getState();
  }

  /**
   * Starts play at start, reading first what the document does not hold of
   * the project, and replaces any session under way. It resolves once the
   * flow has started, or play failed to.
   */
  async start(start: PlayStart): Promise<void> {
    const generation = this.begin();
    this.store.setState({ status: "loading", start, done: 0, total: 0 }, true);
    const current = () => generation === this.generation;
    try {
      const [[{ projectDraft }, { load, Runtime }]] = await Promise.all([
        engine(),
        this.readAll(current),
      ]);
      if (!current()) {
        return;
      }
      const view = this.doc.store.getState().view;
      const projection = projectDraft(view);
      const snapshot = await load(projection.files);
      if (!current()) {
        return;
      }
      this.snapshot = snapshot;
      this.runtime = new Runtime(snapshot, true);
      const flow = this.flowAfter(
        emptyFlow(),
        projection,
        this.runtime.start(start.board, start.node),
        undefined,
      );
      this.store.setState(
        {
          status: "playing",
          start,
          projection,
          flow,
          changed: false,
          turn: ++this.turns,
        },
        true,
      );
      this.watch(view);
    } catch (error) {
      if (current()) {
        this.store.setState({ status: "failed", start, error }, true);
      }
    }
  }

  /** Starts play again where it last started, projecting the draft anew. */
  restart(): Promise<void> {
    const s = this.state;
    return s.status === "idle" ? Promise.resolve() : this.start(s.start);
  }

  /** Takes the current node's choice of the connection id (§5.8.5). */
  choose(id: string): void {
    const s = this.state;
    if (s.status !== "playing" || this.runtime === undefined) {
      return;
    }
    const events = this.runtime.choose(id);
    this.store.setState({
      ...s,
      flow: this.flowAfter(s.flow, s.projection, events, id),
      turn: ++this.turns,
    });
  }

  /** Ends play. */
  stop(): void {
    this.begin();
    this.store.setState({ status: "idle" }, true);
  }

  /** Ends play for good, as the document goes. */
  dispose(): void {
    this.stop();
  }

  // begin ends what the session ran, and returns the next generation.
  private begin(): number {
    this.unwatch?.();
    this.unwatch = undefined;
    this.runtime = undefined;
    this.snapshot = undefined;
    return ++this.generation;
  }

  // readAll reads what the document does not hold of the project yet: the
  // tree, the variables, and the components, then every board's content
  // and every component whole, parallelReads at a time, telling the store
  // how many it has read, while current reports that the start is still
  // the session's.
  private async readAll(current: () => boolean): Promise<void> {
    const doc = this.doc;
    await Promise.all([
      doc.load("tree"),
      doc.load("variables"),
      doc.load("components"),
    ]);
    if (!current()) {
      return;
    }
    const view = doc.store.getState().view;
    const reads: (() => Promise<void>)[] = [];
    for (const b of view.boards.values()) {
      if (b.deleted_at === undefined && !doc.hasRead(boardPart(b.id))) {
        reads.push(() => doc.load(boardPart(b.id)));
      }
    }
    for (const c of view.components.values()) {
      if (c.deleted_at === undefined && c.attributes === undefined) {
        reads.push(() => doc.readComponent(c.id));
      }
    }
    let done = 0;
    const progress = () => {
      const s = this.state;
      if (current() && s.status === "loading") {
        this.store.setState({ ...s, done, total: reads.length });
      }
    };
    progress();
    let next = 0;
    const worker = async () => {
      while (next < reads.length && current()) {
        const read = reads[next++];
        await read();
        done++;
        progress();
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(parallelReads, reads.length) }, worker),
    );
    // A component that a read of the components' list took the values of
    // since, as it answers them without them, is read whole again.
    if (current()) {
      const again = [...doc.store.getState().view.components.values()].filter(
        (c) => c.deleted_at === undefined && c.attributes === undefined,
      );
      await Promise.all(again.map((c) => doc.readComponent(c.id)));
    }
  }

  // watch marks the session changed once the draft that it projected, of
  // view, changes, as the page's own batch or another session's operation
  // changes what the projection reads.
  private watch(projected: Doc): void {
    this.unwatch = this.doc.store.subscribe((state) => {
      const s = this.state;
      if (s.status !== "playing" || s.changed) {
        this.unwatch?.();
        this.unwatch = undefined;
        return;
      }
      if (draftChanged(projected, state.view)) {
        this.store.setState({ ...s, changed: true });
        this.unwatch?.();
        this.unwatch = undefined;
      }
    });
  }

  // flowAfter returns flow after the events of a call of the runtime, the
  // start's, or a choice of the connection chosen.
  private flowAfter(
    flow: PlayFlow,
    projection: Projection,
    events: readonly Event[],
    chosen: string | undefined,
  ): PlayFlow {
    const r = this.runtime;
    const s = this.snapshot;
    if (r === undefined || s === undefined) {
      return flow;
    }
    const step = chosen === undefined ? 0 : flow.steps;
    const path = [...flow.path];
    const visited = new Set(flow.visited);
    const traversed = new Set(flow.traversed);
    const errors = [...flow.errors];
    let ended: Reason | undefined;
    // The node that the flow left last, whose connection to the node that
    // it enters next it took: the one chosen, or, from a branch, which goes
    // on by itself, the first of its connections that leads there.
    let left: string | undefined;
    let pending = chosen;
    for (const e of events) {
      switch (e.kind) {
        case "exit":
          left = e.node;
          break;
        case "enter": {
          if (pending !== undefined) {
            traversed.add(pending);
            pending = undefined;
          } else if (left !== undefined) {
            const c = connectionTo(s, left, e.node);
            if (c !== undefined) {
              traversed.add(c);
            }
          }
          left = undefined;
          path.push(e.node);
          visited.add(e.node);
          break;
        }
        case "end":
          ended = e.reason;
          break;
        case "error":
          errors.push({
            ...placeOf(projection, s, e.at, e.expression),
            code: e.code,
            at: e.at,
            expression: e.expression,
            step,
          });
          break;
        case "ready":
        case "signal":
          break;
      }
    }
    const { node, phase } = r.current();
    const board = node === "" ? "" : (s.nodes.get(node)?.board ?? "");
    const file = projection.boards.get(board)?.nodes.find((n) => n.id === node);
    return {
      board,
      node,
      phase,
      text: node === "" ? "" : r.text(),
      speaker:
        file?.type === "element" && file.speaker !== undefined
          ? (projection.componentNames.get(file.speaker) ?? "")
          : "",
      title: file?.title ?? "",
      choices: r.choices(),
      ended: phase === "ended" ? (ended ?? flow.ended) : undefined,
      halted: r.halted(),
      path,
      visited,
      traversed,
      errors,
      steps: flow.steps + 1,
    };
  }
}

// emptyFlow returns the flow before play starts.
function emptyFlow(): PlayFlow {
  return {
    board: "",
    node: "",
    phase: "",
    text: "",
    speaker: "",
    title: "",
    choices: [],
    ended: undefined,
    halted: false,
    path: [],
    visited: new Set(),
    traversed: new Set(),
    errors: [],
    steps: 0,
  };
}

// connectionTo returns the connection that leaves from, a node of s, for
// to, directly or through a jumper whose target to is, the first in the
// order of from's ports and of their connections, or undefined for none.
function connectionTo(
  s: Snapshot,
  from: string,
  to: string,
): string | undefined {
  const n = s.nodes.get(from);
  if (n === undefined) {
    return undefined;
  }
  for (const connections of n.ports.values()) {
    for (const c of connections) {
      const dest = s.nodes.get(c.to);
      const resolved = dest?.type === "jumper" ? dest.target?.nodeId : dest?.id;
      if (resolved === to) {
        return c.id;
      }
    }
  }
  return undefined;
}

// placeOf returns where the entity at of a runtime error is, as a link opens
// it, and the source of its expression of field, as the snapshot holds
// them: a node's, a connection's by the node that it leaves, and a branch
// output's by its branch.
function placeOf(
  projection: Projection,
  s: Snapshot,
  at: string,
  field: ExpressionField | "",
): { board?: string; node?: string; source?: string } {
  if (at === "") {
    return {};
  }
  const node = s.nodes.get(at);
  if (node !== undefined) {
    const file = projection.boards
      .get(node.board)
      ?.nodes.find((n) => n.id === at);
    const expr =
      file !== undefined && field !== "" && field in file
        ? (file as unknown as Record<string, { source: string } | undefined>)[
            field
          ]
        : undefined;
    return {
      board: node.board,
      node: at,
      ...(expr === undefined ? {} : { source: expr.source }),
    };
  }
  for (const [board, f] of projection.boards) {
    const c = f.connections.find((x) => x.id === at);
    if (c !== undefined) {
      const expr =
        field === "condition"
          ? c.condition
          : field === "onTraverse"
            ? c.onTraverse
            : undefined;
      return {
        board,
        node: c.from,
        ...(expr === undefined ? {} : { source: expr.source }),
      };
    }
    for (const n of f.nodes) {
      if (n.type === "branch") {
        const o = n.outputs.find((x) => x.id === at);
        if (o !== undefined) {
          return {
            board,
            node: n.id,
            ...(o.kind === "else" ? {} : { source: o.condition.source }),
          };
        }
      }
    }
  }
  return {};
}

// The fields of an entity that the projection does not read, which a
// change of alone changes nothing that play shows.
const unread = new Set(["rev", "updated_at", "created_at"]);

// The kinds of the project's content that the projection reads.
const projected = [
  "boards",
  "nodes",
  "connections",
  "variables",
  "componentTypes",
  "components",
] as const;

// draftChanged reports whether after, the document as the page shows it
// now, holds otherwise than before what the projection reads: an entity of
// the project's content, in the trash or not, added, gone, or changed but
// for what the projection does not read, as a board's or a component's
// description, or a component's values where a read of the components'
// list left them out.
export function draftChanged(before: Doc, after: Doc): boolean {
  if (before === after) {
    return false;
  }
  if (!sameEntity(before.project, after.project, "project")) {
    return true;
  }
  for (const kind of projected) {
    const a = before[kind] as ReadonlyMap<string, unknown>;
    const b = after[kind] as ReadonlyMap<string, unknown>;
    if (a === b) {
      continue;
    }
    for (const [id, x] of a) {
      if (!sameEntity(x, b.get(id), kind)) {
        return true;
      }
    }
    for (const id of b.keys()) {
      if (!a.has(id)) {
        return true;
      }
    }
  }
  return false;
}

// sameEntity reports whether a and b, entities of kind, are the same as the
// projection reads them.
function sameEntity(
  a: unknown,
  b: unknown,
  kind: (typeof projected)[number] | "project",
): boolean {
  if (a === b) {
    return true;
  }
  if (a === undefined || b === undefined) {
    return false;
  }
  const x = a as Record<string, unknown>;
  const y = b as Record<string, unknown>;
  const ignored = (k: string) =>
    unread.has(k) ||
    (k === "description" && kind !== "variables") ||
    (kind === "components" &&
      k === "attributes" &&
      (x.attributes === undefined || y.attributes === undefined));
  return stable(x, ignored) === stable(y, ignored);
}

// stable returns the JSON of v, its keys sorted, without the top-level keys
// that ignored tells.
function stable(
  v: Record<string, unknown>,
  ignored: (k: string) => boolean,
): string {
  const sorted = (u: unknown): unknown => {
    if (Array.isArray(u)) {
      return u.map(sorted);
    }
    if (typeof u === "object" && u !== null) {
      const o = u as Record<string, unknown>;
      return Object.fromEntries(
        Object.keys(o)
          .sort()
          .map((k) => [k, sorted(o[k])]),
      );
    }
    return u;
  };
  const top: Record<string, unknown> = {};
  for (const k of Object.keys(v).sort()) {
    if (!ignored(k)) {
      top[k] = sorted(v[k]);
    }
  }
  return JSON.stringify(top);
}

const sessions = new WeakMap<ProjectDocument, PlaySession>();

/** Returns the play session of doc's project, made once for the document. */
export function playSessionOf(doc: ProjectDocument): PlaySession {
  let s = sessions.get(doc);
  if (s === undefined) {
    s = new PlaySession(doc);
    sessions.set(doc, s);
  }
  return s;
}
