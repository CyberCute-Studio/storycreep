// What the device keeps of the projects that an account pins for offline
// use (PRD FR-OFF-02, plan note N97), in a database of the browser's
// (IndexedDB, through idb, §10.6): each pinned project's pin, its copy, the
// project's document as of one seq, an entity to a record, and what a page
// that has no answer from the server starts from, the session, the account,
// and the instance. Every record is the account's, under its ID, so that
// another account of the browser reads none of it, and signing out removes
// it (deleteAllBut), after which a tab that has yet to find that the
// session changed makes none anew (DeviceRecord). A copy is written in one
// transaction, which the browser applies whole or not at all, so that a
// quota that refuses it, or a page that closes meanwhile, leaves the copy
// that was there, or none. The device also keeps the account's batches that
// the server has yet to answer (PRD FR-OFF-03, document/queue.ts), which
// are no copy's: removing a project from the device, a session that ended,
// and another account's signing in leave them, and only signing out, which
// asks first, takes them (deleteQueued, plan note N98).

import type {
  AccountBody,
  InstanceBody,
  ProjectBody,
  SignedInBody,
} from "@storycreep/shared/api";
import { type DBSchema, type IDBPDatabase, openDB } from "idb";
import {
  type Doc,
  type Entities,
  type Kind,
  kinds,
  type Part,
  projectOf,
} from "../document/doc";
import { inQueueOrder, type QueuedBatch, queueFormat } from "../document/queue";
import type { CopySource } from "../document/store";
import type { Workspace } from "../workspace/queries";

/** The name of the device's database of what it keeps offline. */
export const databaseName = "storycreep-offline";

/** What a page that has no answer from the server starts from, as the account last signed in had them. */
export interface KeptSession {
  user_id: string;
  /** The session, without its CSRF token, which no request made offline needs, and which the page asks for anew once it is online. */
  session: SignedInBody;
  account: AccountBody;
  instance: InstanceBody;
  saved_at: string;
}

/** Why a pin's copy could not be written: the browser's quota, or anything else. */
export type PinFailure = "quota" | "error";

/** A pin of a project, of an account, on the device. */
export interface PinRecord {
  user_id: string;
  project_id: string;
  /** The project's name, as the page read it as it pinned the project, or the copy's last write did, which the storage panel and a failure's notice name it by. */
  name: string;
  /** Whether the user made it available offline themself. */
  chosen: boolean;
  /**
   * Whether it is pinned automatically, as a project that the account opened
   * on the device: the one that it opened last (openedLast), or one opened
   * before it, which is kept until that one's copy is written
   * (dropBeforeLast).
   */
  auto: boolean;
  /** When the account last opened it on the device, while it is auto. */
  opened_at?: string;
  pinned_at: string;
  /** Whether its copy is being written for the first time, is written, or could not be written the last time that it was tried. */
  status: "downloading" | "ready" | "failed";
  failure?: PinFailure;
}

/**
 * The format of the copies that this version of the app writes, which a
 * version of the app that holds its entities otherwise, as with a field that
 * they did not have, takes the next number for: a copy of another format is
 * none to a document, as a history of the undo kept by another version of
 * the app is (document/undo.ts), rather than the project as of its seq,
 * which the server, whose seq the change of its records does not move, has
 * otherwise; the writer's keeper writes the whole document in its place.
 */
export const copyFormat = 1;

/** The copy of a pinned project: what the document held, as of seq, but for its entities, which are records of their own (EntityRecord). */
export interface CopyRecord {
  user_id: string;
  project_id: string;
  /** The format of its records (copyFormat). */
  format: number;
  /** A token of the write, which the next write compares with the one that it was made on, to write what changed alone. */
  version: string;
  seq: number;
  /** The project, with the role that the account had on it, as of seq. */
  project: ProjectBody;
  /** The project's workspace, with the role that the account acts with in it. */
  workspace: Workspace;
  parts: Part[];
  /** About how many bytes its records take, as JSON in UTF-8. */
  bytes: number;
  saved_at: string;
}

/** An entity of a copy. */
export interface EntityRecord {
  user_id: string;
  project_id: string;
  id: string;
  kind: Kind;
  entity: Entities[Kind];
}

