// The page's documents of projects (store.ts), one for each project that it
// shows, which a project's pages share, and its routes' loaders read before
// the pages show: a document is made once a page asks for it, follows its
// project while a page of it is shown (acquire), and is dropped a while
// after none is, and at once, with every other, once the page is signed in
// to another account or signed out, when the real-time channel's socket,
// which went with the session before, is opened anew, and the page's
// clipboard (clipboard.ts), which holds the account's nodes, is emptied with
// them. Signing out also
// clears the undo histories that the tab's storage keeps (PRD FR-EDIT-08:
// the history is the user's), which another account's, kept under its own
// key, never reads, as does a page that finds itself signed out once it
// loads, as after the session ended while the tab reloaded.

import type { QueryClient } from "@tanstack/react-query";
import { sessionQuery } from "../api";
import type { ErrorLog } from "../errors/log";
import { type ClipboardStore, createClipboard } from "./clipboard";
import type { QueueStore } from "./queue";
import type { Realtime } from "./realtime";
import { whenSent } from "./sent";
import { type CopySource, ProjectDocument } from "./store";
import { clearHistories } from "./undo";

/** How long, in milliseconds, a document that no page shows is kept, as for a page of the project that the user goes back to. */
export const keptUnshown = 30_000;

export interface DocumentsOptions {
  queryClient: QueryClient;
  realtime?: Realtime;
  log: ErrorLog;
  /** Has the page ask for its session, once a document's read tells that it may have ended (DocumentOptions.checkSession). */
  checkSession?: () => void;
  /** The tab's storage, which keeps the undo histories over a reload. */
  storage?: Storage;
  /** The device's copies of the projects that the account keeps offline, which a document starts from (DocumentOptions.copies). */
  copies?: CopySource;
  /** Reports whether the browser says that it has a connection (DocumentOptions.online). */
  online?: () => boolean;
  /** Calls listener as the browser's connection comes and goes (DocumentOptions.onConnection). */
  onConnection?: (listener: (online: boolean) => void) => () => void;
  /** The device's queue of the batches that the server has yet to answer (DocumentOptions.queue). */
  queue?: QueueStore;
}

interface Held {
  doc: ProjectDocument;
  /** How many hold it: the pages that show it, and the keeper of the copies (acquire). */
  shown: number;
  /** How many of those are pages, while which the document takes the batches of other documents of the device's queue (ProjectDocument.takeQueue). */
  pages: number;
  timer?: ReturnType<typeof setTimeout>;
  /** The timer after which a document that a page asked for, and none shows, takes none of those (asked). */
  asked?: ReturnType<typeof setTimeout>;
}

/** The page's documents. */
export class Documents {
  /** The page's clipboard, of the nodes that the account copied last, on any of its projects' boards. */
  readonly clipboard: ClipboardStore = createClipboard();
  private readonly options: DocumentsOptions;
  private readonly held = new Map<string, Held>();
  /** The documents dropped that have yet to let go of the device's queue, which may write to it still (drop). */
  private readonly dropped = new Set<ProjectDocument>();
  private readonly stop: () => void;

  constructor(options: DocumentsOptions) {
    this.options = options;
    const session = options.queryClient.getQueryData(sessionQuery.queryKey);
    let userId = session?.user_id;
    // Whether the page is known to be signed out, as a page that loads once
    // the session has ended, whose tab's storage may keep the histories of
    // the session before, which it never read.
    let signedOut = session === null;
    if (signedOut) {
      clearHistories(options.storage);
    }
    // The documents, and the histories, are the account's: once the page's
    // session is of another account, or of none, they go.
    this.stop = options.queryClient.getQueryCache().subscribe((event) => {
      if (event.query.queryKey[0] !== sessionQuery.queryKey[0]) {
        return;
      }
      const now = options.queryClient.getQueryData(sessionQuery.queryKey);
      if (now === undefined) {
        return;
      }
      const ended = now === null && !signedOut;
      signedOut = now === null;
      if (now?.user_id !== userId) {
        userId = now?.user_id;
        this.clear();
        this.clipboard.setState(undefined, true);
        // The socket went with the cookie of the session before, whose
        // subscriptions the next documents' would otherwise be for a while.
        options.realtime?.reconnect();
      }
      // The histories are cleared once the documents have gone, each of
      // which writes what its history recorded before as it goes
      // (History.close).
      if (ended) {
        clearHistories(options.storage);
      }
    });
  }

  /**
   * Returns the document of projectId, made now if the page has none, or if
   * the one that it has found the project gone, as to the trash, and no page
   * shows it, so that a page that the user opens again, as once the project
   * is back, asks anew. Where kept says so, as of the keeper of the copies
   * (offline/keeper.ts), it is asked for by no page, and a document made for
   * it takes none of the batches of other documents that the device's queue
   * keeps, as of a tab that closed, which it would hold where no page shows
   * them, until a page asks for it (ProjectDocument.takeQueue): one that a
   * page asks for takes them while a page shows it, and, before one does,
   * as while a route's loader reads it, for a while (asked).
   */
  get(projectId: string, kept = false): ProjectDocument {
    let held = this.held.get(projectId);
    if (
      held !== undefined &&
      held.shown === 0 &&
      held.doc.store.getState().gone
    ) {
      this.forget(projectId);
      held = undefined;
    }
    if (held === undefined) {
      held = {
        doc: new ProjectDocument(
          projectId,
          kept ? { ...this.options, takesQueue: false } : this.options,
        ),
        shown: 0,
        pages: 0,
      };
      this.held.set(projectId, held);
      this.unshown(projectId, held);
    }
    if (!kept) {
      held.doc.takeQueue(true);
      if (held.pages === 0) {
        this.asked(held);
      }
    }
    return held.doc;
  }

