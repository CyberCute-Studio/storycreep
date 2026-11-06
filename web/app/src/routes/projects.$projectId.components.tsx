import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useDeferredValue, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ComponentPanel } from "../components/ComponentPanel";
import { CreateComponentDialog } from "../components/CreateComponentDialog";
import { liveTypes } from "../components/types";
import { ContentTree, FilterField, itemButton } from "../content/ContentTree";
import type { ComponentEntry } from "../document/doc";
import { useDocumentState, useProjectDocument } from "../document/react";
import { editsProject, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { canonicalId, found } from "../shell/found";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { SelectField } from "../ui/Field";
import { Page, Status, textLink } from "../ui/Page";
import { besidePanel, Panel, usePanelItem } from "../ui/Panel";

/** The search of the page: the component whose panel is open, if any. */
interface ComponentsSearch {
  component?: string;
}

export const Route = createFileRoute("/projects/$projectId/components")({
  validateSearch: (search: Record<string, unknown>): ComponentsSearch =>
    typeof search.component === "string" && search.component !== ""
      ? { component: canonicalId("component", search.component) }
      : {},
  // As for the variables (projects.$projectId.variables.tsx): the
  // components' part holds their types and folders too.
  loader: async ({ context: { queryClient, documents }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const doc = documents.get(project.id);
    const held = doc.holds("components");
    await found(doc.load("components"));
    if (held) {
      void doc.refresh("components").catch(() => undefined);
    }
  },
  gcTime: 0,
  component: ComponentsPage,
});

/** The value of the choice of the type filter that shows every component. */
const anyType = "";

// A project's components (PRD FR-COMP-02, FR-COMP-04): the list of them in
// the tree of their folders, which a filter narrows to those whose name or
// key holds its text, compared without regard to case, and to a type (AC3),
// and the panel of the one that the page's address names, beside the list,
// or a sheet over it on a narrow window (NFR-TOUCH-06).
function ComponentsPage() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const { component: selectedId } = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  const doc = useProjectDocument(projectId);
  const components = useDocumentState(doc, (state) => state.view.components);
  const typeMap = useDocumentState(doc, (state) => state.view.componentTypes);
  const folders = useDocumentState(doc, (state) => state.view.folders);
  const types = useMemo(() => liveTypes(typeMap), [typeMap]);
  const tree = useMemo(
    () =>
      buildTree(
        [...folders.values()].filter((f) => f.kind === "component"),
        [...components.values()],
        (component: ComponentEntry) => component.name,
      ),
    [folders, components],
  );
  // The filter's text, which the list follows in a transition (FilterField),
  // and its type, which the list follows once it can. A type that the user
  // chose, which went to the trash since, is none of the choices, so the
  // filter is of every type then, as its choice shows.
  const [filter, setFilter] = useState("");
  const [chosenType, setTypeId] = useState(anyType);
  const typeId = types.some((type) => type.id === chosenType)
    ? chosenType
    : anyType;
  const shownType = useDeferredValue(typeId);
  const matches = useMemo(() => {
    const text = filter.trim().toLocaleLowerCase();
    if (text === "" && shownType === anyType) {
      return undefined;
    }
    return itemsInOrder(tree).filter(
      ({ item }) =>
        (shownType === anyType || item.component_type_id === shownType) &&
        (text === "" ||
          item.name.toLocaleLowerCase().includes(text) ||
          item.key.toLocaleLowerCase().includes(text)),
    );
  }, [tree, filter, shownType]);
  // As on the variables' page, a deletion replaces the address that named
  // what it deleted.
  const select = (id: string | undefined, replace = false) =>
    void navigate({
      search: id === undefined ? {} : { component: id },
      replace,
    });
  const [creating, setCreating] = useState<{
    parentId: string | undefined;
    opener: Opener;
  }>();
  const [created, setCreated] = useState<string>();
  // While a component's deletion is open, its panel shows it as it was.
  const { shown, onDeleting } = usePanelItem(components, selectedId);
  const listId = useId();
  const typeName = (id: string) =>
    typeMap.get(id)?.name ?? t("component.unknown_type");
  return (
    <Page
      title={t("components.title")}
      documentTitle={t("components.page_title", { project: project.name })}
      width="wide"
    >
      <p className="text-fg-muted">{t("components.intro")}</p>
      <div className={shown === undefined ? "" : besidePanel}>
        <section aria-labelledby={listId} className="min-w-0 space-y-4">
          <ContentTree
            projectId={projectId}
            tree={tree}
            folderKind="component"
            editable={editable}
            labelledBy={listId}
            matches={matches}
            selected={selectedId}
            onSelect={select}
            newItemHere={t("components.new_here")}
            createsItems={types.length > 0}
            onCreateIn={(parentId, opener) => setCreating({ parentId, opener })}
            renderItem={(component) => (
              <>
                <span className="break-words">{component.name}</span>
                <span className="text-sm break-all text-fg-muted">
                  {t("components.item_detail", {
                    key: component.key,
                    type: typeName(component.component_type_id),
                  })}
                </span>
              </>
            )}
            header={(newFolder) => (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <h2 id={listId} className="flex-1 text-lg font-semibold">
                    {t("components.list")}
                  </h2>
                  {editable && (
                    <>
                      <Button
                        variant="primary"
                        aria-haspopup="dialog"
                        disabled={types.length === 0}
                        onClick={(event) =>
                          setCreating({
                            parentId: undefined,
                            opener: event.currentTarget,
                          })
                        }
                      >
                        {t("components.new")}
                      </Button>
                      <Button
                        aria-haspopup="dialog"
                        onClick={(event) => newFolder(event.currentTarget)}
                      >
                        {t("boards.new_folder")}
                      </Button>
                    </>
                  )}
                </div>
                {editable && types.length === 0 && (
                  <p>
                    {t("components.no_types")}{" "}
                    <Link
                      to="/projects/$projectId/component-types"
                      params={{ projectId }}
                      className={textLink}
                    >
                      {t("components.to_types")}
                    </Link>
                  </p>
                )}
                <div className="grid gap-2 sm:grid-cols-2">
                  <FilterField
                    label={t("components.filter")}
                    onFilter={setFilter}
                  />
                  <SelectField
                    label={t("components.filter_type")}
                    value={typeId}
                    onChange={(event) => setTypeId(event.currentTarget.value)}
                  >
                    <option value={anyType}>{t("components.any_type")}</option>
                    {types.map((type) => (
                      <option key={type.id} value={type.id}>
                        {type.name}
                      </option>
                    ))}
                  </SelectField>
                </div>
                <Status
                  message={
                    created === undefined
                      ? undefined
                      : t("boards.created", { name: created })
                  }
                />
                <p role="status" className="text-fg-muted">
                  {matches === undefined
                    ? t("components.count", { count: components.size })
                    : t("components.matching", {
                        count: matches.length,
                        total: components.size,
                      })}
                </p>
                {selectedId !== undefined && shown === undefined && (
                  <p className="text-fg-muted">{t("components.not_found")}</p>
                )}
              </>
            )}
          />
          {components.size === 0 && tree.top.length === 0 && (
            <p className="text-fg-muted">{t("components.empty")}</p>
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
            <ComponentPanel
              project={project}
              component={shown}
              type={typeMap.get(shown.component_type_id)}
              tree={tree}
              editable={editable}
              onDeleting={onDeleting}
              onDeleted={() => select(undefined, true)}
            />
          </Panel>
        )}
      </div>
      {creating !== undefined && (
        <CreateComponentDialog
          projectId={projectId}
          types={types}
          tree={tree}
          typeId={typeId === anyType ? undefined : typeId}
          parentId={creating.parentId}
          opener={creating.opener}
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
