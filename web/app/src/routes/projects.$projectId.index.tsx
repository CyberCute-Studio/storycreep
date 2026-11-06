import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { BoardTree } from "../project/BoardTree";
import { editsProject, loadParts, projectQuery } from "../project/queries";
import { Page } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/")({
  // The project, which the layout's loader reads at once with this one,
  // comes first, and then its tree (loadParts): a tree that the document
  // held already is read anew alone, rather than with every board that the
  // session has opened.
  loader: ({ context, params }) =>
    loadParts(context, params.projectId, ["tree"]),
  // The router keeps no match of the page once it is left, which it would
  // show at once on the way back, before its loader runs again: the
  // project's document, dropped once no page has shown it for a while
  // (keptUnshown), would by then be one made anew, without the tree, which
  // the page would show empty until the loader had read it.
  gcTime: 0,
  component: Boards,
});

// A project's boards, in the tree of their folders (PRD FR-PROJ-02), which
// an editor changes, while the project is not archived (FR-PROJ-01 AC2).
function Boards() {
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  return (
    <Page title={project.name} width="wide">
      {project.description !== "" && (
        <p className="break-words whitespace-pre-line text-fg-muted">
          {project.description}
        </p>
      )}
      <BoardTree project={project} editable={editable} />
    </Page>
  );
}
