// The server's API, as the app calls it: every request goes through the
// client of @storycreep/shared/api, whose types are generated from the
// server's OpenAPI document (PRD FR-API-02), so that the app describes no
// request or answer by hand. Server state is held by TanStack Query (PRD
// §10.6), whose failed requests the error console records.

import {
  type AccountBody,
  ApiError,
  createApiClient,
  type InstanceBody,
  type SignedInBody,
  type VersionBody,
} from "@storycreep/shared/api";
import {
  infiniteQueryOptions,
  MutationCache,
  type MutationMeta,
  QueryCache,
  QueryClient,
  type QueryKey,
  type QueryMeta,
  queryOptions,
} from "@tanstack/react-query";
import type { i18n as I18n } from "i18next";
import type { ErrorLog } from "./errors/log";

/** The locale that the page is shown in, once requestsIn has been told it. */
let pageLocale: string | undefined;

/**
 * Makes every request ask for the server's messages in the locale that i18n
 * shows the page in, from then on, as the locale changes too: the
 * refusals that a form shows in the server's own words, and the emails that
 * a request asks for, where the account prefers no locale. The browser's
 * own Accept-Language names its languages alone, which the user's
 * preference comes before (PRD FR-I18N-02).
 */
export function requestsIn(i18n: I18n): void {
  pageLocale = i18n.language;
  i18n.on("languageChanged", (locale: string) => {
    pageLocale = locale;
  });
}

/** The client of the API of the server that serves the app. */
export const api = createApiClient({
  baseUrl: "",
  headers: (): Record<string, string> =>
    pageLocale === undefined ? {} : { "Accept-Language": pageLocale },
});

/**
 * What a query or a mutation tells the query client of itself. A form that
 * shows the server's refusals to the user, as a wrong password or a field
 * that the server does not take, says so with showsRefusals, and the error
 * console does not record them: they are the user's to mend, not errors of
 * the app. An answer of 500 or more is recorded all the same.
 */
interface Meta extends Record<string, unknown> {
  showsRefusals?: boolean;
}

declare module "@tanstack/react-query" {
  interface Register {
    queryMeta: Meta;
    mutationMeta: Meta;
  }
}

/**
 * The server's version, which the error console's reports name. The app asks
 * for it once, when it starts (requestVersion), and the console reads it
 * from the cache without asking (enabled: false), so that a report names the
 * server without a request of its own, even on the page of a crash, which
 * may show before any other component has.
 */
export const versionQuery = queryOptions({
  queryKey: ["version"],
  queryFn: async ({ signal }): Promise<VersionBody> =>
    (await api.call("getVersion", {}, { signal })).body,
  // The server's version changes only with a new page, which loads anew, so
  // it is kept for as long as the page is open, while no component reads it.
  staleTime: Number.POSITIVE_INFINITY,
  gcTime: Number.POSITIVE_INFINITY,
});

/**
 * What the instance offers before anyone signs in: whether its first-run
 * setup is pending, who can register, whether it sends email, and its
 * default locale. It changes only with the instance's configuration, or
 * when setup completes, which the setup screen tells the cache.
 */
export const instanceQuery = queryOptions({
  queryKey: ["instance"],
  queryFn: async ({ signal }): Promise<InstanceBody> =>
    (await api.call("getInstance", {}, { signal })).body,
  staleTime: Number.POSITIVE_INFINITY,
});

/**
 * Asks the server for the session of the browser's cookie, and returns null
 * if it has none: the server answers 401 then, which is no error of the app.
 */
export async function fetchSession(
  signal?: AbortSignal,
): Promise<SignedInBody | null> {
  try {
    return (await api.call("getSession", {}, { signal })).body;
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      return null;
    }
    throw error;
  }
}

/**
 * The session of the page, or null if the page is signed out. Signing in
 * and out set it (auth/session.ts), and so does any answer of 401 to another
 * request, which tells that the session ended elsewhere, as when it was
 * revoked, and so does the page once it is shown again after another tab
 * changed it (followSession).
 */
