import "fake-indexeddb/auto";
import {
  type Board,
  type CommandBody,
  NetworkError,
} from "@storycreep/shared/api";
import { onlineManager } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IDBFactory } from "fake-indexeddb";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  onTestFinished,
  vi,
} from "vitest";
import { renderApp, testSession } from "../test/app";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import type { OfflineDatabase, QueueRecord } from "./db";
import { syncStateOf } from "./pending";

// The sync indicator of a project's pages and its panel (PRD FR-OFF-06,
// FR-OFF-03, plan note N98): the four states, as the project's document
// holds the user's changes, each shown within a second of the change (AC1);
// the panel's list of the changes that the server has yet to answer, which
// it exports as JSON (AC2); and, offline, its list of what needs the
// server, with why (FR-OFF-03 AC3).

beforeEach(() => {
  globalThis.indexedDB = new IDBFactory();
});

afterEach(() => {
  vi.restoreAllMocks();
  onlineManager.setOnline(true);
});

const quay: Board = {
  id: "bd_quay",
  project_id: testProject.id,
  name: "Quay",
  slug: "quay",
  description: "",
  color: "none",
  order: "V",
  rev: 1,
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
} as Board;

const page = <T,>(items: T[]) => ({ items, next_cursor: null });

/**
 * Serves the test project, of one board, while the browser has a
 * connection, and answers each batch once release is called, or with what
 * apply answers, which may throw, if given; returns the batches sent.
 */
