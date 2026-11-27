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
// it (save); the removal is remembered in the browser's storage as the user
// signs out anyway, before the server has signed the page out (intend),
// and so is one that the device refuses, as of a database that does not
// open, either of which is made before a document takes the account's
// batches again, which takes none until it is (discarded), so that a tab
// that closes first leaves no batch that the user signed out without to be
// sent; while the tab that signs out holds the removal's lock, as the
// server answers, it is not made, as the page may still be signed in
// after, nor are its batches taken, and once the server has signed the
// page out the other tabs are told, which ask for their session, as their
// documents may hold such batches too, which a page signed out keeps none
// of. A database that does not open in time refuses a write, as one that
// refuses it for its quota does, rather than hold the document's writes,
// and the tries that wait for them, for as long as the page is open.

import { formatUuid, newUuid } from "@storycreep/shared/ids";
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
  /** Tells the other tabs that the page signed out of user's session, as the user signed out anyway (QueueStore.intend). */
  signedOut?(user: string): void;
  /** Calls listener with the account that a tab signed out of, as signedOut tells, until what it returns is called. */
  listenSignedOut?(listener: (user: string) => void): () => void;
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
    signedOut: (user) => channel.postMessage({ user, signedOut: true }),
    // On the channel object that tells it, which never hears what it posts
    // itself, so that the other tabs alone hear it, as the tab that signed
    // out knows already.
    listenSignedOut: (listener) => {
      const heard = (event: MessageEvent) => {
        const news = event.data as { user?: unknown; signedOut?: unknown };
        if (news?.signedOut === true && typeof news.user === "string") {
          listener(news.user);
        }
      };
      channel.addEventListener("message", heard);
      return () => channel.removeEventListener("message", heard);
    },
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
 * What signing out takes off the device, of an account's, which the
 * browser's storage keeps until it has, as one that the device refused to
 * take, or that a tab that closed first did not: the batches that it named,
 * by their projects and IDs, and, where it could not read them, every one
 * made until when the user signed out (before), as an ISO time.
 */
interface Discarded {
  only: { project_id: string; id: string }[];
  before?: string;
  /**
   * The lock that the tab that signs out holds as the server answers, of a
   * removal remembered as the user signs out anyway (intend): while a tab
   * holds it, the removal is not made, nor are its batches taken, as the
   * page may still be signed in after (discarded).
   */
  lock?: string;
}

/** Returns the key of the browser's storage that keeps what signing out could not take off the device of userId's (Discarded). */
function discardedKey(userId: string): string {
  return `storycreep.queue.discarded.${userId}`;
}

/** Returns the key of the browser's storage that keeps what signing out anyway takes off the device of userId's, until it has, or the page is still signed in (intend). */
function signingOutKey(userId: string): string {
  return `storycreep.queue.signing-out.${userId}`;
}

