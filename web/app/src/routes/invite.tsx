import {
  type AccountBody,
  ApiError,
  type InvitePreviewBody,
  type MembershipBody,
  type SignedInBody,
} from "@storycreep/shared/api";
import { formatDate } from "@storycreep/shared/i18n";
import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, api, instanceQuery, sessionQuery } from "../api";
import {
  type Mismatch,
  minPasswordLength,
  passwordMismatch,
  registrationField,
  signInRegistered,
} from "../auth/forms";
import { useFragmentToken } from "../auth/fragment";
import { refusalOf } from "../auth/refusal";
import { roleName } from "../auth/roles";
import { csrfHeaders, signedIn } from "../auth/session";
import { useSignOut } from "../auth/signOut";
import { Button } from "../ui/Button";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/invite")({
  component: Invite,
});

/** What the holder of the invitation's link sees of it. */
function inviteQuery(token: string) {
  return queryOptions({
    queryKey: ["invite", token],
    // A POST, so that the token travels in the body, never in a URL.
    queryFn: async ({ signal }): Promise<InvitePreviewBody> =>
      (await api.call("lookupInvite", { body: { token } }, { signal })).body,
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
    meta: { showsRefusals: true },
  });
}

/** Accepts the invitation of token in session, and returns the membership. */
async function accept(
  token: string,
  session: SignedInBody,
): Promise<MembershipBody> {
  return (
    await api.call("acceptInvite", {
      headers: csrfHeaders(session),
      body: { token },
    })
  ).body;
}

// The page of an invitation's link (PRD FR-TEN-02, plan note N27), whose
// token is in the URL's fragment: it shows the invitation, and accepts it as
// the signed-in user, or as one that signs in or registers with it first,
// where the signup policy lets an invitation register an account.
function Invite() {
  const { t } = useTranslation();
  const token = useFragmentToken();
  if (token === undefined) {
    return (
      <Page title={t("invite.title")}>
        <p>{t("invite.no_token")}</p>
      </Page>
    );
  }
  return <InviteOf token={token} />;
}

function InviteOf({ token }: { token: string }) {
  const { t, i18n } = useTranslation();
  const lookup = useQuery(inviteQuery(token));
  const session = useQuery(sessionQuery).data;
  const [joined, setJoined] = useState<MembershipBody>();
  // The refusal of an acceptance that signed in first, which the signed-in
  // step that follows shows, since the step that made it is gone.
  const [refused, setRefused] = useState<unknown>(null);
  // Once joined, the page shows the membership, whatever it reads of the
  // invitation since, which its acceptance used: signing in with it drops
  // the page's queries, the invitation's too, which then answers 410.
  if (joined !== undefined) {
    return (
      <Page title={t("invite.joined_title", { workspace: joined.name })}>
        <Status
          message={t("invite.joined", {
            workspace: joined.name,
            role: roleName(t, joined.role),
          })}
        />
        <p>
          <TextLink to="/">{t("invite.home")}</TextLink>
        </p>
      </Page>
    );
  }
  if (lookup.isPending) {
    return (
      <Page title={t("invite.title")}>
        <Status message={t("invite.loading")} />
      </Page>
    );
  }
  if (lookup.isError) {
    return (
      <Page title={t("invite.title")}>
        <Alert message={refusalOf(lookup.error, t).message} />
        <p>
          <TextLink to="/">{t("invite.home")}</TextLink>
        </p>
      </Page>
    );
  }
  const invite = lookup.data;
  const workspace = invite.workspace_name;
  return (
    <Page title={t("invite.title_of", { workspace })}>
      <div className="space-y-2">
        <p>
          {t("invite.summary", { workspace, role: roleName(t, invite.role) })}
        </p>
        {invite.email !== undefined && (
          <p>{t("invite.for_email", { email: invite.email })}</p>
        )}
        <p className="text-fg-muted">
          {t("invite.expires", {
            date: formatDate(i18n.language, new Date(invite.expires_at)),
          })}
        </p>
      </div>
      {session ? (
        <AcceptSignedIn
          token={token}
          invite={invite}
          session={session}
          refused={refused}
          onJoined={setJoined}
        />
      ) : (
        <AcceptSignedOut
          token={token}
          invite={invite}
          onJoined={setJoined}
          onRefused={setRefused}
        />
      )}
    </Page>
  );
}

