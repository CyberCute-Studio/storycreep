import type { GrantBody, MemberBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { Dialog } from "radix-ui";
import { type FormEvent, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, revoking, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { type GrantRole, grantRoles, roleLabel, roleName } from "../auth/roles";
import { csrfHeaders } from "../auth/session";
import { grantsQuery, projectQuery } from "../project/queries";
import { found } from "../shell/found";
import { Button, MoreButton } from "../ui/Button";
import { closesUnlessPending, DialogFrame, FormButtons } from "../ui/Dialog";
import { SelectField, useFocusRefusal } from "../ui/Field";
import { Alert, Page } from "../ui/Page";
import {
  managesWorkspace,
  membersQuery,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";

export const Route = createFileRoute("/projects/$projectId/access")({
  loader: async ({ context: { queryClient }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const workspace = await found(
      queryClient.ensureQueryData(workspaceQuery(project.workspace_id)),
    );
    // A grant never authorises an operation of the workspace, as managing
    // grants is (PRD FR-TEN-04 AC4).
    if (!managesWorkspace(workspace.role)) {
      throw notFound();
    }
    await Promise.all([
      queryClient.ensureQueryData(grantsQuery(project.id)),
      queryClient.ensureInfiniteQueryData(membersQuery(workspace.id)),
    ]);
  },
  component: Access,
});

/** The value of the choice that stands for no grant: the member's role in the workspace. */
const noGrant = "";

// Who acts on a project with which role (PRD FR-TEN-04): each member of its
// workspace, with their role there, and the grant that replaces it on this
// project, raising or lowering it, which the workspace's owners and admins
// give and take away; a guest acts on the projects granted to them alone,
// and only an owner changes an owner's grant (§5.6).
function Access() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const workspace = useSuspenseQuery(workspaceQuery(project.workspace_id)).data;
  const grants = useSuspenseQuery(grantsQuery(projectId)).data;
  const list = useSuspenseInfiniteQuery(membersQuery(workspace.id));
  const members = list.data.pages.flatMap((page) => page.items);
  const byMembership = new Map(grants.map((g) => [g.membership_id, g]));
  return (
    <Page
      title={t("access.title")}
      documentTitle={t("access.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("access.intro")}</p>
      <ul
        className="space-y-3"
        aria-label={t("access.list", { project: project.name })}
      >
        {members.map((member) => (
          <AccessItem
            key={member.id}
            projectId={projectId}
            member={member}
            grant={byMembership.get(member.id)}
            workspace={workspace}
          />
        ))}
      </ul>
      <MoreButton list={list}>{t("access.more")}</MoreButton>
    </Page>
  );
}

/** Returns what a member's role on the project is, in words. */
function accessOf(
  t: ReturnType<typeof useTranslation>["t"],
  member: MemberBody,
  grant: GrantBody | undefined,
): string {
  if (grant !== undefined) {
    return t("access.granted", { role: roleName(t, grant.role) });
  }
  return member.role === "guest"
    ? t("access.none")
    : t("access.workspace_role", { role: roleName(t, member.role) });
}

function AccessItem({
  projectId,
  member,
  grant,
  workspace,
}: {
  projectId: string;
  member: MemberBody;
  grant: GrantBody | undefined;
  workspace: Workspace;
}) {
  const { t } = useTranslation();
  const nameId = useId();
  // Only an owner sets or removes an owner's grant, which replaces their
  // role on the project (PRD FR-TEN-04).
  const changes = workspace.role === "owner" || member.role !== "owner";
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {member.display_name}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("access.in_workspace")}</dt>
        <dd>{roleLabel(t, member.role)}</dd>
        <dt className="text-fg-muted">{t("access.on_project")}</dt>
        <dd>{accessOf(t, member, grant)}</dd>
      </dl>
      {changes && (
        <ChangeGrant
          projectId={projectId}
          member={member}
          grant={grant}
          nameId={nameId}
        />
      )}
    </li>
  );
}

/** The dialog that gives a member a grant on the project, changes it, or takes it away. */
function ChangeGrant({
  projectId,
  member,
  grant,
  nameId,
}: {
  projectId: string;
  member: MemberBody;
  grant: GrantBody | undefined;
  nameId: string;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const path = { project_id: projectId, membership_id: member.id };
  const save = useMutation({
    mutationFn: async (role: string) => {
      const headers = csrfHeaders(session);
      if (role === noGrant) {
        // The grant is taken away whether or not the page read one, since
        // another admin may have given one since, which the user asks to be
        // gone, and one that has been taken away elsewhere since the page
        // read it is taken away, as asked.
        await revoking(api.call("removeProjectGrant", { path, headers }));
        return;
      }
      await api.call("setProjectGrant", {
        path,
        headers,
        body: { role: role as GrantRole },
      });
    },
    onSuccess: async () => {
      setOpen(false);
      // The user's own role on the project may be the one that changed.
      await client.invalidateQueries({ queryKey: ["projects", projectId] });
    },
  });
  const refusal = refusalOf(save.error, t);
  useFocusRefusal(form, save.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate(String(new FormData(event.currentTarget).get("role")));
  };
  return (
    <Dialog.Root open={open} onOpenChange={closesUnlessPending(setOpen, save)}>
      <Dialog.Trigger asChild>
        <Button aria-haspopup="dialog" aria-describedby={nameId}>
          {t("access.change")}
        </Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("access.change_title", { name: member.display_name })}
        description={t("access.change_intro")}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <SelectField
            label={t("access.role")}
            name="role"
            defaultValue={grant?.role ?? noGrant}
          >
            <option value={noGrant}>
              {member.role === "guest"
                ? t("access.choice_none")
                : t("access.choice_workspace", {
                    role: roleName(t, member.role),
                  })}
            </option>
            {grantRoles.map((role) => (
              <option key={role} value={role}>
                {roleLabel(t, role)}
              </option>
            ))}
          </SelectField>
          <FormButtons
            cancel={t("access.cancel")}
            submit={t("access.save")}
            pending={save.isPending}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
