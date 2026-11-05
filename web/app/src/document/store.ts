// A project's document as a page holds it (PRD §10.6): a Zustand store of
// what the server has confirmed of the project, as of a seq, and of what the
// page shows, which is that with the page's own batches applied on top,
// before the server answers them (optimistic). The document is fed by the
// command pipeline, through the page's batches' answers, and by the
// real-time channel, whose operations it applies in the order of their seqs,
// from the seq that it has, so that a socket that was lost misses none
// (FR-RT-01 AC3), and it reads from the API what an operation's appliers
// could not know (reconcile.ts). What the page holds besides the document,
// as a selection, a viewport, or a field being edited, is no part of it, so
// an operation that applies leaves it as it is (FR-RT-02 AC3).
//
// The page's batches are sent one at a time, in the order that they were
// made, each expecting the revisions that the page showed, moved to those
// that the answers to the batches before it gave, where it was made on top
// of them. A batch refused for a revision that changed elsewhere since is
// sent again, expecting the current revision, where what changed elsewhere
// is none of what the batch changes, field by field; where it is, the
// refusal names each field with both values (ConflictError), and nothing is
// changed over (§7.10 item 1). The answer to a batch holds its undo, which
// the history keeps (undo.ts), and which undo and redo send back.

import {
  ApiError,
  type CommandBody,
  type CommandResultBody,
  type UndoCommandBody,
} from "@storycreep/shared/api";
import { formatUuid, newUuid } from "@storycreep/shared/ids";
import type { OperationMessage } from "@storycreep/shared/realtime";
import type { QueryClient } from "@tanstack/react-query";
import { createStore, type StoreApi } from "zustand/vanilla";
import { allAtOnce, api, everyPage, sessionQuery } from "../api";
import { csrfHeaders } from "../auth/session";
import type { ErrorLog } from "../errors/log";
import { projectQuery } from "../project/queries";
import { applyCommands, canonical } from "./apply";
import {
  type Doc,
  Draft,
  type Entities,
  emptyDoc,
  type Kind,
  nodeOfOutput,
  type Part,
  type Project,
  partOf,
  placeOf,
  typeOfAttribute,
} from "./doc";
import type { Realtime } from "./realtime";
import {
  applyOperation,
  applyPending,
  entityOf,
  mergeEntity,
  mergeList,
  type Operation,
  withEntity,
} from "./reconcile";
import {
  type Captured,
  History,
  historyKey,
  joinedUndo,
  type Step,
  withRevisions,
} from "./undo";

/** A command of a batch, without the ID that the document gives it. */
export type Command = DistributiveOmit<CommandBody, "command_id">;

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown
  ? Omit<T, K>
  : never;

/** What a page reads of a project's document. */
export interface DocumentState {
  /** The document as the page shows it: what the server confirmed, with the page's own batches on top. */
  view: Doc;
  /** Whether the real-time channel brings the project's operations as they commit. */
  live: boolean;
  /** Whether the project can no longer be read, as once it went to the trash. */
  gone: boolean;
  /** How many of the page's batches are queued or under way. */
  sending: number;
  /** How many steps can be undone and redone. */
  undoable: number;
  redoable: number;
  /** Whether an undo or a redo is under way. */
  stepping: boolean;
}

/** An entity that a conflict found changed elsewhere, with the fields that both changed. */
export interface Overlap {
  id: string;
  /** The entity as it is now, as the server answered. */
  state: Record<string, unknown>;
  /** Each field that the batch changes and that changed elsewhere since, with the value of each. */
  fields: { field: string; mine: unknown; theirs: unknown }[];
  /** Whether the entity is gone, as to the trash, or for good. */
  gone: boolean;
}

/**
 * The refusal of a batch for revisions that changed elsewhere since, where
 * what changed is what the batch changes too, field by field (PRD §7.10):
 * the overlaps name each such field, with the value that the batch would
 * write and the one that it has now.
 */
export class ConflictError extends ApiError {
  readonly overlaps: Overlap[];

  constructor(error: ApiError, overlaps: Overlap[]) {
    super(error.response, error.error);
    this.name = "ConflictError";
    this.overlaps = overlaps;
  }
}

/** Reports whether error refuses an undo that would not fit in a request, which smaller batches mend (FR-API-03). */
export function undoTooLarge(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.error?.code === "validation_failed" &&
    (error.error.details as { reason?: unknown } | undefined)?.reason ===
      "undo_too_large"
  );
}

/** Reports whether error is the server's own answer, with the error envelope, rather than none. */
function answered(error: unknown): boolean {
  return error instanceof ApiError && error.error !== undefined;
}

