import type {
  UpdateWorkspaceBody,
  WorkspaceBody,
} from "@storycreep/shared/api";
import {
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, notFound } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { bodyField } from "../auth/forms";
import { refusalOf } from "../auth/refusal";
import { roleLabel } from "../auth/roles";
import { csrfHeaders } from "../auth/session";
import { languageName, supportedLocales } from "../i18n";
import { found } from "../shell/found";
import { Button } from "../ui/Button";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert, Page, Status } from "../ui/Page";
import {
  managesWorkspace,
  usageQuery,
  type Workspace,
  workspaceQuery,
} from "../workspace/queries";
import { UsageTable } from "../workspace/Usage";

export const Route = createFileRoute("/workspaces/$workspaceId/settings")({
  loader: async ({ context: { queryClient }, params }) => {
    const workspace = await found(
      queryClient.ensureQueryData(workspaceQuery(params.workspaceId)),
    );
    // A guest has no role in the workspace, and sees nothing of it but the
    // projects granted to them (§5.5).
    if (workspace.role === "guest") {
      throw notFound();
    }
  },
  component: Settings,
});

// A workspace's settings (PRD FR-TEN-01): its name, its slug, and its
// default locale, which its owners and admins change, and, to them alone,
// how much it uses of each limit of its plan (FR-ENT-02 AC2).
function Settings() {
  const { t } = useTranslation();
  const { workspaceId } = Route.useParams();
  const workspace = useSuspenseQuery(workspaceQuery(workspaceId)).data;
  const manages = managesWorkspace(workspace.role);
  return (
    <Page
      title={t("workspace_settings.title")}
      documentTitle={t("workspace.page_title", {
        page: t("workspace_settings.title"),
        workspace: workspace.name,
      })}
      width="wide"
    >
      {manages ? (
        <SettingsForm workspace={workspace} />
      ) : (
        <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
          <dt className="text-fg-muted">{t("workspace_settings.name")}</dt>
          <dd className="break-words">{workspace.name}</dd>
          <dt className="text-fg-muted">{t("workspace_settings.slug")}</dt>
          <dd className="break-words">{workspace.slug}</dd>
          <dt className="text-fg-muted">{t("workspace_settings.your_role")}</dt>
          <dd>{roleLabel(t, workspace.role)}</dd>
        </dl>
      )}
      {manages && <Usage workspace={workspace} />}
    </Page>
  );
}

const settingsField = bodyField("name", "slug", "default_locale");

/**
 * The form of a workspace's settings, which sends what of them changed, and
 * nothing else, so that a setting that another admin changed since the page
 * read the workspace is not set back to what it was: the server's change of
 * a workspace expects no revision. It shows the workspace as the page has
 * it until the user changes something in it, and then as it was, so that a
 * change made elsewhere, which the page reads as its tab is shown again,
 * does not take away what the user typed, until the form is saved.
 */
function SettingsForm({ workspace }: { workspace: Workspace }) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const form = useRef<HTMLFormElement>(null);
  const [unchanged, setUnchanged] = useState(false);
  const [held, setHeld] = useState<Workspace>();
  const shown = held ?? workspace;
  const save = useMutation({
    mutationFn: async (body: UpdateWorkspaceBody): Promise<WorkspaceBody> =>
      (
        await api.call("updateWorkspace", {
          path: { workspace_id: workspace.id },
          headers: csrfHeaders(session),
          body,
        })
      ).body,
    onSuccess: () => client.invalidateQueries({ queryKey: ["workspaces"] }),
  });
  const refusal = refusalOf(save.error, t, settingsField, {
    slug_taken: t("workspace_settings.error.slug_taken"),
  });
  useFocusRefusal(form, save.error);
  // A locale that the workspace keeps, which the app may have no catalog
  // of, stays a choice, so that saving the form keeps it.
  const locales = supportedLocales(i18n);
  if (!locales.includes(shown.default_locale)) {
    locales.push(shown.default_locale);
  }
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const name = String(data.get("name"));
    const slug = String(data.get("slug"));
    const locale = String(data.get("default_locale"));
    const body: UpdateWorkspaceBody = {
      ...(name === shown.name ? {} : { name }),
      ...(slug === shown.slug ? {} : { slug }),
      ...(locale === shown.default_locale ? {} : { default_locale: locale }),
    };
    const changed = Object.keys(body).length > 0;
    setUnchanged(!changed);
    if (!changed) {
      save.reset();
      setHeld(undefined);
      return;
    }
    save.mutate(body, { onSuccess: () => setHeld(undefined) });
  };
  return (
    <div className="max-w-md space-y-4">
      <Alert message={refusal?.message} />
      <Status
        message={
          unchanged
            ? t("workspace_settings.unchanged")
            : save.isSuccess
              ? t("workspace_settings.saved")
              : undefined
        }
      />
      {/* Shown anew once the workspace that it shows has changed, as once
          this form saved it, rather than showing what it was. */}
      <form
        key={`${shown.name}\n${shown.slug}\n${shown.default_locale}`}
        ref={form}
        className="space-y-4"
        onSubmit={submit}
        onChange={() => setHeld((before) => before ?? workspace)}
        noValidate
      >
        <TextField
          label={t("workspace_settings.name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={shown.name}
          error={refusal?.fields.name}
        />
        <TextField
          label={t("workspace_settings.slug")}
          hint={t("workspace_settings.slug_hint")}
          name="slug"
          autoComplete="off"
          spellCheck={false}
          required
          defaultValue={shown.slug}
          error={refusal?.fields.slug}
        />
        <SelectField
          label={t("workspace_settings.default_locale")}
          hint={t("workspace_settings.default_locale_hint")}
          name="default_locale"
          defaultValue={shown.default_locale}
          error={refusal?.fields.default_locale}
        >
          {locales.map((locale) => (
            <option key={locale} value={locale} lang={locale}>
              {languageName(locale)}
            </option>
          ))}
        </SelectField>
        <Button type="submit" variant="primary" disabled={save.isPending}>
          {t("workspace_settings.save")}
        </Button>
      </form>
    </div>
  );
}

/** What the workspace uses against its limits, which its owners and admins read. */
function Usage({ workspace }: { workspace: Workspace }) {
  const { t } = useTranslation();
  const usage = useQuery(usageQuery(workspace.id));
  return (
    <section className="space-y-3" aria-labelledby="workspace-usage">
      <h2 id="workspace-usage" className="text-lg font-semibold">
        {t("usage.title")}
      </h2>
      <p className="text-fg-muted">{t("usage.intro")}</p>
      {usage.isError && <Alert message={refusalOf(usage.error, t)?.message} />}
      {usage.data !== undefined && <UsageTable usage={usage.data} />}
    </section>
  );
}
