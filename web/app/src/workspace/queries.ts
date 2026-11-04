// What the pages of a workspace read of the server (PRD FR-TEN-01–04,
// FR-PROJ-01, FR-ENT-02): the user's workspaces, the workspace that a page
// shows, its projects, members, invitations, trash, and usage, and the
// workspace that the device last showed, which the project list opens.

import type {
  MembershipBody,
  PageInviteBody,
  PageMemberBody,
  PageMembershipBody,
  PageProject,
  PageProjectBody,
  UsageBody,
} from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { allAtOnce, api, everyPage, pagedQuery } from "../api";
import type { Role } from "../auth/roles";

/**
 * The workspaces that the user is a member of, a page at a time, each with
 * the role of the user's membership, guest included: a page shows the
 * first, and the rest come as the user asks for them.
 */
export const workspacesQuery = pagedQuery(
  ["workspaces"],
  async (query, signal): Promise<PageMembershipBody> =>
    (await api.call("listWorkspaces", { query }, { signal })).body,
);

/** A workspace as its pages show it, with the role that the user acts with. */
export interface Workspace {
  id: string;
  name: string;
  slug: string;
  default_locale: string;
  role: Role;
  /**
   * Whether the workspace is one of the user's memberships, rather than one
   * that an instance admin acts as the owner of (PRD §5.5), which the
   * switcher does not list, nor the project list opens again.
   */
  membership: boolean;
}

/**
 * The workspace of workspaceId that the user acts in: the one of their
 * memberships, a guest's too, whose role is the membership's, or, for an
 * instance admin who acts as the owner of a workspace that they are not a
 * member of (PRD §5.5), the workspace that the server reads them. The
 * server keeps no current workspace, so a page names it by its ID. It
 * rejects with the server's 404 not_found for a workspace that the user may
 * not see, as one that they have left.
 */
export function workspaceQuery(workspaceId: string) {
  return queryOptions({
    queryKey: ["workspaces", workspaceId, "membership"],
    // A workspace that the user may not see shows the page of no page.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }): Promise<Workspace> => {
      // A user is a member of few workspaces, so the pages are few too.
      const memberships = await everyPage(
        async (cursor) =>
          (
            await api.call(
              "listWorkspaces",
              { query: { cursor, limit: allAtOnce } },
              { signal },
            )
          ).body,
      );
      const membership = memberships.find((m) => m.id === workspaceId);
      if (membership !== undefined) {
        return workspaceOf(membership, true);
      }
      const workspace = (
        await api.call(
          "getWorkspace",
          { path: { workspace_id: workspaceId } },
          { signal },
        )
      ).body;
      return workspaceOf(workspace, false);
    },
  });
}

/** Returns the fields of a membership, or of a workspace that is none of the user's, that its pages show. */
function workspaceOf(
  w: Omit<MembershipBody, "membership_id" | "created_at" | "updated_at">,
  membership: boolean,
): Workspace {
  return {
    id: w.id,
    name: w.name,
    slug: w.slug,
    default_locale: w.default_locale,
    role: w.role,
    membership,
  };
}

/** Reports whether role manages the workspace: its members, projects, and settings (§5.6). */
export function managesWorkspace(role: Role): boolean {
  return role === "owner" || role === "admin";
}

/**
 * The projects of a workspace that the user can see, a page at a time,
 * archived ones included, each with the role that the user acts with on it:
 * a guest's, those granted to them alone.
 */
export function projectsQuery(workspaceId: string) {
  return pagedQuery(
    ["workspaces", workspaceId, "projects"],
    async (query, signal): Promise<PageProjectBody> =>
      (
        await api.call(
          "listProjects",
          { path: { workspace_id: workspaceId }, query },
          { signal },
        )
      ).body,
  );
}

/** The projects of a workspace's trash, a page at a time, which its admins see. */
export function trashedProjectsQuery(workspaceId: string) {
  return pagedQuery(
    ["workspaces", workspaceId, "trash"],
    async (query, signal): Promise<PageProject> =>
      (
        await api.call(
          "listTrashedProjects",
          { path: { workspace_id: workspaceId }, query },
          { signal },
        )
      ).body,
  );
}

/**
 * The members of a workspace, a page at a time, guests included, with their
 * roles, and, to its owners and admins alone, their email addresses.
 */
export function membersQuery(workspaceId: string) {
  return pagedQuery(
    ["workspaces", workspaceId, "members"],
    async (query, signal): Promise<PageMemberBody> =>
      (
        await api.call(
          "listMembers",
          { path: { workspace_id: workspaceId }, query },
          { signal },
        )
      ).body,
  );
}

/** The open invitations of a workspace, a page at a time, which its admins see. */
export function invitesQuery(workspaceId: string) {
  return pagedQuery(
    ["workspaces", workspaceId, "invites"],
    async (query, signal): Promise<PageInviteBody> =>
      (
        await api.call(
          "listInvites",
          { path: { workspace_id: workspaceId }, query },
          { signal },
        )
      ).body,
  );
}

/**
 * How much a workspace uses of what the plans meter, and, of what its plan
 * limits, the limit and whether it uses 80% of it or more, which its owners
 * and admins alone may read (PRD FR-ENT-02).
 */
export function usageQuery(workspaceId: string) {
  return queryOptions({
    queryKey: ["workspaces", workspaceId, "usage"],
    queryFn: async ({ signal }): Promise<UsageBody> =>
      (
        await api.call(
          "getWorkspaceUsage",
          { path: { workspace_id: workspaceId } },
          { signal },
        )
      ).body,
  });
}

/** Where the device keeps the workspace that an account last showed on it. */
function lastWorkspaceKey(userId: string): string {
  return `storycreep.workspace.${userId}`;
}

/**
 * Records that the device showed the account userId the workspace
 * workspaceId, which the project list opens next (lastWorkspace). A browser
 * that keeps nothing, as in a private window whose storage throws, records
 * nothing.
 */
export function rememberWorkspace(userId: string, workspaceId: string): void {
  try {
    localStorage.setItem(lastWorkspaceKey(userId), workspaceId);
  } catch {
    // The project list opens the first workspace instead.
  }
}

/**
 * Returns the workspace that the device last showed the account userId, if
 * it kept it: a hint, never more, since the user may have left it since,
 * which the project list checks against the user's memberships.
 */
export function lastWorkspace(userId: string): string | undefined {
  try {
    return localStorage.getItem(lastWorkspaceKey(userId)) ?? undefined;
  } catch {
    return undefined;
  }
}
