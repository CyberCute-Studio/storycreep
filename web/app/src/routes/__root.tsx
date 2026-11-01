import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";
import type { RouterContext } from "../router";
import { NotFound } from "../shell/NotFound";
import { RouteError } from "../shell/RouteError";
import { Shell } from "../shell/Shell";

export const Route = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <Shell>
      <Outlet />
    </Shell>
  ),
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});
