// The projects that the account keeps offline on the device (PRD FR-OFF-02,
// plan note N97): the pins, the copies, and what the page shows of them.
// A project is pinned once the user makes it available offline, and once
// the account opens its pages, as the last opened, whose pin moves to the
// next project that it opens, the copy of the one before taken out unless
// the user made it available offline too. While the page is signed in, one
// tab of the browser's keeps the copies, the one that holds the lock of the
// writer (Web Locks): it holds the document of each pinned project, which
// the real-time channel keeps current, has it read the whole of the project,
// every board's content and every component whole, which play mode needs
// (document/whole.ts), and writes it to the device once it has, and holds
// nothing that it has yet to read from the server (ProjectDocument.settled),
// as of one seq, in one transaction (offline/db.ts), which the browser
// applies whole or not at all: a quota that refuses it leaves the copy that
// was there, or none, and the pin says so, which the header and the storage
// panel tell the user, naming the project. Any tab changes the pins, and
// tells the others (BroadcastChannel), whose keepers read them again. Every
// record is the account's: once the page's session is of another account,
// or of none, as once it has signed out, what the device keeps of every
// other account is taken out.

import type { QueryClient } from "@tanstack/react-query";
import { createStore, type StoreApi } from "zustand/vanilla";
import { accountQuery, instanceQuery, notFoundBy, sessionQuery } from "../api";
import type { Documents } from "../document/documents";
import { inTurns, type ProjectDocument } from "../document/store";
import { unreadOf } from "../document/whole";
import type { ErrorLog } from "../errors/log";
import { projectQuery } from "../project/queries";
import { workspaceQuery } from "../workspace/queries";
import {
  type CopyRecord,
  OfflineDatabase,
  overQuota,
  type PinFailure,
  type PinRecord,
  type Written,
} from "./db";
import type { KeptReads } from "./kept";

/** A pinned project, as the page shows it. */
export interface PinView {
  projectId: string;
  name: string;
  /** Whether the user made it available offline. */
  chosen: boolean;
  /** Whether it is the project that the account last opened on the device. */
  auto: boolean;
  status: PinRecord["status"];
  failure?: PinFailure;
  /** The copy that the device keeps of it, if it keeps one. */
  copy?: { seq: number; bytes: number; savedAt: string };
}

/** What the page shows of the projects that the device keeps offline. */
export interface OfflineState {
  /**
   * Whether the page can keep projects offline: in a secure context, where
   * the browser gives it a service worker and persistent storage (PRD
   * FR-OFF-01), with a database that opens; undefined until it is known.
   */
  available: boolean | undefined;
  /** The account whose pins these are, or undefined while the page is signed out. */
  userId: string | undefined;
  pins: ReadonlyMap<string, PinView>;
}

/** The channel that tells the browser's other tabs that the pins or the copies changed. */
export interface Broadcast {
  post(): void;
  listen(listener: () => void): () => void;
}

export interface KeeperOptions {
  queryClient: QueryClient;
  documents: Documents;
  log: ErrorLog;
  /** The device's database, a new one if not set. */
  db?: OfflineDatabase;
  /** Whether the page is a secure context (isSecureContext). */
  secure: boolean;
  /** Reports whether the browser says that it has a connection. */
  online: () => boolean;
  /** Calls listener each time that the browser's connection comes or goes, until what it returns is called. */
  onConnection: (listener: (online: boolean) => void) => () => void;
  /**
   * Runs write once the tab holds the lock of the writer of the copies, for
   * as long as the promise that it returns has not settled; at once where
   * the browser has no locks (writerLock).
   */
  lock: (write: () => Promise<void>) => void;
  /** The other tabs' channel, or none (broadcast). */
  broadcast?: Broadcast;
  /** The browser's storage manager, which persist asks to keep the data. */
  storage?: Pick<StorageManager, "persist">;
  /** Returns the time now, as the records write it. */
  now?: () => string;
  /** How long, in milliseconds, after a change of a kept project's document its copy is written: a second, if not set. */
  writeDelay?: number;
  /** How long, in milliseconds, after a read or a write that failed it is tried again: 30 seconds, if not set. */
  retryDelay?: number;
}

/** How many reads a kept project's document makes at once, as play mode's (inTurns). */
const readsAtOnce = 6;

