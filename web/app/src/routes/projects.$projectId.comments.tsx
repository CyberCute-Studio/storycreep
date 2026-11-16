import type { CommentBody } from "@storycreep/shared/api";
import {
  useInfiniteQuery,
  useQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { type ReactNode, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import {
  type CommentFilter,
  commentAuthorsQuery,
  commentListQuery,
  commentsOn,
  threadsOf,
} from "../comments/queries";
import { ReadFailed, ThreadList } from "../comments/Threads";
import { collator } from "../content/textNames";
import {
  useDocumentState,
  useProjectDocument,
  useTree,
} from "../document/react";
import { loadParts, projectQuery } from "../project/queries";
import { buildTree, itemsInOrder } from "../project/tree";
import { searchId } from "../shell/found";
import { MoreButton } from "../ui/Button";
import { SelectField } from "../ui/Field";
import { Page, Status, textLink } from "../ui/Page";

/**
 * The search of the page, its filters, which a link names, and Back goes
 * back through: the threads' status, open unless it says resolved or all;
 * the author of a comment of the thread; and the board that the threads are
 * on, or on whose nodes, connections, or variables.
 */
interface CommentsSearch {
  status?: "resolved" | "all";
  author?: string;
  board?: string;
}

export const Route = createFileRoute("/projects/$projectId/comments")({
  // An ID that is none of its kind, as of a link cut short, is no filter,
  // rather than a request that the server refuses (searchId). Each filter
  // is said, if only as undefined, as the router lays what this returns
  // over what the address spells, which would else stay as it was spelt.
  validateSearch: (search: Record<string, unknown>): CommentsSearch => ({
    status:
      search.status === "resolved" || search.status === "all"
        ? search.status
        : undefined,
    author: searchId("user", search.author),
    board: searchId("board", search.board),
  }),
  // The tree of the project's boards, which the board filter offers, and
  // its variables, by whose keys a thread on one is named.
  loader: ({ context, params }) =>
    loadParts(context, params.projectId, ["tree", "variables"]),
  gcTime: 0,
  component: CommentsPage,
});

/** The value of a filter's choice of every thread, whatever the filter is of. */
const any = "";

// A project's threaded comments (PRD FR-COMMENT-01, plan item 74): every
// thread on its nodes, connections, boards, and variables, newest first, a
// page at a time, each under a link to what it is on, as the search's
// results are (plan item 65), with its replies, and Reply and Resolve, or
// Reopen, for a role that comments, and the user's own comments' Edit and
// Delete, filtered by their status, open unless the address says otherwise,
// by an author of theirs, and by their board, which the address holds. The
// project's document reads them again as an operation of a comment applies,
// here or in another session, or as anything goes to the trash or comes
// back from it, with whose threads.
function CommentsPage() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const doc = useProjectDocument(projectId);
  const writable =
    commentsOn(project.role) && project.archived_at === undefined;
  const filter: CommentFilter = {
    ...(search.status === "all" ? {} : { status: search.status ?? "open" }),
    ...(search.author === undefined ? {} : { author: search.author }),
    ...(search.board === undefined ? {} : { board: search.board }),
  };
  const list = useInfiniteQuery(commentListQuery(doc, filter));
  const authors = useQuery(commentAuthorsQuery(doc)).data ?? [];
  const tree = useTree(doc);
  const boards = useMemo(
    () =>
      itemsInOrder(buildTree(tree.folders, tree.boards)).map(
        (node) => node.item,
      ),
    [tree],
  );
  const variables = useDocumentState(doc, (state) => state.view.variables);
  const heading = useRef<HTMLHeadingElement>(null);
  const threads = threadsOf(
    list.data?.pages.flatMap((page) => page.items) ?? [],
  );
  const set = (next: Partial<CommentsSearch>) =>
    void navigate({
      search: (prev) => {
        const merged = { ...prev, ...next };
        return Object.fromEntries(
          Object.entries(merged).filter(([, v]) => v !== undefined),
        ) as CommentsSearch;
      },
    });
  // What each thread is on, as a link that opens it where it is edited: a
  // node, and a connection by the node that it leaves, selected on its
  // board's canvas with its inspector at its comments, a board's page at
  // its own, and a variable's panel.
  const targetOf = (root: CommentBody): ReactNode => {
    const untitled = t("boards.untitled_node");
    const node =
      root.node_title === undefined || root.node_title === ""
        ? untitled
        : root.node_title;
    const board = root.board_name ?? "";
    switch (root.target_type) {
      case "node":
      case "connection":
        return root.board_id === undefined || root.node_id === undefined ? (
          node
        ) : (
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: root.board_id }}
            search={{
              node: root.node_id,
              ...(root.target_type === "connection"
                ? { connection: root.target_id }
                : {}),
              comments: true,
            }}
            className={textLink}
          >
            {root.target_type === "node"
              ? t("comments.on_node", { node, board })
              : t("comments.on_connection", { node, board })}
          </Link>
        );
      case "board":
        return root.board_id === undefined ? (
          board
        ) : (
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: root.board_id }}
            search={{ comments: true }}
            className={textLink}
          >
            {t("comments.on_board", { board })}
          </Link>
        );
      case "variable":
        return (
          <Link
            to="/projects/$projectId/variables"
            params={{ projectId }}
            search={{ variable: root.target_id }}
            className={textLink}
          >
            {t("comments.on_variable", {
              key: variables.get(root.target_id)?.key ?? root.target_id,
            })}
          </Link>
        );
    }
  };
  const sortedAuthors = [...authors].sort((a, b) =>
    collator.compare(a.display_name, b.display_name),
  );
  return (
    <Page
      title={t("comments.page.title")}
      documentTitle={t("comments.page.page_title", { project: project.name })}
      width="wide"
    >
      <p className="text-fg-muted">
        {writable ? t("comments.page.intro") : t("comments.page.intro_read")}
      </p>
      <search>
        <div className="flex flex-wrap items-end gap-4">
          <SelectField
            label={t("comments.page.status")}
            value={search.status ?? "open"}
            onChange={(e) => {
              const value = e.currentTarget.value;
              set({
                status:
                  value === "resolved" || value === "all" ? value : undefined,
              });
            }}
          >
            <option value="open">{t("comments.page.status_open")}</option>
            <option value="resolved">
              {t("comments.page.status_resolved")}
            </option>
            <option value="all">{t("comments.page.status_all")}</option>
          </SelectField>
          <SelectField
            label={t("comments.page.author")}
            value={search.author ?? any}
            onChange={(e) => {
              const value = e.currentTarget.value;
              set({ author: value === any ? undefined : value });
            }}
          >
            <option value={any}>{t("comments.page.author_any")}</option>
            {/* An author that the address names whom the list of authors
                lacks, as one whose comments were all deleted since, is
                offered by their ID, so that the filter shows what it is. */}
            {search.author !== undefined &&
              !authors.some((a) => a.id === search.author) && (
                <option value={search.author}>{search.author}</option>
              )}
            {sortedAuthors.map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name}
              </option>
            ))}
          </SelectField>
          <SelectField
            label={t("comments.page.board")}
            value={search.board ?? any}
            onChange={(e) => {
              const value = e.currentTarget.value;
              set({ board: value === any ? undefined : value });
            }}
          >
            <option value={any}>{t("comments.page.board_any")}</option>
            {search.board !== undefined &&
              !boards.some((b) => b.id === search.board) && (
                <option value={search.board}>{search.board}</option>
              )}
            {boards.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </SelectField>
        </div>
      </search>
      <section aria-labelledby="comments-threads" className="space-y-4">
        <h2
          id="comments-threads"
          ref={heading}
          tabIndex={-1}
          className="text-xl font-semibold"
        >
          {t("comments.page.threads")}
        </h2>
        <Status
          message={
            list.isPending
              ? t("comments.reading")
              : list.isError
                ? undefined
                : t("comments.page.shown", {
                    count: threads.length,
                    more: list.hasNextPage ? "yes" : "no",
                  })
          }
        />
        {list.isError && (
          <ReadFailed error={list.error} onRetry={() => void list.refetch()} />
        )}
        {list.data !== undefined && (
          <ThreadList
            doc={doc}
            threads={threads}
            writable={writable}
            readAt={list.dataUpdatedAt}
            reread={() => list.refetch()}
            heading={heading}
            empty={
              search.status === "resolved"
                ? t("comments.page.none_resolved")
                : search.status === "all"
                  ? t("comments.page.none")
                  : t("comments.page.none_open")
            }
            targetOf={targetOf}
          />
        )}
        <MoreButton list={list}>{t("comments.page.more")}</MoreButton>
      </section>
    </Page>
  );
}
