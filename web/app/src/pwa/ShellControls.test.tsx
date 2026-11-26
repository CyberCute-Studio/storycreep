import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CrashBoundary } from "../App";
import { createQueryClient } from "../api";
import { ErrorConsoleProvider } from "../errors/ErrorConsole";
import { createErrorLog } from "../errors/log";
import { createAppI18n } from "../i18n";
import { renderApp } from "../test/app";
import type { AppShell, ShellState } from "./worker";

// What the header shows of the app's worker and of installing the app (PRD
// FR-OFF-01, plan note N95), over a fake of the page's side of the worker
// (worker.ts): the offer to install, while the browser makes it, and the
// notice of a new version, which takes neither the focus nor what the user
// typed; and the crash page's reload, which is the shell's too.

afterEach(() => {
  vi.restoreAllMocks();
});

/** A fake of the page's side of the worker, whose state the test sets. */
function fakeShell(initial: Partial<ShellState> = {}) {
  let state: ShellState = {
    offline: "available",
    update: false,
    reloading: false,
    installable: false,
    ...initial,
  };
  const listeners = new Set<() => void>();
  const shell = {
    getState: () => state,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    install: vi.fn(async () => {}),
    reload: vi.fn(async () => {}),
    /** Changes the state, as the worker's events do. */
    set(change: Partial<ShellState>) {
      state = { ...state, ...change };
      act(() => {
        for (const listener of listeners) {
          listener();
        }
      });
    },
  } satisfies AppShell & { set: unknown };
  return shell;
}

const header = () => within(screen.getByRole("banner"));

describe("the header", () => {
  // Verifies: FR-OFF-01/AC1
  it("offers to install the app while the browser does", async () => {
    const user = userEvent.setup();
    const shell = fakeShell();
    renderApp("/login", { pwa: shell });
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(header().queryByRole("button", { name: "Install app" })).toBeNull();
    shell.set({ installable: true });
    await user.click(header().getByRole("button", { name: "Install app" }));
    expect(shell.install).toHaveBeenCalledOnce();
    shell.set({ installable: false });
    expect(header().queryByRole("button", { name: "Install app" })).toBeNull();
  });

  // Verifies: FR-OFF-01/AC2
  it("says that a new version is ready without taking the focus or what is typed, and reloads once asked", async () => {
    const user = userEvent.setup();
    const shell = fakeShell();
    renderApp("/login", { pwa: shell });
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    const email = screen.getByLabelText("Email address") as HTMLInputElement;
    await user.click(email);
    await user.type(email, "jo@exam");
    shell.set({ update: true });
    const notice = header().getByRole("status");
    expect(notice.textContent).toContain(
      "A new version of Storycreep is ready. Save what you are editing, then reload to use it.",
    );
    expect(document.activeElement).toBe(email);
    await user.type(email, "ple.com");
    expect(email.value).toBe("jo@example.com");
    expect(shell.reload).not.toHaveBeenCalled();

    await user.click(within(notice).getByRole("button", { name: "Reload" }));
    expect(shell.reload).toHaveBeenCalledOnce();
    shell.set({ reloading: true });
    expect(
      (
        within(notice).getByRole("button", {
          name: "Reloading…",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
});

describe("the crash page", () => {
  it("reloads through the shell, so that a new build's worker that waits takes over first", async () => {
    // React reports the error that its boundary caught.
    vi.spyOn(console, "error").mockImplementation(() => {});
    const user = userEvent.setup();
    const shell = fakeShell({ update: true });
    const log = createErrorLog();
    function Fails(): never {
      throw new Error("a page that fails");
    }
    render(
      <I18nextProvider i18n={createAppI18n(["en"], document.documentElement)}>
        <QueryClientProvider client={createQueryClient(log)}>
          <ErrorConsoleProvider log={log}>
            <CrashBoundary shell={shell}>
              <Fails />
            </CrashBoundary>
          </ErrorConsoleProvider>
        </QueryClientProvider>
      </I18nextProvider>,
    );
    const reload = (await screen.findByRole("button", {
      name: "Reload the page",
    })) as HTMLButtonElement;
    await user.click(reload);
    expect(shell.reload).toHaveBeenCalledOnce();
    shell.set({ reloading: true });
    expect(reload.disabled).toBe(true);
  });
});
