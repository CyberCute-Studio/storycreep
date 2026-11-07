import type { Board } from "@storycreep/shared/api";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testNode } from "../test/canvas";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import type { Engine } from "./engine";

const engines = vi.hoisted(() => ({
  fail: false,
  created: 0,
  destroyed: 0,
}));

// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of: the page's
// tests draw with an engine that draws nothing.
vi.mock("./engine", () => ({
  createEngine: async (): Promise<Engine> => {
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

/** Answers the board's page, of a board of nodes, whose nodes' read fails with failNodes. */
function boardPage(
  nodes: ReturnType<typeof testNode>[] = [],
  failNodes?: () => boolean,
) {
  return fakeApi((operation) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
      case "listBoardConnections":
      case "listComponentTypes":
      case "listComponents":
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
    boardPage([testNode("nd_1", 0, 0)], () => fail);
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const alert = await screen.findByText(
      "The board's nodes could not be loaded.",
      {
        exact: false,
      },
    );
    fail = false;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(
        screen.queryByText("The board's nodes could not be loaded.", {
          exact: false,
        }),
      ).toBeNull(),
    );
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
