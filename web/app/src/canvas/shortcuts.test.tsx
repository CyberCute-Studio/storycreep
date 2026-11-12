import type { Board, Node } from "@storycreep/shared/api";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, startPage, testProject } from "../test/fake";
import type { Engine } from "./engine";

// The canvas's shortcuts (PRD FR-EDIT-10, plan note N72): its letters, which
// do what the toolbar does, the undo's keys, the palette's runs of the
// canvas's commands, and the tree's letters, which stay its type-ahead.

vi.mock("./engine", () => ({
  createEngine: async (): Promise<Engine> => ({
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
    destroy: () => {},
  }),
}));

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return this.getAttribute("role") === "application"
        ? new DOMRect(0, 0, 800, 600)
        : new DOMRect();
    },
  );
});

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

type Sent = { type: string; payload: unknown; expected_rev?: unknown }[];

/**
 * Answers the board's page, of a board of nodes, and applies each batch
 * that the page sends, whose undo is the batch itself, as the history
 * keeps it.
 */
function boardPage(nodes: Node[]) {
  let seq = testProject.seq;
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, seq };
      case "listFolders":
      case "listBoardConnections":
      case "listComponentTypes":
      case "listComponents":
      case "listVariables":
        return { items: [], next_cursor: null };
      case "listBoards":
        return { items: [harbour], next_cursor: null };
      case "listBoardNodes":
        return { items: nodes, next_cursor: null };
      case "listTags":
        return { items: [] };
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "applyCommands": {
        const { commands } = (input as { body: { commands: Sent } }).body;
        seq++;
        return {
          status: "applied",
          seq,
          revisions: [],
          not_reconnected: [],
          undo: commands.map(({ type, payload }) => ({
            type,
            payload,
            expected_rev: {},
          })),
        };
      }
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

function batches(calls: ReturnType<typeof fakeApi>): Sent[] {
  return calls.mock.calls
    .filter(([operation]) => operation === "applyCommands")
    .map(([, input]) =>
      (
        input as {
          body: { commands: (Sent[number] & { command_id: string })[] };
        }
      ).body.commands.map(({ command_id: _, ...command }) => command),
    );
}

const mouse = { pointerId: 1, pointerType: "mouse", button: 0 };

async function openBoard(search = "") {
  renderApp(`/projects/${testProject.id}/boards/bd_a${search}`, {
    session: testSession,
  });
  const canvas = await screen.findByRole("application", {
    name: "Nodes of Harbour",
  });
  await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
  return canvas;
}

