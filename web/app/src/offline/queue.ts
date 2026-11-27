// Where a project's document keeps the batches that the server has yet to
// answer (PRD FR-OFF-03, plan note N98): the device's database, whose
// records are the account's, with the lock of the document that holds
// them, so that two tabs never send one batch: each document holds a lock
// of its own (Web Locks) for as long as it is kept, and takes the batches of
// a document whose lock no tab holds, as of a tab that closed, or a browser
// that restarted, at once, and those of an open tab's once its lock is let
// go, as the tab closes, which it hears of from the other tabs
// (BroadcastChannel) as they keep batches, or take them; it takes them
// while a page shows it, and not while the keeper of the copies alone holds
// it, which would hold them where no page shows them (takes). A browser
// without Web Locks, none of those that the app supports, keeps no queue,
// as it could not tell an open tab's batches from a closed one's, and two
// tabs that sent one batch, each with IDs of its own once a refusal had
// them sent again, would apply it twice. Signing out takes the account's
// batches that it named off the device (discard), after which no tab writes
// one of them there again, as of an answer to it that comes after, since a
// batch written before is written again only where the device still keeps
// it (save); a removal that the device refuses, as of a database that does
// not open, is remembered in the browser's storage, and made before a
// document takes the account's batches again, which takes none until it is
// (discarded). A database that does not open in time refuses a write, as one
// that refuses it for its quota does, rather than hold the document's writes,
// and the tries that wait for them, for as long as the page is open.

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

/**
 * The channel that tells the documents of the browser's tabs, this one's
 * too, which documents keep batches: each listener hears what any posts,
 * as a document of the tab that took a project's batches after another of
 * the tab was dropped hears of what that one kept since, which it takes
 * once that one lets go (adopt).
 */
export interface QueueBroadcast {
  post(news: QueueNews): void;
  listen(listener: (news: QueueNews) => void): () => void;
}

/** Returns the queue's channel of the browser's tabs, or none where it has no BroadcastChannel. */
export function queueBroadcast(): QueueBroadcast | undefined {
  if (typeof BroadcastChannel === "undefined") {
    return undefined;
  }
  const name = "storycreep-queue";
  const channel = new BroadcastChannel(name);
  return {
    post: (news) => channel.postMessage(news),
    // Each listener on a channel object of its own, which hears what the
    // tab's posts, as a BroadcastChannel never hears what it posts itself.
    listen: (listener) => {
      const own = new BroadcastChannel(name);
      own.addEventListener("message", (event: MessageEvent) => {
        const news = event.data as Partial<QueueNews> | null;
        if (
          typeof news?.user === "string" &&
          typeof news.project === "string" &&
          typeof news.owner === "string"
        ) {
          listener(news as QueueNews);
        }
      });
      return () => own.close();
    },
  };
}

/** Returns the name of the lock that the document of owner holds. */
function lockOf(owner: string): string {
  return `storycreep-queue:${owner}`;
}

/**
 * What signing out took off the device that the device refused to take, of
 * an account's, which the browser's storage keeps until it has: the batches
 * that it named, by their projects and IDs, and, where it could not read
 * them, every one made until when it signed out (before), as an ISO time.
 */
interface Discarded {
  only: { project_id: string; id: string }[];
  before?: string;
}

/** Returns the key of the browser's storage that keeps what signing out could not take off the device of userId's (Discarded). */
function discardedKey(userId: string): string {
  return `storycreep.queue.discarded.${userId}`;
}

