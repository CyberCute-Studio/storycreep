// The page's clipboard (PRD FR-EDIT-07, plan note N71): the nodes that the
// user copied or cut last, as the server's copy answered them, which a paste
// onto a board of any project of the workspace that they were copied in
// pastes, on any board's canvas of the tab, until the page copies others,
// or is signed in to another account, or signed out (Documents), or the tab
// is reloaded, as the page keeps it in its memory alone. It is not put on
// the system's clipboard, which the server's answer would come too late for
// in a copy event's handler, and which the Clipboard API writes only in a
// secure context, as a self-hosted instance served over plain HTTP is not
// (NFR-SEC-15).

import type { Clipboard } from "@storycreep/shared/api";
import { createStore, type StoreApi } from "zustand/vanilla";

/** What the page's clipboard holds. */
export interface Copied {
  /** The account that copied it. */
  userId: string;
  /** The workspace of the project that it was copied from, in whose projects it is pasted. */
  workspaceId: string;
  clipboard: Clipboard;
  /** Counts the page's copies, so that each is told anew, though of as many nodes as the one before it. */
  serial: number;
}

/** The page's clipboard: what it holds, or undefined while it holds nothing. */
export type ClipboardStore = StoreApi<Copied | undefined>;

/** Returns a clipboard that holds nothing. */
export function createClipboard(): ClipboardStore {
  return createStore<Copied | undefined>(() => undefined);
}

let serial = 0;

/** Has store hold copied, in place of what it held. */
export function holdCopy(
  store: ClipboardStore,
  copied: Omit<Copied, "serial">,
): void {
  serial++;
  store.setState({ ...copied, serial }, true);
}
