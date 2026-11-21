import type { Snapshot } from "@storycreep/shared/api";
import {
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { publishes } from "../content/deletion";
import { projectQuery } from "../project/queries";
import { PublishButton } from "../releases/PublishDialog";
import { buildPoll, draft, releasesQuery } from "../releases/queries";
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
// draft. The page reads the list again while a snapshot is building, and
// says what became of the one that it published.
function Releases() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const list = useSuspenseInfiniteQuery({
    ...releasesQuery(projectId),
    refetchInterval: (query) =>
      query.state.data?.pages.some((page) =>
        page.items.some((s) => s.status === "building"),
      )
        ? buildPoll
        : false,
  });
  const releases = list.data.pages.flatMap((page) => page.items);
  const [published, setPublished] = useState<Snapshot>();
  const watched =
    published === undefined
      ? undefined
      : (releases.find((s) => s.id === published.id) ?? published);
  const said =
    watched === undefined
      ? undefined
      : watched.status === "building"
        ? t("releases.said.building", { version: watched.version })
        : watched.status === "ready"
          ? t("releases.said.ready", { version: watched.version })
          : t("releases.said.failed", { version: watched.version });
  return (
    <Page
      title={t("releases.title")}
      documentTitle={t("releases.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("releases.intro")}</p>
      {publishes(project) && project.archived_at === undefined && (
        <PublishButton project={project} onPublished={setPublished} />
      )}
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
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("releases.more")}</MoreButton>
    </Page>
  );
}

/** A release of the list, whose diff compares it with previous, the ready one before it, or else with the draft. */
function ReleaseRow({
  projectId,
  release,
  previous,
}: {
  projectId: string;
  release: Snapshot;
  previous: Snapshot | undefined;
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
                previous === undefined
                  ? { from: release.id, to: draft }
                  : { from: previous.id, to: release.id }
              }
              aria-label={
                previous === undefined
                  ? t("releases.compare_draft_of", {
                      version: release.version,
                    })
                  : t("releases.compare_of", {
                      version: release.version,
                      previous: previous.version,
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
