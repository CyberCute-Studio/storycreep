// The personal-data export (PRD FR-GDPR-01, plan note N94), a section of the
// account's own screens: a zip of what the instance holds of the signed-in
// user, from every workspace, which a job builds without an operator, and
// which the section follows until it ends, and then offers to download, until
// it expires. An export is the user's own, so the section shows the one on its
// way, or the latest, as it opens.

import { ApiError, type PersonalExportBody } from "@storycreep/shared/api";
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
import { nextPoll, refusedByServer } from "../releases/queries";
import { Button, buttonClass } from "../ui/Button";
import { Alert, RefusalAlert, Status } from "../ui/Page";
import { formatBytes } from "../workspace/Usage";
import { refusalOf } from "./refusal";
import { csrfHeaders } from "./session";

/** The key of every read of the user's exports. */
const exportsKey = ["account", "data-exports"] as const;

/** Whether the export has yet to end. */
function onItsWay(e: PersonalExportBody | undefined): boolean {
  return e?.status === "queued" || e?.status === "building";
}

/** The user's newest export, which the section shows first. */
const newestExportQuery = queryOptions({
  queryKey: [...exportsKey, "newest"] as const,
  // The section shows a refusal of it.
  meta: { showsRefusals: true },
  queryFn: async ({ signal }) =>
    (
      await api.call(
        "listPersonalDataExports",
        { query: { limit: 1 } },
        { signal },
      )
    ).body.items[0] ?? null,
  staleTime: 0,
});

/**
 * The export id, read again until it ends or the server refuses it (nextPoll),
 * including when no read of it has succeeded yet, as the section waits on it.
 */
function exportQuery(id: string) {
  return queryOptions({
    queryKey: [...exportsKey, id] as const,
    // The section shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "getPersonalDataExport",
          { path: { export_id: id } },
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

/** The section of the account's screens that exports the user's own data. */
export function PersonalDataExport() {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [followed, setFollowed] = useState<string>();
  const newest = useQuery(newestExportQuery);
  const id = followed ?? newest.data?.id;
  const current = useQuery({
    ...exportQuery(id ?? ""),
    enabled: id !== undefined,
  });
  // A refusal other than the rate limit's says that the export is no longer
  // the user's to read, as one past its time, so what was last read of it no
  // longer stands.
  const gone = refusedByServer(current.error) && current.error.status !== 429;
  const shown = gone
    ? undefined
    : (current.data ??
      (newest.data?.id === id ? (newest.data ?? undefined) : undefined));

  const start = useMutation({
    mutationFn: async () =>
      (
        await api.call("createPersonalDataExport", {
          headers: csrfHeaders(session),
        })
      ).body,
    onSuccess: (e) => {
      client.setQueryData(exportQuery(e.id).queryKey, e);
      client.setQueryData(newestExportQuery.queryKey, e);
      setFollowed(e.id);
    },
    onError: (error) => {
      // The one on its way is what the user is after, which the section then
      // shows, rather than a refusal of another.
      const existing = inProgressId(error);
      if (existing !== undefined) {
        setFollowed(existing);
        void client.invalidateQueries({ queryKey: exportsKey });
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
    <section className="space-y-3" aria-labelledby="personal-data-export">
      <h2 id="personal-data-export" className="text-lg font-semibold">
        {t("personal_export.title")}
      </h2>
      <p className="text-fg-muted">{t("personal_export.intro")}</p>
      <p className="rounded-md border border-border bg-surface px-4 py-3">
        {t("personal_export.personal_data")}
      </p>
      <RefusalAlert refusal={refusal} />
      {shown !== undefined && (
        <ExportResult item={shown} onDownload={() => void current.refetch()} />
      )}
      <Button
        variant="primary"
        disabled={start.isPending || onItsWay(shown)}
        onClick={() => start.mutate()}
      >
        {t("personal_export.submit")}
      </Button>
    </section>
  );
}

/** Where the export is, and once it is ready its size and its download. */
function ExportResult({
  item,
  onDownload,
}: {
  item: PersonalExportBody;
  /** Reads the export again, whose refusal the section then shows. */
  onDownload: () => void;
}) {
  const { t, i18n } = useTranslation();
  return (
    <div className="space-y-2 border-t border-border pt-3">
      <h3 className="font-semibold">{t("personal_export.latest")}</h3>
      {item.status === "failed" ? (
        <Alert message={t("personal_export.failed")} />
      ) : (
        <Status
          message={
            item.status === "ready"
              ? t("personal_export.ready", {
                  size: formatBytes(i18n.language, item.size_bytes ?? 0),
                  until: formatDateTime(
                    i18n.language,
                    new Date(item.expires_at),
                  ),
                })
              : item.status === "building"
                ? t("personal_export.building")
                : t("personal_export.queued")
          }
        />
      )}
      {item.status === "ready" && (
        // A link rather than a read into memory, which the browser then saves
        // as it arrives. The browser reports a refusal as a failed download,
        // which says nothing of why, so the click also asks the server whether
        // the export still stands.
        <a
          href={`/api/v1/auth/account/data-exports/${item.id}/download`}
          download={item.file_name}
          className={buttonClass()}
          onClick={onDownload}
        >
          {t("personal_export.download")}
        </a>
      )}
    </div>
  );
}
