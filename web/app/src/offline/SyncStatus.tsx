// The sync indicator of a project's pages (PRD FR-OFF-06, FR-OFF-03, plan
// note N98): one of four states, as the project's document holds its
// changes at each moment, which it shows as they change: synced, syncing,
// offline with how many changes the device keeps, or needing review, as
// the server refused changes made offline. It opens the sync panel, which
// lists the changes that the server has yet to answer, those that need
// review, each with when it was made, exports them as JSON for support
// (AC2), and, offline, lists what needs the server, which the pages offer
// no longer meanwhile, with why (FR-OFF-03 AC3).

import { ApiError } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import { useQueryClient } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useDocumentState, useProjectDocument } from "../document/react";
import type { PendingBatch } from "../document/store";
import { Button } from "../ui/Button";
import { DialogFrame } from "../ui/Dialog";
import { useKeepsChanges } from "./OfflinePages";
import { useOnline } from "./online";
import {
  exportedOf,
  exportFileName,
  pendingExport,
  type SyncState,
  saveJson,
  syncStateOf,
  useServerReachable,
} from "./pending";

/** Returns the sync indicator's state of the document of projectId, as it changes. */
export function useSyncState(projectId: string): SyncState {
  const doc = useProjectDocument(projectId);
  const online = useOnline();
  const sending = useDocumentState(doc, (s) => s.sending);
  const queued = useDocumentState(doc, (s) => s.queued);
  const review = useDocumentState(doc, (s) => s.review);
  const holding = useDocumentState(doc, (s) => s.holding);
  return useMemo(
    () => syncStateOf({ sending, queued, review, holding }, online),
    [sending, queued, review, holding, online],
  );
}

/** Returns what the indicator says of state. */
function useLabel(state: SyncState): string {
  const { t } = useTranslation();
  switch (state.state) {
    case "synced":
      return t("sync.state.synced");
    case "syncing":
      return t("sync.state.syncing", { count: state.pending });
    case "offline":
      return t("sync.state.offline", { count: state.pending });
    case "review":
      return t("sync.state.review", { count: state.review });
  }
}

/**
 * The sync indicator of the pages of projectId, which opens the sync panel,
 * whose words of the changes kept offline are for a user that edits says
 * can make them (useKeepsChanges).
 */
export function SyncIndicator({
  projectId,
  edits,
}: {
  projectId: string;
  edits: boolean;
}) {
  const state = useSyncState(projectId);
  const label = useLabel(state);
  // What the live region says, which a screen reader reads each change of:
  // each state but syncing, which comes and goes with every change saved
  // online, so that the user hears that the changes are saved, kept on the
  // device, or need review, rather than of each save as it is made.
  const [announced, setAnnounced] = useState(label);
  if (state.state !== "syncing" && announced !== label) {
    setAnnounced(label);
  }
  const mark =
    state.state === "review"
      ? "border-danger text-danger"
      : state.state === "offline"
        ? "border-border text-fg"
        : "border-border text-fg-muted";
  return (
    <Dialog.Root>
      <div className="flex">
        <Dialog.Trigger asChild>
          <Button className={mark} aria-haspopup="dialog">
            {label}
          </Button>
        </Dialog.Trigger>
        <span role="status" className="sr-only">
          {announced}
        </span>
      </div>
      <SyncPanel
        projectId={projectId}
        state={state}
        label={label}
        edits={edits}
      />
    </Dialog.Root>
  );
}

