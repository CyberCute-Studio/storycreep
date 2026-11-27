// The sync indicator of a project's pages (PRD FR-OFF-06, FR-OFF-03, plan
// note N98): one of four states, as the project's document holds its
// changes at each moment, which it shows as they change: synced, syncing,
// offline with how many changes the device keeps, or needing review, as
// the server refused changes made offline. It opens the sync panel, which
// lists the changes that the server has yet to answer, those that need
// review, each with when it was made, exports them as JSON for support
// (AC2), and, offline, lists what needs the server, which the pages offer
// no longer meanwhile, with why (FR-OFF-03 AC3).

import { formatDateTime } from "@storycreep/shared/i18n";
import { useQueryClient } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { useDocumentState, useProjectDocument } from "../document/react";
import type { PendingBatch } from "../document/store";
import { Button } from "../ui/Button";
import { DialogFrame } from "../ui/Dialog";
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

/** The sync indicator of the pages of projectId, which opens the sync panel. */
export function SyncIndicator({ projectId }: { projectId: string }) {
  const state = useSyncState(projectId);
  const label = useLabel(state);
  const mark =
    state.state === "review"
      ? "border-danger text-danger"
      : state.state === "offline"
        ? "border-border text-fg"
        : "border-border text-fg-muted";
  return (
    <Dialog.Root>
      {/* A live region, which a screen reader reads each change of. */}
      <div role="status" className="flex">
        <Dialog.Trigger asChild>
          <Button className={mark} aria-haspopup="dialog">
            {label}
          </Button>
        </Dialog.Trigger>
      </div>
      <SyncPanel projectId={projectId} state={state} label={label} />
    </Dialog.Root>
  );
}

/** What each change of the panel says it is. */
function useBatchState(batch: PendingBatch): string {
  const { t } = useTranslation();
  // What the server said as it refused the change last, if it did.
  const reason =
    batch.refusal?.error?.message ??
    t("sync.batch.status", { status: batch.refusal?.status ?? 0 });
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

/** The list of what needs the server, with why. */
function Unavailable() {
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
        {unavailable.map((key) => (
          <li key={key}>{names[key]}</li>
        ))}
      </ul>
    </section>
  );
}

/** The sync panel of the pages of projectId, as of state, which the indicator says as label. */
function SyncPanel({
  projectId,
  state,
  label,
}: {
  projectId: string;
  state: SyncState;
  label: string;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const queryClient = useQueryClient();
  // What the panel lists, read anew as it changes (queueVersion), and as
  // what the page sends changes.
  const version = useDocumentState(doc, (s) => s.queueVersion);
  const sending = useDocumentState(doc, (s) => s.sending);
  // biome-ignore lint/correctness/useExhaustiveDependencies: version and sending tell that the document's batches changed
  const batches = useMemo(() => doc.pendingBatches(), [doc, version, sending]);
  const review = batches.filter((b) => b.state === "review");
  const pending = batches.filter((b) => b.state !== "review");
  // What needs the server is listed while the page cannot reach it, whether
  // or not a change needs review meanwhile.
  const offline = !useServerReachable(doc);
  const exportPending = () => {
    const now = new Date().toISOString();
    const session = queryClient.getQueryData(sessionQuery.queryKey);
    saveJson(
      exportFileName(now),
      pendingExport(
        session?.user_id ?? "",
        exportedOf(projectId, batches),
        now,
      ),
    );
  };
  const about =
    state.state === "synced"
      ? t("sync.panel.synced")
      : state.state === "syncing"
        ? t("sync.panel.syncing")
        : state.state === "offline"
          ? t("sync.panel.offline")
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
            {review.map((batch) => (
              <BatchItem key={batch.id} batch={batch} />
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
              <BatchItem key={batch.id === "" ? i : batch.id} batch={batch} />
            ))}
          </ul>
        )}
      </section>
      {offline && <Unavailable />}
      <div className="flex flex-wrap justify-end gap-2">
        <Button disabled={batches.length === 0} onClick={exportPending}>
          {t("sync.panel.export")}
        </Button>
        <Dialog.Close asChild>
          <Button variant="primary">{t("sync.panel.close")}</Button>
        </Dialog.Close>
      </div>
    </DialogFrame>
  );
}
