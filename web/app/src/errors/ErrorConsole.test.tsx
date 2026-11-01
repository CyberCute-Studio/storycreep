import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, it, vi } from "vitest";
import { versionQuery } from "../api";
import { ErrorConsoleButton, ErrorConsoleProvider } from "./ErrorConsole";
import { createErrorLog } from "./log";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function renderConsole() {
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

  await user.click(
    await screen.findByRole("button", { name: "Error console, 2 errors" }),
  );
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

it("copies one error's details", async () => {
  const user = userEvent.setup();
  const log = renderConsole();
  log.record("error", new Error("first"));
  log.record("error", new Error("second"));
  await user.click(
    await screen.findByRole("button", { name: /^Error console/ }),
  );
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
