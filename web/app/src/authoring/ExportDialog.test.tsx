import type { AuthoringExportBody } from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { type ReactNode, Suspense } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, sessionQuery } from "../api";
import { refused } from "../test/fake";
import { ExportButton } from "./ExportDialog";

// The dialog of the authoring export (PRD FR-DATA-01 AC5, AC6): what it asks
// the server for, the notice of personal data, an archive's size with the
// warning of the import limit, and an export that is already on its way.

const base: AuthoringExportBody = {
  id: "job_a",
  project_id: "prj_a",
  status: "queued",
  profile: "full",
  include_assets: true,
  include_releases: true,
  contains_personal_data: true,
  import_max_bytes: 2048,
  exceeds_import_limit: false,
  created_at: "2026-11-22T10:00:00Z",
  expires_at: "2026-11-29T10:00:00Z",
};

const release = {
  id: "snp_a",
  version: "1.2.0",
  status: "ready",
} as never;

/**
 * Answers the dialog's requests, with created as the answer of asking for an
 * export, and readError, if any, as the answer of reading one.
 */
function serve(
  newest: AuthoringExportBody | null,
  created?: unknown,
  readError?: Error,
) {
  return vi.spyOn(api, "call").mockImplementation((async (op: string) => {
    switch (op) {
      case "listSnapshots":
        return { body: { items: [release], next_cursor: null } };
      case "listAuthoringExports":
        return { body: { items: newest === null ? [] : [newest] } };
      case "getAuthoringExport":
        if (readError !== undefined) {
          throw readError;
        }
        return { body: newest ?? { ...base, id: "job_b" } };
      case "createAuthoringExport":
        if (created instanceof Error) {
          throw created;
        }
        return { body: created };
      default:
        throw new Error(`unexpected ${op}`);
    }
  }) as never);
}

