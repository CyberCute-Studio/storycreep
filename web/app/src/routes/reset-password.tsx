import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import {
  bodyField,
  type Mismatch,
  minPasswordLength,
  passwordMismatch,
} from "../auth/forms";
import { useFragmentToken } from "../auth/fragment";
import { refusalOf } from "../auth/refusal";
import { signedOut } from "../auth/session";
import { Button } from "../ui/Button";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/reset-password")({
  component: ResetPassword,
});

const resetField = bodyField("new_password");

// The page of a link that resets a password (PRD FR-AUTH-03), whose token
// is in the URL's fragment. The reset signs the account out everywhere, in
// this browser too if it is signed in to it, so the page then leads to
// signing in with the new password.
function ResetPassword() {
  const { t } = useTranslation();
  const token = useFragmentToken();
  if (token === undefined) {
    return (
      <Page title={t("reset.title")}>
        <p>{t("reset.no_token")}</p>
        <p>
          <TextLink to="/forgot-password">{t("reset.new_link")}</TextLink>
        </p>
      </Page>
    );
  }
  // Another link opened in the same page, whose fragment alone changes,
  // resets with its own token, from an empty form.
  return <ResetWith key={token} token={token} />;
}

function ResetWith({ token }: { token: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [mismatch, setMismatch] = useState<Mismatch>();
  const reset = useMutation({
    mutationFn: async (password: string) => {
      await api.call("resetPassword", {
        body: { token, new_password: password },
      });
    },
    // The server revoked every session of the account, which this page's
    // may be one of, or not, as one of another account's: the page asks
    // the server, and is signed out if the server says so. If the server
    // cannot say, the page stays as it is, and the next request that needs
    // the session finds out, as any does (onUnauthenticated).
    onSuccess: async () => {
      const session = await client
        .fetchQuery({ ...sessionQuery, staleTime: 0 })
        .catch(() => undefined);
      if (session === null) {
        signedOut(client);
      }
    },
    meta: { showsRefusals: true },
  });
  const refusal =
    reset.error === null ? undefined : refusalOf(reset.error, t, resetField);
  const errors: Record<string, string | undefined> = {
    ...refusal?.fields,
    ...(mismatch && { [mismatch.field]: t("auth.error.passwords_differ") }),
  };
  useFocusRefusal(form, mismatch ?? reset.error);

  if (reset.isSuccess) {
    return (
      <Page title={t("reset.title")}>
        <Status message={t("reset.done")} />
        <p>
          <TextLink to="/login">{t("reset.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const differ = passwordMismatch(data, "new_password");
    setMismatch(differ);
    if (differ === undefined) {
      reset.mutate(String(data.get("new_password")));
    }
  };
  return (
    <Page title={t("reset.title")}>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("auth.new_password")}
          hint={t("auth.password_hint", { min: minPasswordLength })}
          name="new_password"
          type="password"
          autoComplete="new-password"
          required
          error={errors.new_password}
        />
        <TextField
          label={t("auth.confirm_password")}
          name="confirm"
          type="password"
          autoComplete="new-password"
          required
          error={errors.confirm}
        />
        <Button type="submit" variant="primary" disabled={reset.isPending}>
          {t("reset.submit")}
        </Button>
      </form>
      {refusal?.message !== undefined && (
        <p>
          <TextLink to="/forgot-password">{t("reset.new_link")}</TextLink>
        </p>
      )}
    </Page>
  );
}
