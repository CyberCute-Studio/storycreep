// Where a project's document keeps the batches that the server has yet to
// answer (PRD FR-OFF-03, plan note N98): the device's database, whose
// records are the account's, with the lock of the document that holds
// them, so that two tabs never send one batch: each document holds a lock
// of its own (Web Locks) for as long as it is kept, and takes the batches of
// a document whose lock no tab holds, as of a tab that closed, or a browser
// that restarted, at once, and those of an open tab's once its lock is let
// go, as the tab closes, which it hears of from the other tabs
// (BroadcastChannel) as they keep batches. A browser without Web Locks,
// none of those that the app supports, keeps no queue, as it could not
// tell an open tab's batches from a closed one's, and two tabs that sent
// one batch, each with IDs of its own once a refusal had them sent again,
// would apply it twice. Signing out takes the account's batches off the
// device (discard), after which no tab writes one of them there again, as
// of an answer to it that comes after.

import type { QueuedBatch, QueueStore } from "../document/queue";
import { type OfflineDatabase, opensWithin, openWait } from "./db";

/** The locks of the browser's tabs that the queue's documents hold (Web Locks). */
export interface QueueLocks {
  /** Holds name until what it resolves to is called, once the browser gives it, or rejects once signal aborts first. */
  hold(name: string, signal?: AbortSignal): Promise<() => void>;
  /** Runs task while it holds name, which no other tab holds meanwhile. */
  run<T>(name: string, task: () => Promise<T>): Promise<T>;
  /** Resolves to the names of the locks that the browser's tabs hold now. */
  held(): Promise<ReadonlySet<string>>;
}

/** Returns the queue's locks of the browser's Web Locks, or undefined where it has none. */
export function webLocks(
  locks: LockManager | undefined,
): QueueLocks | undefined {
  if (locks === undefined) {
    return undefined;
  }
  return {
    hold: (name, signal) =>
      new Promise((resolve, reject) => {
        locks
          .request(
            name,
            signal === undefined ? {} : { signal },
            () =>
              new Promise<void>((release) => {
                resolve(() => release());
              }),
          )
          .catch(reject);
      }),
    run: (name, task) => locks.request(name, () => task()),
    held: async () => {
      const state = await locks.query();
      return new Set(
        (state.held ?? []).flatMap((lock) =>
          lock.name === undefined ? [] : [lock.name],
        ),
      );
    },
  };
}

/** What a tab tells the others as its document keeps batches of a project: whose they are, of which project, and which document holds them. */
export interface QueueNews {
  user: string;
  project: string;
  owner: string;
}

/** What a tab tells the others as signing out takes the batches of user's account off the device: their IDs. */
export interface QueueDiscard {
  user: string;
  discarded: string[];
}

/** The channel that tells the browser's other tabs which documents keep batches, and whose batches signing out took off the device. */
export interface QueueBroadcast {
  post(news: QueueNews | QueueDiscard): void;
  listen(listener: (news: QueueNews | QueueDiscard) => void): () => void;
}

/** Returns the queue's channel of the browser's tabs, or none where it has no BroadcastChannel. */
export function queueBroadcast(): QueueBroadcast | undefined {
  if (typeof BroadcastChannel === "undefined") {
    return undefined;
  }
  const channel = new BroadcastChannel("storycreep-queue");
  return {
    post: (news) => channel.postMessage(news),
    listen: (listener) => {
      const on = (event: MessageEvent) => {
        const news = event.data as Partial<
          QueueNews & { discarded: unknown }
        > | null;
        if (typeof news?.user !== "string") {
          return;
        }
        if (Array.isArray(news.discarded)) {
          listener({
            user: news.user,
            discarded: news.discarded.filter(
              (id): id is string => typeof id === "string",
            ),
          });
        } else if (
          typeof news.project === "string" &&
          typeof news.owner === "string"
        ) {
          listener(news as QueueNews);
        }
      };
      channel.addEventListener("message", on);
      return () => channel.removeEventListener("message", on);
    },
  };
}

/** Returns the name of the lock that the document of owner holds. */
function lockOf(owner: string): string {
  return `storycreep-queue:${owner}`;
}

/** Options of deviceQueue. */
export interface DeviceQueueOptions {
  /** Whether the page is a secure context, without which the device keeps nothing (PRD FR-OFF-01). */
  secure: boolean;
  /** The browser's Web Locks, without which the device keeps nothing either. */
  locks?: QueueLocks;
  broadcast?: QueueBroadcast;
  /** How long, in milliseconds, a document waits at most for the database to open (openWait). */
  wait?: number;
}

