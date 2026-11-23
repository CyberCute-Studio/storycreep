import { ApiError, type ProjectImportBody } from "@storycreep/shared/api";
import { useInfiniteQuery } from "@tanstack/react-query";
import { createRootRoute, createRoute } from "@tanstack/react-router";
import {
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { renderApp, testSession } from "../test/app";
import { refused } from "../test/fake";
import { projectsQuery } from "../workspace/queries";
import { ImportButton } from "./ImportDialog";
import { retries, retryAfter, retryWait, storedFileName } from "./importUpload";

// The dialog of the project import (PRD FR-DATA-01 AC4): how it slices and
// sends an archive, tries again, resumes, follows the job to its report or
// its failure, and cancels.

/** The chunk size of the fake imports, small enough to read in a test. */
const chunkBytes = 4;

const report = {
  policy: "remap" as const,
  project_id: "prj_new",
  project_name: "Harbour",
  boards: 2,
  nodes: 7,
  variables: 3,
  components: 1,
  comments: 4,
  play_states: 1,
  releases: 2,
  assets: 5,
  imported_users: 2,
  missing_assets: 1,
};

/** An import of a file of size bytes, uploading, of which received is stored. */
function importOf(
  size: number,
  received: number[] = [],
  more: Partial<ProjectImportBody> = {},
): ProjectImportBody {
  return {
    id: "job_i",
    workspace_id: "ws_a",
    file_name: "harbour.zip",
    size_bytes: size,
    chunk_bytes: chunkBytes,
    chunk_count: Math.ceil(size / chunkBytes),
    received_chunks: received,
    status: "uploading",
    phase: "upload",
    id_policy: "auto",
    progress_done: 0,
    progress_total: 0,
    created_at: "2026-11-23T10:00:00Z",
    expires_at: "2026-11-30T10:00:00Z",
    ...more,
  };
}

/** The archive that the tests upload: ten bytes, or three chunks. */
function archive(content = "0123456789", name = "harbour.zip") {
  return new File([content], name, { type: "application/zip" });
}

/** Reads a chunk's bytes as text, which jsdom's Blob has no method of. */
function textOf(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

type Handler = (input: {
  path?: Record<string, unknown>;
  body?: unknown;
}) => unknown;

/**
 * Answers the dialog's calls, an operation by the handler that handlers has
 * for it, whose throw is the server's refusal, and records the chunks sent.
 */
function serve(handlers: Record<string, Handler>) {
  const chunks: { index: number; body: Blob }[] = [];
  const call = vi.spyOn(api, "call").mockImplementation((async (
    op: string,
    input: { path?: Record<string, unknown>; body?: unknown },
    options?: { signal?: AbortSignal },
  ) => {
    options?.signal?.throwIfAborted();
    const handler = handlers[op];
    if (op === "listProjectImports" && handler === undefined) {
      return { status: 200, body: { items: [], next_cursor: null } };
    }
    if (op === "listProjects" || op === "getWorkspaceUsage") {
      return { status: 200, body: { items: [], next_cursor: null } };
    }
    if (handler === undefined) {
      throw new Error(`unexpected ${op}`);
    }
    const body = await handler(input);
    if (op === "putProjectImportChunk") {
      chunks.push({
        index: input.path?.index as number,
        body: input.body as Blob,
      });
    }
    return { status: 200, body, response: new Response() };
  }) as never);
  const count = (op: string) =>
    call.mock.calls.filter(([name]) => name === op).length;
  return { call, chunks, count };
}

/** The workspace's page, as far as the import is concerned: its list and the button. */
function WorkspacePage() {
  useInfiniteQuery(projectsQuery("ws_a"));
  return <ImportButton workspaceId="ws_a" />;
}

/** Renders the button on a page, and opens its dialog. */
async function open() {
  const root = createRootRoute();
  renderApp("/import", {
    session: testSession,
    routes: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: "/import",
        component: WorkspacePage,
      }),
      createRoute({
        getParentRoute: () => root,
        path: "/projects/$projectId",
        component: () => null,
      }),
    ]),
  });
  await userEvent.click(
    await screen.findByRole("button", { name: "Import project" }),
  );
  return screen.findByRole("dialog", { name: "Import a project" });
}

/** Picks file in the dialog's field. */
function pick(dialog: HTMLElement, file: File) {
  fireEvent.change(within(dialog).getByLabelText("Archive (.zip)"), {
    target: { files: [file] },
  });
}

