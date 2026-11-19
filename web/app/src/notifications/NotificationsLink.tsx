// The header's link to the account's notifications (PRD FR-COMMENT-02, plan
// item 75), on every page of a signed-in account, with how many of them are
// unread, a number on a badge that assistive technology is told in words,
// so that neither the badge's colour nor its number alone says it.

import { formatNumber } from "@storycreep/shared/i18n";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unreadQuery } from "./queries";

/** The link to the page of the account's notifications, with the count of the unread ones, once it is read, and while it is any. */
export function NotificationsLink() {
  const { t, i18n } = useTranslation();
  const unread = useQuery(unreadQuery).data ?? 0;
  return (
    <Link
      to="/notifications"
      className="flex min-h-11 items-center gap-2 rounded-md px-2 text-fg underline-offset-2 hover:underline data-[status=active]:font-semibold"
    >
      <span>{t("shell.notifications")}</span>
      {unread > 0 && (
        <>
          {/* The space between the parts of the link's name, which the
          flex box's gap shows. */}{" "}
          <span className="rounded-full bg-accent px-2 text-sm font-semibold text-accent-fg">
            <span aria-hidden="true">
              {formatNumber(i18n.language, unread)}
            </span>
            <span className="sr-only">
              {t("shell.notifications_unread", { count: unread })}
            </span>
          </span>
        </>
      )}
    </Link>
  );
}
