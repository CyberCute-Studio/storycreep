// What the dependency view inspects of what is selected in it (PRD FR-DEP-02,
// plan note N77): of a pair of boards, each kind of edge between them, with
// its contributions, a page at a time, each the variable or the attribute
// that a site of the one board writes and a site of the other reads, named
// as an expression of the other names it, with the two sites, the jumper of
// the one whose target is a node of the other, or the node of the one whose
// visit an expression of the other reads (AC2), each site a link that opens
// its node on its board's canvas, or its connection; and of a board, its
// badge's counts, a link to its canvas, and one that keeps the view to its
// edges.

import type {
  Contribution,
  Edge,
  End,
  GraphBoard,
} from "@storycreep/shared/api";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { notFoundBy } from "../api";
import { refusalOf } from "../auth/refusal";
import { nodeTitleText, type ValueNamer } from "../content/textNames";
import { useTextNames } from "../content/useTextNames";
import { useProjectDocument } from "../document/react";
import { Button } from "../ui/Button";
import { Alert, textLink } from "../ui/Page";
import { contributionsQuery, type DependencyFilters } from "./queries";

/** Returns what an end of a contribution is: a node itself, as a jumper, its target, or a node visited, or a part of one, as a usage's site names it (usage.site). */
export function endText(t: TFunction, end: End): string {
  const node = nodeTitleText(t, end.node_title);
  return end.site === "node" ? node : t("usage.site", { site: end.site, node });
}

/** An end of a contribution, a link that opens its node on the canvas of boardId, and its connection where it is of one. */
function EndLink({
  projectId,
  boardId,
  end,
}: {
  projectId: string;
  boardId: string;
  end: End;
}) {
  const { t } = useTranslation();
  return (
    <Link
      to="/projects/$projectId/boards/$boardId"
      params={{ projectId, boardId }}
      search={{
        node: end.node_id,
        ...(end.connection_id === undefined
          ? {}
          : { connection: end.connection_id }),
      }}
      className={textLink}
    >
      {endText(t, end)}
    </Link>
  );
}

/** A contribution to an edge, with its ends, as its kind names them, and its variable or its attribute, as nameOf names it. */
function ContributionItem({
  projectId,
  edge,
  contribution,
  nameOf,
}: {
  projectId: string;
  edge: Edge;
  contribution: Contribution;
  nameOf: ValueNamer;
}) {
  const { t } = useTranslation();
  const { from, to } = contribution;
  const value =
    edge.kind === "variable"
      ? (nameOf(contribution.variable_id ?? "", undefined) ??
        t("dependencies.a_variable"))
      : edge.kind === "attribute"
        ? (nameOf(
            contribution.component_id ?? "",
            contribution.attribute_id ?? "",
          ) ?? t("dependencies.an_attribute"))
        : undefined;
  return (
    <li className="space-y-1 border-t border-border py-2">
      {value !== undefined && (
        <p>
          <code className="break-words">{value}</code>
        </p>
      )}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1">
        <dt className="text-fg-muted">
          {t("dependencies.from_end", { kind: edge.kind })}
        </dt>
        <dd className="break-words">
          <EndLink
            projectId={projectId}
            boardId={edge.from_board_id}
            end={from}
          />
        </dd>
        <dt className="text-fg-muted">
          {t("dependencies.to_end", { kind: edge.kind })}
        </dt>
        <dd className="break-words">
          <EndLink projectId={projectId} boardId={edge.to_board_id} end={to} />
        </dd>
      </dl>
    </li>
  );
}

