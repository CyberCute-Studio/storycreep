// A project's threaded comments (PRD FR-COMMENT-01, plan item 74), as the
// server keeps them: a thread is a first comment on a node, a connection, a
// board, or a variable, and its replies, one level deep. The page reads them
// as queries of their own, each once the page's batches have been answered,
// which the project's document reads again as an operation of a comment
// applies, and as anything goes to the trash or comes back from it, whose
// threads are in the trash with it (followed): the threads of one target,
// which its inspector or its panel shows, the project's list of them, a page
// at a time, with its filters, the counts of each target's threads, which
// the canvas marks its cards with, and the authors, whom the list's filter
// offers. A comment is no part of the project's document, whose appliers of
// its commands change nothing (document/appliers/other.ts), nor of its
// history: its commands are sent without it (sendComments).

import type {
  CommentAuthor,
  CommentBody,
  CommentCount,
  ProjectBody,
  UndoCommandBody,
} from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { allAtOnce, api, everyPage, pagedQuery } from "../api";
import { sentWait, whenSent } from "../document/sent";
import type { Command, ProjectDocument } from "../document/store";

/** The key of every read of the comments of projectId, which an operation that may change them reads again. */
export function commentsKey(projectId: string) {
  return ["projects", projectId, "comments"] as const;
}

/** What the project's list of threads lists, as its address holds it. */
export interface CommentFilter {
  /** Open threads, resolved ones, or, without it, both. */
  status?: "open" | "resolved";
  /** The ID of a user who wrote a comment of the thread that is not deleted. */
  author?: string;
  /** The ID of a board, of whose threads, its nodes', its connections', and its variables' alone. */
  board?: string;
}

/** How many comments a page of the project's list holds: a thread and its replies are seldom more. */
const listPage = 50;

/**
 * The project's threads that filter lists, newest first, each its first
 * comment and then its replies, oldest first, flat, a page of comments at a
 * time (threadsOf groups them), each page read once the page's batches have
 * been answered, or after sentWait, so that it holds what the page has just
 * posted. The list says why it could not be read (showsRefusals).
 */
export function commentListQuery(doc: ProjectDocument, filter: CommentFilter) {
  return pagedQuery(
    [
      ...commentsKey(doc.projectId),
      "list",
      filter.status ?? null,
      filter.author ?? null,
      filter.board ?? null,
    ],
    async (query, signal) => {
      await whenSent(doc, signal, sentWait);
      return (
        await api.call(
          "listComments",
          {
            path: { project_id: doc.projectId },
            query: { ...query, ...filter },
          },
          { signal },
        )
      ).body;
    },
    { limit: listPage, meta: { showsRefusals: true } },
  );
}

/**
 * Every thread on the target of targetId, a node's, a connection's, a
 * board's own, or a variable's, open and resolved, flat as the list is,
 * read once the page's batches have been answered, or after sentWait: a
 * target has few, which its inspector or panel shows whole, or says why it
 * could not read (showsRefusals).
 */
export function targetCommentsQuery(doc: ProjectDocument, targetId: string) {
  return queryOptions({
    queryKey: [...commentsKey(doc.projectId), "target", targetId],
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<CommentBody[]> => {
      await whenSent(doc, signal, sentWait);
      return everyPage(
        async (cursor) =>
          (
            await api.call(
              "listComments",
              {
                path: { project_id: doc.projectId },
                query: { target: targetId, cursor, limit: allAtOnce },
              },
              { signal },
            )
          ).body,
      );
    },
  });
}

/**
 * How many open and resolved threads each target that has any has: of the
 * board of boardId, and of its nodes, connections, and variables, which its
 * canvas and its page mark, or, without it, of every target of the project,
 * as the variables' list marks its own.
 */
export function commentCountsQuery(doc: ProjectDocument, boardId?: string) {
  return queryOptions({
    queryKey: [...commentsKey(doc.projectId), "counts", boardId ?? null],
    queryFn: async ({ signal }): Promise<CommentCount[]> => {
      await whenSent(doc, signal, sentWait);
      return everyPage(
        async (cursor) =>
          (
            await api.call(
              "countComments",
              {
                path: { project_id: doc.projectId },
                query: {
                  ...(boardId === undefined ? {} : { board: boardId }),
                  cursor,
                  limit: allAtOnce,
                },
              },
              { signal },
            )
          ).body,
      );
    },
  });
}

/**
 * The users who wrote a comment of the project that is not deleted, of a
 * thread that is shown, with their names, which the list's filter of the
 * author offers: a guest cannot list the workspace's members.
 */
export function commentAuthorsQuery(doc: ProjectDocument) {
  return queryOptions({
    queryKey: [...commentsKey(doc.projectId), "authors"],
    queryFn: async ({ signal }): Promise<CommentAuthor[]> => {
      await whenSent(doc, signal, sentWait);
      return everyPage(
        async (cursor) =>
          (
            await api.call(
              "listCommentAuthors",
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

/** A thread: its first comment, which holds whether it is resolved, and its replies, oldest first. */
export interface Thread {
  root: CommentBody;
  replies: CommentBody[];
}

/**
 * Returns the threads of comments, as the server lists them, flat: a
 * thread's first comment, then its replies, threads newest first, which a
 * page of the list may cut between a thread's comments, whose next page
 * goes on with it. A reply whose first comment comes before none of it, as
 * of a list read again between its pages, starts no thread.
 */
export function threadsOf(comments: readonly CommentBody[]): Thread[] {
  const threads: Thread[] = [];
  const byId = new Map<string, Thread>();
  for (const c of comments) {
    if (c.parent_id === undefined) {
      if (!byId.has(c.id)) {
        const thread = { root: c, replies: [] };
        byId.set(c.id, thread);
        threads.push(thread);
      }
      continue;
    }
    const thread = byId.get(c.thread_id);
    if (thread !== undefined && !thread.replies.some((r) => r.id === c.id)) {
      thread.replies.push(c);
    }
  }
  return threads;
}

/** Returns the open threads of the target of id that counts have, 0 for none. */
export function openOf(
  counts: readonly CommentCount[] | undefined,
  id: string,
): number {
  return counts?.find((c) => c.target_id === id)?.open ?? 0;
}

/** Reports whether role starts, answers, and resolves threads: that of "Create/resolve comments" (PRD §5.6). */
export function commentsOn(role: ProjectBody["role"]): boolean {
  return (
    role === "owner" ||
    role === "admin" ||
    role === "editor" ||
    role === "commenter"
  );
}

/**
 * Sends a batch of commands of comments through the project's document of
 * the page, as every batch of the page's, but outside its history, which
 * Ctrl+Z and the history's buttons undo: a comment's action is no step of
 * the page's undo (plan item 74), and a deleted comment offers its own undo
 * in its place, which sends the undo that the server answered its batch
 * with (sendUndo). It resolves with that undo, empty where the batch
 * changed nothing.
 */
export async function sendComments(
  doc: ProjectDocument,
  commands: Command[],
): Promise<UndoCommandBody[]> {
  const answer = await doc.send(commands, { history: false });
  return answer.status === "applied" ? (answer.undo ?? []) : [];
}

/**
 * Sends undo, the undo that the server answered a batch of comments with,
 * as a batch of its own, outside the page's history too: each of its
 * commands expects what the batch left, so that it is refused once the
 * comment has changed since.
 */
export async function sendUndo(
  doc: ProjectDocument,
  undo: readonly UndoCommandBody[],
): Promise<void> {
  // An undo is a command of the generated union by its type, which the
  // server gave it, as the history sends it (ProjectDocument.step).
  await doc.send(undo as unknown as Command[], { history: false });
}
