// The link to where the threads of a target of comments show (PRD
// FR-COMMENT-01, plan item 74), which the project's list of comments names
// each thread by, and the search each comment that it finds.

import type { CommentBody } from "@storycreep/shared/api";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { textLink } from "../ui/Page";

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
 * children alone.
 */
export function ThreadTargetLink({
  projectId,
  place,
  children,
}: {
  projectId: string;
  place: ThreadPlace;
  children: ReactNode;
}) {
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
        >
          {children}
        </Link>
      );
  }
}
