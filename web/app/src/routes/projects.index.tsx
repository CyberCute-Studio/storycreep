import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { accountQuery, instanceQuery, notFoundBy, sessionQuery } from "../api";
import { requireSession } from "../auth/session";
import { VerifyNotice } from "../auth/VerifyNotice";
import { Page } from "../ui/Page";
import {
  lastWorkspace,
  workspaceQuery,
  workspacesQuery,
} from "../workspace/queries";
import {
  CreateWorkspaceForm,
  useCreateWorkspace,
} from "../workspace/WorkspaceBar";

// The project list (PRD FR-PUB-05 AC2), which is a workspace's: the one that
// the device last showed the user, while they are still a member of it, or
// else their first. The server keeps no current workspace, so the page goes
// to the list of the one that it chose, whose address names it.
export const Route = createFileRoute("/projects/")({
  beforeLoad: requireSession,
  loader: async ({ context: { queryClient } }) => {
    const session = await queryClient.ensureQueryData(sessionQuery);
    const last = session === null ? undefined : lastWorkspace(session.user_id);
    let workspaceId: string | undefined;
    if (last !== undefined) {
      try {
        const workspace = await queryClient.fetchQuery(workspaceQuery(last));
        // One that an instance admin acts in, without being a member of it,
        // is no workspace of theirs to go back to.
        if (workspace.membership) {
          workspaceId = workspace.id;
        }
      } catch (error) {
        // A workspace that the user has left since goes for the first.
        if (!notFoundBy(error)) {
          throw error;
        }
      }
    }
    workspaceId ??= (await queryClient.fetchInfiniteQuery(workspacesQuery))
      .pages[0]?.items[0]?.id;
    if (workspaceId !== undefined) {
      throw redirect({
        to: "/workspaces/$workspaceId",
        params: { workspaceId },
        replace: true,
      });
    }
  },
  component: NoWorkspace,
});

// The page of a user who is a member of no workspace, as once they have left
// every one, which creates one.
function NoWorkspace() {
  const { t } = useTranslation();
  const account = useSuspenseQuery(accountQuery).data;
  const instance = useSuspenseQuery(instanceQuery).data;
  const create = useCreateWorkspace();
  return (
    <Page title={t("projects.title")}>
      <VerifyNotice account={account} instance={instance} />
      <p>{t("projects.no_workspace")}</p>
      <CreateWorkspaceForm create={create} />
    </Page>
  );
}
