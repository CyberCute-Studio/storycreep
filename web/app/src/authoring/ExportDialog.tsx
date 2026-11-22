// The dialog of the authoring export (PRD FR-DATA-01, plan note N90): what to
// export, of the draft or of a release, the full or the content-only profile,
// with or without assets and the release history, and what a full export
// holds of people. A job builds the archive, which the dialog follows until
// it ends, and then shows its size, with a warning where it is larger than
// the instance's import limit. An export is the user's own, so the dialog
// picks up the one that is on its way when it opens again.

import {
  ApiError,
  type AuthoringExportBody,
  type Snapshot,
} from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { type FormEvent, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import {
  nextPoll,
  readyReleasesQuery,
  refusedByServer,
} from "../releases/queries";
import { Button, buttonClass } from "../ui/Button";
import { DialogFrame, FormButtons } from "../ui/Dialog";
import {
  CheckboxField,
  GroupField,
  RadioField,
  SelectField,
} from "../ui/Field";
import { Alert, RefusalAlert, Status } from "../ui/Page";
import { formatBytes } from "../workspace/Usage";

/** The source of an export of the draft. */
const draftSource = "draft";

/** The key of every read of projectId's exports. */
function exportsKey(projectId: string) {
  return ["projects", projectId, "authoring-exports"] as const;
}

/** Whether the export has yet to end. */
function onItsWay(e: AuthoringExportBody | undefined): boolean {
  return e?.status === "queued" || e?.status === "building";
}

/** The user's newest export of the project, which the dialog shows as it opens. */
function newestExportQuery(projectId: string) {
  return queryOptions({
    queryKey: [...exportsKey(projectId), "newest"] as const,
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "listAuthoringExports",
          { path: { project_id: projectId }, query: { limit: 1 } },
          { signal },
        )
      ).body.items[0] ?? null,
    staleTime: 0,
  });
}

/**
 * The export id, read again until it ends or the server refuses it (nextPoll),
 * including when no read of it has succeeded yet, as the form waits on it.
 */
function exportQuery(projectId: string, id: string) {
  return queryOptions({
    queryKey: [...exportsKey(projectId), id] as const,
    // The form shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "getAuthoringExport",
          { path: { project_id: projectId, export_id: id } },
          { signal },
        )
      ).body,
    refetchInterval: (query) =>
      query.state.data === undefined || onItsWay(query.state.data)
        ? nextPoll(query.state.error)
        : false,
  });
}

/** The button that opens the dialog, whose form starts anew each time. */
export function ExportButton({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  return (
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <Button>{t("authoring_export.open")}</Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("authoring_export.title")}
        description={t("authoring_export.intro")}
      >
        <ExportForm projectId={projectId} />
      </DialogFrame>
    </Dialog.Root>
  );
}