interface AcceptProps {
  token: string;
  invite: InvitePreviewBody;
  onJoined: (membership: MembershipBody) => void;
}

/**
 * Accepting the invitation as the signed-in user, which shows refused, the
 * refusal of an acceptance that signed in first, until it accepts again.
 * Signing out of an account that the invitation is not for stays on the
 * invitation, to sign in with the one that it is for.
 */
function AcceptSignedIn({
  token,
  invite,
  session,
  refused,
  onJoined,
}: AcceptProps & { session: SignedInBody; refused: unknown }) {
  const { t } = useTranslation();
  const account = useSuspenseQuery(accountQuery).data;
  const signOut = useSignOut({ stay: true });
  const join = useMutation({
    mutationFn: () => accept(token, session),
    onSuccess: onJoined,
    meta: { showsRefusals: true },
  });
  const error = join.isIdle ? refused : join.error;
  const mismatch =
    error instanceof ApiError && error.error?.code === "invite_email_mismatch";
  return (
    <section className="space-y-4" aria-labelledby="invite-accept">
      <h2 id="invite-accept" className="text-lg font-semibold">
        {t("invite.accept_title")}
      </h2>
      <p>{t("invite.signed_in_as", { email: account.email })}</p>
      <Alert
        message={
          error === null
            ? undefined
            : mismatch
              ? mismatchMessage(t, invite, account)
              : refusalOf(error, t).message
        }
      />
      <div className="flex flex-wrap gap-2">
        <Button
          variant="primary"
          disabled={join.isPending}
          onClick={() => join.mutate()}
        >
          {t("invite.accept")}
        </Button>
        {mismatch && (
          <Button
            disabled={signOut.isPending}
            onClick={() => signOut.mutate(session)}
          >
            {t("invite.sign_out")}
          </Button>
        )}
      </div>
    </section>
  );
}

function mismatchMessage(
  t: TFunction,
  invite: InvitePreviewBody,
  account: AccountBody,
): string {
  return t("invite.error.mismatch", {
    invited: invite.email ?? "",
    email: account.email,
  });
}

/**
 * Signing in, or registering, and accepting the invitation, in one step:
 * the page records its session once the invitation is accepted, or refused,
 * so that it does not show the signed-in step in between. A refusal is told
 * to onRefused first, since the signed-in step, which shows it, replaces
 * this one.
 */
function AcceptSignedOut({
  token,
  invite,
  onJoined,
  onRefused,
}: AcceptProps & { onRefused: (error: unknown) => void }) {
  const { t } = useTranslation();
  const instance = useSuspenseQuery(instanceQuery).data;
  const client = useQueryClient();
  const signInThenAccept = async (email: string, password: string) => {
    const session = (await api.call("login", { body: { email, password } }))
      .body;
    try {
      return await accept(token, session);
    } catch (error) {
      onRefused(error);
      throw error;
    } finally {
      signedIn(client, session);
    }
  };
  return (
    <div className="space-y-8">
      <SignInAndAccept
        invite={invite}
        signInThenAccept={signInThenAccept}
        onJoined={onJoined}
      />
      {instance.signup_policy === "closed" ? (
        <p>{t("invite.signup_closed")}</p>
      ) : (
        <RegisterAndAccept
          token={token}
          invite={invite}
          signInThenAccept={signInThenAccept}
          onJoined={onJoined}
        />
      )}
    </div>
  );
}

interface StepProps {
  invite: InvitePreviewBody;
  signInThenAccept: (
    email: string,
    password: string,
  ) => Promise<MembershipBody>;
  onJoined: (membership: MembershipBody) => void;
}

