import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { requireSession } from "../auth/session";
import { useDocumentState, useProjectDocument } from "../document/react";
import { History } from "../project/History";
import { editsProject, managesProject, projectQuery } from "../project/queries";
import { canonicalId, found } from "../shell/found";
import { NotFound } from "../shell/NotFound";
import { textLink } from "../ui/Page";
import { managesWorkspace, workspaceQuery } from "../workspace/queries";
import { navLink } from "../workspace/WorkspaceBar";

export const Route = createFileRoute("/projects/$projectId")({
  // The project's ID as the server writes it, which its pages and theirs
  // read, however the address spells it (canonicalId).
  params: {
    parse: ({ projectId }) => ({
      projectId: canonicalId("project", projectId),
    }),
  },
  beforeLoad: requireSession,
  loader: async ({ context: { queryClient }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    await found(
      queryClient.ensureQueryData(workspaceQuery(project.workspace_id)),
    );
  },
  component: ProjectLayout,
});

// The pages of a project (PRD FR-PROJ-01, FR-PROJ-02, FR-PROJ-06, FR-TEN-04):
// the tree of its boards, its variables, its components, and their types
// (FR-VAR-01, FR-COMP-01, FR-COMP-02), its trash, the members' grants on
// it, which its workspace's owners and admins manage, and its settings,
// under a link back
// to its workspace's projects, the undo and redo of the user's changes
// (FR-EDIT-08), and the note that an archived project is read-only. They
// follow the project's document while they are shown (PRD §10.6), and show
// the page of no page once the project can no longer be read, as once it
// went to the trash elsewhere.
function ProjectLayout() {
  const { projectId } = Route.useParams();
  const gone = useDocumentState(
    useProjectDocument(projectId),
    (state) => state.gone,
  );
  return gone ? <NotFound /> : <ProjectPages projectId={projectId} />;
}

function ProjectPages({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const workspace = useSuspenseQuery(workspaceQuery(project.workspace_id)).data;
  const params = { projectId };
  const settings =
    managesProject(project.role) || managesWorkspace(workspace.role);
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div className="space-y-4">
        <p>
          <Link
            to="/workspaces/$workspaceId"
            params={{ workspaceId: workspace.id }}
            className={textLink}
          >
            {t("project.back", { workspace: workspace.name })}
          </Link>
        </p>
        <nav aria-label={t("project.nav.label", { name: project.name })}>
          <ul className="flex flex-wrap gap-2 border-b border-border pb-2">
            <li>
              <Link
                to="/projects/$projectId"
                params={params}
                activeOptions={{ exact: true }}
                className={navLink}
              >
                {t("project.nav.boards")}
              </Link>
            </li>
            <li>
              <Link
                to="/projects/$projectId/variables"
                params={params}
                className={navLink}
              >
                {t("project.nav.variables")}
              </Link>
            </li>
            <li>
              <Link
                to="/projects/$projectId/components"
                params={params}
                className={navLink}
              >
                {t("project.nav.components")}
              </Link>
            </li>
            <li>
              <Link
                to="/projects/$projectId/component-types"
                params={params}
                className={navLink}
              >
                {t("project.nav.component_types")}
              </Link>
            </li>
            <li>
              <Link
                to="/projects/$projectId/trash"
                params={params}
                className={navLink}
              >
                {t("project.nav.trash")}
              </Link>
            </li>
            {managesWorkspace(workspace.role) && (
              <li>
                <Link
                  to="/projects/$projectId/access"
                  params={params}
                  className={navLink}
                >
                  {t("project.nav.access")}
                </Link>
              </li>
            )}
            {settings && (
              <li>
                <Link
                  to="/projects/$projectId/settings"
                  params={params}
                  className={navLink}
                >
                  {t("project.nav.settings")}
                </Link>
              </li>
            )}
          </ul>
        </nav>
        {editsProject(project.role) && project.archived_at === undefined && (
          <History projectId={projectId} />
        )}
        {project.archived_at !== undefined && (
          <p className="rounded-md border border-border bg-surface px-4 py-3">
            {managesProject(project.role)
              ? t("project.archived_manager")
              : t("project.archived")}
          </p>
        )}
      </div>
      <Outlet />
    </div>
  );
}