/** Returns what storage keeps at key, or null where it keeps nothing, or refuses to be read. */
function storedAt(storage: Storage | undefined, key: string): string | null {
  try {
    return storage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

/** Returns the Discarded that value, as storage kept it, holds, if it is one that this code wrote. */
function discardedOf(value: string | null): Discarded | undefined {
  try {
    const held = JSON.parse(value ?? "null") as Partial<Discarded> | null;
    if (held === null || !Array.isArray(held.only)) {
      return undefined;
    }
    return {
      only: held.only.filter(
        (r) => typeof r?.project_id === "string" && typeof r.id === "string",
      ),
      ...(typeof held.before === "string" ? { before: held.before } : {}),
      ...(typeof held.lock === "string" ? { lock: held.lock } : {}),
    };
  } catch {
    return undefined;
  }
}

/**
 * Returns the test of the batches that removals, of sign-outs under way,
 * take off the device: those that they named, and those made until when the
 * user signed out, of one that could not read them (Discarded's before).
 */
function namedBy(
  removals: readonly Discarded[],
): (record: { project_id: string; id: string; time: string }) => boolean {
  const named = new Set(
    removals.flatMap((d) => d.only.map((r) => `${r.project_id}/${r.id}`)),
  );
  const before = latestOf(removals.map((d) => d.before));
  return (record) =>
    named.has(`${record.project_id}/${record.id}`) ||
    (before !== undefined && Date.parse(record.time) <= Date.parse(before));
}

/** Returns the latest of times, ISO times, or undefined where none is given. */
function latestOf(times: readonly (string | undefined)[]): string | undefined {
  return times
    .filter((time) => time !== undefined)
    .sort((a, b) => Date.parse(a) - Date.parse(b))
    .at(-1);
}

/** Returns what signing out takes off the device, of the batches of only, or, where it is not given, of every one made until now, with what was held before, if anything (Discarded). */
function discardedWith(
  held: Discarded | undefined,
  only: readonly { project_id: string; id: string }[] | undefined,
  now: string,
): Discarded {
  const before = latestOf([held?.before, only === undefined ? now : undefined]);
  const named = new Map(
    [...(held?.only ?? []), ...(only ?? [])].map((r) => [
      `${r.project_id}/${r.id}`,
      { project_id: r.project_id, id: r.id },
    ]),
  );
  return {
    only: [...named.values()],
    ...(before === undefined ? {} : { before }),
  };
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
   * The browser's storage, which keeps what signing out takes off the
   * device, from when the user signs out anyway, and what it could not, as
   * of a database that refused, until it has (Discarded); without it, such
   * a removal is tried once alone.
   */
  storage?: Storage;
  /** Returns the time now, as an ISO time, which a removal of every batch made until then remembers (Discarded's before). */
  now?: () => string;
  /**
   * Called as another tab tells that it signed out of an account's session,
   * as the user signed out anyway (intend), as the page asks for its own
   * then, which signs it out too, where it was that session, so that its
   * documents, dropped as the page signs out, send none of the batches that
   * the user signed out without, as once the account signs in again.
   */
  signedOutElsewhere?: (userId: string) => void;
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
  const { storage, signedOutElsewhere } = options;
  const now = options.now ?? (() => new Date().toISOString());
  if (signedOutElsewhere !== undefined) {
    broadcast?.listenSignedOut?.(signedOutElsewhere);
  }
  // The writes under way, which signing out's removal waits for (discard).
  const writes = new Set<Promise<void>>();
  // Writes value at key of the browser's storage, which may refuse it, as
  // with the site's data blocked, after which nothing more is kept of it.
  const store = (key: string, value: Discarded): string | undefined => {
    const written = JSON.stringify(value);
    try {
      storage?.setItem(key, written);
      return written;
    } catch {
      return undefined;
    }
  };
  // Remembers that the device refused to take the batches of userId's of
  // only off it, or, where it is not given, every one made until now, with
  // what it remembered before.
  const remember = (
    userId: string,
    only: readonly { project_id: string; id: string }[] | undefined,
  ) => {
    const key = discardedKey(userId);
    store(key, discardedWith(discardedOf(storedAt(storage, key)), only, now()));
  };
  // The keys of the browser's storage that keep what signing out takes off
  // the device of userId's: what it could not, and what it does as the
  // user signs out anyway (intend).
  const keysOf = (userId: string) => [
    discardedKey(userId),
    signingOutKey(userId),
  ];
  // Forgets what keys held as read, of each that holds it still, but those
  // that kept says to keep, and reports whether any holds what was written
  // since, as another tab remembered as it signed out, which is not
  // forgotten.
  const forgetRead = (
    keys: readonly string[],
    read: (string | null)[],
    kept: readonly boolean[] = [],
  ) => {
    let since = false;
    keys.forEach((key, i) => {
      if (storedAt(storage, key) !== read[i]) {
        since = true;
      } else if (read[i] !== null && kept[i] !== true) {
        try {
          storage?.removeItem(key);
        } catch {
          // It is taken again, of none, next time.
        }
      }
    });
    return since;
  };
  // Takes off the device what signing out took, or could not, of userId's,
  // if any, and forgets it once it has, rejecting where the device refuses
  // again, after which a document takes none of the account's batches
  // (adopt), which would send those that the user signed out without. What
  // another tab remembers meanwhile, as it signs out, is not forgotten with
  // it, which the storage then holds as written since, and is taken too.
  // A removal of a sign-out under way, whose tab holds its lock as the
  // server answers, is neither made, as the page may still be signed in
  // after, nor forgotten: it resolves to the test of its batches, which a
  // document does not take meanwhile, as it would send them (intend).
  const discarded = async (userId: string) => {
    const keys = keysOf(userId);
    for (;;) {
      const read = keys.map((key) => storedAt(storage, key));
      const removals = read.map(discardedOf);
      const locked = removals.some((d) => d?.lock !== undefined)
        ? await locks.held()
        : new Set<string>();
      // A removal forgotten as the page is still signed in, before its lock
      // was let go (intend), which the locks read after it no longer show,
      // is read anew, rather than made as one whose tab closed.
      if (keys.some((key, i) => storedAt(storage, key) !== read[i])) {
        continue;
      }
      const under = removals.map(
        (d) => d?.lock !== undefined && locked.has(d.lock),
      );
      const spared = namedBy(
        removals.filter((d, i): d is Discarded => d !== undefined && under[i]),
      );
      const held = removals.filter(
        (d, i): d is Discarded => d !== undefined && !under[i],
      );
      if (held.length === 0) {
        return spared;
      }
      const before = latestOf(held.map((d) => d.before));
      const only = [
        ...held.flatMap((d) => d.only),
        ...(before === undefined
          ? []
          : (await db.queued(userId)).filter(
              (r) => Date.parse(r.time) <= Date.parse(before),
            )),
      ];
      await db.deleteQueued(userId, only);
      if (!forgetRead(keys, read, under)) {
        return spared;
      }
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
      // the test that from resolves to allows, read inside the lock once
      // the batches have been, so that one that a document wrote holding
      // its lock is not taken for closed, and resolves to them: none once
      // owner's document has stopped taking them, as once it was dropped,
      // which sends none of them, and would leave them to no document, nor
      // while it takes none (takes), as one that no page shows, which
      // leaves them to the next that does; nor those that a sign-out under
      // way takes off the device, which it leaves to the next take once the
      // page is still signed in (discarded).
      const take = (from: () => Promise<(other: string) => boolean>) =>
        locks
          .run(takeLock, async () => {
            if (stopped.signal.aborted || !takes()) {
              return [];
            }
            const spared = await discarded(userId);
            return db.takeQueued(userId, projectId, owner, async () => {
              const allows = await from();
              return (other, record) => allows(other) && !spared(record);
            });
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
        if (only === undefined) {
          // Every one of the account's goes, and with it what signing out
          // remembered of them until now, which needs no read of them,
          // which the device may refuse, as it did as the user clicked.
          const keys = keysOf(userId);
          const read = keys.map((key) => storedAt(storage, key));
          await db.deleteQueued(userId);
          if (forgetRead(keys, read)) {
            await discarded(userId);
          }
          return;
        }
        await discarded(userId);
        await db.deleteQueued(userId, only);
      } catch (error) {
        remember(userId, only);
        throw error;
      }
    },
    intend: async (userId, only) => {
      // Of this sign-out's alone, so that forgetting it leaves what another
      // remembered, as one whose removal the device refused (remember),
      // with a lock of its own, which the tab holds as the server answers,
      // and lets go as it closes, after which the next take makes it; one
      // that the browser refuses leaves it to be made by the next take.
      const key = signingOutKey(userId);
      const lock = `storycreep-queue-signing-out:${userId}:${formatUuid(newUuid())}`;
      const release = await locks.hold(lock).catch(() => undefined);
      const written = store(key, {
        ...discardedWith(undefined, only, now()),
        ...(release === undefined ? {} : { lock }),
      });
      return (signedOut) => {
        if (
          !signedOut &&
          written !== undefined &&
          storedAt(storage, key) === written
        ) {
          try {
            storage?.removeItem(key);
          } catch {
            // The next document that takes the account's batches takes
            // these off the device, as the user signed out anyway.
          }
        }
        release?.();
        if (signedOut) {
          broadcast?.signedOut?.(userId);
        }
      };
    },
  };
}
