// The dialog of the project import (PRD FR-DATA-01, AC4): it uploads an
// archive in chunks, resumes an upload that was interrupted, follows the
// import's job until it ends, and shows what it imported or why it refused
// the archive, which it does whole or not at all. The upload belongs to the
// page's button rather than to the dialog's content, so that a dialog closed
// and opened again finds it: closing the dialog stops an upload, which then
// waits to be resumed, while a job that has begun runs on the server anyway,
// and the page goes on following it, to read its list of projects again.

import type {
  ProjectImportBody,
  ProjectImportFailure,
  ProjectImportReport,
} from "@storycreep/shared/api";
import { ApiError } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { Dialog } from "radix-ui";
import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { api, notFoundBy, sessionQuery } from "../api";
import { type Refusal, refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { nextPoll, refusedByServer } from "../releases/queries";
import { Button, buttonClass } from "../ui/Button";
import { DialogFrame } from "../ui/Dialog";
import { CheckboxField, useFocusRefusal } from "../ui/Field";
import { RefusalAlert, Status } from "../ui/Page";
import {
  projectsQuery,
  readListsAgain,
  usageQuery,
} from "../workspace/queries";
import { formatBytes } from "../workspace/Usage";
import {
  beginImport,
  finishUpload,
  isTransient,
  isUploadOf,
  readImport,
  receivedBytes,
  type UploadRun,
} from "./importUpload";

/** How often the page reads an import that its job has, in milliseconds. */
const importPoll = 1000;

/** The key of every read of the workspace's imports. */
function importsKey(workspaceId: string) {
  return ["workspaces", workspaceId, "project-imports"] as const;
}

/** Whether the import's job has it, or is about to. */
function withJob(item: ProjectImportBody | undefined): boolean {
  return item?.status === "queued" || item?.status === "importing";
}

/** Whether the import has yet to end. */
function onItsWay(item: ProjectImportBody | null | undefined): boolean {
  return item?.status === "uploading" || withJob(item ?? undefined);
}

/** The user's newest import, which the dialog picks up as it opens. */
function newestImportQuery(workspaceId: string) {
  return queryOptions({
    queryKey: [...importsKey(workspaceId), "newest"] as const,
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<ProjectImportBody | null> =>
      (
        await api.call(
          "listProjectImports",
          { path: { workspace_id: workspaceId }, query: { limit: 1 } },
          { signal },
        )
      ).body.items[0] ?? null,
    staleTime: 0,
  });
}

/**
 * The import id, read again about once a second while its job has it, or
 * until a first read has succeeded. A failed read waits as a build's poll
 * does (nextPoll): out the rate limit, and for good at another refusal.
 */
function importQuery(workspaceId: string, id: string) {
  return queryOptions({
    queryKey: [...importsKey(workspaceId), id] as const,
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "getProjectImport",
          { path: { workspace_id: workspaceId, import_id: id } },
          { signal },
        )
      ).body,
    refetchInterval: (query) => {
      const item = query.state.data;
      if (item !== undefined && !withJob(item)) {
        return false;
      }
      return query.state.error === null
        ? importPoll
        : nextPoll(query.state.error);
    },
  });
}

