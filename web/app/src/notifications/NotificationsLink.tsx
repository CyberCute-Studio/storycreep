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
      className="flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-md px-2 text-fg underline-offset-2 hover:underline data-[status=active]:font-semibold"
    >
      {/* Below the md breakpoint, as on the reference tablet in portrait
          (640 px), the link shows its bell alone, and the error console's
          button its sign alone, so that the header holds one line there, as
          it did before this link joined it, and leaves the canvas its
          height; the label stays the start of the link's name all the
          same. */}
      <svg
        aria-hidden="true"
        viewBox="0 0 24 24"
        className="size-5 shrink-0"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M6 16V11a6 6 0 0 1 12 0v5l2 2H4l2-2z" />
        <path d="M10 21h4" />
      </svg>
      <span className="sr-only md:not-sr-only">{t("shell.notifications")}</span>
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