function SignInAndAccept({ invite, signInThenAccept, onJoined }: StepProps) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const join = useMutation({
    mutationFn: (c: { email: string; password: string }) =>
      signInThenAccept(c.email, c.password),
    onSuccess: onJoined,
    meta: { showsRefusals: true },
  });
  const refusal = join.error === null ? undefined : refusalOf(join.error, t);
  useFocusRefusal(form, join.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    join.mutate({
      email: String(data.get("email")),
      password: String(data.get("password")),
    });
  };
  return (
    <section className="space-y-4" aria-labelledby="invite-sign-in">
      <h2 id="invite-sign-in" className="text-lg font-semibold">
        {t("invite.sign_in_title")}
      </h2>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("auth.email")}
          name="email"
          type="email"
          autoComplete="username"
          defaultValue={invite.email}
          required
        />
        <TextField
          label={t("auth.password")}
          name="password"
          type="password"
          autoComplete="current-password"
          required
        />
        <Button type="submit" variant="primary" disabled={join.isPending}>
          {t("invite.sign_in_submit")}
        </Button>
      </form>
    </section>
  );
}

/** How registering with the invitation ended, if it did not join. */
type Registered = MembershipBody | "registered";

function RegisterAndAccept({
  token,
  invite,
  signInThenAccept,
  onJoined,
}: StepProps & { token: string }) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [mismatch, setMismatch] = useState<Mismatch>();
  const join = useMutation({
    mutationFn: async (c: {
      email: string;
      display_name: string;
      password: string;
    }): Promise<Registered> => {
      await api.call("registerWithInvite", {
        // An invitation to an address registers that address.
        body: {
          token,
          display_name: c.display_name,
          password: c.password,
          ...(invite.kind === "link" ? { email: c.email } : {}),
        },
      });
      return signInRegistered(() => signInThenAccept(c.email, c.password));
    },
    onSuccess: (registered) => {
      if (registered !== "registered") {
        onJoined(registered);
      }
    },
    meta: { showsRefusals: true },
  });
  const refusal =
    join.error === null
      ? undefined
      : refusalOf(join.error, t, registrationField);
  const errors: Record<string, string | undefined> = {
    ...refusal?.fields,
    ...(mismatch && { [mismatch.field]: t("auth.error.passwords_differ") }),
  };
  useFocusRefusal(form, mismatch ?? join.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const differ = passwordMismatch(data);
    setMismatch(differ);
    if (differ === undefined) {
      join.mutate({
        email: invite.email ?? String(data.get("email")),
        display_name: String(data.get("display_name")),
        password: String(data.get("password")),
      });
    }
  };
  return (
    <section className="space-y-4" aria-labelledby="invite-register">
      <h2 id="invite-register" className="text-lg font-semibold">
        {t("invite.register_title")}
      </h2>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <Status
          message={
            join.data === "registered" ? t("invite.registered") : undefined
          }
        />
        <TextField
          label={t("auth.display_name")}
          hint={t("auth.display_name_hint")}
          name="display_name"
          autoComplete="name"
          required
          error={errors.display_name}
        />
        {invite.kind === "link" ? (
          <TextField
            label={t("auth.email")}
            name="email"
            type="email"
            autoComplete="email"
            required
            error={errors.email}
          />
        ) : (
          <TextField
            label={t("auth.email")}
            hint={t("invite.email_fixed")}
            name="email"
            type="email"
            value={invite.email}
            readOnly
          />
        )}
        <TextField
          label={t("auth.new_password")}
          hint={t("auth.password_hint", { min: minPasswordLength })}
          name="password"
          type="password"
          autoComplete="new-password"
          required
          error={errors.password}
        />
        <TextField
          label={t("auth.confirm_password")}
          name="confirm"
          type="password"
          autoComplete="new-password"
          required
          error={errors.confirm}
        />
        <Button type="submit" variant="primary" disabled={join.isPending}>
          {t("invite.register_submit")}
        </Button>
      </form>
    </section>
  );
}