/**
 * The account whose records the device keeps: recorded with the first pin
 * that the device makes (changePin, markOpened), and, from then on, as a
 * tab that finds the browser's session, which every tab shares, of another
 * account takes the others' records out (deleteAllBut), null once that
 * session was of none, as once a tab signed out. Until the first pin there
 * is no record, and nothing of any account's to keep from coming back, so
 * that a page writes nothing to the device before it pins a project.
 */
interface DeviceRecord {
  user_id: string | null;
}

/** A batch of an account's project that the server has yet to answer, as the device keeps it (document/queue.ts). */
export interface QueueRecord extends QueuedBatch {
  user_id: string;
  project_id: string;
}

interface Schema extends DBSchema {
  sessions: { key: string; value: KeptSession };
  pins: { key: [string, string]; value: PinRecord };
  copies: { key: [string, string]; value: CopyRecord };
  entities: { key: [string, string, string]; value: EntityRecord };
  device: { key: string; value: DeviceRecord };
  queue: { key: [string, string, string]; value: QueueRecord };
}

/** The key of the device's one record (DeviceRecord). */
const deviceKey = "account";

/** A copy as a document starts from it: the document, as of the copy's seq, and the version of the write that it is of. */
export interface Copy {
  doc: Doc;
  record: CopyRecord;
}

/** What a write of a copy leaves, which the next write of the same document is made on. */
export interface Written {
  doc: Doc;
  version: string;
  bytes: number;
}

/** Returns the bounds of the keys of the records of key's, every key that key begins. */
function under(...key: string[]): IDBKeyRange {
  // An array sorts after every string, so [...key, []] is past every key
  // that holds key's strings and one more.
  return IDBKeyRange.bound(key, [...key, []]);
}

/** Encodes text as UTF-8, which sizeOf counts the bytes of. */
const utf8 = new TextEncoder();

/** Returns the size of value as JSON, in UTF-8 bytes, which the copy's bytes count. */
function bytesOf(value: unknown): number {
  return utf8.encode(JSON.stringify(value)).byteLength;
}

/**
 * The sizes of the entities that sizeOf has sized, by the entity, which the
 * document never changes in place, but replaces: a write made on another
 * sizes what it no longer holds without serialising it again, as each
 * entity of a document read anew, which holds new ones of them all.
 */
const sizes = new WeakMap<object, number>();

/** Returns the size of entity, a document's, as bytesOf does, once for each entity. */
function sizeOf(entity: object): number {
  let size = sizes.get(entity);
  if (size === undefined) {
    size = bytesOf(entity);
    sizes.set(entity, size);
  }
  return size;
}

/** Reports whether error is the browser's refusal of a write beyond its quota. */
export function overQuota(error: unknown): boolean {
  return (
    (error instanceof DOMException || error instanceof Error) &&
    error.name === "QuotaExceededError"
  );
}

/**
 * Returns the pin of pins of the project that the account opened last on
 * the device, its automatic pin opened last, or, of two opened at the same
 * time, as in two tabs, the one of the greater project ID, or undefined for
 * none.
 */
export function openedLast(pins: readonly PinRecord[]): PinRecord | undefined {
  let last: PinRecord | undefined;
  for (const pin of pins) {
    if (!pin.auto) {
      continue;
    }
    const at = pin.opened_at ?? "";
    const lastAt = last?.opened_at ?? "";
    if (
      last === undefined ||
      at > lastAt ||
      (at === lastAt && pin.project_id > last.project_id)
    ) {
      last = pin;
    }
  }
  return last;
}

/**
 * Reports whether the device keeps the records of userId, as device says:
 * where it has recorded none's yet, before its first pin, or userId's.
 */
function keeps(device: DeviceRecord | undefined, userId: string): boolean {
  return device === undefined || device.user_id === userId;
}

/** Returns a new version of a copy's write. */
function newVersion(): string {
  return crypto.randomUUID();
}

/**
 * How long, in milliseconds, a project's document waits at most for the
 * device's database to open, before it reads the project from the server
 * alone (copySource): a browser's open of a database may never settle, as
 * one that another tab blocks, which would else keep every project's pages
 * from showing, though the server answers.
 */
export const openWait = 3_000;

/** Resolves to whether db opens within wait milliseconds, leaving no timer behind. */
export function opensWithin(
  db: OfflineDatabase,
  wait: number,
): Promise<boolean> {
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), wait);
    const done = (opened: boolean) => {
      clearTimeout(timer);
      resolve(opened);
    };
    db.open().then(
      () => done(true),
      () => done(false),
    );
  });
}

