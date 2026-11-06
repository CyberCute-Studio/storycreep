// What the pages of a project read of the server (PRD FR-PROJ-01, FR-PROJ-02,
// FR-PROJ-06, FR-TEN-04): the project, the jumpers that target a board, its
// trash, and the members' grants on it; and how a page changes the
// project's content, with batches of commands (useCommands), each of which
// applies whole or not at all, through the project's document
// (document/store.ts), which holds the content that the pages show, as the
// tree of its boards.

import {
  ApiError,
  type GrantBody,
  type Node,
  type PageTrashItemBody,
  type ProjectBody,
} from "@storycreep/shared/api";
import {
  type QueryClient,
  queryOptions,
  useMutation,
} from "@tanstack/react-query";
import { allAtOnce, api, everyPage, pagedQuery } from "../api";
import type { Part } from "../document/doc";
import type { Documents } from "../document/documents";
import { useProjectDocument } from "../document/react";
import type { Command } from "../document/store";
import { found } from "../shell/found";

export type { Command } from "../document/store";

/** The project of projectId, with the role that the user acts with on it. */
export function projectQuery(projectId: string) {
  return queryOptions({
    queryKey: ["projects", projectId],
    // A project that the user may not see shows the page of no page.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<ProjectBody> =>
      (
        await api.call(
          "getProject",
          { path: { project_id: projectId } },
          { signal },
        )
      ).body,
  });
}

/**
 * Reads what a page of the project of projectId shows before it shows, as
 * its loader: the project first, as for the tree of its boards
 * (projects.$projectId.index.tsx), so that a project that the account may
 * not see shows the page of no page before its document reads anything,
 * and then the parts of its document that the page shows, at once. A part
 * that the document held already, which the real-time channel does not
 * keep current while it is not live, is shown as it is, and read anew; a
 * read anew that fails is recorded, and tried again later.
 */
export async function loadParts(
  context: { queryClient: QueryClient; documents: Documents },
  projectId: string,
  parts: readonly Part[],
): Promise<void> {
  const project = await found(
    context.queryClient.ensureQueryData(projectQuery(projectId)),
  );
  const doc = context.documents.get(project.id);
  const held = parts.filter((part) => doc.holds(part));
  await Promise.all(parts.map((part) => found(doc.load(part))));
  for (const part of held) {
    void doc.refresh(part).catch(() => undefined);
  }
}

/** Reports whether role changes a project's settings: renames, archives, and describes it (§5.6). */
export function managesProject(role: ProjectBody["role"]): boolean {
  return role === "owner" || role === "admin";
}

/** Reports whether role creates, renames, moves, and deletes a project's boards and folders (§5.6). */
export function editsProject(role: ProjectBody["role"]): boolean {
  return role === "owner" || role === "admin" || role === "editor";
}

/**
 * What a list of a project tells the query client, which a page's loader
 * reads with found(), so that the page of no page shows once the user may
 * no longer see the project, as once it is in the trash.
 */
const shownAsNotFound = { showsNotFound: true };

/** How many of the jumpers that target a board its deletion lists at most. */
export const referrersShown = 100;

/**
 * The first of the jumpers on a project's other boards that target the nodes
 * of a board, which deleting the board would leave broken (PRD FR-PROJ-02
 * AC2), which the deletion's confirmation lists.
 */
export function referrersQuery(projectId: string, boardId: string) {
  return queryOptions({
    queryKey: ["projects", projectId, "boards", boardId, "referrers"],
    // The confirmation shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<{ items: Node[]; more: boolean }> => {
      const page = (
        await api.call(
          "listBoardReferrers",
          {
            path: { project_id: projectId, board_id: boardId },
            query: { limit: referrersShown },
          },
          { signal },
        )
      ).body;
      return { items: page.items, more: page.next_cursor !== null };
    },
    // What the confirmation lists is what the board has when it opens.
    staleTime: 0,
    gcTime: 0,
  });
}

/** What is in a project's trash, the most recently deleted first, a page at a time. */
export function trashQuery(projectId: string) {
  return pagedQuery(
    ["projects", projectId, "trash"],
    async (query, signal): Promise<PageTrashItemBody> =>
      (
        await api.call(
          "listTrash",
          { path: { project_id: projectId }, query },
          { signal },
        )
      ).body,
    { meta: shownAsNotFound },
  );
}

/**
 * The members' grants on a project, each of which replaces a member's role
 * in the workspace on the project, which its workspace's owners and admins
 * alone may list (PRD FR-TEN-04).
 */
export function grantsQuery(projectId: string) {
  return queryOptions({
    queryKey: ["projects", projectId, "grants"],
    meta: shownAsNotFound,
    queryFn: async ({ signal }): Promise<GrantBody[]> =>
      everyPage(
        async (cursor) =>
          (
            await api.call(
              "listProjectGrants",
              {
                path: { project_id: projectId },
                query: { cursor, limit: allAtOnce },
              },
              { signal },
            )
          ).body,
      ),
  });
}

/**
 * Reads again what the pages of projectId read besides its document, as
 * once a change of its settings has applied: the project, its trash, and
 * the members' grants, but not the jumpers that a board's deletion lists
 * (referrersQuery), which its confirmation reads as it opens.
 */
export function readProjectAgain(
  client: QueryClient,
  projectId: string,
): Promise<void> {
  return client.invalidateQueries({
    queryKey: ["projects", projectId],
    predicate: (query) => query.queryKey.at(-1) !== "referrers",
  });
}

/**
 * The mutation that applies a batch of commands to projectId, through its
 * document, which shows the batch at once, sends it after the page's
 * batches before it, and shows the project without it again once it is
 * refused, or no answer came to it (ProjectDocument.send). A batch sent
 * again as it was, as once no answer came to it, has the IDs that its
 * commands had, so that the server answers it as the batch that it applied
 * already, if it did, rather than apply it again. The document tells what
 * the pages read of the project besides it what the batch changed of that,
 * the project and its trash, as it does of another session's.
 */
export function useCommands(projectId: string) {
  const doc = useProjectDocument(projectId);
  return useMutation({
    mutationFn: (commands: Command[]) => doc.send(commands),
  });
}

/** Reports whether error is a refusal of a revision that a batch expected (revision_conflict). */
export function conflictOf(error: unknown): error is ApiError {
  return error instanceof ApiError && error.error?.code === "revision_conflict";
}
