import type {
  APITokenBody,
  CreatedAPITokenBody,
  CreateTokenBody,
  MembershipBody,
  PageAPITokenBody,
  PageMembershipBody,
  PageProjectBody,
} from "@storycreep/shared/api";
import { formatDate, formatRelativeTime } from "@storycreep/shared/i18n";
import {
  type QueryClient,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { AlertDialog, Dialog } from "radix-ui";
import { type FormEvent, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  api,
  askedSparingly,
  type PageQuery,
  pagedQuery,
  revoking,
  sessionQuery,
} from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { copyText } from "../errors/copy";
import { focusContentIfLost } from "../shell/focus";
import { Button, MoreButton } from "../ui/Button";
import { ConfirmFrame, DialogFrame } from "../ui/Dialog";
import {
  CheckboxField,
  GroupField,
  SelectField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/account/tokens")({
  loader: ({ context }) =>
    context.queryClient.ensureInfiniteQueryData(workspacesQuery),
  component: Tokens,
});

/**
 * The workspaces that the user is a member of, a page at a time: the page
 * shows itself once it has the first, and the rest come as the user asks
 * for them, as the tokens do.
 */
const workspacesQuery = pagedQuery(
  ["workspaces"],
  async (query, signal): Promise<PageMembershipBody> =>
    (await api.call("listWorkspaces", { query }, { signal })).body,
);

/** The projects of a workspace that the user can see, a page at a time. */
function projectsQuery(workspaceId: string) {
  return pagedQuery(
    ["workspaces", workspaceId, "projects"],
    async (query, signal): Promise<PageProjectBody> =>
      (
        await api.call(
          "listProjects",
          { path: { workspace_id: workspaceId }, query },
          { signal },
        )
      ).body,
  );
}

/** What a token acts on: a whole workspace, or one project of it. */
type Target =
  | { kind: "workspace"; workspaceId: string }
  | { kind: "project"; projectId: string };

/**
 * A token that the user is making: of what, and the control that they made
 * it from, which gets the focus back once the dialog closes.
 */
interface Making {
  target: Target;
  /** The name of target, which the dialog's title says. */
  targetName: string;
  opener: HTMLElement;
}

/** The headers of a request that changes a target's tokens. */
type ChangeHeaders = ReturnType<typeof csrfHeaders>;

/** The requests of a target's tokens, which the server has of each kind. */
interface TokenRequests {
  list: (query: PageQuery, signal: AbortSignal) => Promise<PageAPITokenBody>;
  create: (
    body: CreateTokenBody,
    headers: ChangeHeaders,
  ) => Promise<CreatedAPITokenBody>;
  revoke: (tokenId: string, headers: ChangeHeaders) => Promise<void>;
}

/** Returns the requests of the tokens of target, a workspace or a project. */
function tokenRequests(target: Target): TokenRequests {
  if (target.kind === "workspace") {
    const workspace = { workspace_id: target.workspaceId };
    return {
      list: async (query, signal) =>
        (
          await api.call(
            "listWorkspaceTokens",
            { path: workspace, query },
            { signal },
          )
        ).body,
      create: async (body, headers) =>
        (
          await api.call("createWorkspaceToken", {
            path: workspace,
            headers,
            body,
          })
        ).body,
      revoke: async (tokenId, headers) => {
        await api.call("revokeWorkspaceToken", {
          path: { ...workspace, token_id: tokenId },
          headers,
        });
      },
    };
  }
  const project = { project_id: target.projectId };
  return {
    list: async (query, signal) =>
      (
        await api.call(
          "listProjectTokens",
          { path: project, query },
          { signal },
        )
      ).body,
    create: async (body, headers) =>
      (await api.call("createProjectToken", { path: project, headers, body }))
        .body,
    revoke: async (tokenId, headers) => {
      await api.call("revokeProjectToken", {
        path: { ...project, token_id: tokenId },
        headers,
      });
    },
  };
}

/**
 * The tokens of a target, a page at a time, which the server counts among
 * the requests about credentials: the page reads them again once it has
 * made or revoked one (readTokensAgain), and else at most once a minute, as
 * when a target is chosen again, whether they were read or refused, while
 * it shows their first page alone (askedSparingly).
 */
