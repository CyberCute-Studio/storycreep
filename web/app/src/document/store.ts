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
import { type AnyCommand, applyCommands, canonical } from "./apply";
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
  /** How many of the page's batches are queued or under way, or wait for a step of the history. */
  sending: number;
  /** How many steps can be undone and redone. */
  undoable: number;
  redoable: number;
  /**
   * Counts the changes of the history's steps (History.version), which tell
   * that the step that undo would send is another, as the counts do not once
   * the history holds as many steps as it keeps.
   */
  historyVersion: number;
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

/**
 * A batch of the page's own made while a step of the history is under way,
 * which waits to be queued until the step has settled (afterSteps), or a
 * batch of a change sent as one that waits for the one before it to apply
 * (sendParts), which the page shows meanwhile, on top of the batches queued.
 */
interface Waiting {
  commands: readonly Command[];
  /** The time that the page gave the batch as it was made, which it keeps once it is queued (enqueue). */
  time: string;
  shown?: Shown;
}

/**
 * The view that a batch's commands, as they were, and the revisions of its
 * answer, as it was, left from, which the page showed last, so that the view
 * is made anew from what changed alone (shownOn).
 */
interface Shown {
  from: Doc;
  commands: readonly AnyCommand[];
  revisions: CommandResultBody["revisions"] | undefined;
  view: Doc;
}

/**
 * Returns view with batch on top, its commands applied as of its time, at
 * revisions, if given, as the page showed it last where none of those, nor
 * view, has changed since (Shown), as each batch applied anew copies what it
 * changes of the document's maps.
 */
function shownOn(
  view: Doc,
  batch: { commands: readonly AnyCommand[]; time: string; shown?: Shown },
  revisions?: CommandResultBody["revisions"],
): Doc {
  const last = batch.shown;
  if (
    last !== undefined &&
    last.from === view &&
    last.commands === batch.commands &&
    last.revisions === revisions
  ) {
    return last.view;
  }
  const next = applyPending(view, batch.commands, batch.time, revisions);
  batch.shown = { from: view, commands: batch.commands, revisions, view: next };
  return next;
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
   * What the server was known to have had as the batch's commands, with
   * their IDs, were first sent, which a batch whose commands have the IDs of
   * one that no answer came to (unanswered), and which the server answers as
   * it answered that one, if it applied, at its seq, rather than apply it
   * again, takes from that one (pump).
   */
  known?: Known;
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
   * What the batch's operation left, once the document has followed it, as
   * of a batch whose commands have the IDs of one that no answer came to,
   * which applied (unanswered): what an answer of already_applied does not
   * give, whose revisions are those that the entities have now (Sent).
   */
  followed?: Followed;
  shown?: Shown;
  /** Settles the batch with its answer, which it applied after as many restores as given (Sent.restores). */
  resolve: (answer: CommandResultBody, restores: number) => void;
  reject: (error: unknown) => void;
}

/**
 * What a batch's operation left, as the document followed it (confirm): the
 * seq that it applied as, and the revision that it gave each entity that it
 * changed, by its ID.
 */
interface Followed {
  seq: number;
  revisions: ReadonlyMap<string, number>;
}

/**
 * What the server was known to have had of the project (latest) once a
 * request was sent, as a batch, or a read of the project, past which the
 * batch commits, if it applies, and as of which, at least, the read
 * answers, but for a database restored from an older backup since, and how
 * many times the document had found the server's seq gone back then
 * (restores), after which a seq of before is no longer what the server has.
 */
