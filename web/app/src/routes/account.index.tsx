import type { AccountBody, InstanceBody } from "@storycreep/shared/api";
import {
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, api, instanceQuery, sessionQuery } from "../api";
import {
  bodyField,
  type Mismatch,
  minPasswordLength,
  passwordMismatch,
} from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders, signedIn } from "../auth/session";
import { VerifyNotice } from "../auth/VerifyNotice";
import { applyLocale, supportedLocales } from "../i18n";
import { Button } from "../ui/Button";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/account/")({
  component: Profile,
});

// The account's profile: its name and email address, whether the address
// is verified, the language that the user prefers, and its password.
function Profile() {
  const { t } = useTranslation();
  const account = useSuspenseQuery(accountQuery).data;
  const instance = useSuspenseQuery(instanceQuery).data;
  return (
    <Page title={t("account.title")} width="wide">
      <section className="space-y-3" aria-labelledby="account-profile">
        <h2 id="account-profile" className="text-lg font-semibold">
          {t("account.profile.title")}
        </h2>
        <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
          <dt className="text-fg-muted">{t("auth.display_name")}</dt>
          <dd>{account.display_name}</dd>
          <dt className="text-fg-muted">{t("auth.email")}</dt>
          <dd className="break-all">{account.email}</dd>
          <dt className="text-fg-muted">{t("account.profile.verification")}</dt>
          <dd>
            {account.email_verified
              ? t("account.profile.verified")
              : instance.email_configured
                ? t("account.profile.unverified")
                : t("account.profile.unverified_no_email")}
          </dd>
        </dl>
        <VerifyNotice account={account} instance={instance} />
      </section>
      <Language account={account} instance={instance} />
      <Password />
    </Page>
  );
}

/** Returns the name of locale in its own language, as English for en. */
function languageName(locale: string): string {
  try {
    return (
      new Intl.DisplayNames([locale], { type: "language" }).of(locale) ?? locale
    );
  } catch {
    return locale;
  }
}

/**
 * The language that the user prefers (PRD FR-I18N-02), which the account
 * keeps, so that every page shows itself in it, on every device; saving it
 * shows this page in it at once, without a reload (AC1).
 */
function Language({
  account,
  instance,
}: {
  account: AccountBody;
  instance: InstanceBody;
}) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const save = useMutation({
    mutationFn: async (locale: string) => {
      if (!session) {
        throw new Error("The page is signed out.");
      }
      return (
        await api.call("updateAccount", {
          headers: csrfHeaders(session),
          body: { locale },
        })
      ).body;
    },
    onSuccess: (updated) => {
      client.setQueryData(accountQuery.queryKey, updated);
      applyLocale(i18n, {
        preference: updated.locale,
        instanceDefault: instance.default_locale,
      });
    },
    meta: { showsRefusals: true },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate(String(new FormData(event.currentTarget).get("locale")));
  };
  return (
    <section className="space-y-3" aria-labelledby="account-language">
      <h2 id="account-language" className="text-lg font-semibold">
        {t("account.language.title")}
      </h2>
      <form className="space-y-4" onSubmit={submit}>
        <Alert
          message={
            save.error === null ? undefined : refusalOf(save.error, t).message
          }
        />
        <Status
          message={save.isSuccess ? t("account.language.saved") : undefined}
        />
        <SelectField
          label={t("account.language.label")}
          hint={t("account.language.hint")}
          name="locale"
          defaultValue={account.locale ?? ""}
        >
          <option value="">{t("account.language.browser")}</option>
          {supportedLocales(i18n).map((locale) => (
            <option key={locale} value={locale} lang={locale}>
              {languageName(locale)}
            </option>
          ))}
        </SelectField>
        <Button type="submit" disabled={save.isPending}>
          {t("account.language.save")}
        </Button>
      </form>
    </section>
  );
}

const passwordField = bodyField("current_password", "new_password");

/**
 * Changing the password (PRD FR-AUTH-02 AC4), which signs out every other
 * session of the account, and gives this one a new secret and CSRF token.
 */
function Password() {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [mismatch, setMismatch] = useState<Mismatch>();
  const change = useMutation({
    mutationFn: async (body: {
      current_password: string;
      new_password: string;
    }) => {
      if (!session) {
        throw new Error("The page is signed out.");
      }
      return (
        await api.call("changePassword", {
          headers: csrfHeaders(session),
          body,
        })
      ).body;
    },
    onSuccess: (renewed) => {
      signedIn(client, renewed);
      form.current?.reset();
    },
    meta: { showsRefusals: true },
  });
  const refusal =
    change.error === null
      ? undefined
      : refusalOf(change.error, t, passwordField);
  const errors: Record<string, string | undefined> = {
    ...refusal?.fields,
    ...(mismatch && { [mismatch.field]: t("auth.error.passwords_differ") }),
  };
  useFocusRefusal(form, mismatch ?? change.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const differ = passwordMismatch(data, "new_password");
    setMismatch(differ);
    if (differ === undefined) {
      change.mutate({
        current_password: String(data.get("current_password")),
        new_password: String(data.get("new_password")),
      });
    }
  };
  return (
    <section className="space-y-3" aria-labelledby="account-password">
      <h2 id="account-password" className="text-lg font-semibold">
        {t("account.password.title")}
      </h2>
      <form
        ref={form}
        className="max-w-md space-y-4"
        onSubmit={submit}
        noValidate
      >
        <Alert message={refusal?.message} />
        <Status
          message={change.isSuccess ? t("account.password.changed") : undefined}
        />
        <TextField
          label={t("account.password.current")}
          name="current_password"
          type="password"
          autoComplete="current-password"
          required
          error={errors.current_password}
        />
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
        <Button type="submit" disabled={change.isPending}>
          {t("account.password.submit")}
        </Button>
      </form>
    </section>
  );
}
