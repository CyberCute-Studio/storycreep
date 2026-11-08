import type { Board, Node } from "@storycreep/shared/api";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, startPage, testProject } from "../test/fake";
import type { Engine } from "./engine";
import { boxPlace } from "./TextEditor";

// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of: the page's
// tests draw with an engine that draws nothing.
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

// jsdom lays nothing out: the canvas is 800 by 600, from the window's
// corner, so that it is placed on the board, and a pointer's point on it
// is its point in the window.
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

/** Returns the IDs of the nodes that command changes or makes. */
function touched(command: Sent[number]): string[] {
  const p = command.payload as {
    id?: string;
    ids?: string[];
    nodes?: { id: string }[];
  };
  return [
    ...(p.id === undefined ? [] : [p.id]),
    ...(p.ids ?? []),
    ...(p.nodes ?? []).map((n) => n.id),
  ];
}

/**
 * Answers the board's page, of a board of nodes, and applies each batch
 * that the page sends, which leaves each node that it changes or makes at
 * its next revision, and whose undo is the batch itself, as the history
 * keeps it.
 */
function boardPage(nodes: Node[]) {
  let seq = testProject.seq;
  const revs = new Map(nodes.map((n) => [n.id, n.rev]));
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        // As of the batches that it applied, which an undo reads first.
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
      case "applyCommands": {
        const { commands } = (input as { body: { commands: Sent } }).body;
        seq++;
        const ids = new Set(commands.flatMap(touched));
        for (const id of ids) {
          revs.set(id, (revs.get(id) ?? 0) + 1);
        }
        return {
          status: "applied",
          seq,
          revisions: [...ids].map((id) => ({ id, rev: revs.get(id) })),
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

/** Returns the commands of the batches that calls sent, without their IDs. */
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

/** Opens the board's page at search, and returns its canvas once it is placed on the board. */
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

/** Returns where the board's point p is on canvas, as its data attributes tell its view. */
function screenOf(canvas: HTMLElement, p: { x: number; y: number }) {
  const zoom = Number(canvas.dataset.zoom);
  return {
    clientX: (p.x - Number(canvas.dataset.x)) * zoom,
    clientY: (p.y - Number(canvas.dataset.y)) * zoom,
  };
}

const mouse = { pointerId: 1, pointerType: "mouse", button: 0 };

describe("the canvas's edits", () => {
  // Verifies: FR-EDIT-02/AC1
  it("adds a node at the centre of the canvas with one command, and selects it", async () => {
    const user = userEvent.setup();
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    await openBoard();
    const edit = screen.getByRole("group", { name: "Edit the board" });
    await user.click(within(edit).getByRole("button", { name: "Add" }));
    await user.click(await screen.findByRole("menuitem", { name: "Element" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[create]] = batches(calls);
    expect(create.type).toBe("node.create");
    expect(create.payload).toMatchObject({ board_id: "bd_a", type: "element" });
    expect(await screen.findByText("1 node selected")).toBeDefined();
  });

  // Verifies: FR-EDIT-02/AC2, NFR-TOUCH-05
  it("moves the selection by a drag with one command, and offers its undo, which sends it back", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]);
    const canvas = await openBoard("?node=nd_1");
    expect(canvas.dataset.selected).toBe("1");
    // A click with Shift adds the second node.
    const second = screenOf(canvas, { x: 520, y: 64 });
    fireEvent.pointerDown(canvas, {
      ...mouse,
      buttons: 1,
      shiftKey: true,
      ...second,
    });
    fireEvent.pointerUp(canvas, {
      ...mouse,
      buttons: 0,
      shiftKey: true,
      ...second,
    });
    await waitFor(() => expect(canvas.dataset.selected).toBe("2"));
    // A drag from the first moves both, by 100 pixels at the zoom.
    const from = screenOf(canvas, { x: 120, y: 64 });
    const zoom = Number(canvas.dataset.zoom);
    const to = { clientX: from.clientX + 100 * zoom, clientY: from.clientY };
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...from });
    fireEvent.pointerMove(canvas, { ...mouse, buttons: 1, ...to });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...to });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.move",
        payload: {
          nodes: [
            { id: "nd_1", position: { x: 104, y: 0 } },
            { id: "nd_2", position: { x: 504, y: 0 } },
          ],
        },
        expected_rev: { nd_1: 1, nd_2: 1 },
      },
    ]);
    const bar = await screen.findByText("Moved 2 nodes.");
    await userEvent.click(
      within(bar).getByRole("button", { name: "Undo the move" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1][0].type).toBe("node.move");
    await waitFor(() =>
      expect(screen.queryByText("Moved 2 nodes.")).toBeNull(),
    );
  });

  it("opens the node that a link names, selected, with its inspector, whose form saves what changed in one command", async () => {
    const user = userEvent.setup();
    const calls = boardPage([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]);
    const canvas = await openBoard("?node=nd_2");
    expect(canvas.dataset.selected).toBe("1");
    const inspector = await screen.findByRole("region", { name: "nd_2" });
    const title = within(inspector).getByLabelText("Title");
    await user.clear(title);
    await user.type(title, "The gate");
    await user.click(
      within(inspector).getByRole("button", { name: "Save the node" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.update",
        payload: { id: "nd_2", title: "The gate" },
        expected_rev: { nd_2: 1 },
      },
    ]);
  });

  it("deletes the selection to the trash with the toolbar's Delete, and with the Delete key", async () => {
    const user = userEvent.setup();
    const calls = boardPage([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]);
    const canvas = await openBoard("?node=nd_1");
    const edit = screen.getByRole("group", { name: "Edit the board" });
    await user.click(within(edit).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.delete",
        payload: { id: "nd_1" },
        expected_rev: { nd_1: 1 },
      },
    ]);
    await waitFor(() => expect(canvas.dataset.selected).toBe("0"));
    const left = screenOf(canvas, { x: 520, y: 64 });
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...left });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...left });
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    fireEvent.keyDown(canvas, { key: "Delete" });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1][0].type).toBe("node.delete");
  });

  // Verifies: FR-EDIT-06/AC1
  it("dims the nodes without the tag that the filter names, whatever its case, and sends nothing", async () => {
    const user = userEvent.setup();
    const calls = boardPage([
      testNode("nd_1", 0, 0, { tags: ["Quest"] }),
      testNode("nd_2", 400, 0, { tags: ["quest", "Mira"] }),
      testNode("nd_3", 800, 0),
    ]);
    const canvas = await openBoard();
    expect(canvas.dataset.dimmed).toBe("0");
    const filter = screen.getByLabelText("Dim nodes without the tag");
    expect(
      within(filter)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["No tag: dim none", "Quest", "Mira"]);
    await user.selectOptions(filter, "Quest");
    await waitFor(() => expect(canvas.dataset.dimmed).toBe("1"));
    await user.selectOptions(filter, "Mira");
    await waitFor(() => expect(canvas.dataset.dimmed).toBe("2"));
    // The graph is as it was.
    expect(canvas.dataset.nodes).toBe("3");
    await user.selectOptions(filter, "");
    await waitFor(() => expect(canvas.dataset.dimmed).toBe("0"));
    expect(batches(calls)).toEqual([]);
  });

  it("edits an element's text over its card, from Enter, and saves it in one command", async () => {
    const calls = boardPage([testNode("nd_1", 0, 0)]);
    const canvas = await openBoard("?node=nd_1");
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Enter" });
    const editor = await screen.findByRole("form", {
      name: "The text of nd_1",
    });
    const textbox = within(editor).getByRole("textbox", {
      name: "Text of nd_1",
    });
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    act(() => {
      (
        textbox as unknown as {
          editor: { commands: { insertContent: (text: string) => void } };
        }
      ).editor.commands.insertContent("Elara waits.");
    });
    await userEvent.click(
      within(editor).getByRole("button", { name: "Save the text" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.update",
        payload: {
          id: "nd_1",
          text: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: "Elara waits." }],
              },
            ],
          },
        },
        expected_rev: { nd_1: 1 },
      },
    ]);
    await waitFor(() =>
      expect(
        screen.queryByRole("form", { name: "The text of nd_1" }),
      ).toBeNull(),
    );
  });
});

