// The page's side of the app's service worker, and of installing the app
// (PRD FR-OFF-01, plan note N95). The page registers the worker of the
// build (src/sw/sw.ts), which keeps the app's shell, in a secure context
// alone: browsers give a page served over plain HTTP from another machine
// no service worker, and there the app runs online alone, with nothing to
// register and no error, as the header's warning says (shell/transport.ts).
//
// A new build of the app is a new worker, which installs as the page goes
// on, and then waits: the page that its worker answered goes on with its own
// build, whose files its worker keeps, and says that a new version is
// ready, and the user reloads when they choose, once they have saved what
// they are editing. The reload waits for the page's batches that are on
// their way to the server, and then has the new worker take over, which
// every page of the app then has, and loads the page anew. The page asks
// the server for a new worker once an hour, and once it is shown again after
// that, since a page of the app may stay open for days, and browsers ask
// only as a page loads.
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
   * and "unsupported" in a browser without service workers, where the app
   * runs online alone.
   */
  offline: "available" | "insecure" | "unsupported";
  /** Whether a new build of the app waits to take over from the page's. */
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
   * Waits for the page's batches, has the new build's worker take over, if
   * one waits, and loads the page anew.
   */
  reload(): Promise<void>;
}

export interface ShellOptions {
  /** Whether to register the worker, which the build has, and the development server not. */
  register: boolean;
  /** What a reload waits for first: the page's batches that are on their way to the server. */
  beforeReload?: () => Promise<void>;
  /** How long, in milliseconds, the page waits between its questions for a new worker. */
  checkEvery?: number;
  /**
   * How long, in milliseconds, a reload waits for the new worker to take
   * over before it loads the page anew all the same, which the new worker
   * then answers once it has.
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
  // once it has: another page, which did not, goes on as it is.
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
    state.offline === "available" ? win.navigator.serviceWorker : undefined;
  if (container !== undefined && register) {
    container.addEventListener("controllerchange", () => {
      if (takeover) {
        win.location.reload();
      }
    });
    container
      .register(workerURL, { scope: appPath, updateViaCache: "none" })
      .then(
        // A browser driven by a test may answer with nothing, as
        // Playwright's that blocks service workers does.
        (r: ServiceWorkerRegistration | undefined) => {
          if (r !== undefined) {
            registration = r;
            watch(r, container);
          }
        },
        (error: unknown) => {
          // A browser may refuse it, as for a certificate that it does not
          // trust, or in a private window: the app runs online alone then,
          // as without a worker, which is no error of the app's.
          console.warn("The app's service worker was not registered.", error);
        },
      );
  }

  /**
   * Follows the workers of r: a new build's that waits, once the page has a
   * worker, which its build is then of. A page without one, as on the first
   * visit, or after a reload that skipped the worker, loaded the server's
   * build, which a worker that waits is of too.
   */
  function watch(r: ServiceWorkerRegistration, sw: ServiceWorkerContainer) {
    const waits = () => {
      if (r.waiting !== null && sw.controller !== null) {
        set({ update: true });
      }
    };
    waits();
    r.addEventListener("updatefound", () => {
      const worker = r.installing;
      worker?.addEventListener("statechange", () => {
        if (worker.state === "installed") {
          waits();
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
      try {
        await beforeReload?.();
      } catch {
        // The reload goes on: the user asked for it.
      }
      const waiting = registration?.waiting;
      if (waiting && container?.controller) {
        takeover = true;
        waiting.postMessage({ type: "SKIP_WAITING" });
        win.setTimeout(() => win.location.reload(), takeoverWait);
      } else {
        // The new build's worker took over already, as once another page
        // asked it to, or the page has none.
        win.location.reload();
      }
    },
  };
}

/** Returns whether a page of win can have the worker. */
function offlineState(win: Window): ShellState["offline"] {
  if (!win.isSecureContext) {
    return "insecure";
  }
  return "serviceWorker" in win.navigator ? "available" : "unsupported";
}

/** Returns the state of shell, which the component renders anew as it changes. */
export function useShellState(shell: AppShell): ShellState {
  return useSyncExternalStore(shell.subscribe, shell.getState);
}
