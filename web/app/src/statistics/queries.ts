// The writing statistics of a project (PRD FR-TEXT-06, plan note N76), as
// the server counts them: the words and the characters of its texts, and
// its nodes, choices, and conditions, of the project and of each of its
// boards, and of a node. The server counts each text as a batch changes it,
// so a read gives what the project holds now, and the project's document
// has its reads read again as each of its operations applies
// (ProjectDocument), so that what a page shows follows an edit, the page's
// own or another session's, within a second (AC1).

import type { NodeStatistics, ProjectStatistics } from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";

/** The key of every read of the statistics of projectId, which each operation of it has read again. */
export function statisticsKey(projectId: string) {
  return ["projects", projectId, "statistics"] as const;
}

/**
 * The writing statistics of the project of doc and of each of its boards,
 * read once the page's batches have been answered, so that they hold what
 * the page shows of them.
 */
export function projectStatisticsQuery(doc: ProjectDocument) {
  return queryOptions({
    queryKey: [...statisticsKey(doc.projectId), "project"] as const,
    queryFn: async ({ signal }): Promise<ProjectStatistics> => {
      await whenSent(doc, signal);
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
 * text and of the connections that leave it, read as the project's are.
 */
export function nodeStatisticsQuery(doc: ProjectDocument, nodeId: string) {
  return queryOptions({
    queryKey: [...statisticsKey(doc.projectId), "nodes", nodeId] as const,
    queryFn: async ({ signal }): Promise<NodeStatistics> => {
      await whenSent(doc, signal);
      return (
        await api.call(
          "getNodeStatistics",
          { path: { project_id: doc.projectId, node_id: nodeId } },
          { signal },
        )
      ).body;
    },
    // The inspector shows a refusal of it.
    meta: { showsRefusals: true },
  });
}
