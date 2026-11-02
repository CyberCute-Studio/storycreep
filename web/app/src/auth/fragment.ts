// The token of a link that the instance emails or an admin gives out, which
// is in the fragment of the link's URL, as /app/reset-password#token=…, so
// that a browser never sends it to a server, nor writes it in a Referer
// (PRD FR-AUTH-03, plan note N26): the page reads it, and sends it in the
// body of its request.

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
