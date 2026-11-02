import type {
  APITokenBody,
  CreatedAPITokenBody,
  CreateTokenBody,
  MembershipBody,
  PageAPITokenBody,
  ProjectBody,
} from "@storycreep/shared/api";
import { formatDate, formatRelativeTime } from "@storycreep/shared/i18n";
import {
  infiniteQueryOptions,
  queryOptions,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AlertDialog, Dialog } from "radix-ui";
import { type FormEvent, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { copyText } from "../errors/copy";
import { Button } from "../ui/Button";
import { ConfirmFrame, DialogFrame } from "../ui/Dialog";
import {
  CheckboxField,
  SelectField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/account/tokens")({
  loader: ({ context }) => context.queryClient.ensureQueryData(workspacesQuery),
  component: Tokens,
});

/** The most pages of a list that the page reads, of 100 items each. */
const maxPages = 20;

/** Returns every item of a paged list, from its first page on. */
async function allPages<T>(
  page: (cursor: string | undefined) => Promise<{
    items: T[];
    next_cursor: string | null;
  }>,
): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const p = await page(cursor);
    items.push(...p.items);
    if (p.next_cursor === null) {
      break;
    }
    cursor = p.next_cursor;
  }
  return items;
}

/** The workspaces that the user is a member of. */
const workspacesQuery = queryOptions({
  queryKey: ["workspaces"],
  queryFn: ({ signal }): Promise<MembershipBody[]> =>
    allPages(
      async (cursor) =>
        (
          await api.call(
            "listWorkspaces",
            { query: { cursor, limit: 100 } },
            { signal },
          )
        ).body,
    ),
});

/** The projects of a workspace that the user can see. */
function projectsQuery(workspaceId: string) {
  return queryOptions({
    queryKey: ["workspaces", workspaceId, "projects"],
    queryFn: ({ signal }): Promise<ProjectBody[]> =>
      allPages(
        async (cursor) =>
          (
            await api.call(
              "listProjects",
              {
                path: { workspace_id: workspaceId },
                query: { cursor, limit: 100 },
              },
              { signal },
            )
          ).body,
      ),
  });
}

/** What a token acts on: a whole workspace, or one project of it. */
type Target =
  | { kind: "workspace"; workspaceId: string }
  | { kind: "project"; projectId: string };

/** The tokens of a target, a page at a time. */
function tokensQuery(target: Target) {
  return infiniteQueryOptions({
    queryKey: ["tokens", target],
    queryFn: async ({ pageParam, signal }): Promise<PageAPITokenBody> => {
      const query = { cursor: pageParam || undefined, limit: 100 };
      return target.kind === "workspace"
        ? (
            await api.call(
              "listWorkspaceTokens",
              { path: { workspace_id: target.workspaceId }, query },
              { signal },
            )
          ).body
        : (
            await api.call(
              "listProjectTokens",
              { path: { project_id: target.projectId }, query },
              { signal },
            )
          ).body;
    },
    initialPageParam: "",
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    // A role that may not see the tokens is refused, which the page shows.
    meta: { showsRefusals: true },
  });
}

/** The scopes of a token (PRD FR-AUTH-04), in the order that the form lists them. */
const scopes = [
  "snapshot:read",
  "project:read",
  "project:write",
  "snapshot:write",
  "export:read",
] as const;

type Scope = (typeof scopes)[number];

/** How long a new token lasts, in days, or 0 for no expiry. */
const lifetimes = [0, 7, 30, 90, 365] as const;