/** What the dialog's form shows and does, which outlives its content. */
function useProjectImport(workspaceId: string, open: boolean) {
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [file, setFile] = useState<File>();
  const [remap, setRemap] = useState(false);
  const [followed, setFollowed] = useState<string>();
  const [sent, setSent] = useState(0);
  const [waiting, setWaiting] = useState(false);
  const [canceled, setCanceled] = useState(false);
  const running = useRef<AbortController>(null);
  const announced = useRef<string>(null);

  const newest = useQuery({
    ...newestImportQuery(workspaceId),
    enabled: open,
  });
  const newestOnItsWay =
    newest.data !== null && onItsWay(newest.data) ? newest.data : undefined;
  const id = followed ?? newestOnItsWay?.id;

  const start = useMutation({
    mutationFn: async ({
      chosen,
      resuming,
    }: {
      chosen: File;
      resuming?: string;
    }) => {
      const controller = new AbortController();
      running.current = controller;
      setWaiting(false);
      setCanceled(false);
      const run: UploadRun = {
        workspaceId,
        headers: csrfHeaders(session),
        signal: controller.signal,
        onSent: setSent,
        onWaiting: setWaiting,
      };
      const show = (item: ProjectImportBody) => {
        client.setQueryData(importQuery(workspaceId, item.id).queryKey, item);
        setFollowed(item.id);
      };
      try {
        let item: ProjectImportBody;
        let upload: boolean;
        if (resuming === undefined) {
          ({ item, upload } = await beginImport(
            chosen,
            remap ? "remap" : "auto",
            run,
          ));
        } else {
          item = await readImport(resuming, run);
          upload = isUploadOf(item, chosen);
        }
        show(item);
        if (upload) {
          show(await finishUpload(chosen, item, run));
        }
      } catch (error) {
        // Stopped by the user, as by closing the dialog, which is no failure.
        if (!controller.signal.aborted) {
          throw error;
        }
      }
    },
    onError: (error) => {
      // An import that has gone is no longer one to resume.
      if (notFoundBy(error)) {
        setFollowed(undefined);
        client.setQueryData(newestImportQuery(workspaceId).queryKey, null);
      }
    },
  });

  const current = useQuery({
    ...importQuery(workspaceId, id ?? ""),
    // While the upload runs it knows what the server holds, and a read of
    // it would only spend the rate limit.
    enabled: id !== undefined && !start.isPending,
  });
  // A refusal other than the rate limit's says that the import is no longer
  // the user's to read, so what was last read of it no longer holds the form.
  const gone = refusedByServer(current.error) && current.error.status !== 429;
  const item = gone
    ? undefined
    : (current.data ??
      (newestOnItsWay?.id === id ? newestOnItsWay : undefined));

  /** Forgets the import, so that the form starts anew. */
  const reset = () => {
    setFollowed(undefined);
    setFile(undefined);
    setSent(0);
    setWaiting(false);
    start.reset();
    client.setQueryData(newestImportQuery(workspaceId).queryKey, null);
  };

  const cancel = useMutation({
    mutationFn: async (importId: string) => {
      running.current?.abort();
      await api.call("cancelProjectImport", {
        path: { workspace_id: workspaceId, import_id: importId },
        headers: csrfHeaders(session),
      });
    },
    onSuccess: () => {
      reset();
      setCanceled(true);
    },
    onError: (error) => {
      if (notFoundBy(error)) {
        reset();
      } else {
        void current.refetch();
      }
    },
  });

  // An upload is not left running once the page that shows it has gone.
  useEffect(() => () => running.current?.abort(), []);

  // The new project joins the workspace's list and its usage, once.
  const readyId = item?.status === "ready" ? item.id : undefined;
  useEffect(() => {
    if (readyId !== undefined && announced.current !== readyId) {
      announced.current = readyId;
      void readListsAgain(client, [
        projectsQuery(workspaceId).queryKey,
        usageQuery(workspaceId).queryKey,
      ]);
    }
  }, [client, readyId, workspaceId]);

  return {
    file,
    remap,
    item,
    sent,
    waiting,
    canceled,
    start,
    cancel,
    readError: gone ? current.error : null,
    uploading: start.isPending,
    setRemap,
    choose: (chosen: File | undefined) => {
      setFile(chosen);
      setSent(0);
      setCanceled(false);
      start.reset();
    },
    /** Stops the upload, which stays to be resumed, as the dialog closes. */
    closed: () => {
      running.current?.abort();
      cancel.reset();
      if (item?.status === "ready" || item?.status === "failed") {
        reset();
        return;
      }
      start.reset();
    },
    reset,
  };
}

type ImportFlow = ReturnType<typeof useProjectImport>;

/** The button that opens the dialog, on the workspace's page. */
export function ImportButton({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const flow = useProjectImport(workspaceId, open);
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      flow.closed();
    }
  };
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger asChild>
        <Button aria-haspopup="dialog">{t("project_import.open")}</Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("project_import.title")}
        description={t("project_import.intro")}
        // A stray press beside the dialog does not stop an upload.
        closesOutside={!flow.uploading}
      >
        <ImportForm flow={flow} onClose={() => onOpenChange(false)} />
      </DialogFrame>
    </Dialog.Root>
  );
}

