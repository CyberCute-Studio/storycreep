import { createFileRoute, redirect } from "@tanstack/react-router";
import { requireSession } from "../auth/session";
import { lastBoard } from "../project/lastOpened";

// The start page of a signed-in user (PRD FR-PROJ-04, FR-PUB-05): the board
// that the device last showed them, while they may still read it, as the
// server sends / and /login to, or else the project list, of the workspace
// that the device last showed them.
export const Route = createFileRoute("/")({
  beforeLoad: async (args) => {
    const { session } = await requireSession(args);
    const last = await lastBoard(args.context, session.user_id);
    if (last !== undefined) {
      throw redirect({
        to: "/projects/$projectId/boards/$boardId",
        params: last,
        replace: true,
      });
    }
    throw redirect({ to: "/projects", replace: true });
  },
});
