// What the app's tests of pages render them with: the app's router, in
// memory, at a path, over the app's query client (createQueryClient), whose
// cache holds the instance, the session, and the account that the test
// gives, so that the root route's guards read them without a request, and
// that follows the session as the app does (followSession), once a page, or
// an answer that doubts the session, asks it to (checkSession).

import type {
  AccountBody,
  InstanceBody,
  SignedInBody,
} from "@storycreep/shared/api";
import type { Catalog } from "@storycreep/shared/i18n";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  type AnyRoute,
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { i18n as I18n } from "i18next";
import { I18nextProvider } from "react-i18next";
import { onTestFinished, vi } from "vitest";
import {
  accountQuery,
  createQueryClient,
  instanceQuery,
  sessionQuery,
} from "../api";
import { followSession, sessionChanges } from "../auth/session";
import { Documents } from "../document/documents";
import type { Realtime } from "../document/realtime";
import { ErrorConsoleProvider } from "../errors/ErrorConsole";
import { createErrorLog } from "../errors/log";
import { createAppI18n } from "../i18n";
import { routeTree } from "../routeTree.gen";

/** An instance whose setup has completed, which anyone may register on. */
export const testInstance: InstanceBody = {
  email_configured: false,
  setup_pending: false,
  signup_policy: "open",
  default_locale: "en",
};

/** A session of testAccount. */
export const testSession: SignedInBody = {
  user_id: "usr_test",
  csrf_token: "csrf-token",
  session: {
    id: "ses_test",
    created_at: "2026-11-01T10:00:00Z",
    last_seen_at: "2026-11-01T10:00:00Z",
    expires_at: "2027-01-30T10:00:00Z",
    idle_expires_at: "2026-12-01T10:00:00Z",
    current: true,
  },
};

/** The account of testSession. */
export const testAccount: AccountBody = {
  id: "usr_test",
  email: "jo@example.com",
  display_name: "Jo",
  email_verified: false,
};

export interface RenderAppOptions {
  instance?: InstanceBody;
  /** The page's session, or null for a signed-out page. */
  session?: SignedInBody | null;
  /**
   * The account of the session, testAccount unless it says another, or
   * null for one that the page has yet to read.
   */
  account?: AccountBody | null;
  /** Catalogs to show the app in, instead of the shared ones. */
  catalogs?: Readonly<Record<string, Catalog>>;
  /**
   * The browser's languages, most preferred first, which the page reads as
   * navigator.languages for the test, as the root route's locale does.
   */
  languages?: readonly string[];
  /** The routes, instead of the app's. */
  routes?: AnyRoute;
  /**
   * The page's real-time channel, as a fake that sends the operations of
   * other sessions (fakeRealtime); none if not set.
   */
  realtime?: Realtime;
}

/**
 * Renders the app at path, and returns what the test reads of it. It must
 * be called within a test, after which the browser's languages are as they
 * were again.
 */
export function renderApp(path: string, options: RenderAppOptions = {}) {
  const languages = options.languages ?? ["en"];
  const browser = vi
    .spyOn(navigator, "languages", "get")
    .mockReturnValue([...languages]);
  onTestFinished(() => browser.mockRestore());
  const log = createErrorLog();
  const queryClient = createQueryClient(log, {
    checkSession: () => void follower.check(),
  });
  // A request that the app tries again is tried again at once, rather than
  // seconds later, which a test would wait for.
  const defaults = queryClient.getDefaultOptions();
  queryClient.setDefaultOptions({
    ...defaults,
    queries: { ...defaults.queries, retryDelay: 0 },
  });
  queryClient.setQueryData(
    instanceQuery.queryKey,
    options.instance ?? testInstance,
  );
  const session = options.session === undefined ? null : options.session;
  queryClient.setQueryData(sessionQuery.queryKey, session);
  if (session !== null && options.account !== null) {
    queryClient.setQueryData(
      accountQuery.queryKey,
      options.account ?? testAccount,
    );
  }
  const i18n: I18n = createAppI18n(
    languages,
    document.documentElement,
    options.catalogs,
  );
  const checkSession = () => follower.check();
  // The page's documents, without a real-time channel unless the test
  // gives one, so that its pages follow their projects through their own
  // batches alone.
  const documents = new Documents({
    queryClient,
    realtime: options.realtime,
    log,
    checkSession: () => void follower.check(),
    storage: window.sessionStorage,
  });
  // The documents go after the setup has cleared the tab's storage for the
  // next test, which each writes what its history recorded to as it goes
  // (History.close): it is cleared again once they have.
  onTestFinished(() => {
    documents.close();
    window.sessionStorage.clear();
  });
  const router = createRouter({
    routeTree: options.routes ?? routeTree,
    basepath: "/app",
    history: createMemoryHistory({ initialEntries: [`/app${path}`] }),
    context: { queryClient, i18n, checkSession, documents },
    disableGlobalCatchBoundary: true,
  });
  const follower = followSession(
    queryClient,
    document,
    sessionChanges(queryClient, router),
  );
  onTestFinished(() => follower.stop());
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <ErrorConsoleProvider log={log}>
          <RouterProvider router={router} />
        </ErrorConsoleProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...view, queryClient, i18n, router, checkSession, log, documents };
}