export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: ({ signal }): Promise<SignedInBody | null> => fetchSession(signal),
  staleTime: Number.POSITIVE_INFINITY,
});

/**
 * The signed-in account, which a page asks for only while it has a session.
 * The account's own screens change it, and set it in the cache with the
 * answer.
 */
export const accountQuery = queryOptions({
  queryKey: ["account"],
  queryFn: async ({ signal }): Promise<AccountBody> =>
    (await api.call("getAccount", {}, { signal })).body,
  staleTime: Number.POSITIVE_INFINITY,
});

/** What a list of the server answers a page at a time has of its next page. */
interface Paged {
  /** The cursor of the next page, or null on the last. */
  next_cursor: string | null;
}

/** What pagedQuery asks fetchPage for: a page of at most limit items. */
export interface PageQuery {
  /** Where the page starts, or undefined for the first. */
  cursor?: string;
  limit: number;
}

/**
 * The query of a list that the server answers a page at a time, from a
 * cursor, as the sessions and the tokens: fetchPage asks for one page,
 * the first once the page shows the list, and the next whenever the user
 * asks for more (fetchNextPage), until the last.
 */
export function pagedQuery<TPage extends Paged, TKey extends QueryKey>(
  queryKey: TKey,
  fetchPage: (query: PageQuery, signal: AbortSignal) => Promise<TPage>,
  { limit = 100, meta }: { limit?: number; meta?: Meta } = {},
) {
  return infiniteQueryOptions({
    queryKey,
    queryFn: ({
      pageParam,
      signal,
    }: {
      pageParam: string;
      signal: AbortSignal;
    }) => fetchPage({ cursor: pageParam || undefined, limit }, signal),
    initialPageParam: "",
    getNextPageParam: (page: TPage) => page.next_cursor ?? undefined,
    meta,
  });
}

/**
 * Asks for the server's version, once. A failure is recorded as any
 * request's is, and the version is then unknown until the page loads again:
 * nothing asks for it again, as when the tab gets the focus back.
 */
export function requestVersion(client: QueryClient): void {
  void client.prefetchQuery(versionQuery);
}

/**
 * Reports whether error is the server's answer to a request that needs a
 * session, made without one: 401 unauthenticated, which a wrong password's
 * 401 invalid_credentials is not.
 */
export function signedOutBy(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 401 &&
    error.error?.code === "unauthenticated"
  );
}

/** Options of createQueryClient. */
export interface QueryClientOptions {
  /**
   * Called when the server answers a request with 401 unauthenticated, as
   * when the page's session was revoked or has ended: the page is then
   * signed out.
   */
  onUnauthenticated?: () => void;
}

/**
 * Returns the app's query client, which records in log each request that
 * failed, but for an answer of 401 unauthenticated, which ends the page's
 * session (onUnauthenticated), and a refusal that a form shows
 * (Meta.showsRefusals).
 * A request that the server refused, with a status below 500, is not tried
 * again, since it would be refused again.
 */
export function createQueryClient(
  log: ErrorLog,
  options: QueryClientOptions = {},
): QueryClient {
  const failed = (error: Error, meta: Meta | undefined) => {
    if (signedOutBy(error)) {
      options.onUnauthenticated?.();
      return;
    }
    if (
      meta?.showsRefusals &&
      error instanceof ApiError &&
      error.status < 500
    ) {
      return;
    }
    log.record("request", error);
  };
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error, query) => failed(error, query.meta as QueryMeta),
    }),
    mutationCache: new MutationCache({
      onError: (error, _variables, _context, mutation) =>
        failed(error, mutation.meta as MutationMeta),
    }),
    defaultOptions: {
      queries: {
        retry: (failures, error) =>
          failures < 3 && !(error instanceof ApiError && error.status < 500),
      },
      // A mutation holds what it sent and what it was answered, as a
      // password or a new token's secret: it is dropped once the form that
      // made it is gone, as when the page signs out, rather than kept for
      // minutes after.
      mutations: { gcTime: 0 },
    },
  });
}
