import type { Variable } from "@storycreep/shared/api";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useDeferredValue, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ContentTree, FilterField, itemButton } from "../content/ContentTree";
import { typeName } from "../content/ValueField";
import {
  useDocumentState,
  useProjectDocument,
  useTree,
} from "../document/react";
import { editsProject, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { canonicalId, found } from "../shell/found";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { SelectField } from "../ui/Field";
import { Page, Status } from "../ui/Page";
import { Panel } from "../ui/Panel";
import { CreateVariableDialog } from "../variables/CreateVariableDialog";
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
  // The project comes first, as for the tree of its boards, and then the
  // variables and the boards, which a board's variable is of, at once. What
  // the document held already, which the real-time channel does not keep
  // current while it is not live, is shown as it is, and read anew.
  loader: async ({ context: { queryClient, documents }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const doc = documents.get(project.id);
    const held = doc.holds("variables");
    await Promise.all([found(doc.load("variables")), found(doc.load("tree"))]);
    if (held) {
      void doc.refresh("variables").catch(() => undefined);
    }
  },
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
  // The filter's text, which the list follows in a transition, so that
  // typing in the filter of a thousand variables stays quick (FilterField),
  // and its scope, which the list follows once it can.
  const [filter, setFilter] = useState("");
  const [scope, setScope] = useState(anyScope);
  const shownScope = useDeferredValue(scope);
  const matches = useMemo(() => {
    const text = filter.trim().toLocaleLowerCase();
    if (text === "" && shownScope === anyScope) {
      return undefined;
    }
    return itemsInOrder(tree).filter(({ item }) => {
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
  }, [tree, filter, shownScope]);
  const boardNames = useMemo(
    () => new Map(boards.map((board) => [board.id, board.name])),
    [boards],
  );
  const select = (id: string | undefined) =>
    void navigate({ search: id === undefined ? {} : { variable: id } });
  const [creating, setCreating] = useState<{
    parentId: string | undefined;
    opener: Opener;
  }>();
  const [created, setCreated] = useState<string>();
  // While a variable's deletion is open, its panel shows it as it was, as
  // the document takes it away at once, once its deletion is sent, so that
  // the confirmation stays, and says what became of it.
  const [deleting, setDeleting] = useState(false);
  const last = useRef<Variable>(undefined);
  const selected =
    selectedId === undefined ? undefined : variables.get(selectedId);
  if (selected !== undefined) {
    last.current = selected;
  }
  const shown =
    selected ??
    (deleting && last.current?.id === selectedId ? last.current : undefined);
  const listId = useId();
  const scopeOf = (variable: Variable) =>
    variable.scope === "global"
      ? t("variable.scope_global")
      : t("variable.scope_of", {
          board:
            boardNames.get(variable.board_id ?? "") ??
            t("boards.unknown_board"),
        });
  return (
    <Page
      title={t("variables.title")}
      documentTitle={t("variables.page_title", { project: project.name })}
      width="wide"
    >
      <p className="text-fg-muted">{t("variables.intro")}</p>
      <div
        className={
          shown === undefined
            ? ""
            : "grid items-start gap-6 min-[900px]:grid-cols-[minmax(0,1fr)_24rem]"
        }
      >
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
              onDeleting={setDeleting}
              onDeleted={() => {
                setDeleting(false);
                select(undefined);
              }}
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