/**
 * Returns where a project's document finds the copy that db keeps of it
 * (DocumentOptions.copies): none on a page that is not a secure context,
 * which keeps no project offline (PRD FR-OFF-01), nor where the database
 * cannot be read, nor where it does not open within wait milliseconds
 * (openWait).
 */
export function copySource(
  db: OfflineDatabase,
  secure: boolean,
  wait = openWait,
): CopySource {
  const opens = async () => secure && (await opensWithin(db, wait));
  return {
    seqOf: async (userId, projectId) =>
      (await opens())
        ? db.copySeq(userId, projectId).catch(() => undefined)
        : undefined,
    readCopy: async (userId, projectId) => {
      if (!(await opens())) {
        return undefined;
      }
      const copy = await db.readCopy(userId, projectId).catch(() => undefined);
      return copy === undefined
        ? undefined
        : {
            doc: copy.doc,
            version: copy.record.version,
            bytes: copy.record.bytes,
          };
    },
  };
}

/** The device's database of what it keeps offline, opened at the first use, or never where the browser refuses it. */
export class OfflineDatabase {
  private opening: Promise<IDBPDatabase<Schema>> | undefined;

  /** Opens the database, once, rejecting where the browser has none or refuses it, as with the site's data blocked. */
  open(): Promise<IDBPDatabase<Schema>> {
    if (this.opening === undefined) {
      const opening =
        typeof indexedDB === "undefined"
          ? Promise.reject(new Error("The browser has no IndexedDB."))
          : openDB<Schema>(databaseName, 3, {
              upgrade(db, oldVersion) {
                if (oldVersion < 1) {
                  db.createObjectStore("sessions", { keyPath: "user_id" });
                  db.createObjectStore("pins", {
                    keyPath: ["user_id", "project_id"],
                  });
                  db.createObjectStore("copies", {
                    keyPath: ["user_id", "project_id"],
                  });
                  db.createObjectStore("entities", {
                    keyPath: ["user_id", "project_id", "id"],
                  });
                }
                if (oldVersion < 2) {
                  db.createObjectStore("device");
                }
                if (oldVersion < 3) {
                  db.createObjectStore("queue", {
                    keyPath: ["user_id", "project_id", "id"],
                  });
                }
              },
              // A connection that the browser closed itself, as once the
              // site's data was cleared while the page was open, is opened
              // again at the next use, rather than fail every use after.
              terminated: () => {
                if (this.opening === opening) {
                  this.opening = undefined;
                }
              },
            }).then((db) => {
              // A newer version of the app, in another tab, that needs the
              // database anew is let have it; this one opens it again at its
              // next use.
              db.addEventListener("versionchange", () => {
                db.close();
                if (this.opening === opening) {
                  this.opening = undefined;
                }
              });
              return db;
            });
      this.opening = opening;
      // A database that could not be opened is tried again at the next use.
      opening.catch(() => {
        if (this.opening === opening) {
          this.opening = undefined;
        }
      });
    }
    return this.opening;
  }

  /** Closes the database, as at the end of a test. */
  async close(): Promise<void> {
    const opening = this.opening;
    this.opening = undefined;
    if (opening !== undefined) {
      (await opening.catch(() => undefined))?.close();
    }
  }

  /** Returns the session that the device keeps, of the account last signed in, or undefined for none. */
  async session(): Promise<KeptSession | undefined> {
    const db = await this.open();
    const all = await db.getAll("sessions");
    return all.sort((a, b) => b.saved_at.localeCompare(a.saved_at))[0];
  }

  /**
   * Keeps session, the account's, in place of what the device kept of it,
   * while the device pins a project of the account's, in one transaction,
   * so that a session is never kept after the account's last pin has gone,
   * as one removed meanwhile, here or in another tab, whose session goes
   * with it (OfflineKeeper.read).
   */
  async putSession(session: KeptSession): Promise<void> {
    const db = await this.open();
    const tx = db.transaction(["sessions", "pins"], "readwrite");
    const pinned = await tx.objectStore("pins").count(under(session.user_id));
    if (pinned > 0) {
      await tx.objectStore("sessions").put(session);
    }
    await tx.done;
  }

