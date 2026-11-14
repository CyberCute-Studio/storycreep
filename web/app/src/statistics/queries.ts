// The writing statistics of a project (PRD FR-TEXT-06, plan note N76), as
// the server counts them: the words and the characters of its texts, and
// its nodes, choices, and conditions, of the project and of each of its
// boards, and of a node. The server counts each text as a batch changes it,
// so a read gives what the project holds now, and the project's document
// has its reads read again as its operations apply (ProjectDocument), so
// that what a page shows follows an edit, the page's own or another
// session's, within a second (AC1).

import type { NodeStatistics, ProjectStatistics } from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { api, notFoundBy } from "../api";
import { whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";

/** The key of every read of the statistics of projectId, which each operation of it has read again. */
export function statisticsKey(projectId: string) {
  return ["projects", projectId, "statistics"] as const;
}

/**
 * How long, in milliseconds, a read of the statistics waits for the page's
 * batches at most, which the server answers in far less, so that one that
 * waits for long, as one queued while the page is offline, keeps neither
 * the page from opening nor the inspector's counts loading: the read goes
 * on without it, and the document has it read again once it applies. A
 * node's that the server answers with not_found while the page's batches
 * are unanswered waits for them for as long as they take, and reads again
 * (nodeStatisticsQuery).
 */
const sentWait = 3_000;

/**
 * The writing statistics of the project of doc and of each of its boards,
 * read once the page's batches have been answered, or after sentWait, so
 * that they hold what the page shows of them.
 */
export function projectStatisticsQuery(doc: ProjectDocument) {
  return queryOptions({
    queryKey: [...statisticsKey(doc.projectId), "project"] as const,
    queryFn: async ({ signal }): Promise<ProjectStatistics> => {
      await whenSent(doc, signal, sentWait);
      return (
        await api.call(
          "getProjectStatistics",
          { path: { project_id: doc.projectId } },
          { signal },
        )
      ).body;
    },
    meta: { showsNotFound: true },
  });
}

/**
 * The writing statistics of the node nodeId of the project of doc, of its
 * text and of the connections that leave it, read as the project's are, but
 * that a read that the server answers with not_found while the page's
 * batches are unanswered, as of a node that one of them creates, which the
 * server does not have until it has applied it, as on a connection slower
 * than sentWait, waits for them for as long as they take and reads again,
 * so that the inspector says that the counts load, rather than that there
 * is no such node, of the node that the page has just made.
 */
export function nodeStatisticsQuery(doc: ProjectDocument, nodeId: string) {
  return queryOptions({
    queryKey: [...statisticsKey(doc.projectId), "nodes", nodeId] as const,
    queryFn: async ({ signal }): Promise<NodeStatistics> => {
      const read = async () =>
        (
          await api.call(
            "getNodeStatistics",
            { path: { project_id: doc.projectId, node_id: nodeId } },
            { signal },
          )
        ).body;
      await whenSent(doc, signal, sentWait);
      try {
        return await read();
      } catch (error) {
        if (!notFoundBy(error) || doc.store.getState().sending === 0) {
          throw error;
        }
        await whenSent(doc, signal);
        return await read();
      }
    },
    // The inspector shows a refusal of it.
    meta: { showsRefusals: true },
  });
}