/** A batch of the page's own, which the document shows until the server confirms it by seq. */
interface Entry {
  commands: CommandBody[];
  /** The batch's commands without their IDs, as JSON, which a batch sent again as it was matches. */
  key: string;
  time: string;
  /** What the page showed of each entity that the batch expects, when the batch was made. */
  base: Map<string, Revised | undefined>;
  /** The revisions that the page showed of what the batch changes, before it. */
  before: Map<string, number>;
  /** The parts that hold what the batch changes, which the document reads again once no answer comes to it while the channel is not live. */
  parts: Set<Part>;
  /** Whether a batch refused for a revision is sent again where nothing that it changes changed elsewhere. */
  retries: number;
  sent: boolean;
  answer?: CommandResultBody;
  /** What the document held of the entities that the batch changed, before its operation applied. */
  captured?: Captured;
  resolve: (answer: CommandResultBody) => void;
  reject: (error: unknown) => void;
}

interface Revised {
  id: string;
  rev: number;
}

/** A part that the document is reading, with the entities that operations changed while it did. */
interface Load {
  promise: Promise<void>;
  changed: Set<string>;
}

/** The batches that an undo sent in parts applied, before the one that error refused. */
class PartsError extends Error {
  readonly applied: Sent[];
  readonly cause: unknown;

  constructor(applied: Sent[], cause: unknown) {
    super(cause instanceof Error ? cause.message : String(cause));
    this.applied = applied;
    this.cause = cause;
  }
}

/** The answer to a batch, with what the document held of what it changed, before it applied, if it knows yet. */
interface Sent {
  answer: CommandResultBody;
  captured?: Captured;
}

/** How many of a batch's refusals for a revision that changed elsewhere, but not what it changes, it is sent again after. */
const conflictRetries = 3;

/** How many entities the document reads from the server at once. */
const readsAtOnce = 6;

/** How long, in milliseconds, an undo waits for the operations of the batch that it undoes to reach the document. */
const confirmWait = 5_000;

/** The fields of an entity that no command writes, which a conflict does not compare. */
const meta = new Set(["rev", "created_at", "updated_at"]);

export interface DocumentOptions {
  queryClient: QueryClient;
  /** The page's real-time channel, or none, as in a test. */
  realtime?: Realtime;
  log: ErrorLog;
  /** The tab's storage, which keeps the undo history over a reload; none if not set. */
  storage?: Storage;
  /** Returns the time now, as the server writes it. */
  now?: () => string;
}

/** Returns the project of body, as the document holds it. */
function projectOf(body: { role?: unknown; seq?: unknown } & Project): Project {
  const { role: _role, seq: _seq, ...project } = body;
  return project as Project;
}

/** Reports whether a and b are the same JSON value. */
function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** Returns command with what it expects of id moved from before to after. */
function moved(
  command: CommandBody,
  id: string,
  before: number,
  after: number,
): CommandBody {
  const expected = command.expected_rev as Record<string, number> | undefined;
  if (expected?.[id] !== before) {
    return command;
  }
  return { ...command, expected_rev: { ...expected, [id]: after } };
}

/** Returns the revisions that answers left, each entity's from the last answer that changed it. */
function latestRevisions(
  answers: readonly CommandResultBody[],
): Map<string, number> {
  const latest = new Map<string, number>();
  for (const answer of answers) {
    for (const r of answer.revisions ?? []) {
      latest.set(canonical(r.id), r.rev);
    }
  }
  return latest;
}

/** The document of one project. */
export class ProjectDocument {
  readonly projectId: string;
  readonly store: StoreApi<DocumentState>;
  /** Settles once the project has been read, and the document follows it. */
  readonly ready: Promise<void>;
  private readonly options: DocumentOptions;
  private readonly now: () => string;
  private confirmed: Doc = emptyDoc();
  private entries: Entry[] = [];
  private inflight = false;
  /** The operations that came while the document read the project, which it applies once it has. */
  private buffer: Operation[] | undefined = [];
  private readonly loads = new Map<Part, Load>();
  private readonly toRead = new Set<string>();
  private reading = false;
  private readonly unanswered = new Map<string, string[]>();
  private readonly waiters: { seq: number; resolve: () => void }[] = [];
  private readonly history: History;
  private unsubscribe: (() => void) | undefined;
  private disposed = false;

  constructor(projectId: string, options: DocumentOptions) {
    this.projectId = projectId;
    this.options = options;
    this.now = options.now ?? (() => new Date().toISOString());
    const userId = options.queryClient.getQueryData(
      sessionQuery.queryKey,
    )?.user_id;
    this.history = new History(
      options.storage,
      userId === undefined ? undefined : historyKey(userId, projectId),
    );
    this.store = createStore<DocumentState>(() => ({
      view: this.confirmed,
      live: false,
      gone: false,
      sending: 0,
      undoable: this.history.undoable,
      redoable: this.history.redoable,
      stepping: false,
    }));
    this.ready = this.start();
    // A page that waits for the project shows what refuses it; nothing else
    // needs to hear of it here.
    this.ready.catch(() => undefined);
  }

  /** The document as the server has confirmed it. */
  get doc(): Doc {
    return this.confirmed;
  }

