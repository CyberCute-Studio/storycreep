// The server's API, as the app calls it: every request goes through the
// client of @storycreep/shared/api, whose types are generated from the
// server's OpenAPI document (PRD FR-API-02), so that the app describes no
// request or answer by hand. Server state is held by TanStack Query (PRD
// §10.6), whose failed requests the error console records.

import {
  ApiError,
  createApiClient,
  type VersionBody,
} from "@storycreep/shared/api";
import {
  MutationCache,
  QueryCache,
  QueryClient,
  queryOptions,
} from "@tanstack/react-query";
import type { ErrorLog } from "./errors/log";

/** The client of the API of the server that serves the app. */
export const api = createApiClient({ baseUrl: "" });

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
 * Asks for the server's version, once. A failure is recorded as any
 * request's is, and the version is then unknown until the page loads again:
 * nothing asks for it again, as when the tab gets the focus back.
 */
export function requestVersion(client: QueryClient): void {
  void client.prefetchQuery(versionQuery);
}

/**
 * Returns the app's query client, which records in log each request that
 * failed. A request that the server refused, with a status below 500, is not
 * tried again, since it would be refused again.
 */
export function createQueryClient(log: ErrorLog): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({
      onError: (error) => log.record("request", error),
    }),
    mutationCache: new MutationCache({
      onError: (error) => log.record("request", error),
    }),
    defaultOptions: {
      queries: {
        retry: (failures, error) =>
          failures < 3 && !(error instanceof ApiError && error.status < 500),
      },
    },
  });
}