  /**
   * Has the document of held, which a page asked for, and none shows, take
   * none of the batches of other documents a while after, unless a page
   * shows it by then, as of a page whose loader read it, which the user did
   * not open, while the keeper of the copies, or the batches that it holds
   * (busy), keep it.
   */
  private asked(held: Held): void {
    clearTimeout(held.asked);
    held.asked = setTimeout(() => {
      if (held.pages === 0) {
        held.doc.takeQueue(false);
      }
    }, keptUnshown);
  }

  /**
   * Keeps doc, the document of its project that get returned, while a page
   * of it is shown, or the keeper of the copies holds it, as kept says,
   * until what it returns is called. One that the page's documents have
   * dropped since get returned it, as with every other once the session is
   * of another account, is not kept, and it returns undefined, so that the
   * page asks for the one that they have now, rather than show one while
   * another is kept.
   */
  acquire(doc: ProjectDocument, kept = false): (() => void) | undefined {
    const projectId = doc.projectId;
    const held = this.held.get(projectId);
    if (held?.doc !== doc) {
      return undefined;
    }
    held.shown++;
    clearTimeout(held.timer);
    if (!kept) {
      held.pages++;
      clearTimeout(held.asked);
      doc.takeQueue(true);
    }
    let released = false;
    return () => {
      if (released || this.held.get(projectId) !== held) {
        return;
      }
      released = true;
      held.shown--;
      if (!kept) {
        held.pages--;
        if (held.pages === 0) {
          held.doc.takeQueue(false);
        }
      }
      if (held.shown === 0) {
        this.unshown(projectId, held);
      }
    };
  }

  /**
   * Drops the document of projectId once no page has shown it for a while,
   * and it is not busy (ProjectDocument.busy): reading what a route's loader
   * may be waiting for, before the page that it is for shows, or sending the
   * page's batches, whose answers join the history that it keeps, however
   * long that takes, as on a slow connection.
   */
  private unshown(projectId: string, held: Held): void {
    clearTimeout(held.timer);
    held.timer = setTimeout(() => {
      if (held.shown !== 0 || this.held.get(projectId) !== held) {
        return;
      }
      if (held.doc.busy) {
        this.unshown(projectId, held);
        return;
      }
      this.drop(projectId, held);
    }, keptUnshown);
  }

  /** Drops the document of projectId at once, as once the project could not be read, so that a page asks anew. */
  forget(projectId: string): void {
    const held = this.held.get(projectId);
    if (held !== undefined) {
      this.drop(projectId, held);
    }
  }

  /**
   * Drops held, the document of projectId, which writes what it was keeping
   * of the device's queue still, until it lets go of it, as signing out
   * waits for (whenWritten).
   */
  private drop(projectId: string, held: Held): void {
    clearTimeout(held.timer);
    clearTimeout(held.asked);
    this.held.delete(projectId);
    held.doc.dispose();
    const doc = held.doc;
    this.dropped.add(doc);
    void doc.lettingGo().then(() => this.dropped.delete(doc));
  }

  /**
   * Resolves once no document sends any of the page's batches, or once wait
   * milliseconds have passed, as before the page reloads for a new version
   * of the app (PRD FR-OFF-01), so that a batch that the user made just
   * before is not cut off on its way.
   */
  async whenSent(wait: number): Promise<void> {
    const signal = new AbortController().signal;
    await Promise.all(
      [...this.held.values()].map((held) => whenSent(held.doc, signal, wait)),
    );
  }

  /** Whether the page keeps the changes that it cannot send in the device's queue, as it does not over plain HTTP, nor in a browser without Web Locks. */
  get keepsQueue(): boolean {
    return this.options.queue !== undefined;
  }

  /**
   * Takes the changes of userId's of only, by their projects and IDs, or,
   * where it is not given, every one, that the device's queue keeps off the
   * device, as signing out does once the user decided (QueueStore.discard),
   * resolving once it has; undefined where the page keeps no queue.
   */
  discardQueued(
    userId: string,
    only?: readonly { project_id: string; id: string }[],
  ): Promise<void> | undefined {
    return this.options.queue?.discard(userId, only);
  }

  /**
   * Resolves once every document's writes of the device's queue begun so
   * far are done, kept or refused (ProjectDocument.queueWritten), those of
   * the documents dropped that have yet to let go of the queue too, as of a
   * project found gone, or once wait milliseconds have passed, as of a
   * database that another tab blocks, so that signing out, which reads the
   * device after, names a change that a document was still keeping as the
   * user clicked.
   */
  async whenWritten(wait: number): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([
      Promise.all(
        [
          ...[...this.held.values()].map((held) => held.doc),
          ...this.dropped,
        ].map((doc) => doc.queueWritten()),
      ),
      new Promise<void>((resolve) => {
        timer = setTimeout(resolve, wait);
      }),
    ]);
    clearTimeout(timer);
  }

  /** Drops every document. */
  clear(): void {
    for (const projectId of [...this.held.keys()]) {
      this.forget(projectId);
    }
  }

  /** Drops every document, and stops following the session, as once the page goes. */
  close(): void {
    this.stop();
    this.clear();
  }
}
