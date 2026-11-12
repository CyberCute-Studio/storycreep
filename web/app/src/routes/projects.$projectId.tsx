import { useSuspenseQuery } from "@tanstack/react-query";
import {
  createFileRoute,
  Link,
  Outlet,
  useChildMatches,
  useNavigate,
  useRouter,
} from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { requireSession } from "../auth/session";
import { useCommandHandlers } from "../commands/registry";
import { useDocumentState, useProjectDocument } from "../document/react";
import { HistoryOwned, LayoutHistory } from "../project/History";
import { editsProject, managesProject, projectQuery } from "../project/queries";
import { askSearchFocus, forgetSearchFocus } from "../search/focus";
import { searchText } from "../search/query";
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
// under a link back to its workspace's projects, its search (FR-PROJ-03),
// the undo and redo of the user's changes (FR-EDIT-08), and the note that an
// archived project is read-only. The palette goes to each of its pages, and
// Ctrl+/, on macOS too, to its search, with the focus in the search's
// field (FR-EDIT-10, plan note N72). They follow the project's document while they are shown (PRD
// §10.6), and show the page of no page once the project can no longer be
// read, as once it went to the trash elsewhere.
function ProjectLayout() {
  const { projectId } = Route.useParams();
  const gone = useDocumentState(
    useProjectDocument(projectId),
    (state) => state.gone,
  );
  // A part of a page that shows the undo and the redo itself, as a board's
  // canvas in its toolbar, says so while it is shown (useOwnHistory), and
  // the layout shows them above the page whenever none does.
  return gone ? (
    <NotFound />
  ) : (
    <HistoryOwned>
      <ProjectPages projectId={projectId} />
    </HistoryOwned>
  );
}

/** The pages of a project that the palette goes to. */
const pages = [
  "/projects/$projectId",
  "/projects/$projectId/variables",
  "/projects/$projectId/components",
  "/projects/$projectId/component-types",
  "/projects/$projectId/trash",
  "/projects/$projectId/access",
  "/projects/$projectId/settings",
] as const;

function ProjectPages({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const workspace = useSuspenseQuery(workspaceQuery(project.workspace_id)).data;
  const params = { projectId };
  const settings =
    managesProject(project.role) || managesWorkspace(workspace.role);
  // A page that takes the window's width, as a board's for its canvas,
  // says so of itself (staticData), rather than the layout knowing it.
  const wide = useChildMatches({
    select: (matches) => matches.some((m) => m.staticData.wide === true),
  });
  const navigate = useNavigate();
  const router = useRouter();
  // The focus that the search's command asked for its page is forgotten
  // once a page that is not the search's has been shown in its place, as
  // one whose navigation overtook the search's, or once the project's
  // pages are left, so that the search's page, opened later by its link,
  // or by Back, leaves the focus where the router puts it.
  useEffect(() => {
    const stop = router.subscribe("onRendered", () => {
      if (
        !router.state.matches.some(
          (m) => m.routeId === "/projects/$projectId/search",
        )
      ) {
        forgetSearchFocus();
      }
    });
    return () => {
      stop();
      forgetSearchFocus();
    };
  }, [router]);
  const go = (to: (typeof pages)[number]) => ({
    run: () => void navigate({ to, params }),
  });
  useCommandHandlers("project", {
    // The search's page, of what the palette's search was of, as the
    // server takes it (searchText), or, from the search's key, a new one,
    // whose field takes the focus.
    "project.search": {
      run: (asked) => {
        const query = asked === undefined ? "" : searchText(asked);
        askSearchFocus();
        void navigate({
          to: "/projects/$projectId/search",
          params,
          search: query === "" ? {} : { q: query },
        });
      },
    },
    "go.boards": go("/projects/$projectId"),
    "go.variables": go("/projects/$projectId/variables"),
    "go.components": go("/projects/$projectId/components"),
    "go.component_types": go("/projects/$projectId/component-types"),
    "go.trash": go("/projects/$projectId/trash"),
    ...(managesWorkspace(workspace.role)
      ? { "go.access": go("/projects/$projectId/access") }
      : {}),
    ...(settings ? { "go.settings": go("/projects/$projectId/settings") } : {}),
  });
  return (
    <div className={`mx-auto space-y-6 ${wide ? "" : "max-w-3xl"}`}>
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
                to="/projects/$projectId/search"
                params={params}
                className={navLink}
              >
                {t("project.nav.search")}
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
          <LayoutHistory projectId={projectId} />
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