  private async start(): Promise<void> {
    const body = await this.options.queryClient.ensureQueryData(
      projectQuery(this.projectId),
    );
    if (this.disposed) {
      return;
    }
    this.confirmed = { ...emptyDoc(body.seq), project: projectOf(body) };
    const buffered = this.buffer ?? [];
    this.buffer = undefined;
    this.unsubscribe = this.options.realtime?.subscribe(this.projectId, {
      since: () => this.confirmed.seq,
      subscribed: (seq) => {
        // A project whose seq is behind the document's, as of a database
        // restored from a backup older than the page, has other operations
        // after it than those that the document has.
        if (seq < this.confirmed.seq) {
          void this.resync(seq);
        }
      },
      operation: (message) => this.receive(operationOf(message)),
      resync: (seq) => void this.resync(seq),
      gone: () => this.goneAway(),
      live: (live) => this.store.setState({ live }),
    });
    for (const op of buffered) {
      this.receive(op);
    }
    this.show();
  }

  /** Stops following the project. */
  dispose(): void {
    this.disposed = true;
    this.unsubscribe?.();
    this.unsubscribe = undefined;
  }

  /**
   * Reads part of the project, once, and keeps it current from then on,
   * resolving once it has; a part being read resolves with that read. A
   * read that fails takes the part out again, so that the next asks anew.
   */
  load(part: Part): Promise<void> {
    const loading = this.loads.get(part);
    if (loading !== undefined) {
      return loading.promise;
    }
    const load: Load = { promise: Promise.resolve(), changed: new Set() };
    load.promise = (async () => {
      await this.ready;
      const since = this.confirmed.seq;
      this.confirmed = withParts(this.confirmed, [part], []);
      try {
        const lists = await readPart(this.projectId, part);
        let doc = this.confirmed;
        for (const list of lists) {
          doc = mergeList(
            doc,
            list.kind,
            part,
            list.items,
            since,
            load.changed,
          );
        }
        this.confirmed = doc;
        this.show();
      } catch (error) {
        this.loads.delete(part);
        this.confirmed = withParts(this.confirmed, [], [part]);
        this.show();
        throw error;
      }
    })();
    this.loads.set(part, load);
    return load.promise;
  }

  /** Reports whether the document holds part, read or being read. */
  holds(part: Part): boolean {
    return this.loads.has(part);
  }

  /** Applies an operation of the project, of the real-time channel, in the order of seqs. */
  private receive(op: Operation): void {
    if (this.disposed) {
      return;
    }
    if (this.buffer !== undefined) {
      this.buffer.push(op);
      return;
    }
    if (op.seq <= this.confirmed.seq) {
      this.settle();
      return;
    }
    if (op.seq > this.confirmed.seq + 1) {
      // The channel sends each subscription its operations in order, so a
      // gap is of a document that went on without one, which reads the
      // project anew, as of op at least, which the reads begin after, and
      // which the channel's next operation follows.
      void this.resync(op.seq);
      return;
    }
    this.confirm(op);
    this.settle();
  }

  /** Applies op, whose seq is the next of the document's, to what the server confirmed. */
  private confirm(op: Operation): void {
    const first = op.commands[0] as { command_id?: string } | undefined;
    const own = this.entries.find(
      (e) =>
        e.commands[0]?.command_id === first?.command_id ||
        e.answer?.seq === op.seq,
    );
    const wanted = own !== undefined || this.history.wants(op.seq);
    const captured: Captured = {};
    if (wanted) {
      for (const r of op.revisions) {
        const id = canonical(r.id);
        captured[id] = entityOf(this.confirmed, id)?.rev ?? null;
      }
    }
    const reconciled = applyOperation(this.confirmed, op);
    for (const load of this.loads.values()) {
      for (const r of op.revisions) {
        load.changed.add(canonical(r.id));
      }
    }
    const before = this.confirmed.project;
    this.confirmed = reconciled.doc;
    if (own !== undefined) {
      own.captured = captured;
      this.entries = this.entries.filter((e) => e !== own);
    }
    if (this.history.wants(op.seq)) {
      this.history.capture(op.seq, captured);
    }
    for (const id of reconciled.toRead) {
      this.toRead.add(id);
    }
    for (const part of reconciled.reread) {
      void this.reread(part);
    }
    this.readLater();
    this.followed(op, before);
  }

  /**
   * Tells the page's other reads of the project what an operation changed:
   * the project itself, which the project's pages read with its role, and
   * the trash, which a deletion or a restoration changes.
   */
  private followed(op: Operation, before: Project | undefined): void {
    const client = this.options.queryClient;
    const project = this.confirmed.project;
    if (project !== undefined && project !== before) {
      client.setQueryData(projectQuery(this.projectId).queryKey, (body) =>
        body === undefined
          ? body
          : { ...body, ...project, seq: this.confirmed.seq },
      );
    }
    if (op.commands.some((c) => /\.(delete|restore)(_many)?$/.test(c.type))) {
      void client.invalidateQueries({
        queryKey: ["projects", this.projectId, "trash"],
      });
    }
  }

  /** Drops what the server has confirmed of the page's batches, and shows the document. */
  private settle(): void {
    const seq = this.confirmed.seq;
    this.entries = this.entries.filter(
      (e) => e.answer === undefined || e.answer.seq > seq,
    );
    for (const waiter of this.waiters.filter((w) => w.seq <= seq)) {
      this.waiters.splice(this.waiters.indexOf(waiter), 1);
      waiter.resolve();
    }
    this.show();
  }

