import "fake-indexeddb/auto";
import { type Board, NetworkError } from "@storycreep/shared/api";
import { onlineManager } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IDBFactory } from "fake-indexeddb";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { projectQuery, readUnanswered } from "../project/queries";
import { renderApp, testSession } from "../test/app";
import { fakeApi, startPage, testProject, testWorkspace } from "../test/fake";

// What the pages show of the projects that the device keeps offline (PRD
// FR-OFF-02, plan note N97): the project's switch, and what it says of the
// copy; the project's pages while the browser has no connection, the
// boards from the copy and the others that need one; the storage panel,
// which lists and removes the copies; the header's notice of a project that
// the device's storage could not keep; and the switch over plain HTTP.

beforeEach(() => {
  globalThis.indexedDB = new IDBFactory();
});

afterEach(() => {
  vi.restoreAllMocks();
  // TanStack Query, which heard the "offline" event of goOffline, takes the
  // next test's page for online again, as the browser does.
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

/** Serves the test project, of one board without nodes, and what a signed-in page reads besides, while the browser has a connection. */
function serve() {
  return fakeApi((operation) => {
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
      case "listBoardNodes":
      case "listBoardConnections":
        return page([]);
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

/** Makes the browser say that it has no connection, as the page reads it (useOnline). */
function goOffline() {
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
  act(() => {
    window.dispatchEvent(new Event("offline"));
  });
}

/** Makes the browser say that it has a connection again. */
function goOnline() {
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
  act(() => {
    window.dispatchEvent(new Event("online"));
  });
}

describe("a project's switch of keeping it offline", () => {
  it("pins the project that the page opens as the last opened, and makes it available offline once switched on", async () => {
    serve();
    const user = userEvent.setup();
    const { offline } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: {},
    });
    const box = await screen.findByRole("checkbox", {
      name: "Available offline",
    });
    expect((box as HTMLInputElement).checked).toBe(false);
    // The page's project is the last that the account opened.
    await screen.findByText(
      /^On this device as the project opened last, as of /,
    );
    await user.click(box);
    expect((box as HTMLInputElement).checked).toBe(true);
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)).toMatchObject({
        chosen: true,
        auto: true,
        status: "ready",
      }),
    );
    await screen.findByText(/^On this device, as of [^.]*\.$/);
  });

  it("shows the last change asked for, switched on and off again before the first was recorded", async () => {
    serve();
    const user = userEvent.setup();
    const { offline } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: {},
    });
    const box = await screen.findByRole("checkbox", {
      name: "Available offline",
    });
    if (offline === undefined) {
      throw new Error("The page has no keeper.");
    }
    // Each change waits to be recorded until the test lets it.
    const held = (
      change: (projectId: string, name: string) => Promise<void>,
    ) => {
      let release = () => {};
      const released = new Promise<void>((resolve) => {
        release = resolve;
      });
      return {
        change: async (projectId: string, name: string) => {
          await released;
          return change(projectId, name);
        },
        release: () => release(),
      };
    };
    const pin = held(offline.pin.bind(offline));
    const unchooseFirst = offline.unchoose.bind(offline);
    const unchoose = held((projectId) => unchooseFirst(projectId));
    vi.spyOn(offline, "pin").mockImplementation(pin.change);
    vi.spyOn(offline, "unchoose").mockImplementation((projectId) =>
      unchoose.change(projectId, ""),
    );
    await user.click(box);
    await user.click(box);
    expect((box as HTMLInputElement).checked).toBe(false);
    pin.release();
    await waitFor(() =>
      expect(offline.store.getState().pins.get(testProject.id)?.chosen).toBe(
        true,
      ),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    expect((box as HTMLInputElement).checked).toBe(false);
    unchoose.release();
    await waitFor(() =>
      expect(offline.store.getState().pins.get(testProject.id)?.chosen).toBe(
        false,
      ),
    );
    expect((box as HTMLInputElement).checked).toBe(false);
  });

  it("moves the pin of the project opened last as of the read that opening its pages made, which a network without the internet leaves unanswered", async () => {
    const lighthouse = {
      ...testProject,
      id: "prj_lighthouse",
      name: "Lighthouse",
      slug: "lighthouse",
    };
    let answers = true;
    fakeApi(async (operation, input) => {
      const projectId =
        (input as { path?: { project_id?: string } } | undefined)?.path
          ?.project_id ?? testProject.id;
      switch (operation) {
        case "getProject":
          if (!answers) {
            // A request that waits a while before it fails, as for a
            // network that does not answer.
            await new Promise((resolve) => setTimeout(resolve, 50));
            throw new NetworkError(new TypeError("Failed to fetch"));
          }
          return projectId === lighthouse.id ? lighthouse : testProject;
        case "listBoards":
          return page([{ ...quay, project_id: projectId }]);
        case "listFolders":
        case "listVariables":
        case "listComponentTypes":
        case "listComponents":
        case "listBoardNodes":
        case "listBoardConnections":
          return page([]);
      }
      const answer = startPage(operation);
      if (answer === undefined) {
        throw new Error(`unexpected ${operation}`);
      }
      return answer;
    });
    const { offline, router, queryClient } = renderApp(
      `/projects/${lighthouse.id}`,
      { session: testSession, offline: {} },
    );
    queryClient.setDefaultOptions({ queries: { retry: false } });
    const pins = () => offline?.store.getState().pins;
    await waitFor(() => expect(pins()?.get(lighthouse.id)?.auto).toBe(true));
    await act(() =>
      router.navigate({
        to: "/projects/$projectId",
        params: { projectId: testProject.id },
      }),
    );
    await waitFor(() => expect(pins()?.get(testProject.id)?.auto).toBe(true));
    expect(pins()?.has(lighthouse.id)).toBe(false);
    // The page's read of the lighthouse, which the server answered, is
    // stale by now, and its pages read it again as they open, which, on a
    // network without the internet, gets no answer.
    await act(() => new Promise((resolve) => setTimeout(resolve, 1_100)));
    answers = false;
    await act(() =>
      router.navigate({
        to: "/projects/$projectId",
        params: { projectId: lighthouse.id },
      }),
    );
    await waitFor(() =>
      expect(readUnanswered(queryClient, lighthouse.id)).toBe(true),
    );
    await waitFor(() =>
      expect(
        queryClient.getQueryState(projectQuery(lighthouse.id).queryKey)
          ?.fetchStatus,
      ).toBe("idle"),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    await offline?.idle();
    expect(pins()?.get(testProject.id)?.auto).toBe(true);
    expect(pins()?.has(lighthouse.id)).toBe(false);
  });

  it("is unavailable over plain HTTP, and says why", async () => {
    serve();
    renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: { secure: false },
    });
    const box = await screen.findByRole("checkbox", {
      name: "Available offline",
    });
    expect((box as HTMLInputElement).disabled).toBe(true);
    expect(
      screen.getByText(
        "Keeping projects offline needs HTTPS, which this page is not served over.",
      ),
    ).toBeDefined();
  });
});