/** Returns a lock of the writer of navigator's, or one that every tab holds at once where the browser has no Web Locks. */
export function writerLock(
  navigator: Pick<Navigator, "locks"> | undefined,
): KeeperOptions["lock"] {
  const locks = navigator?.locks;
  if (locks === undefined) {
    return (write) => void write();
  }
  return (write) =>
    void locks
      .request("storycreep-offline-writer", () => write())
      .catch(() => undefined);
}

/** Returns the channel of the browser's tabs, or none where it has no BroadcastChannel. */
export function tabsBroadcast(): Broadcast | undefined {
  if (typeof BroadcastChannel === "undefined") {
    return undefined;
  }
  const channel = new BroadcastChannel("storycreep-offline");
  return {
    post: () => channel.postMessage("changed"),
    listen: (listener) => {
      const on = () => listener();
      channel.addEventListener("message", on);
      return () => channel.removeEventListener("message", on);
    },
  };
}

/** The device's pins and copies of the page's account. */
export class OfflineKeeper {
  readonly store: StoreApi<OfflineState>;
  readonly db: OfflineDatabase;
  readonly options: KeeperOptions;
  readonly now: () => string;
  /** The projects whose copies this tab keeps, once it is the writer, by their IDs. */
  private readonly kept = new Map<string, Kept>();
  /** Why the copy of each project that the tab could not write was not written, which the device may not have been able to keep (failed). */
  private readonly failures = new Map<string, PinFailure>();
  private writer = false;
  /** The account whose records the device keeps, null once the page is signed out, undefined until the session is known. */
  private followed: string | null | undefined;
  /** The project whose pages the page last opened, which opened has pinned already. */
  private lastOpened: string | undefined;
  /** The project whose pages the page opened while the browser had no connection, which opened pins once it has one. */
  private openedOffline: { projectId: string; name: string } | undefined;
  /** The changes of the records under way, one at a time, in order. */
  private queue: Promise<void> = Promise.resolve();
  private readonly stops: (() => void)[] = [];
  private stopWriting: (() => void) | undefined;
  /** Whether the keeper has stopped, after which it changes no record. */
  private stopped = false;

  constructor(options: KeeperOptions) {
    this.options = options;
    this.db = options.db ?? new OfflineDatabase();
    this.now = options.now ?? (() => new Date().toISOString());
    this.store = createStore<OfflineState>(() => ({
      available: options.secure ? undefined : false,
      userId: undefined,
      pins: new Map(),
    }));
  }

  /** The reads of what the device keeps, which the page's queries read once the server does not answer (offline/kept.ts). */
  readonly reads: KeptReads = {
    session: () => this.db.session(),
    project: async (userId, projectId) =>
      (await this.db.copies(userId)).find((c) => c.project_id === projectId)
        ?.project,
    workspace: async (userId, workspaceId) =>
      (await this.db.copies(userId)).find((c) => c.workspace.id === workspaceId)
        ?.workspace,
  };

  /** Starts following the page's session, and keeping its account's copies once the tab is the writer. */
  start(): void {
    if (!this.options.secure) {
      return;
    }
    const client = this.options.queryClient;
    void this.db.open().then(
      () => this.store.setState({ available: true }),
      () => this.store.setState({ available: false }),
    );
    this.stops.push(
      client.getQueryCache().subscribe((event) => {
        const key = event.query.queryKey[0];
        if (key === sessionQuery.queryKey[0]) {
          this.follow();
        } else if (
          (key === accountQuery.queryKey[0] ||
            key === instanceQuery.queryKey[0]) &&
          event.type === "updated" &&
          event.action.type === "success"
        ) {
          // What the device keeps of them follows what the page reads, as a
          // change of the account's name, but not each component that reads
          // them as it mounts.
          this.enqueue(() => this.saveSession());
        }
      }),
    );
    this.follow();
    const broadcast = this.options.broadcast;
    if (broadcast !== undefined) {
      this.stops.push(broadcast.listen(() => this.enqueue(() => this.read())));
    }
    this.stops.push(
      this.options.onConnection((online) => {
        if (online) {
          const opened = this.openedOffline;
          this.openedOffline = undefined;
          if (opened !== undefined) {
            this.moveAuto(opened.projectId, opened.name);
          }
          for (const kept of this.kept.values()) {
            kept.schedule(0);
          }
        }
      }),
    );
    this.options.lock(
      () =>
        new Promise<void>((resolve) => {
          this.stopWriting = resolve;
          this.writer = true;
          this.enqueue(async () => this.reconcile());
        }),
    );
  }

