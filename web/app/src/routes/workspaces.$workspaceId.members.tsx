import {
  ApiError,
  type CreateInviteBody,
  type InviteBody,
  type MemberBody,
  type ProjectBody,
} from "@storycreep/shared/api";
import { formatDate } from "@storycreep/shared/i18n";
import {
  type Query,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, notFound, useNavigate } from "@tanstack/react-router";
import { AlertDialog, Dialog } from "radix-ui";
import { type FormEvent, type ReactNode, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, instanceQuery, notFoundBy, revoking, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import {
  actsOn,
  type Role,
  roleLabel,
  roleName,
  rolesGivenBy,
} from "../auth/roles";
import { csrfHeaders } from "../auth/session";
import { found } from "../shell/found";
import { Button, MoreButton } from "../ui/Button";
import { CopyField } from "../ui/CopyField";
import {
  ConfirmButtons,
  ConfirmFrame,
  closesBy,
  closesUnlessPending,
  DialogFrame,
  FormButtons,
  focusBackTo,
} from "../ui/Dialog";
import {
  GroupField,
  RadioField,
  SelectField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert, Page, RefusalAlert, Status } from "../ui/Page";
import {
  invitesQuery,
  managesWorkspace,
  membersQuery,
  usageQuery,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";
import { LimitNote } from "../workspace/Usage";

export const Route = createFileRoute("/workspaces/$workspaceId/members")({
  loader: async ({ context: { queryClient }, params }) => {
    const workspace = await found(
      queryClient.ensureQueryData(workspaceQuery(params.workspaceId)),
    );
    // A guest, whose role in the workspace is none, may not list its
    // members, which the server answers as for a workspace that does not
    // exist, so the page asks for no list that it knows to be refused.
    if (workspace.role === "guest") {
      throw notFound();
    }
    // A user whom another removed from the workspace since the page read it
    // is answered alike.
    await found(
      Promise.all([
        queryClient.ensureInfiniteQueryData(membersQuery(workspace.id)),
        managesWorkspace(workspace.role)
          ? queryClient.ensureInfiniteQueryData(invitesQuery(workspace.id))
          : undefined,
      ]),
    );
  },
  component: Members,
});

// A workspace's members and their roles, which every member with a role in
// it sees, and its open invitations, which, with the members' addresses,
// its owners and admins alone see, invite, and remove, as they change the
// members' roles, up to admin; only an owner gives the role owner, or
// changes or removes an owner's (PRD FR-TEN-02, FR-TEN-03, §5.6).
function Members() {
  const { t } = useTranslation();
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  return (
    <Page
      title={t("members.title")}
      documentTitle={t("workspace.page_title", {
        page: t("members.title"),
        workspace: workspace.name,
      })}
      width="wide"
    >
      <MemberList workspace={workspace} />
      {managesWorkspace(workspace.role) && <Invites workspace={workspace} />}
    </Page>
  );
}

function MemberList({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const list = useSuspenseInfiniteQuery(membersQuery(workspace.id));
  const members = list.data.pages.flatMap((page) => page.items);
  return (
    <section className="space-y-4" aria-labelledby="members-list">
      <h2 id="members-list" className="text-lg font-semibold">
        {t("members.list_title")}
      </h2>
      <ul className="space-y-3" aria-labelledby="members-list">
        {members.map((member) => (
          <MemberItem key={member.id} member={member} workspace={workspace} />
        ))}
      </ul>
      <MoreButton list={list}>{t("members.more")}</MoreButton>
    </section>
  );
}

/**
 * Reports whether a member whose role is actor's changes the role of a
 * member whose role is role, or removes them: an owner or an admin does,
 * but only an owner an owner's (actsOn).
 */
function managesMember(actor: Role, role: Role): boolean {
  return managesWorkspace(actor) && actsOn(actor, role);
}

function MemberItem({
  member,
  workspace,
}: {
  member: MemberBody;
  workspace: Workspace;
}) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const nameId = useId();
  const you = member.user_id === session?.user_id;
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {you
          ? t("members.you", { name: member.display_name })
          : member.display_name}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        {member.email !== undefined && (
          <>
            <dt className="text-fg-muted">{t("members.email")}</dt>
            <dd className="break-words">{member.email}</dd>
          </>
        )}
        <dt className="text-fg-muted">{t("members.role")}</dt>
        <dd>{roleLabel(t, member.role)}</dd>
        <dt className="text-fg-muted">{t("members.joined")}</dt>
        <dd>{formatDate(i18n.language, new Date(member.joined_at))}</dd>
      </dl>
      {managesMember(workspace.role, member.role) && (
        <div className="flex flex-wrap gap-2">
          <ChangeRole member={member} workspace={workspace} nameId={nameId} />
          <RemoveMember
            member={member}
            workspace={workspace}
            nameId={nameId}
            you={you}
          />
        </div>
      )}
    </li>
  );
}

/** The dialog that changes a member's role. */
function ChangeRole({
  member,
  workspace,
  nameId,
}: {
  member: MemberBody;
  workspace: Workspace;
  nameId: string;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const change = useMutation({
    mutationFn: (role: Role) =>
      api.call("changeMemberRole", {
        path: { workspace_id: workspace.id, membership_id: member.id },
        headers: csrfHeaders(session),
        body: { role },
      }),
    onSuccess: async (_answer, role) => {
      setOpen(false);
      // The user's own role may be the one that changed, and with it what
      // the workspace's pages show them. A role that no longer manages the
      // workspace may not list its invitations, which the page stops
      // showing, so they are not read again, which would be refused.
      const demoted =
        member.user_id === session?.user_id && !managesWorkspace(role);
      const invites = invitesQuery(workspace.id).queryKey;
      await client.invalidateQueries({
        queryKey: ["workspaces"],
        predicate: (query: Query) =>
          !(demoted && invites.every((part, i) => query.queryKey[i] === part)),
      });
    },
  });
  const refusal = refusalOf(change.error, t, bodyField("role"));
  useFocusRefusal(form, change.error);
  // A role that the page does not offer, as a guest's, stays a choice while
  // it is the member's.
  const offered = rolesGivenBy(workspace.role);
  const roles = offered.includes(member.role)
    ? offered
    : [...offered, member.role];
  const onOpenChange = closesUnlessPending(setOpen, change);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    change.mutate(
      String(new FormData(event.currentTarget).get("role")) as Role,
    );
  };
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger asChild>
        <Button aria-haspopup="dialog" aria-describedby={nameId}>
          {t("members.change_role")}
        </Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("members.change_role_title", { name: member.display_name })}
        description={t("members.roles_hint")}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <SelectField
            label={t("members.role")}
            name="role"
            defaultValue={member.role}
            error={refusal?.fields.role}
          >
            {roles.map((role) => (
              <option key={role} value={role}>
                {roleLabel(t, role)}
              </option>
            ))}
          </SelectField>
          <FormButtons
            cancel={t("members.cancel")}
            submit={t("members.change_role_submit")}
            pending={change.isPending}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}

