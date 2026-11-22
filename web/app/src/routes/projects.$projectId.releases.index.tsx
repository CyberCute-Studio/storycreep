import type { Snapshot } from "@storycreep/shared/api";
import {
  useQueries,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExportButton } from "../authoring/ExportDialog";
import { publishes } from "../content/deletion";
import { projectQuery } from "../project/queries";
import { PublishButton } from "../releases/PublishDialog";
import {
  draft,
  readReleasesAgain,
  releaseQuery,
  releasesQuery,
} from "../releases/queries";
import {
  DownloadButton,
  ReleaseFacts,
  StatusLabel,
} from "../releases/ReleaseItem";
import { found } from "../shell/found";
import { buttonClass, MoreButton } from "../ui/Button";
import { Page, Status } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/releases/")({
  loader: async ({ context: { queryClient }, params }) => {
    await found(queryClient.ensureQueryData(projectQuery(params.projectId)));
    await found(
      queryClient.ensureInfiniteQueryData(releasesQuery(params.projectId)),
    );
  },
  component: Releases,
});

// A project's releases (PRD FR-SNAP-01, FR-SNAP-02, J4, plan note N88): its
// snapshots, newest first, each with its status, who published it and when,
// its note, and its counts, a link to its read-only view, its zip, and its
// diff from the release before; and, for a role that may publish a project
// that is not archived (publish_min_role), the dialog that publishes the
// draft. The page reads each snapshot that is building again by itself, so
// that a poll costs the same however many of the list's pages are loaded,
// and the whole list once as a build ends. It says once that the build of
// what it published has started, and the list's rows show the rest.
function Releases() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const list = useSuspenseInfiniteQuery(releasesQuery(projectId));
  const listed = list.data.pages.flatMap((page) => page.items);
  // Each starts from what the list says, so that a first read that fails
  // leaves it building, which goes on being read.
  const builds = useQueries({
    queries: listed
      .filter((s) => s.status === "building")
      .map((s) => ({
        ...releaseQuery(projectId, s.id),
        initialData: s,
      })),
  });
  const ended = builds.flatMap((build) =>
    build.data !== undefined && build.data.status !== "building"
      ? [build.data]
      : [],
  );
  const releases = listed.map(
    (s) => ended.find((build) => build.id === s.id) ?? s,
  );
  // A build that ends changes what the lists say, as the newest ready
  // release, which the publish dialog's suggestion compares with.
  const client = useQueryClient();
  const endedKey = ended.map((s) => s.id).join(" ");
  useEffect(() => {
    if (endedKey !== "") {
      void readReleasesAgain(client, projectId);
    }
  }, [endedKey, client, projectId]);
  const [published, setPublished] = useState<Snapshot>();
  const said =
    published === undefined
      ? undefined
      : t("releases.said.building", { version: published.version });
  return (
    <Page
      title={t("releases.title")}
      documentTitle={t("releases.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("releases.intro")}</p>
      <div className="flex flex-wrap gap-2">
        {publishes(project) && project.archived_at === undefined && (
          <PublishButton
            project={project}
            // Cleared as an attempt is sent, so that the live region says the
            // next build's start, of the same version too, as a change.
            onPublishing={() => setPublished(undefined)}
            onPublished={setPublished}
          />
        )}
        <ExportButton projectId={projectId} releases={releases} />
      </div>
      <Status message={said} />
      {releases.length === 0 ? (
        <p>{t("releases.none")}</p>
      ) : (
        <ul>
          {releases.map((release, i) => (
            <ReleaseRow
              key={release.id}
              projectId={projectId}
              release={release}
              previous={releases.slice(i + 1).find((s) => s.status === "ready")}
              olderUnread={list.hasNextPage}
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("releases.more")}</MoreButton>
    </Page>
  );
}

/** A release of the list, whose diff compares it with previous, the ready one before it, with what the compare page finds before it while older pages are unread, which may be the draft, or else with the draft. */
function ReleaseRow({
  projectId,
  release,
  previous,
  olderUnread,
}: {
  projectId: string;
  release: Snapshot;
  previous: Snapshot | undefined;
  /** Whether the list has older releases that it has not read. */
  olderUnread: boolean;
}) {
  const { t } = useTranslation();
  const params = { projectId };
  return (
    <li className="space-y-2 border-t border-border py-3">
      <h2 className="flex flex-wrap items-center gap-2 text-lg font-semibold">
        <span>{release.version}</span>
        <StatusLabel status={release.status} />
      </h2>
      <ReleaseFacts release={release} />
      <div className="flex flex-wrap gap-2">
        <Link
          to="/projects/$projectId/releases/$snapshotId"
          params={{ ...params, snapshotId: release.id }}
          aria-label={t("releases.view_of", { version: release.version })}
          className={buttonClass()}
        >
          {t("releases.view_button")}
        </Link>
        {release.status === "ready" && (
          <>
            <Link
              to="/projects/$projectId/releases/compare"
              params={params}
              search={
                previous !== undefined
                  ? { from: previous.id, to: release.id }
                  : olderUnread
                    ? { to: release.id }
                    : { from: release.id, to: draft }
              }
              aria-label={
                previous !== undefined
                  ? t("releases.compare_of", {
                      version: release.version,
                      previous: previous.version,
                    })
                  : olderUnread
                    ? t("releases.compare_earlier_of", {
                        version: release.version,
                      })
                    : t("releases.compare_draft_of", {
                        version: release.version,
                      })
              }
              className={buttonClass()}
            >
              {t("releases.compare_button")}
            </Link>
            <DownloadButton projectId={projectId} release={release} />
          </>
        )}
      </div>
    </li>
  );
}