  /** Stops keeping the copies, and following the session, as once the page goes. */
  stop(): void {
    this.stopped = true;
    for (const stop of this.stops.splice(0)) {
      stop();
    }
    for (const kept of this.kept.values()) {
      kept.stop();
    }
    this.kept.clear();
    this.writer = false;
    this.stopWriting?.();
  }

  /** Resolves once the change of the records under way is done, after which a keeper that has stopped changes none, as before its database is closed. */
  idle(): Promise<void> {
    return this.queue;
  }

  /** Reports whether the page can keep projects offline, as far as it knows yet. */
  get available(): boolean {
    return this.store.getState().available === true;
  }

  /** Makes the project of projectId, named name, available offline, as the user asked (chosen), and asks the browser to keep the device's data. */
  pin(projectId: string, name: string): Promise<void> {
    // The browser decides whether it keeps the data persistently, as of the
    // user's engagement with the site, or by asking them, once the user has
    // asked for it, as with this click.
    void this.options.storage?.persist?.().catch(() => false);
    return this.change(projectId, (pin) => ({
      ...(pin ?? this.newPin(projectId, name)),
      name,
      chosen: true,
    }));
  }

  /**
   * Undoes the user's making the project of projectId available offline: its
   * copy goes, at once, unless it is the project that the account last
   * opened on the device, which is pinned for that until the account opens
   * another.
   */
  unchoose(projectId: string): Promise<void> {
    return this.change(projectId, (pin) =>
      pin === undefined ? undefined : { ...pin, chosen: false },
    );
  }

  /** Takes the project of projectId off the device: its pin and its copy, at once (AC3). */
  unpin(projectId: string): Promise<void> {
    return this.change(projectId, () => undefined);
  }

  /**
   * Pins the project of projectId, named name, as the project that the
   * account last opened on the device, in place of the one before, whose
   * copy goes unless the user made it available offline. While the browser
   * has no connection, the pin moves once it has one: the copy of the one
   * before, which the device could not download again until then, stays
   * meanwhile, as for a user on a flight who goes from one project that the
   * device keeps to another, and back.
   */
  opened(projectId: string, name: string): void {
    if (!this.options.secure || this.lastOpened === `${projectId}:${name}`) {
      return;
    }
    this.lastOpened = `${projectId}:${name}`;
    if (!this.options.online()) {
      this.openedOffline = { projectId, name };
      return;
    }
    this.openedOffline = undefined;
    this.moveAuto(projectId, name);
  }

  /** Moves the pin of the project that the account last opened to projectId, named name, as opened says. */
  private moveAuto(projectId: string, name: string): void {
    // The session's account is known once the changes before it are done,
    // as the page's first, which follows the session (follow).
    this.enqueue(async () => {
      const userId = this.store.getState().userId;
      if (userId === undefined || !(await this.ready())) {
        return;
      }
      for (const pin of await this.db.pins(userId)) {
        if (pin.auto && pin.project_id !== projectId) {
          await this.db.changePin(userId, pin.project_id, (p) =>
            p === undefined ? undefined : { ...p, auto: false },
          );
        }
      }
      await this.db.changePin(userId, projectId, (pin) => ({
        ...(pin ?? this.newPin(projectId, name)),
        name,
        auto: true,
      }));
      await this.changed();
    });
  }

  /**
   * Records that the copy of userId's projectId could not be written, and
   * why, which the page shows: in the tab, and on the device for the other
   * tabs, where it can, as a device whose storage is full may refuse even
   * that; once the changes before it are done, and while the device still
   * pins the project for the page's account, so that a write that failed as
   * the project was unpinned, here or in another tab, or as the session
   * became another account's, is not shown once it is pinned again.
   */
  failed(
    userId: string,
    projectId: string,
    failure: PinFailure,
  ): Promise<void> {
    return this.enqueue(async () => {
      if (this.store.getState().userId !== userId || !(await this.ready())) {
        return;
      }
      const pins = await this.db.pins(userId);
      if (!pins.some((pin) => pin.project_id === projectId)) {
        return;
      }
      this.failures.set(projectId, failure);
      await this.db
        .setStatus(userId, projectId, "failed", failure)
        .catch(() => undefined);
      await this.changed();
    });
  }

