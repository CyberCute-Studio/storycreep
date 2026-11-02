import { useMutation, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type FormEvent, useRef } from "react";
import { useTranslation } from "react-i18next";
import { api, instanceQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { Button } from "../ui/Button";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/forgot-password")({
  component: ForgotPassword,
});

// Asking for a link that resets a password (PRD FR-AUTH-03), which the
// instance emails, if it sends email. One that does not says so, and where
// the link comes from instead: an instance admin. The server answers alike
// whether or not the address has an account, and so does the page.
function ForgotPassword() {
  const { t } = useTranslation();
  const instance = useSuspenseQuery(instanceQuery).data;
  const form = useRef<HTMLFormElement>(null);
  const request = useMutation({
    mutationFn: async (email: string) => {
      await api.call("requestPasswordReset", { body: { email } });
    },
  });
  const refusal =
    request.error === null ? undefined : refusalOf(request.error, t);
  useFocusRefusal(form, request.error);

  if (!instance.email_configured) {
    return (
      <Page title={t("forgot.title")}>
        <p>{t("forgot.no_email")}</p>
        <p>
          <TextLink to="/login">{t("forgot.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    request.mutate(String(new FormData(event.currentTarget).get("email")));
  };
  return (
    <Page title={t("forgot.title")}>
      <p>{t("forgot.intro")}</p>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <Status message={request.isSuccess ? t("forgot.sent") : undefined} />
        <TextField
          label={t("auth.email")}
          name="email"
          type="email"
          autoComplete="email"
          required
        />
        <Button type="submit" variant="primary" disabled={request.isPending}>
          {t("forgot.submit")}
        </Button>
      </form>
      <p>
        <TextLink to="/login">{t("forgot.sign_in")}</TextLink>
      </p>
    </Page>
  );
}