// The API tokens of the user's workspaces (PRD FR-AUTH-04), which CI jobs
// and the Godot addon read and publish with: a token acts in one workspace,
// or on one project of it, with the scopes that it holds, never beyond its
// owner's role. Its secret is shown once, when it is made.
function Tokens() {
  const { t } = useTranslation();
  const workspaces = useSuspenseQuery(workspacesQuery).data;
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? "");
  const workspace = workspaces.find((w) => w.id === workspaceId);
  return (
    <Page title={t("tokens.title")} width="wide">
      <p>{t("tokens.intro")}</p>
      {workspace === undefined ? (
        <p>{t("tokens.no_workspace")}</p>
      ) : (
        <>
          {workspaces.length > 1 && (
            <div className="max-w-md">
              <SelectField
                label={t("tokens.workspace")}
                value={workspace.id}
                onChange={(event) => setWorkspaceId(event.currentTarget.value)}
              >
                {workspaces.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </SelectField>
            </div>
          )}
          <WorkspaceTokens key={workspace.id} workspace={workspace} />
        </>
      )}
    </Page>
  );
}

function WorkspaceTokens({ workspace }: { workspace: MembershipBody }) {
  const { t } = useTranslation();
  const projects = useQuery(projectsQuery(workspace.id));
  // A guest acts on the projects granted to them alone, so their tokens are
  // narrowed to one (§5.5).
  const guest = workspace.role === "guest";
  const [choice, setChoice] = useState("");
  const projectId = choice || (guest ? (projects.data?.[0]?.id ?? "") : "");
  const target: Target | undefined =
    projectId !== ""
      ? { kind: "project", projectId }
      : guest
        ? undefined
        : { kind: "workspace", workspaceId: workspace.id };
  const project = projects.data?.find((p) => p.id === projectId);
  return (
    <div className="space-y-6">
      <div className="max-w-md">
        <SelectField
          label={t("tokens.target")}
          hint={t("tokens.target_hint")}
          value={projectId}
          onChange={(event) => setChoice(event.currentTarget.value)}
        >
          {!guest && <option value="">{t("tokens.whole_workspace")}</option>}
          {(projects.data ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </SelectField>
      </div>
      {projects.isError && (
        <Alert message={refusalOf(projects.error, t).message} />
      )}
      {target === undefined ? (
        <p>{t("tokens.no_project")}</p>
      ) : (
        <TokenList
          key={JSON.stringify(target)}
          target={target}
          targetName={project?.name ?? workspace.name}
        />
      )}
    </div>
  );
}

function TokenList({
  target,
  targetName,
}: {
  target: Target;
  targetName: string;
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(tokensQuery(target));
  const tokens = list.data?.pages.flatMap((page) => page.items) ?? [];
  if (list.isError) {
    return <Alert message={refusalOf(list.error, t).message} />;
  }
  return (
    <section className="space-y-4" aria-labelledby="tokens-list">
      <div className="flex flex-wrap items-center gap-4">
        <h2 id="tokens-list" className="flex-1 text-lg font-semibold">
          {t("tokens.list_title", { target: targetName })}
        </h2>
        <CreateToken target={target} targetName={targetName} />
      </div>
      {list.isSuccess && tokens.length === 0 && (
        <p className="text-fg-muted">{t("tokens.empty")}</p>
      )}
      <ul className="space-y-3" aria-labelledby="tokens-list">
        {tokens.map((token) => (
          <TokenItem key={token.id} token={token} target={target} />
        ))}
      </ul>
      {list.hasNextPage && (
        <Button
          disabled={list.isFetchingNextPage}
          onClick={() => void list.fetchNextPage()}
        >
          {t("tokens.more")}
        </Button>
      )}
    </section>
  );
}

function TokenItem({ token, target }: { token: APITokenBody; target: Target }) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const nameId = useId();
  const locale = i18n.language;
  const [confirming, setConfirming] = useState(false);
  const revoke = useMutation({
    mutationFn: async () => {
      if (!session) {
        throw new Error("The page is signed out.");
      }
      const headers = csrfHeaders(session);
      if (target.kind === "workspace") {
        await api.call("revokeWorkspaceToken", {
          path: { workspace_id: target.workspaceId, token_id: token.id },
          headers,
        });
      } else {
        await api.call("revokeProjectToken", {
          path: { project_id: target.projectId, token_id: token.id },
          headers,
        });
      }
    },
    onSuccess: () => {
      setConfirming(false);
      return client.invalidateQueries({ queryKey: ["tokens", target] });
    },
    meta: { showsRefusals: true },
  });
  const expired =
    token.expires_at !== undefined &&
    Date.parse(token.expires_at) <= Date.now();
  // The confirmation stays open while the token is being revoked, and opens
  // again without the refusal of the attempt before.
  const onOpenChange = (next: boolean) => {
    if (!next && revoke.isPending) {
      return;
    }
    setConfirming(next);
    if (!next) {
      revoke.reset();
    }
  };
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {token.name}
      </p>
      <ul className="flex flex-wrap gap-2" aria-label={t("tokens.scopes")}>
        {token.scopes.map((scope) => (
          <li key={scope}>
            <code className="rounded bg-surface px-2 py-0.5 text-sm">
              {scope}
            </code>
          </li>
        ))}
      </ul>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("tokens.owner")}</dt>
        <dd>
          {token.user_id === session?.user_id
            ? t("tokens.owner_you")
            : t("tokens.owner_other")}
        </dd>
        <dt className="text-fg-muted">{t("tokens.created")}</dt>
        <dd>{formatDate(locale, new Date(token.created_at))}</dd>
        <dt className="text-fg-muted">{t("tokens.last_used")}</dt>
        <dd>
          {token.last_used_at === undefined
            ? t("tokens.never_used")
            : formatRelativeTime(locale, new Date(token.last_used_at))}
        </dd>
        <dt className="text-fg-muted">{t("tokens.expires")}</dt>
        <dd>
          {token.expires_at === undefined
            ? t("tokens.no_expiry")
            : expired
              ? t("tokens.expired", {
                  date: formatDate(locale, new Date(token.expires_at)),
                })
              : formatDate(locale, new Date(token.expires_at))}
        </dd>
      </dl>
      <AlertDialog.Root open={confirming} onOpenChange={onOpenChange}>
        <AlertDialog.Trigger asChild>
          <Button variant="danger" aria-describedby={nameId}>
            {t("tokens.revoke")}
          </Button>
        </AlertDialog.Trigger>
        <ConfirmFrame
          title={t("tokens.revoke_title", { name: token.name })}
          description={t("tokens.revoke_body")}
        >
          <Alert
            message={
              revoke.error === null
                ? undefined
                : refusalOf(revoke.error, t).message
            }
          />
          <div className="flex flex-wrap justify-end gap-2">
            <AlertDialog.Cancel asChild>
              <Button disabled={revoke.isPending}>{t("tokens.cancel")}</Button>
            </AlertDialog.Cancel>
            {/* Not an AlertDialog.Action, which would close the dialog
                before the request has answered. */}
            <Button
              variant="danger"
              disabled={revoke.isPending}
              onClick={() => revoke.mutate()}
            >
              {t("tokens.revoke_confirm")}
            </Button>
          </div>
        </ConfirmFrame>
      </AlertDialog.Root>
    </li>
  );
}

