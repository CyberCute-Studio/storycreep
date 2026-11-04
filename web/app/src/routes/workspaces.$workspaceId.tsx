import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useEffect } from "react";
import { sessionQuery } from "../api";
import { requireSession } from "../auth/session";
import { found } from "../shell/found";
import { rememberWorkspace, workspaceQuery } from "../workspace/queries";
import { WorkspaceBar } from "../workspace/WorkspaceBar";

export const Route = createFileRoute("/workspaces/$workspaceId")({
  beforeLoad: requireSession,
  loader: ({ context, params }) =>
    found(
      context.queryClient.ensureQueryData(workspaceQuery(params.workspaceId)),
    ),
  component: WorkspaceLayout,
});

// The pages of a workspace (PRD FR-TEN-01–04, FR-PROJ-01): its projects, its
// members and invitations, its trash, and its settings, under the bar that
// switches to another of the user's workspaces.
function WorkspaceLayout() {
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  const userId = useSuspenseQuery(sessionQuery).data?.user_id;
  // The project list opens this workspace next on this device.
  useEffect(() => {
    if (userId !== undefined) {
      rememberWorkspace(userId, workspace.id);
    }
  }, [userId, workspace.id]);
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <WorkspaceBar workspace={workspace} />
      <Outlet />
    </div>
  );
}