/** What each change of the panel says it is. */
function useBatchState(batch: PendingBatch): string {
  const { t } = useTranslation();
  // What the server said as it refused the change last, if it did, in the
  // words of the app's catalog where the app knows its code, as a form
  // says it (refusalOf), or else its status, as of a proxy's page.
  const refusal = batch.refusal;
  const reason =
    (refusal?.error === undefined
      ? undefined
      : refusalOf(
          new ApiError(
            new Response(null, { status: refusal.status }),
            refusal.error,
          ),
          t,
        ).message) ?? t("sync.batch.status", { status: refusal?.status ?? 0 });
  switch (batch.state) {
    case "sending":
      return t("sync.batch.sending");
    case "queued":
      // One that the server refused for when or by whom it was sent, as for
      // a role that no longer allows it, waits to be tried again, with why.
      if (batch.refusal !== undefined) {
        return t("sync.batch.queued_refused", { reason });
      }
      return batch.sent ? t("sync.batch.queued_sent") : t("sync.batch.queued");
    case "review":
      return t("sync.batch.review", { reason });
  }
}

/** A change that the panel lists. */
function BatchItem({ batch }: { batch: PendingBatch }) {
  const { t, i18n } = useTranslation();
  const state = useBatchState(batch);
  return (
    <li className="space-y-1 rounded-md border border-border px-3 py-2">
      <p>
        {t("sync.batch.made", {
          time: formatDateTime(i18n.language, new Date(batch.time)),
        })}{" "}
        {state}
      </p>
      <p className="text-fg-muted">
        {t("sync.batch.commands", { count: batch.commands.length })}{" "}
        {[...new Set(batch.commands.map((c) => c.type))].map((type, i) => (
          <span key={type}>
            {i > 0 && ", "}
            <code>{type}</code>
          </span>
        ))}
      </p>
    </li>
  );
}

/** What needs the server, which the pages do not offer while it cannot be reached (FR-OFF-03 AC3). */
const unavailable = [
  "sync.unavailable.publish",
  "sync.unavailable.access",
  "sync.unavailable.exports",
  "sync.unavailable.uploads",
  "sync.unavailable.clipboard",
  "sync.unavailable.comments",
  "sync.unavailable.saved_states",
  "sync.unavailable.search",
  "sync.unavailable.analysis",
  "sync.unavailable.trash",
  "sync.unavailable.settings",
] as const;

/**
 * What of unavailable the project's pages that read the server alone are,
 * which they do not offer while the browser has no connection (offlinePages),
 * and offer while it has one, though the page may not reach the server, as
 * while a try waits after no answer: the others follow whether it does
 * (useServerReachable).
 */
const pagesOffline: ReadonlySet<(typeof unavailable)[number]> = new Set([
  "sync.unavailable.publish",
  "sync.unavailable.access",
  "sync.unavailable.exports",
  "sync.unavailable.search",
  "sync.unavailable.analysis",
  "sync.unavailable.trash",
  "sync.unavailable.settings",
]);

/**
 * The list of what needs the server, with why, which the pages do not offer
 * now: with those that read the server alone where connected says that the
 * browser has no connection (pagesOffline).
 */
function Unavailable({ connected }: { connected: boolean }) {
  const { t } = useTranslation();
  // Each key as a literal, which extraction reads (i18n.ts's TestCatalogs).
  const names: Record<(typeof unavailable)[number], string> = {
    "sync.unavailable.publish": t("sync.unavailable.publish"),
    "sync.unavailable.access": t("sync.unavailable.access"),
    "sync.unavailable.exports": t("sync.unavailable.exports"),
    "sync.unavailable.uploads": t("sync.unavailable.uploads"),
    "sync.unavailable.clipboard": t("sync.unavailable.clipboard"),
    "sync.unavailable.comments": t("sync.unavailable.comments"),
    "sync.unavailable.saved_states": t("sync.unavailable.saved_states"),
    "sync.unavailable.search": t("sync.unavailable.search"),
    "sync.unavailable.analysis": t("sync.unavailable.analysis"),
    "sync.unavailable.trash": t("sync.unavailable.trash"),
    "sync.unavailable.settings": t("sync.unavailable.settings"),
  };
  return (
    <section aria-labelledby="sync-unavailable" className="space-y-2">
      <h3 id="sync-unavailable" className="font-semibold">
        {t("sync.unavailable.title")}
      </h3>
      <p>{t("sync.unavailable.intro")}</p>
      <ul className="list-disc space-y-1 pl-6">
        {unavailable
          .filter((key) => !connected || !pagesOffline.has(key))
          .map((key) => (
            <li key={key}>{names[key]}</li>
          ))}
      </ul>
    </section>
  );
}