  /** Records that the copy of projectId has been written, which the page shows. */
  async wrote(projectId: string): Promise<void> {
    if (this.stopped) {
      return;
    }
    this.failures.delete(projectId);
    await this.saveSession();
    await this.changed();
  }

  /** Takes the pin and the copy of projectId off the device, as once the project can no longer be read. */
  forget(projectId: string): Promise<void> {
    return this.change(projectId, () => undefined);
  }

  /** Returns a new pin of projectId, of the account, neither chosen nor auto yet. */
  private newPin(projectId: string, name: string): PinRecord {
    return {
      user_id: this.store.getState().userId ?? "",
      project_id: projectId,
      name,
      chosen: false,
      auto: false,
      pinned_at: this.now(),
      status: "downloading",
    };
  }

  /** Changes the pin of projectId, as change returns it, once the changes before it are done. */
  private change(
    projectId: string,
    change: (pin: PinRecord | undefined) => PinRecord | undefined,
  ): Promise<void> {
    if (!this.options.secure) {
      return Promise.resolve();
    }
    return this.enqueue(async () => {
      const userId = this.store.getState().userId;
      if (userId === undefined || !(await this.ready())) {
        return;
      }
      const pin = await this.db.changePin(userId, projectId, change);
      if (pin === undefined) {
        this.failures.delete(projectId);
      }
      await this.changed();
    });
  }

  /**
   * Runs task once the changes before it are done, and resolves or rejects
   * as it does; a failure does not stop the next. Once the keeper has
   * stopped, a task that has not begun does nothing.
   */
  private enqueue(task: () => Promise<void>): Promise<void> {
    const run = this.queue.then(() => (this.stopped ? undefined : task()));
    this.queue = run.catch((error: unknown) => {
      this.options.log.record("error", error);
    });
    return run;
  }

  /** Follows the page's session: once it is of another account, or of none, the device's records of every other are taken out. */
  private follow(): void {
    const session = this.options.queryClient.getQueryData(
      sessionQuery.queryKey,
    );
    if (session === undefined) {
      return;
    }
    const userId = session?.user_id ?? null;
    if (userId === this.followed) {
      return;
    }
    this.followed = userId;
    this.lastOpened = undefined;
    this.openedOffline = undefined;
    this.enqueue(async () => {
      for (const kept of this.kept.values()) {
        kept.stop();
      }
      this.kept.clear();
      this.failures.clear();
      this.store.setState({ userId: undefined, pins: new Map() });
      if (!(await this.ready())) {
        return;
      }
      await this.db.deleteAllBut(userId ?? undefined);
      this.store.setState({ userId: userId ?? undefined });
      await this.read();
      this.options.broadcast?.post();
    });
  }

  /** Resolves to whether the database opens. */
  private async ready(): Promise<boolean> {
    try {
      await this.db.open();
      return true;
    } catch {
      return false;
    }
  }

  /** Reads the pins and the copies again, tells the other tabs, and keeps what is pinned. */
  private async changed(): Promise<void> {
    await this.read();
    this.options.broadcast?.post();
  }

  /** Reads the account's pins and copies from the device, which the page shows, and has the writer keep what is pinned. */
  private async read(): Promise<void> {
    const userId = this.store.getState().userId;
    if (userId === undefined || !(await this.ready())) {
      return;
    }
    const [pins, copies] = await Promise.all([
      this.db.pins(userId),
      this.db.copies(userId),
    ]);
    if (this.store.getState().userId !== userId) {
      return;
    }
    const byProject = new Map<string, CopyRecord>(
      copies.map((c) => [c.project_id, c]),
    );
    const views = new Map<string, PinView>();
    for (const pin of pins) {
      const copy = byProject.get(pin.project_id);
      const failure = this.failures.get(pin.project_id) ?? pin.failure;
      views.set(pin.project_id, {
        projectId: pin.project_id,
        name: pin.name,
        chosen: pin.chosen,
        auto: pin.auto,
        status: this.failures.has(pin.project_id) ? "failed" : pin.status,
        ...(failure === undefined ? {} : { failure }),
        ...(copy === undefined
          ? {}
          : {
              copy: {
                seq: copy.seq,
                bytes: copy.bytes,
                savedAt: copy.saved_at,
              },
            }),
      });
    }
    this.store.setState({ pins: views });
    if (pins.length === 0) {
      await this.db.deleteSession(userId);
    }
    this.reconcile();
  }

