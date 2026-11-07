import { type Board, NetworkError } from "@storycreep/shared/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PRODUCT_NAME } from "./product";
import { lastOpened, rememberBoard } from "./project/lastOpened";
import { renderApp, testSession } from "./test/app";
import { fakeApi, refused, startPage, testProject } from "./test/fake";

// The app's start page (PRD FR-PROJ-04, FR-PUB-05), as the router shows it,
// over a fake of the API's client: the board that the device last showed the
// account, while it is still there, or else the project list.

afterEach(() => {
  vi.restoreAllMocks();
});

const harbour: Board = {
  id: "bd_a",
  project_id: testProject.id,
  name: "Harbour",
  slug: "harbour",
  description: "",
  color: "none",
  order: "V",
  rev: 3,
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
};

/**
 * Answers the start page and the pages that it goes to, with the test
 * project's boards, and getProject with project, which may throw.
 */
function answer(boards: Board[], project: () => unknown = () => testProject) {
  return fakeApi((operation) => {
    switch (operation) {
      case "getProject":
        return project();
      case "listBoards":
        return { items: boards, next_cursor: null };
      case "listFolders":
      case "listBoardNodes":
      case "listBoardConnections":
      case "listComponentTypes":
      case "listComponents":
      case "listVariables":
        return { items: [], next_cursor: null };
    }
    const page = startPage(operation);
    if (page === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return page;
  });
}

describe("the start page", () => {
  it("opens the board that the device last showed the account", async () => {
    answer([harbour]);
    rememberBoard(testSession.user_id, testProject.id, harbour.id);
    const { router } = renderApp("/", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Harbour" });
    expect(router.state.location.pathname).toBe(
      `/projects/${testProject.id}/boards/${harbour.id}`,
    );
  });

  it("leaves the header's product name to the project list, from the board that it would open", async () => {
    answer([harbour]);
    const { router } = renderApp(
      `/projects/${testProject.id}/boards/${harbour.id}`,
      { session: testSession },
    );
    await screen.findByRole("heading", { level: 1, name: "Harbour" });
    await userEvent.click(screen.getByRole("link", { name: PRODUCT_NAME }));
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(router.state.location.pathname).toBe("/workspaces/ws_studio");
  });

  it("opens the project list on a device that showed the account no board, as another account's board is none of its", async () => {
    answer([harbour]);
    rememberBoard("usr_other", testProject.id, harbour.id);
    const { router } = renderApp("/", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(router.state.location.pathname).toBe("/workspaces/ws_studio");
  });

  // Verifies: FR-PROJ-04/AC2
  it("opens the project list, and forgets the board, once the board has gone, or its project may no longer be read, which is no error of the app's", async () => {
    for (const [what, boards, project] of [
      ["a board gone to the trash", [], () => testProject],
      [
        "a project that may no longer be read",
        [harbour],
        () => {
          throw refused(404, { code: "not_found" });
        },
      ],
    ] as const) {
      vi.restoreAllMocks();
      answer([...boards], project);
      rememberBoard(testSession.user_id, testProject.id, harbour.id);
      const { router, log, unmount } = renderApp("/", {
        session: testSession,
      });
      await screen.findByRole("heading", { level: 1, name: "Projects" });
      expect(router.state.location.pathname, what).toBe(
        "/workspaces/ws_studio",
      );
      expect(lastOpened(testSession.user_id), what).toBeUndefined();
      expect(log.entries(), what).toEqual([]);
      unmount();
    }
  });

  it("opens the project list, and keeps the board, while the project cannot be read", async () => {
    answer([harbour], () => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    rememberBoard(testSession.user_id, testProject.id, harbour.id);
    const { router } = renderApp("/", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(router.state.location.pathname).toBe("/workspaces/ws_studio");
    await waitFor(() =>
      expect(lastOpened(testSession.user_id)?.boardId).toBe(harbour.id),
    );
  });
});
