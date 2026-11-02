import type { LoginBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import {
  createFileRoute,
  useLocation,
  useNavigate,
} from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { instanceQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import {
  requireSignedOut,
  signIn,
  signInReturn,
  signInSearch,
} from "../auth/session";
import { Button } from "../ui/Button";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, TextLink } from "../ui/Page";

export const Route = createFileRoute("/login")({
  validateSearch: signInSearch,
  beforeLoad: requireSignedOut,
  component: Login,
});

// Signing in (PRD FR-AUTH-02), which goes back to the page that sent the
// user here, with the fragment that it had, or else to the start page.
function Login() {
  const { t } = useTranslation();
  const search = Route.useSearch();
  // The fragment of the page to go back to (signInFor), as the screen opened
  // with it, whatever its skip link has made of it since.
  const [hash] = useState(useLocation({ select: (location) => location.hash }));
  const navigate = useNavigate();
  const client = useQueryClient();
  const instance = useSuspenseQuery(instanceQuery).data;
  const form = useRef<HTMLFormElement>(null);
  const login = useMutation({
    mutationFn: (credentials: LoginBody) => signIn(client, credentials),
    onSuccess: () => navigate({ href: signInReturn(search, hash) }),
    meta: { showsRefusals: true },
  });
  const refusal = login.error === null ? undefined : refusalOf(login.error, t);
  useFocusRefusal(form, login.error);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    login.mutate({
      email: String(data.get("email")),
      password: String(data.get("password")),
    });
  };
  return (
    <Page title={t("login.title")}>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("auth.email")}
          name="email"
          type="email"
          autoComplete="username"
          required
        />
        <TextField
          label={t("auth.password")}
          name="password"
          type="password"
          autoComplete="current-password"
          required
        />
        <Button type="submit" variant="primary" disabled={login.isPending}>
          {t("login.submit")}
        </Button>
      </form>
      <p>
        <TextLink to="/forgot-password">{t("login.forgot_password")}</TextLink>
      </p>
      {instance.signup_policy === "open" && (
        <p>
          {t("login.no_account")}{" "}
          <TextLink to="/signup">{t("login.sign_up")}</TextLink>
        </p>
      )}
    </Page>
  );
}
