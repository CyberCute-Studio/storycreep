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
//
// A batch of the draft that the page cannot send, as while the browser has
// no connection, or that no answer came to, is kept in the device's queue
// (queue.ts, PRD FR-OFF-03), the page showing it as before, and sent again,
// with its commands' IDs, once the server answers, in its order, the same
// commands as online (AC2). Its caller is told that it is queued, rather
// than kept waiting. The server's refusal of a queued batch for what it does
// keeps it, as needing review, and every batch after it, until the user
// decides (plan item 92). Undo takes the latest queued batch that never
// reached the server out of the queue, and redo puts it back, with no
// connection (AC4).

import {
  ApiError,
  type BranchOutput,
  type CommandBody,
  type CommandResultBody,
  NetworkError,
  type ProjectBody,
  type SignedInBody,
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
import { retryAfter } from "../authoring/importUpload";
import { commentsKey } from "../comments/queries";
import { dependenciesKey } from "../dependencies/queries";
import type { ErrorLog } from "../errors/log";
import { overQuota } from "../offline/db";
import { playStatesKey } from "../play/queries";
import { conflictOf, projectQuery } from "../project/queries";
import { statisticsKey } from "../statistics/queries";
import { validationKey } from "../validation/queries";
import { type AnyCommand, applyCommands, canonical } from "./apply";
import {
  type Doc,
  Draft,
  type Entities,
  emptyDoc,
  expressionFields,
  type Gone,
  type Kind,
  kinds,
  nodeOfOutput,
  ownsExpressions,
  type Part,
  type Project,
  partOf,
  placeOf,
  projectOf,
  typeOfAttribute,
} from "./doc";
import { Follower } from "./follow";
import {
  madeOnTop,
  nextOrder,
  type QueuedBatch,
  type QueueStore,
  queueFormat,
  type Refusal,
} from "./queue";
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

/**
 * What send resolves to for a batch that the device's queue keeps, rather
 * than the server's answer, which comes once the queue is sent (PRD
 * FR-OFF-03): the page shows the batch meanwhile, as it showed it before.
 */
export interface Queued {
  status: "queued";
}

/** What send resolves to: the server's answer to the batch, or that the device's queue keeps it. */
export type SendResult = CommandResultBody | Queued;

/** A batch of the page's that the server has yet to answer, as the sync panel lists it (PRD FR-OFF-06). */
export interface PendingBatch {
  /**
   * Its ID, the queue's record's for a queued batch, else its first
   * command's, or none of one that waits for a step of the history, which
   * is queued, its commands given their IDs, once the step has settled
   * (sendAfterSteps).
   */
  id: string | undefined;
  /** When the page made it. */
  time: string;
  /** Its commands, each with its command_id, but of one that has no ID yet (id). */
  commands: readonly Command[];
  /**
   * sending, on its way to the server or about to be; queued, in the
   * device's queue, waiting for the server; review, refused by the server
   * for what it does, waiting for the user's decision (plan item 92).
   */
  state: "sending" | "queued" | "review";
  /** Whether it was sent and no answer came, so that it may have applied. */
  sent: boolean;
  /** The server's latest refusal of it, if any. */
  refusal?: Refusal;
}

/** How the document sends a batch of the page's (ProjectDocument.send). */
export interface SendOptions {
  /**
   * Whether the batch's undo joins the page's history, which Ctrl+Z sends,
   * as it does unless this is false: a comment's commands are no step of
   * it, whose own undo the comment offers where it was (plan item 74).
   */
  history?: boolean;
}

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
  /**
   * How many of the page's batches the device's queue keeps, to be sent, or
   * needing review (PRD FR-OFF-03), once it has written them, which a
   * reload, as for a new version of the app, no longer cuts off (whenSent).
   */
  queued: number;
  /** How many of those the server refused for what they do, which wait for the user's decision, with every batch after them (plan item 92). */
  review: number;
  /**
   * Whether the device's queue keeps batches of the page's that the server
   * has yet to answer, those that it is still writing too (holdsQueued),
   * which a step of the history of a change that the server has waits for,
   * as queued, of those that it has written alone, does not tell.
   */
  holdsQueued: boolean;
  /**
   * Whether the document sends the batches that the device's queue keeps
   * now, rather than hold them (holdReason), as offline, while one needs
   * review, or until the next try, or once it has been dropped, after which
   * it sends none of them (pump): a read that waits for the page's batches
   * waits for those that it sends, which the server answers soon, but not
   * for those that it holds, which it may hold for long (whenSent).
   */
  sendsQueued: boolean;
  /**
   * Whether the page cannot reach the server now, for want of a connection,
   * or of an answer, or of the page's own session, as while it holds the
   * one that the device kept, without its CSRF token (PRD FR-OFF-02), for
   * which the document keeps the batches that it can in the device's queue
   * rather than send them; not while the queue waits for a refusal of when
   * a batch was sent, as of too many requests, which is an answer.
   */
  holding: boolean;
  /** Whether undo would take the latest batch out of the device's queue, which needs no connection (AC4). */
  undoLocal: boolean;
  /** Whether redo would put a batch that undo took out of the queue back, rather than send a step of the history. */
  redoLocal: boolean;
  /** Counts the changes of what the sync panel lists (pendingBatches). */
  queueVersion: number;
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
 * The error of a batch that the page did not send, as it is signed in to
 * another account than the batch's, or out, which the device's queue keeps
 * the batch for, as the account's (ProjectDocument.ownSession).
 */
class OtherAccountError extends Error {
  constructor() {
    super("The page is signed in to another account than the batch's.");
    this.name = "OtherAccountError";
  }
}

/**
 * The refusal of a write of the device's queue that would keep a batch
 * anew for a document dropped as the page signed out, which keeps none
 * (ProjectDocument.signedOutOf): its caller is refused as without a queue,
 * which is no fault for the error console.
 */
class SignedOutError extends Error {
  constructor() {
    super("The page signed out, which keeps no change on the device anew.");
    this.name = "SignedOutError";
  }
}

/**
 * Reports whether error tells that no answer came to a batch, which the
 * device's queue keeps (PRD FR-OFF-03): a request that failed on its way,
 * which may have applied, or a batch that the page did not send, as of
 * another account's (OtherAccountError); not a fault of the page's own, as
 * an answer of a form that it does not expect.
 */
function unansweredBy(error: unknown): boolean {
  return error instanceof NetworkError || error instanceof OtherAccountError;
}

/**
 * Reports whether error is a proxy's page of a failure, without the error
 * envelope, that answered for the server, as 502 while it restarts, or 504
 * for one that took too long, which may have applied: the page did not
 * reach the server, which answered nothing itself.
 */
function proxyFailure(error: unknown): boolean {
  return error instanceof ApiError && !answered(error) && error.status >= 500;
}

/** The longest that a Retry-After has the queue wait before it tries again, in milliseconds. */
const retryAfterMax = 300_000;

/**
 * Returns how long, in milliseconds, the Retry-After of answer, as of a
 * refusal of too many requests (429), or a server that is not ready to
 * serve yet (503), has the page wait before it asks again, in seconds or
 * as a date, at most retryAfterMax, or 0 where it gives none.
 */
function retryAfterOf(answer: ApiError): number {
  return Math.min(
    retryAfter(answer.response.headers.get("Retry-After")) ?? 0,
    retryAfterMax,
  );
}

/**
 * The statuses of the answers that refuse when or by whom a batch was sent,
 * rather than what it does, which may not refuse it later: a session that
 * has ended (401), credentials or a role that do not allow it (403), a
 * request that took too long (408), and too many requests (429), whether
 * the server's own or a proxy's, as one whose own sign-in has ended.
 */
const refusedForWhen = [401, 403, 408, 429];

/** Why a document keeps a batch in the device's queue rather than send it now (ProjectDocument.holdReasons). */
type HoldReason = "review" | "offline" | "unanswered" | "session" | "refused";

/**
 * How long, in milliseconds, a try waits for its answer before the
 * document takes it for one that no answer comes to, as on a network whose
 * connection stalls, which the browser may take minutes to give up on: the
 * batch is kept in the device's queue then, as sent, as it may apply, with
 * every batch after it, while the try goes on (stalled).
 */
const answerWait = 10_000;

/**
 * Reports whether error refuses what a step of the history does, which
 * sending it again would meet again, as for what changed since: the server's
 * answer with a status of 4xx, but for those that refuse when or by whom it
 * was sent (refusedForWhen), and a project that is archived until it is
 * unarchived (project_archived). A failure of the server's own, as 503
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
    !refusedForWhen.includes(status) &&
    body?.code !== "project_archived"
  );
}

/**
 * The undo or the redo under way (ProjectDocument.step), which settles as
 * done does, and, of one sent to the server, which the page's batches made
 * meanwhile wait for (sendAfterSteps), what they wait for no longer, once
 * its read of the server, or its try, or one that it waits for, has had no
 * answer for answerWait (givingWayAfter, stalled): that it gave way
 * (gaveWay), as it says (given), which giveWay gives, and whether a batch
 * of the page's went on before it since (passed), after which the step,
 * not sent yet, is not sent (stepped).
 */
interface StepUnderWay {
  which: "undo" | "redo";
  done: Promise<boolean>;
  gaveWay?: Promise<void>;
  given?: boolean;
  giveWay?: () => void;
  passed?: boolean;
}

/**
 * A batch of the page's own made while a step of the history is under way,
 * which waits to be queued until the step has settled (sendAfterSteps), and
 * which the page shows meanwhile, on top of the batches queued.
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
  /** Whether its undo joins the history once it applies (SendOptions.history), as it does not of a step's. */
  history: boolean;
  /** The document that made the batch (QueuedBatch.maker). */
  maker: string;
  /** The makers of the other batches that the page showed as the batch was made (QueuedBatch.atop). */
  atop: readonly string[];
  /**
   * Where the batch is in the queue, by when it was made (QueuedBatch.order),
   * as of the page's batches that the queue keeps once one made after them
   * is kept, as those that no answer came to, which come before it still.
   */
  order: number;
  sent: boolean;
  /**
   * The batch as the device's queue keeps it, once it does (PRD FR-OFF-03):
   * it is no longer the caller's, who was told that it is queued, and the
   * document records its answer itself, and keeps what changes of it there.
   */
  held?: QueuedBatch;
  /** Whether what the queue keeps of it is behind the batch, as once the answer to a batch before it moved what it expects (persist). */
  dirty?: boolean;
  /**
   * Whether the device's queue has written the batch, after which it is
   * the queue's, which a reload keeps, rather than the page's, which a read
   * that waits for the page's batches waits for (DocumentState.queued).
   */
  stored?: boolean;
  /** Tells the caller that the device's queue keeps the batch, once it does. */
  queued?: () => void;
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

/**
 * How long, in milliseconds, the document waits after it has had the
 * writing statistics read again before it has them read again for the
 * operations that applied meanwhile (Follower), so that a run of
 * operations, as another session's typing, has them read twice a second at
 * most, rather than once for each, while they follow each edit within a
 * second (PRD FR-TEXT-06 AC1).
 */
const statisticsPause = 500;

/**
 * How long, in milliseconds, the document waits after it has had the
 * validation report and the dependency graph read again before it has them
 * read again for the operations that applied meanwhile (Follower): once a
 * second at most, as the server validates the whole draft anew for each
 * read of the report, which takes up to seconds of a project of 5,000 nodes
 * (PRD FR-DEP-03 AC2), and a read under way is let finish first.
 */
const derivedPause = 1_000;

/**
 * How long, in milliseconds, the document waits after it has had the
 * threads of comments read again before it has them read again for the
 * operations that applied meanwhile (Follower): twice a second at most, as
 * nearly any operation may change what they show of their targets, as a
 * node's title or its board's name, while a run of them, as a drag's or
 * another session's typing, has them read once each pause, rather than once
 * for each.
 */
const commentsPause = 500;

/**
 * The prefixes of the types of command of what is no part of the project's
 * draft, which the document holds none of, and which the page reads as
 * queries of their own: the saved play states (play/queries.ts) and the
 * threaded comments (comments/queries.ts). Their commands change none of
 * the draft, nor what the trash lists, nor what the server derives of the
 * draft, and the document reads their queries again as their operations
 * apply (followed).
 */
const outsideDraft = ["play_state.", "comment."] as const;

/** Reports whether command is of what is no part of the project's draft (outsideDraft), of the prefix of, if given. */
function outside(
  command: { readonly type: string },
  of?: (typeof outsideDraft)[number],
): boolean {
  return of === undefined
    ? outsideDraft.some((prefix) => command.type.startsWith(prefix))
    : command.type.startsWith(of);
}

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
  /**
   * The device's copies of the projects that the account keeps offline
   * (PRD FR-OFF-02, offline/keeper.ts), which the document starts from
   * where the device has one (start); none if not set.
   */
  copies?: CopySource;
  /**
   * Reports whether the browser says that it has a connection, as
   * navigator.onLine does, without which the document reads nothing anew
   * (refresh); always, if not set.
   */
  online?: () => boolean;
  /** Calls listener each time that the browser's connection comes or goes, until what it returns is called; never, if not set. */
  onConnection?: (listener: (online: boolean) => void) => () => void;
  /**
   * The device's queue of the batches that the server has yet to answer
   * (PRD FR-OFF-03, queue.ts), which keeps a batch that the page cannot
   * send, as offline; none if not set, as over plain HTTP, where a batch
   * that no answer comes to is refused, as one that the page cannot keep.
   */
  queue?: QueueStore;
  /**
   * Whether the document takes the batches of the queue that no open
   * page's document holds, as of a tab that closed, from its start, as a
   * page's does; not the keeper's, which no page shows, until a page asks
   * for it (takeQueue); always, if not set.
   */
  takesQueue?: boolean;
  /** Returns a number in [0, 1), which spreads the queue's tries again (backoff); Math.random, if not set. */
  random?: () => number;
  /** How long, in milliseconds, a try waits for its answer before the document keeps its batch as one that no answer comes to (stalled); answerWait, if not set. */
  answerWait?: number;
}