  /** Takes out the session that the device keeps of userId. */
  async deleteSession(userId: string): Promise<void> {
    const db = await this.open();
    await db.delete("sessions", userId);
  }

  /** Returns the pins of userId. */
  async pins(userId: string): Promise<PinRecord[]> {
    const db = await this.open();
    return db.getAll("pins", under(userId));
  }

  /** Returns the copies of userId, without their entities. */
  async copies(userId: string): Promise<CopyRecord[]> {
    const db = await this.open();
    return db.getAll("copies", under(userId));
  }

  /**
   * Changes the pin of userId's project, in one transaction, as change
   * returns it from the pin that the device has, or undefined for none:
   * a pin that it returns undefined for, or with neither chosen nor auto,
   * is taken out, with the project's copy. It returns the pin that it left.
   * A pin of an account whose records the device no longer keeps, as of a
   * tab that has yet to find that another signed out, or in to another
   * account (DeviceRecord), is not made, nor changed.
   */
  async changePin(
    userId: string,
    projectId: string,
    change: (pin: PinRecord | undefined) => PinRecord | undefined,
  ): Promise<PinRecord | undefined> {
    const db = await this.open();
    const tx = db.transaction(
      ["pins", "copies", "entities", "device"],
      "readwrite",
    );
    const pins = tx.objectStore("pins");
    const [pin, device] = await Promise.all([
      pins.get([userId, projectId]),
      tx.objectStore("device").get(deviceKey),
    ]);
    const next = change(pin);
    if (next !== undefined && (next.chosen || next.auto)) {
      if (!keeps(device, userId)) {
        await tx.done;
        return undefined;
      }
      await Promise.all([
        pins.put(next),
        device === undefined
          ? tx.objectStore("device").put({ user_id: userId }, deviceKey)
          : undefined,
        tx.done,
      ]);
      return next;
    }
    await Promise.all([
      pins.delete([userId, projectId]),
      tx.objectStore("copies").delete([userId, projectId]),
      tx.objectStore("entities").delete(under(userId, projectId)),
      tx.done,
    ]);
    return undefined;
  }

  /**
   * Pins userId's project, as change returns its pin from the one that the
   * device has, or undefined for none, as the project that the account
   * opened last on the device, at at, in one transaction: automatic, and
   * opened after every other automatic pin of the account's, a millisecond
   * after the one opened last where at is not past it, as in the same
   * millisecond, so that openedLast finds it; but for an account whose
   * records the device no longer keeps, as changePin.
   */
  async markOpened(
    userId: string,
    projectId: string,
    change: (pin: PinRecord | undefined) => PinRecord,
    at: string,
  ): Promise<void> {
    const db = await this.open();
    const tx = db.transaction(["pins", "device"], "readwrite");
    const [pins, device] = await Promise.all([
      tx.objectStore("pins").getAll(under(userId)),
      tx.objectStore("device").get(deviceKey),
    ]);
    if (!keeps(device, userId)) {
      await tx.done;
      return;
    }
    const before = openedLast(pins.filter((p) => p.project_id !== projectId));
    let openedAt = at;
    if (before?.opened_at !== undefined && before.opened_at >= at) {
      const after = Date.parse(before.opened_at) + 1;
      if (!Number.isNaN(after)) {
        openedAt = new Date(after).toISOString();
      }
    }
    const pin = change(pins.find((p) => p.project_id === projectId));
    await Promise.all([
      tx.objectStore("pins").put({ ...pin, auto: true, opened_at: openedAt }),
      device === undefined
        ? tx.objectStore("device").put({ user_id: userId }, deviceKey)
        : undefined,
      tx.done,
    ]);
  }

  /**
   * Takes out, in one transaction, every automatic pin of userId's but the
   * one of the project that the account opened last (openedLast), once that
   * one's copy is written (ready), with its copy, but for a pin that the
   * user chose, which is kept, with its copy, no longer automatic. It
   * resolves to whether it changed any pin.
   */
  async dropBeforeLast(userId: string): Promise<boolean> {
    const db = await this.open();
    const tx = db.transaction(["pins", "copies", "entities"], "readwrite");
    const pins = tx.objectStore("pins");
    const all = await pins.getAll(under(userId));
    const last = openedLast(all);
    const writes: Promise<unknown>[] = [];
    if (last?.status === "ready") {
      for (const pin of all) {
        if (!pin.auto || pin.project_id === last.project_id) {
          continue;
        }
        if (pin.chosen) {
          const kept: PinRecord = { ...pin, auto: false };
          delete kept.opened_at;
          writes.push(pins.put(kept));
        } else {
          writes.push(
            pins.delete([userId, pin.project_id]),
            tx.objectStore("copies").delete([userId, pin.project_id]),
            tx.objectStore("entities").delete(under(userId, pin.project_id)),
          );
        }
      }
    }
    await Promise.all([...writes, tx.done]);
    return writes.length > 0;
  }

