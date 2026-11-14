// The dependencies between a project's boards (PRD FR-DEP-02, plan notes
// N56 and N77), as the server reads them of its draft: the graph of its
// boards, whose edges are of variables, attributes, jumpers, and visits, and
// the contributions to an edge. The project's document has every read of
// them read again as its operations apply (Follower), so that the view and
// a board's badge follow an edit, the page's own or another session's.

import type { Edge, Graph, PageContribution } from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { api, pagedQuery } from "../api";
import { sentWait, whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";

/** The key of every read of the dependencies of projectId, which each operation of it has read again. */
export function dependenciesKey(projectId: string) {
  return ["projects", projectId, "dependencies"] as const;
}

/** A kind of dependency. */
export type DependencyKind = Edge["kind"];

/** The kinds of dependency, in the order that the server lists an edge's. */
export const dependencyKinds: readonly DependencyKind[] = [
  "variable",
  "attribute",
  "jumper",
  "visit",
];

/** What the view keeps of the graph: the edges of a kind, of a board, or of a variable, each of which none means all of. */
export interface DependencyFilters {
  kind?: DependencyKind;
  board?: string;
  variable?: string;
}

/**
 * The dependency graph of the project of doc, of the edges that filters
 * keep, read once the page's batches have been answered, or after
 * sentWait, so that it is of what the page shows.
 */
export function graphQuery(
  doc: ProjectDocument,
  filters: DependencyFilters = {},
) {
  return queryOptions({
    queryKey: [
      ...dependenciesKey(doc.projectId),
      "graph",
      filters.kind ?? "",
      filters.board ?? "",
      filters.variable ?? "",
    ] as const,
    queryFn: async ({ signal }): Promise<Graph> => {
      await whenSent(doc, signal, sentWait);
      return (
        await api.call(
          "getDependencyGraph",
          {
            path: { project_id: doc.projectId },
            query: {
              ...(filters.kind === undefined ? {} : { kind: filters.kind }),
              ...(filters.board === undefined
                ? {}
                : { board_id: filters.board }),
              ...(filters.variable === undefined
                ? {}
                : { variable_id: filters.variable }),
            },
          },
          { signal },
        )
      ).body;
    },
    meta: { showsNotFound: true },
  });
}

/** An edge of the graph, as its contributions' list names it: the board that it leaves, the board that it enters, and its kind. */
export interface EdgeOf {
  from: string;
  to: string;
  kind: DependencyKind;
}

/**
 * The contributions to the edge of the project of doc, which a variable's
 * filter keeps those of alone, a page at a time, each read once the page's
 * batches have been answered, or after sentWait, as the graph is.
 */
export function contributionsQuery(
  doc: ProjectDocument,
  edge: EdgeOf,
  variable: string | undefined,
) {
  return pagedQuery(
    [
      ...dependenciesKey(doc.projectId),
      "contributions",
      edge.from,
      edge.to,
      edge.kind,
      variable ?? "",
    ],
    async (query, signal): Promise<PageContribution> => {
      await whenSent(doc, signal, sentWait);
      return (
        await api.call(
          "listDependencyContributions",
          {
            path: {
              project_id: doc.projectId,
              from_board_id: edge.from,
              to_board_id: edge.to,
            },
            query: {
              ...query,
              kind: edge.kind,
              ...(variable === undefined ? {} : { variable_id: variable }),
            },
          },
          { signal },
        )
      ).body;
    },
    { meta: { showsRefusals: true } },
  );
}
