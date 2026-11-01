// The app's router (PRD §10.6): TanStack Router, of the routes that the
// files of src/routes declare, from which its plugin generates
// routeTree.gen.ts, under /app, where the server serves the app.

import type { QueryClient } from "@tanstack/react-query";
import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

/** What every route can read. */
export interface RouterContext {
  queryClient: QueryClient;
}

/** Returns the app's router. */
export function createAppRouter(context: RouterContext) {
  return createRouter({
    routeTree,
    basepath: "/app",
    context,
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
