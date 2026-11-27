// What the pages show while the browser has no connection (PRD FR-OFF-02,
// plan note N97): of the app's, the start page, which opens the board that
// the device showed last, a project's pages, and the storage panel, every
// other saying that it needs a connection (offlineRoutes); and, of a
// project's, a notice that the page is offline, and of the copy of the
// project that the device keeps, as of when, and, in place of a page that
// reads the server alone, as the search, the statistics, the releases, the
// comments, the trash, the access, and the settings, that it needs a
// connection. The boards, the variables, the components, and their types
// read the project's document, which the device's copy holds; the
// validation and the dependency view go offline with plan item 93. Each
// leads to the storage panel, where the device keeps projects for the
// account, which leads to each, as nothing else that a page offline shows
// does, as the project list, which needs a connection.

import { formatDateTime } from "@storycreep/shared/i18n";
import { useTranslation } from "react-i18next";
import { TextLink } from "../ui/Page";
import { useOffline } from "./react";

/**
 * The routes of the app that a page shows while the browser has no
 * connection, as the root route reads them before any page's loader: the
 * start page, which opens the board that the device showed last, or else
 * goes to the project list; a project's pages, which say themselves which
 * need a connection (offlinePages); and the storage panel. Every other, as
 * the project list, a workspace's pages, the account's other screens, and
 * the notifications, which read the server alone, says that it needs one.
 */
export const offlineRoutes: ReadonlySet<string> = new Set([
  "/",
  "/projects/$projectId",
  "/account/offline",
]);

/** The routes of a project's pages that read its document alone, which the device's copy holds. */
export const offlinePages: ReadonlySet<string> = new Set([
  "/projects/$projectId/",
  "/projects/$projectId/boards/$boardId",
  "/projects/$projectId/variables",
  "/projects/$projectId/components",
  "/projects/$projectId/component-types",
]);

/** The link to the storage panel, which lists the projects that the device keeps for the account, each a link to it, or nothing where it keeps none. */
function KeptLink() {
  const { t } = useTranslation();
  const kept = useOffline((s) => s.pins.size > 0);
  return kept ? (
    <TextLink to="/account/offline">{t("offline.kept_link")}</TextLink>
  ) : null;
}

/** The notice of a project's pages while the browser has no connection, of the copy of projectId that the device keeps, if it keeps one. */
export function OfflineNotice({ projectId }: { projectId: string }) {
  const { t, i18n } = useTranslation();
  const copy = useOffline((s) => s.pins.get(projectId)?.copy);
  return (
    <div role="status">
      <p className="rounded-md border border-border bg-surface px-4 py-3">
        {copy === undefined
          ? t("offline.notice.no_copy")
          : t("offline.notice.copy", {
              time: formatDateTime(i18n.language, new Date(copy.savedAt)),
            })}{" "}
        <KeptLink />
      </p>
    </div>
  );
}

/** What a page that reads the server alone shows in its place while the browser has no connection. */
export function NeedsConnection() {
  const { t } = useTranslation();
  return (
    <div className="space-y-2">
      <h1 className="text-2xl font-semibold">
        {t("offline.needs_connection.title")}
      </h1>
      <p>{t("offline.needs_connection.body")}</p>
      <p>
        <KeptLink />
      </p>
    </div>
  );
}
