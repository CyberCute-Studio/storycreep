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
 * preference comes before (PRD FR-I18N-02). It returns what stops it, after
 * which the requests ask for no locale, as before it.
 */
export function requestsIn(i18n: I18n): () => void {
  const follow = (locale: string) => {
    pageLocale = locale;
  };
  follow(i18n.language);
  i18n.on("languageChanged", follow);
  return () => {
    i18n.off("languageChanged", follow);
    pageLocale = undefined;
  };
}

/** The client of the API of the server that serves the app. */
export const api = createApiClient({
  baseUrl: "",
  headers: (): Record<string, string> =>
    pageLocale === undefined ? {} : { "Accept-Language": pageLocale },
});

/**
 * What a query or a mutation tells the query client of itself: whether what
 * reads it shows the server's refusals to the user, as a wrong password or
 * a field that the server does not take, which the error console then does
 * not record, since they are the user's to mend, not errors of the app. A
 * mutation is a user's request, whose refusal the form that made it shows,
 * so it does unless it says showsRefusals: false; a query does only if it
 * says showsRefusals: true. An answer of 500 or more is recorded all the
 * same, and so is one without the error envelope, as a proxy's page, which
 * is no refusal of the server's, and which a form shows as unexpected
 * (refusalOf).
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
 * Reads the instance again, as once its first-run setup has completed: if
 * the server cannot answer, the page drops it, and reads it anew where it
 * goes next, rather than holding its setup as pending.
 */
export function readInstanceAgain(client: QueryClient): Promise<void> {
  return client.fetchQuery({ ...instanceQuery, staleTime: 0 }).then(
    () => undefined,
    () => client.removeQueries({ queryKey: instanceQuery.queryKey }),
  );
}

/**
 * How the page reads its session and its account: for as long as it is
 * open, and asked for again by the page itself alone, as it loads
 * (loadSessionState), once its tab is shown again (followSession), and
 * once the error page's Retry asks. A page that could not read them shows
 * the error page, whose header reads them too: what reads them does not ask
 * again as it mounts, nor as the tab gets the focus again, nor as the
 * browser gets its connection again, which would record the failure again
 * each time.
 */
const readByThePage = {
  staleTime: Number.POSITIVE_INFINITY,
  retryOnMount: false,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const;

/**
 * Asks the server for the session of the browser's cookie, and returns null
 * if it has none: the server answers 401 unauthenticated then, which is no
 * error of the app (signedOutBy). A 401 without the error envelope, as a
 * proxy's page, is no answer of the server's, which the error console
 * records, rather than a page that seems signed out.
 */
export async function fetchSession(
  signal?: AbortSignal,
): Promise<SignedInBody | null> {
  try {
    return (await api.call("getSession", {}, { signal })).body;
  } catch (error) {
    if (signedOutBy(error)) {
      return null;
    }
    throw error;
  }
}

/**
 * The session of the page, or null if the page is signed out. Signing in
 * and out set it (auth/session.ts), and so does the page once it asks the
 * server for it again (followSession): when it is shown again after another
 * tab may have changed it, or when the answer to another request tells
 * that it may have changed elsewhere (sessionDoubtedBy), as when it was
 * revoked.
 */
export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: ({ signal }): Promise<SignedInBody | null> => fetchSession(signal),
  ...readByThePage,
});

/**
 * The signed-in account, which a page asks for only while it has a session.
 * The account's own screens change it, and set it in the cache with the
 * answer. It is read as the session is (readByThePage).
 */
export const accountQuery = queryOptions({
  queryKey: ["account"],
  queryFn: async ({ signal }): Promise<AccountBody> =>
    (await api.call("getAccount", {}, { signal })).body,
  ...readByThePage,
});

/**
 * How long the page keeps what the server answered to a request about
 * credentials, or how it refused it, before it asks again (askedSparingly).
 */
const credentialsKept = 60_000;

/**
 * Reports whether the page tries again a query that has failed failures
 * times: up to three times more, unless the server refused it, with a
 * status below 500, which it would refuse again.
 */
function retried(failures: number, error: unknown): boolean {
  return failures < 3 && !(error instanceof ApiError && error.status < 500);
}

/**
 * Reports whether error is the server's own answer, with the error
 * envelope, as a refusal, or a fault of the server's, as 500, which its
 * rate limits counted as they count any request that reaches it, rather
 * than a request that did not reach it: one that no answer came to
 * (NetworkError), or one that a proxy answered with a page of its own, as
 * 502 while the server restarts.
 */
function answered(error: unknown): boolean {
  return error instanceof ApiError && error.error !== undefined;
}

/**
 * Reports whether the page may ask again, of its own accord, for what query
 * holds, as it would any query's: unless the server answered it with an
 * error (answered), within credentialsKept, and unless it is a list of
 * which the page holds more than one page, every one of which TanStack
 * Query would ask for again, each a request about credentials of its own.
 */
function mayAskAgain(query: {
  state: {
    status: string;
    error: unknown;
    errorUpdatedAt: number;
    data: unknown;
  };
}): boolean {
  const pages = (query.state.data as { pages?: unknown[] } | undefined)?.pages;
  return (
    (pages?.length ?? 0) <= 1 &&
    (query.state.status !== "error" ||
      !answered(query.state.error) ||
      Date.now() - query.state.errorUpdatedAt >= credentialsKept)
  );
}

