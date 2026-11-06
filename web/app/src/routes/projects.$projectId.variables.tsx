import type { Variable } from "@storycreep/shared/api";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useDeferredValue, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ContentTree, FilterField, itemButton } from "../content/ContentTree";
import { typeName } from "../content/ValueField";
import {
  useDocumentState,
  useProjectDocument,
  useTree,
} from "../document/react";
import { editsProject, loadParts, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { canonicalId } from "../shell/found";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { SelectField } from "../ui/Field";
import { Page, Status } from "../ui/Page";
import { besidePanel, Panel, usePanelItem } from "../ui/Panel";
import { CreateVariableDialog } from "../variables/CreateVariableDialog";
import { scopeName } from "../variables/fields";
import { VariablePanel } from "../variables/VariablePanel";

/** The search of the page: the variable whose panel is open, if any. */
interface VariablesSearch {
  variable?: string;
}

export const Route = createFileRoute("/projects/$projectId/variables")({
  validateSearch: (search: Record<string, unknown>): VariablesSearch =>
    typeof search.variable === "string" && search.variable !== ""
      ? { variable: canonicalId("variable", search.variable) }
      : {},
  // The variables, and the boards, which a board's variable is of.
  loader: ({ context, params }) =>
    loadParts(context, params.projectId, ["variables", "tree"]),
  // As for the tree of the project's boards (projects.$projectId.index.tsx).
  gcTime: 0,
  component: VariablesPage,
});

/** The value of the choice of the scope filter that shows every variable. */
const anyScope = "";
/** The value of the choice of the scope filter that shows the global variables. */
const globalScope = "global";

// A project's variables (PRD FR-VAR-01 to FR-VAR-05): the list of them in the
// tree of their folders, which a filter narrows to those whose key or
// description holds its text, and to a scope, and the panel of the one that
// the page's address names, beside the list, or a sheet over it on a narrow
// window (NFR-TOUCH-06). It shows them as the project's document has them,
// so that a change that another session makes shows as it commits.
function VariablesPage() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const { variable: selectedId } = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (state) => state.view.variables);
  const folders = useDocumentState(doc, (state) => state.view.folders);
  const boardData = useTree(doc);
  // The project's boards, in the order of their tree.
  const boards = useMemo(
    () =>
      itemsInOrder(buildTree(boardData.folders, boardData.boards)).map(
        (node) => node.item,
      ),
    [boardData],
  );
  const tree = useMemo(
    () =>
      buildTree(
        [...folders.values()].filter((f) => f.kind === "variable"),
        [...variables.values()],
        (variable: Variable) => variable.key,
      ),
    [folders, variables],
  );
  const boardNames = useMemo(
    () => new Map(boards.map((board) => [board.id, board.name])),
    [boards],
  );
  // The filter's text, which the list follows in a transition, so that
  // typing in the filter of a thousand variables stays quick (FilterField),
  // and its scope, which the list follows once it can. A board that the
  // user chose, which the list of boards no longer has, as one that went to
  // the trash elsewhere, is none of the choices, so the filter is of every
  // scope then, as its choice shows.
  const [filter, setFilter] = useState("");
  const [chosenScope, setScope] = useState(anyScope);
  const scope =
    chosenScope === anyScope ||
    chosenScope === globalScope ||
    boardNames.has(chosenScope)
      ? chosenScope
      : anyScope;
  const shownScope = useDeferredValue(scope);
  // The variables in the order of the tree, which the filter does not walk
  // again at each key.
  const ordered = useMemo(() => itemsInOrder(tree), [tree]);
  const matches = useMemo(() => {
    const text = filter.trim().toLocaleLowerCase();
    if (text === "" && shownScope === anyScope) {
      return undefined;
    }
    return ordered.filter(({ item }) => {
      const inScope =
        shownScope === anyScope ||
        (shownScope === globalScope
          ? item.scope === "global"
          : item.board_id === shownScope);
      return (
        inScope &&
        (text === "" ||
          item.key.toLocaleLowerCase().includes(text) ||
          item.description.toLocaleLowerCase().includes(text))
      );
    });
  }, [ordered, filter, shownScope]);
  // Closing the panel once its variable has been deleted replaces the
  // address that named it, as a board's deletion does, so that Back does
  // not show it as not found.
  const select = (id: string | undefined, replace = false) =>
    void navigate({
      search: id === undefined ? {} : { variable: id },
      replace,
    });
  const [creating, setCreating] = useState<{
    parentId: string | undefined;
    opener: Opener;
  }>();
  const [created, setCreated] = useState<string>();
  // While a variable's deletion is open, its panel shows it as it was.
  const { shown, onDeleting } = usePanelItem(variables, selectedId);
  const listId = useId();
  const scopeOf = (variable: Variable) =>
    scopeName(t, variable.scope, boardNames.get(variable.board_id ?? ""));
  return (
    <Page
      title={t("variables.title")}
      documentTitle={t("variables.page_title", { project: project.name })}
      width="wide"
    >
      <p className="text-fg-muted">{t("variables.intro")}</p>
      <div className={shown === undefined ? "" : besidePanel}>
        <section aria-labelledby={listId} className="min-w-0 space-y-4">
          <ContentTree
            projectId={projectId}
            tree={tree}
            folderKind="variable"
            editable={editable}
            labelledBy={listId}
            matches={matches}
            selected={selectedId}
            onSelect={select}
            newItemHere={t("variables.new_here")}
            onCreateIn={(parentId, opener) => setCreating({ parentId, opener })}
            renderItem={(variable) => (
              <>
                <span className="font-mono break-all">{variable.key}</span>
                <span className="text-sm break-words text-fg-muted">
                  {t("variables.item_detail", {
                    type: typeName(t, variable.type),
                    scope: scopeOf(variable),
                  })}
                </span>
              </>
            )}
            header={(newFolder) => (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <h2 id={listId} className="flex-1 text-lg font-semibold">
                    {t("variables.list")}
                  </h2>
                  {editable && (
                    <>
                      <Button
                        variant="primary"
                        aria-haspopup="dialog"
                        onClick={(event) =>
                          setCreating({
                            parentId: undefined,
                            opener: event.currentTarget,
                          })
                        }
                      >
                        {t("variables.new")}
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
                <div className="grid gap-2 sm:grid-cols-2">
                  <FilterField
                    label={t("variables.filter")}
                    onFilter={setFilter}
                  />
                  <SelectField
                    label={t("variables.filter_scope")}
                    value={scope}
                    onChange={(event) => setScope(event.currentTarget.value)}
                  >
                    <option value={anyScope}>{t("variables.any_scope")}</option>
                    <option value={globalScope}>
                      {t("variable.scope_global")}
                    </option>
                    {boards.map((board) => (
                      <option key={board.id} value={board.id}>
                        {t("variable.scope_of", { board: board.name })}
                      </option>
                    ))}
                  </SelectField>
                </div>
                <Status
                  message={
                    created === undefined
                      ? undefined
                      : t("variables.created", { key: created })
                  }
                />
                <p role="status" className="text-fg-muted">
                  {matches === undefined
                    ? t("variables.count", { count: variables.size })
                    : t("variables.matching", {
                        count: matches.length,
                        total: variables.size,
                      })}
                </p>
                {selectedId !== undefined && shown === undefined && (
                  <p className="text-fg-muted">{t("variables.not_found")}</p>
                )}
              </>
            )}
          />
          {variables.size === 0 && tree.top.length === 0 && (
            <p className="text-fg-muted">{t("variables.empty")}</p>
          )}
        </section>
        {shown !== undefined && (
          <Panel
            key={shown.id}
            title={shown.key}
            subtitle={scopeOf(shown)}
            onClose={() => select(undefined)}
            returnFocus={() => itemButton(shown.id)}
          >
            <VariablePanel
              project={project}
              variable={shown}
              boards={boards}
              tree={tree}
              editable={editable}
              onDeleting={onDeleting}
              onDeleted={() => select(undefined, true)}
            />
          </Panel>
        )}
      </div>
      {creating !== undefined && (
        <CreateVariableDialog
          projectId={projectId}
          boards={boards}
          tree={tree}
          parentId={creating.parentId}
          opener={creating.opener}
          onClose={() => setCreating(undefined)}
          onCreated={(id, key) => {
            setCreated(key);
            select(id);
          }}
        />
      )}
    </Page>
  );
}