  /**
   * Sets the status of the pin of userId's project, if the device has it, as
   * once its copy could not be written.
   */
  async setStatus(
    userId: string,
    projectId: string,
    status: PinRecord["status"],
    failure?: PinFailure,
  ): Promise<void> {
    const db = await this.open();
    const tx = db.transaction("pins", "readwrite");
    const pin = await tx.store.get([userId, projectId]);
    if (pin !== undefined) {
      const next: PinRecord = { ...pin, status };
      if (failure === undefined) {
        delete next.failure;
      } else {
        next.failure = failure;
      }
      await tx.store.put(next);
    }
    await tx.done;
  }

  /**
   * Returns the seq of the copy of userId's project, without reading its
   * entities, or undefined where the device has none, of this version's
   * format (copyFormat), or the project is not pinned, as a project's
   * document reads it to tell whether it uses the copy, before it reads the
   * whole of it (readCopy).
   */
  async copySeq(
    userId: string,
    projectId: string,
  ): Promise<number | undefined> {
    const db = await this.open();
    const tx = db.transaction(["pins", "copies"], "readonly");
    const [pin, record] = await Promise.all([
      tx.objectStore("pins").get([userId, projectId]),
      tx.objectStore("copies").get([userId, projectId]),
      tx.done,
    ]);
    return pin === undefined || record?.format !== copyFormat
      ? undefined
      : record.seq;
  }

  /**
   * Reads the copy of userId's project, the document as of its seq, holding
   * every part that it held, or resolves to undefined where the device has
   * none, of this version's format (copyFormat), or the project is not
   * pinned.
   */
  async readCopy(userId: string, projectId: string): Promise<Copy | undefined> {
    const db = await this.open();
    const tx = db.transaction(["pins", "copies", "entities"], "readonly");
    const [pin, record, entities] = await Promise.all([
      tx.objectStore("pins").get([userId, projectId]),
      tx.objectStore("copies").get([userId, projectId]),
      tx.objectStore("entities").getAll(under(userId, projectId)),
      tx.done,
    ]);
    if (pin === undefined || record?.format !== copyFormat) {
      return undefined;
    }
    const maps = new Map<Kind, Map<string, Entities[Kind]>>(
      kinds.map((kind) => [kind, new Map()]),
    );
    for (const r of entities) {
      maps.get(r.kind)?.set(r.id, r.entity);
    }
    const doc: Doc = {
      seq: record.seq,
      project: projectOf(record.project),
      folders: maps.get("folders") as Doc["folders"],
      boards: maps.get("boards") as Doc["boards"],
      nodes: maps.get("nodes") as Doc["nodes"],
      connections: maps.get("connections") as Doc["connections"],
      variables: maps.get("variables") as Doc["variables"],
      componentTypes: maps.get("componentTypes") as Doc["componentTypes"],
      components: maps.get("components") as Doc["components"],
      parts: new Set(record.parts),
      gone: new Map(),
    };
    return { doc, record };
  }

