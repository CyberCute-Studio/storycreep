import {
  ApiError,
  type CompleteSetupBody,
  type SetupBody,
} from "@storycreep/shared/api";
import {
  queryOptions,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, instanceQuery, readInstanceAgain } from "../api";
import {
  bodyPath,
  minPasswordLength,
  usePasswordConfirmation,
} from "../auth/forms";
import { type FieldOf, refusalOf } from "../auth/refusal";
import { signIn } from "../auth/session";
import { Button } from "../ui/Button";
import { CheckboxField, GroupField, SelectField, TextField } from "../ui/Field";
import { Alert, Page, Status, TextLink } from "../ui/Page";

/**
 * What first-run setup asks for, while it is pending, or null once it has
 * completed, as by another tab since the page read the instance, which the
 * server answers with 404: no error, but where the page goes to sign in.
 */
const setupQuery = queryOptions({
  queryKey: ["setup"],
  queryFn: async ({ signal }): Promise<SetupBody | null> => {
    try {
      return (await api.call("getSetup", {}, { signal })).body;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        return null;
      }
      throw error;
    }
  },
  staleTime: Number.POSITIVE_INFINITY,
});

export const Route = createFileRoute("/setup")({
  beforeLoad: async ({ context }) => {
    const instance = await context.queryClient.ensureQueryData(instanceQuery);
    if (!instance.setup_pending) {
      throw redirect({ to: "/login" });
    }
  },
  loader: async ({ context }) => {
    const setup = await context.queryClient.ensureQueryData(setupQuery);
    if (setup === null) {
      // Setup completed since the instance was read, as by another tab.
      await readInstanceAgain(context.queryClient);
      throw redirect({ to: "/login" });
    }
  },
  component: Setup,
});

/** The signup policies that setup chooses from. */
const policies = ["closed", "invite", "open"] as const;

/** The form's fields that the server's locations under the body name. */
const setupField: FieldOf = (location) => {
  const path = bodyPath(location);
  const owner: Record<string, string> = {
    "owner.email": "owner_email",
    "owner.display_name": "owner_name",
    "owner.password": "owner_password",
  };
  if (path in owner) {
    return owner[path];
  }
  if (path.startsWith("workspace.")) {
    // The slug is derived from the name, which the form asks for alone.
    return "workspace_name";
  }
  const section = /^(smtp|storage)\.([a-z_]+)$/.exec(path);
  if (section !== null) {
    return `${section[1]}_${section[2]}`;
  }
  return path === "setup_code" || path === "signup_policy" ? path : undefined;
};

/** Returns the value of a field of data, or undefined if it is empty. */
function optional(data: FormData, name: string): string | undefined {
  const value = String(data.get(name) ?? "").trim();
  return value === "" ? undefined : value;
}

/** Returns the body of POST /api/v1/setup that the form's data asks for. */
function setupBody(data: FormData, setup: SetupBody): CompleteSetupBody {
  const text = (name: string) => String(data.get(name) ?? "");
  const body: CompleteSetupBody = {
    setup_code: text("setup_code"),
    owner: {
      email: text("owner_email"),
      display_name: text("owner_name"),
      password: text("owner_password"),
    },
    workspace: { name: text("workspace_name") },
  };
  if (!setup.signup_policy_fixed) {
    body.signup_policy = text(
      "signup_policy",
    ) as CompleteSetupBody["signup_policy"];
  }
  if (!setup.smtp_fixed && data.get("smtp") === "on") {
    body.smtp = {
      host: text("smtp_host"),
      port: Number(text("smtp_port")),
      security: text("smtp_security") as "tls" | "starttls" | "none",
      username: optional(data, "smtp_username"),
      password: optional(data, "smtp_password"),
      from: text("smtp_from"),
    };
  }
  if (!setup.storage_fixed && data.get("storage") === "on") {
    const port = optional(data, "storage_port");
    body.storage = {
      host: text("storage_host"),
      port: port === undefined ? undefined : Number(port),
      security: text("storage_security") as "tls" | "none",
      bucket: text("storage_bucket"),
      region: optional(data, "storage_region"),
      access_key_id: text("storage_access_key_id"),
      secret_access_key: text("storage_secret_access_key"),
    };
  }
  return body;
}

/**
 * How a setup ended: signed in as the owner, or completed without, as when
 * signing in then failed, or completed first by someone else.
 */
type Completed = "signed-in" | "signed-out" | "taken";

// First-run setup (PRD FR-ADMIN-02): the first account, which administers
// the instance, its first workspace, who can register, and, optionally, the
// SMTP server and the bucket of the uploaded files, which the configuration
// may set instead. It needs the setup code that the server writes to its
// log, so that whoever reaches a new instance first cannot make themselves
// its administrator. Once it completes, the page signs in as the owner.
function Setup() {
  const setup = useSuspenseQuery(setupQuery).data;
  // Once setup has completed, the loader has gone to sign in.
  return setup === null ? null : <SetupForm setup={setup} />;
}

