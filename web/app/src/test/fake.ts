// What the app's tests of pages answer the API's client with: a fake of its
// calls, the server's refusals, and the workspace and the project that a
// signed-in test account starts from, which the start page goes to.

import {
  ApiError,
  type ErrorBody,
  type MembershipBody,
  type ProjectBody,
} from "@storycreep/shared/api";
import { vi } from "vitest";
import { api } from "../api";

type Call = Parameters<typeof api.call>;

/** Answers the client's calls with answer, which returns an operation's body. */
export function fakeApi(
  answer: (operation: Call[0], input: unknown) => unknown,
) {
  return vi.spyOn(api, "call").mockImplementation((async (
    operation: Call[0],
    input: unknown,
  ) => ({
    status: 200,
    body: await answer(operation, input),
    response: new Response(),
  })) as typeof api.call);
}

/** Returns the ApiError of an answer of status with the error envelope of error. */
export function refused(
  status: number,
  error: Partial<ErrorBody> & { code: string },
  headers: Record<string, string> = {},
): ApiError {
  const body = { message: error.code, request_id: "req", ...error };
  return new ApiError(
    new Response(JSON.stringify({ error: body }), {
      status,
      headers: { "Content-Type": "application/json", ...headers },
    }),
    body,
  );
}

/** The test account's workspace, which it owns. */
export const testWorkspace: MembershipBody = {
  membership_id: "mem_test",
  role: "owner",
  id: "ws_studio",
  name: "Studio",
  slug: "studio",
  default_locale: "en",
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};

/** A project of testWorkspace. */
export const testProject: ProjectBody = {
  role: "owner",
  id: "prj_harbour",
  workspace_id: testWorkspace.id,
  name: "Harbour",
  slug: "harbour",
  description: "",
  default_locale: "en",
  publish_min_role: "editor",
  entry: {},
  rev: 1,
  seq: 1,
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};

/**
 * Returns the answer of what the start page reads, the project list of the
 * account's workspaces, whose first is workspaces' first, or undefined for
 * another operation: the account's workspaces, and each one's projects; and
 * of what a board's page reads besides its board, the graph of its badge of
 * dependencies (plan item 70), of no board, so that a test of a board's page
 * that does not answer it shows no badge, rather than a read that fails and
 * is tried again in the test's background.
 */
export function startPage(
  operation: Call[0],
  workspaces: MembershipBody[] = [testWorkspace],
  projects: ProjectBody[] = [],
): unknown {
  switch (operation) {
    case "listWorkspaces":
      return { items: workspaces, next_cursor: null };
    case "listProjects":
      return { items: projects, next_cursor: null };
    case "getDependencyGraph":
      return { seq: 0, boards: [], edges: [] };
  }
  return undefined;
}