async function startImport(dialog: HTMLElement) {
  await userEvent.click(
    within(dialog).getByRole("button", { name: "Start import" }),
  );
}

/** An import through to its job, whose reads answer from steps in turn. */
function throughJob(
  size: number,
  steps: ProjectImportBody[],
  more: Record<string, Handler> = {},
) {
  let read = 0;
  return serve({
    createProjectImport: () => importOf(size),
    putProjectImportChunk: () => undefined,
    completeProjectImport: () =>
      importOf(size, [], { status: "queued", phase: "queued" }),
    getProjectImport: () => steps[Math.min(read++, steps.length - 1)],
    ...more,
  });
}

beforeEach(() => {
  // The backoff's jitter: a retry goes at once.
  vi.spyOn(Math, "random").mockReturnValue(0);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("the project import's dialog", () => {
  // Verifies: FR-DATA-01/AC4
  it("sends the file in chunks of chunk_bytes, in order, then completes it", async () => {
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    const { call, chunks } = throughJob(10, [ready]);
    const dialog = await open();
    pick(dialog, archive());
    expect(within(dialog).getByText(/harbour\.zip, 10/)).toBeTruthy();
    await startImport(dialog);
    await within(dialog).findByText("Imported Harbour.");

    expect(call).toHaveBeenCalledWith(
      "createProjectImport",
      expect.objectContaining({
        path: { workspace_id: "ws_a" },
        body: { file_name: "harbour.zip", size_bytes: 10, id_policy: "auto" },
      }),
      expect.anything(),
    );
    expect(chunks.map((c) => c.index)).toEqual([0, 1, 2]);
    expect(await Promise.all(chunks.map((c) => textOf(c.body)))).toEqual([
      "0123",
      "4567",
      "89",
    ]);
    expect(chunks[0]?.body.type).toBe("application/octet-stream");
    expect(call).toHaveBeenCalledWith(
      "completeProjectImport",
      expect.objectContaining({
        path: { workspace_id: "ws_a", import_id: "job_i" },
      }),
      expect.anything(),
    );
  });

  it("asks for fresh IDs where the box is ticked, which it is not by default", async () => {
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    const { call } = throughJob(10, [ready]);
    const dialog = await open();
    const box = within(dialog).getByRole("checkbox", {
      name: /Give everything fresh IDs/,
    }) as HTMLInputElement;
    expect(box.checked).toBe(false);
    await userEvent.click(box);
    pick(dialog, archive());
    await startImport(dialog);
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "createProjectImport",
        expect.objectContaining({
          body: expect.objectContaining({ id_policy: "remap" }),
        }),
        expect.anything(),
      ),
    );
  });

  // Verifies: FR-DATA-01/AC4
  it("tries a chunk again after a failure that passes, and carries on", async () => {
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    let failed = 0;
    const { chunks, count } = throughJob(10, [ready], {
      putProjectImportChunk: ({ path }) => {
        if (path?.index === 1 && failed < 2) {
          failed += 1;
          throw failed === 1
            ? new ApiError(new Response("", { status: 503 }), undefined)
            : refused(429, { code: "rate_limited" }, { "Retry-After": "0" });
        }
        return undefined;
      },
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText("Imported Harbour.");
    expect(count("putProjectImportChunk")).toBe(5);
    expect(chunks.map((c) => c.index)).toEqual([0, 1, 2]);
  });

  it("waits as long as Retry-After says, in seconds or as a date", () => {
    expect(retryAfter("3")).toBe(3000);
    expect(
      retryAfter("Mon, 23 Nov 2026 10:00:05 GMT", Date.UTC(2026, 10, 23, 10)),
    ).toBe(5000);
    expect(retryAfter("soon")).toBeUndefined();
    expect(retryAfter(null)).toBeUndefined();
    expect(
      retryWait(
        refused(429, { code: "rate_limited" }, { "Retry-After": "7" }),
        0,
      ),
    ).toBe(7000);
  });

  // Verifies: FR-DATA-01/AC4
  it("stops once its retries run out, and resumes with only what the server lacks", async () => {
    let down = true;
    let completed = false;
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    const { chunks, count } = serve({
      createProjectImport: () => importOf(10),
      putProjectImportChunk: ({ path }) => {
        if (down && path?.index === 1) {
          throw new ApiError(new Response("", { status: 502 }), undefined);
        }
        return undefined;
      },
      completeProjectImport: () => {
        completed = true;
        return importOf(10, [], { status: "queued", phase: "queued" });
      },
      getProjectImport: () => (completed ? ready : importOf(10, [0])),
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    const resume = await within(dialog).findByRole("button", {
      name: "Resume upload",
    });
    expect(within(dialog).getByRole("alert").textContent).toMatch(
      /could not be reached/,
    );
    expect(count("putProjectImportChunk")).toBe(1 + retries + 1);
    expect(count("completeProjectImport")).toBe(0);

    // The read that resuming starts from says that chunk 0 is stored.
    chunks.length = 0;
    down = false;
    await userEvent.click(resume);
    await within(dialog).findByText("Imported Harbour.");
    expect(chunks.map((c) => c.index)).toEqual([1, 2]);
  });

  // Verifies: FR-DATA-01/AC4
  it("resumes the upload that the server names, if it is of this file with this policy", async () => {
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    let reads = 0;
    const { chunks } = serve({
      createProjectImport: () => {
        throw refused(409, {
          code: "import_in_progress",
          details: { import_id: "job_i" },
        });
      },
      putProjectImportChunk: () => undefined,
      completeProjectImport: () =>
        importOf(10, [], { status: "queued", phase: "queued" }),
      getProjectImport: () => (reads++ === 0 ? importOf(10, [0, 2]) : ready),
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText("Imported Harbour.");
    expect(chunks.map((c) => c.index)).toEqual([1]);
  });

  it("shows the import on its way when it is of another file, and cancels it", async () => {
    const other = importOf(99, [], { file_name: "other.zip" });
    const { call, chunks } = serve({
      createProjectImport: () => {
        throw refused(409, {
          code: "import_in_progress",
          details: { import_id: "job_i" },
        });
      },
      getProjectImport: () => other,
      cancelProjectImport: () => undefined,
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText(/An upload of other\.zip/);
    expect(chunks).toEqual([]);
    const input = dialog.querySelector('input[type="file"]');
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Cancel import" }),
    );
    await waitFor(() =>
      expect(call).toHaveBeenCalledWith(
        "cancelProjectImport",
        expect.objectContaining({
          path: { workspace_id: "ws_a", import_id: "job_i" },
        }),
      ),
    );
    await within(dialog).findByText(/The import was canceled/);
    expect(
      within(dialog).getByRole("button", { name: "Start import" }),
    ).toBeTruthy();
    // The form holds no file, so neither does its field, which would keep
    // showing it and take no second choice of the same file.
    expect(dialog.querySelector('input[type="file"]')).not.toBe(input);
  });

  it("compares a file's name as the server keeps it", () => {
    const mark = String.fromCodePoint(0x200e);
    const nextLine = String.fromCodePoint(0x85);
    const bom = String.fromCodePoint(0xfeff);
    expect(storedFileName(`  story${mark}.zip${nextLine}`)).toBe("story.zip");
    expect(storedFileName(`${bom}story.zip`)).toBe(`${bom}story.zip`);
  });

  it("does not resume an upload of the same file with another ID policy unasked", async () => {
    const { chunks } = serve({
      createProjectImport: () => {
        throw refused(409, {
          code: "import_in_progress",
          details: { import_id: "job_i" },
        });
      },
      getProjectImport: () => importOf(10, [0], { id_policy: "remap" }),
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByRole("button", { name: "Resume upload" });
    expect(within(dialog).getByText(/give everything fresh IDs/)).toBeTruthy();
    expect(chunks).toEqual([]);
  });

  // Verifies: FR-DATA-01/AC4
  it("follows the job's phases to its report, links to the project, and reads the list again", async () => {
    const importing = (phase: string, done: number, total: number) =>
      importOf(10, [], {
        status: "importing",
        phase,
        progress_done: done,
        progress_total: total,
      });
    const ready = importOf(10, [], { status: "ready", phase: "done", report });
    const { count } = throughJob(10, [
      importing("verifying", 1, 2),
      importing("writing", 0, 0),
      ready,
    ]);
    const dialog = await open();
    await waitFor(() => expect(count("listProjects")).toBe(1));
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText(/Verifying the releases/, undefined, {
      timeout: 3000,
    });
    expect(within(dialog).getByText("1 of 2")).toBeTruthy();
    expect(
      within(dialog)
        .getByRole("progressbar", { name: "Import progress" })
        .getAttribute("value"),
    ).toBe("1");
    await within(dialog).findByText("Imported Harbour.", undefined, {
      timeout: 4000,
    });
    expect(
      within(dialog).getByText(/at least one of the archive/),
    ).toBeTruthy();
    expect(within(dialog).getByText(/2 people named/)).toBeTruthy();
    expect(within(dialog).getByText(/1 reference to an asset/)).toBeTruthy();
    expect(
      within(dialog)
        .getByRole("link", { name: "Open the project" })
        .getAttribute("href"),
    ).toBe("/app/projects/prj_new");
    await waitFor(() => expect(count("listProjects")).toBe(2));
  }, 10_000);

  // Verifies: FR-DATA-01/AC11
  it("says which release and check failed, that nothing was imported", async () => {
    const failed = importOf(10, [], {
      status: "failed",
      phase: "failed",
      failure: {
        code: "release_invalid",
        version: "1.2.0",
        check: "content_hash",
      },
    });
    throughJob(10, [failed]);
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    const alert = await within(dialog).findByRole("alert", undefined, {
      timeout: 3000,
    });
    await waitFor(() => expect(alert.textContent).toMatch(/Release 1\.2\.0/));
    expect(alert.textContent).toMatch(/content hash/);
    expect(alert.textContent).toMatch(/Nothing was imported/);
  });

  // Verifies: FR-DATA-01/AC12
  it("names the section that a server cannot import", async () => {
    const failed = importOf(10, [], {
      status: "failed",
      phase: "failed",
      failure: { code: "section_unsupported", section: "annotations" },
    });
    throughJob(10, [failed]);
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText(/in the section annotations/, undefined, {
      timeout: 3000,
    });
  });

  // Verifies: FR-DATA-01/AC4
  it("stops the upload as the dialog closes, and resumes it once it opens again", async () => {
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { chunks } = serve({
      createProjectImport: () => importOf(10),
      putProjectImportChunk: async ({ path }) => {
        if (path?.index === 1) {
          await held;
        }
        return undefined;
      },
      getProjectImport: () => importOf(10, [0]),
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText(/Closing this dialog stops the upload/);
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Close" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    release();
    // Nothing goes on once it has been stopped.
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(chunks.map((c) => c.index)).not.toContain(2);

    await userEvent.click(
      screen.getByRole("button", { name: "Import project" }),
    );
    const again = await screen.findByRole("dialog");
    await within(again).findByRole("button", { name: "Resume upload" });
  });

  it("says when it is too late to cancel, and follows the import on", async () => {
    let reads = 0;
    const { call } = serve({
      listProjectImports: () => ({
        items: [importOf(10, [], { status: "queued", phase: "queued" })],
        next_cursor: null,
      }),
      getProjectImport: () =>
        reads++ === 0
          ? importOf(10, [], { status: "queued", phase: "queued" })
          : importOf(10, [], { status: "importing", phase: "checking" }),
      cancelProjectImport: () => {
        throw refused(409, {
          code: "import_not_uploading",
          details: { status: "importing" },
        });
      },
    });
    const dialog = await open();
    await userEvent.click(
      await within(dialog).findByRole("button", { name: "Cancel import" }),
    );
    await within(dialog).findByText(/can no longer be canceled/);
    await waitFor(() =>
      expect(
        within(dialog).queryByRole("button", { name: "Cancel import" }),
      ).toBeNull(),
    );
    expect(call).toHaveBeenCalledWith("cancelProjectImport", expect.anything());
  });

  it("refuses an empty file before it asks the server", async () => {
    const { count } = serve({});
    const dialog = await open();
    pick(dialog, archive(""));
    expect(within(dialog).getByRole("alert").textContent).toMatch(/empty/);
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Start import",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(count("createProjectImport")).toBe(0);
  });

  it("says what the instance's limit is of an archive too large", async () => {
    serve({
      createProjectImport: () => {
        throw refused(413, {
          code: "request_too_large",
          details: { max_bytes: 2048 },
        });
      },
    });
    const dialog = await open();
    pick(dialog, archive());
    await startImport(dialog);
    await within(dialog).findByText(/import limit \(2 kB\)/);
  });
});
