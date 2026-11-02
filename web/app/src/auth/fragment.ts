// The token of a link that the instance emails or an admin gives out, which
// is in the fragment of the link's URL, as /app/reset-password#token=…, so
// that a browser never sends it to a server, nor writes it in a Referer
// (PRD FR-AUTH-03, plan note N26): the page reads it, sends it in the body
// of its request, and keeps what became of the link by it.

import { queryOptions, skipToken } from "@tanstack/react-query";
import { useLocation } from "@tanstack/react-router";

/** Returns the token of a URL's fragment, as token=… or #token=…, if it has one. */
export function fragmentToken(hash: string): string | undefined {
  const token = new URLSearchParams(hash.replace(/^#/, "")).get("token");
  return token === null || token === "" ? undefined : token;
}

/** Returns the token of the page's fragment, if it has one. */
export function useFragmentToken(): string | undefined {
  return fragmentToken(useLocation({ select: (location) => location.hash }));
}

/**
 * The query of what became of the link of token once its page, page, used
 * it, as a reset of a password, or an invitation's acceptance, which the
 * page sets (setQueryData) and shows in place of the link's form. It is
 * kept by the token, past the page's components and its session, which
 * sessionless lists page for (auth/session.ts), so that a page that shows
 * itself anew, as once another tab has signed in, or once its session has
 * ended elsewhere, which leaves a link's page where it is, says what became
 * of the link, rather than offering it again, which the server would
 * refuse as used. Nothing asks the server for it.
 */
export function linkOutcome<T>(
  page: "reset-password" | "invite",
  token: string,
) {
  return queryOptions<T>({
    queryKey: [page, token, "outcome"],
    queryFn: skipToken,
    staleTime: Number.POSITIVE_INFINITY,
    gcTime: Number.POSITIVE_INFINITY,
  });
}
