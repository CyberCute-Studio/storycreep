import { useQuery } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  Outlet,
  redirect,
} from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, instanceQuery, sessionQuery } from "../api";
import { loadSessionState } from "../auth/session";
import { applyLocale } from "../i18n";
import type { RouterContext } from "../router";
import { NotFound } from "../shell/NotFound";
import { RouteError } from "../shell/RouteError";
import { Shell } from "../shell/Shell";

export const Route = createRootRouteWithContext<RouterContext>()({
  // Every page needs to know what the instance offers and whether it is
  // signed in, and is shown in the locale that the user prefers, before it
  // shows anything (PRD FR-I18N-02). An instance whose first-run setup is
  // pending has no account to sign in to, so every page leads to setup
  // (FR-ADMIN-02).
  beforeLoad: async ({ context, location }) => {
    const { instance, account } = await loadSessionState(context.queryClient);
    applyLocale(context.i18n, {
      preference: account?.locale,
      instanceDefault: instance.default_locale,
    });
    if (instance.setup_pending && location.pathname !== "/setup") {
      throw redirect({ to: "/setup" });
    }
  },
  component: Root,
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});

function Root() {
  useAccountLocale();
  return (
    <Shell>
      <Outlet />
    </Shell>
  );
}

/**
 * Shows the page in the locale of the account that it is signed in to, as
 * beforeLoad does, once the account changes while the page stays: signing
 * in or out on a page that stays where it is, as an invitation's, or
 * another tab's signing in to another account (followSession).
 */
function useAccountLocale(): void {
  const { i18n } = useTranslation();
  const instanceDefault = useQuery(instanceQuery).data?.default_locale;
  const session = useQuery(sessionQuery).data;
  const account = useQuery({ ...accountQuery, enabled: !!session }).data;
  const preference = session ? account?.locale : undefined;
  useEffect(() => {
    applyLocale(i18n, { preference, instanceDefault });
  }, [i18n, preference, instanceDefault]);
}
