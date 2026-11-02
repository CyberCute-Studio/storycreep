import {
  createRootRouteWithContext,
  Outlet,
  redirect,
} from "@tanstack/react-router";
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
  component: () => (
    <Shell>
      <Outlet />
    </Shell>
  ),
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});