function tokensQuery(target: Target) {
  return pagedQuery(["tokens", target], tokenRequests(target).list, {
    // A role that may not see the tokens is refused, which the page shows.
    meta: { showsRefusals: true },
    ...askedSparingly,
  });
}

/**
 * Reads the tokens of target again, as once one has been made or revoked:
 * the first page alone, since the server counts each page among the
 * requests about credentials, rather than each that the user had asked
 * for, which a list of more than a page would spend the account's minute
 * on.
 */
function readTokensAgain(client: QueryClient, target: Target): Promise<void> {
  const { queryKey } = tokensQuery(target);
  client.setQueryData(
    queryKey,
    (data) =>
      data && {
        pages: data.pages.slice(0, 1),
        pageParams: data.pageParams.slice(0, 1),
      },
  );
  return client.invalidateQueries({ queryKey });
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
  const list = useSuspenseInfiniteQuery(workspacesQuery);
  const workspaces = list.data.pages.flatMap((page) => page.items);
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? "");
  // The dialog that makes a token is the page's, rather than its list's, so
  // that it stays, with the secret that it shows once, whatever becomes of
  // what is behind it: a list that then fails to be read, or a target that
  // is no longer listed, as a project that another tab put in the trash,
  // whose list goes with it. It makes the token of the target that it was
  // opened for.
  const [making, setMaking] = useState<Making>();
  // The first, until the user chooses another, or once the one chosen is no
  // longer listed, as when the user has left it.
  const workspace =
    workspaces.find((w) => w.id === workspaceId) ?? workspaces[0];
  return (
    <Page title={t("tokens.title")} width="wide">
      <p>{t("tokens.intro")}</p>
      {workspace === undefined ? (
        <p>{t("tokens.no_workspace")}</p>
      ) : (
        <>
          {(workspaces.length > 1 || list.hasNextPage) && (
            <div className="max-w-md space-y-2">
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
              <MoreButton list={list}>{t("tokens.more_workspaces")}</MoreButton>
            </div>
          )}
          <WorkspaceTokens
            key={workspace.id}
            workspace={workspace}
            onMake={setMaking}
          />
        </>
      )}
      {making !== undefined && (
        <CreateToken making={making} onClose={() => setMaking(undefined)} />
      )}
    </Page>
  );
}

/** What opens the page's dialog that makes a token. */
type OnMake = (making: Making) => void;

