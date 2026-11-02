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
