import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { requireSession } from "../auth/session";

export const Route = createFileRoute("/account")({
  beforeLoad: requireSession,
  component: AccountLayout,
});

/** The classes of a link of the account's navigation, the current one marked. */
const navLink =
  "flex min-h-11 items-center rounded-md px-3 text-fg underline-offset-2 hover:underline data-[status=active]:bg-surface data-[status=active]:font-semibold";

// The account's own screens (PRD FR-AUTH-02, FR-AUTH-04, FR-I18N-02,
// FR-GDPR-01, FR-OFF-02): its profile, language, and password; its
// sessions; its API tokens; the export of its data; and the projects that
// the device keeps offline for it.
function AccountLayout() {
  const { t } = useTranslation();
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <nav aria-label={t("account.nav.label")}>
        <ul className="flex flex-wrap gap-2 border-b border-border pb-2">
          <li>
            <Link
              to="/account"
              activeOptions={{ exact: true }}
              className={navLink}
            >
              {t("account.nav.profile")}
            </Link>
          </li>
          <li>
            <Link to="/account/sessions" className={navLink}>
              {t("account.nav.sessions")}
            </Link>
          </li>
          <li>
            <Link to="/account/tokens" className={navLink}>
              {t("account.nav.tokens")}
            </Link>
          </li>
          <li>
            <Link to="/account/data" className={navLink}>
              {t("account.nav.data")}
            </Link>
          </li>
          <li>
            <Link to="/account/offline" className={navLink}>
              {t("account.nav.offline")}
            </Link>
          </li>
        </ul>
      </nav>
      <Outlet />
    </div>
  );
}
