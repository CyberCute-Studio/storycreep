import type { Board } from "@storycreep/shared/api";
import { createRootRoute, createRoute } from "@tanstack/react-router";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testNode } from "../test/canvas";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import { BoardCanvas } from "./BoardCanvas";
import type { Engine, EngineOptions } from "./engine";

const engines = vi.hoisted(() => ({
  fail: false,
  created: 0,
  destroyed: 0,
  /** What the engine's start waits for, as the fetch of its chunk. */
  hold: undefined as Promise<void> | undefined,
  /** What tells each start that the page no longer wants it, in the order that they began. */
  signals: [] as (AbortSignal | undefined)[],
}));

// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of: the page's
// tests draw with an engine that draws nothing.
vi.mock("./engine", () => ({
  createEngine: async (
    _surface: HTMLElement,
    options: EngineOptions,
  ): Promise<Engine> => {
    engines.signals.push(options.signal);
    await engines.hold;
    if (engines.fail) {
      throw new Error("no renderer");
    }
    engines.created++;
    return {
      kind: "canvas",
      resize: () => {},
      render: (f) => ({
        lod: "detail",
        nodes: f.scene.nodes.size,
        visible: f.scene.nodes.size,
        texts: 0,
        connections: f.scene.connections.size,
      }),
      textures: () => undefined,
      onRestore: () => () => {},
      destroy: () => {
        engines.destroyed++;
      },
    };
  },
}));

