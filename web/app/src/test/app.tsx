// What the app's tests of pages render them with: the app's router, in
// memory, at a path, over a query client whose cache holds the instance, the
// session, and the account that the test gives, so that the root route's
// guards read them without a request.

import type {
  AccountBody,
  InstanceBody,
  SignedInBody,
} from "@storycreep/shared/api";
import type { Catalog } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  type AnyRoute,
  createMemoryHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { i18n as I18n } from "i18next";
import { I18nextProvider } from "react-i18next";
import { accountQuery, instanceQuery, sessionQuery } from "../api";
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
  account?: AccountBody;
  /** Catalogs to show the app in, instead of the shared ones. */
  catalogs?: Readonly<Record<string, Catalog>>;
  languages?: readonly string[];
  /** The routes, instead of the app's. */
  routes?: AnyRoute;
}

/** Renders the app at path, and returns what the test reads of it. */
export function renderApp(path: string, options: RenderAppOptions = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  queryClient.setQueryData(
    instanceQuery.queryKey,
    options.instance ?? testInstance,
  );
  const session = options.session === undefined ? null : options.session;
  queryClient.setQueryData(sessionQuery.queryKey, session);
  if (session !== null) {
    queryClient.setQueryData(
      accountQuery.queryKey,
      options.account ?? testAccount,
    );
  }
  const i18n: I18n = createAppI18n(
    options.languages ?? ["en"],
    document.documentElement,
    options.catalogs,
  );
  const router = createRouter({
    routeTree: options.routes ?? routeTree,
    basepath: "/app",
    history: createMemoryHistory({ initialEntries: [`/app${path}`] }),
    context: { queryClient, i18n },
    disableGlobalCatchBoundary: true,
  });
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <ErrorConsoleProvider log={createErrorLog()}>
          <RouterProvider router={router} />
        </ErrorConsoleProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...view, queryClient, i18n, router };
}