function serve(apply?: (commands: CommandBody[]) => unknown) {
  const batches: CommandBody[][] = [];
  let release = () => {};
  let held = new Promise<void>((resolve) => {
    release = resolve;
  });
  fakeApi(async (operation, input) => {
    if (!navigator.onLine) {
      throw new NetworkError(new TypeError("Failed to fetch"));
    }
    switch (operation) {
      case "getProject":
        return testProject;
      case "listBoards":
        return page([quay]);
      case "listFolders":
      case "listVariables":
      case "listComponentTypes":
      case "listComponents":
        return page([]);
      case "logout":
        return undefined;
      case "applyCommands": {
        const commands = (input as { body: { commands: CommandBody[] } }).body
          .commands;
        batches.push(commands);
        if (apply !== undefined) {
          return apply(commands);
        }
        await held;
        return {
          status: "applied",
          seq: 2,
          revisions: [{ id: "bd_quay", rev: 2 }],
          not_reconnected: [],
          undo: [],
        };
      }
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
  return {
    batches,
    release: () => {
      release();
      held = Promise.resolve();
    },
  };
}

function setOnline(online: boolean) {
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(online);
  act(() => {
    window.dispatchEvent(new Event(online ? "online" : "offline"));
  });
}

/** The rename of the board Quay to name, expecting its first revision. */
function renameQuay(name: string) {
  return {
    type: "board.rename" as const,
    payload: { id: "bd_quay", name },
    expected_rev: { bd_quay: 1 },
  };
}

/** Opens the tree of the test project's boards, with the device keeping what it can, and returns its document. */
async function openProject() {
  const app = renderApp(`/projects/${testProject.id}`, {
    session: testSession,
    offline: { online: () => navigator.onLine },
  });
  await screen.findByRole("link", { name: "Quay" });
  return { ...app, doc: app.documents.get(testProject.id) };
}

/** Waits at most a second for the sync indicator to say text (AC1). */
function indicator(text: string) {
  return screen.findByRole("button", { name: text }, { timeout: 1_000 });
}

describe("the sync indicator", () => {
  it("is in one of four states, as the document holds the user's changes", () => {
    const base = { sending: 0, queued: 0, review: 0, holding: false };
    expect(syncStateOf(base, true)).toEqual({ state: "synced" });
    expect(syncStateOf({ ...base, sending: 1 }, true)).toEqual({
      state: "syncing",
      pending: 1,
    });
    expect(syncStateOf({ ...base, sending: 2, queued: 2 }, false)).toEqual({
      state: "offline",
      pending: 2,
    });
    // A comment's batch on its way, which the device does not keep.
    expect(syncStateOf({ ...base, sending: 2, queued: 1 }, false)).toEqual({
      state: "offline",
      pending: 1,
    });
    expect(
      syncStateOf({ ...base, sending: 2, queued: 2, holding: true }, true),
    ).toEqual({ state: "offline", pending: 2 });
    expect(
      syncStateOf({ ...base, sending: 2, queued: 2, review: 1 }, true),
    ).toEqual({ state: "review", pending: 2, review: 1 });
  });

  // Verifies: FR-OFF-06/AC1
  it("shows each change of the queue within a second: offline with the changes kept, syncing, and synced", async () => {
    const server = serve();
    const { doc } = await openProject();
    await indicator("All changes saved");
    setOnline(false);
    await indicator("Offline");
    await act(() => doc.send([renameQuay("Pier")]));
    await indicator("Offline, 1 change on this device");
    await act(() => doc.send([renameQuay("Dock")]));
    await indicator("Offline, 2 changes on this device");
    setOnline(true);
    await indicator("Saving 2 changes…");
    server.release();
    await indicator("All changes saved");
    expect(server.batches).toHaveLength(2);
  });
});

describe("the sync panel", () => {
  // Verifies: FR-OFF-06/AC2
  it("lists the changes that the server has yet to answer, with when each was made, and exports them as JSON", async () => {
    serve();
    const user = userEvent.setup();
    const { doc } = await openProject();
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    await user.click(await indicator("Offline, 1 change on this device"));
    const panel = await screen.findByRole("dialog", { name: "Sync status" });
    const waiting = within(panel).getByRole("region", {
      name: "Waiting to be sent",
    });
    expect(
      within(waiting).getByText(
        /^Made .*\. On this device, waiting for the server\.$/,
      ),
    ).toBeDefined();
    expect(within(waiting).getByText("board.rename")).toBeDefined();
    let saved: Blob | undefined;
    const { createObjectURL, revokeObjectURL } = URL;
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    URL.createObjectURL = vi.fn((blob: Blob) => {
      saved = blob;
      return "blob:pending";
    });
    URL.revokeObjectURL = vi.fn();
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    await user.click(
      within(panel).getByRole("button", { name: "Export as JSON" }),
    );
    expect(click).toHaveBeenCalledOnce();
    const exported = JSON.parse(await (saved as Blob).text());
    expect(exported).toMatchObject({
      format: "storycreep-pending-changes",
      version: 1,
      user_id: testSession.user_id,
      batches: [
        {
          project_id: testProject.id,
          state: "queued",
          sent: false,
          commands: [{ ...renameQuay("Pier"), command_id: expect.any(String) }],
        },
      ],
    });
  });

  it("lists a change of the device's as being sent once it is on its way", async () => {
    const server = serve();
    const user = userEvent.setup();
    const { doc } = await openProject();
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    await user.click(await indicator("Offline, 1 change on this device"));
    const panel = await screen.findByRole("dialog", { name: "Sync status" });
    await within(panel).findByText(/On this device, waiting for the server\.$/);
    setOnline(true);
    await within(panel).findByText(/Being sent\.$/, {}, { timeout: 1_000 });
    server.release();
  });

  // Verifies: FR-OFF-03/AC3
  it("lists what needs the server, with why, while the page cannot reach it", async () => {
    serve();
    const user = userEvent.setup();
    await openProject();
    setOnline(false);
    await user.click(await indicator("Offline"));
    const panel = await screen.findByRole("dialog", { name: "Sync status" });
    const list = within(panel).getByRole("region", {
      name: "Not available offline",
    });
    expect(
      within(list).getByText(
        /^These need the server, which this device cannot reach now/,
      ),
    ).toBeDefined();
    for (const name of [
      "Publishing a release",
      "Inviting members and changing who has access",
      "Exports",
      "Uploading images",
      "Copying, cutting, pasting, and duplicating nodes",
    ]) {
      expect(within(list).getByText(name)).toBeDefined();
    }
    setOnline(true);
    await waitFor(() =>
      expect(
        within(panel).queryByRole("region", { name: "Not available offline" }),
      ).toBeNull(),
    );
  });

  it("lists nothing as needing the server while the page reaches it, though a change needs review", async () => {
    serve(() => {
      throw refused(422, { code: "validation_failed" });
    });
    const user = userEvent.setup();
    const { doc } = await openProject();
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    setOnline(true);
    await user.click(await indicator("1 change needs review"));
    const panel = await screen.findByRole("dialog", { name: "Sync status" });
    expect(
      within(panel).getByRole("region", { name: "Needs your review" }),
    ).toBeDefined();
    expect(
      within(panel).queryByRole("region", { name: "Not available offline" }),
    ).toBeNull();
  });
});

describe("the history's controls", () => {
  it("say that undoing a change that the server has waits for the user's decision, while it refused a change of the device's", async () => {
    let refuse = false;
    serve(() => {
      if (refuse) {
        throw refused(422, { code: "validation_failed" });
      }
      return {
        status: "applied",
        seq: 2,
        revisions: [{ id: "bd_quay", rev: 2 }],
        not_reconnected: [],
        undo: [
          {
            type: "board.rename",
            payload: { id: "bd_quay", name: "Quay" },
            expected_rev: { bd_quay: 2 },
          },
        ],
      };
    });
    const { doc } = await openProject();
    await act(() => doc.send([renameQuay("Pier")]));
    setOnline(false);
    await act(() =>
      doc.send([{ ...renameQuay("Dock"), expected_rev: { bd_quay: 2 } }]),
    );
    refuse = true;
    setOnline(true);
    await indicator("1 change needs review");
    expect(
      await screen.findByText(
        "Undoing or redoing a change that the server already has waits until you decide what becomes of the changes that the server refused.",
      ),
    ).toBeDefined();
  });
});

describe("signing out", () => {
  it("asks first where the device keeps changes that the server has yet to answer, which it exports, and takes them off the device once the user signs out anyway", async () => {
    serve();
    const user = userEvent.setup();
    const { doc, offlineDb } = await openProject();
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    await indicator("Offline, 1 change on this device");
    setOnline(true);
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    const asked = await screen.findByRole("alertdialog", {
      name: "Sign out and lose a change not yet sent?",
    });
    await user.click(
      within(asked).getByRole("button", { name: "Stay signed in" }),
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(await offlineDb?.queued(testSession.user_id)).toHaveLength(1);
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    await user.click(
      within(
        await screen.findByRole("alertdialog", {
          name: "Sign out and lose a change not yet sent?",
        }),
      ).getByRole("button", { name: "Sign out anyway" }),
    );
    await waitFor(async () =>
      expect(await offlineDb?.queued(testSession.user_id)).toEqual([]),
    );
  });

  it("asks anew, as the user signs out anyway, where the device keeps a change since that the dialog did not name", async () => {
    serve();
    const user = userEvent.setup();
    const { doc, offlineDb } = await openProject();
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    await indicator("Offline, 1 change on this device");
    setOnline(true);
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    const asked = await screen.findByRole("alertdialog", {
      name: "Sign out and lose a change not yet sent?",
    });
    // Another tab's document keeps another change meanwhile.
    const [kept] = (await offlineDb?.queued(testSession.user_id)) ?? [];
    await offlineDb?.saveQueued(
      testSession.user_id,
      testProject.id,
      [{ ...(kept as QueueRecord), id: "another", owner: "another-tab" }],
      [],
    );
    await user.click(
      within(asked).getByRole("button", { name: "Sign out anyway" }),
    );
    const again = await screen.findByRole("alertdialog", {
      name: "Sign out and lose 2 changes not yet sent?",
    });
    expect(await offlineDb?.queued(testSession.user_id)).toHaveLength(2);
    await user.click(
      within(again).getByRole("button", { name: "Sign out anyway" }),
    );
    await waitFor(async () =>
      expect(await offlineDb?.queued(testSession.user_id)).toEqual([]),
    );
  });

  it("stays asking while it reads the device again as the user signs out anyway, which then signs out", async () => {
    serve();
    const user = userEvent.setup();
    const { doc, offlineDb } = await openProject();
    const db = offlineDb as OfflineDatabase;
    setOnline(false);
    await act(() => doc.send([renameQuay("Pier")]));
    await indicator("Offline, 1 change on this device");
    setOnline(true);
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    const asked = await screen.findByRole("alertdialog", {
      name: "Sign out and lose a change not yet sent?",
    });
    // The device is slow to read as the user signs out anyway.
    const queued = db.queued.bind(db);
    let read = () => {};
    vi.spyOn(db, "queued").mockImplementationOnce(async (...args) => {
      await new Promise<void>((resolve) => {
        read = resolve;
      });
      return queued(...args);
    });
    await user.click(
      within(asked).getByRole("button", { name: "Sign out anyway" }),
    );
    await user.keyboard("{Escape}");
    expect(screen.getByRole("alertdialog")).toBeDefined();
    read();
    await waitFor(async () =>
      expect(await queued(testSession.user_id)).toEqual([]),
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
});
