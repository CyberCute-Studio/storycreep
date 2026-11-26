import type { PersonalExportBody } from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode, Suspense } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, sessionQuery } from "../api";
import { refused } from "../test/fake";
import { PersonalDataExport } from "./PersonalDataExport";

// The export of the user's own data (PRD FR-GDPR-01): what it asks the server
// for, the notice of personal data, a ready archive's download, and an export
// that is already on its way.

const base: PersonalExportBody = {
  id: "job_a",
  status: "queued",
  contains_personal_data: true,
  created_at: "2026-11-25T10:00:00Z",
  expires_at: "2026-12-02T10:00:00Z",
};

/**
 * Answers the section's requests, with newest as the newest export, or the
 * refusal of the list, created as the answer of asking for one, and read as
 * the answer of reading one.
 */
function serve(
  newest: PersonalExportBody | null | Error,
  created?: unknown,
  read?: PersonalExportBody,
) {
  return vi.spyOn(api, "call").mockImplementation((async (op: string) => {
    switch (op) {
      case "listPersonalDataExports":
        if (newest instanceof Error) {
          throw newest;
        }
        return { body: { items: newest === null ? [] : [newest] } };
      case "getPersonalDataExport":
        return {
          body: read ?? (newest instanceof Error ? null : newest) ?? base,
        };
      case "createPersonalDataExport":
        if (created instanceof Error) {
          throw created;
        }
        return { body: created };
      default:
        throw new Error(`unexpected ${op}`);
    }
  }) as never);
}

async function show() {
  const i18n = await createI18n("en");
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(sessionQuery.queryKey, { csrf_token: "t" } as never);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <Suspense>{children}</Suspense>
      </QueryClientProvider>
    </I18nextProvider>
  );
  render(<PersonalDataExport />, { wrapper });
  await screen.findByRole("heading", { name: "Export your data" });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("the export of the user's data", () => {
  // Verifies: FR-GDPR-01/AC1
  it("asks for an export of the user's data, says that it holds personal data, and follows it", async () => {
    const call = serve(null, base);
    await show();
    expect(screen.getByText(/contains your personal data/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "createPersonalDataExport",
        expect.objectContaining({ headers: expect.anything() }),
      ),
    );
    expect(await screen.findByText(/Waiting to start/)).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "Start export",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

  it("links a ready archive to its download, which the session's cookie authenticates, until it expires", async () => {
    const ready: PersonalExportBody = {
      ...base,
      status: "ready",
      size_bytes: 40 * 1024,
      file_name: "personal-data-2026-11-25.zip",
      finished_at: "2026-11-25T10:01:00Z",
    };
    serve(ready);
    await show();
    const link = await screen.findByRole("link", { name: "Download archive" });
    expect(link.getAttribute("href")).toBe(
      "/api/v1/auth/account/data-exports/job_a/download",
    );
    expect(link.getAttribute("download")).toBe("personal-data-2026-11-25.zip");
    expect(screen.getByText(/until/)).toBeTruthy();
  });

  it("follows the export on its way when the server refuses another", async () => {
    const refusal = refused(409, {
      code: "export_in_progress",
      details: { export_id: "job_b" },
    });
    const call = serve(null, refusal, {
      ...base,
      id: "job_b",
      status: "building",
    });
    await show();
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    expect(await screen.findByText(/Building the archive/)).toBeTruthy();
    expect(call).toHaveBeenCalledWith(
      "getPersonalDataExport",
      expect.objectContaining({ path: { export_id: "job_b" } }),
      expect.anything(),
    );
    // The live region of refusals stays empty.
    for (const alert of screen.queryAllByRole("alert")) {
      expect(alert.textContent).toBe("");
    }
  });

  it("says that an export failed, and lets the user start another", async () => {
    serve({ ...base, status: "failed", finished_at: "2026-11-25T10:01:00Z" });
    await show();
    expect(await screen.findByText(/The export failed/)).toBeTruthy();
    expect(
      (
        screen.getByRole("button", {
          name: "Start export",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("says why it shows no export when the server refuses the list", async () => {
    serve(refused(429, { code: "rate_limited" }));
    await show();
    expect(
      await screen.findByText("Too many requests. Try again in a moment."),
    ).toBeTruthy();
  });
});
