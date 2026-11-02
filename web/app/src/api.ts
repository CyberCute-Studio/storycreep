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
  MutationCache,
  type MutationMeta,
  QueryCache,
  QueryClient,
  type QueryMeta,
  queryOptions,
} from "@tanstack/react-query";
import type { ErrorLog } from "./errors/log";

/** The client of the API of the server that serves the app. */
export const api = createApiClient({ baseUrl: "" });

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
 * The session of the page, or null if the page is signed out: the server
 * answers 401 then, which is no error of the app. Signing in and out set it
 * (auth/session.ts), and so does any answer of 401 to another request, which
 * tells that the session ended elsewhere, as when it was revoked.
 */
export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: async ({ signal }): Promise<SignedInBody | null> => {
    try {
      return (await api.call("getSession", {}, { signal })).body;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        return null;
      }
      throw error;
    }
  },
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
    },
  });
}