/** What the form says of a refusal of its requests. */
function refusalOfImport(
  error: unknown,
  t: TFunction,
  locale: string,
): Refusal | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  const max =
    error instanceof ApiError ? error.error?.details?.max_bytes : undefined;
  return refusalOf(error, t, undefined, {
    request_too_large: t("project_import.too_large", {
      limit: typeof max === "number" ? formatBytes(locale, max) : "",
    }),
    import_not_uploading: t("project_import.too_late"),
    not_found: t("project_import.gone"),
  });
}

function ImportForm({
  flow,
  onClose,
}: {
  flow: ImportFlow;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const { file, item, start, cancel } = flow;
  const locale = i18n.language;
  useFocusRefusal(form, start.error ?? cancel.error);

  const empty = file?.size === 0;
  // An upload that ran out of retries is one to resume, which says so; a
  // failure before there was an upload to resume is the request's own.
  const interrupted = isTransient(start.error) && item?.status === "uploading";
  const refusal =
    (interrupted
      ? { message: t("project_import.interrupted"), fields: {} }
      : refusalOfImport(start.error, t, locale)) ??
    refusalOfImport(cancel.error, t, locale) ??
    refusalOfImport(flow.readError, t, locale) ??
    (item?.status === "failed"
      ? {
          message: failureMessage(item.failure ?? { code: "unknown" }, t),
          fields: {},
        }
      : empty
        ? { message: t("project_import.empty_file"), fields: {} }
        : undefined);

  const ended = item?.status === "ready" || item?.status === "failed";
  const paused = item?.status === "uploading" && !flow.uploading;
  const resumes = paused && file !== undefined && isUploadOf(item, file);
  const cancelable = item?.status === "uploading" || item?.status === "queued";
  // The form asks for a file while no import holds it, and for the same file
  // of an upload on its way that this page no longer has.
  const picks = !flow.uploading && !ended && !withJob(item) && !resumes;
  const ready =
    file !== undefined &&
    !empty &&
    (item === undefined || isUploadOf(item, file));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (file !== undefined && (resumes || (picks && ready))) {
      start.mutate({ chosen: file, resuming: resumes ? item.id : undefined });
    }
  };

  return (
    <form ref={form} onSubmit={submit} className="space-y-4" noValidate>
      <RefusalAlert refusal={refusal} />
      <Status message={statusOf(flow, t, locale)} />
      {picks && (
        <FilePicker
          flow={flow}
          asks={item === undefined}
          wrongFile={paused && file !== undefined}
        />
      )}
      {flow.uploading && (
        <>
          <Progress
            label={t("project_import.upload_progress")}
            value={flow.sent}
            max={file?.size ?? item?.size_bytes ?? 0}
            text={t("project_import.bytes_of", {
              sent: formatBytes(locale, flow.sent),
              size: formatBytes(locale, file?.size ?? item?.size_bytes ?? 0),
            })}
          />
          <p className="text-sm text-fg-muted">
            {t("project_import.closing_stops")}
          </p>
        </>
      )}
      {resumes && <Stopped item={item} sent={flow.sent} />}
      {item !== undefined && withJob(item) && <JobProgress item={item} />}
      {item?.status === "ready" && item.report !== undefined && (
        <ImportReport
          report={item.report}
          asked={item.id_policy === "remap"}
          onOpen={onClose}
        />
      )}
      {item?.status === "failed" && item.failure?.detail !== undefined && (
        <p className="text-sm break-words text-fg-muted">
          {t("project_import.failure_detail", {
            detail: item.failure.detail,
          })}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        {cancelable && (
          <Button
            disabled={cancel.isPending}
            onClick={() => cancel.mutate(item.id)}
          >
            {t("project_import.cancel")}
          </Button>
        )}
        {ended && (
          <Button onClick={flow.reset}>{t("project_import.again")}</Button>
        )}
        <Dialog.Close asChild>
          <Button>{t("project_import.close")}</Button>
        </Dialog.Close>
        {resumes && (
          <Button type="submit" variant="primary" disabled={cancel.isPending}>
            {t("project_import.resume")}
          </Button>
        )}
        {picks && (
          <Button type="submit" variant="primary" disabled={!ready}>
            {t("project_import.submit")}
          </Button>
        )}
      </div>
    </form>
  );
}

/** The live region's message: where the import is, as it changes. */
function statusOf(
  flow: ImportFlow,
  t: TFunction,
  locale: string,
): string | undefined {
  const { item } = flow;
  if (flow.uploading) {
    return flow.waiting
      ? t("project_import.retrying")
      : t("project_import.uploading");
  }
  if (flow.canceled) {
    return t("project_import.canceled");
  }
  if (item === undefined) {
    return undefined;
  }
  switch (item.status) {
    case "uploading":
      return flow.file !== undefined && isUploadOf(item, flow.file)
        ? undefined
        : t("project_import.other_upload", {
            name: item.file_name,
            size: formatBytes(locale, item.size_bytes),
          });
    case "queued":
    case "importing":
      return t("project_import.on_its_way", {
        name: item.file_name,
        size: formatBytes(locale, item.size_bytes),
        phase: phaseMessage(item.phase, t),
      });
    case "ready":
      return t("project_import.ready", {
        name: item.report?.project_name ?? item.file_name,
      });
    default:
      return undefined;
  }
}

/** The file field, and the choice of IDs for an import that is new. */
function FilePicker({
  flow,
  asks,
  wrongFile,
}: {
  flow: ImportFlow;
  /** Whether the import is new, whose ID policy the form asks for. */
  asks: boolean;
  /** Whether the file chosen is not the one of the upload on its way. */
  wrongFile: boolean;
}) {
  const { t, i18n } = useTranslation();
  const id = useId();
  const { file } = flow;
  const chosen = (event: ChangeEvent<HTMLInputElement>) =>
    flow.choose(event.target.files?.[0]);
  return (
    <>
      <div className="flex flex-col gap-1">
        <label htmlFor={id} className="font-medium">
          {t("project_import.file_label")}
        </label>
        <input
          id={id}
          type="file"
          accept=".zip,application/zip"
          onChange={chosen}
          className="min-h-11 max-w-full file:me-2 file:min-h-11 file:cursor-pointer file:rounded-md file:border file:border-border file:bg-surface file:px-4 file:text-fg"
        />
        {file !== undefined && (
          <p className="break-words text-fg-muted">
            {t("project_import.file_chosen", {
              name: file.name,
              size: formatBytes(i18n.language, file.size),
            })}
          </p>
        )}
        {wrongFile && file !== undefined && (
          <p className="text-danger">
            {t("project_import.not_that_file", { name: file.name })}
          </p>
        )}
      </div>
      {asks && (
        <CheckboxField
          label={t("project_import.remap")}
          hint={t("project_import.remap_hint")}
          checked={flow.remap}
          onChange={(event) => flow.setRemap(event.target.checked)}
        />
      )}
    </>
  );
}

/** A labelled progress bar, indeterminate where value is undefined. */
function Progress({
  label,
  value,
  max,
  text,
}: {
  label: string;
  value?: number;
  max?: number;
  text?: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="font-medium">
        {label}
      </label>
      <progress
        id={id}
        className="block h-2 w-full accent-accent"
        value={value}
        max={max}
      />
      {text !== undefined && <p className="text-sm text-fg-muted">{text}</p>}
    </div>
  );
}

/** An upload that stopped, and what resuming it goes on with. */
function Stopped({ item, sent }: { item: ProjectImportBody; sent: number }) {
  const { t, i18n } = useTranslation();
  const held = Math.max(sent, receivedBytes(item));
  return (
    <div className="space-y-1">
      <p className="break-words">
        {t("project_import.stopped", {
          name: item.file_name,
          sent: formatBytes(i18n.language, held),
          size: formatBytes(i18n.language, item.size_bytes),
        })}
      </p>
      <p className="text-fg-muted">
        {t("project_import.stopped_policy", { policy: item.id_policy })}
      </p>
    </div>
  );
}

/** How far the import's job is, counted where it counts. */
function JobProgress({ item }: { item: ProjectImportBody }) {
  const { t, i18n } = useTranslation();
  const counted = item.progress_total > 0;
  return (
    <Progress
      label={t("project_import.job_progress")}
      value={counted ? item.progress_done : undefined}
      max={counted ? item.progress_total : undefined}
      text={
        counted
          ? t("project_import.count_of", {
              done: formatNumber(i18n.language, item.progress_done),
              total: formatNumber(i18n.language, item.progress_total),
            })
          : undefined
      }
    />
  );
}

/** Returns the words of a phase of the import. */
function phaseMessage(phase: string, t: TFunction): string {
  switch (phase) {
    case "queued":
      return t("project_import.phase.queued");
    case "checking":
      return t("project_import.phase.checking");
    case "verifying":
      return t("project_import.phase.verifying");
    case "assets":
      return t("project_import.phase.assets");
    case "writing":
      return t("project_import.phase.writing");
    default:
      return t("project_import.phase.working");
  }
}

/** What the import did: the policy, the counts, and the link to the project. */
function ImportReport({
  report,
  asked,
  onOpen,
}: {
  report: ProjectImportReport;
  /** Whether the user asked for fresh IDs. */
  asked: boolean;
  onOpen: () => void;
}) {
  const { t, i18n } = useTranslation();
  const policy =
    report.policy === "preserve"
      ? t("project_import.policy_preserve")
      : asked
        ? t("project_import.policy_remap_asked")
        : t("project_import.policy_remap_auto");
  const counts: readonly [string, number][] = [
    [t("project_import.count.boards"), report.boards],
    [t("project_import.count.nodes"), report.nodes],
    [t("project_import.count.variables"), report.variables],
    [t("project_import.count.components"), report.components],
    [t("project_import.count.comments"), report.comments],
    [t("project_import.count.play_states"), report.play_states],
    [t("project_import.count.releases"), report.releases],
    [t("project_import.count.assets"), report.assets],
  ];
  return (
    <section className="space-y-3 border-t border-border pt-3">
      <h3 className="font-semibold break-words">{report.project_name}</h3>
      <p>{policy}</p>
      <dl className="space-y-1">
        {counts.map(([label, count]) => (
          <div key={label} className="flex justify-between gap-4">
            <dt className="text-fg-muted">{label}</dt>
            <dd>{formatNumber(i18n.language, count)}</dd>
          </div>
        ))}
      </dl>
      {report.imported_users > 0 && (
        <p>
          {t("project_import.imported_users", {
            count: report.imported_users,
          })}
        </p>
      )}
      {report.missing_assets > 0 && (
        <p>
          {t("project_import.missing_assets", {
            count: report.missing_assets,
          })}
        </p>
      )}
      <Link
        to="/projects/$projectId"
        params={{ projectId: report.project_id }}
        className={buttonClass("primary")}
        onClick={onOpen}
      >
        {t("project_import.open_project")}
      </Link>
    </section>
  );
}

/** Returns what the form says of a failure, which imported nothing. */
function failureMessage(failure: ProjectImportFailure, t: TFunction): string {
  const parts = [failureCause(failure.code, t)];
  if (failure.version !== undefined) {
    parts.push(
      failure.check === undefined
        ? t("project_import.failure_version", { version: failure.version })
        : t("project_import.failure_release", {
            version: failure.version,
            check: failure.check,
          }),
    );
  } else if (failure.check !== undefined) {
    parts.push(t("project_import.failure_check", { check: failure.check }));
  }
  if (failure.section !== undefined) {
    parts.push(
      t("project_import.failure_section", { section: failure.section }),
    );
  }
  parts.push(t("project_import.nothing_imported"));
  return parts.join(" ");
}

/** Returns the words of a failure's code. */
function failureCause(code: string, t: TFunction): string {
  switch (code) {
    case "archive_invalid":
      return t("project_import.failure.archive_invalid");
    case "format_unsupported":
      return t("project_import.failure.format_unsupported");
    case "schema_invalid":
      return t("project_import.failure.schema_invalid");
    case "section_unsupported":
      return t("project_import.failure.section_unsupported");
    case "content_invalid":
      return t("project_import.failure.content_invalid");
    case "release_invalid":
      return t("project_import.failure.release_invalid");
    case "asset_invalid":
      return t("project_import.failure.asset_invalid");
    case "workspace_limit":
      return t("project_import.failure.workspace_limit");
    case "forbidden":
      return t("project_import.failure.forbidden");
    case "canceled":
      return t("project_import.failure.canceled");
    case "expired":
      return t("project_import.failure.expired");
    case "internal":
      return t("project_import.failure.internal");
    default:
      return t("project_import.failure.unknown");
  }
}