async function open() {
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
  render(<ExportButton projectId="prj_a" />, { wrapper });
  await userEvent.click(
    await screen.findByRole("button", { name: "Export project" }),
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("the authoring export's dialog", () => {
  // Verifies: FR-DATA-01/AC5, FR-DATA-01/AC6
  it("asks for a full export of the draft with assets and releases by default, and says that it holds personal data", async () => {
    const call = serve(null, base);
    await open();
    expect(screen.getByText(/contains personal data/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "createAuthoringExport",
        expect.objectContaining({
          path: { project_id: "prj_a" },
          body: {
            source: "draft",
            profile: "full",
            include_assets: true,
            include_releases: true,
          },
        }),
      ),
    );
    expect(await screen.findByText(/Waiting to start/)).toBeTruthy();
  });

  // Verifies: FR-DATA-01/AC5
  it("offers the content-only profile, without the notice, of a release, without assets", async () => {
    const call = serve(null, { ...base, profile: "content" });
    await open();
    await userEvent.click(screen.getByRole("radio", { name: /Content only/ }));
    expect(screen.queryByText(/contains personal data/)).toBeNull();
    await screen.findByRole("option", { name: /1\.2\.0/ });
    await userEvent.selectOptions(screen.getByRole("combobox"), "snp_a");
    expect(screen.getByText(/no folders/)).toBeTruthy();
    await userEvent.click(
      screen.getByRole("checkbox", { name: /Include assets/ }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "createAuthoringExport",
        expect.objectContaining({
          body: {
            source: "snp_a",
            profile: "content",
            include_assets: false,
            include_releases: true,
          },
        }),
      ),
    );
  });

  it("shows a ready archive's size, warns above the import limit, and links to its file", async () => {
    serve({
      ...base,
      status: "ready",
      size_bytes: 4096,
      exceeds_import_limit: true,
      file_name: "harbour-draft-authoring.zip",
    });
    await open();
    expect(await screen.findByText(/Ready: 4 kB/)).toBeTruthy();
    expect(screen.getByText(/larger than this instance/)).toBeTruthy();
    const link = screen.getByRole("link", { name: "Download archive" });
    expect(link.getAttribute("href")).toBe(
      "/api/v1/projects/prj_a/authoring-exports/job_a/download",
    );
  });

  it("says that an archive past its time is gone, rather than start a download that fails", async () => {
    const ready = {
      ...base,
      status: "ready" as const,
      size_bytes: 4096,
      file_name: "harbour-draft-authoring.zip",
      expires_at: "2020-01-01T00:00:00Z",
    };
    const call = serve(ready);
    const answer = call.getMockImplementation();
    let expired = false;
    call.mockImplementation((async (op: string, ...rest: unknown[]) => {
      if (op === "getAuthoringExport" && expired) {
        throw refused(404, { code: "not_found", message: "no such export" });
      }
      return (answer as (...args: unknown[]) => unknown)(op, ...rest);
    }) as never);
    await open();
    const link = await screen.findByRole("link", { name: "Download archive" });
    expired = true;
    await userEvent.click(link);
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "Download archive" }),
      ).toBeNull(),
    );
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("follows the export on its way when the server refuses another", async () => {
    const refusal = refused(409, {
      code: "export_in_progress",
      message: "on its way",
      details: { export_id: "job_b" },
    });
    const call = serve(null, refusal);
    await open();
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "getAuthoringExport",
        expect.objectContaining({
          path: { project_id: "prj_a", export_id: "job_b" },
        }),
        expect.anything(),
      ),
    );
    expect(screen.queryByText("on its way")).toBeNull();
  });

  it("shows the export just started when it opens again, before the list has been read", async () => {
    const before = {
      ...base,
      status: "ready" as const,
      size_bytes: 4096,
      file_name: "harbour-draft-authoring.zip",
    };
    const started = { ...base, id: "job_b" };
    const call = serve(before, started);
    const answer = call.getMockImplementation();
    let listHangs = false;
    call.mockImplementation((async (op: string, ...rest: unknown[]) => {
      if (op === "listAuthoringExports" && listHangs) {
        return new Promise(() => {});
      }
      if (op === "getAuthoringExport") {
        const { path } = rest[0] as { path: { export_id: string } };
        return { body: path.export_id === started.id ? started : before };
      }
      return (answer as (...args: unknown[]) => unknown)(op, ...rest);
    }) as never);
    await open();
    await screen.findByRole("link", { name: "Download archive" });
    await userEvent.click(screen.getByRole("button", { name: "Start export" }));
    await screen.findByText("Waiting to start…");
    listHangs = true;
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Export project" }),
    );
    await screen.findByText("Waiting to start…");
    expect(screen.queryByRole("link", { name: "Download archive" })).toBeNull();
    expect(
      screen
        .getByRole("button", { name: "Start export" })
        .hasAttribute("disabled"),
    ).toBe(true);
  });

  it("lets go of an export on its way that the server no longer answers for, and says so", async () => {
    const gone = refused(404, { code: "not_found", message: "no such export" });
    serve({ ...base, status: "building" }, undefined, gone);
    await open();
    await waitFor(() =>
      expect(
        screen
          .getByRole("button", { name: "Start export" })
          .hasAttribute("disabled"),
      ).toBe(false),
    );
    expect(screen.queryByText(/Building/)).toBeNull();
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("keeps reading an export on its way whose first read failed, until it has ended", async () => {
    const queued = { ...base, status: "queued" as const };
    const call = serve(queued);
    const answer = call.getMockImplementation();
    let reads = 0;
    call.mockImplementation((async (op: string, ...rest: unknown[]) => {
      if (op !== "getAuthoringExport") {
        return (answer as (...args: unknown[]) => unknown)(op, ...rest);
      }
      reads += 1;
      if (reads === 1) {
        throw new TypeError("Failed to fetch");
      }
      return {
        body: {
          ...base,
          status: "ready",
          size_bytes: 4096,
          file_name: "harbour-draft-authoring.zip",
        },
      };
    }) as never);
    await open();
    expect(
      await screen.findByText(/Ready: 4 kB/, undefined, { timeout: 4000 }),
    ).toBeTruthy();
    expect(reads).toBe(2);
  }, 8000);
});
