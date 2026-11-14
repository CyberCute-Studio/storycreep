import type { BoardStatistics } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useProjectDocument, useTree } from "../document/react";
import { loadParts, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { found } from "../shell/found";
import { CountList } from "../statistics/Counts";
import { projectStatisticsQuery } from "../statistics/queries";
import { Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/statistics")({
  // The project comes first, then, together, the tree of its boards, by
  // which the page orders them (loadParts), and the statistics, which need
  // nothing of the tree.
  loader: async ({ context, params }) => {
    await found(
      context.queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    await Promise.all([
      loadParts(context, params.projectId, ["tree"]),
      found(
        context.queryClient.ensureQueryData(
          projectStatisticsQuery(context.documents.get(params.projectId)),
        ),
      ),
    ]);
  },
  // As the tree's page, which reads the tree of the project's document,
  // which a later visit may find made anew (keptUnshown).
  gcTime: 0,
  component: Statistics,
});

// A project's writing statistics (PRD FR-TEXT-06, plan note N76): the words
// and the characters of its texts, and how many nodes, choices, and
// conditions it has, of the project and of each of its boards, in the order
// of the tree of its boards, each linked to its canvas, of what is not in
// its trash. The server counts each text as a batch changes it, and the
// page reads the statistics again as each operation of the project applies,
// so that they follow an edit, here or in another session, within a second
// (AC1). Where the server has yet to count some text, as content that
// predates the statistics, the page says so.
function Statistics() {
  const { t, i18n } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const doc = useProjectDocument(projectId);
  const stats = useSuspenseQuery(projectStatisticsQuery(doc)).data;
  const tree = useTree(doc);
  // The boards in the order of the tree, and after them, in the order of
  // their IDs, those that the tree does not hold yet, as one that another
  // session created as the statistics were read.
  const boards = useMemo(() => {
    const byId = new Map(stats.boards.map((b) => [b.id, b]));
    const out: BoardStatistics[] = [];
    for (const item of itemsInOrder(buildTree(tree.folders, tree.boards))) {
      const b = byId.get(item.item.id);
      if (b !== undefined) {
        out.push(b);
        byId.delete(b.id);
      }
    }
    return [...out, ...byId.values()];
  }, [stats.boards, tree]);
  const number = (n: number) => formatNumber(i18n.language, n);
  const heading = "pb-2 pe-4 text-end font-normal text-fg-muted";
  const cell = "py-2 pe-4 text-end tabular-nums";
  return (
    <Page
      title={t("statistics.title")}
      documentTitle={t("statistics.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("statistics.intro")}</p>
      <Status
        message={stats.complete ? undefined : t("statistics.incomplete")}
      />
      <section aria-labelledby="statistics-project" className="space-y-2">
        <h2 id="statistics-project" className="text-xl font-semibold">
          {t("statistics.project")}
        </h2>
        <CountList counts={stats} nodes={stats.nodes} />
      </section>
      <section aria-labelledby="statistics-boards" className="space-y-2">
        <h2 id="statistics-boards" className="text-xl font-semibold">
          {t("statistics.boards")}
        </h2>
        {boards.length === 0 ? (
          <p className="text-fg-muted">{t("statistics.no_boards")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <caption className="sr-only">
                {t("statistics.boards_caption", { project: project.name })}
              </caption>
              <thead>
                <tr>
                  <th
                    scope="col"
                    className="pb-2 pe-4 text-start font-normal text-fg-muted"
                  >
                    {t("statistics.board")}
                  </th>
                  <th scope="col" className={heading}>
                    {t("statistics.nodes")}
                  </th>
                  <th scope="col" className={heading}>
                    {t("statistics.words")}
                  </th>
                  <th scope="col" className={heading}>
                    {t("statistics.characters")}
                  </th>
                  <th scope="col" className={heading}>
                    {t("statistics.choices")}
                  </th>
                  <th scope="col" className={heading}>
                    {t("statistics.conditions")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {boards.map((b) => (
                  <tr key={b.id} className="border-t border-border">
                    <th
                      scope="row"
                      className="py-2 pe-4 text-start font-medium break-words"
                    >
                      <TextLink
                        to="/projects/$projectId/boards/$boardId"
                        params={{ projectId, boardId: b.id }}
                      >
                        {b.name}
                      </TextLink>
                    </th>
                    <td className={cell}>{number(b.nodes)}</td>
                    <td className={cell}>{number(b.words)}</td>
                    <td className={cell}>{number(b.characters)}</td>
                    <td className={cell}>{number(b.choices)}</td>
                    <td className={cell}>{number(b.conditions)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Page>
  );
}
