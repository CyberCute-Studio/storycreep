// What every page of a workspace shows above its own content (PRD FR-TEN-01):
// the workspace's name, which, for a user with a workspace other than it,
// switches to another (WorkspaceSwitcher), a hidden switcher otherwise, as
// an instance of one workspace never shows one (§5.4); a way to create
// another workspace; and the links to the workspace's pages that the user's
// role shows.

import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
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
import { Menu, MenuItem } from "../ui/Menu";
import { Alert } from "../ui/Page";
import { managesWorkspace, type Workspace, workspacesQuery } from "./queries";

/**
 * The classes of a link of a workspace's or a project's navigation, the
 * current one marked: of a finger's height, 8 px apart (the list's gap), with
 * 8 px each side of its text, so that the project's eleven pages take two
 * rows of a tablet held upright, whose canvas takes the window's height
 * below them.
 */
export const navLink =
  "flex min-h-11 items-center rounded-md px-2 text-fg underline-offset-2 hover:underline data-[status=active]:bg-surface data-[status=active]:font-semibold";

export function WorkspaceBar({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const params = { workspaceId: workspace.id };
  // A guest acts on the projects granted to them alone, and sees nothing
  // else of the workspace (§5.5).
  const member = workspace.role !== "guest";
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <WorkspaceSwitcher current={workspace} />
        <NewWorkspace />
      </div>
      <nav aria-label={t("workspace.nav.label", { name: workspace.name })}>
        <ul className="flex flex-wrap gap-2 border-b border-border pb-2">
          <li>
            <Link
              to="/workspaces/$workspaceId"
              params={params}
              activeOptions={{ exact: true }}
              className={navLink}
            >
              {t("workspace.nav.projects")}
            </Link>
          </li>
          {member && (
            <li>
              <Link
                to="/workspaces/$workspaceId/members"
                params={params}
                className={navLink}
              >
                {t("workspace.nav.members")}
              </Link>
            </li>
          )}
          {managesWorkspace(workspace.role) && (
            <li>
              <Link
                to="/workspaces/$workspaceId/trash"
                params={params}
                className={navLink}
              >
                {t("workspace.nav.trash")}
              </Link>
            </li>
          )}
          {member && (
            <li>
              <Link
                to="/workspaces/$workspaceId/settings"
                params={params}
                className={navLink}
              >
                {t("workspace.nav.settings")}
              </Link>
            </li>
          )}
        </ul>
      </nav>
    </div>
  );
}

/**
 * The name of the workspace that the page shows, which opens a menu of the
 * user's other workspaces to switch to, once they have one other than it,
 * as an instance admin does of their own in a workspace that they act in
 * without being a member of it: a menu of links, rather than a choice that
 * goes to another page as it changes, which would take the user somewhere
 * that they only meant to look at (WCAG 3.2.2).
 */
function WorkspaceSwitcher({ current }: { current: Workspace }) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(workspacesQuery);
  const workspaces = list.data?.pages.flatMap((page) => page.items) ?? [];
  const name = <span className="min-w-0 truncate">{current.name}</span>;
  if (!workspaces.some((w) => w.id !== current.id) && !list.hasNextPage) {
    return (
      <p className="flex min-h-11 min-w-0 items-center text-lg font-semibold">
        {name}
      </p>
    );
  }
  return (
    <Menu
      trigger={
        <Button
          className="max-w-full text-lg font-semibold"
          aria-label={t("workspace.switcher", { name: current.name })}
        >
          {name}
          <span aria-hidden="true">▾</span>
        </Button>
      }
    >
      {workspaces.map((w) => (
        <MenuItem key={w.id} asChild>
          <Link
            to="/workspaces/$workspaceId"
            params={{ workspaceId: w.id }}
            // The router marks the link of the workspace that the page
            // shows as the current page (aria-current).
            className="data-[status=active]:font-semibold"
          >
            <span className="min-w-0 flex-1 truncate">{w.name}</span>
            {w.id === current.id && <span aria-hidden="true">✓</span>}
          </Link>
        </MenuItem>
      ))}
      {list.hasNextPage && (
        <MenuItem
          disabled={list.isFetchingNextPage}
          // The menu stays open, with the next page's workspaces in it.
          onSelect={(event) => {
            event.preventDefault();
            void list.fetchNextPage();
          }}
        >
          {t("workspace.more")}
        </MenuItem>
      )}
    </Menu>
  );
}

/** The button that creates a workspace, in a dialog. */
function NewWorkspace() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const create = useCreateWorkspace(() => setOpen(false));
  // The dialog stays open while the workspace is being created, which the
  // page then goes to, and opens again without the refusal of the attempt
  // before.
  return (
    <Dialog.Root
      open={open}
      onOpenChange={closesUnlessPending(setOpen, create)}
    >
      <Dialog.Trigger asChild>
        <Button aria-haspopup="dialog">{t("workspace.create")}</Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("workspace.create_title")}
        description={t("workspace.create_intro")}
      >
        <CreateWorkspaceForm create={create} inDialog />
      </DialogFrame>
    </Dialog.Root>
  );
}

/**
 * The mutation that creates a workspace, whose owner the user becomes (PRD
 * FR-TEN-01 AC2), and goes to it once it has, after onCreated.
 */
export function useCreateWorkspace(onCreated?: () => void) {
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: async (name: string) =>
      (
        await api.call("createWorkspace", {
          headers: csrfHeaders(session),
          body: { name },
        })
      ).body,
    onSuccess: async (workspace) => {
      await client.invalidateQueries({ queryKey: ["workspaces"] });
      onCreated?.();
      await navigate({
        to: "/workspaces/$workspaceId",
        params: { workspaceId: workspace.id },
      });
    },
  });
}

const workspaceField = bodyField("name");

/**
 * The form that creates a workspace with create (useCreateWorkspace): in a
 * dialog, or on the page of a user who has none.
 */
export function CreateWorkspaceForm({
  create,
  inDialog = false,
}: {
  create: ReturnType<typeof useCreateWorkspace>;
  inDialog?: boolean;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const refusal = refusalOf(create.error, t, workspaceField);
  useFocusRefusal(form, create.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    create.mutate(String(new FormData(event.currentTarget).get("name")));
  };
  return (
    <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
      <Alert message={refusal?.message} />
      <TextField
        label={t("workspace.name")}
        name="name"
        autoComplete="off"
        required
        maxLength={100}
        error={refusal?.fields.name}
      />
      <FormButtons
        cancel={inDialog ? t("workspace.cancel") : undefined}
        submit={t("workspace.create_submit")}
        pending={create.isPending}
      />
    </form>
  );
}
