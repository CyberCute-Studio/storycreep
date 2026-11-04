import type { CreateProjectBody, ProjectBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, api, instanceQuery, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { VerifyNotice } from "../auth/VerifyNotice";
import { found } from "../shell/found";
import { Button, MoreButton } from "../ui/Button";
import { DialogFrame } from "../ui/Dialog";
import { TextAreaField, TextField, useFocusRefusal } from "../ui/Field";
import { Page, RefusalAlert, textLink } from "../ui/Page";
import {
  managesWorkspace,
  projectsQuery,
  usageQuery,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";
import { LimitNote } from "../workspace/Usage";

export const Route = createFileRoute("/workspaces/$workspaceId/")({
  // The layout's loader, which runs at once with this one, reads the
  // workspace too, which the list waits for, so that a workspace that the
  // account may not see shows the page of no page before the list is asked
  // for, whose refusal would be no error of the app's either.
  loader: async ({ context: { queryClient }, params }) => {
    const workspace = await found(
      queryClient.ensureQueryData(workspaceQuery(params.workspaceId)),
    );
    await found(
      queryClient.ensureInfiniteQueryData(projectsQuery(workspace.id)),
    );
  },
  component: Projects,
});

// A workspace's projects (PRD FR-PROJ-01), archived ones too, which its
// owners and admins create: a guest's, those granted to them alone
// (FR-TEN-04 AC2).
function Projects() {
  const { t } = useTranslation();
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  const account = useSuspenseQuery(accountQuery).data;
  const instance = useSuspenseQuery(instanceQuery).data;
  const list = useSuspenseInfiniteQuery(projectsQuery(workspaceId));
  const projects = list.data.pages.flatMap((page) => page.items);
  const manages = managesWorkspace(workspace.role);
  return (
    <Page
      title={t("projects.title")}
      documentTitle={t("workspace.page_title", {
        page: t("projects.title"),
        workspace: workspace.name,
      })}
      width="wide"
    >
      <VerifyNotice account={account} instance={instance} />
      {manages && <NewProject workspace={workspace} />}
      {projects.length === 0 ? (
        <p className="text-fg-muted">
          {workspace.role === "guest"
            ? t("projects.none_shared")
            : t("projects.empty")}
        </p>
      ) : (
        <ul
          className="space-y-3"
          aria-label={t("projects.list", { workspace: workspace.name })}
        >
          {projects.map((project) => (
            <ProjectItem key={project.id} project={project} />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("projects.more")}</MoreButton>
    </Page>
  );
}

function ProjectItem({ project }: { project: ProjectBody }) {
  const { t } = useTranslation();
  return (
    <li className="space-y-1 rounded-md border border-border p-4">
      <p className="flex flex-wrap items-center gap-2">
        <Link
          to="/projects/$projectId"
          params={{ projectId: project.id }}
          className={`font-medium break-words ${textLink}`}
        >
          {project.name}
        </Link>
        {project.archived_at !== undefined && (
          <span className="rounded border border-border px-2 text-sm text-fg-muted">
            {t("projects.archived")}
          </span>
        )}
      </p>
      {project.description !== "" && (
        <p className="break-words text-fg-muted">{project.description}</p>
      )}
    </li>
  );
}

const projectField = bodyField("name", "description");

/**
 * The button that creates a project, in a dialog that says how many the
 * workspace may still create, and why it may not, once it has as many as it
 * may (PRD FR-ENT-03), and goes to the project once it has.
 */
function NewProject({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const usage = useQuery({ ...usageQuery(workspace.id), enabled: open });
  const create = useMutation({
    mutationFn: async (body: CreateProjectBody) =>
      (
        await api.call("createProject", {
          path: { workspace_id: workspace.id },
          headers: csrfHeaders(session),
          body,
        })
      ).body,
    onSuccess: async (project) => {
      await client.invalidateQueries({
        queryKey: ["workspaces", workspace.id],
      });
      setOpen(false);
      await navigate({
        to: "/projects/$projectId",
        params: { projectId: project.id },
      });
    },
    // A refusal of the limit changes what the dialog says of it.
    onError: () => usage.refetch(),
  });
  const refusal = refusalOf(create.error, t, projectField);
  useFocusRefusal(form, create.error);
  const onOpenChange = (next: boolean) => {
    if (!next && create.isPending) {
      return;
    }
    setOpen(next);
    if (!next) {
      create.reset();
    }
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    create.mutate({
      name: String(data.get("name")),
      description: String(data.get("description")),
    });
  };
  return (
    <div className="flex flex-wrap items-center gap-4">
      <Dialog.Root open={open} onOpenChange={onOpenChange}>
        <Dialog.Trigger asChild>
          <Button variant="primary" aria-haspopup="dialog">
            {t("projects.create")}
          </Button>
        </Dialog.Trigger>
        <DialogFrame
          title={t("projects.create_title", { workspace: workspace.name })}
        >
          <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
            <LimitNote usage={usage.data} limit="projects" />
            <RefusalAlert refusal={refusal} />
            <TextField
              label={t("projects.name")}
              name="name"
              autoComplete="off"
              required
              maxLength={100}
              error={refusal?.fields.name}
            />
            <TextAreaField
              label={t("projects.description")}
              name="description"
              maxLength={2000}
              error={refusal?.fields.description}
            />
            <div className="flex flex-wrap justify-end gap-2">
              <Dialog.Close asChild>
                <Button disabled={create.isPending}>
                  {t("projects.cancel")}
                </Button>
              </Dialog.Close>
              <Button
                type="submit"
                variant="primary"
                disabled={create.isPending}
              >
                {t("projects.create_submit")}
              </Button>
            </div>
          </form>
        </DialogFrame>
      </Dialog.Root>
    </div>
  );
}
