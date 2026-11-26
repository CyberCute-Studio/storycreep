import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type AppShell, type ShellOptions, startShell } from "./worker";

// The page's side of the app's service worker and of installing the app
// (PRD FR-OFF-01, plan note N95), in a window of fakes: what it registers,
// where it registers nothing, when it says that a new version is ready, and
// how its reload has the new version take over, in its page alone.

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
  /** The waiting worker takes over, as once a page asked it to. */
  takeOver() {
    this.registration.waiting = null;
    this.controller = {};
    this.dispatchEvent(new Event("controllerchange"));
  }
}

interface FakeWindow {
  win: Window;
  container: FakeContainer;
  reload: ReturnType<typeof vi.fn>;
  document: EventTarget & { visibilityState: DocumentVisibilityState };
}

/** Returns a window of fakes: a secure context with service workers, unless told otherwise. */
function fakeWindow({
  secure = true,
  serviceWorker = true,
}: {
  secure?: boolean;
  serviceWorker?: boolean;
} = {}): FakeWindow {
  const container = new FakeContainer();
  const reload = vi.fn();
  const document = Object.assign(new EventTarget(), {
    visibilityState: "visible" as DocumentVisibilityState,
  });
  const win = Object.assign(new EventTarget(), {
    isSecureContext: secure,
    navigator: serviceWorker ? { serviceWorker: container } : {},
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

  it("registers nothing in a browser without service workers, nor in the development server", async () => {
    const without = await start(fakeWindow({ serviceWorker: false }));
    expect(without.getState().offline).toBe("unsupported");
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
    w.container.controller = {};
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
    w.container.controller = {};
    w.container.registration.waiting = new FakeWorker();
    const shell = await start(w);
    expect(shell.getState().update).toBe(true);
  });

  it("is not said of the first worker, nor in a page that loaded without the worker, which has the server's build", async () => {
    const w = fakeWindow();
    const shell = await start(w);
    w.container.registration.newWorker();
    expect(shell.getState().update).toBe(false);
  });

  // Verifies: FR-OFF-01/AC2
  it("takes over once the user reloads, after the page's batches are sent, and the page loads anew", async () => {
    const w = fakeWindow();
    w.container.controller = {};
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
    w.container.controller = {};
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
    w.container.controller = {};
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