describe("the canvas's shortcuts", () => {
  it("add an element, a branch, and a jumper by E, B, and J, as the toolbar's Add does", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard();
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "e", code: "KeyE" });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    fireEvent.keyDown(canvas, { key: "b", code: "KeyB" });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    fireEvent.keyDown(canvas, { key: "j", code: "KeyJ" });
    await waitFor(() => expect(batches(calls)).toHaveLength(3));
    expect(
      batches(calls).map(([c]) => [
        c.type,
        (c.payload as { type: string }).type,
      ]),
    ).toEqual([
      ["node.create", "element"],
      ["node.create", "branch"],
      ["node.create", "jumper"],
    ]);
    // Shift and E is no shortcut.
    fireEvent.keyDown(canvas, { key: "E", code: "KeyE", shiftKey: true });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches(calls)).toHaveLength(3);
  });

  it("run once for a key held down, but for those that pan and zoom on", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard();
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "e", code: "KeyE" });
    // The repeats of E held down add nothing more, and are the canvas's.
    expect(
      fireEvent.keyDown(canvas, { key: "e", code: "KeyE", repeat: true }),
    ).toBe(false);
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches(calls)).toHaveLength(1);
    // The repeats of a key whose command cannot run, as G with no jumper
    // selected, are the page's, as the key itself is.
    expect(
      fireEvent.keyDown(canvas, { key: "g", code: "KeyG", repeat: true }),
    ).toBe(true);
    // The repeats of an arrow pan on.
    const x = canvas.dataset.x;
    fireEvent.keyDown(canvas, {
      key: "ArrowLeft",
      code: "ArrowLeft",
      repeat: true,
    });
    await waitFor(() => expect(canvas.dataset.x).not.toBe(x));
  });

  it("cancel a rubber band drawn with nothing selected by Escape, which selects nothing then", async () => {
    boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard();
    await userEvent.click(screen.getByRole("button", { name: "Select" }));
    canvas.focus();
    fireEvent.pointerDown(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 2,
      clientY: 2,
    });
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 798,
      clientY: 598,
    });
    expect(fireEvent.keyDown(canvas, { key: "Escape", code: "Escape" })).toBe(
      false,
    );
    fireEvent.pointerUp(canvas, {
      ...mouse,
      buttons: 0,
      clientX: 798,
      clientY: 598,
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(canvas.dataset.selected).toBe("0");
  });

  it("change nothing of the board in the middle of a drag, and do once it has ended", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard("?node=nd_1");
    await screen.findByText(/Selected: /);
    canvas.focus();
    // A change, which the undo's key would undo.
    fireEvent.keyDown(canvas, { key: "e", code: "KeyE" });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const undo = await screen.findByRole("button", { name: "Undo" });
    await waitFor(() => expect(undo).toHaveProperty("disabled", false));
    // A pan of the board, in the pan mode, from its empty corner.
    fireEvent.pointerDown(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 2,
      clientY: 2,
    });
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 100,
      clientY: 100,
    });
    fireEvent.keyDown(canvas, { key: "Delete", code: "Delete" });
    fireEvent.keyDown(canvas, { key: "e", code: "KeyE" });
    fireEvent.keyDown(canvas, { key: "d", code: "KeyD" });
    fireEvent.keyDown(canvas, { key: "z", code: "KeyZ", ctrlKey: true });
    fireEvent.pointerUp(canvas, {
      ...mouse,
      buttons: 0,
      clientX: 100,
      clientY: 100,
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches(calls)).toHaveLength(1);
    fireEvent.keyDown(canvas, { key: "z", code: "KeyZ", ctrlKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
  });

  it("leave the board, and open nothing over it, in the middle of a drag no more, and do once it has ended", async () => {
    boardPage([
      testNode("nd_j", 0, 0, {
        type: "jumper",
        kind: undefined,
        title: "Back to the dock",
        target: { board_id: "bd_a", node_id: "nd_dock" },
      }),
      testNode("nd_dock", 4000, 3000, { title: "The dock" }),
    ]);
    const canvas = await openBoard("?node=nd_j");
    await screen.findByText(/Selected: Back to the dock/);
    const inspector = screen.getByRole("button", { name: "Inspector" });
    const wasOpen = inspector.getAttribute("aria-pressed");
    canvas.focus();
    fireEvent.pointerDown(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 2,
      clientY: 2,
    });
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 1,
      clientX: 100,
      clientY: 100,
    });
    for (const [key, code, shiftKey] of [
      ["g", "KeyG", false],
      ["i", "KeyI", false],
      ["?", "Slash", true],
    ] as const) {
      fireEvent.keyDown(canvas, { key, code, shiftKey });
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(inspector.getAttribute("aria-pressed")).toBe(wasOpen);
    expect(screen.getByText(/Selected: Back to the dock/)).toBeDefined();
    fireEvent.pointerUp(canvas, {
      ...mouse,
      buttons: 0,
      clientX: 100,
      clientY: 100,
    });
    fireEvent.keyDown(canvas, { key: "i", code: "KeyI" });
    await waitFor(() =>
      expect(inspector.getAttribute("aria-pressed")).not.toBe(wasOpen),
    );
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "g", code: "KeyG" });
    expect(await screen.findByText(/Selected: The dock/)).toBeDefined();
  });

  it("open the inspector at its Connect to by C, of the one element selected", async () => {
    boardPage([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]);
    const canvas = await openBoard("?node=nd_1");
    await screen.findByText(/Selected: /);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "c", code: "KeyC" });
    const field = await screen.findByLabelText("Connect to");
    await waitFor(() => expect(document.activeElement).toBe(field));
  });

  it("undo the last change by Ctrl+Z, and show the table of the shortcuts by ?", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard();
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "e", code: "KeyE" });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const undo = await screen.findByRole("button", { name: "Undo" });
    await waitFor(() => expect(undo).toHaveProperty("disabled", false));
    fireEvent.keyDown(canvas, { key: "z", code: "KeyZ", ctrlKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(await screen.findByText("Undone.")).toBeDefined();

    // The help names the keys of the page's platform, as Ctrl+C on Linux.
    expect(
      screen.getByText(/Ctrl\+C, Ctrl\+X, and Ctrl\+V copy it/),
    ).toBeDefined();
    fireEvent.keyDown(canvas, { key: "?", code: "Slash", shiftKey: true });
    const table = await screen.findByRole("dialog", {
      name: "Keyboard shortcuts",
    });
    expect(
      table.querySelector('[data-command="canvas.add_element"]')?.textContent,
    ).toContain("E");
  });

  it("run from the palette as from their keys, once the focus is back on the canvas", async () => {
    const user = userEvent.setup();
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard();
    canvas.focus();
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    await user.type(within(palette).getByRole("combobox"), "add elem");
    // The command, and, in a project, the search for what was typed.
    const options = within(palette).getAllByRole("option");
    expect(options.map((o) => o.dataset.command)).toEqual([
      "canvas.add_element",
      undefined,
    ]);
    expect(options[1].textContent).toBe("Search the project for “add elem”");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0][0].type).toBe("node.create");
    expect(screen.queryByRole("dialog", { name: "Commands" })).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(canvas));
  });

  it("leave the tree's letters to its type-ahead", async () => {
    const user = userEvent.setup();
    const calls = boardPage([
      testNode("nd_1", 0, 0, { title: "Alpha" }),
      testNode("nd_2", 0, 400, { title: "Echo" }),
    ]);
    await openBoard();
    const tree = await screen.findByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    await waitFor(() =>
      expect(within(tree).getAllByRole("treeitem")).toHaveLength(2),
    );
    const [first, second] = within(tree).getAllByRole("treeitem");
    first.focus();
    await user.keyboard("e");
    expect(document.activeElement).toBe(second);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(batches(calls)).toHaveLength(0);
  });
});