describe("the place of the editor of an element's text", () => {
  // Verifies: FR-EDIT-09/AC3, NFR-TOUCH-04
  it("stays within the part of the canvas that the window shows above the on-screen keyboard, at the canvas's edge where the node is beyond it", () => {
    const canvas = { width: 1000, height: 800, top: 200 };
    // The window shows 1200 pixels; the keyboard leaves 45% of it.
    const band = { top: 0, bottom: 540 };
    const node = { x: 100, y: 500, width: 480, height: 256 };
    const p = boxPlace(node, canvas, band, 260);
    // What the window shows of the canvas: from 0 to 340 of it.
    expect(p.maxHeight).toBe(340 - 16);
    expect(p.y).toBeGreaterThanOrEqual(8);
    expect(p.y + Math.min(260, p.maxHeight)).toBeLessThanOrEqual(332);
    expect(p).toMatchObject({ x: 100, width: 480 });
    // A node in view keeps the box over it.
    expect(boxPlace({ ...node, y: 20 }, canvas, band, 100).y).toBe(20);
    // A node narrower than a line of text, at a low zoom, has a box wide
    // enough for one, within the canvas.
    expect(
      boxPlace({ ...node, x: 900, width: 24 }, canvas, band, 100),
    ).toMatchObject({
      x: 1000 - 8 - 320,
      width: 320,
    });
  });
});
