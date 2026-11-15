// A project's saved play states (PRD FR-PLAY-05, plan note N80), as the
// server keeps them: the list, each without its state, which every member
// who reads the project reads, and a play state with its state, which play
// reads as it loads it. The project's document reads the list again as an
// operation of a play state applies, the page's own or another session's
// (followed), so that the list follows them.

import type { PlayStateBody, PlayStateSummary } from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { allAtOnce, api, everyPage } from "../api";
import { sentWait, whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";

/** The key of the list of the saved play states of projectId, which an operation of one of them has read again. */
export function playStatesKey(projectId: string) {
  return ["projects", projectId, "play-states"] as const;
}

/**
 * Every saved play state of the project of doc that is not in its trash,
 * read once the page's batches have been answered, or after sentWait, so
 * that it holds what the page has just saved.
 */
export function playStatesQuery(doc: ProjectDocument) {
  return queryOptions({
    queryKey: playStatesKey(doc.projectId),
    queryFn: async ({ signal }): Promise<PlayStateSummary[]> => {
      await whenSent(doc, signal, sentWait);
      return everyPage(
        async (cursor) =>
          (
            await api.call(
              "listPlayStates",
              {
                path: { project_id: doc.projectId },
                query: { cursor, limit: allAtOnce },
              },
              { signal },
            )
          ).body,
      );
    },
  });
}

/** Reads the saved play state id of projectId, with its state. */
export async function readPlayState(
  projectId: string,
  id: string,
): Promise<PlayStateBody> {
  return (
    await api.call("getPlayState", {
      path: { project_id: projectId, play_state_id: id },
    })
  ).body;
}
