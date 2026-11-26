import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type AppShell, type ShellOptions, startShell } from "./worker";

// The page's side of the app's service worker and of installing the app
// (PRD FR-OFF-01, plan note N95), in a window of fakes: what it registers,
// where it registers nothing, when it says that a new version is ready,
// whether a worker answered the page or not, and how its reload has the
// new version take over, and reloads its page alone.

/** A worker of the registration, whose state the test moves on. */
class FakeWorker extends EventTarget {
  state: ServiceWorkerState = "installing";
  readonly posted: unknown[] = [];
  postMessage(message: unknown) {
    this.posted.push(message);
  }
  moveTo(state: ServiceWorkerState) {
    this.state = state;
    this.dispatchEvent(new Event("statechange"));
  }
}

class FakeRegistration extends EventTarget {
  installing: FakeWorker | null = null;
  waiting: FakeWorker | null = null;
  active: FakeWorker | null = null;
  update = vi.fn(async () => undefined);
  /** A new worker installs and waits, as of a new build. */
  newWorker(): FakeWorker {
    const worker = new FakeWorker();
    this.installing = worker;
    this.dispatchEvent(new Event("updatefound"));
    this.installing = null;
    this.waiting = worker;
    worker.moveTo("installed");
    return worker;
  }
}

class FakeContainer extends EventTarget {
  controller: object | null = null;
  registration = new FakeRegistration();
  register = vi.fn(
    async (_url: string, _options?: RegistrationOptions) =>
      this.registration as unknown as ServiceWorkerRegistration | undefined,
  );
  /**
   * The waiting worker takes over, as once a page asked it to, the page
   * too, whether a worker answered it or not (clients.claim).
   */
  takeOver() {
    const worker = this.registration.waiting;
    this.registration.waiting = null;
    this.registration.active = worker;
    this.controller = worker ?? {};
    this.dispatchEvent(new Event("controllerchange"));
  }
}

/** A worker of an earlier build answered the page, and is the registration's active one. */
function answered(w: FakeWindow) {
  const worker = new FakeWorker();
  worker.state = "activated";
  w.container.registration.active = worker;
  w.container.controller = worker;
}

interface FakeWindow {
  win: Window;
  container: FakeContainer;
  reload: ReturnType<typeof vi.fn>;
  document: EventTarget & { visibilityState: DocumentVisibilityState };
}

/**
 * Returns a window of fakes: a secure context with service workers, unless
 * told otherwise, or whose service workers throw as the page asks for them,
 * if refuses says so.
 */
function fakeWindow({
  secure = true,
  serviceWorker = true,
  refuses = false,
}: {
  secure?: boolean;
  serviceWorker?: boolean;
  refuses?: boolean;
} = {}): FakeWindow {
  const container = new FakeContainer();
  const reload = vi.fn();
  const document = Object.assign(new EventTarget(), {
    visibilityState: "visible" as DocumentVisibilityState,
  });
  const navigator = serviceWorker ? { serviceWorker: container } : {};
  if (refuses) {
    Object.defineProperty(navigator, "serviceWorker", {
      get() {
        throw new DOMException("The operation is insecure.", "SecurityError");
      },
    });
  }
  const win = Object.assign(new EventTarget(), {
    isSecureContext: secure,
    navigator,
    location: { reload },
    document,
    setInterval: (f: () => void, ms: number) => setInterval(f, ms),
    setTimeout: (f: () => void, ms: number) => setTimeout(f, ms),
  });
  return { win: win as unknown as Window, container, reload, document };
}