/** Returns what storage keeps that signing out could not take off the device of userId's, if it keeps any that this code wrote. */
function discardedOf(
  storage: Storage | undefined,
  userId: string,
): Discarded | undefined {
  try {
    const value = JSON.parse(
      storage?.getItem(discardedKey(userId)) ?? "null",
    ) as Partial<Discarded> | null;
    if (value === null || !Array.isArray(value.only)) {
      return undefined;
    }
    return {
      only: value.only.filter(
        (r) => typeof r?.project_id === "string" && typeof r.id === "string",
      ),
      ...(typeof value.before === "string" ? { before: value.before } : {}),
    };
  } catch {
    return undefined;
  }
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
  /**
   * The browser's storage, which keeps what signing out could not take off
   * the device, as of a database that refused, until it has (Discarded);
   * without it, such a removal is tried once alone.
   */
  storage?: Storage;
  /** Returns the time now, as an ISO time, which a removal of every batch made until then remembers (Discarded's before). */
  now?: () => string;
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
  const { storage } = options;
  const now = options.now ?? (() => new Date().toISOString());
  // The writes under way, which signing out's removal waits for (discard).
  const writes = new Set<Promise<void>>();
  // Remembers that the device refused to take the batches of userId's of
  // only off it, or, where it is not given, every one made until now, with
  // what it remembered before, which the browser's storage may refuse in
  // turn, as with the site's data blocked.
  const remember = (
    userId: string,
    only: readonly { project_id: string; id: string }[] | undefined,
  ) => {
    const held = discardedOf(storage, userId);
    const times = [held?.before, only === undefined ? now() : undefined]
      .filter((time) => time !== undefined)
      .sort((a, b) => Date.parse(a) - Date.parse(b));
    const before = times.at(-1);
    const discarded: Discarded = {
      only: [...(held?.only ?? []), ...(only ?? [])].map((r) => ({
        project_id: r.project_id,
        id: r.id,
      })),
      ...(before === undefined ? {} : { before }),
    };
    try {
      storage?.setItem(discardedKey(userId), JSON.stringify(discarded));
    } catch {
      // Nothing more is kept of it.
    }
  };
  // Takes off the device what signing out could not of userId's, if any,
  // and forgets it once it has, rejecting where the device refuses again,
  // after which a document takes none of the account's batches (adopt),
  // which would send those that the user signed out without.
  const discarded = async (userId: string) => {
    const held = discardedOf(storage, userId);
    if (held === undefined) {
      return;
    }
    const before = held.before;
    const only =
      before === undefined
        ? held.only
        : [
            ...held.only,
            ...(await db.queued(userId)).filter(
              (r) => Date.parse(r.time) <= Date.parse(before),
            ),
          ];
    await db.deleteQueued(userId, only);
    try {
      storage?.removeItem(discardedKey(userId));
    } catch {
      // It is taken again, of none, next time.
    }
  };
  return {
    hold: (owner) => locks.hold(lockOf(owner)),
    adopt: async (userId, projectId, owner, found, takes = () => true) => {
      // A document takes batches under a lock of the project's, so that two
      // that start at once do not both take those of a tab that closed, and
      // reads which documents are open inside it, so that one that starts
      // meanwhile, whose lock it holds before it takes any, is not taken
      // for closed.
      const takeLock = `storycreep-queue-take:${userId}:${projectId}`;
      const stopped = new AbortController();
      // Tells the other tabs that owner's document holds batches that it
      // took, as a write does (save), so that they wait for its lock, and
      // take them once it is let go, rather than leave them to the next
      // page that opens the project.
      const tell = (taken: QueuedBatch[]) => {
        if (taken.length > 0) {
          broadcast?.post({ user: userId, project: projectId, owner });
        }
        return taken;
      };
      // Takes, under the lock, as owner's, the batches of the owners that
      // the test that from resolves to allows, read inside the lock, and
      // resolves to them: none once owner's document has stopped taking
      // them, as once it was dropped, which sends none of them, and would
      // leave them to no document, nor while it takes none (takes), as one
      // that no page shows, which leaves them to the next that does.
      const take = (from: () => Promise<(other: string) => boolean>) =>
        locks
          .run(takeLock, async () => {
            if (stopped.signal.aborted || !takes()) {
              return [];
            }
            await discarded(userId);
            return db.takeQueued(userId, projectId, owner, await from());
          })
          .then(tell);
      // The owners whose documents no tab holds now.
      const closed = async () => {
        const held = await locks.held();
        return (other: string) => !held.has(lockOf(other));
      };
      // The batches of an open tab's document are taken once its lock is let
      // go, as the tab closes, or the document is dropped; and those that
      // it wrote since, if any, as the next news of it tells.
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
            const more = await take(async () => (o) => o === other).catch(
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
      let unlisten: (() => void) | undefined;
      const stop = () => {
        stopped.abort();
        unlisten?.();
      };
      // Takes those of no open tab's document, and then watches those of
      // the open tabs', resolving to the first.
      const begin = async () => {
        const first = await take(closed).catch(() => [] as QueuedBatch[]);
        if (stopped.signal.aborted) {
          return first;
        }
        unlisten = broadcast?.listen((news) => {
          if (news.user === userId && news.project === projectId) {
            watch(news.owner);
          }
        });
        for (const record of await db
          .queued(userId, projectId)
          .catch(() => [] as QueuedBatch[])) {
          watch(record.owner);
        }
        return first;
      };
      // Takes those that no open tab's document holds, as once owner's
      // takes them again, which it left meanwhile, as of a tab that closed,
      // resolving once it has, or after wait, as of a database that does not
      // open by then, which a read waits for no longer than for the first
      // (whenReady): those that it takes after it gives as it gives those of
      // a tab that closes (found), leaving no timer behind.
      const again = () =>
        new Promise<void>((resolve) => {
          const timer = setTimeout(resolve, wait);
          void take(closed)
            .catch(() => [] as QueuedBatch[])
            .then((more) => {
              if (more.length > 0 && !stopped.signal.aborted) {
                found(more);
              }
              clearTimeout(timer);
              resolve();
            });
        });
      if (await opensWithin(db, wait)) {
        return { batches: await begin(), stop, again };
      }
      // A database that does not open within wait, as one that another tab
      // blocks, leaves the document to start without them, which it is given
      // once the database has opened, as those of a tab that closes are
      // (found), rather than never.
      db.open().then(
        async () => {
          if (stopped.signal.aborted) {
            return;
          }
          const first = await begin();
          if (first.length > 0 && !stopped.signal.aborted) {
            found(first);
          }
        },
        () => {},
      );
      return { batches: [], stop, again };
    },
    save: async (userId, projectId, put, remove, created) => {
      // A database whose open does not settle within wait, as a browser's
      // may never, refuses the write, as a device that refuses it does
      // (ProjectDocument.persist): a batch made meanwhile is refused to its
      // caller, as without a queue, and one of the queue's that a try
      // records as sent is sent all the same.
      const write = opensWithin(db, wait).then((opened) => {
        if (!opened) {
          throw new Error("The device's database did not open in time.");
        }
        return db.saveQueued(userId, projectId, put, remove, created);
      });
      writes.add(write);
      try {
        await write;
      } finally {
        writes.delete(write);
      }
      // Every write that keeps batches tells the other tabs, as one that
      // opened since the document's last write found none of its batches,
      // and so waits for no lock of its yet (adopt).
      for (const owner of new Set(put.map((b) => b.owner))) {
        broadcast?.post({ user: userId, project: projectId, owner });
      }
    },
    discard: async (userId, only) => {
      // The writes under way, which may write the batches again, as a try
      // that records one as sent, are done first; one that comes after
      // writes none of them there again (save). What the device refuses to
      // take off it is taken once it can, before a document takes the
      // account's batches again (discarded).
      await Promise.allSettled([...writes]);
      try {
        await discarded(userId);
        await db.deleteQueued(userId, only);
      } catch (error) {
        remember(userId, only);
        throw error;
      }
    },
  };
}