function SetupForm({ setup }: { setup: SetupBody }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const navigate = useNavigate();
  const form = useRef<HTMLFormElement>(null);
  const [smtp, setSmtp] = useState(false);
  const [storage, setStorage] = useState(false);
  const complete = useMutation({
    mutationFn: async (body: CompleteSetupBody): Promise<Completed> => {
      try {
        await api.call("completeSetup", { body });
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) {
          // Another setup completed first.
          await readInstanceAgain(client);
          return "taken";
        }
        throw error;
      }
      // Setup has completed. What fails from here on is signing in, or
      // reading the instance again, not setup: shown on the form, it would
      // have the user set up again, which the server would answer as if
      // someone else had completed it first. The two go at once.
      const [, owner] = await Promise.allSettled([
        readInstanceAgain(client),
        signIn(client, {
          email: body.owner.email,
          password: body.owner.password,
        }),
      ]);
      return owner.status === "fulfilled" ? "signed-in" : "signed-out";
    },
    // Setup stays pending until the start page shows, so that its form,
    // shown until then, cannot be sent again, which would be refused.
    onSuccess: (completed) =>
      completed === "signed-in" ? navigate({ to: "/" }) : undefined,
  });
  const refusal = refusalOf(complete.error, t, setupField);
  const { errors, confirmed } = usePasswordConfirmation(
    form,
    complete,
    refusal?.fields,
    "owner_password",
    "owner_confirm",
  );

  if (complete.data === "signed-out") {
    return (
      <Page title={t("setup.title")}>
        <Status message={t("setup.completed")} />
        <p>
          <TextLink to="/login">{t("setup.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }
  if (complete.data === "taken") {
    return (
      <Page title={t("setup.title")}>
        <p>{t("setup.taken")}</p>
        <p>
          <TextLink to="/login">{t("setup.sign_in")}</TextLink>
        </p>
      </Page>
    );
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    if (confirmed(data)) {
      complete.mutate(setupBody(data, setup));
    }
  };
  return (
    <Page title={t("setup.title")}>
      <p>{t("setup.intro")}</p>
      <form ref={form} className="space-y-8" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("setup.code.label")}
          hint={t("setup.code.hint")}
          name="setup_code"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          required
          error={errors.setup_code}
        />
        <section className="space-y-4" aria-labelledby="setup-owner">
          <h2 id="setup-owner" className="text-lg font-semibold">
            {t("setup.owner.title")}
          </h2>
          <p className="text-fg-muted">{t("setup.owner.hint")}</p>
          <TextField
            label={t("auth.display_name")}
            hint={t("auth.display_name_hint")}
            name="owner_name"
            autoComplete="name"
            required
            error={errors.owner_name}
          />
          <TextField
            label={t("auth.email")}
            name="owner_email"
            type="email"
            autoComplete="email"
            required
            error={errors.owner_email}
          />
          <TextField
            label={t("auth.new_password")}
            hint={t("auth.password_hint", { min: minPasswordLength })}
            name="owner_password"
            type="password"
            autoComplete="new-password"
            required
            error={errors.owner_password}
          />
          <TextField
            label={t("auth.confirm_password")}
            name="owner_confirm"
            type="password"
            autoComplete="new-password"
            required
            error={errors.owner_confirm}
          />
        </section>
        <section className="space-y-4" aria-labelledby="setup-workspace">
          <h2 id="setup-workspace" className="text-lg font-semibold">
            {t("setup.workspace.title")}
          </h2>
          <TextField
            label={t("setup.workspace.label")}
            hint={t("setup.workspace.hint")}
            name="workspace_name"
            autoComplete="organization"
            required
            error={errors.workspace_name}
          />
        </section>
        <SignupPolicy setup={setup} error={errors.signup_policy} />
        {!setup.smtp_fixed && (
          <section className="space-y-4" aria-labelledby="setup-email">
            <h2 id="setup-email" className="text-lg font-semibold">
              {t("setup.smtp.title")}
            </h2>
            <CheckboxField
              label={t("setup.smtp.enable")}
              hint={t("setup.smtp.hint")}
              name="smtp"
              checked={smtp}
              onChange={(event) => setSmtp(event.currentTarget.checked)}
            />
            {smtp && <SmtpFields errors={errors} />}
          </section>
        )}
        {!setup.storage_fixed && (
          <section className="space-y-4" aria-labelledby="setup-storage">
            <h2 id="setup-storage" className="text-lg font-semibold">
              {t("setup.storage.title")}
            </h2>
            <CheckboxField
              label={t("setup.storage.enable")}
              hint={t("setup.storage.hint")}
              name="storage"
              checked={storage}
              onChange={(event) => setStorage(event.currentTarget.checked)}
            />
            {storage && <StorageFields errors={errors} />}
          </section>
        )}
        <Button type="submit" variant="primary" disabled={complete.isPending}>
          {t("setup.submit")}
        </Button>
      </form>
    </Page>
  );
}

/** The choice of who can register, unless the configuration sets it. */
function SignupPolicy({ setup, error }: { setup: SetupBody; error?: string }) {
  const { t } = useTranslation();
  const label = {
    closed: t("setup.signup.closed"),
    invite: t("setup.signup.invite"),
    open: t("setup.signup.open"),
  };
  const hint = {
    closed: t("setup.signup.closed_hint"),
    invite: t("setup.signup.invite_hint"),
    open: t("setup.signup.open_hint"),
  };
  if (setup.signup_policy_fixed) {
    return (
      <section className="space-y-2" aria-labelledby="setup-signup">
        <h2 id="setup-signup" className="text-lg font-semibold">
          {t("setup.signup.title")}
        </h2>
        <p>{t("setup.signup.fixed", { policy: label[setup.signup_policy] })}</p>
      </section>
    );
  }
  return (
    <GroupField
      legend={t("setup.signup.title")}
      legendClassName="mb-2 text-lg font-semibold"
      className="space-y-2"
      error={error}
    >
      {policies.map((policy) => (
        <label key={policy} className="flex min-h-11 items-start gap-3 py-1">
          <input
            type="radio"
            name="signup_policy"
            value={policy}
            defaultChecked={policy === setup.signup_policy}
            className="mt-1 size-5 shrink-0 accent-accent"
          />
          <span>
            <span className="block font-medium">{label[policy]}</span>
            <span className="block text-sm text-fg-muted">{hint[policy]}</span>
          </span>
        </label>
      ))}
    </GroupField>
  );
}

type Errors = Record<string, string | undefined>;

/** The SMTP server's settings. */
function SmtpFields({ errors }: { errors: Errors }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <TextField
        label={t("setup.smtp.host")}
        name="smtp_host"
        autoComplete="off"
        spellCheck={false}
        required
        error={errors.smtp_host}
      />
      <TextField
        label={t("setup.smtp.port")}
        hint={t("setup.smtp.port_hint")}
        name="smtp_port"
        type="number"
        inputMode="numeric"
        min={1}
        max={65535}
        defaultValue={587}
        required
        error={errors.smtp_port}
      />
      <SelectField
        label={t("setup.smtp.security")}
        name="smtp_security"
        defaultValue="starttls"
        error={errors.smtp_security}
      >
        <option value="starttls">{t("setup.smtp.security_starttls")}</option>
        <option value="tls">{t("setup.smtp.security_tls")}</option>
        <option value="none">{t("setup.smtp.security_none")}</option>
      </SelectField>
      <TextField
        label={t("setup.smtp.username")}
        hint={t("setup.smtp.username_hint")}
        name="smtp_username"
        autoComplete="off"
        spellCheck={false}
        error={errors.smtp_username}
      />
      <TextField
        label={t("setup.smtp.password")}
        name="smtp_password"
        type="password"
        autoComplete="off"
        error={errors.smtp_password}
      />
      <TextField
        label={t("setup.smtp.from")}
        hint={t("setup.smtp.from_hint")}
        name="smtp_from"
        autoComplete="off"
        spellCheck={false}
        required
        error={errors.smtp_from}
      />
    </div>
  );
}

/** The bucket of an S3-compatible object storage. */
function StorageFields({ errors }: { errors: Errors }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <TextField
        label={t("setup.storage.host")}
        hint={t("setup.storage.host_hint")}
        name="storage_host"
        autoComplete="off"
        spellCheck={false}
        required
        error={errors.storage_host}
      />
      <TextField
        label={t("setup.storage.port")}
        hint={t("setup.storage.port_hint")}
        name="storage_port"
        type="number"
        inputMode="numeric"
        min={1}
        max={65535}
        error={errors.storage_port}
      />
      <SelectField
        label={t("setup.storage.security")}
        name="storage_security"
        defaultValue="tls"
        error={errors.storage_security}
      >
        <option value="tls">{t("setup.storage.security_tls")}</option>
        <option value="none">{t("setup.storage.security_none")}</option>
      </SelectField>
      <TextField
        label={t("setup.storage.bucket")}
        name="storage_bucket"
        autoComplete="off"
        spellCheck={false}
        required
        error={errors.storage_bucket}
      />
      <TextField
        label={t("setup.storage.region")}
        hint={t("setup.storage.region_hint")}
        name="storage_region"
        autoComplete="off"
        spellCheck={false}
        error={errors.storage_region}
      />
      <TextField
        label={t("setup.storage.access_key_id")}
        name="storage_access_key_id"
        autoComplete="off"
        spellCheck={false}
        required
        error={errors.storage_access_key_id}
      />
      <TextField
        label={t("setup.storage.secret_access_key")}
        name="storage_secret_access_key"
        type="password"
        autoComplete="off"
        required
        error={errors.storage_secret_access_key}
      />
    </div>
  );
}