  /** Shows the page the document: what the server confirmed, with the page's batches on top. */
  private show(): void {
    let view = this.confirmed;
    for (const entry of this.entries) {
      view = applyPending(
        view,
        entry.commands,
        entry.time,
        entry.answer?.revisions,
      );
    }
    this.store.setState({
      view,
      sending: this.entries.filter((e) => e.answer === undefined).length,
      undoable: this.history.undoable,
      redoable: this.history.redoable,
    });
  }

  /** Resolves once the document is as of seq, or after wait milliseconds. */
  private whenConfirmed(seq: number, wait: number): Promise<void> {
    if (this.confirmed.seq >= seq) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      const waiter = { seq, resolve };
      this.waiters.push(waiter);
      setTimeout(() => {
        const i = this.waiters.indexOf(waiter);
        if (i >= 0) {
          this.waiters.splice(i, 1);
        }
        resolve();
      }, wait);
    });
  }

  /**
   * Reads the project anew, as once the channel cannot send the operations
   * that the document missed: the project, and every part that it holds,
   * as of seq, after which the operations after seq follow, of which it
   * passes over what it has read already, by their revisions.
   */
  private async resync(seq: number): Promise<void> {
    if (this.buffer !== undefined || this.disposed) {
      return;
    }
    this.buffer = [];
    const parts = [...this.loads.keys()];
    try {
      const body = (
        await api.call("getProject", { path: { project_id: this.projectId } })
      ).body;
      let doc = withParts(
        { ...emptyDoc(seq), project: projectOf(body) },
        parts,
        [],
      );
      for (const part of parts) {
        for (const list of await readPart(this.projectId, part)) {
          doc = mergeList(doc, list.kind, part, list.items, seq, new Set());
        }
      }
      this.confirmed = doc;
    } catch (error) {
      this.options.log.record("request", error);
    } finally {
      const buffered = (this.buffer ?? []).sort((a, b) => a.seq - b.seq);
      this.buffer = undefined;
      for (const op of buffered) {
        this.receive(op);
      }
      this.settle();
    }
  }

  /** Reads part again, as once entities of it came back without revisions of their own. */
  private async reread(part: Part): Promise<void> {
    const since = this.confirmed.seq;
    const changed = new Set<string>();
    const load: Load = { promise: Promise.resolve(), changed };
    const before = this.loads.get(part);
    this.loads.set(part, load);
    try {
      const lists = await readPart(this.projectId, part);
      let doc = this.confirmed;
      if (doc.parts.has(part)) {
        for (const list of lists) {
          doc = mergeList(doc, list.kind, part, list.items, since, changed);
        }
        this.confirmed = doc;
        this.show();
      }
    } catch (error) {
      this.options.log.record("request", error);
    } finally {
      if (this.loads.get(part) === load && before !== undefined) {
        this.loads.set(part, before);
      }
    }
  }

  /** The project can no longer be read: the project's pages show the page of no page. */
  private goneAway(): void {
    this.store.setState({ gone: true, live: false });
  }

  /** Reads what operations changed that their appliers could not know, soon, a few at a time. */
  private readLater(): void {
    if (this.reading || this.toRead.size === 0) {
      return;
    }
    this.reading = true;
    queueMicrotask(() => void this.readAll());
  }

  private async readAll(): Promise<void> {
    try {
      while (this.toRead.size > 0 && !this.disposed) {
        const ids = [...this.toRead];
        this.toRead.clear();
        const since = this.confirmed.seq;
        const folders = ids.some(
          (id) => placeOf(id)?.of === "kind" && id.startsWith("fld_"),
        );
        const tasks = ids
          .filter((id) => !id.startsWith("fld_"))
          .map((id) => () => this.readEntity(id, since));
        if (folders) {
          for (const part of ["tree", "variables", "components"] as const) {
            if (this.confirmed.parts.has(part)) {
              tasks.push(() => this.readFolders(part, since));
            }
          }
        }
        await inTurns(tasks, readsAtOnce);
      }
    } finally {
      this.reading = false;
    }
  }

  /** Reads the entity of id from the server, as one that an operation changed in a way that its appliers could not know. */
  private async readEntity(id: string, since: number): Promise<void> {
    const place = placeOf(id);
    let owner = id;
    if (place?.of === "output") {
      const node = nodeOfOutput(this.confirmed.nodes, id);
      if (node === undefined) {
        return;
      }
      owner = node.id;
    } else if (place?.of === "attribute") {
      const type = typeOfAttribute(this.confirmed.componentTypes, id);
      if (type === undefined) {
        // An attribute definition that came back from the trash, which no
        // type that the document holds has, is of a type that the list of
        // the types has with it.
        if (this.confirmed.parts.has("components")) {
          await this.reread("components");
        }
        return;
      }
      owner = type.id;
    }
    let body: Revised | undefined;
    try {
      body = await readOne(this.projectId, owner);
    } catch (error) {
      this.options.log.record("request", error);
      return;
    }
    if (owner === this.projectId) {
      if (body === undefined) {
        this.goneAway();
        return;
      }
      body = projectOf(body as Project);
    }
    this.confirmed = mergeEntity(this.confirmed, owner, body, since);
    this.show();
  }

  private async readFolders(
    part: "tree" | "variables" | "components",
    since: number,
  ): Promise<void> {
    try {
      const kind =
        part === "tree"
          ? "board"
          : part === "variables"
            ? "variable"
            : "component";
      const items = await everyPage(
        async (cursor) =>
          (
            await api.call("listFolders", {
              path: { project_id: this.projectId },
              query: { kind, cursor, limit: allAtOnce },
            })
          ).body,
      );
      this.confirmed = mergeList(
        this.confirmed,
        "folders",
        part,
        items,
        since,
        new Set(),
      );
      this.show();
    } catch (error) {
      this.options.log.record("request", error);
    }
  }

  /**
   * Applies a batch of commands to the project: at once to what the page
   * shows, and then on the server, after the page's batches before it,
   * resolving with the server's answer, or rejecting with its refusal, once
   * the page shows the project without the batch again. A batch sent again
   * as it was, as once no answer came to it, has the IDs that its commands
   * had, so that the server answers it as the batch that it applied, if it
   * did, rather than apply it again. The batch's undo joins the history.
   */
  async send(commands: readonly Command[]): Promise<CommandResultBody> {
    const sent = await this.enqueue(commands, conflictRetries);
    const answer = sent.answer;
    if (answer.status === "applied") {
      this.history.done(
        answer.undo == null || answer.undo.length === 0
          ? undefined
          : { commands: answer.undo, seqs: [answer.seq] },
      );
      if (sent.captured !== undefined && answer.undo?.length) {
        this.history.capture(answer.seq, sent.captured);
      }
      this.show();
    }
    return answer;
  }

  /** Undoes the latest step of the history, rejecting with the server's refusal, as of what changed since (FR-EDIT-08 AC4). */
  undo(): Promise<void> {
    return this.step("undo");
  }

  /** Redoes the latest step that was undone. */
  redo(): Promise<void> {
    return this.step("redo");
  }

  private async step(which: "undo" | "redo"): Promise<void> {
    const step =
      which === "undo" ? this.history.nextUndo() : this.history.nextRedo();
    if (step === undefined || this.store.getState().stepping) {
      return;
    }
    this.store.setState({ stepping: true });
    try {
      // What the step expects is moved from what the document held before
      // the batches that it undoes, which it knows once it has them.
      if (this.store.getState().live) {
        await this.whenConfirmed(Math.max(...step.seqs), confirmWait);
      }
      const before = this.history.before(step);
      let applied: Sent[] = [];
      let failure: unknown;
      try {
        applied = await this.sendInParts(step.commands);
      } catch (error) {
        if (error instanceof PartsError) {
          applied = error.applied;
          failure = error.cause;
        } else {
          failure = error;
        }
      }
      if (applied.length > 0) {
        const answers = applied.map((s) => s.answer);
        const latest = latestRevisions(answers);
        for (const [id, rev] of latest) {
          const was = before.get(id);
          if (was !== undefined && was !== null) {
            this.history.move(id, was, rev);
          }
        }
        const undos = answers.map((a) => a.undo);
        const next: Step | undefined =
          undos.every((u) => u !== null) && undos.some((u) => u.length > 0)
            ? {
                commands: joinedUndo(undos as UndoCommandBody[][], latest),
                seqs: answers.map((a) => a.seq),
              }
            : undefined;
        if (which === "undo") {
          this.history.undone(step, next);
        } else {
          this.history.redone(step, next);
        }
        for (const s of applied) {
          if (s.captured !== undefined && this.history.wants(s.answer.seq)) {
            this.history.capture(s.answer.seq, s.captured);
          }
        }
      } else if (answered(failure)) {
        // A step that the server refused, as for what changed since, would
        // be refused again.
        this.history.drop(step);
      }
      if (failure !== undefined) {
        throw failure;
      }
    } finally {
      this.store.setState({ stepping: false });
      this.show();
    }
  }

  /**
   * Sends the commands of a step, as one batch, or, once the server refuses
   * it as an undo too large for one request, in two halves, each split
   * again as it needs, in order, each once the one before has applied, with
   * what it expects of an entity that those before it brought back moved to
   * the revision that the last of them that changed it left (FR-EDIT-08).
   */
  private async sendInParts(
    commands: readonly UndoCommandBody[],
  ): Promise<Sent[]> {
    try {
      return [await this.enqueue(commands as unknown as Command[], 0)];
    } catch (error) {
      if (!undoTooLarge(error) || commands.length < 2) {
        throw error;
      }
    }
    const half = Math.ceil(commands.length / 2);
    const first = await this.sendInParts(commands.slice(0, half));
    const latest = latestRevisions(first.map((s) => s.answer));
    try {
      const rest = await this.sendInParts(
        commands.slice(half).map((c) => withRevisions(c, latest)),
      );
      return [...first, ...rest];
    } catch (error) {
      const applied =
        error instanceof PartsError ? [...first, ...error.applied] : first;
      throw new PartsError(
        applied,
        error instanceof PartsError ? error.cause : error,
      );
    }
  }

  /** Queues a batch, which the page shows at once, and resolves with its answer. */
  private enqueue(
    commands: readonly Command[],
    retries: number,
  ): Promise<Sent> {
    const key = JSON.stringify(commands);
    const ids =
      this.unanswered.get(key) ?? commands.map(() => formatUuid(newUuid()));
    this.unanswered.set(key, ids);
    const withIds = commands.map(
      (command, i) => ({ ...command, command_id: ids[i] }) as CommandBody,
    );
    const view = this.store.getState().view;
    const base = new Map<string, Revised | undefined>();
    for (const command of withIds) {
      for (const id of Object.keys(command.expected_rev ?? {})) {
        base.set(id, entityOf(view, canonical(id)));
      }
    }
    const draft = new Draft(view, this.now());
    applyCommands(draft, withIds);
    const before = new Map<string, number>();
    const parts = new Set<Part>();
    const after = draft.finish(view.seq);
    for (const id of draft.changes.keys()) {
      const was = entityOf(view, id);
      if (was !== undefined) {
        before.set(id, was.rev);
      }
      const part = partOfId(view, id) ?? partOfId(after, id);
      if (part !== undefined) {
        parts.add(part);
      }
    }
    return new Promise<Sent>((resolve, reject) => {
      const entry: Entry = {
        commands: withIds,
        key,
        time: draft.time,
        base,
        before,
        parts,
        retries,
        sent: false,
        resolve: (answer) => resolve({ answer, captured: entry.captured }),
        reject,
      };
      this.entries.push(entry);
      this.show();
      void this.pump();
    });
  }

  /** Sends the next queued batch, once the one before has been answered. */
  private async pump(): Promise<void> {
    if (this.inflight) {
      return;
    }
    const entry = this.entries.find((e) => !e.sent);
    if (entry === undefined) {
      return;
    }
    this.inflight = true;
    entry.sent = true;
    try {
      await this.ready;
      const answer = await this.post(entry.commands);
      entry.answer = answer;
      this.moveQueued(entry, answer);
      if (
        answer.status === "applied" &&
        answer.seq === this.confirmed.seq + 1 &&
        this.entries.includes(entry) &&
        this.buffer === undefined
      ) {
        this.confirm({
          seq: answer.seq,
          commands: entry.commands,
          revisions: answer.revisions ?? [],
          time: entry.time,
        });
      }
      this.unanswered.delete(entry.key);
      this.settle();
      entry.resolve(answer);
    } catch (error) {
      if (
        error instanceof ApiError &&
        error.error?.code === "revision_conflict"
      ) {
        // The page shows what changed elsewhere at once, whatever becomes
        // of the batch, as the channel would bring it later.
        this.adopt(error);
      }
      if (this.retried(entry, error)) {
        return;
      }
      if (answered(error)) {
        // The server answers a batch sent again with these IDs alike, so
        // the next has IDs of its own.
        this.unanswered.delete(entry.key);
      } else if (!this.store.getState().live) {
        // A batch that no answer came to may have applied all the same,
        // which the channel, while it is not live, would not bring: the
        // parts that it changes are read again, so that the page shows it,
        // as a board that a dialog created, rather than offer to make it
        // anew, with IDs of its own, which would make another.
        for (const part of entry.parts) {
          void this.reread(part);
        }
      }
      this.entries = this.entries.filter((e) => e !== entry);
      this.show();
      entry.reject(
        error instanceof ApiError && error.error?.code === "revision_conflict"
          ? new ConflictError(error, this.overlaps(entry, error))
          : error,
      );
    } finally {
      this.inflight = false;
      void this.pump();
    }
  }

  private async post(commands: CommandBody[]): Promise<CommandResultBody> {
    const session = this.options.queryClient.getQueryData(
      sessionQuery.queryKey,
    );
    const answer = (
      await api.call("applyCommands", {
        path: { project_id: this.projectId },
        headers: csrfHeaders(session),
        body: { commands },
      })
    ).body;
    // A single batch is answered with its result, never with a list.
    if ("results" in answer) {
      throw new Error("The server answered a batch with a list of results.");
    }
    return answer;
  }

  /**
   * Moves what the queued batches after entry expect of each entity that
   * entry changed, from the revision that the page showed before entry,
   * which they were made on top of, to the one that its answer gives.
   */
  private moveQueued(entry: Entry, answer: CommandResultBody): void {
    for (const r of answer.revisions ?? []) {
      const id = canonical(r.id);
      const from = entry.before.get(id);
      if (from === undefined) {
        continue;
      }
      for (const queued of this.entries) {
        if (!queued.sent && queued !== entry) {
          queued.commands = queued.commands.map((c) =>
            moved(c, id, from, r.rev),
          );
        }
      }
    }
  }

  /**
   * Sends entry again, once the server refused it for revisions that
   * changed elsewhere, where nothing that it changes did, field by field,
   * expecting the revisions that the entities have now, with new IDs, and
   * reports whether it did (PRD §7.10 item 1).
   */
  private retried(entry: Entry, error: unknown): boolean {
    if (
      entry.retries <= 0 ||
      !(error instanceof ApiError) ||
      error.error?.code !== "revision_conflict"
    ) {
      return false;
    }
    const conflicts = conflictsOf(error);
    if (conflicts.length === 0 || this.overlaps(entry, error).length > 0) {
      return false;
    }
    let commands = entry.commands;
    for (const c of conflicts) {
      if (c.current_rev === undefined) {
        return false;
      }
      commands = commands.map((command) =>
        moved(command, c.id, c.expected_rev, c.current_rev as number),
      );
    }
    entry.commands = commands.map((c) => ({
      ...c,
      command_id: formatUuid(newUuid()),
    }));
    this.unanswered.delete(entry.key);
    entry.retries--;
    entry.sent = false;
    this.show();
    return true;
  }

  /**
   * Takes into what the server confirmed the states that a refusal for a
   * revision gives of the entities that changed elsewhere, which the
   * channel brings too, but maybe later, so that the page shows them at
   * once, as the batch sent again applies on top of them.
   */
  private adopt(error: ApiError): void {
    const states = statesOf(error);
    // An entity whose state the refusal does not give, as one that is gone,
    // is read from the server.
    for (const c of conflictsOf(error)) {
      if (!(c.id in states)) {
        this.toRead.add(canonical(c.id));
      }
    }
    this.readLater();
    let doc = this.confirmed;
    for (const [id, state] of Object.entries(states)) {
      if (state.deleted_at === undefined && typeof state.rev === "number") {
        const entity =
          placeOf(id)?.of === "project"
            ? projectOf(state as unknown as Project)
            : (state as unknown as Revised);
        doc = mergeEntity(doc, canonical(id), entity, doc.seq);
      }
    }
    this.confirmed = doc;
  }

  /**
   * Returns each entity of a refusal for revisions that changed elsewhere
   * whose change is in what entry changes, field by field, or that is gone,
   * with both values of each such field: the batch's, as it would leave the
   * entity as the page showed it, and the one that it has now.
   */
  private overlaps(entry: Entry, error: ApiError): Overlap[] {
    const states = statesOf(error);
    const overlaps: Overlap[] = [];
    for (const c of conflictsOf(error)) {
      const id = canonical(c.id);
      const state = states[c.id] ?? states[id];
      if (!c.exists || state === undefined || state.deleted_at !== undefined) {
        overlaps.push({ id, state: state ?? {}, fields: [], gone: true });
        continue;
      }
      const base = entry.base.get(c.id) as Record<string, unknown> | undefined;
      if (base === undefined) {
        overlaps.push({ id, state, fields: [], gone: false });
        continue;
      }
      const mine = this.written(entry, id, base);
      const fields = [];
      for (const field of new Set([
        ...Object.keys(base),
        ...Object.keys(mine ?? {}),
      ])) {
        if (meta.has(field)) {
          continue;
        }
        const value = mine?.[field];
        if (!same(value, base[field]) && !same(state[field], base[field])) {
          fields.push({ field, mine: value, theirs: state[field] });
        }
      }
      if (mine === undefined || fields.length > 0) {
        overlaps.push({ id, state, fields, gone: false });
      }
    }
    return overlaps;
  }

  /** Returns the entity of id as entry's commands would leave it from base, or undefined if they take it away. */
  private written(
    entry: Entry,
    id: string,
    base: Record<string, unknown>,
  ): Record<string, unknown> | undefined {
    const seeded = withEntity(this.confirmed, id, base as unknown as Revised);
    const after = applyPending(seeded, entry.commands, entry.time);
    return entityOf(after, id) as Record<string, unknown> | undefined;
  }
}