  /**
   * Writes doc as the copy of userId's project, with what record says of
   * it, in one transaction: what changed since base alone, where the copy
   * that the device has is base's write still, and otherwise the whole
   * document, in place of what the copy had. It writes nothing, and
   * resolves to undefined, where the project is not pinned, as once it was
   * unpinned in another tab meanwhile; and it rejects, writing nothing,
   * where the browser refuses the transaction, as for its quota
   * (overQuota). The pin's status is ready once it has written, and its
   * name the project's, as of the copy, which the storage panel and the
   * header's notice name it by. The copy is of this version's format
   * (copyFormat).
   */
  async writeCopy(
    record: Omit<CopyRecord, "format" | "version" | "bytes" | "parts">,
    doc: Doc,
    base: Written | undefined,
  ): Promise<Written | undefined> {
    const db = await this.open();
    const key: [string, string] = [record.user_id, record.project_id];
    const tx = db.transaction(["pins", "copies", "entities"], "readwrite");
    const pins = tx.objectStore("pins");
    const copies = tx.objectStore("copies");
    const entities = tx.objectStore("entities");
    const [pin, stored] = await Promise.all([pins.get(key), copies.get(key)]);
    if (pin === undefined) {
      tx.abort();
      await tx.done.catch(() => undefined);
      return undefined;
    }
    const writes: Promise<unknown>[] = [];
    const put = (kind: Kind, entity: Entities[Kind]) =>
      writes.push(
        entities.put({
          user_id: record.user_id,
          project_id: record.project_id,
          id: entity.id,
          kind,
          entity,
        }),
      );
    let bytes: number;
    let version: string;
    try {
      if (
        base !== undefined &&
        stored?.version === base.version &&
        stored.format === copyFormat
      ) {
        bytes = base.bytes;
        for (const kind of kinds) {
          const before = base.doc[kind] as ReadonlyMap<string, Entities[Kind]>;
          const after = doc[kind] as ReadonlyMap<string, Entities[Kind]>;
          if (before === after) {
            continue;
          }
          for (const [id, entity] of after) {
            const was = before.get(id);
            if (was !== entity) {
              bytes += sizeOf(entity) - (was === undefined ? 0 : sizeOf(was));
              put(kind, entity);
            }
          }
          for (const [id, was] of before) {
            if (!after.has(id)) {
              bytes -= sizeOf(was);
              writes.push(
                entities.delete([record.user_id, record.project_id, id]),
              );
            }
          }
        }
        // The copy's record as it was sized, before its bytes were set.
        bytes -= bytesOf({ ...stored, bytes: 0 });
      } else {
        bytes = 0;
        writes.push(entities.delete(under(record.user_id, record.project_id)));
        for (const kind of kinds) {
          for (const entity of (
            doc[kind] as ReadonlyMap<string, Entities[Kind]>
          ).values()) {
            bytes += sizeOf(entity);
            put(kind, entity);
          }
        }
      }
      version = newVersion();
      const copy: CopyRecord = {
        ...record,
        format: copyFormat,
        version,
        parts: [...doc.parts],
        bytes: 0,
      };
      bytes += bytesOf(copy);
      copy.bytes = bytes;
      writes.push(copies.put(copy));
      const ready: PinRecord = {
        ...pin,
        name: record.project.name,
        status: "ready",
      };
      delete ready.failure;
      writes.push(pins.put(ready));
    } catch (error) {
      // A request that the browser refuses at once, as for a value that it
      // cannot store, would otherwise leave those before it to commit.
      tx.abort();
      await Promise.allSettled([...writes, tx.done]);
      throw error;
    }
    await Promise.all([...writes, tx.done]);
    return { doc, version, bytes };
  }

  /**
   * Takes out every record of every account but keep's, or of every account
   * for none, as once the page has signed out, and records that the device
   * keeps keep's alone, or none, from then on, once it has recorded an
   * account's (DeviceRecord): a pin of another, as of a tab that has yet to
   * find that the browser's session changed, is not made (changePin,
   * markOpened), so that a copy, and a session, which a pin is needed for,
   * are not written of it either.
   */
  async deleteAllBut(keep: string | undefined): Promise<void> {
    const db = await this.open();
    const tx = db.transaction(
      ["sessions", "pins", "copies", "entities", "device"],
      "readwrite",
    );
    const device = tx.objectStore("device");
    const recorded = await device.get(deviceKey);
    const users = new Set<string>();
    for (const key of await tx.objectStore("sessions").getAllKeys()) {
      users.add(key);
    }
    for (const [user] of await tx.objectStore("pins").getAllKeys()) {
      users.add(user);
    }
    for (const [user] of await tx.objectStore("copies").getAllKeys()) {
      users.add(user);
    }
    if (keep !== undefined) {
      users.delete(keep);
    }
    // The device's record changes once it has one, or once it held another
    // account's records without one, as a database of an earlier version,
    // rather than as each page follows its session.
    const kept = keep ?? null;
    const writes: Promise<unknown>[] =
      (recorded !== undefined || users.size > 0) && recorded?.user_id !== kept
        ? [device.put({ user_id: kept }, deviceKey)]
        : [];
    // The account's queued batches are no copy's, and stay (queued).
    for (const user of users) {
      writes.push(
        tx.objectStore("sessions").delete(user),
        tx.objectStore("pins").delete(under(user)),
        tx.objectStore("copies").delete(under(user)),
        tx.objectStore("entities").delete(under(user)),
      );
    }
    await Promise.all([...writes, tx.done]);
  }