  /** Has the writer keep the copy of each pinned project, and no other. */
  private reconcile(): void {
    const { userId, pins } = this.store.getState();
    if (!this.writer || userId === undefined) {
      return;
    }
    let removed = false;
    for (const [projectId, kept] of this.kept) {
      if (!pins.has(projectId)) {
        kept.stop();
        this.kept.delete(projectId);
        removed = true;
      }
    }
    for (const projectId of pins.keys()) {
      if (!this.kept.has(projectId)) {
        const kept = new Kept(this, userId, projectId);
        this.kept.set(projectId, kept);
        kept.start();
      }
    }
    // A copy that the browser's quota refused is tried again once another
    // project's copy has gone, in this tab or another, which may have made
    // room for it.
    if (removed) {
      for (const kept of this.kept.values()) {
        kept.retry();
      }
    }
  }

  /**
   * Keeps the session, the account, and the instance, as the page has them,
   * for a page that loads with no answer from the server, while the account
   * keeps a project offline: without the session's CSRF token, which no
   * request made offline needs, and which the page asks for anew once it is
   * online (main.tsx).
   */
  private async saveSession(): Promise<void> {
    const { userId, pins } = this.store.getState();
    const client = this.options.queryClient;
    const session = client.getQueryData(sessionQuery.queryKey);
    const account = client.getQueryData(accountQuery.queryKey);
    const instance = client.getQueryData(instanceQuery.queryKey);
    if (
      userId === undefined ||
      pins.size === 0 ||
      !session ||
      session.user_id !== userId ||
      account === undefined ||
      account.id !== userId ||
      instance === undefined
    ) {
      return;
    }
    await this.db.putSession({
      user_id: userId,
      session: { ...session, csrf_token: "" },
      account,
      instance,
      saved_at: this.now(),
    });
  }
}

/** The copy of a pinned project that the writer keeps: the project's document, which it holds, and what it last wrote of it. */
class Kept {
  private readonly keeper: OfflineKeeper;
  private readonly userId: string;
  private readonly projectId: string;
  private doc: ProjectDocument | undefined;
  private release: (() => void) | undefined;
  private unsubscribe: (() => void) | undefined;
  /** What the last write left, which the next is made on, or undefined for a write of the whole document. */
  private written: Written | undefined;
  /** Whether written has been taken from the copy that the document started from, once. */
  private based = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  /** Before when nothing is tried again, after a read or a write that failed. */
  private notBefore = 0;
  private stopped = false;
  private stepping = false;
  private again = false;

  constructor(keeper: OfflineKeeper, userId: string, projectId: string) {
    this.keeper = keeper;
    this.userId = userId;
    this.projectId = projectId;
  }

  /** Holds the project's document, and writes its copy once it can. */
  start(): void {
    if (this.stopped) {
      return;
    }
    const documents = this.keeper.options.documents;
    const doc = documents.get(this.projectId);
    const release = documents.acquire(doc);
    if (release === undefined) {
      // The page's documents dropped it meanwhile, as once the session was
      // of another account, which the keeper follows too.
      this.timer = setTimeout(() => this.start(), this.delay());
      return;
    }
    this.doc = doc;
    this.release = release;
    this.unsubscribe = doc.store.subscribe(() => this.schedule());
    this.schedule(0);
  }

  /** Lets the document go, and writes nothing more. */
  stop(): void {
    this.stopped = true;
    clearTimeout(this.timer);
    this.unsubscribe?.();
    this.release?.();
  }

  /** Tries again at once what failed, as once room has been made on the device. */
  retry(): void {
    this.notBefore = 0;
    this.schedule(0);
  }

  /** Tries to write the copy after delay milliseconds, or once a failure's wait is over. */
  schedule(delay = this.delay()): void {
    if (this.stopped) {
      return;
    }
    clearTimeout(this.timer);
    const wait = Math.max(delay, this.notBefore - Date.now());
    this.timer = setTimeout(() => void this.step(), wait);
  }