interface Known {
  latest: number;
  restores: number;
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

/**
 * The server's seq went back behind the batches that a step of the history
 * undoes or redoes, as of a database restored from an older backup, while
 * the step was sent in parts, after which no more of them are sent
 * (sendInParts).
 */
class WentBackError extends Error {
  constructor() {
    super("The server's seq went back behind the step's batches.");
  }
}

/** The answer to a batch, with what the document held of what it changed, before it applied, if it knows yet. */
interface Sent {
  answer: CommandResultBody;
  captured?: Captured;
  /** What the batch's operation left, where the document followed it, which an answer of already_applied does not give (Entry.followed). */
  followed?: Followed;
  /**
   * How many times the document had found the server's seq gone back
   * (restores) as the batch was sent, or as its answer told of a restore
   * itself, after which the answer is of what the server has. One of fewer
   * than the document has found since may be of what the server no longer
   * has, as a part of a step sent in parts that applied before the answer
   * to a later part told of a restore, or that was under way as the
   * document found one, whose seq the timeline after the restore gives to
   * another batch (stepped).
   */
  restores: number;
}

/**
 * The batch of a change sent as one (sendJoined) whose step the undos of the
 * batches after it join, while it is the latest: its seq, and how many times
 * the document had found the server's seq gone back as the step joined the
 * history (restores), after which that seq is another batch's, as a restore
 * gives its seqs anew, which clears the history.
 */
interface Joined {
  seq: number;
  restores: number;
}

/** How many of a batch's refusals for a revision that changed elsewhere, but not what it changes, it is sent again after. */
const conflictRetries = 3;

/** The most commands that a batch sent through the API holds (FR-API-03). */
export const maxBatchCommands = 1000;

/** How many entities the document reads from the server at once. */
const readsAtOnce = 6;

/**
 * How many entities of one part an operation may leave to read from the
 * server, each by its ID, before the part is read again instead, as once a
 * variable's rename has compiled anew the expressions of a board's hundreds
 * of nodes, which its lists, 200 to a page, answer in a few requests.
 */
const readsOfOnePart = 20;

/** How many times a read of a component whole is tried while what it reads is older than the document's (readComponent). */
const componentReadAttempts = 3;

/**
 * How long, in milliseconds, the document waits for the real-time channel
 * to bring the operations that it expects before it goes on without them:
 * an undo, those of the batch that it undoes; a page's reading anew, those
 * up to the seq that the subscription holds as of (refresh); and a socket
 * that was lost, to be back with those up to the answers to the page's
 * batches (catchUp).
 */
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

/**
 * Returns the ID of the first of a batch's commands, which tells its
 * operation from another batch's of the same seq, as of a database restored
 * from an older backup, whose seqs commit anew (wentBack).
 */
function firstCommandId(commands: readonly unknown[]): string | undefined {
  return (commands[0] as { command_id?: string } | undefined)?.command_id;
}

/**
 * Returns the revisions that the batches of sent left, each entity's from
 * the last that changed it, as their answers give them, but for an answer
 * of already_applied, as to a step of the history sent again with the IDs
 * of one that no answer came to, once the server no longer kept its first
 * answer: its revisions are those that the entities have now, which may be
 * past what other sessions changed since, which the other steps, and the
 * rest of a step sent in parts, moved there, would expect, and write over
 * unasked, rather than be refused for it (FR-EDIT-08 AC4), as the page's
 * batches are not moved past them either (pump). What its batch left is
 * what the batch's operation gave, where the document followed it
 * (Sent.followed), as of a channel that brought it while no answer came;
 * otherwise nothing moves, and what expects what it left is refused, with
 * what changed, once sent.
 */
function latestRevisions(sent: readonly Sent[]): Map<string, number> {
  const latest = new Map<string, number>();
  for (const { answer, followed } of sent) {
    if (answer.status === "applied") {
      for (const r of answer.revisions ?? []) {
        latest.set(canonical(r.id), r.rev);
      }
    } else if (followed?.seq === answer.seq) {
      for (const [id, rev] of followed.revisions) {
        latest.set(id, rev);
      }
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
  /** The page's batches that wait for a step of the history under way, in the order that they were made in. */
  private waiting: Waiting[] = [];
  /**
   * The document as the page shows it without the batches that wait for a
   * step of the history (waiting): what the server confirmed, with the
   * page's queued batches on top, on which a batch queued now, or a step,
   * is made (enqueue).
   */
  private queuedView: Doc = this.confirmed;
  private inflight = false;
  /** The operations that came while the document read the project, which it applies once it has. */
  private buffer: Operation[] | undefined = [];
  /** The parts that the document holds, read or being read, each with its read. */
  private readonly loads = new Map<Part, Promise<void>>();
  /** The reads of loads that have finished, which hasRead tells of. */
  private readonly finished = new WeakSet<Promise<void>>();
  /**
   * The reads from the server under way, each with the entities that
   * operations changed meanwhile, so that a list does not take out what an
   * operation put in since the read began, and what the read found of an
   * entity before an operation changed it is read again (readBehind), and
   * with the seq that it began at, before which what went from the document
   * is forgotten once no read began before it (forgetGone).
   */
  private readonly watchers = new Set<Watcher>();
  /** How many reads of parts, or of components whole, are under way. */
  private partReads = 0;
  /** The reads of components whole under way, by their IDs (readComponent). */
  private readonly componentReads = new Map<string, Promise<void>>();
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
  /**
   * The IDs of the commands of each batch that no answer came to, by its key,
   * which a batch sent again as it was takes (enqueue), with what the server
   * was known to have had once the first of them was sent (Entry.known), and
   * what its operation left, once the document has followed it (confirm),
   * which the answer to the batch sent again takes (Entry.followed).
   */
  private readonly unanswered = new Map<
    string,
    { ids: string[]; known: Known; followed?: Followed }
  >();
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
  private readonly sends = new Set<Promise<unknown>>();
  /**
   * The page's batches that send has sent, or queued, and that have yet to
   * settle, each with what its answer adds to the history, which an undo or
   * a redo asked for meanwhile waits for before it is sent, so that it sends
   * the step that is the latest once they have joined it (stepped), and the
   * batches that sendJoined sends as one change, all of them, as one.
   */
  private readonly batches = new Set<Promise<unknown>>();
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
   * (retime), each with the ID of the batch's first command, which the
   * operation of that seq that the channel brings has, but for a database
   * restored from an older backup meanwhile (receive).
   */
  private readonly answeredAt = new Map<
    number,
    { time: string; command: string | undefined }
  >();
  /**
   * The seq that the project read anew is to be as of, at least, as a
   * resync_required that came while it was under way asks, whose
   * operations up to it the channel does not send (reload).
   */
  private resyncTo = 0;
  /**
   * How many times the server's seq has gone back behind what the document
   * had, as of a database restored from an older backup (wentBack), so that
   * the project read anew that was under way then, which may be of before,
   * is read again.
   */
  private restores = 0;
  /**
   * The latest seq that the server is known to have had of the project, of
   * the timeline that the document follows: of the operations that the
   * channel brought, its subscription's answers, the project's reads, the
   * answers to the page's batches, and the steps of the history, as kept
   * over a reload. A seq that the server gives in answer to a request sent
   * once the document knew it, as the seq that a batch applies at, which
   * commits after, is not before it, but for a database restored from an
   * older backup (wentBack).
   */
  private latest = 0;
  /**
   * What the server is known to have had before the channel's next message:
   * what it was known to have had (latest) as the channel sent the
   * document's subscription (since), and each seq that the channel told of
   * since, which a seq of the channel's is not before, but for a database
   * restored from an older backup. The answers to the page's batches that
   * came since are not, as the channel may have sent its message before
   * they committed.
   */
  private told = 0;
  /** How many of the pages' readings anew of the project are under way (refresh), which the document is busy with. */
  private refreshes = 0;
  /**
   * How many reads of the project's seq are under way (seqChecked), which
   * the document is busy with, as once its first read of the project is of a
   * seq before the history kept over a reload (start), so that it is not
   * dropped before the read has told whether the history is of what the
   * server has: the project's next document would read the same steps from
   * the tab's storage meanwhile, which a restore that the read tells of once
   * this one has been dropped takes out of the storage alone, not out of the
   * steps that that document holds, and writes again (History.clear).
   */
  private seqChecks = 0;
  /**
   * Reads the project anew a while after the socket was lost, unless it is
   * back by then (catchUp), for the answers to the page's batches that came
   * before, and that come meanwhile too, which catchUp leaves to it, but for
   * a step of the history asked for meanwhile, which needs them read now
   * (stepped); undefined once it has, or the socket is back.
   */
  private lostTimer: ReturnType<typeof setTimeout> | undefined;
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
    // The history kept over a reload is of batches that the server had,
    // whose seqs, had the server gone back behind them since, would be of
    // other changes.
    this.latest = this.history.latest;
    this.store = createStore<DocumentState>(() => ({
      view: this.confirmed,
      live: false,
      gone: false,
      sending: 0,
      undoable: this.history.undoable,
      redoable: this.history.redoable,
      historyVersion: this.history.version,
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
   * or reading it anew, which a route's loader may be waiting for (refresh),
   * or the project's seq, which tells whether the history kept over a
   * reload is of what the server has (seqChecks), or sending the page's
   * batches, or a step of the history, whose answers join the history,
   * which a document dropped keeps no longer in the tab's storage (dispose).
   */
  get busy(): boolean {
    return (
      !this.started ||
      this.partReads > 0 ||
      this.refreshes > 0 ||
      this.seqChecks > 0 ||
      this.resyncing !== undefined ||
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
    // A read of the project as of a seq before the steps of the history, kept
    // over a reload, may be one that the query client kept from before them,
    // or of a database restored from an older backup since, behind them,
    // which the project's seq read anew tells apart (seqChecked), as, while
    // the channel is not live, nothing else would until a step or a batch is
    // sent, and the page would offer to undo what the server no longer has.
    // The document is kept until the read has settled (seqChecks). A read
    // that fails once the document has been dropped all the same, as with
    // every other once the session is of another account, is of no page's;
    // one that tells of a restore then takes the history out of the tab's
    // storage still, unless the project's next document has written its own
    // there since (History.clear).
    if (body.seq < this.latest) {
      void this.seqChecked().catch((error: unknown) => {
        if (!this.disposed) {
          this.failed(error);
        }
      });
    }
    this.heardOf(body.seq);
    const buffered = this.buffer ?? [];
    this.buffer = undefined;
    // A seq that the channel gives before what the server was known to have
    // had once the subscription was sent, or once it told of an operation,
    // is of a database restored from an older backup, as one that the
    // subscription's answer gives, to a document that the page's own batch,
    // answered before it was sent, had moved past, or whose history, kept
    // over a reload, is of batches past it.
    this.unsubscribe = this.options.realtime?.subscribe(this.projectId, {
      since: (sending = true) => {
        if (sending) {
          this.told = this.latest;
        }
        return this.confirmed.seq;
      },
      subscribed: (seq) => {
        this.subscribedTo = seq;
        if (seq < this.told) {
          this.wentBack(seq);
          return;
        }
        this.heardOf(seq, true);
      },
      operation: (message) => this.receive(operationOf(message)),
      resync: (seq) => {
        this.subscribedTo = seq;
        if (seq < this.told) {
          this.wentBack(seq);
          return;
        }
        this.heardOf(seq, true);
        void this.resync(seq);
      },
      gone: () => this.goneAway(),
      live: (live) => {
        // The operations up to the seq that the subscription is sent from,
        // once the socket is back, are not sent again.
        if (!live) {
          this.answeredAt.clear();
        }
        this.store.setState({ live });
        // The answers to the page's batches past what the document has, as
        // one whose operations the socket was lost before it brought, are
        // followed by the project read anew, as they are once they come
        // while the channel is not live, rather than shown on top of what
        // the document had until the socket is back, if ever (catchUp):
        // once the socket has been lost a while, as one that is back soon
        // brings those operations itself, rather than have the project and
        // every part that the document holds read for each socket that is
        // lost for a moment, as once the browser's network changes; the
        // answers that come meanwhile wait for it too, but for a step of the
        // history asked for meanwhile (stepped).
        clearTimeout(this.lostTimer);
        this.lostTimer = undefined;
        if (!live) {
          this.lostTimer = setTimeout(() => {
            this.lostTimer = undefined;
            this.catchUp();
          }, confirmWait);
        }
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
    clearTimeout(this.lostTimer);
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
        // A loader shows a part that is not there as the page of no page,
        // and any other failure as the error page, which offers the error
        // console that recorded it: the part's read here, and the project's
        // by the query client, which read it (start).
        this.failed(error, generation !== undefined && !notFoundBy(error));
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
        // A read that failed takes its part out of loads, but where the
        // project, read anew meanwhile, holds the part as that read found
        // it, so that a finished read that loads still keeps is of a part
        // that the document has read.
        if (load !== undefined) {
          this.finished.add(load);
        }
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
   * operations do not come in a while. Given a part that the document
   * holds, as the tree that a page shows alone, it reads that part again
   * in place of the project, rather than every part that the session has
   * opened, and resolves once it has, or has failed to, which has the
   * project read anew, and records it (readFailed). The document is busy
   * meanwhile, so that it is not dropped under the loader that waits.
   */
  async refresh(part?: Part): Promise<void> {
    this.refreshes++;
    try {
      if (this.store.getState().live && this.buffer === undefined) {
        await this.whenConfirmed(this.subscribedTo, confirmWait);
        // The project read anew meanwhile, as once the server's seq went back
        // (wentBack), is what the page waits for then.
        if (
          this.disposed ||
          (this.buffer === undefined && this.confirmed.seq >= this.subscribedTo)
        ) {
          return;
        }
      }
      if (
        part !== undefined &&
        this.loads.has(part) &&
        this.buffer === undefined
      ) {
        await this.reread(part);
        return;
      }
      await this.resync(this.confirmed.seq);
      if (this.resyncFailure !== undefined) {
        throw this.resyncFailure;
      }
    } finally {
      this.refreshes--;
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

  /**
   * Reports whether the document holds part as a read of it found it,
   * rather than reads it still, as a page that asks for it during its first
   * read would else read it anew after that read (loadPart).
   */
  hasRead(part: Part): boolean {
    const load = this.loads.get(part);
    return load !== undefined && this.finished.has(load);
  }

  /**
   * Reads the component of id whole, with its description and its values,
   * which the list of the project's components leaves out, as the panel of
   * a component shows them (PRD FR-COMP-02), once the document holds the
   * components, and resolves once it holds the component so, or no longer
   * holds it, as one that went to the trash meanwhile, or rejects with what
   * the read failed with, which the page that asked for it shows. The
   * document keeps it current from then on, as operations change it, and
   * reads it whole again where they change it as it cannot tell, but a read
   * of the components' list, which answers it without them at a newer
   * revision, takes them out again (mergeList), and the page asks for them
   * anew. A read of the component under way resolves with that read. One
   * that found it older than the document holds it now, as of an operation
   * that applied meanwhile, whose appliers could not give it what the read
   * would have, or that a reading anew of the project read over, is tried
   * again, a few times.
   */
  readComponent(id: string): Promise<void> {
    const reading = this.componentReads.get(id);
    if (reading !== undefined) {
      return reading;
    }
    const whole = () => {
      const held = this.confirmed.components.get(id);
      return held === undefined || held.attributes !== undefined;
    };
    this.partReads++;
    const read = (async () => {
      try {
        await this.load("components");
        for (let attempt = 0; attempt < componentReadAttempts; attempt++) {
          if (this.disposed || whole()) {
            return;
          }
          const since = this.confirmed.seq;
          const generation = this.generation;
          const watcher = this.watch(since);
          let body: Revised | undefined;
          try {
            body = await readOne(this.projectId, id);
          } catch (error) {
            // The page shows the failure, and the error console has it, as
            // a part's failed read; a session that may have ended is asked
            // for, as the app's queries ask.
            this.failed(error);
            throw error;
          } finally {
            this.watchers.delete(watcher);
          }
          // The project read anew meanwhile read the component as its list
          // answers it, which this read may be older than.
          if (this.disposed || this.generation !== generation) {
            continue;
          }
          this.took(
            mergeEntity(this.confirmed, id, body, since, watcher.changed),
            [id],
          );
          this.readBehind(watcher);
          this.show();
        }
      } finally {
        this.partReads--;
        this.componentReads.delete(id);
      }
    })();
    this.componentReads.set(id, read);
    return read;
  }

  /** Applies an operation of the project, of the real-time channel, in the order of seqs. */
  private receive(op: Operation): void {
    if (this.disposed) {
      return;
    }
    this.heardOf(op.seq, true);
    if (this.buffer !== undefined) {
      this.buffer.push(op);
      return;
    }
    const first = firstCommandId(op.commands);
    const answered = this.answeredAt.get(op.seq);
    if (
      (op.seq <= this.confirmed.seq &&
        answered !== undefined &&
        answered.command !== first) ||
      (op.seq > this.confirmed.seq &&
        this.entries.some(
          (e) =>
            e.answer?.status === "applied" &&
            e.answer.seq === op.seq &&
            firstCommandId(e.commands) !== first,
        ))
    ) {
      // The operation of the seq that the page's own batch applied as, as
      // its answer says, whether the document has taken it from the answer
      // already or not, is of another batch (an answer of already_applied
      // gives the latest of the seqs that applied its commands, which may
      // be of several, and is never taken for its operation): the server's
      // seq went back behind that answer, as of a database restored from an
      // older backup, which no resync_required tells where the channel had
      // not sent the operations up to it, and the operation is of a change
      // that another session made since, which the project read anew has,
      // and which is kept until it has (buffer).
      this.wentBack(op.seq - 1);
      this.receive(op);
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
    const first = firstCommandId(op.commands);
    // A batch sent again as it was, with the IDs that its commands had, that
    // is queued still, is not the operation's: the operation is of the batch
    // that no answer came to, and the one queued, which would never be sent
    // once taken out, is answered as that one once it is (settle).
    const own = this.entries.find(
      (e) =>
        e.sent &&
        (firstCommandId(e.commands) === first || e.answer?.seq === op.seq),
    );
    // What the operation of a batch that no answer came to left, whether the
    // batch sent again as it was is under way, or yet to be, which its
    // answer, of already_applied, does not give (latestRevisions).
    let unanswered =
      own === undefined ? undefined : this.unanswered.get(own.key);
    if (own === undefined) {
      for (const u of this.unanswered.values()) {
        if (u.ids[0] === first) {
          unanswered = u;
          break;
        }
      }
    }
    if (first !== undefined && unanswered?.ids[0] === first) {
      unanswered.followed = {
        seq: op.seq,
        revisions: new Map(op.revisions.map((r) => [canonical(r.id), r.rev])),
      };
    }
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
    const time = this.answeredAt.get(op.seq)?.time;
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
   * copies what it changes of the document's maps (Entry.shown). The
   * batches that wait for a step of the history under way are on top of
   * them all (waiting), as they are sent after, each applied anew only once
   * what it applies on top of has changed too (Waiting.shown).
   */
  private show(): void {
    let view = this.confirmed;
    for (const entry of this.entries) {
      view = shownOn(
        view,
        entry,
        entry.answer?.status === "applied" ? entry.answer.revisions : undefined,
      );
    }
    this.queuedView = view;
    for (const waiting of this.waiting) {
      view = shownOn(view, waiting);
    }
    this.store.setState({
      view,
      sending:
        this.entries.filter((e) => e.answer === undefined).length +
        this.waiting.length,
      undoable: this.history.undoable,
      redoable: this.history.redoable,
      historyVersion: this.history.version,
    });
  }

  /**
   * Resolves once the document is as of seq, or after wait milliseconds,
   * whichever comes first, the other then left undone, so that a wait that
   * ended, as with each page's reading anew of the project while the
   * channel is live, and each step of the history, leaves no timer behind,
   * which would hold the document after it has been dropped.
   */
  private whenConfirmed(seq: number, wait: number): Promise<void> {
    if (this.confirmed.seq >= seq) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      const waiter = {
        seq,
        resolve: () => {
          clearTimeout(timer);
          resolve();
        },
      };
      this.waiters.push(waiter);
      timer = setTimeout(() => {
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

  /**
   * Forgets what the document knows of the project from before seq, once
   * the server's seq is found gone back to it, behind what the server had
   * (latest, told), as by the channel's resync_required or its
   * subscription's answer, an operation of another batch than the page's
   * own of its seq, the answer to a batch of the page's, or the project read
   * anew (wentBehind), as of a database restored from an older backup,
   * whose seqs and revisions after seq, which commit anew, are of other
   * changes than those that the document saw: the steps of the history,
   * which wait for seqs, and expect revisions, from before, and would
   * otherwise write over a change that another session made since, which
   * took the same revision (FR-EDIT-08 AC4). Seqs are all that tell it: a
   * restore that the server's seq has passed again by the time the
   * document hears from the server, or that took it back to the seq that
   * the document has, is not told apart, which only a timeline that a
   * restore changes would tell, and a batch that the page made on what it
   * showed before is sent as it was, as a form held open across the
   * restore would be. It forgets the revisions that the page's own batches
   * moved entities to (ownMoves), and what the document held of each at
   * each revision (earlier); the page's batches answered past seq, which the
   * restore took back; what the server was known to have had past seq
   * (latest, told), so that the answers that follow are compared with what
   * it has now; and the operations that came while the project was read
   * anew, which are of before, as may be that read itself, which it reads
   * again (reload), as it reads the project anew now. The document is as of
   * seq, at most, from then on, as what it holds past seq is of before too,
   * so that the page's batches answered past seq are shown until the
   * project read anew has them, and the subscription sent anew meanwhile is
   * from seq; and an undo that waits for the operations up to the batch
   * that it undoes, which will never come, goes on at once, as nothing
   * (stepped).
   */
  private wentBack(seq: number): void {
    this.history.clear();
    this.ownMoves.clear();
    this.earlier.clear();
    this.entries = this.entries.filter(
      (e) => e.answer === undefined || e.answer.seq <= seq,
    );
    this.restores++;
    this.latest = seq;
    this.told = seq;
    this.subscribedTo = Math.min(this.subscribedTo, seq);
    if (this.confirmed.seq > seq) {
      this.confirmed = { ...this.confirmed, seq };
    }
    for (const waiter of this.waiters.filter((w) => w.seq > seq)) {
      this.waiters.splice(this.waiters.indexOf(waiter), 1);
      waiter.resolve();
    }
    if (this.buffer !== undefined) {
      this.buffer = [];
      this.resyncTo = seq;
    }
    void this.resync(seq);
    this.show();
  }

  /** Returns what the server is known to have had of the project now, past which what it commits from now on commits (Known). */
  private knownNow(): Known {
    return { latest: this.latest, restores: this.restores };
  }

  /**
   * Takes the document back to seq (wentBack), and reports whether it did,
   * if seq, which the server had once it answered a request sent once it
   * was known to have had what known says, is before that, as only a
   * database restored from an older backup since leaves it, but for a
   * restore that the document has found since (Known.restores); otherwise
   * it records that the server has had seq (heardOf).
   */
  private wentBehind(seq: number, known: Known): boolean {
    if (seq >= known.latest || this.restores !== known.restores) {
      this.heardOf(seq);
      return false;
    }
    this.wentBack(seq);
    return true;
  }

  /**
   * Reads the project's seq from the server, and takes the document back to
   * it if it is before what the server was known to have had once the read
   * was sent (wentBehind), which, while the channel is not live, nothing
   * else would tell, and which the document is busy with (seqChecks). It
   * reports whether the project can still be read.
   */
  private async seqChecked(): Promise<boolean> {
    const known = this.knownNow();
    this.seqChecks++;
    try {
      const body = await readOne(this.projectId, this.projectId);
      if (body === undefined) {
        this.goneAway();
        return false;
      }
      this.wentBehind((body as ProjectBody).seq, known);
      return true;
    } finally {
      this.seqChecks--;
    }
  }

  /** Records that the server has had the project as of seq, at least, which the channel told of, if told (latest, told). */
  private heardOf(seq: number, told = false): void {
    this.latest = Math.max(this.latest, seq);
    if (told) {
      this.told = Math.max(this.told, seq);
    }
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
    const known = this.knownNow();
    let restores = known.restores;
    try {
      body = (
        await api.call("getProject", { path: { project_id: this.projectId } })
      ).body;
      // The parts are read after the project, so they are as of its seq at
      // least, which may be past seq, as once the page read the project
      // anew while the channel was not live (refresh), and which the next
      // answer to the page's batches follows. A project read as of a seq
      // before what the server was known to have had once the read was sent
      // is of a database restored from an older backup since, which the
      // document goes back to (wentBehind), and which the read is of what
      // the server has now, as of that seq.
      let asOf = Math.max(seq, body.seq);
      if (this.wentBehind(body.seq, known)) {
        restores = this.restores;
        asOf = body.seq;
      }
      // A document dropped meanwhile reads no parts: it has told of the
      // restore, if any, which takes its history out of the tab's storage.
      if (this.disposed) {
        return;
      }
      doc = { ...emptyDoc(asOf), project: projectOf(body) };
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
      // The read tried again is as of the latest seq that the channel asked
      // for meanwhile, or the one that the server's seq went back to.
      const retry = () => {
        clearTimeout(this.resyncTimer);
        this.retryNow = undefined;
        void this.reloading(this.resyncTo, failures + 1);
      };
      this.retryNow = retry;
      this.resyncTimer = setTimeout(retry, backoff(failures + 1, Math.random));
      return;
    }
    if (this.disposed) {
      return;
    }
    if (this.restores !== restores) {
      // The server's seq went back while the project was read, which may
      // have read it as it was before: it is read again, which what waits
      // for this read waits for too (refresh), rather than take the document
      // as it was before for what the project has.
      await this.reload(this.resyncTo, 0);
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
   * to the page's batches are past what the document has, as they come, or,
   * for those that came before the socket was lost, before it brought the
   * operations up to them, or that come within a while after, once that
   * while has passed, unless it is back by then (lostTimer): the operations
   * before them, of other sessions, would not come until the channel is
   * live again, if ever, as behind a proxy that refuses the socket, and
   * until they do the batches stay on top of the document, which would show
   * nothing of what changed elsewhere. A project that can no longer be
   * read, as once the socket's subscription found it gone, is not. Nothing
   * reads it anew here while the socket has been lost for a moment, as the
   * answers that come meanwhile, and those that a reading anew leaves past
   * what it read, wait for that while too, rather than have the project and
   * every part read anew for a socket that is back soon, with the operations
   * before them; the timer of that while has ended it once it calls this.
   */
  private catchUp(): void {
    const { live, gone } = this.store.getState();
    if (
      live ||
      gone ||
      this.buffer !== undefined ||
      this.lostTimer !== undefined
    ) {
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
   * one that a page shows as the page of no page, as a loader's read of a
   * part that is not there, is not recorded here (record).
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
    return this.settling(
      this.afterSteps([commands], (held) =>
        this.sendBatch(commands, held?.[0]),
      ),
    );
  }

  /**
   * Applies batches as one change of the user's, as an edit of more
   * entities than a batch holds commands for (FR-API-03): each as send
   * applies one, in turn, each once the one before has applied, resolving
   * with their answers, or rejecting with the refusal of the first that is
   * refused, which sends no more of them, while those before it stay
   * applied, as the server applies no batches together. The page shows them
   * all at once, as made at one time, each on top of what is queued until
   * it is queued in its turn, as those that wait for a step are (waiting),
   * rather than each once the one before has been answered, and no longer
   * those after one that is refused. Their undos join the history as one
   * step, which undoes them all, the last first, rather than as a step for
   * each, of which an undo would take back the last alone; an undo or a
   * redo asked for meanwhile waits for them all.
   */
  sendJoined(
    batches: readonly (readonly Command[])[],
  ): Promise<CommandResultBody[]> {
    const parts = batches.filter((batch) => batch.length > 0);
    if (parts.length === 0) {
      return Promise.resolve([]);
    }
    return this.settling(
      this.afterSteps(parts, (held) => this.sendParts(parts, held)),
    );
  }

  /** Returns sending, which the document is busy with until it has settled (sends). */
  private settling<T>(sending: Promise<T>): Promise<T> {
    this.sends.add(sending);
    const settled = () => {
      this.sends.delete(sending);
    };
    sending.then(settled, settled);
    return sending;
  }

  /**
   * Sends batches, the batch of a change or those of a change sent as one,
   * by start, once no step of the history is under way: those made while an
   * undo or a redo waits to be sent, as a form saved while the undo waits
   * for the channel's operations, were asked for after it, and are sent
   * after it, rather than before it, which would leave the undo the latest
   * change, the one that the user did not ask to undo, or end what the redo
   * would redo, or else be written over by it (stepped). They are sent in
   * the order that they were made in among the steps asked for meanwhile,
   * as those are among themselves (step), and the page shows them at once
   * all the same, on top of what is queued, each held as made at the time
   * that they were (waiting), as a board dropped in the tree where it was
   * dropped, rather than where it was until the step has settled; start is
   * given what held them, if anything did, which takes each out of what the
   * page shows on top as it queues it (queued).
   */
  private afterSteps<T>(
    batches: readonly (readonly Command[])[],
    start: (held: readonly Waiting[] | undefined) => Promise<T>,
    waiting?: readonly Waiting[],
  ): Promise<T> {
    const underWay = this.stepUnderWay;
    if (underWay !== undefined) {
      let held = waiting;
      if (held === undefined) {
        const time = this.now();
        held = batches.map((commands) => ({ commands, time }));
        this.waiting.push(...held);
        this.show();
      }
      const after = () => this.afterSteps(batches, start, held);
      return underWay.done.then(after, after);
    }
    const batch = start(waiting);
    this.batches.add(batch);
    const settled = () => {
      this.batches.delete(batch);
    };
    batch.then(settled, settled);
    return batch;
  }

  /**
   * Takes held, a batch that the page shows on top of what is queued
   * (waiting), out of what it shows so, as it is queued now, which the page
   * shows in its place, and returns the time that it was made at, which the
   * page showed it with, and which it keeps (enqueue), or undefined for none.
   */
  private queued(held: Waiting | undefined): string | undefined {
    if (held === undefined) {
      return undefined;
    }
    this.waiting = this.waiting.filter((w) => w !== held);
    return held.time;
  }

  /** Sends a batch as send does, which held showed while it waited, if anything did, adding its undo to the history once it has applied. */
  private async sendBatch(
    commands: readonly Command[],
    held?: Waiting,
  ): Promise<CommandResultBody> {
    const sent = await this.enqueue(
      commands,
      conflictRetries,
      false,
      this.queued(held),
    );
    this.recorded(sent);
    return sent.answer;
  }

  /**
   * Sends batches as sendJoined does, each after the one before has applied,
   * which held showed while they waited for a step, if anything did, and
   * which are otherwise made now: those after the first are shown on top of
   * what is queued (waiting) until each is queued, and those left once one
   * is refused are shown no longer. The undo of each that applied joins the
   * step of those before it, while that is the latest step, as it is but
   * where a batch of the page's made while the one before was under way
   * applied between them, or the history was forgotten meanwhile, as of a
   * database restored from an older backup, after which those after it join
   * its own.
   */
  private async sendParts(
    batches: readonly (readonly Command[])[],
    held?: readonly Waiting[],
  ): Promise<CommandResultBody[]> {
    let shown = held;
    if (shown === undefined) {
      const time = this.now();
      shown = batches.map((commands) => ({ commands, time }));
      // The first is queued at once, which shows it.
      this.waiting.push(...shown.slice(1));
    }
    const answers: CommandResultBody[] = [];
    // The batch of the step that the parts join, once one has one.
    let joined: Joined | undefined;
    try {
      for (const [i, commands] of batches.entries()) {
        const sent = await this.enqueue(
          commands,
          conflictRetries,
          false,
          this.queued(shown[i]),
        );
        answers.push(sent.answer);
        joined = this.recorded(sent, joined);
      }
    } finally {
      const left = new Set<Waiting>(shown);
      if (this.waiting.some((w) => left.has(w))) {
        this.waiting = this.waiting.filter((w) => !left.has(w));
        this.show();
      }
    }
    return answers;
  }

  /**
   * Adds the undo of the batch of sent to the history once it has applied,
   * as a step of its own, or, where joined is given, to the latest step,
   * which undoes the batch of joined, while it is the latest (History's
   * join) and the server's seq has not gone back since it joined the
   * history, and returns the batch of the step that undoes it now, or
   * joined, where it added none.
   */
  private recorded(sent: Sent, joined?: Joined): Joined | undefined {
    const answer = sent.answer;
    if (answer.status !== "applied") {
      return joined;
    }
    if (answer.undo == null || answer.undo.length === 0) {
      this.history.done(undefined);
      this.show();
      return joined;
    }
    const step = { commands: answer.undo, seqs: [answer.seq] };
    if (
      joined === undefined ||
      joined.restores !== this.restores ||
      !this.history.join(joined.seq, step, latestRevisions([sent]))
    ) {
      this.history.done(step);
      joined = { seq: answer.seq, restores: this.restores };
    }
    if (sent.captured !== undefined) {
      this.history.capture(answer.seq, sent.captured);
    }
    this.show();
    return joined;
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
   * Reports whether the step that undo would send is the one of the batch
   * of seq, as once the batch's answer added it to the history and nothing
   * joined it after: not of a batch whose answer added none, as one of
   * already_applied, or with an empty undo, whose undo would else be that
   * of the step before it.
   */
  undoesNext(seq: number): boolean {
    return this.history.nextUndo()?.seqs.includes(seq) ?? false;
  }

  /**
   * Sends the latest step of the history to undo or to redo. One asked for
   * while another of the same is under way, as by a second click before the
   * first has disabled the button, is that one, and settles as it does, so
   * that what the page says of it is what became of it; one of the other is
   * sent once that one has settled, as what was asked for after it, as is a
   * batch of the page's made meanwhile (afterSteps).
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
    earlier: readonly Promise<unknown>[],
  ): Promise<boolean> {
    this.store.setState({ stepping: true });
    try {
      // The page's batches made before the step was asked for, queued or
      // under way, as a form's save just before it, join the history first,
      // so that the step sent is the latest once they have, rather than one
      // before them, which would write over what they changed, unasked, and
      // be refused for it, and dropped, with the steps before it, which
      // expect what it undoes. Those made after wait for it (afterSteps).
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
      // for the step. Once the channel is not live after that, nothing would
      // tell the document of a database restored from an older backup
      // meanwhile, behind those batches, but a seq that the server gives, as
      // the project's, which it reads first (seqChecked): the project read
      // anew may have failed, and wait to try again, or be slower than the
      // wait, or have read the project before the step was asked for. A
      // socket lost a moment before, whose answers' reading anew waits a
      // while for it to be back (lostTimer), would leave the step without
      // those batches, and so without what it moves, after which the other
      // steps, which go on expecting what the document held before them,
      // would be refused for the user's own step, and dropped: the project is
      // read anew now, as of them, rather than once that while has passed.
      const restores = this.restores;
      const last = Math.max(...step.seqs);
      if (
        this.lostTimer !== undefined &&
        this.buffer === undefined &&
        last > this.confirmed.seq
      ) {
        void this.resync(last);
      }
      if (this.store.getState().live || this.buffer !== undefined) {
        await this.whenConfirmed(last, confirmWait);
      }
      if (
        !this.store.getState().live &&
        this.restores === restores &&
        !(await this.seqChecked())
      ) {
        // The project can no longer be read, which its pages show.
        return false;
      }
      if (this.restores !== restores) {
        // The server's seq went back meanwhile, behind the batches that the
        // step undoes or redoes, which the history no longer has, and whose
        // operations no longer come, which the step waits for no longer
        // (wentBack).
        return false;
      }
      const before = this.history.before(step);
      let applied: Sent[] = [];
      let rest: UndoCommandBody[] = [];
      let failure: unknown;
      try {
        applied = await this.sendInParts(step.commands, restores);
      } catch (error) {
        if (error instanceof PartsError) {
          applied = error.applied;
          rest = error.rest;
          failure = error.cause;
        } else {
          failure = error;
        }
      }
      // A step sent in parts whose batches the server's seq went back behind
      // meanwhile, as the answer to a part told, sends no more of them: the
      // history, which no longer has the step, keeps the redo of the parts
      // that applied after that restore, which are of what the server has
      // now, and nothing of those that applied before it, or were under way
      // as the document found it (Sent.restores), whose changes the restore
      // may have taken back, and whose seqs and revisions the timeline after
      // it gives to other changes, which their redo would wait for, and
      // write over, nor of the rest (sendInParts).
      const wentBack = failure instanceof WentBackError;
      if (wentBack) {
        failure = undefined;
        rest = [];
      }
      const current = applied.filter((s) => s.restores === this.restores);
      if (current.length > 0) {
        const answers = current.map((s) => s.answer);
        // What an answer of already_applied gives, the entities' current
        // revisions, moves nothing: the other steps go on expecting what
        // they did, and are refused once sent, rather than moved past what
        // another session may have changed since, but to what its batch's
        // operation left, where the document followed it (latestRevisions).
        const latest = latestRevisions(current);
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
        for (const s of current) {
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
      return applied.length > 0 || !wentBack;
    } finally {
      this.store.setState({ stepping: false });
      this.show();
    }
  }

  /**
   * Sends the commands of a step, as one batch, or, of more commands than a
   * batch holds (maxBatchCommands), as the step of batches sent as one
   * change may be (sendJoined), or once the server refuses it as an undo too
   * large for one request, in two halves, each split again as it needs, in
   * order, each once the one before has applied, with what it expects of an
   * entity that those before it brought back moved to the revision that the
   * last of them that changed it left (FR-EDIT-08). A part is not sent once
   * the document has found the server's seq gone back since the step was
   * sent (restores), as the answer to a part before it may tell (pump),
   * behind the batches that the step undoes, whose revisions its commands
   * expect (WentBackError).
   */
  private async sendInParts(
    commands: readonly UndoCommandBody[],
    restores: number,
  ): Promise<Sent[]> {
    if (this.restores !== restores) {
      throw new WentBackError();
    }
    if (commands.length <= maxBatchCommands) {
      try {
        return [await this.enqueue(commands as unknown as Command[], 0, true)];
      } catch (error) {
        if (!undoTooLarge(error) || commands.length < 2) {
          throw error;
        }
      }
    }
    const half = Math.ceil(commands.length / 2);
    let first: Sent[];
    try {
      first = await this.sendInParts(commands.slice(0, half), restores);
    } catch (error) {
      if (!(error instanceof PartsError)) {
        throw error;
      }
      // Part of the first half applied: the rest of it, and the second
      // half, expect what that part left.
      const latest = latestRevisions(error.applied);
      throw new PartsError(error.applied, error.cause, [
        ...error.rest,
        ...commands.slice(half).map((c) => withRevisions(c, latest)),
      ]);
    }
    const latest = latestRevisions(first);
    const second = commands.slice(half).map((c) => withRevisions(c, latest));
    try {
      return [...first, ...(await this.sendInParts(second, restores))];
    } catch (error) {
      throw error instanceof PartsError
        ? new PartsError([...first, ...error.applied], error.cause, error.rest)
        : new PartsError(first, error, second);
    }
  }

  /** Queues a batch, made at time, or now, which the page shows at once, a step of the history or not (Entry.step), and resolves with its answer. */
  private enqueue(
    commands: readonly Command[],
    retries: number,
    step: boolean,
    time = this.now(),
  ): Promise<Sent> {
    const key = JSON.stringify(commands);
    // A batch the same as one that is queued or under way, as once a change
    // goes back and forth, is another batch, with IDs of its own, which the
    // server would otherwise answer as that one, applying nothing.
    const again = this.entries.some((e) => e.key === key)
      ? undefined
      : this.unanswered.get(key);
    const ids = again?.ids ?? commands.map(() => formatUuid(newUuid()));
    // A batch of the page's, but for a step of the history, which expects
    // what the history moved it to, expects what the page's own batches that
    // applied on top of what it expects left (ownMoves), as one that was
    // queued behind them would (moveQueued).
    const withIds = commands.map((command, i) => {
      const body = { ...command, command_id: ids[i] } as CommandBody;
      return step ? body : this.pastOwn(body);
    });
    // Made on what is queued, without the batches that wait for a step,
    // which come after it (waiting).
    const view = this.queuedView;
    const base = new Map<string, Base>();
    for (const command of withIds) {
      for (const [id, rev] of Object.entries(command.expected_rev ?? {})) {
        base.set(id, this.baseOf(view, canonical(id), rev));
      }
    }
    const draft = new Draft(view, time);
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
        known: again?.known,
        step,
        sent: false,
        resolve: (answer, restores) =>
          resolve({
            answer,
            captured: entry.captured,
            followed: entry.followed,
            restores,
          }),
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
    // What the server is known to have had as the batch's commands were
    // first sent, past which they commit, if they apply, and past which the
    // server's answer to them sent again gives the seq that applied them.
    entry.known ??= this.knownNow();
    const known = entry.known;
    try {
      await this.whenReady();
      // The restores that the document had found as the batch was sent,
      // after which its answer is of what the server has (Sent.restores).
      let restores = this.restores;
      const answer = await this.post(entry.commands);
      // A batch that applied at a seq that the server had had before its
      // commands were first sent tells that its seq went back, as of a
      // database restored from an older backup, which the channel may tell
      // only later, or never, as while it is not live. The history is
      // forgotten before the batch's undo joins it, which is of what the
      // server has now, as the batch applied after that restore.
      if (
        answer.status === "applied" &&
        this.wentBehind(answer.seq - 1, known)
      ) {
        restores = this.restores;
      }
      this.heardOf(answer.seq);
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
          this.answeredAt.set(answer.seq, {
            time: entry.time,
            command: firstCommandId(entry.commands),
          });
        }
      }
      // What the operation of the batch that no answer came to left, which
      // the document followed meanwhile, of the batch whose commands' IDs
      // this one sent again has (confirm).
      const unanswered = this.unanswered.get(entry.key);
      if (
        unanswered !== undefined &&
        unanswered.ids[0] === firstCommandId(entry.commands)
      ) {
        entry.followed ??= unanswered.followed;
      }
      this.unanswered.delete(entry.key);
      this.settle();
      this.catchUp();
      entry.resolve(answer, restores);
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
        this.unanswered.set(entry.key, {
          ids: entry.commands.map((c) => c.command_id),
          known,
        });
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
    entry.known = undefined;
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
      const byValue = fieldsOfValues(id);
      const fields = [];
      for (const field of new Set([
        ...Object.keys(base),
        ...Object.keys(mine ?? {}),
      ])) {
        if (meta.has(field) || unread.has(field)) {
          continue;
        }
        const value = mine?.[field];
        const values = byValue.has(field)
          ? namedValues(value, base[field], state[field])
          : undefined;
        const overlapping =
          values === undefined
            ? overlapsAt(shown.exact, value, base[field], state[field])
            : [
                ...new Set([
                  ...Object.keys(values.mine),
                  ...Object.keys(values.was),
                ]),
              ].some((key) =>
                overlapsAt(
                  shown.exact,
                  values.mine[key],
                  // The page showed a value that the entity held none of
                  // as its definition's default, as of a definition that
                  // another session created after the page showed the
                  // entity, which gave the entity that default: a change
                  // of the value made elsewhere is one from the default.
                  key in values.was
                    ? values.was[key]
                    : defaultOf(this.confirmed, key),
                  values.now[key],
                ),
              );
        if (overlapping) {
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
 * Reports whether value, what a batch leaves of a field that the page showed
 * as was, overlaps a change of it made elsewhere, which left it as now: the
 * batch changes it, it changed elsewhere, and not to what the batch writes,
 * as two users who give a board the same name, which is none that the batch
 * would write over. Where the document no longer knows what the page showed
 * at the revision that the batch expects (exact), a field that the batch
 * changes, which has another value now than the batch's, may have changed
 * elsewhere since, and is not written over unasked.
 */
function overlapsAt(
  exact: boolean,
  value: unknown,
  was: unknown,
  now: unknown,
): boolean {
  const elsewhere = exact ? !equal(now, was) : !equal(now, value);
  return !equal(value, was) && elsewhere && !equal(value, now);
}

/**
 * Returns the fields of the entity of id that hold a value of each of
 * several things, as a component's values, one of each of its type's
 * attribute definitions, which a command of its own sets: a conflict
 * compares them value by value, so that a change of one made elsewhere does
 * not overlap the batch's change of another (PRD §7.10 item 1).
 */
function fieldsOfValues(id: string): Set<string> {
  const place = placeOf(id);
  return place?.of === "kind" && place.kind === "components"
    ? new Set(["attributes"])
    : new Set();
}

/**
 * Returns the default of the attribute definition of id, as doc holds it,
 * which a component's form shows as the component's value of it where the
 * component holds none (ComponentPanel), or undefined if doc holds no such
 * definition.
 */
function defaultOf(doc: Doc, id: string): unknown {
  return typeOfAttribute(doc.componentTypes, id)?.attributes.find(
    (attribute) => attribute.id === id,
  )?.default;
}

/** Returns a field of values as a batch leaves it, as the page showed it, and as it is now, or undefined unless each holds values by their names. */
function namedValues(
  mine: unknown,
  was: unknown,
  now: unknown,
):
  | {
      mine: Record<string, unknown>;
      was: Record<string, unknown>;
      now: Record<string, unknown>;
    }
  | undefined {
  const named = (value: unknown): value is Record<string, unknown> =>
    typeof value === "object" && value !== null && !Array.isArray(value);
  return named(mine) && named(was) && named(now)
    ? { mine, was, now }
    : undefined;
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