const tokenField = bodyField("name", "scopes", "expires_at");

/** Making a token of target, and showing its secret, once. */
function CreateToken({
  target,
  targetName,
}: {
  target: Target;
  targetName: string;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const create = useMutation({
    mutationFn: async (body: CreateTokenBody): Promise<CreatedAPITokenBody> => {
      if (!session) {
        throw new Error("The page is signed out.");
      }
      const headers = csrfHeaders(session);
      return target.kind === "workspace"
        ? (
            await api.call("createWorkspaceToken", {
              path: { workspace_id: target.workspaceId },
              headers,
              body,
            })
          ).body
        : (
            await api.call("createProjectToken", {
              path: { project_id: target.projectId },
              headers,
              body,
            })
          ).body;
    },
    onSuccess: () => client.invalidateQueries({ queryKey: ["tokens", target] }),
    meta: { showsRefusals: true },
  });
  const refusal =
    create.error === null ? undefined : refusalOf(create.error, t, tokenField);
  useFocusRefusal(form, create.error);
  const onOpenChange = (next: boolean) => {
    // The dialog stays open while the token is being made, whose secret
    // only it shows.
    if (!next && create.isPending) {
      return;
    }
    setOpen(next);
    if (!next) {
      // The secret goes with the dialog: it is never shown again.
      create.reset();
    }
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const days = Number(data.get("lifetime"));
    create.mutate({
      name: String(data.get("name")),
      scopes: scopes.filter((scope) => data.get(scope) === "on"),
      ...(days > 0
        ? { expires_at: new Date(Date.now() + days * 86_400_000).toISOString() }
        : {}),
    });
  };
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger asChild>
        <Button variant="primary">{t("tokens.create")}</Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("tokens.create_title", { target: targetName })}
        description={create.isSuccess ? undefined : t("tokens.create_intro")}
      >
        {create.isSuccess ? (
          <Secret token={create.data} onDone={() => onOpenChange(false)} />
        ) : (
          <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
            <Alert message={refusal?.message} />
            <TextField
              label={t("tokens.name")}
              hint={t("tokens.name_hint")}
              name="name"
              autoComplete="off"
              required
              error={refusal?.fields.name}
            />
            <ScopeChoice error={refusal?.fields.scopes} />
            <SelectField
              label={t("tokens.lifetime")}
              name="lifetime"
              defaultValue="90"
              error={refusal?.fields.expires_at}
            >
              {lifetimes.map((days) => (
                <option key={days} value={days}>
                  {days === 0
                    ? t("tokens.lifetime_never")
                    : t("tokens.lifetime_days", { count: days })}
                </option>
              ))}
            </SelectField>
            <div className="flex flex-wrap justify-end gap-2">
              <Dialog.Close asChild>
                <Button disabled={create.isPending}>
                  {t("tokens.cancel")}
                </Button>
              </Dialog.Close>
              <Button
                type="submit"
                variant="primary"
                disabled={create.isPending}
              >
                {t("tokens.create_submit")}
              </Button>
            </div>
          </form>
        )}
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The scopes to tick, each with what it grants. */
function ScopeChoice({ error }: { error?: string }) {
  const { t } = useTranslation();
  const errorId = useId();
  const grants: Record<Scope, string> = {
    "snapshot:read": t("tokens.scope.snapshot_read"),
    "project:read": t("tokens.scope.project_read"),
    "project:write": t("tokens.scope.project_write"),
    "snapshot:write": t("tokens.scope.snapshot_write"),
    "export:read": t("tokens.scope.export_read"),
  };
  return (
    <fieldset aria-describedby={error === undefined ? undefined : errorId}>
      <legend className="font-medium">{t("tokens.scopes")}</legend>
      {scopes.map((scope) => (
        <CheckboxField
          key={scope}
          name={scope}
          label={scope}
          hint={grants[scope]}
          defaultChecked={scope === "snapshot:read"}
        />
      ))}
      {error !== undefined && (
        <p id={errorId} className="text-sm text-danger">
          {error}
        </p>
      )}
    </fieldset>
  );
}

/** The secret of a token that was just made, which no answer holds again. */
function Secret({
  token,
  onDone,
}: {
  token: CreatedAPITokenBody;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState<boolean>();
  const area = useRef<HTMLDivElement>(null);
  const copy = async () => {
    setCopied(await copyText(token.secret, area.current ?? document.body));
  };
  return (
    <div ref={area} className="space-y-4">
      <p>{t("tokens.secret_intro", { name: token.name })}</p>
      <TextField
        label={t("tokens.secret")}
        value={token.secret}
        readOnly
        spellCheck={false}
        className="min-h-11 w-full rounded-md border border-border bg-surface px-3 font-mono text-fg"
        onFocus={(event) => event.currentTarget.select()}
      />
      <Status
        message={
          copied === undefined
            ? undefined
            : copied
              ? t("tokens.copied")
              : t("tokens.copy_failed")
        }
      />
      <div className="flex flex-wrap justify-end gap-2">
        <Button onClick={() => void copy()}>{t("tokens.copy")}</Button>
        <Button variant="primary" onClick={onDone}>
          {t("tokens.done")}
        </Button>
      </div>
    </div>
  );
}
