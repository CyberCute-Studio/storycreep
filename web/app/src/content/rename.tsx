// Renaming the key of a variable or a component (PRD FR-VAR-03, FR-COMP-02
// AC2): a refactor, which rewrites every expression that names it, as one
// command, which the history undoes, after a preview of what it rewrites:
// the expressions whose names of it it rewrites, and, of a variable's, those
// whose names of other variables or components' attributes it qualifies, as
// global.trust, which the new key would take from them, listed apart. The
// server lists both a page at a time (the rename previews), with whether a
// rewritten source would pass the 16,384 characters that a source may have,
// which refuses the rename.

import type { PageRenameSite, RenameSite } from "@storycreep/shared/api";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { type FormEvent, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, pagedQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { payloadField, useExpected } from "../project/dialogs";
import { useCommands } from "../project/queries";
import { Button, MoreButton } from "../ui/Button";
import { closesBy, DialogFrame, focusBackTo, type Opener } from "../ui/Dialog";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";

/** What a key's rename rewrites, of the renamed names or of those it qualifies. */
export type Rewrite = "renamed" | "qualified";

/** What the rename of a variable's or a component's key to key rewrites, a page at a time. */
export function renamePreviewQuery(
  projectId: string,
  renamed: Pick<Renamed, "kind" | "id">,
  key: string,
  rewrite: Rewrite,
) {
  const { id } = renamed;
  return pagedQuery(
    ["projects", projectId, "rename-preview", id, key, rewrite],
    async (query, signal): Promise<PageRenameSite> =>
      renamed.kind === "variable"
        ? (
            await api.call(
              "previewVariableRename",
              {
                path: { project_id: projectId, variable_id: id },
                query: { ...query, key, rewrite },
              },
              { signal },
            )
          ).body
        : (
            await api.call(
              "previewComponentRename",
              {
                path: { project_id: projectId, component_id: id },
                query: { ...query, key, rewrite },
              },
              { signal },
            )
          ).body,
    // The dialog shows a refusal of it, as of a key that is no identifier.
    { meta: { showsRefusals: true } },
  );
}

/** The field of the key, which the preview's query and the command's payload name. */
const keyField = (location: string) =>
  location === "query.key" ? "key" : payloadField("key")(location);

/** A site that the rename rewrites, as it is and as it becomes. */
function RenameSiteItem({ site }: { site: RenameSite }) {
  const { t } = useTranslation();
  const node =
    site.node_title === "" ? t("boards.untitled_node") : site.node_title;
  return (
    <li className="space-y-1">
      <p className="break-words">
        {t("usage.site", { site: site.site, node })}
        {" · "}
        {site.board_name}
        {site.in_trash && ` · ${t("rename.in_trash")}`}
      </p>
      <dl className="grid gap-x-2 gap-y-1 sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("rename.before")}</dt>
        <dd>
          <code className="block font-mono break-all whitespace-pre-wrap">
            {site.source}
          </code>
        </dd>
        <dt className="text-fg-muted">{t("rename.after")}</dt>
        <dd>
          <code className="block font-mono break-all whitespace-pre-wrap">
            {site.rewritten}
          </code>
        </dd>
      </dl>
      {site.too_long && (
        <p className="text-danger">{t("rename.too_long_site")}</p>
      )}
    </li>
  );
}

/** One list of the preview, read a page at a time. */
function PreviewList({
  query,
  title,
  empty,
}: {
  query: ReturnType<typeof renamePreviewQuery>;
  title: string;
  empty: string;
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(query);
  const titleId = useId();
  const sites = list.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <section aria-labelledby={titleId} className="space-y-2">
      <h3 id={titleId} className="font-semibold">
        {title}
      </h3>
      {list.isPending ? (
        <p className="text-fg-muted">{t("usage.loading")}</p>
      ) : list.isError ? null : sites.length === 0 ? (
        <p className="text-fg-muted">{empty}</p>
      ) : (
        <ul className="space-y-3" aria-labelledby={titleId}>
          {sites.map((site) => (
            <RenameSiteItem
              key={`${site.site}:${site.connection_id ?? site.branch_output_id ?? site.node_id}`}
              site={site}
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("usage.more")}</MoreButton>
    </section>
  );
}

/** What the rename dialog renames: a variable, or a component, with the key and the revision that the page shows. */
export interface Renamed {
  kind: "variable" | "component";
  id: string;
  key: string;
  rev: number;
}

/**
 * The dialog that renames the key of a variable or a component: it shows
 * what the rename rewrites before it renames, for the key that the field
 * holds, and renames, expecting the revision that it opened with, once the
 * preview of that key has been read, and says nothing would pass the
 * characters that a source may have.
 */
export function RenameKeyDialog({
  projectId,
  renamed,
  opener,
  onClose,
}: {
  projectId: string;
  renamed: Renamed;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const variable = renamed.kind === "variable";
  const [key, setKey] = useState(renamed.key);
  // The key whose preview the dialog shows, which renaming waits for.
  const [previewed, setPreviewed] = useState<string>();
  const commands = useCommands(projectId);
  const rev = useExpected(renamed.rev, commands.error);
  const renamedQuery = renamePreviewQuery(
    projectId,
    renamed,
    previewed ?? "",
    "renamed",
  );
  const qualifiedQuery = renamePreviewQuery(
    projectId,
    renamed,
    previewed ?? "",
    "qualified",
  );
  // The preview's refusal, as of a key that is no identifier or that
  // another of its scope has, is the form's, as the rename's would be.
  const preview = useInfiniteQuery({
    ...renamedQuery,
    enabled: previewed !== undefined,
  });
  const qualified = useInfiniteQuery({
    ...qualifiedQuery,
    enabled: previewed !== undefined && variable,
  });
  // A site of either list whose source the rename would take past the
  // characters that a source may have refuses the rename, as the server
  // would, of the pages read so far; the server finds one of the pages
  // after them, which the user has not asked for, itself.
  const tooLong = [preview, qualified].some((list) =>
    list.data?.pages.some((page) => page.items.some((site) => site.too_long)),
  );
  const own = {
    key_taken: variable
      ? t("variable.error.key_taken")
      : t("component.error.key_taken"),
  };
  const error =
    commands.error ??
    (previewed === key ? (preview.error ?? qualified.error) : null);
  const refusal = refusalOf(error, t, keyField, own);
  useFocusRefusal(form, error as object | null);
  const ready =
    previewed === key &&
    preview.isSuccess &&
    (!variable || qualified.isSuccess) &&
    !tooLong;
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (key === renamed.key) {
      onClose();
      return;
    }
    if (previewed !== key) {
      commands.reset();
      setPreviewed(key);
      return;
    }
    if (!ready) {
      return;
    }
    commands.mutate(
      [
        variable
          ? {
              type: "variable.rename",
              payload: { id: renamed.id, key },
              expected_rev: { [renamed.id]: rev },
            }
          : {
              type: "component.set_key",
              payload: { id: renamed.id, key },
              expected_rev: { [renamed.id]: rev },
            },
      ],
      { onSuccess: onClose },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("rename.title", { key: renamed.key })}
        description={
          variable ? t("rename.intro_variable") : t("rename.intro_component")
        }
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("rename.new_key")}
            hint={t("content.key_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            required
            maxLength={64}
            className="min-h-11 w-full rounded-md border border-border bg-canvas px-3 font-mono text-fg"
            value={key}
            onChange={(event) => setKey(event.currentTarget.value)}
            error={refusal?.fields.key}
          />
          {previewed === key && key !== renamed.key && (
            <div className="space-y-4">
              <PreviewList
                query={renamedQuery}
                title={t("rename.renamed")}
                empty={t("rename.renamed_empty")}
              />
              {variable && (
                <PreviewList
                  query={qualifiedQuery}
                  title={t("rename.qualified")}
                  empty={t("rename.qualified_empty")}
                />
              )}
              {tooLong && <p className="text-danger">{t("rename.too_long")}</p>}
            </div>
          )}
          <div className="flex flex-wrap justify-end gap-2">
            <Dialog.Close asChild>
              <Button disabled={commands.isPending}>
                {t("boards.cancel")}
              </Button>
            </Dialog.Close>
            <Button
              type="submit"
              variant="primary"
              disabled={
                commands.isPending ||
                (previewed === key && key !== renamed.key && !ready)
              }
            >
              {previewed === key || key === renamed.key
                ? t("rename.submit")
                : t("rename.preview")}
            </Button>
          </div>
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
