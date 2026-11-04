import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { BoardTree } from "../project/BoardTree";
import { editsProject, projectQuery, treeQuery } from "../project/queries";
import { Page } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(treeQuery(params.projectId)),
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
