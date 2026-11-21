// What the pages of a project's releases show of one (PRD FR-SNAP-01,
// FR-SNAP-02, plan note N88): its status, in words, why its build failed,
// who published it and when, its counts, and the button that downloads its
// zip, which says why where the server refused it, as over the hourly limit
// of exports.

import type { Snapshot } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import { useMutation } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { Button } from "../ui/Button";
import { RefusalAlert } from "../ui/Page";
import { downloadRelease } from "./queries";

/** Returns a snapshot's status, in words. */
export function statusText(t: TFunction, status: Snapshot["status"]): string {
  switch (status) {
    case "building":
      return t("releases.status.building");
    case "ready":
      return t("releases.status.ready");
    case "failed":
      return t("releases.status.failed");
  }
}

/** Returns why a snapshot's build failed, in words. */
function failureText(
  t: TFunction,
  code: NonNullable<Snapshot["failure_code"]>,
): string {
  switch (code) {
    case "validation_failed":
      return t("releases.failure.validation_failed");
    case "validation_changed":
      return t("releases.failure.validation_changed");
    case "build_failed":
      return t("releases.failure.build_failed");
  }
}

/** A snapshot's status, as a label that its words tell, and its colour only repeats. */
export function StatusLabel({ status }: { status: Snapshot["status"] }) {
  const { t } = useTranslation();
  return (
    <span
      className={`rounded border px-2 text-sm ${
        status === "failed"
          ? "border-danger text-danger"
          : "border-border text-fg-muted"
      }`}
    >
      {statusText(t, status)}
    </span>
  );
}

/** Who published a snapshot and when, its counts once ready, and why it failed, if it did. */
export function ReleaseFacts({ release }: { release: Snapshot }) {
  const { t, i18n } = useTranslation();
  return (
    <>
      <p className="break-words whitespace-pre-line">{release.note}</p>
      <p className="break-words text-fg-muted">
        {t("releases.published", {
          name: release.published_by_name ?? t("releases.former_member"),
          at: formatDateTime(i18n.language, new Date(release.published_at)),
        })}
      </p>
      {release.counts !== undefined && (
        <p className="text-fg-muted">
          {t("releases.counts", { ...release.counts })}
        </p>
      )}
      {release.failure_code !== undefined && (
        <p className="text-danger">{failureText(t, release.failure_code)}</p>
      )}
    </>
  );
}

/** The button that downloads a ready release's zip. */
export function DownloadButton({
  projectId,
  release,
}: {
  projectId: string;
  release: Snapshot;
}) {
  const { t } = useTranslation();
  const download = useMutation({
    mutationFn: () => downloadRelease(projectId, release),
  });
  return (
    <>
      <Button
        aria-label={t("releases.download_of", { version: release.version })}
        disabled={download.isPending}
        onClick={() => download.mutate()}
      >
        {t("releases.download")}
      </Button>
      <RefusalAlert refusal={refusalOf(download.error, t)} />
    </>
  );
}
