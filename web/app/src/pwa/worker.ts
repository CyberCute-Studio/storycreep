// The page's side of the app's service worker, and of installing the app
// (PRD FR-OFF-01, plan note N95). The page registers the worker of the
// build (src/sw/sw.ts), which keeps the app's shell, in a secure context
// alone: browsers give a page served over plain HTTP from another machine
// no service worker, and there the app runs online alone, with nothing to
// register and no error, as the header's warning says (shell/transport.ts).
//
// The page registers it once it has loaded, so that the worker, which asks
// the server for every file of the build as it installs, does not slow the
// page's own first requests. A new build of the app is a new worker, which
// installs as the page goes on, and then waits: the page that its worker
// answered goes on with its own build, whose files its worker keeps, and
// says that a new version is ready, and the user reloads when they choose,
// once they have saved what they are editing. A page says so whenever a
// worker waits while another is active, whether a worker answered the page
// or not, as one reloaded past the cache, whose build is the server's, and
// may be the waiting worker's already, where the reload loads the same
// build; and a page that a new worker took over from another without its
// asking, as for another page's reload, says so then. A worker that takes
// over a page that had none, as the first one does, says nothing. The
// reload waits a while for the page's batches and saves that are on their
// way to the server, has the new worker take over, which every page of the
// app then has, waits until it has activated, whether it took this page
// over or not, as it does not take over a page that loaded outside its
// scope, as the entry page at /, then for what the page sent meanwhile,
// and loads the page anew. The page asks the server for a new worker once
// an hour, and once it is shown again after that, since a page of the app
// may stay open for days, and browsers ask only as a page loads.
//
// The browser's offer to install the app is the page's to make: the page
// keeps it, rather than let the browser show its own bar, and its header
// offers it while it stands (shell/Shell.tsx).

import { useSyncExternalStore } from "react";
import { appPath } from "../sw/shell";

/** What the page knows of its worker and of installing the app. */
export interface ShellState {
  /**
   * Whether the page can have the worker: "insecure" in a page that is not
   * a secure context, as one served over plain HTTP from another machine,
   * and "unsupported" in a browser without service workers, or that
   * refuses them as the page asks for them or registers the worker, where
   * the app runs online alone.
   */
  offline: "available" | "insecure" | "unsupported";
  /**
   * Whether a new build of the app is ready: its worker waits while another
   * is active, or has taken over the page from another worker.
   */
  update: boolean;
  /** Whether the page is reloading for the new build. */
  reloading: boolean;
  /** Whether the browser offers to install the app now. */
  installable: boolean;
}

/** The page's worker, and installing the app. */
export interface AppShell {
  getState(): ShellState;
  /** Calls listener after each change, until the returned function is called. */
  subscribe(listener: () => void): () => void;
  /** Shows the browser's prompt to install the app, while it offers to. */
  install(): Promise<void>;
  /**
   * Waits a while for the page's batches and saves, has the new build's
   * worker take over, if one waits, and loads the page anew.
   */
  reload(): Promise<void>;
}

export interface ShellOptions {
  /** Whether to register the worker, which the build has, and the development server not. */
  register: boolean;
  /**
   * What a reload waits for, first and again before the page loads anew:
   * the page's batches and saves that are on their way to the server.
   */
  beforeReload?: () => Promise<void>;
  /** How long, in milliseconds, the page waits between its questions for a new worker. */
  checkEvery?: number;
  /**
   * How long, in milliseconds, a reload waits for the new worker to
   * activate before it loads the page anew all the same, as while a
   * request of the old worker's holds it: the old one answers the page
   * then, which says that a new version is ready once the new one takes it
   * over.
   */
  takeoverWait?: number;
}

/** The event of the browser's offer to install the app (Chromium's). */
interface InstallPromptEvent extends Event {
  prompt(): Promise<unknown>;
}

/** The URL of the build's worker, which vite-plugin-pwa writes. */
const workerURL = `${appPath}sw.js`;

/** How often the page asks for a new worker: once an hour. */
const hour = 60 * 60 * 1000;

