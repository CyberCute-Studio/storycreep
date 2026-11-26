// What a page reads of the projects that the device keeps offline (PRD
// FR-OFF-02): the page's keeper (offline/keeper.ts), which the router's
// context holds, and its state, as it changes.

import { useRouter } from "@tanstack/react-router";
import { useStore } from "zustand";
import { createStore } from "zustand/vanilla";
import type { OfflineKeeper, OfflineState } from "./keeper";

/** The state of a page without a keeper, as in a test that gives none: nothing kept, nor available. */
const none = createStore<OfflineState>(() => ({
  available: false,
  userId: undefined,
  pins: new Map(),
}));

/** Returns the page's keeper of what the device keeps offline, or undefined where it has none. */
export function useKeeper(): OfflineKeeper | undefined {
  return useRouter().options.context.offline;
}

/** Returns what select reads of the state of the page's keeper, as it changes. */
export function useOffline<T>(select: (state: OfflineState) => T): T {
  return useStore(useKeeper()?.store ?? none, select);
}
