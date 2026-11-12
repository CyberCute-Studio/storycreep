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
import type { Realtime } from "./realtime";
import { ProjectDocument } from "./store";
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
}

interface Held {
  doc: ProjectDocument;
  shown: number;
  timer?: ReturnType<typeof setTimeout>;
}

/** The page's documents. */
export class Documents {
  /** The page's clipboard, of the nodes that the account copied last, on any of its projects' boards. */
  readonly clipboard: ClipboardStore = createClipboard();
  private readonly options: DocumentsOptions;
  private readonly held = new Map<string, Held>();
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
   * is back, asks anew.
   */
  get(projectId: string): ProjectDocument {
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
        doc: new ProjectDocument(projectId, this.options),
        shown: 0,
      };
      this.held.set(projectId, held);
      this.unshown(projectId, held);
    }
    return held.doc;
  }

  /**
   * Keeps doc, the document of its project that get returned, while a page
   * of it is shown, until what it returns is called. One that the page's
   * documents have dropped since get returned it, as with every other once
   * the session is of another account, is not kept, and it returns
   * undefined, so that the page asks for the one that they have now, rather
   * than show one while another is kept.
   */
  acquire(doc: ProjectDocument): (() => void) | undefined {
    const projectId = doc.projectId;
    const held = this.held.get(projectId);
    if (held?.doc !== doc) {
      return undefined;
    }
    held.shown++;
    clearTimeout(held.timer);
    let released = false;
    return () => {
      if (released || this.held.get(projectId) !== held) {
        return;
      }
      released = true;
      held.shown--;
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
      this.held.delete(projectId);
      held.doc.dispose();
    }, keptUnshown);
  }

  /** Drops the document of projectId at once, as once the project could not be read, so that a page asks anew. */
  forget(projectId: string): void {
    const held = this.held.get(projectId);
    if (held !== undefined) {
      clearTimeout(held.timer);
      this.held.delete(projectId);
      held.doc.dispose();
    }
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
