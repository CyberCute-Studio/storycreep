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
// of them, as by a dialog that opened before they were answered. A batch
// refused for a revision that changed elsewhere since is sent again,
// expecting the current revision, where what changed elsewhere is none of
// what the batch changes, field by field; where it is, the refusal names
// each field with both values (ConflictError), and nothing is changed over
// (§7.10 item 1). The answer to a batch holds its undo, which the history
// keeps (undo.ts), and which undo and redo send back, once the page's
// batches made before they were asked for have joined the history, and
// before those made after, expecting what the server left, which no answer
// to a batch before them moves.

import {
  ApiError,
  type BranchOutput,
  type CommandBody,
  type CommandResultBody,
  type ProjectBody,
  type UndoCommandBody,
} from "@storycreep/shared/api";
import { formatUuid, newUuid } from "@storycreep/shared/ids";
import type { OperationMessage } from "@storycreep/shared/realtime";
import type { QueryClient } from "@tanstack/react-query";
import { createStore, type StoreApi } from "zustand/vanilla";
import {
  allAtOnce,
  api,
  everyPage,
  notFoundBy,
  sessionDoubtedBy,
  sessionQuery,
  signedOutBy,
} from "../api";
import { csrfHeaders } from "../auth/session";
import type { ErrorLog } from "../errors/log";
import { conflictOf, projectQuery } from "../project/queries";
import { applyCommands, canonical } from "./apply";
import {
  type Doc,
  Draft,
  type Entities,
  emptyDoc,
  type Gone,
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
  entitiesOf,
  entityOf,
  equal,
  mergeEntity,
  mergeList,
  nestedFieldOf,
  nestedOf,
  type Operation,
  ownersFirst,
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
  /**
   * Whether the batch is a step of the history, whose commands expect what
   * the server left of the entities that the batch that it undoes or redoes
   * changed, rather than what the page showed, so that the answers to the
   * batches before it never move them past those batches (moveQueued,
   * ownMoves): an undo that would write over what they changed is refused
   * (FR-EDIT-08 AC4).
   */
  step: boolean;
  sent: boolean;
  answer?: CommandResultBody;
  /** What the document held of the entities that the batch changed, before its operation applied. */
  captured?: Captured;
  /**
   * The view that the batch's commands, as they were, and the revisions of
   * its answer, as it was, left from, which the page showed last, so that
   * the view is made anew from what changed alone (show).
   */
  shown?: {
    from: Doc;
    commands: CommandBody[];
    revisions: CommandResultBody["revisions"] | undefined;
    view: Doc;
  };
  resolve: (answer: CommandResultBody) => void;
  reject: (error: unknown) => void;
}

interface Revised {
  id: string;
  rev: number;
}

/**
 * A read from the server under way, of a part, of a part's folders, or of an
 * entity: the seq that the document was as of once it began, at the latest,
 * and each entity that operations changed meanwhile, with the latest
 * revision that one gave it.
 */
