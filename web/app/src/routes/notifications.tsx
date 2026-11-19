import type { NotificationBody } from "@storycreep/shared/api";
import { formatDateTime, formatRelativeTime } from "@storycreep/shared/i18n";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { requireSession } from "../auth/session";
import { ThreadTargetLink } from "../comments/TargetLink";
import {
  markAllRead,
  markedRead,
  markRead,
  notificationListQuery,
  readNotificationsAgain,
  unreadQuery,
} from "../notifications/queries";
import { Button, MoreButton } from "../ui/Button";
import { Alert, Page, RefusalAlert, Status } from "../ui/Page";

export const Route = createFileRoute("/notifications")({
  beforeLoad: requireSession,
  component: Notifications,
});

/** The words of the link to where the thread of a notification's comment shows, by what the thread is on. */
function openLabel(t: TFunction, type: NotificationBody["target_type"]) {
  switch (type) {
    case "node":
      return t("notifications.open_node");
    case "connection":
      return t("notifications.open_connection");
    case "board":
      return t("notifications.open_board");
    case "variable":
      return t("notifications.open_variable");
  }
}

// The account's notification centre (PRD FR-COMMENT-02, plan item 75): the
// comments that mention the account, in every workspace that it is a
// member of, newest first, a page at a time, each saying who mentioned it,
// in which project, and when, with the comment's preview and a link to
// where its thread shows, which marks it read as it is followed; an unread
// one says so in words, and is marked read by its own button, and every one
// up to the newest that the page lists by Mark all as read, so that one that
// came after the page listed it stays unread. The page reads them again as
// the real-time channel tells that they changed (useNotificationListener).
function Notifications() {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const list = useInfiniteQuery(notificationListQuery);
  const unread = useQuery(unreadQuery).data ?? 0;
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  const newest = list.data?.pages[0]?.items[0];
  const all = useMutation({
    mutationFn: (through: string) => markAllRead(session, through),
    // Marked, they show read, which the server's message of the change has
    // the page confirm as it reads them again, and the header's count is
    // read again at once (markedRead); refused, as of one that the account
    // no longer has, which the list may still show, they are read again.
    onSuccess: (_, through) => markedRead(client, through, true),
    onError: () => readNotificationsAgain(client),
  });
  const anyUnread =
    unread > 0 || items.some((item) => item.read_at === undefined);
  return (
    <Page title={t("notifications.title")} width="wide">
      <p>{t("notifications.intro")}</p>
      <RefusalAlert refusal={refusalOf(all.error, t)} />
      <Status
        message={all.isSuccess ? t("notifications.marked_all") : undefined}
      />
      {newest !== undefined && (
        <div>
          <Button
            disabled={all.isPending || !anyUnread}
            onClick={() => all.mutate(newest.id)}
          >
            {t("notifications.mark_all")}
          </Button>
        </div>
      )}
      {list.isPending ? (
        <p className="text-fg-muted">{t("notifications.reading")}</p>
      ) : list.isError ? (
        <div className="space-y-2">
          <Alert
            message={[
              t("notifications.read_failed"),
              refusalOf(list.error, t)?.message,
            ]
              .filter((m) => m !== undefined)
              .join(" ")}
          />
          <Button onClick={() => void list.refetch()}>
            {t("notifications.read_again")}
          </Button>
        </div>
      ) : items.length === 0 ? (
        <p className="text-fg-muted">{t("notifications.none")}</p>
      ) : (
        <ol className="space-y-3" aria-label={t("notifications.title")}>
          {items.map((item) => (
            <NotificationItem key={item.id} item={item} />
          ))}
        </ol>
      )}
      <MoreButton list={list}>{t("notifications.more")}</MoreButton>
    </Page>
  );
}

/**
 * A notification: an unread one marked so in words, who mentioned the
 * account, in which project, and when, the comment's preview, the link to
 * where its thread shows, which marks it read as it is followed, and, of an
 * unread one, Mark as read.
 */
function NotificationItem({ item }: { item: NotificationBody }) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const ids = useId();
  const bylineId = `${ids}-byline`;
  const read = useMutation({
    mutationFn: () => markRead(session, item.id),
    // Marked, it shows read, which the server's message of the change has
    // the page confirm as it reads the notifications again, and the
    // header's count is read again at once, on the page that a followed
    // link leads to as well (markedRead); refused, as one that the account
    // no longer has, which the server answers with not_found, they are read
    // again.
    onSuccess: () => markedRead(client, item.id),
    onError: () => readNotificationsAgain(client),
  });
  const unread = item.read_at === undefined;
  const at = new Date(item.created_at);
  const project = item.project.name;
  const actor = item.actor?.display_name;
  return (
    <li
      className={`space-y-2 rounded-md border p-4 ${unread ? "border-accent" : "border-border"}`}
      data-notification={item.id}
    >
      <p id={bylineId} className="break-words">
        {unread && (
          <>
            <span className="rounded-full bg-accent px-2 text-sm font-semibold text-accent-fg">
              {t("notifications.unread")}
            </span>{" "}
          </>
        )}
        <span className="font-medium">
          {actor === undefined
            ? t("notifications.mentioned_by_former", { project })
            : t("notifications.mentioned_by", { name: actor, project })}
        </span>{" "}
        <time
          dateTime={item.created_at}
          title={formatDateTime(i18n.language, at)}
          className="text-fg-muted"
        >
          {formatRelativeTime(i18n.language, at)}
        </time>
      </p>
      <blockquote className="border-s-2 border-border ps-3 break-words whitespace-pre-wrap">
        {item.preview}
      </blockquote>
      <p>
        <ThreadTargetLink
          projectId={item.project.id}
          place={item}
          describedBy={bylineId}
          onFollow={unread ? () => read.mutate() : undefined}
        >
          {openLabel(t, item.target_type)}
        </ThreadTargetLink>
      </p>
      {unread && (
        <div>
          <Button
            disabled={read.isPending}
            aria-describedby={bylineId}
            onClick={() => read.mutate()}
          >
            {t("notifications.mark_read")}
          </Button>
        </div>
      )}
      <RefusalAlert refusal={refusalOf(read.error, t)} />
    </li>
  );
}
