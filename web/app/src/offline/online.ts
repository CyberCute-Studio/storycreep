// Whether the browser says that it has a connection (navigator.onLine), as
// it changes. TanStack Query's own onlineManager takes a page for online
// until the browser says otherwise, so a page that loads offline, as the
// installed app opened on a flight, would be online to it: the app reads
// the browser instead, to say that a page is offline, and what of it then
// works from the device's copies of projects (PRD FR-OFF-02).

import { useSyncExternalStore } from "react";

/** What online reads of a window. */
export type Connected = Pick<
  Window,
  "addEventListener" | "removeEventListener"
> & {
  navigator: Pick<Navigator, "onLine">;
};

/** Reports whether the browser of win says that it has a connection. */
export function online(win: Connected = window): boolean {
  return win.navigator.onLine !== false;
}

/** Calls listener with whether the browser of win has a connection, each time that it changes, until what it returns is called. */
export function onConnection(
  listener: (online: boolean) => void,
  win: Connected = window,
): () => void {
  const on = () => listener(true);
  const off = () => listener(false);
  win.addEventListener("online", on);
  win.addEventListener("offline", off);
  return () => {
    win.removeEventListener("online", on);
    win.removeEventListener("offline", off);
  };
}

/** Returns whether the browser says that it has a connection, as it changes. */
export function useOnline(): boolean {
  return useSyncExternalStore(
    (changed) => onConnection(changed),
    () => online(),
  );
}