beforeEach(() => {
  engines.fail = false;
  engines.created = 0;
  engines.destroyed = 0;
  engines.hold = undefined;
  engines.signals = [];
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

/** Answers the board's page, of a board of nodes, whose nodes' read fails with failNodes, and its components' with failComponents. */
function boardPage(
  nodes: ReturnType<typeof testNode>[] = [],
  failNodes?: () => boolean,
  failComponents?: () => boolean,
) {
  return fakeApi((operation) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listComponents":
        if (failComponents?.()) {
          throw refused(500, { code: "internal" });
        }
        return { items: [], next_cursor: null };
      case "listFolders":
      case "listBoardConnections":
      case "listComponentTypes":
      case "listVariables":
        return { items: [], next_cursor: null };
      case "listBoards":
        return { items: [harbour], next_cursor: null };
      case "listBoardNodes":
        if (failNodes?.()) {
          throw refused(500, { code: "internal" });
        }
        return { items: nodes, next_cursor: null };
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

describe("a board's canvas", () => {
  // Verifies: NFR-TOUCH-03
  it("pans by default, and switches to selecting with a visible toggle", async () => {
    const user = userEvent.setup();
    boardPage([testNode("nd_1", 0, 0)]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const controls = await screen.findByRole("group", {
      name: "What a drag does",
    });
    const pan = within(controls).getByRole("button", { name: "Pan" });
    const select = within(controls).getByRole("button", { name: "Select" });
    expect(pan.getAttribute("aria-pressed")).toBe("true");
    expect(select.getAttribute("aria-pressed")).toBe("false");
    await user.click(select);
    expect(select.getAttribute("aria-pressed")).toBe("true");
    expect(pan.getAttribute("aria-pressed")).toBe("false");
    expect(
      screen.getByRole("button", { name: "Zoom to selection" }),
    ).toHaveProperty("disabled", true);
    const canvas = screen.getByRole("application", {
      name: "Nodes of Harbour",
    });
    // NFR-TOUCH-08: the browser's own gestures, as pull-to-refresh, are off
    // over the canvas.
    expect(canvas.className).toContain("touch-none");
    expect(canvas.className).toContain("overscroll-none");
    await waitFor(() => expect(engines.created).toBe(1));
  });

  it("lets go of Space once the focus leaves the canvas, so that a drag of the select mode draws its rubber band again", async () => {
    const user = userEvent.setup();
    boardPage([testNode("nd_1", 0, 0)]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await user.click(screen.getByRole("button", { name: "Select" }));
    canvas.focus();
    // Space goes down on the canvas, and up once the focus has left it.
    await user.keyboard("[Space>]");
    await user.tab();
    expect(document.activeElement).not.toBe(canvas);
    await user.keyboard("[/Space]");
    await user.pointer([
      { keys: "[MouseLeft>]", target: canvas, coords: { x: 10, y: 10 } },
      { target: canvas, coords: { x: 80, y: 60 } },
    ]);
    const band = canvas.querySelector<HTMLElement>(".border-accent");
    expect(band?.hidden).toBe(false);
    await user.pointer({ keys: "[/MouseLeft]", target: canvas });
    expect(band?.hidden).toBe(true);
  });

  it("ends a press once its mouse moves with no button down, as after a menu of the system's took its release, gives back its pointer's capture, and opens no menu of the browser's over the canvas", async () => {
    const user = userEvent.setup();
    boardPage([testNode("nd_1", 0, 0)]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await user.click(screen.getByRole("button", { name: "Select" }));
    // jsdom captures no pointer: the canvas's captures, as a browser's.
    const captures = new Set<number>();
    Object.assign(canvas, {
      setPointerCapture: (id: number) => captures.add(id),
      hasPointerCapture: (id: number) => captures.has(id),
      releasePointerCapture: (id: number) => captures.delete(id),
    });
    const mouse = { pointerId: 1, pointerType: "mouse" };
    fireEvent.pointerDown(canvas, {
      ...mouse,
      button: 0,
      buttons: 1,
      clientX: 10,
      clientY: 10,
    });
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 80,
      clientY: 60,
    });
    const band = canvas.querySelector<HTMLElement>(".border-accent");
    expect(band?.hidden).toBe(false);
    expect(captures.has(1)).toBe(true);
    // The button went up under a menu of the system's, as a Ctrl+click's
    // on a Mac does, and the mouse moves on without it.
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 0,
      clientX: 120,
      clientY: 90,
    });
    expect(band?.hidden).toBe(true);
    expect(captures.has(1)).toBe(false);
    // The browser's menu, which would take that release, does not open.
    expect(fireEvent.contextMenu(canvas)).toBe(false);
  });

  it("says that a board has no nodes yet", async () => {
    boardPage([]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    expect(
      await screen.findByText("This board has no nodes yet."),
    ).toBeDefined();
  });

  it("says that the board's nodes could not be loaded, and reads them again as the user asks", async () => {
    const user = userEvent.setup();
    let fail = true;
    let reads = 0;
    boardPage([testNode("nd_1", 0, 0)], () => {
      reads++;
      return fail;
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const alert = await screen.findByText(
      "The board's nodes could not be loaded.",
      {
        exact: false,
      },
    );
    // What the loader's read failed with, which the canvas says rather
    // than read the nodes again as it mounts.
    expect(reads).toBe(1);
    fail = false;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(
        screen.queryByText("The board's nodes could not be loaded.", {
          exact: false,
        }),
      ).toBeNull(),
    );
    expect(reads).toBe(2);
  });

  it("reads the board's nodes anew as the user comes back to it, which another session may have changed while the project's document was not kept current", async () => {
    const nodes: ReturnType<typeof testNode>[] = [];
    boardPage(nodes);
    const { router } = renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    expect(
      await screen.findByText("This board has no nodes yet."),
    ).toBeDefined();
    // Another session adds a node, which no real-time channel tells of.
    nodes.push(testNode("nd_1", 0, 0));
    await act(() =>
      router.navigate({
        to: "/projects/$projectId",
        params: { projectId: testProject.id },
      }),
    );
    await act(() =>
      router.navigate({
        to: "/projects/$projectId/boards/$boardId",
        params: { projectId: testProject.id, boardId: "bd_a" },
      }),
    );
    await screen.findByRole("application", { name: "Nodes of Harbour" });
    await waitFor(() => {
      expect(screen.queryByText("Loading the board's nodes…")).toBeNull();
      expect(screen.queryByText("This board has no nodes yet.")).toBeNull();
    });
  });

  it("says that the names that cards show could not be loaded, and reads them again as the user asks", async () => {
    const user = userEvent.setup();
    let fail = true;
    let reads = 0;
    boardPage([testNode("nd_1", 0, 0)], undefined, () => {
      reads++;
      return fail;
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const text =
      "The names of the speakers and the variables that the cards show could not be loaded.";
    const alert = await screen.findByText(text, { exact: false });
    expect(reads).toBe(1);
    fail = false;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(screen.queryByText(text, { exact: false })).toBeNull(),
    );
    expect(reads).toBe(2);
  });

  it("says that the canvas starts while its renderer does, once the board's content has been read", async () => {
    let start = () => {};
    engines.hold = new Promise((resolve) => {
      start = resolve;
    });
    boardPage([]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    expect(await screen.findByText("Starting the canvas…")).toBeDefined();
    start();
    expect(
      await screen.findByText("This board has no nodes yet."),
    ).toBeDefined();
    expect(screen.queryByText("Starting the canvas…")).toBeNull();
  });

  it("tells the start of a renderer that the page no longer wants, as once its address asks for another, which starts in its place, so that it adds no canvas beside that one's", async () => {
    let start = () => {};
    engines.hold = new Promise((resolve) => {
      start = resolve;
    });
    boardPage([]);
    const { router } = renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    expect(await screen.findByText("Starting the canvas…")).toBeDefined();
    await waitFor(() => expect(engines.signals.length).toBeGreaterThan(0));
    const before = engines.signals.length;
    await act(() =>
      router.navigate({
        to: "/projects/$projectId/boards/$boardId",
        params: { projectId: testProject.id, boardId: "bd_a" },
        search: { renderer: "canvas" },
      }),
    );
    await waitFor(() => expect(engines.signals.length).toBe(before + 1));
    expect(engines.signals.slice(0, -1).every((s) => s?.aborted)).toBe(true);
    expect(engines.signals.at(-1)?.aborted).toBe(false);
    start();
    expect(
      await screen.findByText("This board has no nodes yet."),
    ).toBeDefined();
    // The starts that were no longer wanted end their renderers.
    await waitFor(() => expect(engines.destroyed).toBe(engines.created - 1));
  });

  it("says that the browser cannot draw the board", async () => {
    engines.fail = true;
    boardPage([testNode("nd_1", 0, 0)]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    expect(
      await screen.findByText(
        "This browser cannot draw the board: it has neither WebGL nor a 2D canvas.",
      ),
    ).toBeDefined();
  });

  it("shows another board's canvas anew where a page gives it another board in place, rather than read that board into the scene of the one before", async () => {
    const user = userEvent.setup();
    const lighthouse: Board = {
      ...harbour,
      id: "bd_b",
      name: "Lighthouse",
      slug: "lighthouse",
    };
    // Harbour has no nodes, and Lighthouse one.
    fakeApi((operation, input) => {
      if (operation === "listBoardNodes") {
        const board = (input as { path: { board_id: string } }).path.board_id;
        return {
          items:
            board === "bd_b"
              ? [testNode("nd_b", 0, 0, { board_id: "bd_b" })]
              : [],
          next_cursor: null,
        };
      }
      if (operation === "getProject") {
        return testProject;
      }
      if (operation === "listBoards") {
        return { items: [harbour, lighthouse], next_cursor: null };
      }
      return startPage(operation) ?? { items: [], next_cursor: null };
    });
    // A page that gives the canvas another board in place, without the
    // remount of the board's page.
    function Boards() {
      const [board, setBoard] = useState(harbour);
      return (
        <>
          <button type="button" onClick={() => setBoard(lighthouse)}>
            Show Lighthouse
          </button>
          <BoardCanvas
            projectId={testProject.id}
            boardId={board.id}
            boardName={board.name}
          />
        </>
      );
    }
    // A root of its own, rather than the app's, whose children it would
    // replace for the tests after this one.
    const root = createRootRoute();
    renderApp("/boards", {
      session: testSession,
      routes: root.addChildren([
        createRoute({
          getParentRoute: () => root,
          path: "/boards",
          component: Boards,
        }),
      ]),
    });
    expect(
      await screen.findByText("This board has no nodes yet."),
    ).toBeDefined();
    await user.click(screen.getByRole("button", { name: "Show Lighthouse" }));
    expect(
      await screen.findByRole("application", { name: "Nodes of Lighthouse" }),
    ).toBeDefined();
    await waitFor(() => expect(engines.created).toBe(2));
    // Once Lighthouse's node has been read, and its renderer started, the
    // canvas says nothing over it: the scene holds Lighthouse's node.
    await waitFor(() => {
      expect(screen.queryByText("Loading the board's nodes…")).toBeNull();
      expect(screen.queryByText("Starting the canvas…")).toBeNull();
    });
    expect(screen.queryByText("This board has no nodes yet.")).toBeNull();
  });

  it("keeps the board's settings under the canvas, which takes the window's width rather than the project's column", async () => {
    boardPage([]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const settings = await screen.findByRole("region", {
      name: "Board settings",
    });
    expect(within(settings).getByLabelText("Name")).toHaveProperty(
      "value",
      "Harbour",
    );
    // The board's page says that it is wide (staticData), which the
    // project's layout reads.
    const nav = screen.getByRole("navigation", {
      name: `Project ${testProject.name}`,
    });
    const layout = nav.closest(".mx-auto");
    expect(layout).not.toBeNull();
    expect(layout?.className).not.toContain("max-w-3xl");
  });

  it("leads from the toolbar to the board's settings, and gives their heading the focus", async () => {
    const user = userEvent.setup();
    boardPage([testNode("nd_1", 0, 0)]);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const link = await screen.findByRole("link", { name: "Board settings" });
    // Of a finger's size, as the toolbar's buttons are (NFR-TOUCH-01).
    expect(link.className).toContain("min-h-11");
    const heading = screen.getByRole("heading", { name: "Board settings" });
    const scrolled = vi.fn();
    heading.scrollIntoView = scrolled;
    await user.click(link);
    expect(document.activeElement).toBe(heading);
    expect(scrolled).toHaveBeenCalledWith({ block: "start" });
    // By the keys too, without changing the page's address.
    link.focus();
    await user.keyboard("{Enter}");
    expect(document.activeElement).toBe(heading);
    expect(scrolled).toHaveBeenCalledTimes(2);
    expect(window.location.hash).toBe("");
    // A click that asks for a new tab or window is the browser's, which
    // opens the board's page there, at its settings: the test's listener,
    // after the page's, reads whether the page took it, and takes it from
    // jsdom, which would follow the link.
    const taken: boolean[] = [];
    const browser = (e: Event) => {
      taken.push(e.defaultPrevented);
      e.preventDefault();
    };
    document.addEventListener("click", browser);
    try {
      for (const key of ["ctrlKey", "metaKey", "shiftKey"]) {
        link.focus();
        fireEvent.click(link, { [key]: true });
        expect(document.activeElement).toBe(link);
      }
      fireEvent.click(link);
    } finally {
      document.removeEventListener("click", browser);
    }
    expect(taken).toEqual([false, false, false, true]);
    expect(scrolled).toHaveBeenCalledTimes(3);
  });
});