/**
 * The options of a query that the server counts among the requests about
 * credentials, as reading a target's tokens, or an invitation by its link,
 * of which it allows an address 20 a minute, and an account 10, signing in
 * and changing the password among them (FR-AUTH-05): the page asks for it
 * again at most once a minute, as a component that reads it mounts again,
 * the tab is shown again, or the browser gets its connection again, whether
 * the server answered or refused it, as it refuses a role that may not see
 * the tokens, rather than each time, which would use up what the address
 * and the account may still do, and it does not try again at once what the
 * server failed to answer itself, as with 500. A request that did not reach
 * the server, as one that no answer came to, is tried again, and asked
 * again, as any query's is, since the server counted nothing of it. A list
 * of which the user has had the page show more than its first page is not
 * asked for again of the page's own accord, since each of its pages would
 * be a request of its own (mayAskAgain). A query whose answer keeps for
 * longer says so (staleTime), and the page asks again at once where it
 * knows that the answer has changed, as once it has made a token, a list
 * its first page alone.
 */
export const askedSparingly = {
  staleTime: credentialsKept,
  retry: (failures: number, error: unknown) =>
    retried(failures, error) && !answered(error),
  retryOnMount: mayAskAgain,
  refetchOnMount: mayAskAgain,
  refetchOnWindowFocus: mayAskAgain,
  refetchOnReconnect: mayAskAgain,
};

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
 * asks for more (fetchNextPage), until the last. A list that the server
 * counts among the requests about credentials, as the tokens, is read
 * askedSparingly.
 */
export function pagedQuery<TPage extends Paged, TKey extends QueryKey>(
  queryKey: TKey,
  fetchPage: (query: PageQuery, signal: AbortSignal) => Promise<TPage>,
  {
    limit = 100,
    meta,
    ...asked
  }: { limit?: number; meta?: Meta } & Partial<typeof askedSparingly> = {},
) {
  return infiniteQueryOptions({
    queryKey,
    queryFn: ({
      pageParam,
      signal,
    }: {
      pageParam: string | undefined;
      signal: AbortSignal;
    }) => fetchPage({ cursor: pageParam, limit }, signal),
    initialPageParam: undefined,
    getNextPageParam: (page: TPage) => page.next_cursor ?? undefined,
    meta,
    ...asked,
  });
}

/** The most items that a page of a list holds, which the server allows. */
export const allAtOnce = 200;

/**
 * Reads every page of a list, from the first, with fetchPage, which asks
 * for allAtOnce items a page, and returns their items: a list that a page
 * shows whole, as the tree of a project's boards, or looks through whole,
 * as the user's workspaces for the one that it shows, whose items are few
 * enough, as a project's boards are hundreds at most (PRD §9.1).
 */
export async function everyPage<T>(
  fetchPage: (
    cursor: string | undefined,
  ) => Promise<{ items: T[]; next_cursor: string | null }>,
): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  do {
    const page = await fetchPage(cursor);
    items.push(...page.items);
    cursor = page.next_cursor ?? undefined;
  } while (cursor !== undefined);
  return items;
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

/**
 * Reports whether error is an answer that tells that the page's session may
 * not be the one that the page holds: 401 unauthenticated (signedOutBy), as
 * of a session that was revoked or has ended, or that the page has replaced
 * since, as by changing the password, and 403 csrf_failed, as of a request
 * with the CSRF token of the session before another tab changed the
 * password, which gave the session a new one, since the app sends the
 * token of its session with every request that changes something.
 */
export function sessionDoubtedBy(error: unknown): boolean {
  return (
    signedOutBy(error) ||
    (error instanceof ApiError &&
      error.status === 403 &&
      error.error?.code === "csrf_failed")
  );
}

/**
 * Waits for request, which revokes something that the page lists, as a
 * session or an API token, and takes the server's 404 not_found for done:
 * what the page listed has been revoked elsewhere since, as on another
 * device, which is what the user asked for, and the page reads its list
 * again, without it, rather than showing a refusal under an item that
 * every attempt again would be refused for.
 */
export async function revoking(request: Promise<unknown>): Promise<void> {
  try {
    await request;
  } catch (error) {
    if (
      !(error instanceof ApiError && error.status === 404 && notFoundBy(error))
    ) {
      throw error;
    }
  }
}

/**
 * Reports whether error is the server's answer that what a request names is
 * not there for the user, as a workspace that they are not a member of, or a
 * project in the trash, which the server does not tell from one that does
 * not exist: 404 not_found, or 400 invalid_id, of an ID that a page's
 * address holds that is none.
 */
export function notFoundBy(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    ((error.status === 404 && error.error?.code === "not_found") ||
      (error.status === 400 && error.error?.code === "invalid_id"))
  );
}

/** Options of createQueryClient. */
export interface QueryClientOptions {
  /**
   * Called when the server's answer to a request tells that the page's
   * session may not be the one that the page holds (sessionDoubtedBy): the
   * page then asks the server for its session.
   */
  checkSession?: () => void;
}

/**
 * Returns the app's query client, which records in log each request that
 * failed, but for an answer of 401 unauthenticated, which tells that the
 * page's session may have ended (checkSession), and a refusal of the
 * server's that a form shows (Meta.showsRefusals).
 * A request that the server refused, with a status below 500, is not tried
 * again, since it would be refused again.
 */
export function createQueryClient(
  log: ErrorLog,
  options: QueryClientOptions = {},
): QueryClient {
  const failed = (error: Error, meta: Meta | undefined) => {
    if (sessionDoubtedBy(error)) {
      options.checkSession?.();
    }
    if (signedOutBy(error)) {
      return;
    }
    if (
      meta?.showsRefusals &&
      error instanceof ApiError &&
      error.status < 500 &&
      error.error !== undefined
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
        failed(error, {
          showsRefusals: true,
          ...(mutation.meta as MutationMeta | undefined),
        }),
    }),
    defaultOptions: {
      queries: { retry: retried },
      // A mutation holds what it sent and what it was answered, as a
      // password or a new token's secret: it is dropped once the form that
      // made it is gone, as when the page signs out, rather than kept for
      // minutes after.
      mutations: { gcTime: 0 },
    },
  });
}