/** Starts the shell in the window, and lets its registration settle. */
async function start(
  w: FakeWindow,
  options: Partial<ShellOptions> = {},
): Promise<AppShell> {
  const shell = startShell(w.win, { register: true, ...options });
  await vi.advanceTimersByTimeAsync(0);
  return shell;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("registering the worker", () => {
  it("registers the build's worker for the app's path, which the browser asks the server for anew each time", async () => {
    const w = fakeWindow();
    const shell = await start(w);
    expect(w.container.register).toHaveBeenCalledWith("/app/sw.js", {
      scope: "/app/",
      updateViaCache: "none",
    });
    expect(shell.getState()).toEqual({
      offline: "available",
      update: false,
      reloading: false,
      installable: false,
    });
  });

  // Verifies: FR-OFF-01/AC4
  it("registers nothing in a page that is not a secure context, as over plain HTTP from another machine, and says why", async () => {
    // A browser gives such a page no service worker; the fake has one, which
    // the page must not touch all the same.
    const w = fakeWindow({ secure: false });
    const shell = await start(w);
    expect(w.container.register).not.toHaveBeenCalled();
    expect(shell.getState().offline).toBe("insecure");
  });

  it("registers nothing in a browser without service workers, or that throws as the page asks for them, nor in the development server", async () => {
    const without = await start(fakeWindow({ serviceWorker: false }));
    expect(without.getState().offline).toBe("unsupported");
    // As a browser may with the site's data blocked.
    const refusing = await start(fakeWindow({ refuses: true }));
    expect(refusing.getState().offline).toBe("unsupported");
    const w = fakeWindow();
    await start(w, { register: false });
    expect(w.container.register).not.toHaveBeenCalled();
  });

  it("goes on without a worker where the browser refuses it, or answers with none", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const refused = fakeWindow();
    refused.container.register.mockRejectedValue(new Error("SecurityError"));
    const shell = await start(refused);
    expect(warn).toHaveBeenCalledOnce();
    expect(shell.getState().update).toBe(false);
    // Playwright's browser that blocks service workers answers with nothing.
    const none = fakeWindow();
    none.container.register.mockResolvedValue(undefined);
    await start(none);
    await shell.reload();
    expect(refused.reload).toHaveBeenCalledOnce();
  });
});

