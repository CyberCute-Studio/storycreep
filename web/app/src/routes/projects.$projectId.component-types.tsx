import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { CreateTypeDialog } from "../components/CreateTypeDialog";
import { TypeGlyph, TypePanel } from "../components/TypePanel";
import { typesByName } from "../components/types";
import { ItemButton, itemButton } from "../content/ContentTree";
import { useDocumentState, useProjectDocument } from "../document/react";
import { editsProject, loadParts, projectQuery } from "../project/queries";
import { canonicalId } from "../shell/found";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { Page, Status } from "../ui/Page";
import { besidePanel, Panel, usePanelItem } from "../ui/Panel";

/** The search of the page: the type whose panel is open, if any. */
interface TypesSearch {
  type?: string;
}

export const Route = createFileRoute("/projects/$projectId/component-types")({
  validateSearch: (search: Record<string, unknown>): TypesSearch =>
    typeof search.type === "string" && search.type !== ""
      ? { type: canonicalId("component_type", search.type) }
      : {},
  // The types are of the components' part (projects.$projectId.components.tsx).
  loader: ({ context, params }) =>
    loadParts(context, params.projectId, ["components"]),
  gcTime: 0,
  component: TypesPage,
});

// A project's component types (PRD FR-COMP-01): the list of them, by their
// names, and the panel of the one that the page's address names, with its
// attribute definitions, beside the list, or a sheet over it on a narrow
// window (NFR-TOUCH-06).
function TypesPage() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const { type: selectedId } = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  const doc = useProjectDocument(projectId);
  const typeMap = useDocumentState(doc, (state) => state.view.componentTypes);
  const types = useMemo(() => typesByName(typeMap), [typeMap]);
  // As on the variables' page, a deletion replaces the address that named
  // what it deleted.
  const select = (id: string | undefined, replace = false) =>
    void navigate({ search: id === undefined ? {} : { type: id }, replace });
  const [creating, setCreating] = useState<Opener>();
  const [created, setCreated] = useState<string>();
  // While a type's deletion is open, its panel shows it as it was.
  const { shown, onDeleting } = usePanelItem(typeMap, selectedId);
  const listId = useId();
  return (
    <Page
      title={t("component_types.title")}
      documentTitle={t("component_types.page_title", { project: project.name })}
      width="wide"
    >
      <p className="text-fg-muted">{t("component_types.intro")}</p>
      <div className={shown === undefined ? "" : besidePanel}>
        <section aria-labelledby={listId} className="min-w-0 space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id={listId} className="flex-1 text-lg font-semibold">
              {t("component_types.list")}
            </h2>
            {editable && (
              <Button
                variant="primary"
                aria-haspopup="dialog"
                onClick={(event) => setCreating(event.currentTarget)}
              >
                {t("component_types.new")}
              </Button>
            )}
          </div>
          <Status
            message={
              created === undefined
                ? undefined
                : t("boards.created", { name: created })
            }
          />
          {selectedId !== undefined && shown === undefined && (
            <p className="text-fg-muted">{t("component_types.not_found")}</p>
          )}
          {types.length === 0 ? (
            <p className="text-fg-muted">{t("component_types.empty")}</p>
          ) : (
            <ul className="space-y-1" aria-labelledby={listId}>
              {types.map((type) => (
                <li key={type.id}>
                  <ItemButton
                    id={type.id}
                    selected={selectedId === type.id}
                    onSelect={select}
                  >
                    <span className="break-words">
                      <TypeGlyph icon={type.icon} />
                      {type.name}
                    </span>
                    <span className="text-sm break-all text-fg-muted">
                      {t("component_types.item_detail", {
                        key: type.key,
                        count: type.attributes.length,
                      })}
                    </span>
                  </ItemButton>
                </li>
              ))}
            </ul>
          )}
        </section>
        {shown !== undefined && (
          <Panel
            key={shown.id}
            title={shown.name}
            subtitle={shown.key}
            onClose={() => select(undefined)}
            returnFocus={() => itemButton(shown.id)}
          >
            <TypePanel
              project={project}
              type={shown}
              editable={editable}
              onDeleting={onDeleting}
              onDeleted={() => select(undefined, true)}
            />
          </Panel>
        )}
      </div>
      {creating !== undefined && (
        <CreateTypeDialog
          projectId={projectId}
          opener={creating}
          onClose={() => setCreating(undefined)}
          onCreated={(id, name) => {
            setCreated(name);
            select(id);
          }}
        />
      )}
    </Page>
  );
}