  private delay(): number {
    return this.keeper.options.writeDelay ?? 1_000;
  }

  private retryDelay(): number {
    return this.keeper.options.retryDelay ?? 30_000;
  }

  private async step(): Promise<void> {
    if (this.stepping) {
      this.again = true;
      return;
    }
    this.stepping = true;
    try {
      await this.stepOnce();
    } catch (error) {
      this.keeper.options.log.record("error", error);
    } finally {
      this.stepping = false;
      if (this.again) {
        this.again = false;
        this.schedule();
      }
    }
  }

  /**
   * Has the document read what it has yet to of the whole of the project,
   * and writes it once it holds it, and nothing that it has yet to read, but
   * while the browser has no connection, which the keeper tries again after
   * (start), and after a failure, which it tries again a while after.
   */
  private async stepOnce(): Promise<void> {
    const doc = this.doc;
    if (this.stopped || doc === undefined || !this.keeper.options.online()) {
      return;
    }
    if (doc.store.getState().gone) {
      await this.keeper.forget(this.projectId);
      return;
    }
    try {
      // Reads the project again where its first read failed, as the
      // document's ready would not.
      await doc.load("tree");
    } catch (error) {
      // A project that the account can no longer read, as one in the trash,
      // is kept no more; another failure is tried again a while after.
      if (notFoundBy(error)) {
        await this.keeper.forget(this.projectId);
        return;
      }
      this.failedNow();
      return;
    }
    if (!this.based) {
      this.based = true;
      this.written = doc.copy;
    }
    const view = doc.doc;
    const unread = unreadOf(doc, view);
    if (unread.parts.length > 0 || unread.components.length > 0) {
      try {
        await inTurns(
          [
            ...unread.parts.map((part) => () => doc.load(part)),
            ...unread.components.map((id) => () => doc.readComponent(id)),
          ],
          readsAtOnce,
        );
      } catch {
        // The document recorded what failed, as it records a page's reads.
        if (this.keeper.options.online()) {
          this.failedNow();
          await this.keeper.failed(this.userId, this.projectId, "error");
        }
        return;
      }
      this.schedule(0);
      return;
    }
    if (!doc.settled) {
      this.schedule();
      return;
    }
    if (
      this.written?.doc === view &&
      this.keeper.store.getState().pins.get(this.projectId)?.status === "ready"
    ) {
      return;
    }
    await this.write(doc);
  }

  /**
   * Waits a while before anything is tried again: a write that the
   * browser's quota refused, ten times as long, as it is likely to be
   * refused again until room is made (retry).
   */
  private failedNow(quota = false): void {
    this.notBefore = Date.now() + this.retryDelay() * (quota ? 10 : 1);
    this.schedule();
  }

  /** Writes the document's copy, as of its seq, with the project's role and its workspace, as the page reads them. */
  private async write(doc: ProjectDocument): Promise<void> {
    const view = doc.doc;
    const project = view.project;
    if (project === undefined) {
      return;
    }
    const client = this.keeper.options.queryClient;
    let record: Omit<CopyRecord, "version" | "bytes" | "parts">;
    try {
      const body = await client.ensureQueryData(projectQuery(this.projectId));
      const workspace = await client.ensureQueryData(
        workspaceQuery(body.workspace_id),
      );
      record = {
        user_id: this.userId,
        project_id: this.projectId,
        seq: view.seq,
        project: { ...project, role: body.role, seq: view.seq },
        workspace,
        saved_at: this.keeper.now(),
      };
    } catch {
      this.failedNow();
      return;
    }
    if (this.stopped || doc.doc !== view) {
      this.schedule();
      return;
    }
    let written: Written | undefined;
    try {
      written = await this.keeper.db.writeCopy(record, view, this.written);
    } catch (error) {
      const quota = overQuota(error);
      if (!quota) {
        this.keeper.options.log.record("error", error);
      }
      this.failedNow(quota);
      await this.keeper.failed(
        this.userId,
        this.projectId,
        quota ? "quota" : "error",
      );
      return;
    }
    if (written === undefined) {
      // The project was unpinned meanwhile, as in another tab.
      return;
    }
    this.written = written;
    this.notBefore = 0;
    await this.keeper.wrote(this.projectId);
  }
}
