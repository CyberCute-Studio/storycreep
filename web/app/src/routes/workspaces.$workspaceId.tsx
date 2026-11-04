import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useEffect } from "react";
import { sessionQuery } from "../api";
import { requireSession } from "../auth/session";
import { found } from "../shell/found";
import {
  rememberWorkspace,
  workspaceQuery,
  workspacesQuery,
} from "../workspace/queries";
import { WorkspaceBar } from "../workspace/WorkspaceBar";

export const Route = createFileRoute("/workspaces/$workspaceId")({
  beforeLoad: requireSession,
  // The account's workspaces too, so that the switcher shows as it will
  // stay, rather than the name alone until they come.
  loader: ({ context: { queryClient }, params }) =>
    Promise.all([
      found(queryClient.ensureQueryData(workspaceQuery(params.workspaceId))),
      queryClient.ensureInfiniteQueryData(workspacesQuery),
    ]),
  component: WorkspaceLayout,
});

// The pages of a workspace (PRD FR-TEN-01–04, FR-PROJ-01): its projects, its
// members and invitations, its trash, and its settings, under the bar that
// switches to another of the user's workspaces.
function WorkspaceLayout() {
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  const userId = useSuspenseQuery(sessionQuery).data?.user_id;
  // The project list opens this workspace next on this device, if it is one
  // of the user's memberships, rather than one that an instance admin acts
  // in, which the switcher does not list, and so would not leave.
  useEffect(() => {
    if (userId !== undefined && workspace.membership) {
      rememberWorkspace(userId, workspace.id);
    }
  }, [userId, workspace.id, workspace.membership]);
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <WorkspaceBar workspace={workspace} />
      <Outlet />
    </div>
  );
}
