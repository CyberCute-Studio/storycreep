// The link to where the threads of a target of comments show (PRD
// FR-COMMENT-01, plan item 74), which the project's list of comments names
// each thread by, the search each comment that it finds, and the account's
// notifications each comment that mentions it (FR-COMMENT-02, plan item
// 75), and what names the node of a thread's target in the first two.

import type { CommentBody } from "@storycreep/shared/api";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import type { ReactNode } from "react";
import { textLink } from "../ui/Page";

/**
 * Returns what names the node of a thread's target, as the project's list
 * of comments and the search name it, title, the node's title, as a comment
 * and a result of the search give it, or, where it is empty, what the
 * canvas calls a node without a title.
 */
export function placeNodeTitle(t: TFunction, title: string | undefined) {
  return title === undefined || title === ""
    ? t("boards.untitled_node")
    : title;
}

/** What a thread is on, as a comment and a result of the search name it: the target's kind and ID, and its board and node, by which a link opens it. */
export interface ThreadPlace {
  target_type: CommentBody["target_type"];
  target_id: string;
  board_id?: string;
  node_id?: string;
}

/**
 * A link of children to where the threads on place show, where they are
 * answered and resolved too: a node's, and a connection's by the node that
 * it leaves, which the canvas selects, on its board's canvas, with its
 * inspector at its comments; a board's own on its page, at them; and a
 * variable's in its panel. Of one whose board or node is not known, it is
 * children alone. onFollow is called as the link is followed, as a
 * notification's marks it read, and describedBy names what describes it.
 */
export function ThreadTargetLink({
  projectId,
  place,
  onFollow,
  describedBy,
  children,
}: {
  projectId: string;
  place: ThreadPlace;
  onFollow?: () => void;
  describedBy?: string;
  children: ReactNode;
}) {
  const followed = { onClick: onFollow, "aria-describedby": describedBy };
  switch (place.target_type) {
    case "node":
    case "connection":
      return place.board_id === undefined || place.node_id === undefined ? (
        children
      ) : (
        <Link
          to="/projects/$projectId/boards/$boardId"
          params={{ projectId, boardId: place.board_id }}
          search={{
            node: place.node_id,
            ...(place.target_type === "connection"
              ? { connection: place.target_id }
              : {}),
            comments: true,
          }}
          className={textLink}
          {...followed}
        >
          {children}
        </Link>
      );
    case "board":
      return place.board_id === undefined ? (
        children
      ) : (
        <Link
          to="/projects/$projectId/boards/$boardId"
          params={{ projectId, boardId: place.board_id }}
          search={{ comments: true }}
          className={textLink}
          {...followed}
        >
          {children}
        </Link>
      );
    case "variable":
      return (
        <Link
          to="/projects/$projectId/variables"
          params={{ projectId }}
          search={{ variable: place.target_id }}
          className={textLink}
          {...followed}
        >
          {children}
        </Link>
      );
  }
}
