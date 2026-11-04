import type { Project } from "@storycreep/shared/api";
import { formatDate } from "@storycreep/shared/i18n";
import {
  useMutation,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { Button, MoreButton } from "../ui/Button";
import { Page, RefusalAlert, Status } from "../ui/Page";
import {
  managesWorkspace,
  trashedProjectsQuery,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";

export const Route = createFileRoute("/workspaces/$workspaceId/trash")({
  loader: async ({ context: { queryClient }, params }) => {
    const workspace = await queryClient.ensureQueryData(
      workspaceQuery(params.workspaceId),
    );
    // Only its owners and admins list a workspace's trash (§5.6).
    if (!managesWorkspace(workspace.role)) {
      throw notFound();
    }
    await queryClient.ensureInfiniteQueryData(
      trashedProjectsQuery(workspace.id),
    );
  },
  component: Trash,
});

// The projects of a workspace that were deleted and can still be restored,
// for 30 days from their deletion, after which they are deleted for good,
// with all of their content (PRD FR-PROJ-01 AC3).
function Trash() {
  const { t } = useTranslation();
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  const list = useSuspenseInfiniteQuery(trashedProjectsQuery(workspaceId));
  const projects = list.data.pages.flatMap((page) => page.items);
  // What the page says of the project that it restored, which goes from
  // the list with its item.
  const [restored, setRestored] = useState<string>();
  return (
    <Page
      title={t("trash.title")}
      documentTitle={t("workspace.page_title", {
        page: t("trash.title"),
        workspace: workspace.name,
      })}
      width="wide"
    >
      <p>{t("trash.projects_intro")}</p>
      <Status message={restored} />
      {projects.length === 0 ? (
        <p className="text-fg-muted">{t("trash.projects_empty")}</p>
      ) : (
        <ul className="space-y-3" aria-label={t("trash.projects_list")}>
          {projects.map((project) => (
            <TrashedProject
              key={project.id}
              project={project}
              workspace={workspace}
              onRestored={() =>
                setRestored(t("trash.restored", { name: project.name }))
              }
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("trash.more")}</MoreButton>
    </Page>
  );
}

function TrashedProject({
  project,
  workspace,
  onRestored,
}: {
  project: Project;
  workspace: Workspace;
  onRestored: () => void;
}) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const nameId = useId();
  const locale = i18n.language;
  const restore = useMutation({
    mutationFn: () =>
      api.call("restoreProject", {
        path: { project_id: project.id },
        headers: csrfHeaders(session),
      }),
    onSuccess: async () => {
      onRestored();
      await client.invalidateQueries({
        queryKey: ["workspaces", workspace.id],
      });
    },
  });
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {project.name}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        {project.deleted_at !== undefined && (
          <>
            <dt className="text-fg-muted">{t("trash.deleted")}</dt>
            <dd>{formatDate(locale, new Date(project.deleted_at))}</dd>
          </>
        )}
        {project.hard_delete_after !== undefined && (
          <>
            <dt className="text-fg-muted">{t("trash.purged")}</dt>
            <dd>{formatDate(locale, new Date(project.hard_delete_after))}</dd>
          </>
        )}
      </dl>
      {/* A restore that the projects' limit refuses says so, with where the
          plans are, if the server names it (PRD FR-ENT-03). */}
      <RefusalAlert refusal={refusalOf(restore.error, t)} />
      <Button
        aria-describedby={nameId}
        disabled={restore.isPending}
        onClick={() => restore.mutate()}
      >
        {t("trash.restore")}
      </Button>
    </li>
  );
}