/** Returns the part of doc that holds the entity of id, or undefined for none, as the project. */
function partOfId(doc: Doc, id: string): Part | undefined {
  const place = placeOf(id);
  switch (place?.of) {
    case "kind": {
      const entity = doc[place.kind].get(id);
      return entity === undefined ? undefined : partOf(place.kind, entity);
    }
    case "output": {
      const node = nodeOfOutput(doc.nodes, id);
      return node === undefined ? undefined : partOf("nodes", node);
    }
    case "attribute":
      return "components";
  }
  return undefined;
}

/** Returns doc holding the parts of add, and not those of remove, with their entities. */
function withParts(
  doc: Doc,
  add: readonly Part[],
  remove: readonly Part[],
): Doc {
  if (
    add.every((p) => doc.parts.has(p)) &&
    remove.every((p) => !doc.parts.has(p))
  ) {
    return doc;
  }
  const parts = new Set(doc.parts);
  for (const part of add) {
    parts.add(part);
  }
  const draft = new Draft({ ...doc, parts }, "");
  for (const part of remove) {
    draft.drop(part);
  }
  return draft.finish(doc.seq);
}

/** A conflict of a refusal for a revision (revision_conflict). */
interface Conflict {
  id: string;
  expected_rev: number;
  exists: boolean;
  current_rev?: number;
}

