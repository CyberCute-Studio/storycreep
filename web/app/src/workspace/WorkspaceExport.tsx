// The workspace export (PRD FR-DATA-02, plan note N93), a section of the
// workspace's settings that its owners alone see: a zip of the whole
// workspace, which a job builds, and which the section follows until it ends,
// and then offers to download, until it expires. An export is the owner's
// own, so the section shows the one on its way, or the latest, as it opens.

import { ApiError, type WorkspaceExportBody } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { nextPoll, refusedByServer } from "../releases/queries";
import { Button, buttonClass } from "../ui/Button";
import { Alert, RefusalAlert, Status } from "../ui/Page";
import { formatBytes } from "./Usage";

/** The key of every read of workspaceId's exports. */
function exportsKey(workspaceId: string) {
  return ["workspaces", workspaceId, "exports"] as const;
}

/** Whether the export has yet to end. */
function onItsWay(e: WorkspaceExportBody | undefined): boolean {
  return e?.status === "queued" || e?.status === "building";
}

/** The owner's newest export of the workspace, which the section shows first. */
function newestExportQuery(workspaceId: string) {
  return queryOptions({
    queryKey: [...exportsKey(workspaceId), "newest"] as const,
    // The section shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "listWorkspaceExports",
          { path: { workspace_id: workspaceId }, query: { limit: 1 } },
          { signal },
        )
      ).body.items[0] ?? null,
    staleTime: 0,
  });
}

/**
 * The export id, read again until it ends or the server refuses it (nextPoll),
 * including when no read of it has succeeded yet, as the section waits on it.
 */
function exportQuery(workspaceId: string, id: string) {
  return queryOptions({
    queryKey: [...exportsKey(workspaceId), id] as const,
    // The section shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "getWorkspaceExport",
          { path: { workspace_id: workspaceId, export_id: id } },
          { signal },
        )
      ).body,
    refetchInterval: (query) =>
      query.state.data === undefined || onItsWay(query.state.data)
        ? nextPoll(query.state.error)
        : false,
  });
}

/** The ID of the export on its way that a refusal names, if it is that refusal. */
function inProgressId(error: unknown): string | undefined {
  if (
    !(error instanceof ApiError) ||
    error.error?.code !== "export_in_progress"
  ) {
    return undefined;
  }
  const id = error.error.details?.export_id;
  return typeof id === "string" ? id : undefined;
}

/** The section of the workspace's settings that exports it, for its owners. */
export function WorkspaceExport({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [followed, setFollowed] = useState<string>();
  const newest = useQuery(newestExportQuery(workspaceId));
  const id = followed ?? newest.data?.id;
  const current = useQuery({
    ...exportQuery(workspaceId, id ?? ""),
    enabled: id !== undefined,
  });
  // A refusal other than the rate limit's says that the export is no longer
  // the owner's to read, so what was last read of it no longer stands.
  const gone = refusedByServer(current.error) && current.error.status !== 429;
  const shown = gone
    ? undefined
    : (current.data ??
      (newest.data?.id === id ? (newest.data ?? undefined) : undefined));

  const start = useMutation({
    mutationFn: async () =>
      (
        await api.call("createWorkspaceExport", {
          path: { workspace_id: workspaceId },
          headers: csrfHeaders(session),
        })
      ).body,
    onSuccess: (e) => {
      client.setQueryData(exportQuery(workspaceId, e.id).queryKey, e);
      client.setQueryData(newestExportQuery(workspaceId).queryKey, e);
      setFollowed(e.id);
    },
    onError: (error) => {
      // The one on its way is what the owner is after, which the section
      // then shows, rather than a refusal of another.
      const existing = inProgressId(error);
      if (existing !== undefined) {
        setFollowed(existing);
        void client.invalidateQueries({ queryKey: exportsKey(workspaceId) });
      }
    },
  });
  // A refusal of the read of the newest export, as the rate limit's, is shown
  // too, as the section would otherwise show none without saying why.
  const refusal =
    (inProgressId(start.error) === undefined
      ? refusalOf(start.error, t)
      : undefined) ??
    (gone ? refusalOf(current.error, t) : undefined) ??
    (refusedByServer(newest.error) ? refusalOf(newest.error, t) : undefined);
  return (
    <section className="space-y-3" aria-labelledby="workspace-export">
      <h2 id="workspace-export" className="text-lg font-semibold">
        {t("workspace_export.title")}
      </h2>
      <p className="text-fg-muted">{t("workspace_export.intro")}</p>
      <p className="rounded-md border border-border bg-surface px-4 py-3">
        {t("workspace_export.personal_data")}
      </p>
      <RefusalAlert refusal={refusal} />
      {shown !== undefined && (
        <ExportResult
          workspaceId={workspaceId}
          item={shown}
          onDownload={() => void current.refetch()}
        />
      )}
      <Button
        variant="primary"
        disabled={start.isPending || onItsWay(shown)}
        onClick={() => start.mutate()}
      >
        {t("workspace_export.submit")}
      </Button>
    </section>
  );
}

/** Where the export is, and once it is ready its size and its download. */
function ExportResult({
  workspaceId,
  item,
  onDownload,
}: {
  workspaceId: string;
  item: WorkspaceExportBody;
  /** Reads the export again, whose refusal the section then shows. */
  onDownload: () => void;
}) {
  const { t, i18n } = useTranslation();
  return (
    <div className="space-y-2 border-t border-border pt-3">
      <h3 className="font-semibold">{t("workspace_export.latest")}</h3>
      {item.status === "failed" ? (
        <Alert message={t("workspace_export.failed")} />
      ) : (
        <Status
          message={
            item.status === "ready"
              ? t("workspace_export.ready", {
                  count: item.project_count ?? 0,
                  size: formatBytes(i18n.language, item.size_bytes ?? 0),
                  until: formatDateTime(
                    i18n.language,
                    new Date(item.expires_at),
                  ),
                })
              : item.status === "building"
                ? t("workspace_export.building")
                : t("workspace_export.queued")
          }
        />
      )}
      {item.status === "ready" && (
        // A link rather than a read into memory, as an archive may have
        // gigabytes, which the browser then saves as it arrives. The browser
        // reports a refusal as a failed download, which says nothing of why,
        // so the click also asks the server whether the export still stands.
        <a
          href={`/api/v1/workspaces/${workspaceId}/exports/${item.id}/download`}
          download={item.file_name}
          className={buttonClass()}
          onClick={onDownload}
        >
          {t("workspace_export.download")}
        </a>
      )}
    </div>
  );
}
