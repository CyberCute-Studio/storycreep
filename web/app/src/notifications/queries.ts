// The account's notification centre (PRD FR-COMMENT-02, plan item 75): the
// notifications of the comments that mention the account, across every
// workspace that it is a member of, as the server shows them, newest first,
// a page at a time, and how many of them are unread, which the header
// counts; reading one marks it read, and so does marking every one up to
// the newest that the page lists. The server tells each of the account's
// open sessions of each change of them over the real-time channel, which
// the shell listens to (useNotificationListener), and which reads them
// again then, and as each socket opens, as a change may have come while
// none was.

import type { SignedInBody } from "@storycreep/shared/api";
import {
  type QueryClient,
  queryOptions,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { api, pagedQuery, sessionQuery } from "../api";
import { csrfHeaders } from "../auth/session";

/** The key of every read of the account's notifications, which a change of them reads again. */
export const notificationsKey = ["notifications"] as const;

/** How many unread notifications the account has, across its workspaces, which the header counts. */
export const unreadQuery = queryOptions({
  queryKey: [...notificationsKey, "unread"],
  queryFn: async ({ signal }): Promise<number> =>
    (await api.call("countUnreadNotifications", {}, { signal })).body.unread,
});

/** How many notifications a page of the list holds. */
const listPage = 50;

/**
 * The account's notifications, newest first, a page at a time, which the
 * page of the notifications lists, and says why it could not read
 * (showsRefusals).
 */
export const notificationListQuery = pagedQuery(
  [...notificationsKey, "list"],
  async (query, signal) =>
    (await api.call("listNotifications", { query }, { signal })).body,
  { limit: listPage, meta: { showsRefusals: true } },
);

/** Reads the account's notifications again, the list and the count, as once one of them has changed. */
export function readNotificationsAgain(client: QueryClient): Promise<void> {
  return client.invalidateQueries({ queryKey: notificationsKey });
}

/** Marks the notification of id read, as the account's session, whose CSRF token it sends. */
export async function markRead(
  session: SignedInBody | null | undefined,
  id: string,
): Promise<void> {
  await api.call("readNotification", {
    path: { notification_id: id },
    headers: csrfHeaders(session),
  });
}

/**
 * Marks read the notification of through and every one of the account's
 * before it, as the list orders them, so that one that came after the page
 * listed through stays unread.
 */
export async function markAllRead(
  session: SignedInBody | null | undefined,
  through: string,
): Promise<void> {
  await api.call("readAllNotifications", {
    body: { through },
    headers: csrfHeaders(session),
  });
}

/**
 * Listens to the account's notifications over the page's real-time channel
 * while the page is signed in, as the shell does on every page, which keeps
 * the channel's socket open, and reads them again, the header's count and
 * the list, each time the channel tells that they changed, or that it has
 * opened a socket, as it may have missed a change while none was open.
 */
export function useNotificationListener(): void {
  const client = useQueryClient();
  const realtime = useRouter().options.context.realtime;
  const signedIn = !!useQuery(sessionQuery).data;
  useEffect(() => {
    if (!signedIn || realtime === undefined) {
      return;
    }
    return realtime.listen(() => void readNotificationsAgain(client));
  }, [client, realtime, signedIn]);
}