/**
 * Returns where the documents of the page keep their batches (QueueStore):
 * none on a page that is not a secure context, which edits online alone
 * (PRD FR-OFF-01), nor in a browser without Web Locks, nor where the
 * device's database cannot be opened.
 */
export function deviceQueue(
  db: OfflineDatabase,
  options: DeviceQueueOptions,
): QueueStore | undefined {
  const { locks, broadcast } = options;
  if (
    !options.secure ||
    typeof indexedDB === "undefined" ||
    locks === undefined
  ) {
    return undefined;
  }
  const wait = options.wait ?? openWait;
  // The IDs of the batches that signing out took off the device, by
  // account, in this tab or another, which no write puts there again, as
  // of an answer to one that comes after (discard), while a batch made
  // since, as once the account signed in again, is written; and the writes
  // under way, which the removal waits for.
  const discarded = new Map<string, Set<string>>();
  const drop = (userId: string, ids: readonly string[]) => {
    const dropped = discarded.get(userId) ?? new Set<string>();
    for (const id of ids) {
      dropped.add(id);
    }
    discarded.set(userId, dropped);
  };
  const writes = new Set<Promise<void>>();
  broadcast?.listen((news) => {
    if ("discarded" in news) {
      drop(news.user, news.discarded);
    }
  });
  return {
    hold: (owner) => locks.hold(lockOf(owner)),
    adopt: async (userId, projectId, owner, found) => {
      if (!(await opensWithin(db, wait))) {
        return { batches: [], stop: () => {} };
      }
      // A document takes batches under a lock of the project's, so that two
      // that start at once do not both take those of a tab that closed, and
      // reads which documents are open inside it, so that one that starts
      // meanwhile, whose lock it holds before it takes any, is not taken
      // for closed.
      const takeLock = `storycreep-queue-take:${userId}:${projectId}`;
      const take = (from: (other: string) => boolean) =>
        locks.run(takeLock, () =>
          db.takeQueued(userId, projectId, owner, from),
        );
      const first = await locks
        .run(takeLock, async () => {
          const held = await locks.held();
          return db.takeQueued(
            userId,
            projectId,
            owner,
            (other) => !held.has(lockOf(other)),
          );
        })
        .catch(() => [] as QueuedBatch[]);
      // The batches of an open tab's document are taken once its lock is let
      // go, as the tab closes, or the document is dropped; and those that
      // it wrote since, if any, as the next news of it tells.
      const stopped = new AbortController();
      const waiting = new Set<string>();
      const watch = (other: string) => {
        if (other === owner || waiting.has(other) || stopped.signal.aborted) {
          return;
        }
        waiting.add(other);
        locks.hold(lockOf(other), stopped.signal).then(
          async (release) => {
            release();
            waiting.delete(other);
            if (stopped.signal.aborted) {
              return;
            }
            const more = await take((o) => o === other).catch(
              () => [] as QueuedBatch[],
            );
            if (more.length > 0 && !stopped.signal.aborted) {
              found(more);
            }
          },
          () => {
            waiting.delete(other);
          },
        );
      };
      const unlisten = broadcast?.listen((news) => {
        if (
          !("discarded" in news) &&
          news.user === userId &&
          news.project === projectId
        ) {
          watch(news.owner);
        }
      });
      for (const record of await db
        .queued(userId, projectId)
        .catch(() => [] as QueuedBatch[])) {
        watch(record.owner);
      }
      return {
        batches: first,
        stop: () => {
          stopped.abort();
          unlisten?.();
        },
      };
    },
    save: async (userId, projectId, put, remove) => {
      const dropped = discarded.get(userId);
      const kept =
        dropped === undefined
          ? put
          : put.filter((batch) => !dropped.has(batch.id));
      if (kept.length === 0 && remove.length === 0) {
        return;
      }
      const write = db.saveQueued(userId, projectId, kept, remove);
      writes.add(write);
      try {
        await write;
      } finally {
        writes.delete(write);
      }
      // Every write that keeps batches tells the other tabs, as one that
      // opened since the document's last write found none of its batches,
      // and so waits for no lock of its yet (adopt).
      for (const owner of new Set(kept.map((b) => b.owner))) {
        broadcast?.post({ user: userId, project: projectId, owner });
      }
    },
    discard: async (userId) => {
      // Those that the device keeps now, and those that the writes under
      // way kept, which the removal takes too.
      const tell = (ids: string[]) => {
        drop(userId, ids);
        broadcast?.post({ user: userId, discarded: ids });
      };
      tell((await db.queued(userId)).map((batch) => batch.id));
      await Promise.allSettled([...writes]);
      tell(await db.deleteQueued(userId));
    },
  };
}