/** Starts the page's side of the app's worker in win, and returns it. */
export function startShell(win: Window, options: ShellOptions): AppShell {
  const { register, beforeReload, checkEvery = hour } = options;
  const takeoverWait = options.takeoverWait ?? 3_000;
  let state: ShellState = {
    offline: offlineState(win),
    update: false,
    reloading: false,
    installable: false,
  };
  const listeners = new Set<() => void>();
  const set = (change: Partial<ShellState>) => {
    state = { ...state, ...change };
    for (const listener of listeners) {
      listener();
    }
  };
  let prompt: InstallPromptEvent | undefined;
  let registration: ServiceWorkerRegistration | undefined;
  // Whether this page asked the new worker to take over, and so reloads
  // once it has activated: another page, which did not, goes on as it is,
  // and says that a new version is ready.
  let takeover = false;

  win.addEventListener("beforeinstallprompt", (event) => {
    // The header offers it, rather than the browser's own bar.
    event.preventDefault();
    prompt = event as InstallPromptEvent;
    set({ installable: true });
  });
  win.addEventListener("appinstalled", () => {
    prompt = undefined;
    set({ installable: false });
  });

  const container =
    state.offline === "available" ? containerOf(win) : undefined;
  if (container !== undefined && register) {
    // The worker that answers the page's requests, if one does.
    let controller = container.controller;
    container.addEventListener("controllerchange", () => {
      const before = controller;
      controller = container.controller;
      if (!takeover && before !== null) {
        // A new build's worker took over from the page's without the page
        // asking, as for another page's reload, or once a reload that did
        // not wait for it loaded the page from the old one: the page goes
        // on with an older build than the worker's. A worker that takes
        // over a page that had none, as the first one does, says nothing.
        set({ update: true });
      }
    });
    const registerWorker = () =>
      container
        .register(workerURL, { scope: appPath, updateViaCache: "none" })
        .then(
          // A browser driven by a test may answer with nothing, as
          // Playwright's that blocks service workers does.
          (r: ServiceWorkerRegistration | undefined) => {
            if (r !== undefined) {
              registration = r;
              watch(r);
            }
          },
          (error: unknown) => {
            // A browser may refuse it, as for a certificate that it does
            // not trust, or in a private window: the app runs online alone
            // then, as without a worker, which is no error of the app's.
            console.warn("The app's service worker was not registered.", error);
            set({ offline: "unsupported" });
          },
        );
    // Once the page has loaded, so that the worker's install, which asks
    // the server for every file of the build, comes after the page's own.
    if (win.document.readyState === "complete") {
      void registerWorker();
    } else {
      win.addEventListener("load", () => void registerWorker(), {
        once: true,
      });
    }
  }

  /**
   * Follows the workers of r: the page says that a new version is ready
   * whenever a worker waits while another is active, as the page registers,
   * or once the new one has installed as the page goes on. The waiting
   * worker's build is newer than the page's where the active one answered
   * the page, or where the server had the active one's build as the page
   * loaded past the cache; where the server had the waiting one's build by
   * then, the page has that build already, and the notice's reload loads it
   * again. The first worker, which installs while none is active, says
   * nothing, as one of the page's build.
   */
  function watch(r: ServiceWorkerRegistration) {
    if (r.waiting !== null && r.active !== null) {
      set({ update: true });
    }
    r.addEventListener("updatefound", () => {
      const replaces = r.active !== null;
      const worker = r.installing;
      worker?.addEventListener("statechange", () => {
        if (worker.state === "installed" && replaces) {
          set({ update: true });
        }
      });
    });
    let asked = Date.now();
    const check = () => {
      asked = Date.now();
      // Offline, or with the server down, the page asks again later.
      r.update().catch(() => {});
    };
    win.setInterval(check, checkEvery);
    win.document.addEventListener("visibilitychange", () => {
      if (
        win.document.visibilityState === "visible" &&
        Date.now() - asked >= checkEvery
      ) {
        check();
      }
    });
  }

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    async install() {
      const offer = prompt;
      if (offer === undefined) {
        return;
      }
      // An offer shows once: the browser makes another if it offers again.
      prompt = undefined;
      set({ installable: false });
      try {
        await offer.prompt();
      } catch {
        // Not shown, as without the user's gesture: it still stands.
        prompt = offer;
        set({ installable: true });
      }
    },
    async reload() {
      if (state.reloading) {
        return;
      }
      set({ reloading: true });
      await sent();
      const waiting = registration?.waiting;
      if (waiting) {
        // The new worker takes over every page of the app as it activates,
        // this one too, whether a worker answered it or not (clients.claim,
        // in sw.ts), but for a page that loaded outside its scope, as the
        // entry page at /, which reloads once the worker has activated all
        // the same.
        takeover = true;
        await activated(waiting);
      }
      // What the page sent meanwhile goes first too. Where the new worker
      // has not activated by then, the old one answers the page, which says
      // that a new version is ready once the new one takes it over.
      await sent();
      win.location.reload();
    },
  };

  /** Waits for the page's batches and saves, for a while at most (beforeReload). */
  async function sent(): Promise<void> {
    try {
      await beforeReload?.();
    } catch {
      // The reload goes on: the user asked for it.
    }
  }

  /**
   * Asks worker, which waits, to take over, and resolves once it has
   * activated, or once takeoverWait has passed, as while a request of the
   * old worker's still holds it.
   */
  function activated(worker: ServiceWorker): Promise<void> {
    return new Promise((resolve) => {
      const timer = win.setTimeout(resolve, takeoverWait);
      worker.addEventListener("statechange", () => {
        if (worker.state === "activated") {
          win.clearTimeout(timer);
          resolve();
        }
      });
      worker.postMessage({ type: "SKIP_WAITING" });
    });
  }
}

/** Returns whether a page of win can have the worker. */
function offlineState(win: Window): ShellState["offline"] {
  if (!win.isSecureContext) {
    return "insecure";
  }
  return containerOf(win) === undefined ? "unsupported" : "available";
}

/**
 * Returns the service workers of a page of win, or undefined in a browser
 * that has none, or that refuses them as the page asks, as one may with
 * the site's data blocked, where the app runs online alone.
 */
function containerOf(win: Window): ServiceWorkerContainer | undefined {
  try {
    return win.navigator.serviceWorker;
  } catch {
    return undefined;
  }
}

/** Returns the state of shell, which the component renders anew as it changes. */
export function useShellState(shell: AppShell): ShellState {
  return useSyncExternalStore(shell.subscribe, shell.getState);
}
