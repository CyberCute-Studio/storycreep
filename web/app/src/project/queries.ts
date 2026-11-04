// What the pages of a project read of the server (PRD FR-PROJ-01, FR-PROJ-02,
// FR-PROJ-06, FR-TEN-04): the project, the tree of its boards, a board and
// the jumpers that target it, its trash, and the members' grants on it; and
// how a page changes the project's content, with batches of commands
// (useCommands), each of which applies whole or not at all.

import {
  ApiError,
  type Board,
  type CommandBody,
  type CommandResultBody,
  type Folder,
  type GrantBody,
  type Node,
  type PageTrashItemBody,
  type ProjectBody,
} from "@storycreep/shared/api";
import { formatUuid, newUuid } from "@storycreep/shared/ids";
import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { useRef } from "react";
import { api, pagedQuery, sessionQuery } from "../api";
import { csrfHeaders } from "../auth/session";

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

/** Reports whether role changes a project's settings: renames, archives, and describes it (§5.6). */
export function managesProject(role: ProjectBody["role"]): boolean {
  return role === "owner" || role === "admin";
}

/** Reports whether role creates, renames, moves, and deletes a project's boards and folders (§5.6). */
export function editsProject(role: ProjectBody["role"]): boolean {
  return role === "owner" || role === "admin" || role === "editor";
}

/** The most items that a page of a list holds, which the server allows. */
const allAtOnce = 200;

/**
 * Reads every page of a list, from the first, with fetchPage, and returns
 * their items: a list that a page shows whole, as a tree, whose items are
 * few enough, as a project's boards are hundreds at most (PRD §9.1).
 */
async function everyPage<T>(
  fetchPage: (
    cursor: string | undefined,
  ) => Promise<{ items: T[]; next_cursor: string | null }>,
): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  do {
    const page = await fetchPage(cursor);
    items.push(...page.items);
    cursor = page.next_cursor ?? undefined;
  } while (cursor !== undefined);
  return items;
}

/** What the tree of a project's boards is built from. */
export interface TreeData {
  folders: Folder[];
  boards: Board[];
}

/**
 * The folders of boards and the boards of a project that are not in its
 * trash, which its tree shows (buildTree).
 */
export function treeQuery(projectId: string) {
  return queryOptions({
    queryKey: ["projects", projectId, "tree"],
    queryFn: async ({ signal }): Promise<TreeData> => {
      const path = { project_id: projectId };
      const [folders, boards] = await Promise.all([
        everyPage(
          async (cursor) =>
            (
              await api.call(
                "listFolders",
                { path, query: { kind: "board", cursor, limit: allAtOnce } },
                { signal },
              )
            ).body,
        ),
        everyPage(
          async (cursor) =>
            (
              await api.call(
                "listBoards",
                { path, query: { cursor, limit: allAtOnce } },
                { signal },
              )
            ).body,
        ),
      ]);
      return { folders, boards };
    },
  });
}

/** A board of a project. */
export function boardQuery(projectId: string, boardId: string) {
  return queryOptions({
    queryKey: ["projects", projectId, "boards", boardId],
    // A board that is not there, as one in the trash, shows the page of no page.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<Board> =>
      (
        await api.call(
          "getBoard",
          { path: { project_id: projectId, board_id: boardId } },
          { signal },
        )
      ).body,
  });
}

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

/** A command of a batch, without the ID that useCommands gives it. */
export type Command = DistributiveOmit<CommandBody, "command_id">;

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown
  ? Omit<T, K>
  : never;

/** Returns the ID of a new command, a UUID, which a page that is not served over HTTPS makes too. */
function newCommandId(): string {
  return formatUuid(newUuid());
}

/**
 * Reads again what a change of projectId's content may have changed: the
 * project, its tree, its boards, and its trash.
 */
export function readProjectAgain(
  client: QueryClient,
  projectId: string,
): Promise<void> {
  return client.invalidateQueries({ queryKey: ["projects", projectId] });
}

/**
 * The mutation that applies a batch of commands to projectId, which it reads
 * again once the batch has applied (readProjectAgain), and once it was
 * refused for a revision that it expected, as of a board that changed
 * elsewhere since the page read it. A batch sent again as it was, as once
 * no answer came to it, has the IDs that its commands had, so that the
 * server answers it as the batch that it applied already, if it did, rather
 * than apply it again, until the server answers it; a batch of other
 * commands has IDs of its own.
 */
export function useCommands(projectId: string) {
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const last = useRef<{ sent: string; ids: string[] }>(null);
  return useMutation({
    mutationFn: async (commands: Command[]): Promise<CommandResultBody> => {
      const sent = JSON.stringify(commands);
      if (last.current?.sent !== sent) {
        last.current = { sent, ids: commands.map(newCommandId) };
      }
      const { ids } = last.current;
      const body = {
        commands: commands.map(
          (command, i) => ({ ...command, command_id: ids[i] }) as CommandBody,
        ),
      };
      const answer = (
        await api.call("applyCommands", {
          path: { project_id: projectId },
          headers: csrfHeaders(session),
          body,
        })
      ).body;
      // A single batch is answered with its result, never with a list.
      if ("results" in answer) {
        throw new Error("The server answered a batch with a list of results.");
      }
      return answer;
    },
    onSuccess: () => {
      last.current = null;
      return readProjectAgain(client, projectId);
    },
    onError: (error) => {
      // A batch that the server answered, applied or not, is one that it
      // will answer alike if it is sent again with these IDs, as with a
      // refusal of the project's archiving that has ended since (N36), so
      // the next batch has IDs of its own.
      if (answered(error)) {
        last.current = null;
      }
      if (conflictOf(error)) {
        return readProjectAgain(client, projectId);
      }
    },
  });
}

/**
 * Reports whether error is the server's own answer, with the error envelope,
 * rather than none, as a request that no answer came to (NetworkError), or a
 * proxy's page of its own, of which the page cannot tell whether the batch
 * applied.
 */
function answered(error: unknown): boolean {
  return error instanceof ApiError && error.error !== undefined;
}

/** Reports whether error is a refusal of a revision that a batch expected (revision_conflict). */
function conflictOf(error: unknown): boolean {
  return error instanceof ApiError && error.error?.code === "revision_conflict";
}
