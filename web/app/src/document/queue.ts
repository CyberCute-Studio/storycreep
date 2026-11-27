// The device's queue of the page's batches that the server has yet to
// answer (PRD FR-OFF-03, plan note N98): a batch that the page made while
// the browser had no connection, or that no answer came to, is kept on the
// device, as the commands of the command endpoint that it would have sent,
// with their IDs and the revisions that they expect, and sent once the
// server answers again, from this tab or, once it has closed, from the next
// that holds the project's document, as after the browser restarted. The
// same commands apply to what the page shows, offline as online, through
// the same appliers (apply.ts, AC2). A batch that the server refuses for
// what it does is kept, as needing review, with everything queued after it,
// until the user decides (plan item 92).

import type { CommandBody, ErrorBody } from "@storycreep/shared/api";
import type { Part } from "./doc";

/**
 * The format of the batches that this version of the app keeps, which a
 * version that keeps them otherwise takes the next number for: a batch of
 * another format is still the user's, and is kept, but not sent by this
 * version, which cannot read it (QueueStore.adopt).
 */
export const queueFormat = 1;

/** The server's latest refusal of a queued batch, as it answered. */
export interface Refusal {
  /** The answer's HTTP status. */
  status: number;
  /** The answer's error envelope, if it had one. */
  error?: ErrorBody;
  /** When the server answered. */
  at: string;
}

/** What the page showed of an entity that a batch expects, as the device keeps it (the document's Base). */
export interface QueuedBase {
  entity: Record<string, unknown> | null;
  exact: boolean;
}

/** A batch of the queue, as the device keeps it. */
export interface QueuedBatch {
  /** The record's own ID, which no other batch has, and which stays as its commands' IDs change. */
  id: string;
  /**
   * The document that holds it, whose lock another tab's document waits
   * for before it takes the batch (QueueStore.adopt), so that no two send
   * it at once.
   */
  owner: string;
  /**
   * The document that made it, which stays as other documents take it: it
   * was made on top of its maker's batches before it, and of those of the
   * makers of atop, but not of another's, as of a tab that closed, whose
   * batches a document took after its own (madeOnTop).
   */
  maker: string;
  /** The makers of the other batches that the page showed as it was made, on top of which it was made too. */
  atop: string[];
  /** Where it is in the queue, by when it was made, then by its owner's count. */
  order: number;
  /** The format of the record (queueFormat). */
  format: number;
  /** When the page made it, as the server writes a time, which the page shows it as of. */
  time: string;
  /** Its commands, as the command endpoint takes them, each with its command_id and expected_rev. */
  commands: CommandBody[];
  /** Its commands without their IDs, as JSON, which tells a batch sent again as it was. */
  key: string;
  /** What the page showed of each entity that it expects, which a conflict of it is compared with. */
  base: [string, QueuedBase][];
  /** The revisions that the page showed of what it changes, before it, which the batches before it move. */
  before: [string, number][];
  /** The parts that hold what it changes. */
  parts: Part[];
  /** How many more of its refusals for a revision that changed elsewhere, but not what it changes, it is sent again after. */
  retries: number;
  /**
   * The seq that the server was known to have had as its commands, with
   * their IDs, were first sent, if they were, past which it commits, if it
   * applies.
   */
  known?: number;
  /**
   * Whether it was sent and no answer came, so that it may have applied,
   * which the device records before each try, as the page may close before
   * the answer comes, and a refusal, which applied nothing, takes back: it
   * is sent again with its commands' IDs, which the server answers as the
   * batch that it applied, if it did (FR-OFF-04 AC6), and it is not taken
   * out of the queue by an undo, as one that never reached the server is.
   */
  sent: boolean;
  /** Whether its undo joins the history once it applies (SendOptions.history). */
  history: boolean;
  /**
   * pending, to be sent; review, refused by the server for what it does,
   * which holds it, and every batch after it, until the user decides.
   */
  state: "pending" | "review";
  /** The server's latest refusal of it, if any. */
  refusal?: Refusal;
}

/** Where a project's document keeps the batches that the server has yet to answer (DocumentOptions.queue). */
export interface QueueStore {
  /**
   * Holds the lock of owner, a document's, for as long as the page is open,
   * until what it resolves to is called, so that another tab's document,
   * which holds a lock of its own, takes none of its batches meanwhile;
   * resolves once it holds it.
   */
  hold(owner: string): Promise<() => void>;
  /**
   * Resolves to the batches of userId's projectId that no open page's
   * document holds, as of a tab that closed, or a browser that restarted,
   * which are owner's from then on, in their order; to none where the
   * device keeps none, or cannot be read. Then, until stop is called,
   * calls found with those of another document's that closes meanwhile,
   * which are owner's too. Each is taken only while takes says so, as of a
   * document that a page shows, those of a document that closes meanwhile
   * being left to the next that takes them; again takes those that no open
   * page's document holds, as once takes says so again, and resolves once
   * found has them.
   */
  adopt(
    userId: string,
    projectId: string,
    owner: string,
    found: (batches: QueuedBatch[]) => void,
    takes?: () => boolean,
  ): Promise<{
    batches: QueuedBatch[];
    stop: () => void;
    again: () => Promise<void>;
  }>;
  /**
   * Writes put and removes the batches of the IDs of remove, of userId's
   * projectId, in one transaction, which the browser applies whole or not
   * at all, rejecting where the device refused it, as for the quota. A
   * batch of put whose ID created does not hold, written before, is written
   * only where the device still keeps it, so that one that signing out took
   * off the device is not put back by a write that comes after (discard).
   */
  save(
    userId: string,
    projectId: string,
    put: readonly QueuedBatch[],
    remove: readonly string[],
    created: readonly string[],
  ): Promise<void>;
  /**
   * Takes every batch of userId's off the device, as signing out does once
   * the user decided (plan note N98), with those that the writes under way
   * keep, after which save writes none of them there again, in this tab or
   * another, as of an answer to one that comes after, but writes a batch
   * made since.
   */
  discard(userId: string): Promise<void>;
}

/**
 * Reports whether batch, queued after a batch that the document of other
 * made, was made on top of it: as the same document made both, or the one
 * that made batch showed other's, as it had taken them before, so that
 * what the answer to that one moves of what batch expects is the page's
 * own change, and not what another tab changed, which it did not show.
 */
export function madeOnTop(
  batch: { maker: string; atop: readonly string[] },
  other: string,
): boolean {
  return batch.maker === other || batch.atop.includes(other);
}

/** Returns the queue's order of a batch made at time, the next after last, as a millisecond past it where the clock is not. */
export function nextOrder(time: string, last: number): number {
  const at = Date.parse(time);
  return Number.isFinite(at) && at > last ? at : last + 1;
}

/** Sorts batches in the queue's order: by order, then by ID, as of two made at once in two tabs. */
export function inQueueOrder(batches: QueuedBatch[]): QueuedBatch[] {
  return batches.sort((a, b) =>
    a.order === b.order ? (a.id < b.id ? -1 : 1) : a.order - b.order,
  );
}
