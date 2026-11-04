import { createFileRoute, redirect } from "@tanstack/react-router";
import { requireSession } from "../auth/session";

// The start page of a signed-in user is the project list (PRD FR-PUB-05),
// of the workspace that the device last showed them.
export const Route = createFileRoute("/")({
  beforeLoad: async (args) => {
    await requireSession(args);
    throw redirect({ to: "/projects", replace: true });
  },
});
