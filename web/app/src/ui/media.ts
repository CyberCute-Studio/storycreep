// The window's media queries, as the page reads them while it renders: a
// panel's width, which docks it or makes it a sheet, and whether the system
// asks for reduced motion, which the canvas reads. Each query's list is made
// once for the window's matchMedia, rather than at every render, and a
// browser without matchMedia, as the tests' jsdom, has none.

import { useCallback, useSyncExternalStore } from "react";

/** The lists of the queries made so far, for the matchMedia that made them, which a test may replace. */
let lists:
  | {
      matchMedia: typeof window.matchMedia;
      byQuery: Map<string, MediaQueryList>;
    }
  | undefined;

/** Returns the window's media query list of query, or undefined for a browser without matchMedia. */
export function mediaQueryList(query: string): MediaQueryList | undefined {
  if (typeof window.matchMedia !== "function") {
    return undefined;
  }
  if (lists?.matchMedia !== window.matchMedia) {
    lists = { matchMedia: window.matchMedia, byQuery: new Map() };
  }
  let list = lists.byQuery.get(query);
  if (list === undefined) {
    list = window.matchMedia(query);
    lists.byQuery.set(query, list);
  }
  return list;
}

/**
 * Reports whether the window matches query, as it changes, or fallback for a
 * browser without matchMedia.
 */
export function useMediaQuery(query: string, fallback: boolean): boolean {
  const subscribe = useCallback(
    (changed: () => void) => {
      const list = mediaQueryList(query);
      list?.addEventListener("change", changed);
      return () => list?.removeEventListener("change", changed);
    },
    [query],
  );
  return useSyncExternalStore(
    subscribe,
    () => mediaQueryList(query)?.matches ?? fallback,
  );
}