describe("a new version", () => {
  // Verifies: FR-OFF-01/AC2
  it("waits once it has installed, and the page says that it is ready, and goes on as it is until the user reloads", async () => {
    const w = fakeWindow();
    answered(w);
    const shell = await start(w);
    const changes = vi.fn();
    shell.subscribe(changes);
    const worker = w.container.registration.newWorker();
    expect(shell.getState().update).toBe(true);
    expect(changes).toHaveBeenCalled();
    // Nothing takes over, nor reloads, by itself.
    expect(worker.posted).toEqual([]);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(w.reload).not.toHaveBeenCalled();
  });

  it("is said to be ready where it waits as the page registers", async () => {
    const w = fakeWindow();
    answered(w);
    w.container.registration.waiting = new FakeWorker();
    const shell = await start(w);
    expect(shell.getState().update).toBe(true);
  });

  it("is not said of the first worker, which a page that had none takes over, nor where it waits as a page that no worker answered registers, which has the server's build", async () => {
    const w = fakeWindow();
    const shell = await start(w);
    w.container.registration.newWorker();
    expect(shell.getState().update).toBe(false);
    w.container.takeOver();
    expect(shell.getState().update).toBe(false);
    expect(w.reload).not.toHaveBeenCalled();

    // A reload past the cache, as the worker waits.
    const past = fakeWindow();
    past.container.registration.active = new FakeWorker();
    past.container.registration.waiting = new FakeWorker();
    const loaded = await start(past);
    expect(loaded.getState().update).toBe(false);
  });

  it("is said in a page that no worker answered once a worker installs after one that the browser had, and takes the page over once the user reloads", async () => {
    const w = fakeWindow();
    w.container.registration.active = new FakeWorker();
    const shell = await start(w);
    const worker = w.container.registration.newWorker();
    expect(shell.getState().update).toBe(true);
    await shell.reload();
    expect(worker.posted).toEqual([{ type: "SKIP_WAITING" }]);
    expect(w.reload).not.toHaveBeenCalled();
    w.container.takeOver();
    expect(w.reload).toHaveBeenCalledOnce();
  });

  it("is said once a new worker has taken over the page without its asking, as after a reload that did not wait for it", async () => {
    const w = fakeWindow();
    answered(w);
    const shell = await start(w);
    expect(shell.getState().update).toBe(false);
    w.container.registration.waiting = new FakeWorker();
    w.container.takeOver();
    expect(shell.getState().update).toBe(true);
    expect(w.reload).not.toHaveBeenCalled();
    await shell.reload();
    expect(w.reload).toHaveBeenCalledOnce();
  });

  // Verifies: FR-OFF-01/AC2
  it("takes over once the user reloads, after the page's batches are sent, and the page loads anew", async () => {
    const w = fakeWindow();
    answered(w);
    let sent = () => {};
    const beforeReload = vi.fn(
      () => new Promise<void>((resolve) => (sent = resolve)),
    );
    const shell = await start(w, { beforeReload });
    const worker = w.container.registration.newWorker();
    const reloading = shell.reload();
    expect(shell.getState().reloading).toBe(true);
    await vi.advanceTimersByTimeAsync(0);
    // The batches are on their way: the worker waits.
    expect(beforeReload).toHaveBeenCalledOnce();
    expect(worker.posted).toEqual([]);
    sent();
    await reloading;
    expect(worker.posted).toEqual([{ type: "SKIP_WAITING" }]);
    expect(w.reload).not.toHaveBeenCalled();
    w.container.takeOver();
    expect(w.reload).toHaveBeenCalledOnce();
  });

  it("loads the page anew all the same where the worker has not taken over in a while", async () => {
    const w = fakeWindow();
    answered(w);
    const shell = await start(w, { takeoverWait: 3_000 });
    w.container.registration.newWorker();
    await shell.reload();
    await vi.advanceTimersByTimeAsync(2_999);
    expect(w.reload).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(w.reload).toHaveBeenCalledOnce();
  });

  // Verifies: FR-OFF-01/AC2
  it("taking over for another page leaves this one as it is, which reloads at once when its user asks", async () => {
    const w = fakeWindow();
    answered(w);
    const shell = await start(w);
    w.container.registration.newWorker();
    // Another page's user reloaded: the worker takes over every page.
    w.container.takeOver();
    expect(w.reload).not.toHaveBeenCalled();
    expect(shell.getState().update).toBe(true);
    await shell.reload();
    expect(w.reload).toHaveBeenCalledOnce();
  });

  it("is asked for once an hour, and once the page is shown after an hour, and a question that fails is no error", async () => {
    const w = fakeWindow();
    const { update } = w.container.registration;
    update.mockRejectedValue(new TypeError("Failed to fetch"));
    await start(w);
    expect(update).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
    expect(update).toHaveBeenCalledTimes(1);
    // Shown again within the hour: not asked again.
    w.document.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(1);
    // Hidden for longer, as on a machine that slept, whose timers did not run.
    vi.setSystemTime(Date.now() + 2 * 60 * 60 * 1000);
    w.document.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(2);
  });
});

/** The browser's offer to install the app, whose prompt answers with answer. */
function installOffer(answer: () => Promise<unknown>) {
  const event = Object.assign(
    new Event("beforeinstallprompt", { cancelable: true }),
    {
      prompt: vi.fn(answer),
    },
  );
  return event;
}

describe("installing the app", () => {
  // Verifies: FR-OFF-01/AC1
  it("keeps the browser's offer, rather than let it show its own bar, and shows its prompt once the user asks", async () => {
    const w = fakeWindow();
    const shell = await start(w);
    expect(shell.getState().installable).toBe(false);
    const offer = installOffer(async () => ({ outcome: "accepted" }));
    w.win.dispatchEvent(offer);
    expect(offer.defaultPrevented).toBe(true);
    expect(shell.getState().installable).toBe(true);
    await shell.install();
    expect(offer.prompt).toHaveBeenCalledOnce();
    // An offer shows once.
    expect(shell.getState().installable).toBe(false);
    await shell.install();
    expect(offer.prompt).toHaveBeenCalledOnce();
  });

  it("keeps an offer whose prompt did not show, and drops it once the app is installed", async () => {
    const w = fakeWindow();
    const shell = await start(w);
    const offer = installOffer(async () => {
      throw new DOMException("not allowed", "NotAllowedError");
    });
    w.win.dispatchEvent(offer);
    await shell.install();
    expect(shell.getState().installable).toBe(true);
    w.win.dispatchEvent(new Event("appinstalled"));
    expect(shell.getState().installable).toBe(false);
  });
});
