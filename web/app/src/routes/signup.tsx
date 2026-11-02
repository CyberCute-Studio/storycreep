import type { RegisterBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useRef } from "react";
import { useTranslation } from "react-i18next";
import { api, instanceQuery } from "../api";
import {
  minPasswordLength,
  registrationField,
  signInRegistered,
  usePasswordConfirmation,
} from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { requireSignedOut, signIn } from "../auth/session";
import { Button } from "../ui/Button";
import { TextField } from "../ui/Field";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/signup")({
  beforeLoad: requireSignedOut,
  component: Signup,
});

/** How a registration ended. */
type Registered = "signed-in" | "registered";

// Registering an account (PRD FR-AUTH-01), where the instance's signup
// policy lets anyone: the page then signs in with it. The server answers a
// registration alike whether or not the address has an account, so that it
// does not tell which; signing in then fails for an address that had one
// with another password, and the page says only to sign in.
function Signup() {
  const { t } = useTranslation();
  const instance = useSuspenseQuery(instanceQuery).data;
  const navigate = useNavigate();
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const register = useMutation({
    mutationFn: async (body: RegisterBody): Promise<Registered> => {
      await api.call("registerAccount", { body });
      return signInRegistered(async () => {
        await signIn(client, { email: body.email, password: body.password });
        return "signed-in" as const;
      });
    },
    // Registering stays pending until the start page shows, so that its
    // form, shown until then, cannot be sent again.
    onSuccess: (registered) =>
      registered === "signed-in" ? navigate({ to: "/" }) : undefined,
  });
  const refusal =
    register.error === null
      ? undefined
      : refusalOf(register.error, t, registrationField);
  const { errors, confirmed } = usePasswordConfirmation(
    form,
    register,
    refusal?.fields,
  );

  if (instance.signup_policy !== "open") {
    return (
      <Page title={t("signup.title")}>
        <p>
          {instance.signup_policy === "invite"
            ? t("signup.invite_only")
            : t("signup.closed")}
        </p>
        <p>
          <TextLink to="/login">{t("signup.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }
  if (register.data === "registered") {
    return (
      <Page title={t("signup.title")}>
        <Status message={t("signup.registered")} />
        <p>
          <TextLink to="/login">{t("signup.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    if (!confirmed(data)) {
      return;
    }
    register.mutate({
      display_name: String(data.get("display_name")),
      email: String(data.get("email")),
      password: String(data.get("password")),
    });
  };
  return (
    <Page title={t("signup.title")}>
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("auth.display_name")}
          hint={t("auth.display_name_hint")}
          name="display_name"
          autoComplete="name"
          required
          error={errors.display_name}
        />
        <TextField
          label={t("auth.email")}
          name="email"
          type="email"
          autoComplete="email"
          required
          error={errors.email}
        />
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
        <Button type="submit" variant="primary" disabled={register.isPending}>
          {t("signup.submit")}
        </Button>
      </form>
      <p>
        {t("signup.have_account")}{" "}
        <TextLink to="/login">{t("signup.sign_in")}</TextLink>
      </p>
    </Page>
  );
}
