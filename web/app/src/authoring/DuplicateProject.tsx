import type { ProjectBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { Button } from "../ui/Button";
import { closesUnlessPending, DialogFrame, FormButtons } from "../ui/Dialog";
import { TextField, useFocusRefusal } from "../ui/Field";
import { RefusalAlert } from "../ui/Page";
import {
  projectsQuery,
  readListsAgain,
  usageQuery,
} from "../workspace/queries";
import { LimitNote } from "../workspace/Usage";

/** The most characters that a project's name has (PRD FR-PROJ-01). */
const maxNameLength = 100;

const nameField = bodyField("name");

/**
 * The name that the dialog offers a copy of a project named name: the
 * catalog's, as "Harbour (copy)", with as much of name as fits within a
 * project's names, counted in characters, as the server counts them.
 */
export function copyName(format: (name: string) => string, name: string) {
  const room = maxNameLength - Array.from(format("")).length;
  return format(Array.from(name).slice(0, Math.max(room, 0)).join("").trim());
}

/**
 * Duplicating the project into a new project of its workspace, with its
 * content and its images, but not its comments, play states, or releases
 * (PRD FR-PROJ-01), in a dialog that asks for the copy's name and says how
 * many projects the workspace may still create, and goes to the copy once
 * the server has made it.
 */
export function DuplicateProject({ project }: { project: ProjectBody }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const workspaceId = project.workspace_id;
  const usage = useQuery({ ...usageQuery(workspaceId), enabled: open });
  const copy = useMutation({
    mutationFn: async (name: string) =>
      (
        await api.call("duplicateProject", {
          path: { project_id: project.id },
          headers: csrfHeaders(session),
          body: { name },
        })
      ).body,
    onSuccess: async (made) => {
      setOpen(false);
      await navigate({
        to: "/projects/$projectId",
        params: { projectId: made.id },
      });
      await readListsAgain(client, [
        projectsQuery(workspaceId).queryKey,
        usageQuery(workspaceId).queryKey,
      ]);
    },
    // A refusal of the limit changes what the dialog says of it.
    onError: () => usage.refetch(),
  });
  const refusal = refusalOf(copy.error, t, nameField);
  useFocusRefusal(form, copy.error);
  const onOpenChange = closesUnlessPending((next: boolean) => {
    setOpen(next);
    if (!next) {
      copy.reset();
    }
  }, copy);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    copy.mutate(String(new FormData(event.currentTarget).get("name")));
  };
  return (
    <section className="space-y-2" aria-labelledby="project-duplicate">
      <h2 id="project-duplicate" className="text-lg font-semibold">
        {t("project_settings.duplicate_title")}
      </h2>
      <p>{t("project_settings.duplicate_intro")}</p>
      <Dialog.Root open={open} onOpenChange={onOpenChange}>
        <Dialog.Trigger asChild>
          <Button aria-haspopup="dialog">
            {t("project_settings.duplicate")}
          </Button>
        </Dialog.Trigger>
        <DialogFrame
          title={t("project_settings.duplicate_dialog_title", {
            name: project.name,
          })}
        >
          <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
            <LimitNote usage={usage.data} limit="projects" />
            <RefusalAlert refusal={refusal} />
            <TextField
              label={t("project_settings.duplicate_name")}
              name="name"
              autoComplete="off"
              required
              maxLength={maxNameLength}
              defaultValue={copyName(
                (name) =>
                  t("project_settings.duplicate_default_name", { name }),
                project.name,
              )}
              error={refusal?.fields.name}
            />
            <FormButtons
              cancel={t("project_settings.cancel")}
              submit={t("project_settings.duplicate_submit")}
              pending={copy.isPending}
            />
          </form>
        </DialogFrame>
      </Dialog.Root>
    </section>
  );
}
