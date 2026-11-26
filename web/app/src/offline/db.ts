// What the device keeps of the projects that an account pins for offline
// use (PRD FR-OFF-02, plan note N97), in a database of the browser's
// (IndexedDB, through idb, §10.6): each pinned project's pin, its copy, the
// project's document as of one seq, an entity to a record, and what a page
// that has no answer from the server starts from, the session, the account,
// and the instance. Every record is the account's, under its ID, so that
// another account of the browser reads none of it, and signing out removes
// it (deleteAllBut). A copy is written in one transaction, which the
// browser applies whole or not at all, so that a quota that refuses it, or
// a page that closes meanwhile, leaves the copy that was there, or none.

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
  /** The project's name, as the page last read it, which the storage panel and a failure's notice name it by. */
  name: string;
  /** Whether the user made it available offline themself. */
  chosen: boolean;
  /** Whether it is the project that the account last opened on the device, which is pinned automatically. */
  auto: boolean;
  pinned_at: string;
  /** Whether its copy is being written for the first time, is written, or could not be written the last time that it was tried. */
  status: "downloading" | "ready" | "failed";
  failure?: PinFailure;
}

/** The copy of a pinned project: what the document held, as of seq, but for its entities, which are records of their own (EntityRecord). */
export interface CopyRecord {
  user_id: string;
  project_id: string;
  /** A token of the write, which the next write compares with the one that it was made on, to write what changed alone. */
  version: string;
  seq: number;
  /** The project, with the role that the account had on it, as of seq. */
  project: ProjectBody;
  /** The project's workspace, with the role that the account acts with in it. */
  workspace: Workspace;
  parts: Part[];
  /** About how many bytes its records take, as JSON. */
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

interface Schema extends DBSchema {
  sessions: { key: string; value: KeptSession };
  pins: { key: [string, string]; value: PinRecord };
  copies: { key: [string, string]; value: CopyRecord };
  entities: { key: [string, string, string]; value: EntityRecord };
}

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

/** Returns the size of entity as JSON, which the copy's bytes count. */
function sizeOf(entity: unknown): number {
  return JSON.stringify(entity).length;
}

/** Reports whether error is the browser's refusal of a write beyond its quota. */
export function overQuota(error: unknown): boolean {
  return (
    (error instanceof DOMException || error instanceof Error) &&
    error.name === "QuotaExceededError"
  );
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
  return {
    readCopy: async (userId, projectId) => {
      if (!secure || !(await opensWithin(db, wait))) {
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
          : openDB<Schema>(databaseName, 1, {
              upgrade(db) {
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

  /** Keeps session, the account's, in place of what the device kept of it. */
  async putSession(session: KeptSession): Promise<void> {
    const db = await this.open();
    await db.put("sessions", session);
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
   */
  async changePin(
    userId: string,
    projectId: string,
    change: (pin: PinRecord | undefined) => PinRecord | undefined,
  ): Promise<PinRecord | undefined> {
    const db = await this.open();
    const tx = db.transaction(["pins", "copies", "entities"], "readwrite");
    const pins = tx.objectStore("pins");
    const next = change(await pins.get([userId, projectId]));
    if (next !== undefined && (next.chosen || next.auto)) {
      await Promise.all([pins.put(next), tx.done]);
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
   * Reads the copy of userId's project, the document as of its seq, holding
   * every part that it held, or resolves to undefined where the device has
   * none, or the project is not pinned.
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
    if (pin === undefined || record === undefined) {
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
   * (overQuota). The pin's status is ready once it has written.
   */
  async writeCopy(
    record: Omit<CopyRecord, "version" | "bytes" | "parts">,
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
      if (base !== undefined && stored?.version === base.version) {
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
        bytes -= stored === undefined ? 0 : sizeOf({ ...stored, bytes: 0 });
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
        version,
        parts: [...doc.parts],
        bytes: 0,
      };
      bytes += sizeOf(copy);
      copy.bytes = bytes;
      writes.push(copies.put(copy));
      const ready: PinRecord = { ...pin, status: "ready" };
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

  /** Takes out every record of every account but keep's, or of every account for none, as once the page has signed out. */
  async deleteAllBut(keep: string | undefined): Promise<void> {
    const db = await this.open();
    const tx = db.transaction(
      ["sessions", "pins", "copies", "entities"],
      "readwrite",
    );
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
    const writes: Promise<unknown>[] = [];
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
}