  /** Returns the batches of userId's that the device keeps for the server, of projectId's alone if given, in the queue's order. */
  async queued(userId: string, projectId?: string): Promise<QueueRecord[]> {
    const db = await this.open();
    const all = await db.getAll(
      "queue",
      projectId === undefined ? under(userId) : under(userId, projectId),
    );
    return inQueueOrder(all) as QueueRecord[];
  }

  /**
   * Takes the batches of userId's projectId that the test that from
   * resolves to allows, by their owners, or themselves, asked for once the
   * batches have been read, of this version's format (queueFormat), as
   * owner's, and resolves to them in the queue's order: a batch of another
   * format, which another
   * version of the app wrote, is kept as it is. One written after the read
   * is not taken, as its owner may have been allowed for having written
   * none yet, and neither is one whose owner changed after it, as another
   * document took it; the rest are taken in one transaction.
   */
  async takeQueued(
    userId: string,
    projectId: string,
    owner: string,
    from: () => Promise<(owner: string, record: QueueRecord) => boolean>,
  ): Promise<QueueRecord[]> {
    const found = (
      await (await this.open()).getAll("queue", under(userId, projectId))
    ).filter(
      (record) => record.owner !== owner && record.format === queueFormat,
    );
    if (found.length === 0) {
      return [];
    }
    const allows = await from();
    const owners = new Map(
      found
        .filter((record) => allows(record.owner, record))
        .map((record) => [record.id, record.owner]),
    );
    if (owners.size === 0) {
      return [];
    }
    const tx = (await this.open()).transaction("queue", "readwrite");
    const store = tx.objectStore("queue");
    const taken: QueueRecord[] = [];
    const writes: Promise<unknown>[] = [];
    for (const record of await store.getAll(under(userId, projectId))) {
      if (
        record.format === queueFormat &&
        owners.get(record.id) === record.owner
      ) {
        const mine = { ...record, owner };
        taken.push(mine);
        writes.push(store.put(mine));
      }
    }
    await Promise.all([...writes, tx.done]);
    return inQueueOrder(taken) as QueueRecord[];
  }

  /**
   * Writes put and removes the batches of the IDs of remove, of userId's
   * projectId, in one transaction: a batch of put written before, whose ID
   * created does not hold, as it holds every one's unless given, is written
   * only where the device still keeps it, as signing out may have taken it
   * off the device meanwhile, in another tab too (deleteQueued).
   */
  async saveQueued(
    userId: string,
    projectId: string,
    put: readonly QueuedBatch[],
    remove: readonly string[],
    created: readonly string[] = put.map((batch) => batch.id),
  ): Promise<void> {
    const db = await this.open();
    const tx = db.transaction("queue", "readwrite");
    const store = tx.objectStore("queue");
    const fresh = new Set(created);
    await Promise.all([
      ...remove.map((id) => store.delete([userId, projectId, id])),
      ...put.map(async (batch) => {
        const key: [string, string, string] = [userId, projectId, batch.id];
        if (!fresh.has(batch.id) && (await store.count(key)) === 0) {
          return;
        }
        await store.put({ ...batch, user_id: userId, project_id: projectId });
      }),
      tx.done,
    ]);
  }

  /**
   * Takes the batches of userId's that the device keeps for the server off
   * it, in one transaction, as once the user signs out, having been asked
   * (plan note N98): those of only, by their projects and IDs, as those
   * that the user was asked of, where it is given, or else every one.
   */
  async deleteQueued(
    userId: string,
    only?: readonly Pick<QueueRecord, "project_id" | "id">[],
  ): Promise<void> {
    const db = await this.open();
    const tx = db.transaction("queue", "readwrite");
    const store = tx.objectStore("queue");
    await Promise.all([
      ...(only === undefined
        ? [store.delete(under(userId))]
        : only.map((r) => store.delete([userId, r.project_id, r.id]))),
      tx.done,
    ]);
  }
}