function conflictsOf(error: ApiError): Conflict[] {
  const conflicts = (error.error?.details as { conflicts?: unknown })
    ?.conflicts;
  return Array.isArray(conflicts) ? (conflicts as Conflict[]) : [];
}

function statesOf(error: ApiError): Record<string, Record<string, unknown>> {
  const states = (error.error?.details as { states?: unknown })?.states;
  return typeof states === "object" && states !== null
    ? (states as Record<string, Record<string, unknown>>)
    : {};
}

/** Returns the operation of a message of the real-time channel. */
function operationOf(message: OperationMessage): Operation {
  return {
    seq: message.seq,
    commands: message.commands ?? [],
    revisions: message.revisions ?? [],
    time: message.created_at,
  };
}

/** Runs tasks, at most n at a time. */
async function inTurns(
  tasks: readonly (() => Promise<void>)[],
  n: number,
): Promise<void> {
  let next = 0;
  const run = async () => {
    while (next < tasks.length) {
      await tasks[next++]();
    }
  };
  await Promise.all(Array.from({ length: Math.min(n, tasks.length) }, run));
}

/** A list that a part is read from. */
type List = { [K in Kind]: { kind: K; items: Entities[K][] } }[Kind];

/** Reads the lists of part, every page of each. */
async function readPart(projectId: string, part: Part): Promise<List[]> {
  const path = { project_id: projectId };
  const page = { limit: allAtOnce };
  const folders = (kind: "board" | "variable" | "component") =>
    everyPage(
      async (cursor) =>
        (
          await api.call("listFolders", {
            path,
            query: { kind, cursor, ...page },
          })
        ).body,
    ).then((items): List => ({ kind: "folders", items }));
  switch (part) {
    case "tree":
      return Promise.all([
        folders("board"),
        everyPage(
          async (cursor) =>
            (await api.call("listBoards", { path, query: { cursor, ...page } }))
              .body,
        ).then((items): List => ({ kind: "boards", items })),
      ]);
    case "variables":
      return Promise.all([
        folders("variable"),
        everyPage(
          async (cursor) =>
            (
              await api.call("listVariables", {
                path,
                query: { cursor, ...page },
              })
            ).body,
        ).then((items): List => ({ kind: "variables", items })),
      ]);
    case "components":
      return Promise.all([
        folders("component"),
        everyPage(
          async (cursor) =>
            (
              await api.call("listComponentTypes", {
                path,
                query: { cursor, ...page },
              })
            ).body,
        ).then((items): List => ({ kind: "componentTypes", items })),
        everyPage(
          async (cursor) =>
            (
              await api.call("listComponents", {
                path,
                query: { cursor, ...page },
              })
            ).body,
        ).then((items): List => ({ kind: "components", items })),
      ]);
  }
  const board = { ...path, board_id: part.slice("board:".length) };
  return Promise.all([
    everyPage(
      async (cursor) =>
        (
          await api.call("listBoardNodes", {
            path: board,
            query: { cursor, ...page },
          })
        ).body,
    ).then((items): List => ({ kind: "nodes", items })),
    everyPage(
      async (cursor) =>
        (
          await api.call("listBoardConnections", {
            path: board,
            query: { cursor, ...page },
          })
        ).body,
    ).then((items): List => ({ kind: "connections", items })),
  ]);
}