/** A copy of a project that the device keeps, as a document starts from it (PRD FR-OFF-02). */
export interface StartingCopy {
  /** The document as of the copy's seq, holding the parts that the copy holds. */
  doc: Doc;
  /** The token of the copy's write, which the next write of it compares with the one that it was made on (offline/db.ts). */
  version: string;
  /** About how many bytes the copy takes. */
  bytes: number;
}

/** Where a document finds the copy of its project that the device keeps (DocumentOptions.copies). */
export interface CopySource {
  /** Resolves to the seq of the copy of projectId that the device keeps for userId, without its entities, or to undefined for none, without rejecting. */
  seqOf(userId: string, projectId: string): Promise<number | undefined>;
  /** Resolves to that copy, or to undefined for none, without rejecting. */
  readCopy(
    userId: string,
    projectId: string,
  ): Promise<StartingCopy | undefined>;
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
  private stepUnderWay: StepUnderWay | undefined;
  /**
   * The page's batches that send has yet to settle, those that wait for a
   * step of the history under way too (send), which the document is busy
   * with until they have.
   */
  private readonly sends = new Set<Promise<SendResult>>();
  /**
   * The page's batches that send has sent, or queued, and that have yet to
   * settle, each with what its answer adds to the history, which an undo or
   * a redo asked for meanwhile waits for before it is sent, so that it sends
   * the step that is the latest once they have joined it (stepped).
   */
  private readonly batches = new Set<Promise<SendResult>>();
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
  /** What has the reads that the server derives of the whole project read again as operations apply: the writing statistics, the validation report, and the dependency graph (followed). */
  private readonly followers: readonly Follower[];
  /** What has the threads of comments read again as operations apply (followed). */
  private readonly comments: Follower;
  private unsubscribe: (() => void) | undefined;
  private disposed = false;
  /** Whether the document started from the copy of the project that the device keeps (start). */
  private copied = false;
  /**
   * The copy that the document started from, until the keeper takes it as
   * what its first write of the copy is made on (takeCopy), or the document
   * holds something else (show), after which the document holds it no
   * longer, as it would else hold the project as it was then for as long
   * as the document is kept.
   */
  private startedFrom: StartingCopy | undefined;
  /**
   * The boards whose content the document holds for the device's copy of
   * the project alone, as the keeper's download read them (load's kept), or
   * the copy that the document started from held them, which no page has
   * asked for since: a reading anew of the project reads anew, at once,
   * those that the pages asked for, and lets these go, which the keeper's
   * download reads again, at its pace (reload, offline/keeper.ts).
   */
  private readonly keptOnly = new Set<Part>();
  /** Whether the real-time channel has been live since the document subscribed. */
  private wasLive = false;
  /**
   * Settles once the channel of a document that started from the device's
   * copy is live, or a while after the document subscribed, whichever comes
   * first (start), which the document's readings anew wait for, once, as
   * the channel brings what changed since the copy's seq (refresh).
   */
  private liveWait: Promise<void> | undefined;
  /** The ID of the document as the holder of the batches that the device's queue keeps (QueuedBatch.owner). */
  private readonly owner = formatUuid(newUuid());
  /**
   * Resolves to what lets go of the document's lock as the holder of its
   * batches, once it holds it, before any is kept, so that another tab's
   * document takes none of them while this one is kept (QueueStore.hold),
   * or to undefined where the page keeps none, or the lock was not given.
   */
  private readonly ownerLock: Promise<(() => void) | undefined>;
  /**
   * Whether the browser refused the document its lock, as for a page that
   * is not fully active, after which it keeps no queue (queue), as in a
   * browser without Web Locks: another tab's document would take the
   * batches that it kept for a closed tab's, and send them while this one
   * does.
   */
  private lockRefused = false;
  /** The queue's order of the latest batch that the document made, or took (nextOrder). */
  private lastOrder = 0;
  /** The writes of the device's queue under way, one at a time, in order. */
  private saving: Promise<void> = Promise.resolve();
  /** How many writes of the device's queue are under way (busy). */
  private saves = 0;
  /**
   * Whether the document, dropped, has let go of its lock, after which
   * another tab's document may hold its batches, and it refuses to write
   * any (persist).
   */
  private letGo = false;
  /** Settles once the document, dropped, has let go of its lock (letGo, lettingGo). */
  private released: Promise<void> | undefined;
  /** What waits for the document to have no try of a batch, nor a write of the queue, under way, each until it reports so (whenIdle). */
  private idleWaits: (() => boolean)[] = [];
  /** The taking of the project's batches that the device's queue keeps, once begun (adoptQueue). */
  private adopting: Promise<void> | undefined;
  /** Stops taking the batches of the documents of other tabs that close (QueueStore.adopt). */
  private stopAdopting: (() => void) | undefined;
  /**
   * Whether the document takes the batches of other documents, as of a tab
   * that closed, as it does while a page shows it, or asks for it, but not
   * while the keeper of the copies alone holds it, which would hold them
   * where no page shows them, as the tab that the user opens the project
   * in anew would not (takeQueue).
   */
  private takes: boolean;
  /** Takes the batches that no open page's document holds, which the document left while it took none (QueueStore.adopt's again). */
  private retake: (() => Promise<void>) | undefined;
  /** The latest taking of those, once a page asked for the document again, which a read waits for, as for the first (whenReady). */
  private retaking: Promise<void> | undefined;
  /**
   * The batches that undo took out of the device's queue, the latest last,
   * which redo puts back, until the page makes another change (AC4); kept
   * by the page alone, as a reload begins anew. Each has how many steps the
   * history could redo as undo took it out (depth): a step of the history
   * undone since is the latest undone, which redo sends first (putsBack).
   * Each has the entry that it was too, which the device may yet refuse to
   * have kept, as its write was under way, after which redo puts nothing
   * back of it, as its caller was told that it failed (unwithdraw).
   */
  private withdrawn: {
    commands: Command[];
    history: boolean;
    depth: number;
    entry: Entry;
  }[] = [];
  /** Counts the page's changes of the history's that send was asked for, after each of which redo puts back nothing that undo took out of the queue before it (putBack). */
  private changesMade = 0;
  /** The timer of the queue's next try, while the server gives no answer, or refuses for when it was asked. */
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  /**
   * Why the queue waits for its next try (retryTimer), or tries again: no
   * answer came, as the page cannot reach the server, which it holds to
   * until a try is answered, through the try itself, or the server refused
   * for when or by whom the batch was sent, as it answers all the same
   * (holdReason); undefined once the server has answered since.
   */
  private retryReason: "unanswered" | "refused" | undefined;
  /**
   * Whether the try under way has had no answer for answerWait (stalled),
   * after which the page cannot reach the server until the try settles
   * (unreachable): its own, which neither the channel live again nor an
   * answer to another try ends, nor a wait after a refusal that it has
   * (retryReason), which goes on.
   */
  private stalledTry = false;
  /**
   * Whether the read of the project that a step of the history under way
   * makes before it is sent has had no answer for answerWait
   * (givingWayAfter), after which the page cannot reach the server until
   * the read settles (unreachable), as through a try that none came to.
   */
  private stalledRead = false;
  /**
   * How many tries in a row the server gave no answer to, or refused for
   * when or by whom they were sent, which spaces the next (backoff): an
   * answer of the server's own ends a run of those that no answer came to
   * (reached).
   */
  private retryFailures = 0;
  /** Counts the changes of what the sync panel lists (DocumentState.queueVersion), and of what undo and redo would take out of the queue, or put back. */
  private queueChanges = 0;
  /** Counts the queue's batches put on their way, which the sync panel lists as being sent, and which change nothing that undo or redo would do (DocumentState.queueVersion). */
  private queueSends = 0;
  /** Stops following the browser's connection and the page's session, which the queue waits for. */
  private readonly stopFollowing: (() => void)[] = [];