/** The contributions to an edge, a page at a time, under the heading of its kind and their count. */
function EdgeContributions({
  projectId,
  edge,
  filters,
}: {
  projectId: string;
  edge: Edge;
  filters: DependencyFilters;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  // The variables and the attributes as an expression of the board that
  // depends on the other names them, `gold`, `global.gold` where the board
  // has a gold of its own, or `elara.trust` (valueNamer), as the project's
  // document has them, which it reads if the view's loader has not.
  const nameOf = useTextNames(projectId, edge.to_board_id).value;
  const list = useInfiniteQuery(
    contributionsQuery(
      doc,
      { from: edge.from_board_id, to: edge.to_board_id, kind: edge.kind },
      filters.variable,
    ),
  );
  // The server answers not_found once a board of the pair has gone to the
  // trash, as another session's deletion, which has the graph read again as
  // it has the contributions, and whose answer takes the pair, and so this
  // inspection, away: the inspection says meanwhile that the pair's board
  // has gone, in place of the contributions read before, rather than show a
  // refusal of it.
  const gone = notFoundBy(list.error);
  const refusal = gone ? undefined : refusalOf(list.error, t);
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="space-y-2">
      <h3 className="font-semibold">
        {t("dependencies.kind_heading", {
          kind: edge.kind,
          count: edge.contributions,
        })}
      </h3>
      <Alert message={refusal?.message} />
      {gone ? (
        <p className="text-fg-muted">{t("dependencies.pair_gone")}</p>
      ) : list.isPending ? (
        <p className="text-fg-muted">{t("dependencies.loading")}</p>
      ) : (
        <ul>
          {items.map((c) => (
            <ContributionItem
              key={[
                c.variable_id ?? c.component_id ?? "",
                c.attribute_id ?? "",
                c.from.site,
                c.from.connection_id ?? c.from.branch_output_id ?? "",
                c.from.node_id,
                c.to.site,
                c.to.connection_id ?? c.to.branch_output_id ?? "",
                c.to.node_id,
              ].join(":")}
              projectId={projectId}
              edge={edge}
              contribution={c}
              nameOf={nameOf}
            />
          ))}
        </ul>
      )}
      {!gone && list.hasNextPage && (
        <Button
          disabled={list.isFetchingNextPage}
          onClick={() => void list.fetchNextPage()}
        >
          {t("dependencies.more")}
        </Button>
      )}
    </section>
  );
}

/**
 * The inspection of the pair of boards from and to: each edge from the one
 * to the other, of each kind, with its contributions (AC2).
 */
export function PairInspector({
  projectId,
  from,
  to,
  edges,
  filters,
}: {
  projectId: string;
  from: GraphBoard;
  to: GraphBoard;
  /** The edges from from to to, in the order of their kinds. */
  edges: readonly Edge[];
  filters: DependencyFilters;
}) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="dependencies-inspected" className="space-y-4">
      {/* Of the page's second level, as the heading of the list of every
      pair is, which the inspection, beside the graph, comes before. */}
      <h2
        id="dependencies-inspected"
        tabIndex={-1}
        className="text-lg font-semibold"
      >
        {t("dependencies.pair_heading", { from: from.name, to: to.name })}
      </h2>
      <p>{t("dependencies.pair_intro", { from: from.name, to: to.name })}</p>
      {edges.map((edge) => (
        <EdgeContributions
          key={edge.kind}
          projectId={projectId}
          edge={edge}
          filters={filters}
        />
      ))}
    </section>
  );
}

/** The inspection of a board: its badge's counts, a link to its canvas, and one that keeps the view to the edges that leave it or enter it. */
export function BoardInspector({
  projectId,
  board,
  filters,
}: {
  projectId: string;
  board: GraphBoard;
  /** The view's filters, which its link keeps but for the board's. */
  filters: DependencyFilters;
}) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="dependencies-inspected" className="space-y-3">
      <h2
        id="dependencies-inspected"
        tabIndex={-1}
        className="text-lg font-semibold"
      >
        {board.name}
      </h2>
      <p>
        {t("dependencies.board_summary", {
          inbound: board.inbound,
          outbound: board.outbound,
        })}
      </p>
      <ul className="space-y-1">
        <li>
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: board.id }}
            className={textLink}
          >
            {t("dependencies.open_board", { name: board.name })}
          </Link>
        </li>
        {filters.board !== board.id && (
          <li>
            <Link
              to="/projects/$projectId/dependencies"
              params={{ projectId }}
              search={{
                ...(filters.kind === undefined ? {} : { kind: filters.kind }),
                board: board.id,
                ...(filters.variable === undefined
                  ? {}
                  : { variable: filters.variable }),
              }}
              className={textLink}
            >
              {t("dependencies.only_board", { name: board.name })}
            </Link>
          </li>
        )}
      </ul>
    </section>
  );
}