interface Watcher {
  since: number;
  changed: Map<string, number>;
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
  /**
   * Has the page ask for its session, once the answer to a read of the
   * document's tells that it may have ended (sessionDoubtedBy), as the app's
   * query client does of its queries' (createQueryClient), which drops the
   * document once the page has none.
   */
  checkSession?: () => void;
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
   * The reads from the server under way, each with the entities that
   * operations changed meanwhile, so that a list does not take out what an
   * operation put in since the read began, and what the read found of an
   * entity before an operation changed it is read again (readBehind), and
   * with the seq that it began at, before which what went from the document
   * is forgotten once no read began before it (forgetGone).
   */
  private readonly watchers = new Set<Watcher>();
  /** How many reads of parts are under way. */
  private partReads = 0;
  private started = false;
  /** The reading of the project that the document follows it from, under way, or done, or failed (ready). */
  private starting: Promise<void>;
  /** Whether that reading failed, so that the next that waits for it reads the project again (whenReady). */
  private startFailed = false;
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
  /** The parts being read again, each with whether it is to be read once more after (reread). */
  private readonly rereads = new Map<
    Part,
    { again: boolean; done: Promise<void> }
  >();
  /** The IDs of the commands of each batch that no answer came to, by its key, which a batch sent again as it was takes (enqueue). */
  private readonly unanswered = new Map<string, string[]>();
  private readonly waiters: { seq: number; resolve: () => void }[] = [];
  private readonly history: History;
  /** The account that the document is of, as the page's session was once it was made, whose batches it sends as no other's (post). */
  private readonly userId: string | undefined;
  /** The undo or the redo under way, which another of the same asked for meanwhile settles as, and one of the other follows (step). */
  private stepUnderWay:
    | { which: "undo" | "redo"; done: Promise<boolean> }
    | undefined;
  /**
   * The page's batches that send has yet to settle, those that wait for a
   * step of the history under way too (send), which the document is busy
   * with until they have.
   */
  private readonly sends = new Set<Promise<CommandResultBody>>();
  /**
   * The page's batches that send has sent, or queued, and that have yet to
   * settle, each with what its answer adds to the history, which an undo or
   * a redo asked for meanwhile waits for before it is sent, so that it sends
   * the step that is the latest once they have joined it (stepped).
   */
  private readonly batches = new Set<Promise<CommandResultBody>>();
  /**
   * The revision that each of the page's batches that applied moved an
   * entity to, by the entity's ID and the revision that the batch expected
   * of it, which the server had as the batch applied, the latest
   * (earlierKept): a batch made on top of the page's own, as by a dialog
   * that opened before the answer to it came, which holds the revision that
   * it opened with, expects what that batch moved it to, rather than be
   * refused for the page's own change, as for one made elsewhere (enqueue).
   */
  private readonly ownMoves = new Map<string, number>();
  /** Reads the project anew now, once a read of it failed and waits to try again (reload). */
  private retryNow: (() => void) | undefined;
  /** What the latest reading anew of the project failed with, or undefined once one did not fail (refresh). */
  private resyncFailure: unknown;
  /** The seq that the document's subscription held as of, up to which the channel sends the operations that it missed first (refresh). */
  private subscribedTo = 0;
  /**
   * The times that the page gave the operations of its batches that the
   * document took from their answers, by seq, while the channel was live,
   * which the server's own replace once the channel brings the operations
   * (retime).
   */
  private readonly answeredAt = new Map<number, string>();
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
    this.starting = this.begin();
  }

  /** Settles once the project has been read, and the document follows it. */
  get ready(): Promise<void> {
    return this.starting;
  }

  /** The document as the server has confirmed it. */
  get doc(): Doc {
    return this.confirmed;
  }

  /** Reads the project, as start does, and records once it has, or has failed to. */
  private begin(): Promise<void> {
    const starting = this.start();
    // A page that waits for the project shows what refuses it; nothing else
    // needs to hear of it here.
    starting.then(
      () => {
        this.started = true;
      },
      () => {
        this.started = true;
        this.startFailed = true;
      },
    );
    return starting;
  }

  /**
   * Resolves once the project has been read, as ready does, reading it again
   * once its read failed, as of a request that the browser's connection
   * lost, so that a page that asks again, as its loader does once the user
   * tries again, or a batch, does not fail as the first read did for as
   * long as the document is kept.
   */
  private whenReady(): Promise<void> {
    if (this.startFailed && !this.disposed) {
      this.startFailed = false;
      this.started = false;
      this.starting = this.begin();
    }
    return this.starting;
  }

  /**
   * Reports whether the document is reading the project, or a part of it,
   * which a route's loader may be waiting for, or sending the page's batches,
   * or a step of the history, whose answers join the history, which a
   * document dropped keeps no longer in the tab's storage (dispose).
   */
  get busy(): boolean {
    return (
      !this.started ||
      this.partReads > 0 ||
      this.sends.size > 0 ||
      this.stepUnderWay !== undefined
    );
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
      subscribed: (seq, since) => {
        this.subscribedTo = seq;
        // A project whose seq is behind what the subscription was sent from,
        // as of a database restored from a backup older than the page, has
        // other operations after it than those that the document has. One
        // that the document's own batches have moved past since it was sent,
        // whose answers came before it, is not behind.
        if (seq < since) {
          // The revisions that the page's own batches moved its entities
          // from and to are the project's from before it was restored.
          this.ownMoves.clear();
          void this.resync(seq);
        }
      },
      operation: (message) => this.receive(operationOf(message)),
      resync: (seq) => void this.resync(seq),
      gone: () => this.goneAway(),
      live: (live) => {
        // The operations up to the seq that the subscription is sent from,
        // once the socket is back, are not sent again.
        if (!live) {
          this.answeredAt.clear();
        }
        this.store.setState({ live });
      },
    });
    for (const op of buffered) {
      this.receive(op);
    }
    // An undo that waits for the document to have the operations up to the
    // batch that it undoes, as of a history kept over a reload, has them.
    this.settle();
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
    // it out already, and a page has asked for it anew since, or the project
    // read anew since the read began has read it with the others, which the
    // document holds as that read found it.
    let load: Promise<void> | undefined;
    let generation: number | undefined;
    load = (async () => {
      const watcher = this.watch(this.confirmed.seq);
      try {
        await this.whenReady();
        const since = this.confirmed.seq;
        watcher.since = since;
        generation = this.generation;
        this.confirmed = withParts(this.confirmed, [part], []);
        const lists = await readPart(this.projectId, part);
        // The project read anew meanwhile read the part with the others.
        if (this.generation === generation) {
          let doc = this.confirmed;
          for (const list of lists) {
            doc = mergeList(
              doc,
              list.kind,
              part,
              list.items,
              since,
              watcher.changed,
            );
          }
          this.took(doc);
          this.readBehind(watcher);
        }
        this.show();
      } catch (error) {
        this.failed(error, false);
        if (
          this.loads.get(part) === load &&
          generation !== undefined &&
          this.generation !== generation &&
          this.confirmed.parts.has(part)
        ) {
          return;
        }
        if (this.loads.get(part) === load) {
          this.loads.delete(part);
          this.confirmed = withParts(this.confirmed, [], [part]);
          this.show();
        }
        throw error;
      } finally {
        this.watchers.delete(watcher);
        this.partReads--;
      }
    })();
    this.loads.set(part, load);
    return load;
  }

  /**
   * Reads the project anew while the real-time channel is not live, as the
   * document may have missed what changed since, as a board that another
   * session created, and resolves once it has, or rejects with what it
   * failed with, as it tries again later, so that a page does not take the
   * document as it was for what the project has, and say that a board that
   * another session created is none. While the channel is live, it resolves
   * once the document has the operations up to the seq that its
   * subscription held as of, which the channel sends first, once the socket
   * is back, or once the reading anew under way has read the project, as
   * after resync_required; and it reads the project anew if those
   * operations do not come in a while.
   */
  async refresh(): Promise<void> {
    if (this.store.getState().live && this.buffer === undefined) {
      await this.whenConfirmed(this.subscribedTo, confirmWait);
      if (this.confirmed.seq >= this.subscribedTo || this.disposed) {
        return;
      }
    }
    await this.resync(this.confirmed.seq);
    if (this.resyncFailure !== undefined) {
      throw this.resyncFailure;
    }
  }

  /** Returns a watcher of the operations that apply from now on, of a read that began at since, until the caller takes it out of watchers. */
  private watch(since: number): Watcher {
    const watcher: Watcher = { since, changed: new Map() };
    this.watchers.add(watcher);
    return watcher;
  }

  /**
   * Has each entity that operations changed while watcher's read was under
   * way read from the server again, where the document holds it, as the
   * read brought it, at a revision before the one that an operation gave
   * it: one that the document did not hold as the operation applied, whose
   * appliers could not change it, as a board that another session took to
   * the trash while the tree was read for the first time, whose list, which
   * the server answered before, still has it.
   */
  private readBehind(watcher: Watcher): void {
    const held = entitiesOf(this.confirmed, watcher.changed.keys());
    for (const [id, rev] of watcher.changed) {
      const entity = held.get(id);
      if (entity !== undefined && entity.rev < rev) {
        this.toRead.add(id);
      }
    }
    this.readLater();
  }

  /**
   * Forgets what went from the document (Doc.gone) before every read under
   * way began, which only the answer to a read that began before it went
   * could bring back, so that a long session of a busy project does not
   * keep every entity that it saw go, and copy them all with each that goes.
   */
  private forgetGone(): void {
    const gone = this.confirmed.gone;
    if (gone.size === 0) {
      return;
    }
    let floor = this.confirmed.seq;
    for (const watcher of this.watchers) {
      floor = Math.min(floor, watcher.since);
    }
    let kept: Map<string, Gone> | undefined;
    for (const [id, went] of gone) {
      if (went.seq <= floor) {
        kept ??= new Map(gone);
        kept.delete(id);
      }
    }
    if (kept !== undefined) {
      this.confirmed = { ...this.confirmed, gone: kept };
    }
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
      this.retime(op);
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
    // What the document holds of what the operation changed, which
    // applyOperation finds alike, as it was before the operation.
    const held = entitiesOf(
      this.confirmed,
      op.revisions.map((r) => canonical(r.id)),
    );
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
        const rev = held.get(id)?.rev;
        const sent = expected.get(id);
        if (sent !== undefined) {
          captured[id] = sent;
        } else if (rev === undefined) {
          captured[id] = null;
        } else if (rev < r.rev) {
          captured[id] = rev;
        }
      }
    }
    for (const r of op.revisions) {
      const id = canonical(r.id);
      const was = held.get(id);
      if (was !== undefined && was.rev < r.rev) {
        this.remember(id, was);
      }
    }
    const reconciled = applyOperation(this.confirmed, op, held);
    for (const watcher of this.watchers) {
      for (const r of op.revisions) {
        const id = canonical(r.id);
        watcher.changed.set(id, Math.max(r.rev, watcher.changed.get(id) ?? 0));
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
   * Gives what the page's own batch changed, which the document took from
   * the batch's answer, as of the time that the page gave it, which the
   * answer does not say, the time of the batch's operation, which the
   * channel brings after, where the document holds it still as the
   * operation left it, at its revision: each of its timestamps (…_at) that
   * the page gave it, which its appliers wrote, as a create's created_at.
   */
  private retime(op: Operation): void {
    const time = this.answeredAt.get(op.seq);
    if (time === undefined) {
      return;
    }
    this.answeredAt.delete(op.seq);
    if (time === op.time) {
      return;
    }
    const revs = new Map(op.revisions.map((r) => [canonical(r.id), r.rev]));
    const held = entitiesOf(this.confirmed, revs.keys());
    const before = this.confirmed.project;
    let doc = this.confirmed;
    // An owner, as a node, is set whole, so its nested entities that the
    // operation changed are set after it, into it.
    for (const id of ownersFirst(revs.keys())) {
      const entity = held.get(id) as unknown as Record<string, unknown>;
      if (entity?.rev !== revs.get(id)) {
        continue;
      }
      let next: Record<string, unknown> | undefined;
      for (const [field, value] of Object.entries(entity)) {
        if (field.endsWith("_at") && value === time) {
          next ??= { ...entity };
          next[field] = op.time;
        }
      }
      if (next !== undefined) {
        doc = withEntity(doc, id, next as unknown as Revised);
      }
    }
    this.confirmed = doc;
    this.projectFollowed(before);
  }

  /**
   * Tells the page's other reads of the project what an operation changed,
   * or, for none, what the project read anew may have, with the role that
   * it read: the project itself, which the project's pages read with its
   * role, as the document has it, without what the operation took out of
   * it, as an unarchive's archived_at, and the trash, which a deletion or a
   * restoration changes.
   */
  private followed(
    op: Operation | undefined,
    before: Project | undefined,
    role?: ProjectBody["role"],
    readAt?: number,
  ): void {
    this.projectFollowed(before, role, readAt);
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
   * is (applyOperation), with the role that a read of it gave, as once the
   * user's changed while the channel was not live, or else the role that
   * the project's read has. A read of the project's that is as of a later
   * seq than the document, as one that a page made once the document began
   * from what an earlier one read, whose operations since the channel
   * brings it now, is kept, rather than taken back to what they changed,
   * but for one older than the document's own read of the project, at
   * readAt, as the project read anew, which is what the server has now,
   * though at an earlier seq, as of a database restored from a backup.
   */
  private projectFollowed(
    before: Project | undefined,
    role?: ProjectBody["role"],
    readAt?: number,
  ): void {
    const project = this.confirmed.project;
    if (project !== undefined && project !== before) {
      const key = projectQuery(this.projectId).queryKey;
      const older =
        readAt !== undefined &&
        (this.options.queryClient.getQueryState(key)?.dataUpdatedAt ?? 0) <=
          readAt;
      this.options.queryClient.setQueryData(key, (body) =>
        body === undefined || (body.seq > this.confirmed.seq && !older)
          ? body
          : {
              ...project,
              role: role ?? body.role,
              seq: this.confirmed.seq,
            },
      );
    }
  }

  /**
   * Takes doc, which reads from the server have brought, for what the
   * server confirmed, keeping what the document held of each entity that
   * doc holds at a later revision (earlier), as a form may show it still:
   * of those of only, where given, as the one that a read of an entity
   * brought, rather than of every entity of its kind.
   */
  private took(doc: Doc, only?: readonly string[]): void {
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
      const field = nestedFieldOf(kind);
      const all = was[kind] as ReadonlyMap<string, Revised>;
      const ids =
        only === undefined ? all.keys() : only.filter((id) => all.has(id));
      for (const id of ids) {
        const held = all.get(id) as Revised;
        const now = (doc[kind] as ReadonlyMap<string, Revised>).get(id);
        if (now === undefined || now === held) {
          continue;
        }
        if (now.rev > held.rev) {
          this.remember(id, held);
        }
        if (field !== undefined) {
          const later = new Map(nestedOf(now, field).map((c) => [c.id, c]));
          for (const child of nestedOf(held, field)) {
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
    this.forgetGone();
    this.show();
  }

  /**
   * Shows the page the document: what the server confirmed, with the page's
   * batches on top, each that applied at the revisions that its answer gave.
   * One answered with already_applied is not: its answer gives the
   * revisions that the entities have now, which may be past what other
   * sessions changed since, which the document does not show yet, and which
   * a batch made on top of it would otherwise expect, and write over unasked.
   * A batch is applied anew only once what it applies on top of, its
   * commands, or its answer has changed since it was shown last, as each
   * copies what it changes of the document's maps (Entry.shown).
   */
  private show(): void {
    let view = this.confirmed;
    for (const entry of this.entries) {
      const revisions =
        entry.answer?.status === "applied" ? entry.answer.revisions : undefined;
      const last = entry.shown;
      if (
        last !== undefined &&
        last.from === view &&
        last.commands === entry.commands &&
        last.revisions === revisions
      ) {
        view = last.view;
        continue;
      }
      const from = view;
      view = applyPending(view, entry.commands, entry.time, revisions);
      entry.shown = { from, commands: entry.commands, revisions, view };
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
    let body: ProjectBody;
    this.resyncFailure = undefined;
    const readAt = Date.now();
    try {
      body = (
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
              if (part.startsWith("board:") && notFoundBy(error)) {
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
      if (notFoundBy(error)) {
        // The project can no longer be read.
        this.buffer = undefined;
        this.goneAway();
        return;
      }
      this.failed(error);
      this.resyncFailure = error;
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
    // What the project read anew has, it has with the server's times.
    this.answeredAt.clear();
    this.followed(undefined, before, body.role, readAt);
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

  /**
   * Reads part again, as once entities of it came back without revisions of
   * their own. A part being read again is read once more after that read,
   * however many ask meanwhile, as that read may have begun before what
   * they ask for, rather than once for each, as for the boards that each
   * node restored by an undo, and a redo, and an undo again, has read again.
   */
  private reread(part: Part): Promise<void> {
    const under = this.rereads.get(part);
    if (under !== undefined) {
      under.again = true;
      return under.done;
    }
    const reading = { again: false, done: Promise.resolve() };
    reading.done = (async () => {
      try {
        do {
          reading.again = false;
          await this.rereadOnce(part);
        } while (reading.again && !this.disposed);
      } finally {
        this.rereads.delete(part);
      }
    })();
    this.rereads.set(part, reading);
    return reading.done;
  }

  /** Reads part again, as reread does, once. */
  private async rereadOnce(part: Part): Promise<void> {
    if (this.disposed) {
      return;
    }
    const since = this.confirmed.seq;
    const generation = this.generation;
    const watcher = this.watch(since);
    try {
      const lists = await readPart(this.projectId, part);
      let doc = this.confirmed;
      if (doc.parts.has(part) && this.generation === generation) {
        for (const list of lists) {
          doc = mergeList(
            doc,
            list.kind,
            part,
            list.items,
            since,
            watcher.changed,
          );
        }
        this.took(doc);
        this.readBehind(watcher);
        this.show();
      }
    } catch (error) {
      this.readFailed(error, generation);
    } finally {
      this.watchers.delete(watcher);
    }
  }

  /**
   * Records a read that failed of what the document read from the server
   * as its appliers could not know it, whose prediction has the operation's
   * revision, which no read or operation of that revision corrects after,
   * and reads the project anew, which tries again until it can (resync),
   * unless the project has been read anew since the read began.
   */
  private readFailed(error: unknown, generation: number): void {
    this.failed(error);
    // One under way, or waiting to try again, reads it all, at its pace.
    if (this.generation === generation && this.buffer === undefined) {
      void this.resync(this.confirmed.seq);
    }
  }

  /**
   * Records a read of the document's that failed, as the app's query client
   * records a query's (createQueryClient): one whose answer tells that the
   * page's session may have ended has the page ask for its session, which
   * drops the document once it has none, and is not recorded where the
   * session has ended (signedOutBy), which the page shows by signing out;
   * one that a page shows, as a loader's read of a part, is not recorded
   * here (record).
   */
  private failed(error: unknown, record = true): void {
    if (sessionDoubtedBy(error)) {
      this.options.checkSession?.();
    }
    if (record && !signedOutBy(error)) {
      this.options.log.record("request", error);
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
        // A folder is read with its part's folders: the part of its kind, as
        // the document holds it, or, of one that it does not hold, as one
        // that came back from the trash, whose ID does not tell its kind,
        // each that the document holds.
        const folders = new Set<FolderPart>();
        for (const id of ids) {
          if (placeOf(id)?.of !== "kind" || !id.startsWith("fld_")) {
            continue;
          }
          const held = this.confirmed.folders.get(id);
          for (const part of held === undefined
            ? folderParts
            : [partOf("folders", held) as FolderPart]) {
            if (this.confirmed.parts.has(part)) {
              folders.add(part);
            }
          }
        }
        const entities = ids.filter((id) => !id.startsWith("fld_"));
        const held = entitiesOf(this.confirmed, entities);
        const byPart = new Map<Part | undefined, string[]>();
        const rereads = new Set<Part>();
        for (const id of entities) {
          if (placeOf(id)?.of === "attribute" && held.get(id) === undefined) {
            // An attribute definition that no type that the document holds
            // has, as one that came back from the trash, has no read of its
            // own, and is of a type that the types' list answers with it,
            // which is read again, once for them all.
            if (this.confirmed.parts.has("components")) {
              rereads.add("components");
            }
            continue;
          }
          const part = partOfHeld(this.confirmed, id, held.get(id));
          const of = byPart.get(part);
          if (of === undefined) {
            byPart.set(part, [id]);
          } else {
            of.push(id);
          }
        }
        // A part of which there are many to read is read again whole, in a
        // few pages of its lists, rather than each of them by its own read.
        for (const [part, of] of byPart) {
          if (part !== undefined && of.length > readsOfOnePart) {
            rereads.add(part);
          }
        }
        const tasks: (() => Promise<void>)[] = [];
        for (const [part, of] of byPart) {
          if (part === undefined || !rereads.has(part)) {
            tasks.push(...of.map((id) => () => this.readEntity(id, since)));
          }
        }
        for (const part of rereads) {
          tasks.push(() => this.reread(part));
        }
        for (const part of folders) {
          tasks.push(() => this.readFolders(part, since));
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
    if (place === undefined) {
      // One of no place of the document's, as a workspace, which a refusal
      // may name, is none that it holds, or reads.
      return;
    }
    let owner = id;
    if (place.of === "output") {
      const node = nodeOfOutput(this.confirmed.nodes, id);
      if (node === undefined) {
        return;
      }
      owner = node.id;
    } else if (place.of === "attribute") {
      // One that no type that the document holds has is read with the
      // types' list (readAll).
      const type = typeOfAttribute(this.confirmed.componentTypes, id);
      if (type === undefined) {
        return;
      }
      owner = type.id;
    }
    const generation = this.generation;
    const watcher = this.watch(since);
    let body: Revised | undefined;
    try {
      body = await readOne(this.projectId, owner);
    } catch (error) {
      this.readFailed(error, generation);
      return;
    } finally {
      this.watchers.delete(watcher);
    }
    if (this.generation !== generation) {
      // The project read anew meanwhile read the entity with its part.
      return;
    }
    let role: ProjectBody["role"] | undefined;
    if (owner === this.projectId) {
      if (body === undefined) {
        this.goneAway();
        return;
      }
      role = (body as ProjectBody).role;
      body = projectOf(body as Project);
    }
    const before = this.confirmed.project;
    this.took(
      mergeEntity(this.confirmed, owner, body, since, watcher.changed),
      [owner],
    );
    this.readBehind(watcher);
    this.projectFollowed(before, role);
    this.show();
  }

  private async readFolders(part: FolderPart, since: number): Promise<void> {
    const generation = this.generation;
    const watcher = this.watch(since);
    try {
      const items = await listFolders(
        this.projectId,
        part === "tree"
          ? "board"
          : part === "variables"
            ? "variable"
            : "component",
      );
      // The project read anew meanwhile read the folders with their part.
      if (this.generation === generation) {
        this.took(
          mergeList(
            this.confirmed,
            "folders",
            part,
            items,
            since,
            watcher.changed,
          ),
        );
        this.readBehind(watcher);
        this.show();
      }
    } catch (error) {
      this.readFailed(error, generation);
    } finally {
      this.watchers.delete(watcher);
    }
  }

  /**
   * Applies a batch of commands to the project: at once to what the page
   * shows, and then on the server, after the page's batches before it,
   * resolving with the server's answer, or rejecting with its refusal, once
   * the page shows the project without the batch again. A batch sent again
   * as it was, as once no answer came to it, has the IDs that its commands
   * had, so that the server answers it as the batch that it applied, if it
   * did, rather than apply it again. What the batch expects of an entity
   * is moved past the page's own batches that applied on top of that
   * revision, which are no change made elsewhere (ownMoves). The batch's
   * undo joins the history.
   */
  send(commands: readonly Command[]): Promise<CommandResultBody> {
    const sending = this.sendAfterSteps(commands);
    this.sends.add(sending);
    const settled = () => {
      this.sends.delete(sending);
    };
    sending.then(settled, settled);
    return sending;
  }

  /**
   * Sends a batch as sendBatch does, once no step of the history is under
   * way: one made while an undo or a redo waits to be sent, as a form saved
   * while the undo waits for the channel's operations, was asked for after
   * it, and is sent after it, rather than before it, which would leave the
   * undo the latest change, the one that the user did not ask to undo, or
   * end what the redo would redo, or else be written over by it (stepped).
   * It is sent in the order that it was made in among the steps asked for
   * meanwhile, as they are among themselves (step).
   */
  private sendAfterSteps(
    commands: readonly Command[],
  ): Promise<CommandResultBody> {
    const underWay = this.stepUnderWay;
    if (underWay !== undefined) {
      const after = () => this.sendAfterSteps(commands);
      return underWay.done.then(after, after);
    }
    const batch = this.sendBatch(commands);
    this.batches.add(batch);
    const settled = () => {
      this.batches.delete(batch);
    };
    batch.then(settled, settled);
    return batch;
  }

  /** Sends a batch as send does, adding its undo to the history once it has applied. */
  private async sendBatch(
    commands: readonly Command[],
  ): Promise<CommandResultBody> {
    const sent = await this.enqueue(commands, conflictRetries, false);
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

  /**
   * Undoes the latest step of the history, resolving with whether there was
   * one, rejecting with the server's refusal, as of what changed since
   * (FR-EDIT-08 AC4).
   */
  undo(): Promise<boolean> {
    return this.step("undo");
  }

  /** Redoes the latest step that was undone, resolving with whether there was one, as undo does. */
  redo(): Promise<boolean> {
    return this.step("redo");
  }

  /**
   * Sends the latest step of the history to undo or to redo. One asked for
   * while another of the same is under way, as by a second click before the
   * first has disabled the button, is that one, and settles as it does, so
   * that what the page says of it is what became of it; one of the other is
   * sent once that one has settled, as what was asked for after it, as is a
   * batch of the page's made meanwhile (sendAfterSteps).
   */
  private step(which: "undo" | "redo"): Promise<boolean> {
    const underWay = this.stepUnderWay;
    if (underWay !== undefined) {
      if (underWay.which === which) {
        return underWay.done;
      }
      const after = () => this.step(which);
      return underWay.done.then(after, after);
    }
    const newest = () =>
      which === "undo" ? this.history.nextUndo() : this.history.nextRedo();
    const before = [...this.batches];
    if (newest() === undefined && before.length === 0) {
      return Promise.resolve(false);
    }
    const done = this.stepped(which, newest, before).finally(() => {
      if (this.stepUnderWay?.done === done) {
        this.stepUnderWay = undefined;
      }
    });
    this.stepUnderWay = { which, done };
    return done;
  }

  /**
   * Sends the latest step to undo or to redo, which newest returns, once the
   * page's batches made before it was asked for, earlier, have joined the
   * history, and the document has the operations before it, and resolves
   * with whether there was one.
   */
  private async stepped(
    which: "undo" | "redo",
    newest: () => Step | undefined,
    earlier: readonly Promise<CommandResultBody>[],
  ): Promise<boolean> {
    this.store.setState({ stepping: true });
    try {
      // The page's batches made before the step was asked for, queued or
      // under way, as a form's save just before it, join the history first,
      // so that the step sent is the latest once they have, rather than one
      // before them, which would write over what they changed, unasked, and
      // be refused for it, and dropped, with the steps before it, which
      // expect what it undoes. Those made after wait for it (sendAfterSteps).
      await Promise.allSettled(earlier);
      const step = newest();
      if (step === undefined) {
        return false;
      }
      // What the step expects is moved from what the document held before
      // the batches that it undoes, which it knows once it has them: while
      // the channel is live, which brings them, or while the project is read
      // anew, as once an answer was past what the document had while the
      // channel was not, which settles the batches that it read. Nothing
      // changes the history meanwhile, as the page's batches made since wait
      // for the step.
      if (this.store.getState().live || this.buffer !== undefined) {
        await this.whenConfirmed(Math.max(...step.seqs), confirmWait);
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
      return true;
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
      return [await this.enqueue(commands as unknown as Command[], 0, true)];
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

  /** Queues a batch, which the page shows at once, a step of the history or not (Entry.step), and resolves with its answer. */
  private enqueue(
    commands: readonly Command[],
    retries: number,
    step: boolean,
  ): Promise<Sent> {
    const key = JSON.stringify(commands);
    // A batch the same as one that is queued or under way, as once a change
    // goes back and forth, is another batch, with IDs of its own, which the
    // server would otherwise answer as that one, applying nothing.
    const again = this.entries.some((e) => e.key === key)
      ? undefined
      : this.unanswered.get(key);
    const ids = again ?? commands.map(() => formatUuid(newUuid()));
    // A batch of the page's, but for a step of the history, which expects
    // what the history moved it to, expects what the page's own batches that
    // applied on top of what it expects left (ownMoves), as one that was
    // queued behind them would (moveQueued).
    const withIds = commands.map((command, i) => {
      const body = { ...command, command_id: ids[i] } as CommandBody;
      return step ? body : this.pastOwn(body);
    });
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
    const was = entitiesOf(view, draft.changes.keys());
    const now = entitiesOf(after, draft.changes.keys());
    for (const id of draft.changes.keys()) {
      const shown = was.get(id);
      if (shown !== undefined) {
        before.set(id, shown.rev);
      }
      const part =
        partOfHeld(view, id, shown) ?? partOfHeld(after, id, now.get(id));
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
        step,
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
      await this.whenReady();
      const answer = await this.post(entry.commands);
      entry.answer = answer;
      // An answer of already_applied gives the revisions that the entities
      // have now, which may be past what other sessions changed since, over
      // which the batches made on top of it would be moved, and applied
      // unasked: they are sent as they are, and refused for what changed,
      // and compared with it, if anything did (retried).
      if (answer.status === "applied") {
        this.moveQueued(entry, answer);
        this.movedOwn(entry, answer);
      }
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
        // The channel brings the operation after, with the server's time.
        if (this.store.getState().live) {
          this.answeredAt.set(answer.seq, entry.time);
        }
      }
      this.unanswered.delete(entry.key);
      this.settle();
      this.catchUp();
      entry.resolve(answer);
    } catch (error) {
      if (conflictOf(error)) {
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
        conflictOf(error)
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
   * which they were made on top of, to the one that its answer gives, but
   * for the steps of the history, which were not (Entry.step).
   */
  private moveQueued(entry: Entry, answer: CommandResultBody): void {
    for (const r of answer.revisions ?? []) {
      const id = canonical(r.id);
      const from = entry.before.get(id);
      if (from === undefined) {
        continue;
      }
      for (const queued of this.entries) {
        if (queued.sent || queued === entry) {
          continue;
        }
        if (!queued.step) {
          queued.commands = queued.commands.map((c) =>
            moved(c, id, from, r.rev),
          );
        }
        // A batch made on top of this one, after it, expects what this one
        // is moved to, from which its answer moves it on.
        if (queued.before.get(id) === from) {
          queued.before.set(id, r.rev);
        }
      }
    }
  }

  /**
   * Records the revision that entry, which applied, moved each entity that
   * it expected to, from the one that it expected, which the server had as
   * it applied, so that nothing but entry changed the entity between the two
   * (ownMoves): what it changed without expecting it, as another session may
   * have changed it before, is not recorded.
   */
  private movedOwn(entry: Entry, answer: CommandResultBody): void {
    const revisions = new Map(
      (answer.revisions ?? []).map((r) => [canonical(r.id), r.rev]),
    );
    for (const [id, from] of expectedOf(entry.commands)) {
      const to = revisions.get(id);
      if (to === undefined || to === from) {
        continue;
      }
      this.ownMoves.set(`${id}@${from}`, to);
      if (this.ownMoves.size > earlierKept) {
        this.ownMoves.delete(this.ownMoves.keys().next().value as string);
      }
    }
  }

  /**
   * Returns command with what it expects of each entity moved past the
   * page's own batches that applied on top of that revision (ownMoves), to
   * the revision that the last of them left.
   */
  private pastOwn(command: CommandBody): CommandBody {
    let past = command;
    for (const [id, rev] of Object.entries(command.expected_rev ?? {})) {
      let to = rev;
      for (
        let next = this.ownMoves.get(`${canonical(id)}@${to}`);
        next !== undefined && next > to;
        next = this.ownMoves.get(`${canonical(id)}@${to}`)
      ) {
        to = next;
      }
      if (to !== rev) {
        past = moved(past, id, rev, to);
      }
    }
    return past;
  }

  /**
   * Sends entry again, once the server refused it for revisions that
   * changed elsewhere, where nothing that it changes did, field by field,
   * expecting the revisions that the entities have now, with new IDs, and
   * reports whether it did (PRD §7.10 item 1).
   */
  private retried(entry: Entry, error: unknown): boolean {
    if (entry.retries <= 0 || !conflictOf(error)) {
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
    // or gives in the trash, is read from the server, which has none of it
    // to read, so that the page no longer shows it, as the channel, while it
    // is not live, would not tell.
    for (const c of conflictsOf(error)) {
      const state = states[c.id] ?? states[canonical(c.id)];
      if (state === undefined || state.deleted_at !== undefined) {
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
        // elsewhere since, and is not written over unasked. One that changed
        // elsewhere to what the batch writes, as two users who give a board
        // the same name, is none that the batch would write over.
        const elsewhere = shown.exact
          ? !equal(state[field], base[field])
          : !equal(state[field], value);
        if (
          !equal(value, base[field]) &&
          elsewhere &&
          !equal(value, state[field])
        ) {
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

/**
 * Returns the part of doc that holds entity, the entity of id that it holds,
 * as entitiesOf finds it, or undefined for none, as the project: a branch
 * output's is its node's, which it names.
 */
function partOfHeld(
  doc: Doc,
  id: string,
  entity: Revised | undefined,
): Part | undefined {
  const place = placeOf(id);
  switch (place?.of) {
    case "kind":
      return entity === undefined
        ? undefined
        : partOf(place.kind, entity as Entities[Kind]);
    case "output": {
      const node =
        entity === undefined
          ? undefined
          : doc.nodes.get((entity as BranchOutput).node_id);
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

/** A part that holds folders, of the kind of its entities. */
type FolderPart = "tree" | "variables" | "components";

/** The parts that hold folders. */
const folderParts: readonly FolderPart[] = ["tree", "variables", "components"];

/** A list that a part is read from. */
type List = { [K in Kind]: { kind: K; items: Entities[K][] } }[Kind];

/** Reads the project's folders of kind, every page. */
function listFolders(
  projectId: string,
  kind: "board" | "variable" | "component",
): Promise<Entities["folders"][]> {
  return everyPage(
    async (cursor) =>
      (
        await api.call("listFolders", {
          path: { project_id: projectId },
          query: { kind, cursor, limit: allAtOnce },
        })
      ).body,
  );
}

/** Reads the lists of part, every page of each. */
async function readPart(projectId: string, part: Part): Promise<List[]> {
  const path = { project_id: projectId };
  const page = { limit: allAtOnce };
  const folders = (kind: "board" | "variable" | "component") =>
    listFolders(projectId, kind).then(
      (items): List => ({ kind: "folders", items }),
    );
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

/** Reads the entity of id, or resolves to undefined if the server has none for the user (notFoundBy). */
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
    if (notFoundBy(error)) {
      return undefined;
    }
    throw error;
  }
}
