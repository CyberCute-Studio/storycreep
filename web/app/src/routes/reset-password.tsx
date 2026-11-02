import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../api";
import { bodyField, passwordsDiffer } from "../auth/forms";
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
// is in the URL's fragment. The reset signs the account out everywhere, this
// browser too, so the page then leads to signing in with the new password.
function ResetPassword() {
  const { t } = useTranslation();
  const token = useFragmentToken();
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [mismatch, setMismatch] = useState(false);
  const reset = useMutation({
    mutationFn: async (password: string) => {
      if (token === undefined) {
        throw new Error("The page has no token.");
      }
      await api.call("resetPassword", {
        body: { token, new_password: password },
      });
    },
    // The server revoked every session of the account, which this page's
    // may be one of.
    onSuccess: () => signedOut(client),
    meta: { showsRefusals: true },
  });
  const refusal =
    reset.error === null ? undefined : refusalOf(reset.error, t, resetField);
  const errors: Record<string, string | undefined> = {
    ...refusal?.fields,
    ...(mismatch ? { confirm: t("auth.error.passwords_differ") } : {}),
  };
  useFocusRefusal(form, mismatch ? errors : refusal);

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
    const differ = passwordsDiffer(data, "new_password");
    setMismatch(differ);
    if (!differ) {
      reset.mutate(String(data.get("new_password")));
    }
  };
  return (
    <Page title={t("reset.title")}>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("auth.new_password")}
          hint={t("auth.password_hint", { min: 12 })}
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