describe("a project's pages while the browser has no connection", () => {
  // Verifies: FR-OFF-02/AC1
  it("show the boards from the device's copy, and say that a page of the server's needs a connection", async () => {
    serve();
    const user = userEvent.setup();
    const { offline } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: { online: () => navigator.onLine },
    });
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)?.status).toBe(
        "ready",
      ),
    );
    goOffline();
    expect(
      await screen.findByText(
        /You are offline\. The project is shown as this device kept it, as of/,
      ),
    ).toBeDefined();
    expect(screen.getByRole("link", { name: "Quay" })).toBeDefined();
    await user.click(screen.getByRole("link", { name: "Statistics" }));
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "This page needs a connection",
      }),
    ).toBeDefined();
    await user.click(screen.getByRole("link", { name: "Boards" }));
    expect(await screen.findByRole("link", { name: "Quay" })).toBeDefined();
  });

  it("say that a board that the device's copy does not have needs a connection, rather than that there is none", async () => {
    serve();
    const { offline, router } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: { online: () => navigator.onLine },
    });
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)?.status).toBe(
        "ready",
      ),
    );
    goOffline();
    // As one that another session made since the copy was written.
    await act(() =>
      router.navigate({
        to: "/projects/$projectId/boards/$boardId",
        params: { projectId: testProject.id, boardId: "bd_later" },
      }),
    );
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "This page needs a connection",
      }),
    ).toBeDefined();
  });
});

