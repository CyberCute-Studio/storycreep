import { ApiError, NetworkError } from "@storycreep/shared/api";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useOwnHistory } from "../project/History";
import { projectQuery } from "../project/queries";
import type { ReleaseContent } from "../releases/content";
import {
  releaseContentQuery,
  releaseQuery,
  releaseReportQuery,
} from "../releases/queries";
import {
  DownloadButton,
  ReleaseFacts,
  StatusLabel,
} from "../releases/ReleaseItem";
import { ReleaseView } from "../releases/ReleaseView";
import { canonicalId, found, searchId } from "../shell/found";
import { TextExport } from "../textexport/TextExport";
import { Alert, Page, textLink } from "../ui/Page";
import { type FindingNames, whereText } from "../validation/Findings";
import { findingKey } from "../validation/queries";

/** The search of the page: the board of the release that it shows. */
interface ReleaseSearch {
  board?: string;
}

export const Route = createFileRoute(
  "/projects/$projectId/releases/$snapshotId",
)({
  params: {
    parse: ({ snapshotId }) => ({
      snapshotId: canonicalId("snapshot", snapshotId),
    }),
  },
  validateSearch: (search: Record<string, unknown>): ReleaseSearch => {
    const board = searchId("board", search.board);
    return board === undefined ? {} : { board };
  },
  loader: async ({ context: { queryClient }, params }) => {
    await found(queryClient.ensureQueryData(projectQuery(params.projectId)));
    await found(
      queryClient.ensureQueryData(
        releaseQuery(params.projectId, params.snapshotId),
      ),
    );
  },
  component: Release,
});

// A release, read-only (PRD FR-SNAP-02 AC2, plan note N88): its metadata, its
// content, as its files hold it, its stored validation report (FR-DEP-04
// AC2), and its zip. The page shows nothing that changes the project, and
// says that it owns the project's undo and redo, while showing none, so
// that the layout shows no control and registers no key that would send a
// command from it.
function Release() {
  const { t } = useTranslation();
  const { projectId, snapshotId } = Route.useParams();
  const { board } = Route.useSearch();
  useOwnHistory(true);
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const release = useSuspenseQuery(releaseQuery(projectId, snapshotId)).data;
  const content = useQuery({
    ...releaseContentQuery(projectId, snapshotId),
    enabled: release.status === "ready",
  });
  return (
    <Page
      title={t("releases.view.title", { version: release.version })}
      documentTitle={t("releases.view.page_title", {
        version: release.version,
        project: project.name,
      })}
      width="wide"
    >
      <p>
        <Link
          to="/projects/$projectId/releases"
          params={{ projectId }}
          className={textLink}
        >
          {t("releases.view.back")}
        </Link>
      </p>
      {release.status === "ready" && (
        <p className="rounded-md border border-border bg-surface px-4 py-3">
          {t("releases.view.read_only")}
        </p>
      )}
      <div className="space-y-2">
        <StatusLabel status={release.status} />
        <ReleaseFacts release={release} />
      </div>
      {release.status === "ready" && (
        <div className="flex flex-wrap gap-2">
          <DownloadButton projectId={projectId} release={release} />
        </div>
      )}
      {release.status === "ready" && content.isPending && (
        <p>{t("releases.view.loading")}</p>
      )}
      <Alert
        message={
          !content.isError
            ? undefined
            : content.error instanceof ApiError ||
                content.error instanceof NetworkError
              ? refusalOf(content.error, t).message
              : // Its zip came, and this browser could not read it.
                t("releases.view.unreadable")
        }
      />
      {content.data !== undefined && (
        <ReleaseView content={content.data} boardId={board} />
      )}
      {/* The export is the server's, so it needs no read of the zip, which
          only lists the release's boards to narrow it to. */}
      {release.status === "ready" && (
        <TextExport
          projectId={projectId}
          release={release}
          boards={content.data?.boards ?? []}
        />
      )}
      {release.status !== "building" && (
        <StoredReport
          projectId={projectId}
          snapshotId={snapshotId}
          content={content.data}
        />
      )}
    </Page>
  );
}

/**
 * The validation report stored with the release, of what its build read:
 * its findings by severity, and those muted apart with their reasons (PRD
 * FR-DEP-03 AC4), named by what the release holds, once the page has read
 * it, as a failed release holds nothing, and no link to the editor, which
 * shows the draft rather than the release.
 */
function StoredReport({
  projectId,
  snapshotId,
  content,
}: {
  projectId: string;
  snapshotId: string;
  content: ReleaseContent | undefined;
}) {
  const { t } = useTranslation();
  const report = useQuery(releaseReportQuery(projectId, snapshotId));
  const names: FindingNames = {
    variable: (id) => content?.variables.find((v) => v.id === id)?.key,
    component: (id) => content?.components.find((c) => c.id === id)?.name,
  };
  // A failed read again leaves the report that the page holds, which is of
  // a build that has ended, and so stays as it is.
  if (report.isError && report.data === undefined) {
    return <Alert message={refusalOf(report.error, t).message} />;
  }
  if (report.data === undefined || report.data === null) {
    return null;
  }
  const { findings, muted, counts } = report.data;
  const errors = findings.filter((f) => f.severity === "error");
  const warnings = findings.filter((f) => f.severity === "warning");
  const infos = findings.filter((f) => f.severity === "info");
  const groups = [
    {
      key: "errors",
      heading: t("validation.errors", { count: errors.length }),
      items: errors,
    },
    {
      key: "warnings",
      heading: t("validation.warnings", { count: warnings.length }),
      items: warnings,
    },
    {
      key: "infos",
      heading: t("validation.infos", { count: infos.length }),
      items: infos,
    },
    {
      key: "muted",
      heading: t("validation.muted", { count: muted.length }),
      items: muted,
    },
  ].filter((group) => group.items.length > 0);
  return (
    <section aria-labelledby="release-report" className="space-y-2">
      <h2 id="release-report" className="text-xl font-semibold">
        {t("releases.view.report")}
      </h2>
      <p>
        {t("releases.publish.counts", {
          errors: counts.errors,
          warnings: counts.warnings,
        })}
      </p>
      {groups.map((group) => (
        <div key={group.key} className="space-y-1">
          <h3 className="text-lg font-semibold">{group.heading}</h3>
          <ul>
            {group.items.map((finding) => (
              <li
                key={findingKey(finding)}
                className="space-y-1 border-t border-border py-2"
              >
                <p className="break-words">{finding.message}</p>
                <p className="break-words text-fg-muted">
                  {whereText(t, finding, names)}
                  {" · "}
                  <code>{finding.code}</code>
                </p>
                {finding.mute !== undefined && (
                  <p className="break-words whitespace-pre-line">
                    {t("validation.mute_reason", {
                      reason: finding.mute.reason,
                    })}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}
