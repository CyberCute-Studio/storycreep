// The app's service worker (PRD FR-OFF-01, plan note N95), which
// vite-plugin-pwa builds into the build's sw.js, with the list of the
// build's files, whose versions Workbox's precache keeps in the browser's
// cache as the worker installs, for the app's shell. Every file of the
// build is answered from the cache: the files of a build go together, so a
// page that the worker answered with its shell loads the scripts of that
// shell's build, and keeps loading them, as a route's that it opens later,
// once the server has another build and has them no longer. Where the
// browser cannot read its cache, the server answers, as without a worker.
// An entry page that names files of assets/ that the list does not have, as
// one of the other build while two serve at once, fails the install, which
// is tried again as the browser next asks the server for the worker: as a
// page of the app next registers it, or, where a worker is active, as the
// browser or the page next asks for a new one. As it activates, the worker
// takes over each page of the app that has none, as the page that
// registered the first worker, so that it keeps that page's files too. A
// new build is a new worker, which waits, until the page asks it to take
// over (SKIP_WAITING), as once the user takes the reload that the page
// offers (src/pwa/worker.ts), or every page of the old one has gone.
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

import { cacheNames, clientsClaim } from "workbox-core";
import {
  addPlugins,
  cleanupOutdatedCaches,
  getCacheKeyForURL,
  matchPrecache,
  type PrecacheEntry,
  precacheAndRoute,
} from "workbox-precaching";
import { registerRoute } from "workbox-routing";
import {
  answersWithShell,
  assetsOf,
  newNonce,
  shellOf,
  withNonce,
} from "./shell";

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
  async ({ request }) => {
    try {
      const cached = await matchPrecache(shellURL);
      if (cached !== undefined) {
        const body = await cached.text();
        // Not held with waitUntil: the browser activates a new worker,
        // which the user's reload waits for, only once this one has no
        // event under way. A browser keeps a worker that has just answered
        // running for a while, which the refresh needs; one that it stops
        // first leaves the cached shell, as offline does.
        void refreshShell(cached, body);
        return withNonce(cached, body, newNonce());
      }
    } catch {
      // The cache could not be read, as where the browser's storage fails.
    }
    // biome-ignore lint/plugin: the service worker passes on what it cannot answer
    return fetch(request);
  },
);
precacheAndRoute(self.__WB_MANIFEST);
// What the precache keeps as the worker installs: the entry page only if
// the list has every file of assets/ that it names. As two builds serve at
// once, as while the replicas of an instance are upgraded one by one, the
// server may answer the page of the other build, whose scripts nothing
// would have once that build is gone. A file outside assets/, as the
// favicon, an icon, or the manifest, whose name holds no hash, is kept as
// the server answers it then, which may be the other build's, until a
// build changes it: Workbox checks no file against its revision. A plugin
// that decides what the precache keeps replaces Workbox's own, which keeps
// no error's answer, so it keeps none either. Where the cache cannot be
// read as the worker answers a request, the server answers it; an install
// that fails fails as it is.
addPlugins([
  {
    async cacheWillUpdate({ request, response }) {
      if (response.status >= 400) {
        return null;
      }
      if (request.url !== shellURL) {
        return response;
      }
      const files = assetsOf(await response.clone().text(), shellURL);
      return files.every((url) => getCacheKeyForURL(url) !== undefined)
        ? response
        : null;
    },
    async handlerDidError({ event, request }) {
      // biome-ignore lint/plugin: the service worker passes on what it cannot answer
      return event.type === "fetch" ? fetch(request) : undefined;
    },
  },
]);
cleanupOutdatedCaches();
clientsClaim();

self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") {
    void self.skipWaiting();
  }
});

/**
 * Asks the server for the entry page, and keeps its answer in place of
 * cached, whose body is body, if it is of the same build: the same page
 * but for its nonce, and while the cache still has this build's. Of another
 * build, it is the next worker's to keep, and once a new worker has
 * activated, which deleted this build's, it is none's. An answer that is
 * not the page, as an error's or a redirect's, or none, as offline, leaves
 * the cached one.
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
      if ((await cache.match(key)) !== undefined) {
        await cache.put(key, response);
      }
    }
  } catch {
    // Offline, or the server could not be reached: the cached shell stands.
  }
}
