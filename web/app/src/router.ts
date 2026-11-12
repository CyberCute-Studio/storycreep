// The app's router (PRD §10.6): TanStack Router, of the routes that the
// files of src/routes declare, from which its plugin generates
// routeTree.gen.ts, under /app, where the server serves the app.

import type { QueryClient } from "@tanstack/react-query";
import { createRouter, parseSearchWith } from "@tanstack/react-router";
import type { i18n as I18n } from "i18next";
import type { Documents } from "./document/documents";
import { routeTree } from "./routeTree.gen";
import { focusContentIfLost } from "./shell/focus";

/** What every route can read. */
export interface RouterContext {
  queryClient: QueryClient;
  /** The app's i18next instance, whose locale the root route resolves. */
  i18n: I18n;
  /**
   * Asks the server for the page's session now, and records what it says
   * as the page does once its tab is shown again (followSession), as once
   * a page has done what changes or ends the session in the server alone,
   * as a change of the password or a reset of one.
   */
  checkSession: () => Promise<void>;
  /**
   * The page's documents of projects (PRD §10.6), which a project's pages
   * read its content from, as its loaders do before they show.
   */
  documents: Documents;
}

/**
 * Reads each value of a page's address as the text that it spells, as a
 * search's query of 1e3, 1.50, or null, which the router's own reading,
 * JSON's, would take for 1000, 1.5, or nothing: only a value that the
 * router's links write as JSON, a string quoted, as a query of 1e3 that a
 * link names, and an object or a list, is read as JSON. True and false,
 * and a number as String writes it, as 42, the router reads as a boolean
 * or a number before (qss), whose text String gives back.
 */
export const parseSearch = parseSearchWith((value) =>
  /^["[{]/.test(value) ? JSON.parse(value) : value,
);

/** Returns the app's router. */
export function createAppRouter(context: RouterContext) {
  const router = createRouter({
    routeTree,
    basepath: "/app",
    context,
    parseSearch,
    scrollRestoration: true,
    // An error that the root route's error page could not show, as one of
    // that page itself, reaches the app's CrashBoundary, which shows the
    // error console, rather than the router's own boundary, whose page is in
    // English alone and has no console.
    disableGlobalCatchBoundary: true,
  });
  // Once a page is shown in place of another, as a link's or the error
  // page's retry, the page's content gets the focus that the control that
  // went with the other had. The first page that the tab shows, which has
  // no other before it, leaves the focus where the browser puts it, before
  // the skip link.
  router.subscribe("onRendered", ({ fromLocation }) => {
    if (fromLocation !== undefined) {
      focusContentIfLost(document);
    }
  });
  return router;
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
  interface StaticDataRouteOption {
    /**
     * Whether the page takes the window's width, as a board's page does for
     * its canvas, rather than the column of the layout that it is in, which
     * the layout reads of the pages under it.
     */
    wide?: boolean;
  }
}
