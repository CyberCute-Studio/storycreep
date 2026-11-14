import type { Edge, GraphBoard } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { DependencyBench } from "../dependencies/bench";
import {
  DependencyGraph,
  type GraphSelection,
  pairKey,
} from "../dependencies/Graph";
import { BoardInspector, PairInspector } from "../dependencies/Inspector";
import {
  type DependencyFilters,
  type DependencyKind,
  dependencyKinds,
  graphQuery,
} from "../dependencies/queries";
import {
  useDocumentState,
  useProjectDocument,
  useTree,
} from "../document/react";
import { loadPart, loadParts, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { found, searchId } from "../shell/found";
import { Button } from "../ui/Button";
import { SelectField } from "../ui/Field";
import { useDocumentTitle } from "../ui/title";

/**
 * The search of the dependency view: the filters that keep its edges, of a
 * kind, of a board, and of a variable (PRD FR-DEP-02), which a board's badge
 * links to with its board, and a run of the view's benchmark, which reads
 * when the graph was first drawn (NFR-PERF-11).
 */
export interface DependenciesSearch {
  kind?: DependencyKind;
  board?: string;
  variable?: string;
  bench?: "load";
}

export const Route = createFileRoute("/projects/$projectId/dependencies")({
  // Each key of the search, undefined where the address holds nothing that
  // the page takes, which the router would otherwise give the page as the
  // address holds it, as it lays a route's search over that of the routes
  // above it, which take the whole address's: a kind that is no kind, and a
  // board's or a variable's ID that is no such ID (searchId), which the
  // server would refuse. One of a board or a variable that the project does
  // not have is kept, which the filter's choices offer as an unknown one,
  // and which keeps no edge.
  validateSearch: (search: Record<string, unknown>): DependenciesSearch => ({
    kind: dependencyKinds.includes(search.kind as DependencyKind)
      ? (search.kind as DependencyKind)
      : undefined,
    board: searchId("board", search.board),
    variable: searchId("variable", search.variable),
    bench: search.bench === "load" ? "load" : undefined,
  }),
  loaderDeps: ({ search }) => ({
    kind: search.kind,
    board: search.board,
    variable: search.variable,
  }),
  // The project first, then, together, the tree of its boards, by which the
  // view orders them, its variables, which its filter offers, and the graph.
  // Its components, which name the attributes of a pair's inspection alone,
  // start to load as the page does, without the graph's first drawing
  // waiting for them (NFR-PERF-11), and the inspection reads them if this
  // read failed (useTextNames).
  loader: async ({ context, params, deps }) => {
    await found(
      context.queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const doc = context.documents.get(params.projectId);
    loadPart(doc, "components").catch(() => {});
    await Promise.all([
      loadParts(context, params.projectId, ["tree", "variables"]),
      found(context.queryClient.ensureQueryData(graphQuery(doc, deps))),
    ]);
  },
  // The page takes the window's width, for its graph.
  staticData: { wide: true },
  gcTime: 0,
  component: Dependencies,
});

/** The value of a filter's choice of none, which keeps every edge. */
const all = "";

// A project's dependency view (PRD FR-DEP-02, plan note N77): its boards,
// each with its badge's counts, and the dependencies between them, each a
// line from the board that the other depends on, of edges of variables
// written on the one and read on the other, of attributes so, of jumpers,
// and of visits, which a press inspects, listing their contributions (AC2),
// filtered by kind, board, and variable, whatever cycles they make, which
// are drawn as any other and are no error (AC4). It follows the project's
// document: the graph is read again as each operation applies.
function Dependencies() {
  const { t, i18n } = useTranslation();
  const { projectId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const doc = useProjectDocument(projectId);
  const filters: DependencyFilters = useMemo(
    () => ({
      kind: search.kind,
      board: search.board,
      variable: search.variable,
    }),
    [search.kind, search.board, search.variable],
  );
  const graph = useSuspenseQuery(graphQuery(doc, filters)).data;
  useDocumentTitle(t("dependencies.page_title", { project: project.name }));
  const tree = useTree(doc);
  // The boards in the order of the tree, and after them, in the order of
  // their IDs, those that the tree does not hold yet, as one that another
  // session created as the graph was read.
  const boards = useMemo(() => {
    const byId = new Map(graph.boards.map((b) => [b.id, b]));
    const out: GraphBoard[] = [];
    for (const item of itemsInOrder(buildTree(tree.folders, tree.boards))) {
      const b = byId.get(item.item.id);
      if (b !== undefined) {
        out.push(b);
        byId.delete(b.id);
      }
    }
    return [...out, ...byId.values()];
  }, [graph.boards, tree]);
  const boardOf = useMemo(
    () => new Map(graph.boards.map((b) => [b.id, b])),
    [graph.boards],
  );
  // The pairs of boards that edges join, in the order of the graph's
  // edges, each with its edges, in the order of their kinds.
  const pairs = useMemo(() => {
    const out = new Map<string, { from: string; to: string; edges: Edge[] }>();
    for (const e of graph.edges) {
      const key = pairKey(e.from_board_id, e.to_board_id);
      let pair = out.get(key);
      if (pair === undefined) {
        pair = { from: e.from_board_id, to: e.to_board_id, edges: [] };
        out.set(key, pair);
      }
      pair.edges.push(e);
    }
    return out;
  }, [graph.edges]);
  const pairList = useMemo(() => [...pairs.values()], [pairs]);
  const [chosen, setChosen] = useState<GraphSelection>({});
  // A pair inspected from the list, below the graph, gives the focus to its
  // inspection, beside the graph or above the list, once it shows, so that
  // a keyboard and a screen reader go on there.
  const focusInspected = useRef(false);
  useEffect(() => {
    if (focusInspected.current) {
      focusInspected.current = false;
      document.getElementById("dependencies-inspected")?.focus();
    }
  });
  // What is selected, while the graph has it.
  const selection: GraphSelection = {
    pair:
      chosen.pair !== undefined && pairs.has(chosen.pair)
        ? chosen.pair
        : undefined,
    board:
      chosen.board !== undefined && boardOf.has(chosen.board)
        ? chosen.board
        : undefined,
  };
  const selectedPair =
    selection.pair === undefined ? undefined : pairs.get(selection.pair);
  const selectedBoard =
    selection.board === undefined ? undefined : boardOf.get(selection.board);
  const variables = useDocumentState(doc, (state) => state.view.variables);
  const boardNames = useDocumentState(doc, (state) => state.view.boards);
  const variableChoices = useMemo(
    () =>
      [...variables.values()]
        .map((v) => ({
          id: v.id,
          name:
            v.board_id === undefined
              ? v.key
              : t("dependencies.board_variable", {
                  key: v.key,
                  board:
                    boardNames.get(v.board_id)?.name ??
                    t("boards.unknown_board"),
                }),
        }))
        .sort((a, b) => a.name.localeCompare(b.name, i18n.language)),
    [variables, boardNames, t, i18n.language],
  );
  const setFilter = (change: Partial<DependencyFilters>) =>
    void navigate({
      search: (prev) => {
        const next = { ...prev, ...change };
        for (const key of ["kind", "board", "variable"] as const) {
          if (next[key] === undefined) {
            delete next[key];
          }
        }
        return next;
      },
    });
  const filtered =
    filters.kind !== undefined ||
    filters.board !== undefined ||
    filters.variable !== undefined;
  const [drawnAt, setDrawnAt] = useState<number>();
  const onDrawn = useCallback(() => setDrawnAt(performance.now()), []);
  const number = (n: number) => formatNumber(i18n.language, n);
  const nameOf = (id: string) =>
    boardOf.get(id)?.name ?? t("boards.unknown_board");
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">{t("dependencies.title")}</h1>
      <p className="max-w-3xl">{t("dependencies.intro")}</p>
      <form
        aria-label={t("dependencies.filters")}
        className="flex flex-wrap items-end gap-4"
        onSubmit={(event) => event.preventDefault()}
      >
        <SelectField
          label={t("dependencies.kind")}
          name="kind"
          value={filters.kind ?? all}
          onChange={(event) =>
            setFilter({
              kind:
                event.target.value === all
                  ? undefined
                  : (event.target.value as DependencyKind),
            })
          }
        >
          <option value={all}>{t("dependencies.any_kind")}</option>
          {dependencyKinds.map((kind) => (
            <option key={kind} value={kind}>
              {t("dependencies.kind_name", { kind })}
            </option>
          ))}
        </SelectField>
        <SelectField
          label={t("dependencies.board")}
          name="board"
          value={filters.board ?? all}
          onChange={(event) =>
            setFilter({
              board:
                event.target.value === all ? undefined : event.target.value,
            })
          }
        >
          <option value={all}>{t("dependencies.any_board")}</option>
          {boards.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
          {filters.board !== undefined && !boardOf.has(filters.board) && (
            <option value={filters.board}>{t("boards.unknown_board")}</option>
          )}
        </SelectField>
        <SelectField
          label={t("dependencies.variable")}
          name="variable"
          value={filters.variable ?? all}
          onChange={(event) =>
            setFilter({
              variable:
                event.target.value === all ? undefined : event.target.value,
            })
          }
        >
          <option value={all}>{t("dependencies.any_variable")}</option>
          {variableChoices.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name}
            </option>
          ))}
          {filters.variable !== undefined &&
            !variables.has(filters.variable) && (
              <option value={filters.variable}>
                {t("dependencies.a_variable")}
              </option>
            )}
        </SelectField>
        {filtered && (
          <Button
            onClick={() =>
              setFilter({
                kind: undefined,
                board: undefined,
                variable: undefined,
              })
            }
          >
            {t("dependencies.clear_filters")}
          </Button>
        )}
      </form>
      <p>
        {t("dependencies.summary", {
          boards: graph.boards.length,
          pairs: pairs.size,
        })}
      </p>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_24rem]">
        <DependencyGraph
          boards={boards}
          pairs={pairList}
          selection={selection}
          onSelectPair={(pair) => setChosen({ pair })}
          onSelectBoard={(board) => setChosen({ board })}
          onDrawn={search.bench === undefined ? undefined : onDrawn}
        />
        <aside aria-label={t("dependencies.inspector")} className="space-y-4">
          {selectedPair !== undefined ? (
            <PairInspector
              key={selection.pair}
              projectId={projectId}
              from={boardOf.get(selectedPair.from) as GraphBoard}
              to={boardOf.get(selectedPair.to) as GraphBoard}
              edges={selectedPair.edges}
              filters={filters}
            />
          ) : selectedBoard !== undefined ? (
            <BoardInspector
              projectId={projectId}
              board={selectedBoard}
              filters={filters}
            />
          ) : (
            <p className="text-fg-muted">{t("dependencies.select_hint")}</p>
          )}
        </aside>
      </div>
      <section aria-labelledby="dependencies-pairs" className="space-y-2">
        <h2 id="dependencies-pairs" className="text-xl font-semibold">
          {t("dependencies.pairs")}
        </h2>
        {pairList.length === 0 ? (
          <p>
            {filtered
              ? t("dependencies.none_filtered")
              : t("dependencies.none")}
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <caption className="sr-only">
                {t("dependencies.pairs_caption", { project: project.name })}
              </caption>
              <thead>
                <tr>
                  <th
                    scope="col"
                    className="pb-2 pe-4 text-start font-normal text-fg-muted"
                  >
                    {t("dependencies.depended_on")}
                  </th>
                  <th
                    scope="col"
                    className="pb-2 pe-4 text-start font-normal text-fg-muted"
                  >
                    {t("dependencies.depending")}
                  </th>
                  <th
                    scope="col"
                    className="pb-2 pe-4 text-start font-normal text-fg-muted"
                  >
                    {t("dependencies.kinds")}
                  </th>
                  <th
                    scope="col"
                    className="pb-2 pe-4 text-end font-normal text-fg-muted"
                  >
                    {t("dependencies.contributions")}
                  </th>
                  <th
                    scope="col"
                    className="pb-2 text-start font-normal text-fg-muted"
                  >
                    <span className="sr-only">{t("dependencies.inspect")}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {pairList.map((pair) => {
                  const key = pairKey(pair.from, pair.to);
                  return (
                    <tr key={key} className="border-t border-border">
                      <td className="py-2 pe-4 break-words">
                        {nameOf(pair.from)}
                      </td>
                      <td className="py-2 pe-4 break-words">
                        {nameOf(pair.to)}
                      </td>
                      <td className="py-2 pe-4">
                        {pair.edges
                          .map((e) =>
                            t("dependencies.kind_name", { kind: e.kind }),
                          )
                          .join(t("dependencies.kind_separator"))}
                      </td>
                      <td className="py-2 pe-4 text-end tabular-nums">
                        {number(
                          pair.edges.reduce((n, e) => n + e.contributions, 0),
                        )}
                      </td>
                      <td className="py-2">
                        <Button
                          aria-pressed={selection.pair === key}
                          aria-label={t("dependencies.inspect_pair", {
                            from: nameOf(pair.from),
                            to: nameOf(pair.to),
                          })}
                          onClick={() => {
                            focusInspected.current = true;
                            setChosen({ pair: key });
                          }}
                        >
                          {t("dependencies.inspect")}
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {search.bench !== undefined && (
        <DependencyBench
          drawnAt={drawnAt}
          boards={graph.boards.length}
          pairs={pairs.size}
          edges={graph.edges.length}
        />
      )}
    </div>
  );
}