/**
 * The confirmation that removes a member from the workspace, and with them
 * their project grants there, and the open invitations that they made or
 * hold the link of (PRD FR-TEN-02). A user who removes themselves goes to
 * the project list, of a workspace that they are still a member of.
 */
function RemoveMember({
  member,
  workspace,
  nameId,
  you,
}: {
  member: MemberBody;
  workspace: Workspace;
  nameId: string;
  you: boolean;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  const remove = useMutation({
    // A member whom another admin has removed since the page listed them is
    // removed, as asked.
    mutationFn: () =>
      revoking(
        api.call("removeMember", {
          path: { workspace_id: workspace.id, membership_id: member.id },
          headers: csrfHeaders(session),
        }),
      ),
    onSuccess: async () => {
      setConfirming(false);
      if (you) {
        // The workspace is no longer the user's, so the page goes before
        // what it read of the workspace is read again, and then drops what
        // it read of the workspace and of its projects, so that a page of
        // them that the user goes back to asks the server, which answers
        // as for one that does not exist, rather than shows what it read.
        await navigate({ to: "/projects", replace: true });
        client.removeQueries({ queryKey: ["workspaces", workspace.id] });
        client.removeQueries({
          predicate: (query: Query) =>
            query.queryKey[0] === "projects" &&
            client.getQueryData<ProjectBody>(["projects", query.queryKey[1]])
              ?.workspace_id === workspace.id,
        });
      }
      await client.invalidateQueries({ queryKey: ["workspaces"] });
    },
  });
  return (
    <AlertDialog.Root
      open={confirming}
      onOpenChange={closesUnlessPending(setConfirming, remove)}
    >
      <AlertDialog.Trigger asChild>
        <Button variant="danger" aria-describedby={nameId}>
          {t("members.remove")}
        </Button>
      </AlertDialog.Trigger>
      <ConfirmFrame
        title={
          you
            ? t("members.leave_title", { workspace: workspace.name })
            : t("members.remove_title", { name: member.display_name })
        }
        description={you ? t("members.leave_body") : t("members.remove_body")}
      >
        <Alert message={refusalOf(remove.error, t)?.message} />
        <ConfirmButtons
          cancel={t("members.cancel")}
          confirm={
            you ? t("members.leave_confirm") : t("members.remove_confirm")
          }
          pending={remove.isPending}
          onConfirm={() => remove.mutate()}
        />
      </ConfirmFrame>
    </AlertDialog.Root>
  );
}

/** A link of an invitation that the server gave the page once, and whom it is for. */
interface ShownLink {
  url: string;
  /** Whom the invitation is for, or undefined for whoever holds the link. */
  email: string | undefined;
  /** The control that the link was asked for from, which gets the focus back. */
  opener: HTMLElement;
}

/** The open invitations of a workspace, which its owners and admins see, make, send anew, and revoke. */
function Invites({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const list = useSuspenseInfiniteQuery(invitesQuery(workspace.id));
  const invites = list.data.pages.flatMap((page) => page.items);
  // The link of an invitation sent anew is the page's to show, rather than
  // its item's, so that it stays, with the link that the server keeps no
  // copy of, whatever becomes of the list behind it.
  const [shown, setShown] = useState<ShownLink>();
  // So is what became of an invitation that was no longer open when the
  // user sent it anew or revoked it, which goes from the list with its item.
  const [gone, setGone] = useState<string>();
  return (
    <section className="space-y-4" aria-labelledby="invites-list">
      <div className="flex flex-wrap items-center gap-4">
        <h2 id="invites-list" className="flex-1 text-lg font-semibold">
          {t("invites.title")}
        </h2>
        <Invite workspace={workspace} />
      </div>
      <Alert message={gone} />
      {invites.length === 0 && (
        <p className="text-fg-muted">{t("invites.empty")}</p>
      )}
      <ul className="space-y-3" aria-labelledby="invites-list">
        {invites.map((invite) => (
          <InviteItem
            key={invite.id}
            invite={invite}
            workspace={workspace}
            onLink={setShown}
            onGone={setGone}
          />
        ))}
      </ul>
      <MoreButton list={list}>{t("invites.more")}</MoreButton>
      {shown !== undefined && (
        <LinkDialog shown={shown} onClose={() => setShown(undefined)} />
      )}
    </section>
  );
}

function InviteItem({
  invite,
  workspace,
  onLink,
  onGone,
}: {
  invite: InviteBody;
  workspace: Workspace;
  onLink: (shown: ShownLink) => void;
  /** Says what became of the invitation, once it is no longer open. */
  onGone: (message: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const instance = useSuspenseQuery(instanceQuery).data;
  const client = useQueryClient();
  const nameId = useId();
  const locale = i18n.language;
  const [confirming, setConfirming] = useState(false);
  const who = invite.email ?? t("invites.anyone");
  const readAgain = () =>
    client.invalidateQueries({ queryKey: invitesQuery(workspace.id).queryKey });
  // An invitation that has been accepted or revoked elsewhere since the page
  // listed it, or that the workspace no longer has, is no longer open, and
  // one to an address that belongs to a member by now has made the list of
  // members old too: the page reads both again, rather than keep offering
  // what every attempt again would be refused for, or leave out whoever has
  // joined, and says what became of an invitation that goes with its item.
  const listsAgain = (error: Error) => {
    const code = error instanceof ApiError ? error.error?.code : undefined;
    const gone =
      code === "invite_accepted" ||
      code === "invite_revoked" ||
      notFoundBy(error);
    if (gone) {
      onGone(t("invites.gone", { who }));
    }
    if (gone || code === "already_member") {
      return Promise.all([
        readAgain(),
        client.invalidateQueries({
          queryKey: membersQuery(workspace.id).queryKey,
        }),
      ]);
    }
  };
  const resend = useMutation({
    mutationFn: async (_opener: HTMLElement) =>
      (
        await api.call("resendInvite", {
          path: { workspace_id: workspace.id, invite_id: invite.id },
          headers: csrfHeaders(session),
        })
      ).body,
    onSuccess: (sent, opener) => {
      if (sent.url !== undefined) {
        onLink({ url: sent.url, email: sent.email, opener });
      }
      return readAgain();
    },
    onError: listsAgain,
  });
  const revoke = useMutation({
    // An invitation that has been revoked elsewhere since the page listed
    // it is revoked, as asked.
    mutationFn: () =>
      revoking(
        api.call("revokeInvite", {
          path: { workspace_id: workspace.id, invite_id: invite.id },
          headers: csrfHeaders(session),
        }),
      ),
    onSuccess: () => {
      setConfirming(false);
      return readAgain();
    },
    onError: listsAgain,
  });
  // Only an owner sends an invitation as owner anew (PRD FR-TEN-03 AC4).
  const resends = actsOn(workspace.role, invite.role);
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {who}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("invites.role")}</dt>
        <dd>{roleLabel(t, invite.role)}</dd>
        <dt className="text-fg-muted">{t("invites.expires")}</dt>
        <dd>
          {invite.status === "expired"
            ? t("invites.expired", {
                date: formatDate(locale, new Date(invite.expires_at)),
              })
            : formatDate(locale, new Date(invite.expires_at))}
        </dd>
        {invite.kind === "email" && instance.email_configured && (
          <>
            <dt className="text-fg-muted">{t("invites.emailed")}</dt>
            <dd>
              {invite.sent_at === undefined
                ? t("invites.not_emailed")
                : formatDate(locale, new Date(invite.sent_at))}
            </dd>
          </>
        )}
      </dl>
      <Status
        message={
          resend.isSuccess && resend.data.url === undefined
            ? t("invites.resent", { email: who })
            : undefined
        }
      />
      <Alert
        message={
          refusalOf(resend.error, t, undefined, {
            already_member: t("invites.error.already_member"),
          })?.message
        }
      />
      <div className="flex flex-wrap gap-2">
        {resends && (
          <Button
            aria-describedby={nameId}
            disabled={resend.isPending}
            onClick={(event) => resend.mutate(event.currentTarget)}
          >
            {t("invites.resend")}
          </Button>
        )}
        <AlertDialog.Root
          open={confirming}
          onOpenChange={closesUnlessPending(setConfirming, revoke)}
        >
          <AlertDialog.Trigger asChild>
            <Button variant="danger" aria-describedby={nameId}>
              {t("invites.revoke")}
            </Button>
          </AlertDialog.Trigger>
          <ConfirmFrame
            title={t("invites.revoke_title", { who })}
            description={t("invites.revoke_body")}
          >
            <Alert message={refusalOf(revoke.error, t)?.message} />
            <ConfirmButtons
              cancel={t("invites.cancel")}
              confirm={t("invites.revoke_confirm")}
              pending={revoke.isPending}
              onConfirm={() => revoke.mutate()}
            />
          </ConfirmFrame>
        </AlertDialog.Root>
      </div>
    </li>
  );
}

const inviteField = bodyField("email", "role");

/**
 * The button that invites someone, in a dialog: an email address, or
 * whoever holds the link, with a role, which the dialog says how many more
 * members the workspace may have (PRD FR-ENT-02), and, once the server has
 * made the invitation, its link, which only this answer holds, where the
 * instance does not email it: for a link invitation, and for every one on
 * an instance that sends no email.
 */
function Invite({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"email" | "link">("email");
  const form = useRef<HTMLFormElement>(null);
  const usage = useQuery({ ...usageQuery(workspace.id), enabled: open });
  const create = useMutation({
    mutationFn: async (body: CreateInviteBody) =>
      (
        await api.call("createInvite", {
          path: { workspace_id: workspace.id },
          headers: csrfHeaders(session),
          body,
        })
      ).body,
    onSuccess: () =>
      client.invalidateQueries({
        queryKey: invitesQuery(workspace.id).queryKey,
      }),
  });
  const refusal = refusalOf(create.error, t, inviteField, {
    already_member: t("invites.error.already_member"),
  });
  useFocusRefusal(form, create.error);
  const onOpenChange = closesUnlessPending(setOpen, create, () =>
    setKind("email"),
  );
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const role = String(data.get("role")) as Role;
    create.mutate(
      kind === "email" ? { email: String(data.get("email")), role } : { role },
    );
  };
  const made = create.data;
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger asChild>
        <Button variant="primary" aria-haspopup="dialog">
          {t("invites.create")}
        </Button>
      </Dialog.Trigger>
      <DialogFrame
        title={t("invites.create_title", { workspace: workspace.name })}
        // A link that the dialog shows is shown only this once.
        closesOutside={made?.url === undefined}
      >
        {made === undefined ? (
          <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
            <LimitNote usage={usage.data} limit="members" />
            <RefusalAlert refusal={refusal} />
            <GroupField legend={t("invites.kind")}>
              <RadioField
                name="kind"
                label={t("invites.kind_email")}
                checked={kind === "email"}
                onChange={() => setKind("email")}
              />
              <RadioField
                name="kind"
                label={t("invites.kind_link")}
                hint={t("invites.kind_link_hint")}
                checked={kind === "link"}
                onChange={() => setKind("link")}
              />
            </GroupField>
            {kind === "email" && (
              <TextField
                label={t("invites.email")}
                name="email"
                type="email"
                autoComplete="off"
                required
                error={refusal?.fields.email}
              />
            )}
            <SelectField
              label={t("invites.role")}
              hint={t("members.roles_hint")}
              name="role"
              defaultValue="editor"
              error={refusal?.fields.role}
            >
              {rolesGivenBy(workspace.role).map((role) => (
                <option key={role} value={role}>
                  {roleLabel(t, role)}
                </option>
              ))}
            </SelectField>
            <FormButtons
              cancel={t("invites.cancel")}
              submit={t("invites.create_submit")}
              pending={create.isPending}
            />
          </form>
        ) : made.url === undefined ? (
          <div className="space-y-4">
            <Status
              message={t("invites.emailing", {
                email: made.email ?? "",
                role: roleName(t, made.role),
              })}
            />
            <div className="flex justify-end">
              <Dialog.Close asChild>
                <Button variant="primary">{t("invites.done")}</Button>
              </Dialog.Close>
            </div>
          </div>
        ) : (
          <InviteLink url={made.url} email={made.email}>
            <Dialog.Close asChild>
              <Button variant="primary">{t("invites.done")}</Button>
            </Dialog.Close>
          </InviteLink>
        )}
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The link of an invitation, which the server keeps no copy of, to copy and give. */
function InviteLink({
  url,
  email,
  children,
}: {
  url: string;
  email: string | undefined;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <p>
        {email === undefined
          ? t("invites.link_intro_anyone")
          : t("invites.link_intro", { email })}
      </p>
      <CopyField
        label={t("invites.link")}
        value={url}
        messages={{
          copy: t("invites.copy"),
          copied: t("invites.copied"),
          failed: t("invites.copy_failed"),
        }}
      >
        {children}
      </CopyField>
    </div>
  );
}

/** The dialog of the new link of an invitation sent anew. */
function LinkDialog({
  shown,
  onClose,
}: {
  shown: ShownLink;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose)}>
      <DialogFrame
        title={t("invites.link_title")}
        closesOutside={false}
        onCloseAutoFocus={focusBackTo(shown.opener)}
      >
        <InviteLink url={shown.url} email={shown.email}>
          <Button variant="primary" onClick={onClose}>
            {t("invites.done")}
          </Button>
        </InviteLink>
      </DialogFrame>
    </Dialog.Root>
  );
}
