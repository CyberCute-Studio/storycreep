import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { BoardTree } from "../project/BoardTree";
import { editsProject, projectQuery } from "../project/queries";
import { found } from "../shell/found";
import { Page } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/")({
  // The project, which the layout's loader reads at once with this one,
  // comes first, so that a project that the account may not see shows the
  // page of no page before its document reads its tree. A tree that the
  // document held already, which the real-time channel does not keep
  // current while it is not live, is shown as it is, and read anew.
  loader: async ({ context: { queryClient, documents }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const doc = documents.get(project.id);
    const held = doc.holds("tree");
    await found(doc.load("tree"));
    if (held) {
      // A read anew that fails is recorded, and tried again later, while
      // the tree shows what the document held.
      void doc.refresh().catch(() => undefined);
    }
  },
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