  constructor(projectId: string, options: DocumentOptions) {
    this.projectId = projectId;
    this.options = options;
    this.followers = [
      new Follower(
        options.queryClient,
        statisticsKey(projectId),
        statisticsPause,
      ),
      new Follower(options.queryClient, validationKey(projectId), derivedPause),
      new Follower(
        options.queryClient,
        dependenciesKey(projectId),
        derivedPause,
      ),
    ];
    this.comments = new Follower(
      options.queryClient,
      commentsKey(projectId),
      commentsPause,
    );
    this.now = options.now ?? (() => new Date().toISOString());
    this.takes = options.takesQueue ?? true;
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
      queued: 0,
      review: 0,
      holdsQueued: false,
      sendsQueued: true,
      holding: false,
      undoLocal: false,
      redoLocal: false,
      queueVersion: 0,
    }));
    const queue = this.queue;
    this.ownerLock =
      queue === undefined
        ? Promise.resolve(undefined)
        : queue.hold(this.owner).catch(() => {
            this.lockRefused = true;
            this.show();
            return undefined;
          });
    if (queue !== undefined) {
      // The queue is sent once the browser is online again, and once the
      // page has its own session, as one that started offline asks for it;
      // not one that waits after a refusal for when it was sent, as of too
      // many requests, which a try at once would meet again, as the channel
      // live again does not either.
      const onConnection = options.onConnection;
      if (onConnection !== undefined) {
        this.stopFollowing.push(
          onConnection((online) => {
            if (online && this.retryReason === "refused") {
              this.tryNow();
            } else if (online) {
              this.tryAgainNow();
            } else {
              this.show();
            }
          }),
        );
      }
      // The page that has its own session reaches the server, which what
      // needs it says (holding), whether or not the queue holds any batch.
      this.stopFollowing.push(
        options.queryClient.getQueryCache().subscribe((event) => {
          if (
            event.query.queryKey[0] === sessionQuery.queryKey[0] &&
            event.type === "updated"
          ) {
            this.show();
            void this.pump();
          }
        }),
      );
    }
    this.starting = this.begin();
  }

  /** The device's queue of the batches that the server has yet to answer, where the page keeps one, knows its account, and holds the document's lock (lockRefused). */
  private get queue(): QueueStore | undefined {
    return this.userId === undefined || this.lockRefused
      ? undefined
      : this.options.queue;
  }

  /** Settles once the project has been read, and the document follows it, with the batches of the device's queue that it took. */
  get ready(): Promise<void> {
    return this.withTaken(this.starting);
  }

  /** Returns starting, settling once the batches that the document takes anew, if any, are taken too (retaking). */
  private withTaken(starting: Promise<void>): Promise<void> {
    const retaking = this.retaking;
    return retaking === undefined
      ? starting
      : Promise.all([starting, retaking]).then(() => undefined);
  }

  /** The document as the server has confirmed it. */
  get doc(): Doc {
    return this.confirmed;
  }

  /** Whether the document started from the copy of the project that the device keeps, rather than from the server alone. */
  get fromCopy(): boolean {
    return this.copied;
  }

  /**
   * Returns the copy of the project that the device keeps that the document
   * started from, once, for the keeper's first write of the copy to be made
   * on (offline/keeper.ts), after which the document holds it no longer; or
   * undefined, where it started from the server alone, or the copy has been
   * taken, or the document has changed since (show).
   */
  takeCopy(): StartingCopy | undefined {
    const copy = this.startedFrom;
    this.startedFrom = undefined;
    return copy;
  }

  /**
   * Reports whether the document has read the project, and has nothing left
   * to read from the server of what it holds: no part, component, or entity
   * that a read, or a reading anew, is under way of, or waits to be read, as
   * one whose change an operation's appliers could not know, which it holds
   * at the operation's revision, as they predicted it, until the read has
   * corrected it; and it has the operations up to the seq that its
   * subscription held as of, which the channel sends once it is live, so
   * that a document that is catching up is not taken for the server's as of
   * an operation on the way. What the document holds then is what the
   * server had as of its seq, which the device's copy of it may be written
   * of (offline/keeper.ts).
   */
  get settled(): boolean {
    return (
      this.started &&
      !this.startFailed &&
      !this.disposed &&
      this.buffer === undefined &&
      this.resyncing === undefined &&
      this.confirmed.seq >= this.subscribedTo &&
      this.partReads === 0 &&
      this.seqChecks === 0 &&
      this.toRead.size === 0 &&
      !this.reading &&
      this.rereads.size === 0
    );
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
    return this.withTaken(this.starting);
  }

  /**
   * Reports whether the document is reading the project, or a part of it,
   * or reading it anew, which a route's loader may be waiting for (refresh),
   * or the project's seq, which tells whether the history kept over a
   * reload is of what the server has (seqChecks), or sending the page's
   * batches, or a step of the history, whose answers join the history,
   * which a document dropped keeps no longer in the tab's storage (dispose),
   * or holding batches that the device's queue keeps, which it sends once
   * the server answers, rather than leave them for the next page that opens
   * the project, or keeping them there.
   */
  get busy(): boolean {
    return (
      !this.started ||
      this.partReads > 0 ||
      this.refreshes > 0 ||
      this.seqChecks > 0 ||
      this.resyncing !== undefined ||
      this.sends.size > 0 ||
      this.stepUnderWay !== undefined ||
      this.holdsQueued() ||
      this.saves > 0
    );
  }

  /**
   * Reports whether the device's queue keeps batches of the document's that
   * the server has yet to answer: one that it has answered is the queue's no
   * longer, though the page shows it until the document has its operation
   * (settle).
   */
  private holdsQueued(): boolean {
    return this.entries.some(
      (e) => e.held !== undefined && e.answer === undefined,
    );
  }

  private async start(): Promise<void> {
    // The copy of the project that the device keeps, where it keeps one
    // (PRD FR-OFF-02), which the document starts from, holding every part
    // that it holds as of its seq, whose operations since the channel
    // brings, as it does those of a document that the socket missed, rather
    // than have the parts read anew: where the copy is as of the project's
    // seq, as of a project that has not changed since it was written, and,
    // while the browser has no connection, of any seq before. Online, a copy
    // behind the project's seq is not used: the document reads the project
    // from the server, as without a copy, rather than bring the copy up to
    // it. A copy of a seq past the project's is of what the server no longer
    // has, as a database restored from an older backup, and is passed over.
    // The copy's seq is read alone first, while the project is, which tells
    // whether the copy is used, and the whole of it only where it is, so
    // that a copy passed over costs the start no read of its entities,
    // which the browser would hand the page as it loads, for nothing.
    const userId = this.userId;
    const copies = this.options.copies;
    const peeking =
      userId === undefined || copies === undefined
        ? undefined
        : copies.seqOf(userId, this.projectId).catch(() => undefined);
    // The batches of the project that the device's queue keeps and that no
    // open page's document holds, as of a tab that closed, or a browser that
    // restarted (PRD FR-OFF-03 AC1), which the page shows on top of what the
    // server confirmed, and sends, as its own, read while the project is.
    const adopting = this.adoptQueue();
    let body = await this.options.queryClient.ensureQueryData(
      projectQuery(this.projectId),
    );
    const seq = await peeking;
    // The read of the project that the query client holds may be older than
    // the copy, as one kept from before another tab wrote it, whose seq the
    // client's read does not follow but as the project itself changes
    // (projectFollowed): the project is read anew before the copy is passed
    // over, which, offline, gives the copy's own (offline/kept.ts).
    if (seq !== undefined && seq > body.seq && !this.disposed) {
      const held = body;
      body = await this.options.queryClient
        .fetchQuery({ ...projectQuery(this.projectId), staleTime: 0 })
        .catch(() => held);
    }
    if (this.disposed) {
      return;
    }
    const used =
      seq !== undefined &&
      (seq === body.seq || (seq < body.seq && !this.isOnline()));
    const copy =
      used && userId !== undefined && copies !== undefined
        ? await copies.readCopy(userId, this.projectId).catch(() => undefined)
        : undefined;
    if (this.disposed) {
      return;
    }
    // The copy that the whole read found is of the seq that the start
    // decided on, unless another tab wrote it anew between the two reads.
    if (
      copy !== undefined &&
      copy.doc.project?.id === this.projectId &&
      copy.doc.seq === seq
    ) {
      this.copied = true;
      this.startedFrom = copy;
      this.confirmed = copy.doc;
      // A part that a page asked for meanwhile waits for this, and finds
      // that the document holds it (load); a board that none has asked for
      // is held for the copy alone, until one does (keptOnly).
      for (const part of copy.doc.parts) {
        if (!this.loads.has(part)) {
          const read = Promise.resolve();
          this.loads.set(part, read);
          this.finished.add(read);
          if (part.startsWith("board:")) {
            this.keptOnly.add(part);
          }
        }
      }
    } else {
      this.confirmed = { ...emptyDoc(body.seq), project: projectOf(body) };
    }
    await adopting;
    if (this.disposed) {
      return;
    }
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
        } else {
          this.wasLive = true;
        }
        this.store.setState({ live });
        // The server answers again: the device's queue, which waited for an
        // answer, is sent at once, rather than once its next try is due, and
        // the page reaches it; not one that waits after a refusal for when
        // it was sent, as of too many requests, which was an answer, and
        // which a try at once would meet again; nor through a try under way
        // that no answer came to for answerWait, as on the connection that a
        // network change left behind, after which the batches made meanwhile
        // are kept still (stalledTry).
        if (live && this.retryReason === "unanswered") {
          this.retryReason = undefined;
          this.tryAgainNow();
        }
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
    // A part's reading anew waits for the channel once, which brings what
    // changed since the copy's seq (refresh).
    if (this.copied) {
      this.liveWait = this.whenLive(confirmWait);
    }
    // The batches that the device's queue kept are sent, once the server
    // answers.
    void this.pump();
  }

  /**
   * Takes the project's batches that the device's queue keeps, of no open
   * page's document, as the document's own, once, which the page shows on
   * top of what the server confirmed, before any that it made since, and
   * from then on those of another tab's document that closes, after its
   * own; resolves once it has taken the first, at once where the page keeps
   * no queue, and never rejects.
   */
  private adoptQueue(): Promise<void> {
    this.adopting ??= this.adoptOnce();
    return this.adopting;
  }

  private async adoptOnce(): Promise<void> {
    const queue = this.queue;
    const userId = this.userId;
    if (queue === undefined || userId === undefined) {
      return;
    }
    // A batch is taken as the document's once it holds its lock, so that
    // another tab's takes none of what it takes; and none once it has been
    // dropped, as while the database opened, which would take them as one
    // that has let go of its lock, and send none, leaving them to the
    // project's next document (dispose).
    if ((await this.ownerLock) === undefined || this.disposed) {
      return;
    }
    const taken = await queue
      .adopt(
        userId,
        this.projectId,
        this.owner,
        (batches) => this.adoptMore(batches),
        () => this.takes && !this.disposed,
      )
      .catch(() => undefined);
    if (taken === undefined) {
      return;
    }
    if (this.disposed) {
      taken.stop();
      return;
    }
    this.stopAdopting = taken.stop;
    this.retake = taken.again;
    for (const batch of taken.batches) {
      this.lastOrder = Math.max(this.lastOrder, batch.order);
    }
    this.entries = [
      ...taken.batches.map((b) => this.heldEntry(b)),
      ...this.entries,
    ];
    this.queueChanged();
  }

  /**
   * Has the document take the batches of other documents that the device's
   * queue keeps, as of a tab that closed, while takes says so, as while a
   * page shows it, or has asked for it a moment ago, rather than while the
   * keeper of the copies alone holds it (Documents), which would hold them
   * where no page shows them, as the page that the user opens the project
   * in anew, in this tab or another, would not; and, once it does again,
   * those that it left meanwhile, which a read waits for (whenReady), as it
   * does for those taken as the document started. Those that it took
   * before it keeps, and sends.
   */
  takeQueue(takes: boolean): void {
    if (this.takes === takes || this.disposed) {
      return;
    }
    this.takes = takes;
    if (takes) {
      const adopting = this.adopting ?? Promise.resolve();
      this.retaking = adopting.then(() => this.retake?.());
    }
  }

  /**
   * Takes batches of another tab's document that closed, after the
   * document's own, which none of them was made on top of (moveQueued), and
   * sends them, undo taking each out of the queue in the order that they
   * were made, among the document's own (withdrawable), once no step of the
   * history is under way: one whose answer joined the history meanwhile
   * would be the latest change, past the step that the step under way
   * found, which it would then send over it (stepped).
   */
  private adoptMore(batches: QueuedBatch[]): void {
    if (this.disposed) {
      return;
    }
    const underWay = this.stepUnderWay;
    if (underWay !== undefined) {
      // Taken as the document's all the same, which, once dropped meanwhile,
      // leaves them to the next that takes its batches (dispose).
      const after = () => this.adoptMore(batches);
      void underWay.done.then(after, after);
      return;
    }
    for (const batch of batches) {
      this.lastOrder = Math.max(this.lastOrder, batch.order);
    }
    this.entries = [...this.entries, ...batches.map((b) => this.heldEntry(b))];
    this.queueChanged();
    void this.pump();
  }

  /** Returns the entry of a batch that the device's queue keeps, as its document holds it. */
  private heldEntry(batch: QueuedBatch): Entry {
    return {
      commands: batch.commands,
      key: batch.key,
      time: batch.time,
      base: new Map(
        batch.base.map(([id, b]) => [
          id,
          {
            entity: (b.entity ?? undefined) as Revised | undefined,
            exact: b.exact,
          },
        ]),
      ),
      before: new Map(batch.before),
      parts: new Set(batch.parts),
      retries: batch.retries,
      known:
        batch.known === undefined
          ? undefined
          : { latest: batch.known, restores: this.restores },
      step: false,
      history: batch.history,
      maker: batch.maker,
      atop: batch.atop,
      order: batch.order,
      // A batch that needs review is not sent until the user decides.
      sent: false,
      held: batch,
      stored: true,
      resolve: () => {},
      reject: () => {},
    };
  }

  /**
   * Resolves once the real-time channel is live, or has been since the
   * document subscribed, or after wait milliseconds, whichever comes first,
   * leaving no timer, nor listener, behind (whenConfirmed).
   */
  private whenLive(wait: number): Promise<void> {
    if (this.wasLive || this.store.getState().live) {
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      let stop = () => {};
      const done = () => {
        stop();
        clearTimeout(timer);
        resolve();
      };
      const timer = setTimeout(done, wait);
      stop = this.store.subscribe((state) => {
        if (state.live || this.disposed) {
          done();
        }
      });
    });
  }

  /**
   * Resolves once the document has no try of a batch under way, nor a write
   * of the device's queue, nor a batch of the page's that it has yet to send,
   * or keep, nor a step of the history (idleChanged).
   */
  private whenIdle(): Promise<void> {
    return new Promise((resolve) => {
      const idle = () => {
        if (
          this.inflight ||
          this.saves > 0 ||
          this.sends.size > 0 ||
          this.stepUnderWay !== undefined
        ) {
          return false;
        }
        resolve();
        return true;
      };
      if (!idle()) {
        this.idleWaits.push(idle);
      }
    });
  }

  /** Lets what waits for the document to be idle go on, once it is (whenIdle). */
  private idleChanged(): void {
    if (this.idleWaits.length > 0) {
      this.idleWaits = this.idleWaits.filter((idle) => !idle());
    }
  }

  /**
   * Resolves once the document, dropped, has let go of its lock, after which
   * it writes nothing of the device's queue (letGo), or at once where it
   * was never dropped.
   */
  lettingGo(): Promise<void> {
    return this.released ?? Promise.resolve();
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
    clearTimeout(this.retryTimer);
    this.retryTimer = undefined;
    for (const stop of this.stopFollowing.splice(0)) {
      stop();
    }
    this.stopAdopting?.();
    // The lock goes once the try under way, which may have reached the
    // server, has been answered, and the page's batches that the document
    // has yet to send, or keep, as those that wait for a step of the
    // history, have been, with the writes that follow, so that another
    // tab's document takes the batches as they are written, rather than
    // send one while this one does, and none is written after, which no
    // document would take before the project's next.
    this.released = this.whenIdle()
      .then(() => this.ownerLock)
      .then((release) => {
        this.letGo = true;
        release?.();
      });
    for (const follower of this.followers) {
      follower.dispose();
    }
    this.comments.dispose();
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    this.history.close();
    // A document dropped sends none of the queue's, which a read that waits
    // for the page's batches waits for no longer (sendsQueued).
    this.show();
  }

  /**
   * Reads part of the project, once, and keeps it current from then on,
   * resolving once it has, and has taken the batches of the device's queue
   * that it takes; a part being read resolves with that read. A read that
   * fails takes the part out again, so that the next asks anew.
   * Where kept says so, a board's content is read for the device's copy of
   * the project alone, as the keeper's download reads it, until a page asks
   * for it too (keptOnly).
   */
  load(part: Part, kept = false): Promise<void> {
    if (!kept) {
      this.keptOnly.delete(part);
    }
    const loading = this.loads.get(part);
    if (loading !== undefined) {
      // A part that the document holds, as one read before, or of the
      // device's copy, resolves once the document has taken the batches of
      // the device's queue that it is taking, if any, as the first read of a
      // part does (whenReady), which the page shows on top of it: once a page
      // asks again for a document that the keeper of the copies alone held,
      // as a board made offline in a tab that closed meanwhile, whose page
      // would else say that there is none (readBoard).
      return this.adopting === undefined && this.retaking === undefined
        ? loading
        : Promise.all([loading, this.adopting, this.retaking]).then(
            () => undefined,
          );
    }
    if (kept && part.startsWith("board:")) {
      this.keptOnly.add(part);
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
        // The device's copy that the document started from holds the part,
        // as a page asked for it before the copy had been read, which the
        // document keeps current from then on.
        if (this.copied && this.confirmed.parts.has(part)) {
          return;
        }
        // A board that the server does not have yet, as one that a batch
        // made offline creates, has no content as of what the document
        // holds, which the batches on top give it: the page shows it with
        // no read, which would fail, offline, or find none.
        if (this.createdUnanswered(part)) {
          this.confirmed = withParts(this.confirmed, [part], []);
          this.show();
          return;
        }
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
          this.keptOnly.delete(part);
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
   * While the browser has no connection, it reads nothing, and resolves at
   * once: what the document holds, as the device's copy of the project, is
   * what there is to show, and the channel brings what changed once the
   * browser is online again (PRD FR-OFF-02). A document that started from
   * the copy waits for the channel first, which brings what changed since
   * the copy's seq, rather than read the part anew because the socket is
   * still being opened, as while the page loads: until it is live, or a
   * while after the document subscribed, once (liveWait), so that a channel
   * that never is, as behind a proxy that refuses the socket, does not
   * hold up every reading anew after.
   */
  async refresh(part?: Part): Promise<void> {
    if (!this.isOnline()) {
      return;
    }
    this.refreshes++;
    try {
      if (this.liveWait !== undefined && this.options.realtime !== undefined) {
        await this.liveWait;
      }
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
   * Reports whether part is the content of a board that the document does
   * not hold, which a batch of the page's creates that the server has yet
   * to answer, as one that the device's queue keeps: as of what the server
   * confirmed, it has none, and a read of it, which the server answers as
   * of a board that it has not, is none either (load, reload, rereadOnce).
   * Where answered says so, as of a read that the server answered as of no
   * such board, the batch may have been answered since, as the queue is
   * sent while the project is read anew, its operation, which gives the
   * board, and those after it, its content, yet to come (reload).
   */
  private createdUnanswered(part: Part, answered = false): boolean {
    if (!part.startsWith("board:")) {
      return false;
    }
    const id = part.slice("board:".length);
    return (
      !this.confirmed.boards.has(id) &&
      this.entries.some(
        (e) =>
          (answered || e.answer === undefined) &&
          e.commands.some(
            (c) => c.type === "board.create" && canonical(c.payload.id) === id,
          ),
      )
    );
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
      this.keptOnly.delete(part);
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
   * it, as an unarchive's archived_at; the trash, which a deletion or a
   * restoration of the draft's changes; the saved play states, which their
   * commands change and the document holds none of (play/queries.ts); the
   * threaded comments, which their commands change, and nearly any other
   * operation may change what they show of, as the deletion or the
   * restoration of what they are on, whose threads are in the trash with
   * it, though nothing writes them then, a node's title, a board's name, or
   * a connection's node, by which a link opens one (comments/queries.ts);
   * and the reads that the server derives of the whole project, which any
   * change of the draft may: the writing statistics, the validation report,
   * and the dependency graph (Follower). What is no part of the draft
   * (outsideDraft) changes neither the trash nor what the server derives.
   */
  private followed(
    op: Operation | undefined,
    before: Project | undefined,
    role?: ProjectBody["role"],
    readAt?: number,
  ): void {
    this.projectFollowed(before, role, readAt);
    const commands = op?.commands ?? [];
    // Reports whether the operation's commands are all of what is no part
    // of the draft (outsideDraft), of the prefix of, if given, as those of
    // the saved play states alone, which change nothing that the other
    // reads show.
    const only = (of?: (typeof outsideDraft)[number]) =>
      op !== undefined &&
      commands.length > 0 &&
      commands.every((c) => outside(c, of));
    if (
      op === undefined ||
      commands.some(
        (c) => !outside(c) && /\.(delete|restore)(_many)?$/.test(c.type),
      )
    ) {
      void this.options.queryClient.invalidateQueries({
        queryKey: ["projects", this.projectId, "trash"],
      });
    }
    if (op === undefined || commands.some((c) => outside(c, "play_state."))) {
      void this.options.queryClient.invalidateQueries({
        queryKey: playStatesKey(this.projectId),
      });
    }
    if (!only("play_state.")) {
      this.comments.follow();
    }
    if (!only()) {
      for (const follower of this.followers) {
        follower.follow();
      }
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
    // The copy that the document started from is let go once the document
    // holds something else, as in a tab whose keeper does not write the
    // copies, which would else hold the project as it was then for as long
    // as the document is kept: a first write made on it then writes the
    // whole document (takeCopy).
    if (
      this.startedFrom !== undefined &&
      this.startedFrom.doc !== this.confirmed
    ) {
      this.startedFrom = undefined;
    }
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
    let queued = 0;
    let review = 0;
    for (const entry of this.entries) {
      // A batch that the server has answered is the queue's no longer, though
      // the page shows it until the document has its operation (settle), and
      // one is the queue's only once the device has written it, which a
      // reload would cut off before (whenSent).
      if (
        entry.held !== undefined &&
        entry.stored === true &&
        entry.answer === undefined
      ) {
        queued++;
        if (entry.held.state === "review") {
          review++;
        }
      }
    }
    const local = this.withdrawableCount();
    this.store.setState({
      view,
      sending:
        this.entries.filter((e) => e.answer === undefined).length +
        this.waiting.length,
      undoable: this.history.undoable + local,
      redoable: this.history.redoable + this.withdrawn.length,
      // The steps that undo and redo would take out of the device's queue,
      // or put back, are steps too.
      historyVersion: this.history.version + this.queueChanges,
      queued,
      review,
      holdsQueued: this.holdsQueued(),
      sendsQueued: !this.disposed && this.holdReason() === undefined,
      holding: this.unreachable() !== undefined,
      undoLocal: local > 0,
      redoLocal: this.putsBack(),
      queueVersion: this.queueChanges + this.queueSends,
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
      // opened many boards. A board held for the device's copy alone is not
      // (keptOnly): the keeper's download reads it again, at its pace, once
      // the document read anew no longer holds it, rather than have every
      // board of a project that the device keeps read at once, again.
      const read = new Set<Part>();
      for (;;) {
        const parts = [...this.loads.keys()].filter(
          (p) => !read.has(p) && !this.keptOnly.has(p),
        );
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
                // A board that a batch of the page's creates, which the
                // server had yet to apply as it read it, as one that it
                // has answered since, has no content there yet, and is
                // held with none, which the operations after the read give
                // it (createdUnanswered); another went to the trash, by an
                // operation that the document has yet to apply, which takes
                // its part out.
                if (this.createdUnanswered(part, true)) {
                  lists.set(part, []);
                } else {
                  this.loads.delete(part);
                }
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
    // The boards held for the device's copy alone, which the project read
    // anew did not read, are held no longer, as of the document that it
    // takes, before anything that it tells of it could ask for one.
    for (const part of this.keptOnly) {
      this.loads.delete(part);
    }
    this.keptOnly.clear();
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
      // A board that the server has none of yet, as one that a batch made
      // offline creates, has none of its content to read (createdUnanswered).
      if (!(notFoundBy(error) && this.createdUnanswered(part))) {
        this.readFailed(error, generation);
      }
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
    // A request that no answer came to while the browser has no connection
    // is no error of the app's: the device's copy is what the page shows
    // then (PRD FR-OFF-02).
    if (
      record &&
      !signedOutBy(error) &&
      !(error instanceof NetworkError && !this.isOnline())
    ) {
      this.options.log.record("request", error);
    }
  }

  /** Reports whether the browser says that it has a connection (DocumentOptions.online), without which the document reads nothing anew (refresh). */
  isOnline(): boolean {
    return this.options.online?.() ?? true;
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
   * undo joins the history, unless options say that it does not, as of a
   * comment's commands, which are no step of the page's undo (plan item 74):
   * the history is then as it was, its redo too. A batch of the draft that
   * the page cannot send now, as offline, or that no answer comes to, is
   * kept in the device's queue, where the page keeps one, and resolves as
   * queued once it is (PRD FR-OFF-03), the page showing it all the same:
   * its answer, once the queue is sent, joins the history as any does.
   */
  send(
    commands: readonly Command[],
    options: SendOptions = {},
  ): Promise<SendResult> {
    // A new change, after which what undo took out of the device's queue is
    // not put back (AC4).
    if (options.history !== false) {
      this.changesMade++;
      if (this.withdrawn.length > 0) {
        this.withdrawn = [];
        this.queueChanged();
      }
    }
    return this.sendTracked(commands, options);
  }

  /** Sends a batch as send does, without ending what redo would put back, as redo itself sends one. */
  private sendTracked(
    commands: readonly Command[],
    options: SendOptions,
  ): Promise<SendResult> {
    const sending = this.sendAfterSteps(commands, undefined, options);
    this.sends.add(sending);
    const settled = () => {
      this.sends.delete(sending);
      this.idleChanged();
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
   * meanwhile, as they are among themselves (step), and the page shows it
   * at once all the same, on top of what is queued (waiting), as a board
   * dropped in the tree where it was dropped, rather than where it was
   * until the step has settled. A step whose read of the server, or whose
   * try, or a try that it waits for, has had no answer for answerWait,
   * which the browser may take minutes to give up on, gives way
   * (givingWayAfter, stalled): the batch is queued now, behind that try, or
   * the read, before the step, which is then not sent (stepped), or after
   * its try, and so is kept in the device's queue, as the page cannot reach
   * the server until the try, or the read, settles, as are the batches
   * after any try that no answer comes to, rather than in the page alone,
   * which a tab that closes meanwhile would lose (PRD FR-OFF-03); but for
   * those of a step that the server refuses as too large, which it sends in
   * parts, and whose parts after then come after the batch, which may change
   * what they undo.
   */
  private sendAfterSteps(
    commands: readonly Command[],
    waiting: Waiting | undefined,
    options: SendOptions,
  ): Promise<SendResult> {
    const underWay = this.stepUnderWay;
    if (underWay !== undefined && underWay.given !== true) {
      let held = waiting;
      if (held === undefined) {
        held = { commands, time: this.now() };
        this.waiting.push(held);
        this.show();
      }
      // Once the step has settled, in the order that what waits for it was
      // asked for, as a step of the other asked for after it (step), or
      // once it gives way, whichever comes first.
      return new Promise<SendResult>((resolve) => {
        let gone = false;
        const after = () => {
          if (!gone) {
            gone = true;
            resolve(this.sendAfterSteps(commands, held, options));
          }
        };
        underWay.done.then(after, after);
        void underWay.gaveWay?.then(after);
      });
    }
    if (waiting !== undefined) {
      // The batch is queued now, which the page shows in its place, as of
      // the time that it was made at, which the page showed it with.
      this.waiting = this.waiting.filter((w) => w !== waiting);
    }
    // One that goes on as the step under way gave way goes before it, which
    // is then not sent after it; a step that none went before is sent once
    // what it waited for has settled, as it was asked for (stepped).
    if (underWay !== undefined) {
      underWay.passed = true;
    }
    const batch = this.sendBatch(commands, waiting?.time, options);
    this.batches.add(batch);
    const settled = () => {
      this.batches.delete(batch);
    };
    batch.then(settled, settled);
    return batch;
  }

  /**
   * Sends a batch as send does, made at time, or now, its undo joining the
   * history once it has applied, before it resolves, unless options say
   * that it does not (joinHistory).
   */
  private async sendBatch(
    commands: readonly Command[],
    time: string | undefined,
    options: SendOptions,
  ): Promise<SendResult> {
    const sent = await this.enqueue(
      commands,
      conflictRetries,
      false,
      time,
      options.history !== false,
    );
    return "answer" in sent ? sent.answer : sent;
  }

  /**
   * Adds the undo of entry, which applied with answer, to the history, as
   * the user's latest change: as its caller hears of it, or, of a batch of
   * the device's queue, whose caller was told that it is queued, as the
   * queue is sent.
   */
  private joinHistory(entry: Entry, answer: CommandResultBody): void {
    if (answer.status !== "applied" || !entry.history) {
      return;
    }
    this.history.done(
      answer.undo == null || answer.undo.length === 0
        ? undefined
        : { commands: answer.undo, seqs: [answer.seq] },
    );
    if (entry.captured !== undefined && answer.undo?.length) {
      this.history.capture(answer.seq, entry.captured);
    }
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
   * Resolves once the page shows what the batch answered with seq did of
   * part, of an operation that has the document read part again, as a paste
   * has the board that it pastes onto read (the clipboard.paste applier),
   * whose nodes the page shows only then: once the document is as of seq,
   * or after confirmWait, as while another session's operation before it is
   * slow to come, and then once part, where the document is reading it
   * again, has been read, whatever else it is reading again meanwhile, as
   * the variables that the batch created too, which the page waits for no
   * more than for any other change.
   */
  async shown(seq: number, part: Part): Promise<void> {
    await this.whenConfirmed(seq, confirmWait);
    const reading = this.rereads.get(part);
    if (reading !== undefined) {
      await Promise.allSettled([reading.done]);
    }
  }

  /**
   * Resolves once the page's batches that send has sent or queued so far,
   * and the step of the history under way, have settled, as a request that
   * reads what the server has, rather than what the page shows, waits for
   * first, so that it reads what they change too: the clipboard's copy of a
   * node that the page has just added, moved, or changed (plan note N71),
   * which the server would otherwise copy as it was, or not at all.
   */
  async sent(): Promise<void> {
    await Promise.allSettled([
      ...this.sends,
      ...(this.stepUnderWay === undefined ? [] : [this.stepUnderWay.done]),
    ]);
    // And the batches of the device's queue, while it is sent, as once the
    // browser is online again, but not while it is kept for want of the
    // server, or of the user's review, or until a try after a refusal, which
    // may be for long (holdReason).
    await new Promise<void>((resolve) => {
      const done = () =>
        this.store.getState().queued === 0 ||
        this.holdReason() !== undefined ||
        this.disposed;
      if (done()) {
        resolve();
        return;
      }
      const stop = this.store.subscribe(() => {
        if (done()) {
          stop();
          resolve();
        }
      });
    });
  }

  /**
   * Resolves once the writes of the device's queue that the document has
   * begun so far are done, kept or refused, as of a change that it was
   * still keeping, which signing out reads the device after, so that it
   * names that change too (Documents.whenWritten); it never rejects.
   */
  queueWritten(): Promise<void> {
    return this.saving;
  }

  /**
   * Returns the batches of the page's that the server has yet to answer, in
   * the order that they are sent, as the sync panel lists them (PRD
   * FR-OFF-06): those of the device's queue, and those on their way, or
   * waiting for a step of the history.
   */
  pendingBatches(): PendingBatch[] {
    const pending: PendingBatch[] = [];
    for (const entry of this.entries) {
      if (entry.answer !== undefined) {
        continue;
      }
      const held = entry.held;
      pending.push({
        id: held?.id ?? entry.commands[0]?.command_id,
        time: entry.time,
        commands: entry.commands,
        // A batch of the queue on its way is being sent, which the device
        // records as sent before it is (pump).
        state:
          held === undefined || entry.sent
            ? "sending"
            : held.state === "review"
              ? "review"
              : "queued",
        sent: held?.sent ?? entry.sent,
        ...(held?.refusal === undefined ? {} : { refusal: held.refusal }),
      });
    }
    for (const waiting of this.waiting) {
      pending.push({
        id: undefined,
        time: waiting.time,
        commands: waiting.commands,
        state: "sending",
        sent: false,
      });
    }
    return pending;
  }

  /**
   * Returns the entry that undo takes out of the device's queue, which needs
   * no connection (AC4): the page's latest batch, once the queue keeps it,
   * a change of the user's, as long as it never reached the server, as one
   * that the page has not sent, nor sent and got no answer to, which may
   * have applied, and the server has not refused for what it does. The
   * latest is the one made last (latestFirst), as of another tab's batches
   * that the document took after its own as that tab closed, which may have
   * been made before them, and which none was made on top of.
   */
  private withdrawable(): Entry | undefined {
    const latest = this.latestFirst()[0];
    return latest !== undefined && this.withdraws(latest) ? latest : undefined;
  }

  /** Returns the page's batches, the one made last first, by the queue's order (Entry.order). */
  private latestFirst(): Entry[] {
    return [...this.entries].reverse().sort((a, b) => {
      if (a.order !== b.order) {
        return b.order - a.order;
      }
      // As two made at once in two tabs (inQueueOrder).
      const ids = [a.held?.id ?? "", b.held?.id ?? ""] as const;
      return ids[0] === ids[1] ? 0 : ids[0] < ids[1] ? 1 : -1;
    });
  }

  /** Reports whether undo would take entry out of the device's queue, were it the latest (withdrawable). */
  private withdraws(entry: Entry): boolean {
    const held = entry.held;
    return (
      held !== undefined &&
      entry.history &&
      !entry.sent &&
      !held.sent &&
      held.state === "pending" &&
      entry.answer === undefined
    );
  }

  /** How many of the page's latest batches undo would take out of the device's queue in turn (withdrawable). */
  private withdrawableCount(): number {
    let count = 0;
    for (const entry of this.latestFirst()) {
      if (!this.withdraws(entry)) {
        break;
      }
      count++;
    }
    return count;
  }

  /**
   * Takes entry out of the device's queue, and out of what the page shows,
   * as the undo of a change that never reached the server, and keeps its
   * commands for redo, resolving once the queue no longer keeps it, or
   * rejecting, with entry back in its place, where the device refused.
   */
  private async withdraw(entry: Entry): Promise<boolean> {
    const held = entry.held as QueuedBatch;
    this.entries = this.entries.filter((e) => e !== entry);
    const undone = {
      commands: entry.commands.map(
        ({ command_id: _, ...command }) => command as Command,
      ),
      history: entry.history,
      depth: this.history.redoable,
      entry,
    };
    this.withdrawn.push(undone);
    this.queueChanged();
    try {
      await this.persist([held.id]);
    } catch (error) {
      // The device still keeps it, which the page would send once it loads
      // anew: it is kept, as the latest, unless the page has made another
      // change meanwhile, after which it goes back behind those; but not
      // one that the device refused to have kept meanwhile, whose caller
      // was told that it failed (keep).
      this.withdrawn = this.withdrawn.filter((w) => w !== undone);
      if (!this.disposed && entry.held !== undefined) {
        this.entries = [...this.entries, entry];
        this.queueChanged();
        void this.pump();
      }
      throw error;
    }
    return true;
  }

  /**
   * Puts the batch that undo took out of the device's queue last back, as a
   * change of the page's anew, resolving once it is queued or answered. One
   * refused for when it was sent, as with too many requests, or that the
   * device refused to keep, as for its quota, may be put back later: redo
   * puts it back still, as the history keeps a step refused so (stepped),
   * unless the page has made another change meanwhile (send); one refused
   * for what it does would be refused again.
   */
  private async putBack(): Promise<boolean> {
    const batch = this.withdrawn.pop();
    if (batch === undefined) {
      return false;
    }
    const made = this.changesMade;
    this.queueChanged();
    try {
      await this.sendTracked(batch.commands, { history: batch.history });
    } catch (error) {
      if (
        !refusedForGood(error) &&
        !this.disposed &&
        this.changesMade === made
      ) {
        this.withdrawn.push(batch);
        this.queueChanged();
      }
      throw error;
    }
    return true;
  }

  /**
   * Reports whether redo would put back the batch that undo took out of the
   * device's queue last, rather than send the history's step: unless a step
   * of the history was undone since, which is the latest undone, as once
   * the queue before it was sent and that change undone on the server.
   */
  private putsBack(): boolean {
    const latest = this.withdrawn.at(-1);
    return latest !== undefined && this.history.redoable <= latest.depth;
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
    // The latest change that the device's queue keeps and that never reached
    // the server is undone there, with no connection, and put back by redo,
    // as a step under way, which the page's batches made meanwhile wait for
    // (AC4).
    const local =
      which === "undo"
        ? this.withdrawable()
        : this.putsBack()
          ? true
          : undefined;
    if (local !== undefined) {
      const done = (
        local === true ? this.putBack() : this.withdraw(local)
      ).finally(() => {
        if (this.stepUnderWay?.done === done) {
          this.stepUnderWay = undefined;
          this.show();
          this.idleChanged();
        }
      });
      this.stepUnderWay = { which, done };
      return done;
    }
    // A step of the history waits for the batches that the device's queue
    // keeps, which come before it, and which the page cannot undo before
    // the server has answered them, as the button says.
    if (this.holdsQueued()) {
      return Promise.resolve(false);
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
        this.idleChanged();
      }
    });
    let give = () => {};
    const gaveWay = new Promise<void>((resolve) => {
      give = resolve;
    });
    const sent: StepUnderWay = {
      which,
      done,
      gaveWay,
      giveWay: () => {
        sent.given = true;
        give();
      },
    };
    this.stepUnderWay = sent;
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
    earlier: readonly Promise<SendResult>[],
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
      // So do the batches that the device's queue kept before the page
      // loaded, which the document may still be taking as the step is asked
      // for, as the history kept over a reload offers it at once: the step
      // waits until it has taken them (adoptQueue), rather than be sent
      // before them, as the latest change, or ahead of the one of them that
      // undo would take out of the queue. Those that it takes after, as of
      // a tab that closes, come after the step (adoptMore).
      await this.adoptQueue();
      // One that the device's queue kept meanwhile, as no answer came to it,
      // comes before the step still, which waits for it, as step does; and so
      // do the page's batches made since, which went on, as a try that the
      // step waited for, as a comment's, had no answer for a while (stalled),
      // and which are the latest changes once they apply; a step that none
      // went before is sent, as it was asked for.
      if (this.holdsQueued() || this.passed()) {
        return false;
      }
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
      // for the step, or, once it gives way, have it not sent (below). Once
      // the channel is not live after that, nothing would tell the document
      // of a database restored from an older backup meanwhile, behind those
      // batches, but a seq that the server gives, as the project's, which it
      // reads first (seqChecked): the project read anew may have failed, and
      // wait to try again, or be slower than the wait, or have read the
      // project before the step was asked for. A socket lost a moment
      // before, whose answers' reading anew waits a while for it to be back
      // (lostTimer), would leave the step without those batches, and so
      // without what it moves, after which the other steps, which go on
      // expecting what the document held before them, would be refused for
      // the user's own step, and dropped: the project is read anew now, as of
      // them, rather than once that while has passed.
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
        !(await this.givingWayAfter(this.seqChecked()))
      ) {
        // The project can no longer be read, which its pages show.
        return false;
      }
      // The page's batches made meanwhile went on, as the read, or a try, had
      // no answer for a while (givingWayAfter, stalled), and are the latest
      // changes once they apply: the step is not sent after them, as it is
      // not after one that the device's queue kept (holdsQueued); one that
      // none went before is sent, as it was asked for.
      if (this.passed()) {
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

  /** Reports whether a batch of the page's went on before the step under way, as it gave way to those made meanwhile (StepUnderWay.passed). */
  private passed(): boolean {
    return this.stepUnderWay?.passed === true;
  }

  /**
   * Resolves as reading does, the step under way's read of the server,
   * which has the page's batches made meanwhile wait for the step no longer
   * once it has had no answer for answerWait, as on a network whose
   * connection stalls, where the page keeps a queue: the page cannot reach
   * the server until the read settles (stalledRead), so that they are kept
   * there at once, as are those after any try that no answer comes to,
   * rather than in the page alone (sendAfterSteps), and sent once it has.
   */
  private async givingWayAfter<T>(reading: Promise<T>): Promise<T> {
    let stalled = false;
    const timer =
      this.queue === undefined
        ? undefined
        : setTimeout(() => {
            stalled = true;
            this.stalledRead = true;
            this.show();
            this.stepUnderWay?.giveWay?.();
          }, this.options.answerWait ?? answerWait);
    try {
      return await reading;
    } finally {
      clearTimeout(timer);
      if (stalled) {
        this.stalledRead = false;
        this.show();
        void this.pump();
      }
    }
  }

  /**
   * Sends the commands of a step, as one batch, or, once the server refuses
   * it as an undo too large for one request, in two halves, each split
   * again as it needs, in order, each once the one before has applied, with
   * what it expects of an entity that those before it brought back moved to
   * the revision that the last of them that changed it left (FR-EDIT-08). A
   * part is not sent once the document has found the server's seq gone back
   * since the step was sent (restores), as the answer to a part before it
   * may tell (pump), behind the batches that the step undoes, whose
   * revisions its commands expect (WentBackError).
   */
  private async sendInParts(
    commands: readonly UndoCommandBody[],
    restores: number,
  ): Promise<Sent[]> {
    if (this.restores !== restores) {
      throw new WentBackError();
    }
    try {
      // A step is never kept in the device's queue (keepable).
      return [
        (await this.enqueue(commands as unknown as Command[], 0, true)) as Sent,
      ];
    } catch (error) {
      if (!undoTooLarge(error) || commands.length < 2) {
        throw error;
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

  /**
   * Queues a batch, made at time, or now, which the page shows at once, a
   * step of the history or not (Entry.step), whose undo joins the history
   * once it applies, if history says so, and resolves with its answer, or
   * as queued, once the device's queue keeps it (keep).
   */
  private enqueue(
    commands: readonly Command[],
    retries: number,
    step: boolean,
    time = this.now(),
    history = false,
  ): Promise<Sent | Queued> {
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
    // A command of what is no part of the draft (outsideDraft), as a
    // comment's, changes none of it, but changes what it expects, at the
    // revision that it expects, so that the batches queued behind it that
    // were made on top of that revision, as a thread's resolution behind
    // its first comment's edit, expect what its answer leaves, rather than
    // be refused for the page's own change (moveQueued).
    for (const command of withIds) {
      if (outside(command)) {
        for (const [id, rev] of Object.entries(command.expected_rev ?? {})) {
          if (!before.has(canonical(id))) {
            before.set(canonical(id), rev);
          }
        }
      }
    }
    return new Promise<Sent | Queued>((resolve, reject) => {
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
        history: history && !step,
        maker: this.owner,
        atop: this.makersShown(),
        order: this.nextOrder(draft.time),
        sent: false,
        resolve: (answer, restores) =>
          resolve({
            answer,
            captured: entry.captured,
            followed: entry.followed,
            restores,
          }),
        reject,
        queued: () => resolve({ status: "queued" }),
      };
      this.entries.push(entry);
      this.show();
      void this.pump();
    });
  }

  /** Returns the queue's order of a batch made at time, now (nextOrder). */
  private nextOrder(time: string): number {
    this.lastOrder = nextOrder(time, this.lastOrder);
    return this.lastOrder;
  }

  /**
   * Returns the makers of the batches that the page shows now but the
   * document's own, as of other tabs that closed, on top of which a batch
   * made now is made (QueuedBatch.atop).
   */
  private makersShown(): string[] {
    const makers = new Set<string>();
    for (const entry of this.entries) {
      if (entry.maker !== this.owner) {
        makers.add(entry.maker);
      }
    }
    return [...makers];
  }

  /**
   * Reports whether the device's queue can keep entry, rather than refuse
   * it once the page cannot send it (PRD FR-OFF-03): a batch of the draft,
   * where the page keeps a queue; not a step of the history, which the
   * history keeps until it is sent, nor a batch of what is no part of the
   * draft, as a comment's, whose page says that it needs a connection.
   */
  private keepable(entry: Entry): boolean {
    return (
      this.queue !== undefined &&
      !entry.step &&
      !entry.commands.some((command) => outside(command))
    );
  }

  /** Returns why the document sends none of the queue's batches now, or undefined where it sends them (holdReasons). */
  private holdReason(): HoldReason | undefined {
    return this.holdReasons()();
  }

  /**
   * Returns, as of now, why the document keeps the entry at the index of
   * entries that it is given in the device's queue, where it can, rather
   * than send it now, or undefined where it sends it: a queued batch that
   * needs review before it, or itself, in the order that the document sends
   * them, which every batch after it waits for, but none before it, as one
   * of a tab that closed made before it, which the document took with it
   * (plan item 92); a page that cannot reach the server (unreachable); or a
   * try that the server refused for when or by whom it was sent, until the
   * next (retryTimer). Given no index, it returns why the document sends
   * none of the queue's batches now: for review, once none is left before
   * the first that needs it. It reads the queue once, as a pass over every
   * entry asks of each (keep, pump), rather than once for each of them.
   */
  private holdReasons(): (at?: number) => HoldReason | undefined {
    if (this.queue === undefined) {
      return () => undefined;
    }
    const review = this.entries.findIndex((e) => e.held?.state === "review");
    const first =
      review !== -1 &&
      !this.entries
        .slice(0, review)
        .some((e) => e.held !== undefined && e.answer === undefined);
    const other =
      this.unreachable() ??
      (this.retryTimer === undefined ? undefined : "refused");
    return (at) =>
      review !== -1 && (at === undefined ? first : at === -1 || at >= review)
        ? "review"
        : other;
  }

  /**
   * Returns why the page cannot reach the server now, where it keeps a
   * queue, or undefined where it can, as far as it knows: a browser that has
   * no connection; a try that no answer came to, until the next
   * (retryTimer), and through the next, until it is answered, so that what
   * needs the server is not offered at each try only to be taken back as it
   * fails; a try that none came to for answerWait, until it settles, though
   * the channel is live again meanwhile (stalledTry), and so a step's read
   * of the project before it is sent (stalledRead); or a page that holds
   * the session that the device kept, without its CSRF token, which no
   * change can be sent with, until the page has its own (PRD FR-OFF-02).
   */
  private unreachable(): "offline" | "unanswered" | "session" | undefined {
    if (this.queue === undefined) {
      return undefined;
    }
    if (!this.isOnline()) {
      return "offline";
    }
    if (
      this.stalledTry ||
      this.stalledRead ||
      (this.retryReason === "unanswered" &&
        (this.retryTimer !== undefined || this.inflight))
    ) {
      return "unanswered";
    }
    const session = this.options.queryClient.getQueryData(
      sessionQuery.queryKey,
    );
    if (session && session.csrf_token === "") {
      return "session";
    }
    return undefined;
  }

  /**
   * Keeps in the device's queue each batch of the page's that it can keep,
   * and does not yet, that the document has not sent, as the page cannot
   * send it now (holdReason): its caller is told that it is queued once it
   * is written; one that the device refuses, as for its quota, is refused
   * as it would be without a queue, for want of the server, or, as while a
   * batch before it needs review, with the device's refusal itself, which
   * the error console records. A change that the queue keeps ends what the
   * history could redo, as its answer would, once the device has written it
   * (persist).
   */
  private keep(): void {
    const errors = new Map<Entry, Error | undefined>();
    const reasonOf = this.holdReasons();
    for (const [at, entry] of this.entries.entries()) {
      const reason =
        !entry.sent && entry.held === undefined && this.keepable(entry)
          ? reasonOf(at)
          : undefined;
      if (reason !== undefined) {
        errors.set(
          entry,
          reason === "offline" || reason === "unanswered"
            ? new NetworkError(new TypeError("The server cannot be reached."))
            : undefined,
        );
      }
    }
    const fresh = [...errors.keys()];
    if (fresh.length === 0) {
      return;
    }
    for (const entry of fresh) {
      entry.held = this.batchOf(entry);
      entry.dirty = true;
    }
    this.queueChanged();
    // Its caller is told once the write has kept it (persist).
    this.persist().catch((failure: unknown) => {
      if (
        !(failure instanceof SignedOutError) &&
        (!overQuota(failure) || fresh.some((e) => errors.get(e) === undefined))
      ) {
        this.options.log.record("error", failure);
      }
      for (const entry of fresh) {
        const error = errors.get(entry);
        // The device keeps nothing of it, and no write after writes it
        // there (persist). One on its way meanwhile, as the browser came
        // online again, is settled by its answer, or, where none comes,
        // kept anew, which its caller hears of then (queueKept).
        entry.held = undefined;
        entry.dirty = false;
        if (!entry.sent) {
          // Kept no longer, or taken out by an undo meanwhile, after which
          // the device keeps nothing of it either, nor redo puts it back.
          this.entries = this.entries.filter((e) => e !== entry);
          this.unwithdraw(entry);
          entry.reject(error ?? failure);
        }
      }
      this.queueChanged();
    });
  }

  /**
   * Forgets what undo took of entry out of the queue, if it did, as once
   * the device refused to have kept entry, which its caller was told
   * failed, so that redo does not put it back.
   */
  private unwithdraw(entry: Entry): void {
    this.withdrawn = this.withdrawn.filter((w) => w.entry !== entry);
  }

  /**
   * Writes to the device's queue, after the writes before, in one
   * transaction, each batch that it keeps of which it keeps what is behind
   * (Entry.dirty), and the removal of those of the IDs of remove, resolving
   * once it has, or rejecting where the device refused. A batch that the
   * server has answered, which the page shows until the document has its
   * operation (settle), is the queue's no longer, and not written again. A
   * document dropped that has let go of its lock refuses to write, as
   * another tab's may hold its batches since (letGo), and so does one that
   * the browser refused its lock (lockRefused). Each batch that the write
   * keeps for the first time tells its caller that it is queued (queued),
   * and, as a change of the history's, ends what the history could redo,
   * which a change that the device refused to keep, as for its quota, whose
   * caller is told that it failed, leaves as it was (History.endRedo).
   */
  private persist(remove: readonly string[] = []): Promise<void> {
    const queue = this.queue;
    const userId = this.userId;
    if (queue === undefined || userId === undefined) {
      return Promise.resolve();
    }
    const removed = new Set(remove);
    const put: QueuedBatch[] = [];
    const writing: Entry[] = [];
    for (const entry of this.entries) {
      if (
        entry.held !== undefined &&
        entry.dirty &&
        entry.answer === undefined &&
        !removed.has(entry.held.id)
      ) {
        entry.dirty = false;
        entry.held = this.batchOf(entry);
        put.push(entry.held);
        writing.push(entry);
      }
    }
    if (put.length === 0 && remove.length === 0) {
      return this.saving;
    }
    if (this.letGo) {
      return Promise.reject(
        new Error("The document has let go of the device's queue."),
      );
    }
    this.saves++;
    const write = this.saving.then(async () => {
      if ((await this.ownerLock) === undefined) {
        throw new Error("The browser refused the document its lock.");
      }
      // What the document no longer keeps as it was put, as a batch that
      // the device refused to keep by a write before, whose caller was told
      // that it failed, is not written. A batch written before is written
      // again only where the device still keeps it, so that one that signing
      // out took off it meanwhile, in this tab or another, is not put back
      // (QueueStore.save); and a document dropped as the page signed out
      // writes none anew, which it keeps no longer, refusing the write, as
      // their callers are refused without a queue (signedOutOf).
      const signedOut = this.signedOutOf();
      const kept: QueuedBatch[] = [];
      const created: string[] = [];
      const fresh: Entry[] = [];
      let refused = false;
      put.forEach((batch, i) => {
        const entry = writing[i] as Entry;
        if (entry.held?.id !== batch.id) {
          return;
        }
        if (entry.stored !== true) {
          if (signedOut) {
            entry.held = undefined;
            entry.dirty = false;
            refused = true;
            return;
          }
          created.push(batch.id);
          fresh.push(entry);
        }
        kept.push(batch);
      });
      if (kept.length > 0 || remove.length > 0) {
        await queue.save(userId, this.projectId, kept, remove, created);
      }
      // Each is the queue's from now on, which a reload keeps, and which its
      // caller hears of, as of one that the device refused to keep before,
      // while it was on its way again (queueKept).
      for (const entry of fresh) {
        entry.stored = true;
        entry.queued?.();
      }
      if (fresh.some((e) => e.history)) {
        this.history.endRedo();
      }
      if (fresh.length > 0) {
        this.show();
      }
      if (refused) {
        throw new SignedOutError();
      }
    });
    const done = () => {
      this.saves--;
      this.idleChanged();
    };
    write.then(done, (error: unknown) => {
      // A write that the device refused, as for its quota, left its records
      // as they were: each batch that it carried, which the document keeps
      // as it put it, is behind there still, as with the revisions that an
      // answer moved (moveQueued), and the next write carries it, rather
      // than leave a page that loads anew to send it as it was. One that a
      // document dropped as the page signed out no longer keeps is not.
      if (!(error instanceof SignedOutError)) {
        put.forEach((batch, i) => {
          const entry = writing[i] as Entry;
          if (
            entry.held === batch &&
            entry.answer === undefined &&
            this.entries.includes(entry)
          ) {
            entry.dirty = true;
          }
        });
      }
      done();
    });
    this.saving = write.catch(() => undefined);
    return write;
  }

  /**
   * Writes to the device's queue what is behind of the batches that it
   * keeps, and the removals of remove, as persist does, recording a write
   * that failed, after which the queue keeps them as they were, as a page
   * that loads anew sends them, and is answered as they would be.
   */
  private persistLater(remove: readonly string[] = []): void {
    this.persist(remove).catch((error: unknown) => {
      if (error instanceof SignedOutError) {
        // What the write kept no longer is tried, and refused, as without a
        // queue (pump).
        void this.pump();
      } else if (!overQuota(error)) {
        this.options.log.record("error", error);
      }
    });
  }

  /**
   * Returns the batch of entry as the device's queue keeps it, with changes,
   * as of what entry holds now: its latest refusal, unless changes give
   * another, or none (null), as once a try after it got no answer, which
   * the sync panel says instead.
   */
  private batchOf(
    entry: Entry,
    changes: Partial<Pick<QueuedBatch, "state" | "sent">> & {
      refusal?: Refusal | null;
    } = {},
  ): QueuedBatch {
    const held = entry.held;
    const refusal =
      changes.refusal === null ? undefined : (changes.refusal ?? held?.refusal);
    return {
      id: held?.id ?? formatUuid(newUuid()),
      owner: this.owner,
      maker: entry.maker,
      atop: [...entry.atop],
      order: entry.order,
      format: queueFormat,
      time: entry.time,
      commands: entry.commands,
      key: entry.key,
      base: [...entry.base].map(([id, b]) => [
        id,
        {
          entity: (b.entity ?? null) as Record<string, unknown> | null,
          exact: b.exact,
        },
      ]),
      before: [...entry.before],
      parts: [...entry.parts],
      retries: entry.retries,
      ...(entry.known === undefined ? {} : { known: entry.known.latest }),
      sent: changes.sent ?? held?.sent ?? false,
      history: entry.history,
      state: changes.state ?? held?.state ?? "pending",
      ...(refusal === undefined ? {} : { refusal }),
    };
  }

  /** Counts a change of what the sync panel lists, and shows it. */
  private queueChanged(): void {
    this.queueChanges++;
    this.show();
  }

  /**
   * Has the queue tried again later, as the server gave no answer, or refused
   * for when it was asked, which reason says (retryReason), the longer the
   * more tries in a row it did, exponentially, with full jitter, as the
   * channel's socket does (backoff), but no sooner than after atLeast
   * milliseconds, as an answer's Retry-After asks, of too many requests, or
   * of a server that is not ready yet, or at once once the browser is
   * online again, or the channel live.
   */
  private tryAgainLater(reason: "unanswered" | "refused", atLeast = 0): void {
    if (this.disposed) {
      return;
    }
    this.retryReason = reason;
    clearTimeout(this.retryTimer);
    const delay = Math.max(
      atLeast,
      backoff(this.retryFailures, this.options.random ?? Math.random),
    );
    this.retryFailures++;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = undefined;
      this.tryNow();
    }, delay);
    this.show();
  }

  /** Tries the queue again at once, as once the browser is online again, or the channel live. */
  private tryAgainNow(): void {
    if (this.retryTimer !== undefined) {
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
    }
    this.tryNow();
  }

  /**
   * Tries the queue now, which the page reaches the server through, as
   * far as it knows, where no answer came before, until it is answered
   * (unreachable), and as of then where it tries nothing, as of a queue
   * that undo emptied meanwhile.
   */
  private tryNow(): void {
    void this.pump();
    if (!this.inflight && this.retryReason === "unanswered") {
      this.retryReason = undefined;
    }
    this.show();
  }

  /**
   * Records that the server answered a try, after which the page reaches it:
   * the wait for want of an answer is over, the queue being sent next, while
   * one after a refusal for when a batch was sent goes on, as its Retry-After
   * asks, though the try answered had none for answerWait (stalledTry).
   */
  private reached(): void {
    // The tries that no answer came to in a row end, which spaced the next
    // (backoff), unlike those that the server refused for when they were
    // sent, which a refusal of the same goes on spacing.
    if (this.retryReason !== "refused") {
      this.retryFailures = 0;
    }
    if (this.retryReason === "unanswered") {
      this.retryReason = undefined;
      clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
    }
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

  /**
   * Sends the next queued batch, once the one before has been answered.
   * While the page cannot send what the device's queue can keep (holdReason),
   * the queue keeps it, and the batch sent is the next that it cannot keep,
   * as a comment's, which is sent as it would be without a queue, and
   * refused for want of a connection, or else sent before the batches that
   * the queue keeps, which change nothing that it changes. A document that
   * has been dropped sends none of the queue's, which another tab's, or the
   * project's next, takes, and keeps the page's batches that it has yet to
   * send as it would before, until it lets go of its lock (dispose), but for
   * one dropped as the page signed out, which keeps none (signedOutOf): each
   * is tried, and refused, as without a queue (ownSession), rather than left
   * unsent, its caller waiting, and the lock held, for as long as the page
   * is open.
   */
  private async pump(): Promise<void> {
    // A batch made while a try that no answer may come to is under way is
    // kept at once, as one made before it (unreachable).
    const signedOut = this.signedOutOf();
    if (!signedOut) {
      this.keep();
    }
    if (this.inflight) {
      return;
    }
    const reasonOf = this.holdReasons();
    const entry = this.entries.find(
      (e, at) =>
        !e.sent &&
        (signedOut || !this.keepable(e) || reasonOf(at) === undefined) &&
        !(this.disposed && e.held !== undefined),
    );
    if (entry === undefined) {
      return;
    }
    this.inflight = true;
    entry.sent = true;
    if (entry.held !== undefined) {
      // The sync panel lists it as being sent.
      this.queueSends++;
      this.show();
    }
    // What the server is known to have had as the batch's commands were
    // first sent, past which they commit, if they apply, and past which the
    // server's answer to them sent again gives the seq that applied them.
    if (entry.known === undefined && entry.held !== undefined) {
      entry.dirty = true;
    }
    entry.known ??= this.knownNow();
    const known = entry.known;
    // Whether the batch reached the request, which may then have applied.
    let posted = false;
    // Whether the device's queue kept the batch as sent before this try, as
    // one that no answer came to, which a refusal of the try leaves it.
    let sentBefore = entry.held?.sent ?? false;
    // The timer after which the try, unanswered, keeps its batch (stalled).
    let stall: ReturnType<typeof setTimeout> | undefined;
    try {
      await this.whenReady();
      // A document dropped meanwhile sends none of the queue's, which
      // another tab's, or the project's next, takes once it lets go of its
      // lock (dispose).
      if (this.disposed && entry.held !== undefined) {
        entry.sent = false;
        return;
      }
      // Never recorded as sent for another account's session (ownSession).
      this.ownSession();
      // A batch of the device's queue is recorded as sent before it is, as
      // it may apply once it is, whatever becomes of the page meanwhile, as
      // a reload, or a tab that closes: the next page sends it again with
      // its commands' IDs, rather than take it out of the queue by an undo
      // (withdraws). A device that refuses the write, as for its quota, or
      // as a database whose connection the browser lost, leaves it as it
      // was, and the batch is sent all the same, which the device keeps as
      // not sent, rather than held until a try that the device records: a
      // device that refused it may refuse it at every try after, and every
      // change that the page makes meanwhile, which the queue would keep
      // while it waits (holdReason), and refuse as the device does, though
      // the server answers. Only a page that loads before its answer has
      // come, and takes it out of the queue by an undo, as offline, would
      // find the server with it after.
      sentBefore = entry.held?.sent ?? false;
      if (entry.held !== undefined && !sentBefore) {
        entry.held = this.batchOf(entry, { sent: true });
        entry.dirty = true;
        await this.persist().catch((failure: unknown) => {
          if (!overQuota(failure) && !(failure instanceof SignedOutError)) {
            this.options.log.record("error", failure);
          }
        });
        if (this.disposed) {
          // Not sent after all: the device keeps it as it was, which the
          // next page may take out of the queue by an undo, before this
          // document lets go of its lock (whenIdle); unless it keeps it no
          // longer, as it refused to meanwhile, after which it is tried as
          // one that the queue does not keep (keep).
          entry.sent = false;
          if (entry.held !== undefined) {
            entry.held = this.batchOf(entry, { sent: false });
            entry.dirty = true;
            this.persistLater();
          }
          return;
        }
      }
      // The session as of now, after the write, as of a CSRF token that
      // changed meanwhile.
      const session = this.ownSession();
      // The restores that the document had found as the batch was sent,
      // after which its answer is of what the server has (Sent.restores).
      let restores = this.restores;
      posted = true;
      if (this.queue !== undefined) {
        stall = setTimeout(
          () => this.stalled(entry),
          this.options.answerWait ?? answerWait,
        );
      }
      const answer = await this.post(entry.commands, session);
      this.retryFailures = 0;
      this.reached();
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
      // Its undo joins the history before its caller hears of it, or as the
      // device's queue is sent, whose batch the queue no longer keeps, with
      // what moved of those after it, in one write (persist).
      this.joinHistory(entry, answer);
      if (entry.held !== undefined) {
        this.persistLater([entry.held.id]);
        this.queueChanged();
      } else {
        this.persistLater();
      }
      this.settle();
      this.catchUp();
      entry.resolve(answer, restores);
    } catch (error) {
      // A refusal of the server's own, not a proxy's page for it, is an
      // answer, which the page reached the server for.
      if (
        error instanceof ApiError &&
        (answered(error) || error.status < 500)
      ) {
        this.reached();
      }
      if (conflictOf(error)) {
        // The page shows what changed elsewhere at once, whatever becomes
        // of the batch, as the channel would bring it later.
        this.adopt(error);
      }
      if (!this.entries.includes(entry)) {
        // A batch whose operation the channel brought while its try was
        // under way applied (confirm), whatever the try met after, as no
        // answer, or a failure of the server's own: the device's queue
        // keeps it no longer, and its caller, which no answer tells of what
        // it did, is refused, as it would be without a queue, but for one of
        // the queue's, which was told that it is queued, rather than kept,
        // and never settled, holding the document's lock.
        if (entry.held !== undefined) {
          this.persistLater([entry.held.id]);
          this.queueChanged();
        } else {
          this.refuse(entry, error, known);
        }
        return;
      }
      if (this.retried(entry, error)) {
        if (entry.held !== undefined) {
          // Refused, it applied nothing, and is sent again with new IDs.
          entry.held = this.batchOf(entry, { sent: false });
          entry.dirty = true;
          this.persistLater();
        }
        return;
      }
      if (this.queueKept(entry, error, posted, sentBefore)) {
        return;
      }
      this.refuse(entry, error, known);
    } finally {
      clearTimeout(stall);
      this.inflight = false;
      // The try has settled, which its want of an answer held the page to
      // (stalled), as what it met says now, as a wait after it.
      if (this.stalledTry) {
        this.stalledTry = false;
        this.show();
      }
      void this.pump();
      this.idleChanged();
    }
  }

  /**
   * Takes the try of entry, under way with no answer for answerWait, for one
   * that no answer comes to, as on a network whose connection stalls (PRD
   * FR-OFF-03): the page cannot reach the server until a try is answered
   * (unreachable), and entry, which may apply, is kept in the device's
   * queue as sent, where it can be, as is every batch after it (keep),
   * rather than in the page alone, which a tab that closes before the
   * browser gives up on the try would lose; its caller hears that it is
   * queued once it is written. The try goes on, and its answer, or its
   * failure, settles the batch as one of the queue's (queueKept). A step
   * of the history under way, whose try it is, which is never kept, or
   * which waits for it, as for a comment's sent before it, has the page's
   * batches that wait for the step wait no longer, which are queued behind
   * the try, and kept, as the batches after any try are (giveWay), and the
   * step, not sent yet, is then not sent after them (stepped).
   */
  private stalled(entry: Entry): void {
    if (this.queue === undefined || !this.inflight || !entry.sent) {
      return;
    }
    this.stalledTry = true;
    // A document dropped as the page signed out keeps nothing anew (pump).
    const signedOut = this.signedOutOf();
    if (!signedOut) {
      this.stepUnderWay?.giveWay?.();
    }
    if (
      entry.held === undefined &&
      this.keepable(entry) &&
      !signedOut &&
      this.entries.includes(entry)
    ) {
      entry.held = this.batchOf(entry, { sent: true });
      entry.dirty = true;
      this.persist().catch((failure: unknown) => {
        if (!overQuota(failure) && !(failure instanceof SignedOutError)) {
          this.options.log.record("error", failure);
        }
        // The device keeps nothing of it, and no write after writes it
        // there (persist): its try settles it as without a queue, or, where
        // no answer comes, it is kept anew (keep), its caller hearing of it
        // then, or of the device's refusal.
        entry.held = undefined;
        entry.dirty = false;
        this.queueChanged();
      });
    }
    if (!signedOut) {
      this.keep();
    }
    this.queueChanged();
  }

  /**
   * Takes entry, which the server refused, or did not answer, out of what
   * the page shows, and refuses it to its caller with error, as a
   * ConflictError where it is one, which names what both changed.
   */
  private refuse(entry: Entry, error: unknown, known: Known): void {
    if (answered(error)) {
      // The server answers a batch sent again with these IDs alike, so the
      // next has IDs of its own.
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
        // The channel, while it is not live, would not bring it: the parts
        // that it changes are read again, so that the page shows it, as a
        // board that a dialog created, rather than offer to make it anew,
        // with IDs of its own, which would make another.
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
  }

  /**
   * Keeps entry in the device's queue, rather than refuse it to its caller,
   * once error tells that the server did not answer it, or refused a batch
   * that the queue keeps, and reports whether it did (PRD FR-OFF-03). A
   * batch that no answer came to, posted or not (unansweredBy), or that a
   * proxy's page of a failure answered for the server (proxyFailure), which
   * the queue can keep, is kept, and the queue is tried again later, with its
   * commands' IDs, as it may have applied; its caller hears that it is
   * queued once it is written, or, where the device refuses, its refusal. A
   * batch of the queue refused for when or by whom it was sent, as a
   * session that ended (401), a role that does not allow it (403), a
   * request of too many (429), an archived project, or a failure of the
   * server's own, waits too, and is tried again later, no sooner than the
   * answer asks (Retry-After), as is one that met a fault of the page's
   * own, which the error console records; one refused for what it does, as
   * for what changed since, needs review, with every batch after it (plan
   * item 92).
   * One that the queue does not keep yet, refused, or that met a fault of
   * the page's own, is refused to its caller, as it would be without a
   * queue, and so is one of a document dropped as the page signed out,
   * which leaves nothing of the account's on the device that signing out
   * did not ask of (plan note N98), while one dropped as another account
   * signed in keeps it for its own. A refusal leaves the batch as the queue
   * kept it before the try (sentBefore), but where a failure of the
   * server's own may have come after it committed.
   */
  private queueKept(
    entry: Entry,
    error: unknown,
    posted: boolean,
    sentBefore: boolean,
  ): boolean {
    const held = entry.held;
    const answer = error instanceof ApiError ? error : undefined;
    // A batch that the queue does not keep yet, that a proxy's page of a
    // failure answered for the server, as while it restarts, got no answer
    // of the server's either, and is kept as one that none came to; one of
    // the queue's keeps that refusal, which the sync panel says, as below.
    const unanswered =
      unansweredBy(error) || (held === undefined && proxyFailure(error));
    if (
      held === undefined &&
      (!unanswered || !this.keepable(entry) || this.signedOutOf())
    ) {
      return false;
    }
    entry.sent = false;
    // Whether the try may have applied the batch: sent to a server that did
    // not answer, or that failed after it committed.
    const reached =
      posted &&
      !(error instanceof OtherAccountError) &&
      (answer === undefined || answer.status >= 500);
    if (unanswered) {
      if (held === undefined) {
        entry.held = this.batchOf(entry, { sent: reached });
        entry.dirty = true;
        const known = entry.known as Known;
        // Its caller is told once the write has kept it (persist).
        this.persist().catch((failure: unknown) => {
          if (!overQuota(failure) && !(failure instanceof SignedOutError)) {
            this.options.log.record("error", failure);
          }
          // The device could not keep it, and no write after writes it there
          // (persist): it is refused as it would be without a queue, unless
          // it is on its way again meanwhile, which its answer, or its
          // refusal, settles as without a queue, or, where none comes, which
          // is kept anew, its caller hearing of it then, as keep's; and so
          // if an undo took it out meanwhile, as the device keeps nothing of
          // it either.
          entry.held = undefined;
          entry.dirty = false;
          if (entry.sent) {
            return;
          }
          if (this.entries.includes(entry)) {
            this.refuse(entry, error, known);
            this.queueChanged();
          } else {
            this.unwithdraw(entry);
            this.queueChanged();
            entry.reject(error);
          }
        });
      } else {
        // Sent with no answer, it may have applied, which the sync panel
        // says, rather than the refusal of a try before.
        entry.held = this.batchOf(entry, {
          sent: sentBefore || reached,
          ...(reached ? { refusal: null } : {}),
        });
        entry.dirty = true;
        this.persistLater();
      }
      // No sooner than a proxy's page asks, as one that tells when the
      // server will be back (Retry-After).
      this.tryAgainLater(
        "unanswered",
        answer === undefined ? 0 : retryAfterOf(answer),
      );
      this.queueChanged();
      return true;
    }
    if (answer === undefined) {
      // A fault of the page's own, as an answer of a form that it does not
      // expect, which trying again may not mend, but which is no refusal of
      // what the batch does for the user to review: the batch waits, as
      // the device keeps it.
      this.options.log.record("error", error);
      entry.held = this.batchOf(entry, { sent: sentBefore || reached });
      entry.dirty = true;
      this.persistLater();
      this.tryAgainLater("refused");
      this.queueChanged();
      return true;
    }
    // Refused: a batch that the queue keeps, as one refused to its caller
    // returned above.
    const refusal: Refusal = {
      status: answer.status,
      ...(answer.error === undefined ? {} : { error: answer.error }),
      at: this.now(),
    };
    // A proxy's page, as one that its own sign-in answers with once it has
    // ended, refuses when or by whom the batch was sent as the server would,
    // or is its failure (plan note N98).
    const waits = answered(answer)
      ? !refusedForGood(answer)
      : answer.status >= 500 || refusedForWhen.includes(answer.status);
    entry.held = this.batchOf(entry, {
      refusal,
      state: waits ? "pending" : "review",
      // A refusal applied nothing, but a failure of the server's own may
      // have come after the batch committed.
      sent: waits && (sentBefore || reached),
    });
    entry.dirty = true;
    this.persistLater();
    if (waits) {
      if (sessionDoubtedBy(answer)) {
        this.options.checkSession?.();
      }
      // A page that a proxy answers for the server, as 502 while it
      // restarts, does not reach it; an answer that says when to ask again,
      // as a refusal of too many requests, or the server's while it is not
      // ready yet (not_ready), is tried again no sooner (Retry-After).
      this.tryAgainLater(
        proxyFailure(answer) ? "unanswered" : "refused",
        retryAfterOf(answer),
      );
    }
    this.queueChanged();
    return true;
  }

  /**
   * Reports whether the document was dropped as the page signed out, or
   * found its session ended, after which it keeps no batch in the device's
   * queue that it does not already (queueKept, pump).
   */
  private signedOutOf(): boolean {
    return (
      this.disposed &&
      this.options.queryClient.getQueryData(sessionQuery.queryKey) === null
    );
  }

  /**
   * Returns the page's session, which a batch is sent with: the document is
   * its account's (Documents), and a batch queued while the page signed in
   * to another account, or out, whose cookie and CSRF token the request
   * would carry, is never sent as that account's, but kept for its own
   * (OtherAccountError), as the device's queue keeps it, unless the page
   * signed out (signedOutOf).
   */
  private ownSession(): SignedInBody | null | undefined {
    const session = this.options.queryClient.getQueryData(
      sessionQuery.queryKey,
    );
    if (this.userId !== undefined && session?.user_id !== this.userId) {
      throw new OtherAccountError();
    }
    return session;
  }

  private async post(
    commands: CommandBody[],
    session: SignedInBody | null | undefined,
  ): Promise<CommandResultBody> {
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
   * for the steps of the history, which were not (Entry.step), nor those
   * that a page made that did not show entry (madeOnTop).
   */
  private moveQueued(entry: Entry, answer: CommandResultBody): void {
    for (const r of answer.revisions ?? []) {
      const id = canonical(r.id);
      const from = entry.before.get(id);
      if (from === undefined) {
        continue;
      }
      for (const queued of this.entries) {
        // A batch that a page made without showing entry, as another tab's
        // that the document took as that tab closed, was not made on top of
        // it (madeOnTop), nor was one made before it, as one of its maker's
        // that the document took after it, as a sign-out under way passed
        // over that one (QueueStore.intend): what it expects is left, so
        // that it is refused for what entry changed, and compared with it,
        // rather than written over it unasked.
        if (
          queued.sent ||
          queued === entry ||
          queued.order <= entry.order ||
          !madeOnTop(queued, entry.maker)
        ) {
          continue;
        }
        if (!queued.step) {
          const commands = queued.commands.map((c) =>
            moved(c, id, from, r.rev),
          );
          if (commands.some((c, i) => c !== queued.commands[i])) {
            queued.commands = commands;
            queued.dirty = true;
          }
        }
        // A batch made on top of this one, after it, expects what this one
        // is moved to, from which its answer moves it on.
        if (queued.before.get(id) === from) {
          queued.before.set(id, r.rev);
          queued.dirty = true;
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
      const owner = ownsExpressions(id);
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
            ? overlapsAt(
                shown.exact,
                comparable(owner, field, value),
                comparable(owner, field, base[field]),
                comparable(owner, field, state[field]),
              )
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
 * Returns what a conflict compares of value, of field of an entity, which
 * owner says is an owner of expressions (ownsExpressions): of an
 * expression, as a node's scripts and a connection's or an output's
 * condition, its source alone, of which the server compiles the rest
 * (§5.8.8), so that one compiled anew elsewhere, of the same source, as
 * once a variable that it names is created, did not change elsewhere, and
 * one that a batch writes, which the page holds as not compiled yet
 * (scripted), is what it is elsewhere where that has its source; of any
 * other field, or of another entity, the value itself.
 */
function comparable(owner: boolean, field: string, value: unknown): unknown {
  return owner &&
    (expressionFields as readonly string[]).includes(field) &&
    typeof value === "object" &&
    value !== null &&
    "source" in value
    ? value.source
    : value;
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
export interface Conflict {
  id: string;
  expected_rev: number;
  exists: boolean;
  current_rev?: number;
}

/** Returns the conflicts of error, a refusal for a revision, each of an entity whose revision changed elsewhere, or none of another error. */
export function conflictsOf(error: ApiError): Conflict[] {
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
 * does, after which it starts none of those left, whose caller has failed,
 * as play mode's reads of what the document does not hold (play/session.ts).
 */
export async function inTurns(
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
