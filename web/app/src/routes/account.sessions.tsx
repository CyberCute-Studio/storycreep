import type { SessionBody } from "@storycreep/shared/api";
import { formatDateTime, formatRelativeTime } from "@storycreep/shared/i18n";
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { api, pagedQuery, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders, endSession } from "../auth/session";
import { Button } from "../ui/Button";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/account/sessions")({
  component: Sessions,
});

/** The account's active sessions, newest first, a page at a time. */
const sessionsQuery = pagedQuery(
  ["sessions"],
  async (query, signal) =>
    (await api.call("listSessions", { query }, { signal })).body,
  { limit: 50 },
);

// The account's sessions (PRD FR-AUTH-02): where it is signed in, with the
// address and the browser that each started from, and when it was last
// used, each of which, or all but this one, the user signs out.
function Sessions() {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const list = useInfiniteQuery(sessionsQuery);
  const others = useMutation({
    mutationFn: async () => {
      return (
        await api.call("revokeOtherSessions", { headers: csrfHeaders(session) })
      ).body.revoked;
    },
    onSuccess: () =>
      client.invalidateQueries({ queryKey: sessionsQuery.queryKey }),
  });
  const sessions = list.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <Page title={t("sessions.title")} width="wide">
      <p>{t("sessions.intro")}</p>
      <Alert
        message={
          others.error === null ? undefined : refusalOf(others.error, t).message
        }
      />
      <Status
        message={
          others.isSuccess
            ? t("sessions.others_revoked", { count: others.data })
            : undefined
        }
      />
      <div>
        <Button
          disabled={others.isPending || sessions.length < 2}
          onClick={() => others.mutate()}
        >
          {t("sessions.revoke_others")}
        </Button>
      </div>
      {list.isError && <Alert message={refusalOf(list.error, t).message} />}
      <ul className="space-y-3" aria-label={t("sessions.title")}>
        {sessions.map((s) => (
          <SessionItem key={s.id} item={s} />
        ))}
      </ul>
      {list.hasNextPage && (
        <Button
          disabled={list.isFetchingNextPage}
          onClick={() => void list.fetchNextPage()}
        >
          {t("sessions.more")}
        </Button>
      )}
    </Page>
  );
}

function SessionItem({ item }: { item: SessionBody }) {
  const { t, i18n } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const router = useRouter();
  const revoke = useMutation({
    mutationFn: async () => {
      await api.call("revokeSession", {
        path: { session_id: item.id },
        headers: csrfHeaders(session),
      });
    },
    onSuccess: () =>
      // Revoking this page's own session signs it out.
      item.current
        ? endSession(client, router)
        : client.invalidateQueries({ queryKey: sessionsQuery.queryKey }),
  });
  const locale = i18n.language;
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p className="font-medium break-words">
        {item.user_agent ?? t("sessions.unknown_browser")}
        {item.current && (
          <>
            {" "}
            <span className="rounded-full bg-surface px-2 text-sm">
              {t("sessions.current")}
            </span>
          </>
        )}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("sessions.ip")}</dt>
        <dd>{item.ip ?? t("sessions.unknown_ip")}</dd>
        <dt className="text-fg-muted">{t("sessions.created")}</dt>
        <dd>{formatDateTime(locale, new Date(item.created_at))}</dd>
        <dt className="text-fg-muted">{t("sessions.last_seen")}</dt>
        <dd>
          <time dateTime={item.last_seen_at}>
            {formatRelativeTime(locale, new Date(item.last_seen_at))}
          </time>
        </dd>
      </dl>
      <Alert
        message={
          revoke.error === null ? undefined : refusalOf(revoke.error, t).message
        }
      />
      <Button
        variant="danger"
        disabled={revoke.isPending}
        onClick={() => revoke.mutate()}
      >
        {item.current ? t("sessions.sign_out_here") : t("sessions.revoke")}
      </Button>
    </li>
  );
}
