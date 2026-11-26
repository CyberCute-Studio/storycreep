// The app's service worker (PRD FR-OFF-01, plan note N95), which
// vite-plugin-pwa builds into the build's sw.js, with the list of the
// build's files, whose versions Workbox's precache keeps in the browser's
// cache as the worker installs, for the app's shell. Every file of the
// build is answered from the cache: the files of a build go together, so a
// page that the worker answered with its shell loads the scripts of that
// shell's build, and keeps loading them, as a route's that it opens later,
// once the server has another build and has them no longer. A new build is
// a new worker, which waits, until the page asks it to take over
// (SKIP_WAITING), as once the user takes the reload that the page offers
// (src/pwa/worker.ts), or every page of the old one has gone.
//
// A navigation that the server answers with the entry page is answered with
// the shell that the cache holds, with a new nonce (shell.ts), and the
// worker asks the server for the entry page meanwhile, which takes the
// cached one's place if it is of the same build, so that the headers that
// the server sends with it now, as its Content-Security-Policy, are the
// next answer's. The worker answers nothing else: a request of the API, of
// an uploaded file, or of the real-time channel goes to the server as if
// there were no worker, and no answer of the server's is ever kept but the
// build's files.

import { cacheNames } from "workbox-core";
import {
  cleanupOutdatedCaches,
  getCacheKeyForURL,
  matchPrecache,
  type PrecacheEntry,
  precacheAndRoute,
} from "workbox-precaching";
import { registerRoute } from "workbox-routing";
import { answersWithShell, newNonce, shellOf, withNonce } from "./shell";

declare const self: ServiceWorkerGlobalScope & {
  /** The build's files, which vite-plugin-pwa writes in place of this. */
  __WB_MANIFEST: (string | PrecacheEntry)[];
};

/** The entry page's URL, /app/, which the build's list names the shell by (vite.config.ts). */
const shellURL = new URL("./", self.location.href).href;

// Navigations go first: the precache's own route would answer /app/ with
// the cached shell as it is, with the nonce of the response that it cached.
registerRoute(
  ({ request, url }) =>
    request.mode === "navigate" && answersWithShell(url, self.location.origin),
  async ({ event, request }) => {
    const cached = await matchPrecache(shellURL);
    if (cached === undefined) {
      // biome-ignore lint/plugin: the service worker passes on what it cannot answer
      return fetch(request);
    }
    const body = await cached.text();
    event.waitUntil(refreshShell(cached, body));
    return withNonce(cached, body, newNonce());
  },
);
precacheAndRoute(self.__WB_MANIFEST);
cleanupOutdatedCaches();

self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") {
    void self.skipWaiting();
  }
});

/**
 * Asks the server for the entry page, and keeps its answer in place of
 * cached, whose body is body, if it is of the same build: the same page
 * but for its nonce. Of another build, it is the next worker's to keep. An
 * answer that is not the page, as an error's or a redirect's, or none, as
 * offline, leaves the cached one.
 */
async function refreshShell(cached: Response, body: string): Promise<void> {
  try {
    // biome-ignore lint/plugin: the service worker asks the server for its shell
    const response = await fetch(shellURL, {
      cache: "no-store",
      credentials: "same-origin",
      redirect: "manual",
    });
    if (!response.ok || response.type !== "basic") {
      return;
    }
    const fresh = await response.clone().text();
    if (shellOf(response, fresh) !== shellOf(cached, body)) {
      return;
    }
    const key = getCacheKeyForURL(shellURL);
    if (key !== undefined) {
      const cache = await caches.open(cacheNames.precache);
      await cache.put(key, response);
    }
  } catch {
    // Offline, or the server could not be reached: the cached shell stands.
  }
}