/** Reads the entity of id, or resolves to undefined if the server has none for the user (404). */
async function readOne(
  projectId: string,
  id: string,
): Promise<Revised | undefined> {
  const project_id = projectId;
  const read = async (): Promise<Revised> => {
    const place = placeOf(id);
    if (place?.of === "project") {
      return (await api.call("getProject", { path: { project_id } })).body;
    }
    switch (place?.of === "kind" ? place.kind : undefined) {
      case "boards":
        return (
          await api.call("getBoard", { path: { project_id, board_id: id } })
        ).body;
      case "nodes":
        return (
          await api.call("getNode", { path: { project_id, node_id: id } })
        ).body;
      case "connections":
        return (
          await api.call("getConnection", {
            path: { project_id, connection_id: id },
          })
        ).body;
      case "variables":
        return (
          await api.call("getVariable", {
            path: { project_id, variable_id: id },
          })
        ).body;
      case "componentTypes":
        return (
          await api.call("getComponentType", {
            path: { project_id, component_type_id: id },
          })
        ).body;
      case "components":
        return (
          await api.call("getComponent", {
            path: { project_id, component_id: id },
          })
        ).body;
    }
    throw new Error(`The document cannot read ${id} from the server.`);
  };
  try {
    return await read();
  } catch (error) {
    if (
      error instanceof ApiError &&
      error.status === 404 &&
      error.error?.code === "not_found"
    ) {
      return undefined;
    }
    throw error;
  }
}