/** The sync panel of the pages of projectId, as of state, which the indicator says as label, for a user that edits says can change the project. */
function SyncPanel({
  projectId,
  state,
  label,
  edits,
}: {
  projectId: string;
  state: SyncState;
  label: string;
  edits: boolean;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  // Offline, the panel says that the changes are kept on the device only
  // where they are, as they are not over plain HTTP, which keeps no queue,
  // and a change made meanwhile is refused (PRD FR-OFF-03), and neither to
  // a user who cannot change the project, as a viewer, or of one archived,
  // whose changes kept before it lists all the same.
  const keeps = useKeepsChanges(edits);
  const queryClient = useQueryClient();
  // What the panel lists, read anew as it changes (queueVersion), and as
  // what the page sends changes.
  const version = useDocumentState(doc, (s) => s.queueVersion);
  const sending = useDocumentState(doc, (s) => s.sending);
  // biome-ignore lint/correctness/useExhaustiveDependencies: version and sending tell that the document's batches changed
  const batches = useMemo(() => doc.pendingBatches(), [doc, version, sending]);
  const review = batches.filter((b) => b.state === "review");
  const pending = batches.filter((b) => b.state !== "review");
  // What the export holds: not a change that waits for a step of the
  // history, which has no IDs yet (exportedOf), and nothing to export where
  // only such changes are listed.
  const exported = useMemo(
    () => exportedOf(projectId, batches),
    [projectId, batches],
  );
  // What needs the server is listed while the page cannot reach it, whether
  // or not a change needs review meanwhile, as far as the pages do not
  // offer it then (Unavailable).
  const offline = !useServerReachable(doc);
  const connected = useOnline();
  const exportPending = () => {
    const now = new Date().toISOString();
    const session = queryClient.getQueryData(sessionQuery.queryKey);
    saveJson(
      exportFileName(now),
      pendingExport(session?.user_id ?? "", exported, now),
    );
  };
  const about =
    state.state === "synced"
      ? t("sync.panel.synced")
      : state.state === "syncing"
        ? t("sync.panel.syncing")
        : state.state === "offline"
          ? !edits
            ? t("sync.panel.offline_read_only")
            : keeps
              ? t("sync.panel.offline")
              : t("sync.panel.offline_unkept")
          : t("sync.panel.review");
  return (
    <DialogFrame title={t("sync.panel.title")} description={label}>
      <p>{about}</p>
      {review.length > 0 && (
        <section aria-labelledby="sync-review" className="space-y-2">
          <h3 id="sync-review" className="font-semibold">
            {t("sync.panel.review_title")}
          </h3>
          <ul className="space-y-2">
            {review.map((batch, i) => (
              <BatchItem key={batch.id ?? i} batch={batch} />
            ))}
          </ul>
        </section>
      )}
      <section aria-labelledby="sync-pending" className="space-y-2">
        <h3 id="sync-pending" className="font-semibold">
          {t("sync.panel.pending_title")}
        </h3>
        {pending.length === 0 ? (
          <p className="text-fg-muted">{t("sync.panel.none")}</p>
        ) : (
          <ul className="space-y-2">
            {pending.map((batch, i) => (
              <BatchItem key={batch.id ?? i} batch={batch} />
            ))}
          </ul>
        )}
      </section>
      {offline && <Unavailable connected={connected} />}
      <div className="flex flex-wrap justify-end gap-2">
        <Button disabled={exported.length === 0} onClick={exportPending}>
          {t("sync.panel.export")}
        </Button>
        <Dialog.Close asChild>
          <Button variant="primary">{t("sync.panel.close")}</Button>
        </Dialog.Close>
      </div>
    </DialogFrame>
  );
}