function WorkspaceTokens({
  workspace,
  onMake,
}: {
  workspace: MembershipBody;
  onMake: OnMake;
}) {
  const { t } = useTranslation();
  const projects = useInfiniteQuery(projectsQuery(workspace.id));
  const loaded = projects.data?.pages.flatMap((page) => page.items) ?? [];
  // A guest acts on the projects granted to them alone, so their tokens are
  // narrowed to one (§5.5).
  const guest = workspace.role === "guest";
  const [choice, setChoice] = useState("");
  // The project chosen, while it is listed, as Tokens keeps the workspace
  // chosen: once it is not, as once it has gone to the trash, or out of the
  // pages read, the whole workspace, or a guest's first project.
  const project =
    loaded.find((p) => p.id === choice) ?? (guest ? loaded[0] : undefined);
  const projectId = project?.id ?? "";
  const target: Target | undefined =
    project !== undefined
      ? { kind: "project", projectId: project.id }
      : guest
        ? undefined
        : { kind: "workspace", workspaceId: workspace.id };
  return (
    <div className="space-y-6">
      <div className="max-w-md space-y-2">
        <SelectField
          label={t("tokens.target")}
          hint={t("tokens.target_hint")}
          value={projectId}
          onChange={(event) => setChoice(event.currentTarget.value)}
        >
          {!guest && <option value="">{t("tokens.whole_workspace")}</option>}
          {loaded.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </SelectField>
        <MoreButton list={projects}>{t("tokens.more_projects")}</MoreButton>
      </div>
      {projects.isError && (
        <Alert message={refusalOf(projects.error, t)?.message} />
      )}
      {target === undefined ? (
        // A guest's projects that are still loading, or that could not be
        // read, may hold one.
        projects.isSuccess && <p>{t("tokens.no_project")}</p>
      ) : (
        <TokenList
          key={JSON.stringify(target)}
          target={target}
          targetName={project?.name ?? workspace.name}
          onMake={onMake}
        />
      )}
    </div>
  );
}

function TokenList({
  target,
  targetName,
  onMake,
}: {
  target: Target;
  targetName: string;
  onMake: OnMake;
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(tokensQuery(target));
  const tokens = list.data?.pages.flatMap((page) => page.items) ?? [];
  // A list that was never read shows why alone, as to a role that may not
  // see the tokens, nor make one. One that fails to be read again, as once a
  // token has been made, shows why with what it had.
  if (list.isError && list.data === undefined) {
    return <Alert message={refusalOf(list.error, t)?.message} />;
  }
  return (
    <section className="space-y-4" aria-labelledby="tokens-list">
      <div className="flex flex-wrap items-center gap-4">
        <h2 id="tokens-list" className="flex-1 text-lg font-semibold">
          {t("tokens.list_title", { target: targetName })}
        </h2>
        <Button
          variant="primary"
          aria-haspopup="dialog"
          onClick={(event) =>
            onMake({ target, targetName, opener: event.currentTarget })
          }
        >
          {t("tokens.create")}
        </Button>
      </div>
      <Alert message={refusalOf(list.error, t)?.message} />
      {list.isSuccess && tokens.length === 0 && (
        <p className="text-fg-muted">{t("tokens.empty")}</p>
      )}
      <ul className="space-y-3" aria-labelledby="tokens-list">
        {tokens.map((token) => (
          <TokenItem key={token.id} token={token} target={target} />
        ))}
      </ul>
      <MoreButton list={list}>{t("tokens.more")}</MoreButton>
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
    // A token that has been revoked elsewhere since the page listed it, as
    // by another admin, is revoked, as asked.
    mutationFn: () =>
      revoking(tokenRequests(target).revoke(token.id, csrfHeaders(session))),
    onSuccess: () => {
      setConfirming(false);
      return readTokensAgain(client, target);
    },
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
          <Alert message={refusalOf(revoke.error, t)?.message} />
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

/**
 * The dialog that makes a token of making's target, and shows its secret,
 * once: the secret goes with the dialog, and is never shown again.
 */
function CreateToken({
  making: { target, targetName, opener },
  onClose,
}: {
  making: Making;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const create = useMutation({
    mutationFn: (body: CreateTokenBody) =>
      tokenRequests(target).create(body, csrfHeaders(session)),
    // The secret shows at once, while the list is read again.
    onSuccess: () => {
      void readTokensAgain(client, target);
    },
  });
  const refusal = refusalOf(create.error, t, tokenField);
  useFocusRefusal(form, create.error);
  const onOpenChange = (next: boolean) => {
    // The dialog stays open while the token is being made, whose secret
    // only it shows.
    if (!next && !create.isPending) {
      onClose();
    }
  };
  // The control that opened the dialog gets the focus back, or the page's
  // content, once that control has gone with its list.
  const onCloseAutoFocus = (event: Event) => {
    event.preventDefault();
    if (opener.isConnected) {
      opener.focus();
    } else {
      focusContentIfLost(document);
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
    <Dialog.Root open onOpenChange={onOpenChange}>
      <DialogFrame
        title={t("tokens.create_title", { target: targetName })}
        description={create.isSuccess ? undefined : t("tokens.create_intro")}
        closesOutside={!create.isSuccess}
        onCloseAutoFocus={onCloseAutoFocus}
      >
        {create.isSuccess ? (
          <Secret token={create.data} onDone={onClose} />
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
  const grants: Record<Scope, string> = {
    "snapshot:read": t("tokens.scope.snapshot_read"),
    "project:read": t("tokens.scope.project_read"),
    "project:write": t("tokens.scope.project_write"),
    "snapshot:write": t("tokens.scope.snapshot_write"),
    "export:read": t("tokens.scope.export_read"),
  };
  return (
    <GroupField legend={t("tokens.scopes")} error={error}>
      {scopes.map((scope) => (
        <CheckboxField
          key={scope}
          name={scope}
          label={scope}
          hint={grants[scope]}
          defaultChecked={scope === "snapshot:read"}
        />
      ))}
    </GroupField>
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
