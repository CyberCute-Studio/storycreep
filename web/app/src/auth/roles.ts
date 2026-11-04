// The names of the roles of a workspace and a project (PRD §5.5), as a page
// shows them.

import type { MembershipBody } from "@storycreep/shared/api";
import type { TFunction } from "i18next";

/** A role of a membership. */
export type Role = MembershipBody["role"];

/** Returns the name of role, in the page's locale. */
export function roleName(t: TFunction, role: Role): string {
  switch (role) {
    case "owner":
      return t("role.owner");
    case "admin":
      return t("role.admin");
    case "editor":
      return t("role.editor");
    case "commenter":
      return t("role.commenter");
    case "viewer":
      return t("role.viewer");
    case "guest":
      return t("role.guest");
  }
}

/** Returns the name of role as a choice of a list shows it, in the page's locale. */
export function roleLabel(t: TFunction, role: Role): string {
  switch (role) {
    case "owner":
      return t("role_label.owner");
    case "admin":
      return t("role_label.admin");
    case "editor":
      return t("role_label.editor");
    case "commenter":
      return t("role_label.commenter");
    case "viewer":
      return t("role_label.viewer");
    case "guest":
      return t("role_label.guest");
  }
}

/**
 * The roles of a workspace that its pages offer, highest first: owner,
 * admin, editor, commenter, and viewer (PRD FR-TEN-03). A guest is a
 * member whom project grants alone let act, whose role a page shows but
 * does not offer.
 */
export const workspaceRoles = [
  "owner",
  "admin",
  "editor",
  "commenter",
  "viewer",
] as const;

/** A role of a project grant, which is never owner (PRD FR-TEN-04 AC5). */
export const grantRoles = ["admin", "editor", "commenter", "viewer"] as const;

/** A role of a project grant. */
export type GrantRole = (typeof grantRoles)[number];

/**
 * Returns the roles that a member whose role is actor's may give, of
 * workspaceRoles: every one for an owner, and all but owner for an admin,
 * since only an owner grants owner (PRD FR-TEN-03 AC4).
 */
export function rolesGivenBy(actor: Role): readonly Role[] {
  return actor === "owner"
    ? workspaceRoles
    : workspaceRoles.filter((r) => r !== "owner");
}
