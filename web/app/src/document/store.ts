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
  kinds,
  nodeOfOutput,
  type Part,
  type Project,
  partOf,
  placeOf,
  typeOfAttribute,
} from "./doc";
import { backoff, type Realtime } from "./realtime";
import {
  applyOperation,
  applyPending,
  entityOf,
  equal,
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

/**
 * Reports whether error refuses what a step of the history does, which
 * sending it again would meet again, as for what changed since: the server's
 * answer with a status of 4xx, but for those that refuse when or by whom it
 * was sent, which may not refuse it later: a session that has ended (401),
 * credentials or a role that do not allow it (403), a request that took too
 * long (408), too many requests (429), and a project that is archived until
 * it is unarchived (project_archived). A failure of the server's own, as 503
 * not_ready, may not come again either (FR-EDIT-08).
 */
function refusedForGood(error: unknown): boolean {
  if (!answered(error)) {
    return false;
  }
  const { status, error: body } = error as ApiError;
  return (
    status >= 400 &&
    status < 500 &&
    ![401, 403, 408, 429].includes(status) &&
    body?.code !== "project_archived"
  );
}

/** A batch of the page's own, which the document shows until the server confirms it by seq. */
interface Entry {
  commands: CommandBody[];
  /** The batch's commands without their IDs, as JSON, which a batch sent again as it was matches. */
  key: string;
  time: string;
  /** What the page showed of each entity that the batch expects, when the batch was made (baseOf). */
  base: Map<string, Base>;
  /** The revisions that the page showed of what the batch changes, before it, moved as the batches before it are answered (moveQueued). */
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

/** What the page showed of an entity that a batch expects, which a conflict of the batch is compared with (overlaps). */
interface Base {
  /** The entity, or undefined if the page showed none. */
  entity: Revised | undefined;
  /**
   * Whether entity is as the server had it at the revision that the batch
   * expects; otherwise it is as the page showed it once the batch was made,
   * later, as by a form that held what it showed before, which the document
   * no longer knows (earlier).
   */
  exact: boolean;
}

/**
 * The batches that an undo sent in parts applied, before the one that error
 * refused, and the commands that did not apply, each expecting what those
 * that did left.
 */
class PartsError extends Error {
  readonly applied: Sent[];
  readonly cause: unknown;
  readonly rest: UndoCommandBody[];

  constructor(applied: Sent[], cause: unknown, rest: UndoCommandBody[]) {
    super(cause instanceof Error ? cause.message : String(cause));
    this.applied = applied;
    this.cause = cause;
    this.rest = rest;
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

/**
 * How many entities of one part an operation may leave to read from the
 * server, each by its ID, before the part is read again instead, as once a
 * variable's rename has compiled anew the expressions of a board's hundreds
 * of nodes, which its lists, 200 to a page, answer in a few requests.
 */
const readsOfOnePart = 20;

/** How long, in milliseconds, an undo waits for the operations of the batch that it undoes to reach the document. */
const confirmWait = 5_000;

/** How many of the states that entities had before operations changed them the document keeps, the latest (earlier). */
const earlierKept = 1_000;

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

/**
 * Returns the revisions that commands expect of each entity: what the
 * server had of it once it had every operation before their batch, as the
 * batch applied.
 */
function expectedOf(commands: readonly CommandBody[]): Map<string, number> {
  const expected = new Map<string, number>();
  for (const command of commands) {
    for (const [id, rev] of Object.entries(command.expected_rev ?? {})) {
      expected.set(canonical(id), rev);
    }
  }
  return expected;
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
  /** The parts that the document holds, read or being read, each with its read. */
  private readonly loads = new Map<Part, Promise<void>>();
  /**
   * The entities that operations changed while a read of a part, or of a
   * part's folders, was under way, one set for each such read, so that its
   * list does not take out what an operation put in since the read began.
   */
  private readonly watchers = new Set<Set<string>>();
  /** How many reads of parts are under way. */
  private partReads = 0;
  private started = false;
  /** The reading anew of the project, under way, or waiting to try again once it failed. */
  private resyncing: Promise<void> | undefined;
  private resyncTimer: ReturnType<typeof setTimeout> | undefined;
  /**
   * How many times the document has read the project anew (resync), so that
   * a read that began before, whose answer comes after, is not merged into
   * what that read found, which has read what it read too, later, and which
   * has forgotten what went from the document before (Doc.gone).
   */
  private generation = 0;
  /**
   * What the document held of entities before operations changed them, each
   * by its ID and the revision that it had then, the latest (earlierKept):
   * what the page showed of one, as a form that holds it does, while the
   * document went on, which a batch that expects that revision is compared
   * with once it is refused for it (overlaps).
   */
  private readonly earlier = new Map<string, Revised>();
  private readonly toRead = new Set<string>();
  private reading = false;
  /** The IDs of the commands of each batch that no answer came to, by its key, which a batch sent again as it was takes (enqueue). */
  private readonly unanswered = new Map<string, string[]>();
  private readonly waiters: { seq: number; resolve: () => void }[] = [];
  private readonly history: History;
  /** The account that the document is of, as the page's session was once it was made, whose batches it sends as no other's (post). */
  private readonly userId: string | undefined;
  /** The undo or the redo under way, which another asked for meanwhile settles as (step). */
  private stepUnderWay: Promise<void> | undefined;
  /** Reads the project anew now, once a read of it failed and waits to try again (reload). */
  private retryNow: (() => void) | undefined;
  /**
   * The seq that the project read anew is to be as of, at least, as a
   * resync_required that came while it was under way asks, whose
   * operations up to it the channel does not send (reload).
   */
  private resyncTo = 0;
  private unsubscribe: (() => void) | undefined;
  private disposed = false;

  constructor(projectId: string, options: DocumentOptions) {
    this.projectId = projectId;
    this.options = options;
    this.now = options.now ?? (() => new Date().toISOString());
    const userId = options.queryClient.getQueryData(
      sessionQuery.queryKey,
    )?.user_id;
    this.userId = userId;
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
    this.ready.then(
      () => {
        this.started = true;
      },
      () => {
        this.started = true;
      },
    );
  }

  /** The document as the server has confirmed it. */
  get doc(): Doc {
    return this.confirmed;
  }

  /** Reports whether the document is reading the project, or a part of it, which a route's loader may be waiting for. */
  get busy(): boolean {
    return !this.started || this.partReads > 0;
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

  /**
   * Stops following the project, and keeping the history in the tab's
   * storage, so that the answer to a batch that comes after, as once the
   * page has signed out, does not write it there again, over what signing
   * out cleared, or what the project's next document keeps.
   */
  dispose(): void {
    this.disposed = true;
    clearTimeout(this.resyncTimer);
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    this.history.close();
  }

  /**
   * Reads part of the project, once, and keeps it current from then on,
   * resolving once it has; a part being read resolves with that read. A
   * read that fails takes the part out again, so that the next asks anew.
   */
  load(part: Part): Promise<void> {
    const loading = this.loads.get(part);
    if (loading !== undefined) {
      return loading;
    }
    this.partReads++;
    // A read that fails takes out the part, unless an operation has taken
    // it out already, and a page has asked for it anew since.
    let load: Promise<void> | undefined;
    load = (async () => {
      const changed = this.watch();
      try {
        await this.ready;
        const since = this.confirmed.seq;
        const generation = this.generation;
        this.confirmed = withParts(this.confirmed, [part], []);
        const lists = await readPart(this.projectId, part);
        // The project read anew meanwhile read the part with the others.
        if (this.generation === generation) {
          let doc = this.confirmed;
          for (const list of lists) {
            doc = mergeList(doc, list.kind, part, list.items, since, changed);
          }
          this.took(doc);
        }
        this.show();
      } catch (error) {
        if (this.loads.get(part) === load) {
          this.loads.delete(part);
          this.confirmed = withParts(this.confirmed, [], [part]);
          this.show();
        }
        throw error;
      } finally {
        this.watchers.delete(changed);
        this.partReads--;
      }
    })();
    this.loads.set(part, load);
    return load;
  }

  /**
   * Reads the project anew while the real-time channel is not live, as the
   * document may have missed what changed since, as a board that another
   * session created, and resolves once it has, or has failed to.
   */
  refresh(): Promise<void> {
    if (this.store.getState().live) {
      return Promise.resolve();
    }
    return this.resync(this.confirmed.seq);
  }

  /** Returns a set of the entities that operations change from now on, until the caller takes it out of watchers. */
  private watch(): Set<string> {
    const changed = new Set<string>();
    this.watchers.add(changed);
    return changed;
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
    // A batch sent again as it was, with the IDs that its commands had, that
    // is queued still, is not the operation's: the operation is of the batch
    // that no answer came to, and the one queued, which would never be sent
    // once taken out, is answered as that one once it is (settle).
    const own = this.entries.find(
      (e) =>
        e.sent &&
        (e.commands[0]?.command_id === first?.command_id ||
          e.answer?.seq === op.seq),
    );
    const wanted = own !== undefined || this.history.wants(op.seq);
    const captured: Captured = {};
    if (wanted) {
      // What the batch's commands expected is what the server had of each
      // entity once it had every operation before the batch, as the batch
      // applied. What they did not expect is as the document holds it, if it
      // holds it as of before the operation: one that it holds as of the
      // operation or later, as read from the server since, it does not know
      // as it was before, and leaves out.
      const expected = expectedOf(own?.commands ?? []);
      for (const r of op.revisions) {
        const id = canonical(r.id);
        const held = entityOf(this.confirmed, id)?.rev;
        const sent = expected.get(id);
        if (sent !== undefined) {
          captured[id] = sent;
        } else if (held === undefined) {
          captured[id] = null;
        } else if (held < r.rev) {
          captured[id] = held;
        }
      }
    }
    for (const r of op.revisions) {
      const id = canonical(r.id);
      const held = entityOf(this.confirmed, id);
      if (held !== undefined && held.rev < r.rev) {
        this.remember(id, held);
      }
    }
    const reconciled = applyOperation(this.confirmed, op);
    for (const changed of this.watchers) {
      for (const r of op.revisions) {
        changed.add(canonical(r.id));
      }
    }
    // A part that the operation took out whole, as a board's once the board
    // is in the trash, is no longer held, so that a page that shows it again,
    // once the board is back, reads it anew, and the project read anew does
    // not ask for it.
    for (const part of reconciled.dropped) {
      this.loads.delete(part);
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

  /** Keeps what the document held of the entity of id before an operation changed it, as entity (earlier). */
  private remember(id: string, entity: Revised): void {
    const key = `${id}@${entity.rev}`;
    // What it held first at a revision is what the server had at it: what
    // it holds at the same revision after an operation's change that it was
    // not sure of is what it predicted.
    if (this.earlier.has(key)) {
      return;
    }
    this.earlier.set(key, entity);
    if (this.earlier.size > earlierKept) {
      this.earlier.delete(this.earlier.keys().next().value as string);
    }
  }

  /**
   * Tells the page's other reads of the project what an operation changed,
   * or, for none, what the project read anew may have: the project itself,
   * which the project's pages read with its role, as the document has it,
   * without what the operation took out of it, as an unarchive's
   * archived_at, and the trash, which a deletion or a restoration changes.
   */
  private followed(
    op: Operation | undefined,
    before: Project | undefined,
  ): void {
    this.projectFollowed(before);
    if (
      op === undefined ||
      op.commands.some((c) => /\.(delete|restore)(_many)?$/.test(c.type))
    ) {
      void this.options.queryClient.invalidateQueries({
        queryKey: ["projects", this.projectId, "trash"],
      });
    }
  }

  /**
   * Tells the project's read what the document holds of the project, once
   * it is another than before, however it came: by an operation, by the
   * project read anew, or by a read of it, or a refusal's state of it,
   * which an operation of the same revision that comes after leaves as it
   * is (applyOperation).
   */
  private projectFollowed(before: Project | undefined): void {
    const project = this.confirmed.project;
    if (project !== undefined && project !== before) {
      this.options.queryClient.setQueryData(
        projectQuery(this.projectId).queryKey,
        (body) =>
          body === undefined
            ? body
            : { ...project, role: body.role, seq: this.confirmed.seq },
      );
    }
  }

  /**
   * Takes doc, which reads from the server have brought, for what the
   * server confirmed, keeping what the document held of each entity that
   * doc holds at a later revision (earlier), as a form may show it still.
   */
  private took(doc: Doc): void {
    const was = this.confirmed;
    const project = was.project;
    if (
      project !== undefined &&
      doc.project !== undefined &&
      doc.project.rev > project.rev
    ) {
      this.remember(project.id, project);
    }
    for (const kind of kinds) {
      if (was[kind] === doc[kind]) {
        continue;
      }
      for (const [id, held] of was[kind] as ReadonlyMap<string, Revised>) {
        const now = (doc[kind] as ReadonlyMap<string, Revised>).get(id);
        if (now === undefined || now === held) {
          continue;
        }
        if (now.rev > held.rev) {
          this.remember(id, held);
        }
        const field =
          kind === "nodes"
            ? "outputs"
            : kind === "componentTypes"
              ? "attributes"
              : undefined;
        if (field !== undefined) {
          const nested = (entity: Revised) =>
            ((entity as unknown as Record<string, unknown>)[field] ??
              []) as Revised[];
          const later = new Map(nested(now).map((c) => [c.id, c]));
          for (const child of nested(held)) {
            const next = later.get(child.id);
            if (next !== undefined && next.rev > child.rev) {
              this.remember(child.id, child);
            }
          }
        }
      }
    }
    this.confirmed = doc;
  }

  /** Drops what the server has confirmed of the page's batches, and shows the document. */
  private settle(): void {
    const seq = this.confirmed.seq;
    for (const entry of this.entries) {
      const answer = entry.answer;
      if (
        answer?.status === "applied" &&
        answer.seq <= seq &&
        entry.captured === undefined
      ) {
        // The batch's operation reached the document without it, as once no
        // answer came to it at first, and it was sent again, or as the
        // project read anew: what its commands expected is what the server
        // had before it, as it applied, which the step that undoes it moves
        // the other steps from (confirm).
        entry.captured = Object.fromEntries(expectedOf(entry.commands));
        if (this.history.wants(answer.seq)) {
          this.history.capture(answer.seq, entry.captured);
        }
      }
    }
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
   * passes over what it has read already, by their revisions. It resolves
   * once it has, or has failed to, after which it tries again, later each
   * time, as the channel connects again (backoff), with the operations that
   * come meanwhile kept for once it has, rather than applied to what it has
   * missed, which would only find the gap again.
   */
  private resync(seq: number): Promise<void> {
    if (this.disposed) {
      return Promise.resolve();
    }
    if (this.buffer !== undefined) {
      // The document is reading the project already, which is as of seq at
      // least once it has, as it reads it again if it is not (resyncTo), or
      // has yet to read it. A read that failed, which waits to try again,
      // tries now, as the channel, or a page, that asks for it is back.
      this.resyncTo = Math.max(this.resyncTo, seq);
      this.retryNow?.();
      return this.resyncing ?? Promise.resolve();
    }
    this.buffer = [];
    this.resyncTo = seq;
    return this.reloading(seq, 0);
  }

  /** Reads the project anew as reload does, as the reading anew under way, which resync and refresh resolve with. */
  private reloading(seq: number, failures: number): Promise<void> {
    const resyncing = this.reload(seq, failures).finally(() => {
      if (this.resyncing === resyncing) {
        this.resyncing = undefined;
      }
    });
    this.resyncing = resyncing;
    return resyncing;
  }

  /** Reads the project anew as resync does, after failures failures in a row. */
  private async reload(seq: number, failures: number): Promise<void> {
    let doc: Doc;
    try {
      const body = (
        await api.call("getProject", { path: { project_id: this.projectId } })
      ).body;
      // The parts are read after the project, so they are as of its seq at
      // least, which may be past seq, as once the page read the project
      // anew while the channel was not live (refresh), and which the next
      // answer to the page's batches follows.
      doc = {
        ...emptyDoc(Math.max(seq, body.seq)),
        project: projectOf(body),
      };
      // A part that a page asks for while the project is read anew is read
      // with the others, as the document read anew would otherwise not hold
      // it, and pass over what its own read found. The parts are read a few
      // at a time, rather than one after another, as of a session that has
      // opened many boards.
      const read = new Set<Part>();
      for (;;) {
        const parts = [...this.loads.keys()].filter((p) => !read.has(p));
        if (parts.length === 0) {
          break;
        }
        const lists = new Map<Part, List[]>();
        for (const part of parts) {
          read.add(part);
        }
        await inTurns(
          parts.map((part) => async () => {
            try {
              lists.set(part, await readPart(this.projectId, part));
            } catch (error) {
              if (part.startsWith("board:") && notFound(error)) {
                // The board went to the trash, by an operation that the
                // document has yet to apply, which takes its part out.
                this.loads.delete(part);
                return;
              }
              throw error;
            }
          }),
          readsAtOnce,
        );
        for (const [part, of] of lists) {
          doc = withParts(doc, [part], []);
          for (const list of of) {
            doc = mergeList(
              doc,
              list.kind,
              part,
              list.items,
              doc.seq,
              new Set(),
            );
          }
        }
      }
      // A part whose own read failed meanwhile is not held.
      doc = withParts(
        doc,
        [],
        [...doc.parts].filter((p) => !this.loads.has(p)),
      );
    } catch (error) {
      if (this.disposed) {
        return;
      }
      if (notFound(error)) {
        // The project can no longer be read.
        this.buffer = undefined;
        this.goneAway();
        return;
      }
      this.options.log.record("request", error);
      const retry = () => {
        clearTimeout(this.resyncTimer);
        this.retryNow = undefined;
        void this.reloading(seq, failures + 1);
      };
      this.retryNow = retry;
      this.resyncTimer = setTimeout(retry, backoff(failures + 1, Math.random));
      return;
    }
    if (this.disposed) {
      return;
    }
    const before = this.confirmed.project;
    this.took(doc);
    this.generation++;
    this.followed(undefined, before);
    const buffered = (this.buffer ?? []).sort((a, b) => a.seq - b.seq);
    this.buffer = undefined;
    for (const op of buffered) {
      this.receive(op);
    }
    this.settle();
    // A resync_required that came while the project was read, of a seq past
    // what it read, is followed by the operations after that seq alone:
    // those up to it the document reads anew again.
    if (this.resyncTo > this.confirmed.seq) {
      void this.resync(this.resyncTo);
    }
    this.catchUp();
  }

  /**
   * Reads the project anew, while the channel is not live, once the answers
   * to the page's batches are past what the document has: the operations
   * before them, of other sessions, would not come until the channel is live
   * again, if ever, as behind a proxy that refuses the socket, and until
   * they do the batches stay on top of the document, which would show
   * nothing of what changed elsewhere.
   */
  private catchUp(): void {
    if (this.store.getState().live || this.buffer !== undefined) {
      return;
    }
    // An answer of already_applied, as to a batch sent again once the
    // server no longer kept its first answer, gives the seq that applied it,
    // which the document follows as it does an applied one's.
    let seq = this.confirmed.seq;
    for (const entry of this.entries) {
      if (entry.answer !== undefined && entry.answer.seq > seq) {
        seq = entry.answer.seq;
      }
    }
    if (seq > this.confirmed.seq) {
      void this.resync(seq);
    }
  }

  /** Reads part again, as once entities of it came back without revisions of their own. */
  private async reread(part: Part): Promise<void> {
    if (this.disposed) {
      return;
    }
    const since = this.confirmed.seq;
    const generation = this.generation;
    const changed = this.watch();
    try {
      const lists = await readPart(this.projectId, part);
      let doc = this.confirmed;
      if (doc.parts.has(part) && this.generation === generation) {
        for (const list of lists) {
          doc = mergeList(doc, list.kind, part, list.items, since, changed);
        }
        this.took(doc);
        this.show();
      }
    } catch (error) {
      this.options.log.record("request", error);
    } finally {
      this.watchers.delete(changed);
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
        // A part of which there are many to read is read again whole, in a
        // few pages of its lists, rather than each of them by its own read.
        const byPart = new Map<Part | undefined, string[]>();
        for (const id of ids.filter((id) => !id.startsWith("fld_"))) {
          const part = partOfId(this.confirmed, id);
          const of = byPart.get(part);
          if (of === undefined) {
            byPart.set(part, [id]);
          } else {
            of.push(id);
          }
        }
        const tasks: (() => Promise<void>)[] = [];
        for (const [part, of] of byPart) {
          if (part !== undefined && of.length > readsOfOnePart) {
            tasks.push(() => this.reread(part));
          } else {
            tasks.push(...of.map((id) => () => this.readEntity(id, since)));
          }
        }
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
    const generation = this.generation;
    const changed = this.watch();
    let body: Revised | undefined;
    try {
      body = await readOne(this.projectId, owner);
    } catch (error) {
      this.options.log.record("request", error);
      return;
    } finally {
      this.watchers.delete(changed);
    }
    if (this.generation !== generation) {
      // The project read anew meanwhile read the entity with its part.
      return;
    }
    if (owner === this.projectId) {
      if (body === undefined) {
        this.goneAway();
        return;
      }
      body = projectOf(body as Project);
    }
    const before = this.confirmed.project;
    this.took(mergeEntity(this.confirmed, owner, body, since, changed));
    this.projectFollowed(before);
    this.show();
  }

  private async readFolders(
    part: "tree" | "variables" | "components",
    since: number,
  ): Promise<void> {
    const generation = this.generation;
    const changed = this.watch();
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
      // The project read anew meanwhile read the folders with their part.
      if (this.generation === generation) {
        this.took(
          mergeList(this.confirmed, "folders", part, items, since, changed),
        );
        this.show();
      }
    } catch (error) {
      this.options.log.record("request", error);
    } finally {
      this.watchers.delete(changed);
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

  /**
   * Sends the latest step of the history to undo or to redo. One asked for
   * while another is under way, as by a second click before the first has
   * disabled the button, is that one, and settles as it does, so that what
   * the page says of it is what became of it.
   */
  private step(which: "undo" | "redo"): Promise<void> {
    if (this.stepUnderWay !== undefined) {
      return this.stepUnderWay;
    }
    const newest = () =>
      which === "undo" ? this.history.nextUndo() : this.history.nextRedo();
    const first = newest();
    if (first === undefined) {
      return Promise.resolve();
    }
    const underWay = this.stepped(which, first, newest).finally(() => {
      if (this.stepUnderWay === underWay) {
        this.stepUnderWay = undefined;
      }
    });
    this.stepUnderWay = underWay;
    return underWay;
  }

  /** Sends first, the latest step to undo or to redo, which newest returns, or the one that is the latest once the document has the operations before it. */
  private async stepped(
    which: "undo" | "redo",
    first: Step,
    newest: () => Step | undefined,
  ): Promise<void> {
    let step = first;
    this.store.setState({ stepping: true });
    try {
      // What the step expects is moved from what the document held before
      // the batches that it undoes, which it knows once it has them. A
      // batch answered meanwhile, as a form's save, changes the history, of
      // which the step sent is the latest then, if any, as a new change
      // leaves nothing to redo.
      while (this.store.getState().live) {
        await this.whenConfirmed(Math.max(...step.seqs), confirmWait);
        const now = newest();
        if (now === undefined) {
          return;
        }
        const same = now.seqs[0] === step.seqs[0];
        step = now;
        if (same) {
          break;
        }
      }
      const before = this.history.before(step);
      let applied: Sent[] = [];
      let rest: UndoCommandBody[] = [];
      let failure: unknown;
      try {
        applied = await this.sendInParts(step.commands);
      } catch (error) {
        if (error instanceof PartsError) {
          applied = error.applied;
          rest = error.rest;
          failure = error.cause;
        } else {
          failure = error;
        }
      }
      if (applied.length > 0) {
        const answers = applied.map((s) => s.answer);
        const latest = latestRevisions(answers);
        const moves: [string, number, number][] = [];
        for (const [id, rev] of latest) {
          const was = before.get(id);
          if (was !== undefined && was !== null) {
            moves.push([id, was, rev]);
          }
        }
        this.history.move(moves);
        const undos = answers.map((a) => a.undo);
        const next: Step | undefined =
          undos.every((u) => u !== null) && undos.some((u) => u.length > 0)
            ? {
                commands: joinedUndo(undos as UndoCommandBody[][], latest),
                seqs: answers.map((a) => a.seq),
              }
            : undefined;
        // A step sent in parts that no answer came to the rest of, which may
        // apply once sent again, keeps the rest, each command expecting what
        // the parts that applied left, from which it moves what the other
        // steps expect once it applies, as they were moved there; one that
        // the server refused, as for what changed since, would be refused
        // again (FR-EDIT-08).
        const left: Step | undefined =
          rest.length > 0 && !refusedForGood(failure)
            ? { commands: rest, seqs: step.seqs }
            : undefined;
        if (which === "undo") {
          this.history.undone(step, next, left);
        } else {
          this.history.redone(step, next, left);
        }
        if (left !== undefined) {
          this.history.advance(left, latest);
        }
        for (const s of applied) {
          if (s.captured !== undefined && this.history.wants(s.answer.seq)) {
            this.history.capture(s.answer.seq, s.captured);
          }
        }
      } else if (refusedForGood(failure)) {
        // A step that the server refused, as for what changed since, would
        // be refused again; one refused for when it was sent, as with too
        // many requests, may apply later.
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
    let first: Sent[];
    try {
      first = await this.sendInParts(commands.slice(0, half));
    } catch (error) {
      if (!(error instanceof PartsError)) {
        throw error;
      }
      // Part of the first half applied: the rest of it, and the second
      // half, expect what that part left.
      const latest = latestRevisions(error.applied.map((s) => s.answer));
      throw new PartsError(error.applied, error.cause, [
        ...error.rest,
        ...commands.slice(half).map((c) => withRevisions(c, latest)),
      ]);
    }
    const latest = latestRevisions(first.map((s) => s.answer));
    const second = commands.slice(half).map((c) => withRevisions(c, latest));
    try {
      return [...first, ...(await this.sendInParts(second))];
    } catch (error) {
      throw error instanceof PartsError
        ? new PartsError([...first, ...error.applied], error.cause, error.rest)
        : new PartsError(first, error, second);
    }
  }

  /** Queues a batch, which the page shows at once, and resolves with its answer. */
  private enqueue(
    commands: readonly Command[],
    retries: number,
  ): Promise<Sent> {
    const key = JSON.stringify(commands);
    // A batch the same as one that is queued or under way, as once a change
    // goes back and forth, is another batch, with IDs of its own, which the
    // server would otherwise answer as that one, applying nothing.
    const again = this.entries.some((e) => e.key === key)
      ? undefined
      : this.unanswered.get(key);
    const ids = again ?? commands.map(() => formatUuid(newUuid()));
    const withIds = commands.map(
      (command, i) => ({ ...command, command_id: ids[i] }) as CommandBody,
    );
    const view = this.store.getState().view;
    const base = new Map<string, Base>();
    for (const command of withIds) {
      for (const [id, rev] of Object.entries(command.expected_rev ?? {})) {
        base.set(id, this.baseOf(view, canonical(id), rev));
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

  /**
   * Returns what the page showed of the entity of id that a batch expects at
   * rev: as the view shows it, at rev, or, once operations have changed it
   * since, as a form that holds what it showed does, as the document held it
   * at rev, if it still knows, or else as the view shows it, which is not
   * exact (Base).
   */
  private baseOf(view: Doc, id: string, rev: number): Base {
    const shown = entityOf(view, id);
    if (shown === undefined || shown.rev === rev) {
      return { entity: shown, exact: true };
    }
    const held = this.earlier.get(`${id}@${rev}`);
    return held === undefined
      ? { entity: shown, exact: false }
      : { entity: held, exact: true };
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
      this.catchUp();
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
      } else {
        // A batch that no answer came to may have applied all the same: one
        // sent again as it was has the IDs that its commands had, which the
        // server answers as the batch that it applied, if it did.
        this.unanswered.set(
          entry.key,
          entry.commands.map((c) => c.command_id),
        );
        if (!this.store.getState().live) {
          // The channel, while it is not live, would not bring it: the
          // parts that it changes are read again, so that the page shows
          // it, as a board that a dialog created, rather than offer to make
          // it anew, with IDs of its own, which would make another.
          for (const part of entry.parts) {
            void this.reread(part);
          }
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
    // The document is its account's (Documents): a batch queued while the
    // page signed in to another account, whose cookie and CSRF token the
    // request would carry, is never sent as that account's.
    if (this.userId !== undefined && session?.user_id !== this.userId) {
      throw new Error(
        "The page is signed in to another account than the batch's.",
      );
    }
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
          // A batch made on top of this one, after it, expects what this
          // one is moved to, from which its answer moves it on.
          if (queued.before.get(id) === from) {
            queued.before.set(id, r.rev);
          }
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
    // What the page showed before the batch moves with it, so that its
    // answer moves the batches made on top of it as it is now, which expect
    // the current revision, and not those made on top of it before, which
    // expect what the page showed before what changed elsewhere, and are
    // refused for it in turn, and compared with it, rather than written over
    // it unasked (moveQueued).
    for (const c of conflicts) {
      const id = canonical(c.id);
      if (entry.before.get(id) === c.expected_rev) {
        entry.before.set(id, c.current_rev as number);
      }
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
    const before = this.confirmed.project;
    let doc = this.confirmed;
    for (const [id, state] of Object.entries(states)) {
      if (state.deleted_at === undefined && typeof state.rev === "number") {
        const entity =
          placeOf(id)?.of === "project"
            ? projectOf(state as unknown as Project)
            : (state as unknown as Revised);
        const held = entityOf(doc, canonical(id));
        if (held !== undefined && held.rev < entity.rev) {
          this.remember(canonical(id), held);
        }
        doc = mergeEntity(doc, canonical(id), entity, doc.seq);
      }
    }
    this.confirmed = doc;
    this.projectFollowed(before);
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
      const shown = entry.base.get(c.id);
      const base = shown?.entity as Record<string, unknown> | undefined;
      if (shown === undefined || base === undefined) {
        overlaps.push({ id, state, fields: [], gone: false });
        continue;
      }
      const mine = this.written(entry, id, base);
      const unread = unreadFields(id, base);
      const fields = [];
      for (const field of new Set([
        ...Object.keys(base),
        ...Object.keys(mine ?? {}),
      ])) {
        if (meta.has(field) || unread.has(field)) {
          continue;
        }
        const value = mine?.[field];
        // Where the document no longer knows what the page showed at the
        // revision that the batch expects, a field that the batch changes,
        // which has another value now than the batch's, may have changed
        // elsewhere since, and is not written over unasked.
        const elsewhere = shown.exact
          ? !equal(state[field], base[field])
          : !equal(state[field], value);
        if (!equal(value, base[field]) && elsewhere) {
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

/**
 * Returns the fields of the entity of id that the document held of it, as
 * base, without having read them, as a component's description and values,
 * which the list of the components leaves out: what changed of them
 * elsewhere it cannot tell, so a conflict does not compare them, rather than
 * take every value that the server has for one that changed.
 */
function unreadFields(id: string, base: Record<string, unknown>): Set<string> {
  const place = placeOf(id);
  return place?.of === "kind" &&
    place.kind === "components" &&
    base.attributes === undefined
    ? new Set(["attributes", "description"])
    : new Set();
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

/**
 * Runs tasks, at most n at a time, and rejects as the first that fails
 * does, after which it starts none of those left, whose caller has failed.
 */
async function inTurns(
  tasks: readonly (() => Promise<void>)[],
  n: number,
): Promise<void> {
  let next = 0;
  let failed = false;
  const run = async () => {
    while (next < tasks.length && !failed) {
      try {
        await tasks[next++]();
      } catch (error) {
        failed = true;
        throw error;
      }
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
    if (notFound(error)) {
      return undefined;
    }
    throw error;
  }
}

/** Reports whether error is the server's answer that it has no such thing for the user (404). */
function notFound(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 404 &&
    error.error?.code === "not_found"
  );
}
