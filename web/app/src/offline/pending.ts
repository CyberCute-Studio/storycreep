// The page's changes that the server has yet to answer, as the user sees
// them (PRD FR-OFF-06, plan note N98): whether the page reaches the server
// now, the four states of the sync indicator, and the JSON file of the
// changes that the sync panel and signing out export, for support.

import type { CommandBody } from "@storycreep/shared/api";
import type { Refusal } from "../document/queue";
import { useDocumentState } from "../document/react";
import type {
  DocumentState,
  PendingBatch,
  ProjectDocument,
} from "../document/store";
import { useOnline } from "./online";

/**
 * Returns whether the page reaches the server now, as far as it knows: the
 * browser says that it has a connection, and the document of the project
 * does not keep its changes for want of an answer, nor of the page's own
 * session (DocumentState.holding), so that what needs the server, as a
 * copy or an upload, is offered, rather than left to fail.
 */
export function useServerReachable(doc: ProjectDocument): boolean {
  const online = useOnline();
  const holding = useDocumentState(doc, (state) => state.holding);
  return online && !holding;
}

/**
 * The state of the sync indicator: synced, every change saved on the
 * server; syncing, changes on their way; offline, as the page cannot reach
 * the server, with how many changes it keeps meanwhile; review, as the
 * server refused changes made offline, which wait for the user's decision.
 */
export type SyncState =
  | { state: "synced" }
  | { state: "syncing"; pending: number }
  | { state: "offline"; pending: number }
  | { state: "review"; pending: number; review: number };

/** Returns the sync indicator's state of a document of state, while the browser is online or not. */
export function syncStateOf(
  state: Pick<DocumentState, "sending" | "queued" | "review" | "holding">,
  online: boolean,
): SyncState {
  const pending = state.sending;
  if (state.review > 0) {
    return { state: "review", pending, review: state.review };
  }
  // Offline, the changes that the device keeps, once it has written them,
  // and not those on their way meanwhile, as a comment's, which it does
  // not keep, nor any over plain HTTP, where it keeps no queue.
  if (!online || state.holding) {
    return { state: "offline", pending: state.queued };
  }
  if (pending > 0) {
    return { state: "syncing", pending };
  }
  return { state: "synced" };
}

/** A change as the JSON file of pending changes holds it. */
export interface ExportedBatch {
  project_id: string;
  id: string;
  made_at: string;
  state: PendingBatch["state"];
  /** Whether it was sent and no answer came, so that it may have applied. */
  sent: boolean;
  commands: readonly CommandBody[];
  refusal?: Refusal;
}

/** The JSON file of the account's pending changes, for support (FR-OFF-06 AC2). */
export interface PendingExport {
  format: "storycreep-pending-changes";
  version: 1;
  exported_at: string;
  user_id: string;
  batches: ExportedBatch[];
}

/** Returns the JSON file of userId's pending changes of batches, as of now. */
export function pendingExport(
  userId: string,
  batches: readonly ExportedBatch[],
  now: string,
): PendingExport {
  return {
    format: "storycreep-pending-changes",
    version: 1,
    exported_at: now,
    user_id: userId,
    batches: [...batches],
  };
}

/** Returns the batches of projectId that a document lists, as the JSON file holds them. */
export function exportedOf(
  projectId: string,
  batches: readonly PendingBatch[],
): ExportedBatch[] {
  return batches.map((batch) => ({
    project_id: projectId,
    id: batch.id,
    made_at: batch.time,
    state: batch.state,
    sent: batch.sent,
    commands: batch.commands,
    ...(batch.refusal === undefined ? {} : { refusal: batch.refusal }),
  }));
}

/** Returns the name of the file of the pending changes exported at now. */
export function exportFileName(now: string): string {
  return `storycreep-pending-changes-${now.slice(0, 19).replaceAll(":", "-")}.json`;
}

/** Hands the browser the JSON file of exported to save, named name, as a download that the page makes itself. */
export function saveJson(name: string, exported: PendingExport): void {
  const blob = new Blob([`${JSON.stringify(exported, null, 2)}\n`], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
  // A browser reads the object's URL as the download starts, which is after
  // the click returns, and WebKit may still be reading it a while after,
  // whose download fails once it is revoked (saveDownload, of a release).
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
