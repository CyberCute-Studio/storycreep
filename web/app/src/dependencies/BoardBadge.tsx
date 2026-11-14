// A board's badge of its dependencies (PRD FR-DEP-02, plan note N77), in the
// header of its page: how many other boards it depends on, and how many
// depend on it, each once, whatever the kinds of the edges, as the server
// counts them, linked to the dependency view of its edges alone. It reads
// the graph of the board's edges alone, as that view does, which counts the
// board as the whole graph does, rather than the whole graph, which a large
// project's edges make megabytes of. It follows the project's document, as
// the view does, so that it tells of an edit, here or in another session,
// that makes a dependency or takes one away.

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useProjectDocument } from "../document/react";
import { textLink } from "../ui/Page";
import { graphQuery } from "./queries";

/** The badge of the board boardId of the project of projectId, once the graph has been read, and nothing until then, nor if the read fails. */
export function BoardBadge({
  projectId,
  boardId,
}: {
  projectId: string;
  boardId: string;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const board = useQuery({
    ...graphQuery(doc, { board: boardId }),
    select: (graph) => graph.boards.find((b) => b.id === boardId),
  }).data;
  if (board === undefined) {
    return null;
  }
  return (
    <p className="min-w-0 flex-1 truncate text-fg-muted">
      <Link
        to="/projects/$projectId/dependencies"
        params={{ projectId }}
        search={{ board: boardId }}
        className={textLink}
      >
        {t("dependencies.badge", {
          inbound: board.inbound,
          outbound: board.outbound,
        })}
      </Link>
    </p>
  );
}
