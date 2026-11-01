import { createI18n } from "@storycreep/shared/i18n";
import {
  focusManager,
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import type { ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, it, vi } from "vitest";
import { createQueryClient, requestVersion, versionQuery } from "../api";
import {
  ErrorConsoleButton,
  ErrorConsoleProvider,
  useErrorConsole,
} from "./ErrorConsole";
import { createErrorLog } from "./log";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function renderConsole(children?: ReactNode) {
  // The Clipboard API, which userEvent.setup stubs, exists only in a secure
  // context, which jsdom's page is not.
  vi.stubGlobal("isSecureContext", true);
  const log = createErrorLog();
  const queryClient = new QueryClient();
  queryClient.setQueryData(versionQuery.queryKey, {
    version: "1.0.0",
    go_version: "go1.26",
  });
  render(
    <I18nextProvider i18n={createI18n("en")}>
      <QueryClientProvider client={queryClient}>
        <ErrorConsoleProvider log={log}>
          <ErrorConsoleButton />
          {children}
        </ErrorConsoleProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return log;
}

// Verifies: NFR-OBS-06
it("lists the errors with details that the user copies, and sends nothing", async () => {
  const user = userEvent.setup();
  const fetch = vi.spyOn(globalThis, "fetch");
  const log = renderConsole();
  log.record("error", new Error("boom"));
  log.record("rejection", new Error("bust"));

  await user.click(await screen.findByRole("button", { name: "Errors: 2" }));
  const dialog = screen.getByRole("dialog", { name: "Error console" });
  const items = within(
    within(dialog).getByRole("list", { name: "Errors, newest first" }),
  ).getAllByRole("listitem");
  expect(items.map((i) => i.querySelector("summary")?.textContent)).toEqual([
    expect.stringContaining("Error: bust"),
    expect.stringContaining("Error: boom"),
  ]);

  await user.click(
    within(dialog).getByRole("button", { name: "Copy all details" }),
  );
  expect(within(dialog).getByRole("status")).toHaveProperty(
    "textContent",
    "Copied.",
  );
  const copied = await navigator.clipboard.readText();
  expect(copied).toMatch(/^Storycreep error report\n/);
  expect(copied).toContain("Server: 1.0.0");
  expect(copied).toContain("Error: boom");
  // Newest first, as the list shows them.
  expect(copied.indexOf("Error 2, at")).toBeGreaterThan(0);
  expect(copied.indexOf("Error 1, at")).toBeGreaterThan(
    copied.indexOf("Error 2, at"),
  );

  // The button keeps the keyboard's focus once it has cleared the errors,
  // and says that it is unavailable, as Copy all does.
  const clear = within(dialog).getByRole("button", { name: "Clear" });
  await user.click(clear);
  expect(log.entries()).toEqual([]);
  expect(dialog.textContent).toContain("No errors have happened in this tab.");
  expect(document.activeElement).toBe(clear);
  expect(clear.getAttribute("aria-disabled")).toBe("true");
  const copyAll = within(dialog).getByRole("button", {
    name: "Copy all details",
  });
  expect(copyAll.getAttribute("aria-disabled")).toBe("true");
  await user.click(copyAll);
  await user.click(clear);
  expect(within(dialog).getByRole("status")).toHaveProperty(
    "textContent",
    "Cleared.",
  );
  expect(fetch).not.toHaveBeenCalled();
});

it("reads the server's version that the app asked for when it started, without asking again", async () => {
  const user = userEvent.setup();
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    Response.json(
      {
        error: {
          code: "not_found",
          message: "There is no such page.",
          request_id: "req_1",
        },
      },
      { status: 404 },
    ),
  );
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  requestVersion(queryClient);
  await waitFor(() => expect(log.entries()).toHaveLength(1));
  render(
    <I18nextProvider i18n={createI18n("en")}>
      <QueryClientProvider client={queryClient}>
        <ErrorConsoleProvider log={log}>
          <ErrorConsoleButton />
        </ErrorConsoleProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
  await user.click(screen.getByRole("button", { name: "Errors: 1" }));
  expect(screen.getByRole("dialog").textContent).toContain(
    "There is no such page.",
  );
  // A query that failed would be asked for again when its components mount,
  // the tab gets the focus back, or the network comes back.
  try {
    act(() => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
    });
  } finally {
    focusManager.setFocused(undefined);
    onlineManager.setOnline(true);
  }
  await new Promise((resolve) => setTimeout(resolve, 10));
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(log.entries()).toHaveLength(1);
});

it("copies one error's details", async () => {
  const user = userEvent.setup();
  const log = renderConsole();
  log.record("error", new Error("first"));
  log.record("error", new Error("second"));
  await user.click(await screen.findByRole("button", { name: /^Errors: / }));
  const dialog = screen.getByRole("dialog");
  const [newest] = within(dialog).getAllByRole("listitem");
  const summary = newest.querySelector("summary");
  if (summary === null) {
    throw new Error("an error has no summary");
  }
  await user.click(summary);
  // The error's own details, which the copy gives with a report's heading,
  // made as it is copied.
  const details = newest.querySelector("pre")?.textContent ?? "";
  expect(details).toMatch(/^Error 2, at /);
  expect(details).toContain("Error: second");
  expect(details).not.toContain("Report made at");
  await user.click(
    within(newest).getByRole("button", { name: "Copy these details" }),
  );
  const copied = await navigator.clipboard.readText();
  expect(copied).toMatch(/^Storycreep error report\nReport made at: /);
  expect(copied).toContain(details);
  expect(copied).not.toContain("Error: first");
});

// Verifies: NFR-A11Y-01
it("names its button with the label that it shows, and the number of errors", async () => {
  const log = renderConsole();
  const button = await screen.findByRole("button", { name: "Errors: none" });
  expect(button.textContent).toBe("Errors");
  log.record("error", new Error("once"));
  // A user of speech input says the label that the button shows, which its
  // name starts with, and its words are those of its name, in order
  // (WCAG 2.5.3).
  await waitFor(() => expect(button.textContent).toBe("Errors 1"));
  expect(button.getAttribute("aria-label")).toBe("Errors: 1");
});

/** A control that opens the console as the page of an error does. */
function ShowButton() {
  const { show } = useErrorConsole();
  return (
    <button type="button" onClick={show}>
      Show
    </button>
  );
}

// Verifies: NFR-A11Y-01
it("gives the focus back to the control that opened it", async () => {
  const user = userEvent.setup();
  renderConsole(<ShowButton />);
  const show = screen.getByRole("button", { name: "Show" });
  await user.click(show);
  const dialog = screen.getByRole("dialog", { name: "Error console" });
  await user.click(within(dialog).getByRole("button", { name: "Close" }));
  await waitFor(() => expect(document.activeElement).toBe(show));

  // The header's button gets it back when it opened the console.
  const button = screen.getByRole("button", { name: /^Errors: / });
  await user.click(button);
  await user.keyboard("{Escape}");
  await waitFor(() => expect(document.activeElement).toBe(button));
});