function ExportForm({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  // Every ready release, rather than the pages that the page behind has read.
  const ready = useQuery(readyReleasesQuery(projectId)).data ?? [];
  const [chosen, setChosen] = useState(draftSource);
  // A release that left the list is no longer chosen, so that the form never
  // asks for what it does not show.
  const source = ready.some((r) => r.id === chosen) ? chosen : draftSource;
  const [profile, setProfile] = useState<"full" | "content">("full");
  const [assets, setAssets] = useState(true);
  const [history, setHistory] = useState(true);
  const [followed, setFollowed] = useState<string>();
  const profileName = useId();

  const newest = useQuery(newestExportQuery(projectId));
  const id = followed ?? newest.data?.id;
  const current = useQuery({
    ...exportQuery(projectId, id ?? ""),
    enabled: id !== undefined,
  });
  // A refusal other than the rate limit's says that the export is no longer
  // the user's to read, so what was last read of it no longer holds the form.
  const gone = refusedByServer(current.error) && current.error.status !== 429;
  const shown = gone
    ? undefined
    : (current.data ??
      (newest.data?.id === id ? (newest.data ?? undefined) : undefined));

  const start = useMutation({
    mutationFn: async () =>
      (
        await api.call("createAuthoringExport", {
          path: { project_id: projectId },
          headers: csrfHeaders(session),
          body: {
            source,
            profile,
            include_assets: assets,
            include_releases: history,
          },
        })
      ).body,
    onSuccess: (e) => {
      client.setQueryData(exportQuery(projectId, e.id).queryKey, e);
      // The newest too, so that the dialog opened again shows this one rather
      // than the one before it until the list has been read.
      client.setQueryData(newestExportQuery(projectId).queryKey, e);
      setFollowed(e.id);
    },
    onError: (error) => {
      // The one on its way is what the user is after, which the dialog then
      // shows, rather than a refusal of another.
      const existing = inProgressId(error);
      if (existing !== undefined) {
        setFollowed(existing);
        void client.invalidateQueries({
          queryKey: newestExportQuery(projectId).queryKey,
        });
      }
    },
  });
  const refusal =
    (inProgressId(start.error) === undefined
      ? refusalOf(start.error, t)
      : undefined) ?? (gone ? refusalOf(current.error, t) : undefined);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    start.mutate();
  };
  const busy = start.isPending || onItsWay(shown);
  return (
    <form onSubmit={submit} className="space-y-4">
      <SelectField
        label={t("authoring_export.source")}
        value={source}
        disabled={busy}
        hint={
          source === draftSource
            ? undefined
            : t("authoring_export.source_release_hint")
        }
        onChange={(e) => setChosen(e.target.value)}
      >
        <option value={draftSource}>
          {t("authoring_export.source_draft")}
        </option>
        {ready.map((r) => (
          <option key={r.id} value={r.id}>
            {t("authoring_export.source_release", { version: r.version })}
          </option>
        ))}
      </SelectField>
      <GroupField legend={t("authoring_export.profile")}>
        <RadioField
          name={profileName}
          label={t("authoring_export.profile_full")}
          hint={t("authoring_export.profile_full_hint")}
          checked={profile === "full"}
          disabled={busy}
          onChange={() => setProfile("full")}
        />
        <RadioField
          name={profileName}
          label={t("authoring_export.profile_content")}
          hint={t("authoring_export.profile_content_hint")}
          checked={profile === "content"}
          disabled={busy}
          onChange={() => setProfile("content")}
        />
      </GroupField>
      {profile === "full" && (
        <p className="rounded-md border border-border bg-surface px-4 py-3">
          {t("authoring_export.personal_data")}
        </p>
      )}
      <CheckboxField
        label={t("authoring_export.assets")}
        hint={t("authoring_export.assets_hint")}
        checked={assets}
        disabled={busy}
        onChange={(e) => setAssets(e.target.checked)}
      />
      <CheckboxField
        label={t("authoring_export.releases")}
        hint={t("authoring_export.releases_hint")}
        checked={history}
        disabled={busy}
        onChange={(e) => setHistory(e.target.checked)}
      />
      <RefusalAlert refusal={refusal} />
      {shown !== undefined && (
        <ExportResult
          projectId={projectId}
          item={shown}
          releases={ready}
          onExpired={() => void current.refetch()}
        />
      )}
      <FormButtons
        cancel={t("authoring_export.close")}
        submit={t("authoring_export.submit")}
        pending={start.isPending}
        disabled={onItsWay(shown)}
      />
    </form>
  );
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

/** Where the export is, and once it is ready its size and its download. */
function ExportResult({
  projectId,
  item,
  releases,
  onExpired,
}: {
  projectId: string;
  item: AuthoringExportBody;
  releases: readonly Snapshot[];
  /** Reads the export again, whose refusal the form then shows. */
  onExpired: () => void;
}) {
  const { t, i18n } = useTranslation();
  const version = releases.find((r) => r.id === item.snapshot_id)?.version;
  const heading =
    item.snapshot_id === undefined
      ? t("authoring_export.latest_draft")
      : version === undefined
        ? t("authoring_export.latest_release_unlisted")
        : t("authoring_export.latest_release", { version });
  const size =
    item.size_bytes === undefined
      ? undefined
      : formatBytes(i18n.language, item.size_bytes);
  return (
    <section className="space-y-2 border-t border-border pt-3">
      <h3 className="font-semibold">{heading}</h3>
      {item.status === "failed" ? (
        <Alert message={t("authoring_export.failed")} />
      ) : (
        <Status
          message={
            item.status === "ready"
              ? t("authoring_export.ready", {
                  size,
                  until: formatDateTime(
                    i18n.language,
                    new Date(item.expires_at),
                  ),
                })
              : item.status === "building"
                ? t("authoring_export.building")
                : t("authoring_export.queued")
          }
        />
      )}
      {item.exceeds_import_limit && (
        <Alert
          message={t("authoring_export.too_large", {
            size,
            limit: formatBytes(i18n.language, item.import_max_bytes),
          })}
        />
      )}
      {item.status === "ready" && (
        // A link rather than a read into memory, as an archive may have
        // gigabytes, which the browser then saves as it arrives.
        <a
          href={`/api/v1/projects/${projectId}/authoring-exports/${item.id}/download`}
          download={item.file_name}
          className={buttonClass()}
          onClick={(event) => {
            // The browser would report the refusal of an archive past its
            // time as a failed download, which says nothing of why.
            if (Date.now() >= new Date(item.expires_at).getTime()) {
              event.preventDefault();
              onExpired();
            }
          }}
        >
          {t("authoring_export.download")}
        </a>
      )}
    </section>
  );
}
