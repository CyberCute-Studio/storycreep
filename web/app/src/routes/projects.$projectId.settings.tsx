import type { ProjectBody, UpdateProjectBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, notFound, useNavigate } from "@tanstack/react-router";
import { AlertDialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { managesProject, projectQuery } from "../project/queries";
import { Button } from "../ui/Button";
import { ConfirmFrame } from "../ui/Dialog";
import { TextAreaField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, RefusalAlert, Status } from "../ui/Page";
import {
  managesWorkspace,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";

export const Route = createFileRoute("/projects/$projectId/settings")({
  loader: async ({ context: { queryClient }, params }) => {
    const project = await queryClient.ensureQueryData(
      projectQuery(params.projectId),
    );
    const workspace = await queryClient.ensureQueryData(
      workspaceQuery(project.workspace_id),
    );
    if (!managesProject(project.role) && !managesWorkspace(workspace.role)) {
      throw notFound();
    }
  },
  component: Settings,
});

// A project's settings (PRD FR-PROJ-01): its name, slug, and description,
// and archiving it, which makes it read-only, which its owners and admins
// change, and deleting it to the workspace's trash, for 30 days, which the
// workspace's owners and admins alone do (§5.6).
function Settings() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const workspace = useSuspenseQuery(workspaceQuery(project.workspace_id)).data;
  const manages = managesProject(project.role);
  return (
    <Page
      title={t("project_settings.title")}
      documentTitle={t("project_settings.page_title", {
        project: project.name,
      })}
      width="wide"
    >
      {manages && project.archived_at === undefined && (
        <DetailsForm project={project} />
      )}
      {manages && <Archive project={project} />}
      {managesWorkspace(workspace.role) && (
        <DeleteProject project={project} workspace={workspace} />
      )}
    </Page>
  );
}

const detailsField = bodyField("name", "slug", "description");

function DetailsForm({ project }: { project: ProjectBody }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [unchanged, setUnchanged] = useState(false);
  const save = useMutation({
    mutationFn: (body: UpdateProjectBody) =>
      api.call("updateProject", {
        path: { project_id: project.id },
        headers: csrfHeaders(session),
        body,
      }),
    onSuccess: () => readProjectsAgain(client, project),
    // A project that changed elsewhere since the page read it shows as it
    // is now.
    onError: () => readProjectsAgain(client, project),
  });
  const refusal = refusalOf(save.error, t, detailsField, {
    slug_taken: t("project_settings.error.slug_taken"),
  });
  useFocusRefusal(form, save.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const name = String(data.get("name"));
    const slug = String(data.get("slug"));
    const description = String(data.get("description"));
    const body: UpdateProjectBody = {
      ...(name === project.name ? {} : { name }),
      ...(slug === project.slug ? {} : { slug }),
      ...(description === project.description ? {} : { description }),
    };
    const changed = Object.keys(body).length > 0;
    setUnchanged(!changed);
    if (!changed) {
      save.reset();
      return;
    }
    save.mutate({ ...body, expected_rev: project.rev });
  };
  return (
    <section className="max-w-md space-y-4" aria-labelledby="project-details">
      <h2 id="project-details" className="text-lg font-semibold">
        {t("project_settings.details")}
      </h2>
      <Alert message={refusal?.message} />
      <Status
        message={
          unchanged
            ? t("project_settings.unchanged")
            : save.isSuccess
              ? t("project_settings.saved")
              : undefined
        }
      />
      {/* Shown anew once the project has changed, by this form or
          elsewhere, rather than showing what it was. */}
      <form
        key={project.rev}
        ref={form}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <TextField
          label={t("project_settings.name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={project.name}
          error={refusal?.fields.name}
        />
        <TextField
          label={t("project_settings.slug")}
          hint={t("project_settings.slug_hint")}
          name="slug"
          autoComplete="off"
          spellCheck={false}
          required
          maxLength={64}
          defaultValue={project.slug}
          error={refusal?.fields.slug}
        />
        <TextAreaField
          label={t("project_settings.description")}
          name="description"
          maxLength={2000}
          defaultValue={project.description}
          error={refusal?.fields.description}
        />
        <Button type="submit" variant="primary" disabled={save.isPending}>
          {t("project_settings.save")}
        </Button>
      </form>
    </section>
  );
}

/** Reads again the project, and its workspace's list of projects, which shows it. */
function readProjectsAgain(
  client: ReturnType<typeof useQueryClient>,
  project: ProjectBody,
) {
  return Promise.all([
    client.invalidateQueries({ queryKey: ["projects", project.id] }),
    client.invalidateQueries({
      queryKey: ["workspaces", project.workspace_id, "projects"],
    }),
  ]);
}

/**
 * Archiving the project, which makes it read-only and leaves it out of the
 * workspace's count of projects, or unarchiving it, which its limit of
 * projects may refuse (PRD FR-PROJ-01 AC2, FR-ENT-03).
 */
function Archive({ project }: { project: ProjectBody }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const archived = project.archived_at !== undefined;
  const change = useMutation({
    mutationFn: () =>
      api.call(archived ? "unarchiveProject" : "archiveProject", {
        path: { project_id: project.id },
        headers: csrfHeaders(session),
      }),
    onSettled: () => readProjectsAgain(client, project),
  });
  return (
    <section className="space-y-2" aria-labelledby="project-archive">
      <h2 id="project-archive" className="text-lg font-semibold">
        {t("project_settings.archive_title")}
      </h2>
      <p>
        {archived
          ? t("project_settings.unarchive_intro")
          : t("project_settings.archive_intro")}
      </p>
      <RefusalAlert refusal={refusalOf(change.error, t)} />
      <Button disabled={change.isPending} onClick={() => change.mutate()}>
        {archived
          ? t("project_settings.unarchive")
          : t("project_settings.archive")}
      </Button>
    </section>
  );
}

/** Deleting the project to the workspace's trash, after a confirmation, which goes to the workspace's projects. */
function DeleteProject({
  project,
  workspace,
}: {
  project: ProjectBody;
  workspace: Workspace;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  const remove = useMutation({
    mutationFn: () =>
      api.call("deleteProject", {
        path: { project_id: project.id },
        headers: csrfHeaders(session),
      }),
    onSuccess: async () => {
      setConfirming(false);
      // The workspace's projects, which the page goes to, without this one,
      // and its trash, with it.
      await client.invalidateQueries({
        queryKey: ["workspaces", workspace.id],
        refetchType: "all",
      });
      // The project is in the trash, where nothing of it but its restore
      // answers, so the page goes before what it read of it is dropped.
      await navigate({
        to: "/workspaces/$workspaceId",
        params: { workspaceId: workspace.id },
        replace: true,
      });
      client.removeQueries({ queryKey: ["projects", project.id] });
    },
  });
  const onOpenChange = (next: boolean) => {
    if (!next && remove.isPending) {
      return;
    }
    setConfirming(next);
    if (!next) {
      remove.reset();
    }
  };
  return (
    <section className="space-y-2" aria-labelledby="project-delete">
      <h2 id="project-delete" className="text-lg font-semibold">
        {t("project_settings.delete_title")}
      </h2>
      <p>{t("project_settings.delete_intro")}</p>
      <AlertDialog.Root open={confirming} onOpenChange={onOpenChange}>
        <AlertDialog.Trigger asChild>
          <Button variant="danger">{t("project_settings.delete")}</Button>
        </AlertDialog.Trigger>
        <ConfirmFrame
          title={t("project_settings.delete_confirm_title", {
            name: project.name,
          })}
          description={t("project_settings.delete_confirm_body")}
        >
          <Alert message={refusalOf(remove.error, t)?.message} />
          <div className="flex flex-wrap justify-end gap-2">
            <AlertDialog.Cancel asChild>
              <Button disabled={remove.isPending}>
                {t("project_settings.cancel")}
              </Button>
            </AlertDialog.Cancel>
            <Button
              variant="danger"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              {t("project_settings.delete_confirm")}
            </Button>
          </div>
        </ConfirmFrame>
      </AlertDialog.Root>
    </section>
  );
}
