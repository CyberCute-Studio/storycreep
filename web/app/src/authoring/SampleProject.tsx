import {
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { Button } from "../ui/Button";
import { RefusalAlert } from "../ui/Page";
import {
  projectsQuery,
  readListsAgain,
  usageQuery,
} from "../workspace/queries";

/**
 * The button that opens the sample project, The Lighthouse Keeper, in one
 * click (PRD FR-PROJ-05): the server imports it as a new project of the
 * workspace, which the page then goes to. A refusal, as of the workspace's
 * projects limit, shows on a line of its own below the buttons that it sits
 * among.
 */
export function SampleProjectButton({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  const open = useMutation({
    mutationFn: async () =>
      (
        await api.call("importSampleProject", {
          path: { workspace_id: workspaceId },
          headers: csrfHeaders(session),
        })
      ).body,
    onSuccess: async (project) => {
      await navigate({
        to: "/projects/$projectId",
        params: { projectId: project.id },
      });
      // As a new project's creation reads them again: the list, which has
      // one more, and the usage.
      await readListsAgain(client, [
        projectsQuery(workspaceId).queryKey,
        usageQuery(workspaceId).queryKey,
      ]);
    },
  });
  const refusal = refusalOf(open.error, t);
  return (
    <>
      <Button
        disabled={open.isPending}
        aria-busy={open.isPending || undefined}
        onClick={() => open.mutate()}
      >
        {open.isPending
          ? t("sample_project.opening")
          : t("sample_project.open")}
      </Button>
      {refusal !== undefined && (
        <div className="basis-full">
          <RefusalAlert refusal={refusal} />
        </div>
      )}
    </>
  );
}