describe("the app's other pages while the browser has no connection", () => {
  it("say that they need a connection, as the project list, and read once the browser is online again", async () => {
    serve();
    const { offline, router, log } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: { online: () => navigator.onLine },
    });
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)?.status).toBe(
        "ready",
      ),
    );
    goOffline();
    // As the link of the project's pages back to its workspace's projects.
    await act(() =>
      router.navigate({
        to: "/workspaces/$workspaceId",
        params: { workspaceId: testWorkspace.id },
      }),
    );
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "This page needs a connection",
      }),
    ).toBeDefined();
    // The storage panel shows what the device keeps.
    await act(() => router.navigate({ to: "/account/offline" }));
    expect(await screen.findByRole("link", { name: "Harbour" })).toBeDefined();
    await act(() =>
      router.navigate({
        to: "/workspaces/$workspaceId",
        params: { workspaceId: testWorkspace.id },
      }),
    );
    await screen.findByRole("heading", {
      level: 1,
      name: "This page needs a connection",
    });
    goOnline();
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", {
          level: 1,
          name: "This page needs a connection",
        }),
      ).toBeNull(),
    );
    expect(await screen.findByText(testWorkspace.name)).toBeDefined();
    expect(log.entries()).toEqual([]);
  });

  it("say so of a project that the device does not keep, rather than show the error page", async () => {
    serve();
    const { offline, router, log } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: { online: () => navigator.onLine },
    });
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)?.status).toBe(
        "ready",
      ),
    );
    goOffline();
    await act(() =>
      router.navigate({
        to: "/projects/$projectId",
        params: { projectId: "prj_other" },
      }),
    );
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "This page needs a connection",
      }),
    ).toBeDefined();
    expect(log.entries()).toEqual([]);
  });
});

describe("the storage panel", () => {
  // Verifies: FR-OFF-02/AC3
  it("lists the projects that the device keeps, with why and how much, and removes one", async () => {
    serve();
    const user = userEvent.setup();
    const { offline, offlineDb, router } = renderApp(
      `/projects/${testProject.id}`,
      {
        session: testSession,
        offline: {},
      },
    );
    await waitFor(() =>
      expect(offline?.store.getState().pins.get(testProject.id)?.status).toBe(
        "ready",
      ),
    );
    await act(() => router.navigate({ to: "/account/offline" }));
    const item = (await screen.findByRole("link", { name: "Harbour" })).closest(
      "li",
    ) as HTMLElement;
    expect(
      within(item).getByText(
        /Kept as the project that you opened last\. About .*\./,
      ),
    ).toBeDefined();
    expect(within(item).getByText(/^On this device/)).toBeDefined();
    await user.click(
      within(item).getByRole("button", {
        name: "Remove Harbour from this device",
      }),
    );
    expect(
      await screen.findByText("This device keeps no project offline for you."),
    ).toBeDefined();
    expect(await offlineDb?.copies(testSession.user_id)).toEqual([]);
  });
});

describe("the header", () => {
  // Verifies: FR-OFF-02/AC2
  it("names a project that the device's storage could not keep, and leads to the storage panel", async () => {
    serve();
    const put = IDBObjectStore.prototype.put;
    vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (
      this: IDBObjectStore,
      ...args: Parameters<IDBObjectStore["put"]>
    ) {
      if (this.name === "entities") {
        throw new DOMException(
          "The quota has been exceeded.",
          "QuotaExceededError",
        );
      }
      return put.apply(this, args);
    });
    renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      offline: {},
    });
    const header = await screen.findByRole("banner");
    const alert = (
      await within(header).findByText(/^This device's storage is full/)
    ).closest('[role="alert"]') as HTMLElement;
    expect(alert.textContent).toBe(
      "This device's storage is full, so “Harbour” could not be kept offline. Make room in Offline storage",
    );
    expect(
      within(alert).getByRole("link", { name: "Make room in Offline storage" }),
    ).toHaveProperty("pathname", "/app/account/offline");
  });
});
