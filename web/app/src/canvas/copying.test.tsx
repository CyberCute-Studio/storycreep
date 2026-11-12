import type {
  Board,
  Clipboard,
  ClipboardReport,
  Connection,
  Node,
} from "@storycreep/shared/api";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { holdCopy } from "../document/clipboard";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import type { Engine } from "./engine";

// The canvas's clipboard (PRD FR-EDIT-07, plan note N71): Copy, Cut, Paste,
// and Duplicate, by the toolbar, the context menu, and the keys, onto
// another board of the project, with what the server's report lists of the
// clipboard that the project lacks created first, as the dialog offers.

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

/** Returns a board of the test's project. */
function boardOf(id: string, name: string, order: string): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: name.toLowerCase(),
    description: "",
    color: "none",
    order,
    rev: 3,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

const harbour = boardOf("bd_a", "Harbour", "V");
const lighthouse = boardOf("bd_b", "Lighthouse", "W");

/** Harbour's nodes: Quay, connected to Dock, beside it. */
function quayAndDock() {
  return {
    nodes: [
      testNode("nd_quay", 0, 0, { title: "Quay" }),
      testNode("nd_dock", 400, 0, { title: "Dock" }),
    ],
    connections: [testConnection("cn_1", "nd_quay", "nd_dock")],
  };
}

/** Returns the clipboard of the nodes of ids among nodes, with the connections between them, as the server's copy answers it. */
function clipboardOf(
  ids: readonly string[],
  nodes: readonly Node[],
  connections: readonly Connection[],
): Clipboard {
  const copied = nodes.filter((n) => ids.includes(n.id));
  return {
    format: "storycreep.clipboard",
    version: 1,
    project_id: testProject.id,
    board_id: copied[0]?.board_id ?? "bd_a",
    nodes: copied.map((n) => ({
      id: n.id,
      type: n.type,
      title: n.title,
      position: n.position,
    })),
    connections: connections
      .filter((c) => ids.includes(c.from_node_id) && ids.includes(c.to_node_id))
      .map((c) => ({
        id: c.id,
        from_node_id: c.from_node_id,
        to_node_id: c.to_node_id,
      })),
    variables: [],
    components: [],
  };
}

/** A report that lists nothing. */
const nothing: ClipboardReport = {
  variables: [],
  components: [],
  attributes: [],
  speakers: [],
  nodes: [],
  assets: [],
};

type Sent = { type: string; payload: unknown; expected_rev?: unknown }[];

/**
 * Answers the board's pages, of Harbour, of nodes and connections, and of
 * Lighthouse, empty, as the project's role says, and copies, reports, and
 * applies each batch as the server does: a creation puts its node on its
 * board, and a paste its nodes, which the board's next read answers with.
 * report answers each report, and copy each copy, as with a refusal it
 * throws, once what it returns has resolved; apply is waited for before
 * each batch is applied, and refuses it where it throws.
 */
function clipboardPage({
  nodes,
  connections,
  role = "owner",
  report = () => nothing,
  copy,
  apply,
}: {
  nodes: Node[];
  connections: Connection[];
  role?: "owner" | "viewer";
  report?: () => ClipboardReport;
  copy?: () => Clipboard | Promise<Clipboard>;
  apply?: (commands: Sent) => Promise<void>;
}) {
  let seq = testProject.seq;
  const onBoard = new Map<string, Node[]>([
    ["bd_a", nodes],
    ["bd_b", []],
  ]);
  return fakeApi(async (operation, input) => {
    const body = (input as { body?: unknown } | undefined)?.body;
    const boardId = (input as { path?: { board_id?: string } } | undefined)
      ?.path?.board_id;
    switch (operation) {
      case "getProject":
        return { ...testProject, role, seq };
      case "listFolders":
      case "listComponentTypes":
      case "listComponents":
      case "listVariables":
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "listBoardConnections":
        return {
          items: boardId === "bd_a" ? connections : [],
          next_cursor: null,
        };
      case "listBoards":
        return { items: [harbour, lighthouse], next_cursor: null };
      case "listBoardNodes":
        return { items: onBoard.get(boardId ?? "") ?? [], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "copyNodes":
        return (
          (await copy?.()) ??
          clipboardOf(
            (body as { node_ids: string[] }).node_ids,
            [...onBoard.values()].flat(),
            connections,
          )
        );
      case "resolveClipboard":
        return report();
      case "applyCommands": {
        const { commands } = body as { commands: Sent };
        await apply?.(commands);
        seq++;
        // The revisions of the nodes created, which a paste's are not, as the
        // board read again brings them.
        const revisions: { id: string; rev: number }[] = [];
        for (const command of commands) {
          if (command.type === "node.create") {
            const p = command.payload as {
              id: string;
              board_id: string;
              position: { x: number; y: number };
            };
            onBoard.set(p.board_id, [
              ...(onBoard.get(p.board_id) ?? []),
              testNode(p.id, p.position.x, p.position.y, {
                board_id: p.board_id,
              }),
            ]);
            revisions.push({ id: p.id, rev: 1 });
          }
          if (command.type === "clipboard.paste") {
            const p = command.payload as {
              board_id: string;
              clipboard: Clipboard;
              ids: Record<string, string>;
              offset: { x: number; y: number };
            };
            const pasted = p.clipboard.nodes.map((n) =>
              testNode(
                p.ids[n.id],
                n.position.x + p.offset.x,
                n.position.y + p.offset.y,
                { board_id: p.board_id, title: n.title },
              ),
            );
            onBoard.set(p.board_id, [
              ...(onBoard.get(p.board_id) ?? []),
              ...pasted,
            ]);
          }
        }
        return {
          status: "applied",
          seq,
          revisions,
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

/** Returns the bodies of the requests of operation that calls made. */
function bodiesOf(calls: ReturnType<typeof fakeApi>, operation: string) {
  return calls.mock.calls
    .filter(([op]) => op === operation)
    .map(([, input]) => (input as { body: unknown }).body);
}

/** Returns the canvas of the board named name, once it is placed on the board. */
async function canvasOf(name: string) {
  const canvas = await screen.findByRole("application", {
    name: `Nodes of ${name}`,
  });
  await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
  return canvas;
}

/** Opens Harbour at search, and returns its canvas once it is placed on the board, with the page's router and documents. */
async function openHarbour(search = "") {
  const app = renderApp(`/projects/${testProject.id}/boards/bd_a${search}`, {
    session: testSession,
  });
  return { ...app, canvas: await canvasOf("Harbour") };
}

/** Shows Lighthouse in the page of router, and returns its canvas once it is placed on the board. */
async function openLighthouse(router: ReturnType<typeof renderApp>["router"]) {
  await act(() =>
    router.navigate({
      to: "/projects/$projectId/boards/$boardId",
      params: { projectId: testProject.id, boardId: "bd_b" },
    }),
  );
  return canvasOf("Lighthouse");
}

const mouse = { pointerId: 1, pointerType: "mouse", button: 0 };

/** Selects every node in view of canvas, with the select mode's rubber band from its corner. */
async function selectAll(canvas: HTMLElement, count: number) {
  await userEvent.click(screen.getByRole("button", { name: "Select" }));
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
  fireEvent.pointerUp(canvas, {
    ...mouse,
    buttons: 0,
    clientX: 798,
    clientY: 598,
  });
  await waitFor(() => expect(canvas.dataset.selected).toBe(String(count)));
}

/** Returns the button of the toolbar's clipboard named name. */
function clipboardButton(name: string): HTMLElement {
  return within(screen.getByRole("group", { name: "Clipboard" })).getByRole(
    "button",
    { name },
  );
}

/** Returns the offset that pastes nodes whose middle is middle onto canvas's centre, on the grid. */
function centredOffset(canvas: HTMLElement, middle: { x: number; y: number }) {
  const zoom = Number(canvas.dataset.zoom);
  const snap = (n: number) => Math.round(n / 8) * 8;
  return {
    x: snap(Number(canvas.dataset.x) + 400 / zoom - middle.x),
    y: snap(Number(canvas.dataset.y) + 300 / zoom - middle.y),
  };
}

describe("the canvas's clipboard", () => {
  // Verifies: FR-EDIT-07/AC2
  it("copies the selected nodes with Copy, and pastes them onto another board with Paste, moved by one offset, their middle at the canvas's centre, and selects them once the board read again brings them", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections });
    const { canvas, router } = await openHarbour();
    await selectAll(canvas, 2);
    await userEvent.click(clipboardButton("Copy"));
    await waitFor(() =>
      expect(bodiesOf(calls, "copyNodes")).toEqual([
        { node_ids: ["nd_quay", "nd_dock"] },
      ]),
    );
    expect(await screen.findByText("2 nodes on the clipboard")).toBeDefined();
    // The copy changes nothing of the board.
    expect(batches(calls)).toEqual([]);

    const other = await openLighthouse(router);
    expect(screen.getByText("2 nodes on the clipboard")).toBeDefined();
    await userEvent.click(clipboardButton("Paste"));
    const clipboard = clipboardOf(["nd_quay", "nd_dock"], nodes, connections);
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(bodiesOf(calls, "resolveClipboard")).toEqual([
      { board_id: "bd_b", clipboard },
    ]);
    const [batch] = batches(calls);
    expect(batch).toHaveLength(1);
    const paste = batch[0] as {
      type: string;
      payload: {
        board_id: string;
        clipboard: Clipboard;
        ids: Record<string, string>;
        offset: { x: number; y: number };
      };
    };
    expect(paste.type).toBe("clipboard.paste");
    expect(paste.payload.board_id).toBe("bd_b");
    expect(paste.payload.clipboard).toEqual(clipboard);
    expect(Object.keys(paste.payload.ids)).toEqual([
      "nd_quay",
      "nd_dock",
      "cn_1",
    ]);
    // The two elements, of 240 by 128, at 0 and 400 across: their middle
    // is at 320, 64.
    expect(paste.payload.offset).toEqual(
      centredOffset(other, { x: 320, y: 64 }),
    );
    await waitFor(() => {
      expect(other.dataset.nodes).toBe("2");
      expect(other.dataset.selected).toBe("2");
    });
  });

  // Verifies: FR-EDIT-07/AC1
  it("lists what the report says that the project lacks before it pastes, and pastes with it created first, in the paste's batch, or without, or not at all", async () => {
    const { nodes, connections } = quayAndDock();
    const report: ClipboardReport = {
      ...nothing,
      variables: [
        { id: "var_trust", key: "trust", type: "int", scope: "global" },
      ],
      components: [
        {
          component: {
            id: "cmp_elara",
            key: "elara",
            name: "Elara",
            type: { id: "ct_person", key: "person", name: "Person" },
            attributes: [
              { id: "atr_fear", key: "fear", name: "Fear", type: "int" },
            ],
          },
        },
      ],
      speakers: ["cmp_ghost"],
    };
    const calls = clipboardPage({ nodes, connections, report: () => report });
    const { router } = await openHarbour("?node=nd_quay");
    await userEvent.click(clipboardButton("Copy"));
    await screen.findByText("1 node on the clipboard");
    await openLighthouse(router);

    await userEvent.click(clipboardButton("Paste"));
    let dialog = await screen.findByRole("dialog", {
      name: "Some of what the nodes name is missing here",
    });
    expect(
      within(dialog).getByText("trust: global, whole number (int)"),
    ).toBeDefined();
    expect(
      within(dialog).getByText(
        "Elara (elara), with its type, Person, which this project lacks too",
      ),
    ).toBeDefined();
    // The attribute that the type is created with, as Create and paste
    // creates it, though the report lists it of no type of the project's.
    expect(
      within(
        within(dialog)
          .getByRole("heading", { name: "Attributes to create" })
          .closest("section") as HTMLElement,
      ).getByText("Fear (fear)"),
    ).toBeDefined();
    expect(
      within(dialog).getByText(
        "1 speaker is not in this project: its elements are pasted without a speaker.",
      ),
    ).toBeDefined();
    // Cancel sends nothing, and gives the focus back to Paste.
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Cancel" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(clipboardButton("Paste"));
    expect(batches(calls)).toEqual([]);

    await userEvent.click(clipboardButton("Paste"));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Create and paste" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0].map((c) => c.type)).toEqual([
      "variable.create",
      "component_type.create",
      "attribute.create",
      "component.create",
      "clipboard.paste",
    ]);
    expect(batches(calls)[0][0].payload).toMatchObject({
      key: "trust",
      type: "int",
      scope: "global",
    });

    await userEvent.click(clipboardButton("Paste"));
    dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Paste without creating" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1].map((c) => c.type)).toEqual(["clipboard.paste"]);
  });

  it("cuts the selected nodes, as a copy and then their deletion, and deletes nothing of a copy refused, which it says", async () => {
    const { nodes, connections } = quayAndDock();
    let refuse = false;
    const calls = clipboardPage({
      nodes,
      connections,
      copy: () => {
        if (refuse) {
          throw refused(422, {
            code: "validation_failed",
            message: "The nodes are too large to copy.",
          });
        }
        return clipboardOf(["nd_quay"], nodes, connections);
      },
    });
    await openHarbour("?node=nd_quay");
    refuse = true;
    await userEvent.click(clipboardButton("Cut"));
    expect(
      await screen.findByText(
        "The nodes could not be copied: The nodes are too large to copy.",
      ),
    ).toBeDefined();
    expect(batches(calls)).toEqual([]);
    expect(screen.queryByText("1 node on the clipboard")).toBeNull();

    refuse = false;
    await userEvent.click(clipboardButton("Cut"));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.delete",
        payload: { id: "nd_quay" },
        expected_rev: { nd_quay: 1 },
      },
    ]);
    expect(screen.getByText("1 node on the clipboard")).toBeDefined();
  });

  // Verifies: FR-EDIT-07/AC2
  it("duplicates the selection with the connections between them, as a copy pasted back beside it, and leaves the clipboard as it was", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections });
    const { canvas } = await openHarbour();
    await selectAll(canvas, 2);
    await userEvent.click(
      within(screen.getByRole("group", { name: "Edit the board" })).getByRole(
        "button",
        { name: "Duplicate" },
      ),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(bodiesOf(calls, "resolveClipboard")).toEqual([]);
    const paste = batches(calls)[0][0] as {
      type: string;
      payload: { board_id: string; offset: unknown; ids: object };
    };
    expect(paste.type).toBe("clipboard.paste");
    expect(paste.payload.board_id).toBe("bd_a");
    expect(paste.payload.offset).toEqual({ x: 32, y: 32 });
    expect(Object.keys(paste.payload.ids)).toContain("cn_1");
    await waitFor(() => {
      expect(canvas.dataset.nodes).toBe("4");
      expect(canvas.dataset.selected).toBe("2");
    });
    expect(screen.queryByText(/on the clipboard/)).toBeNull();
  });

  it("copies, cuts, and pastes by Ctrl or Cmd and C, X, and V on the canvas, by the key's place on a layout whose letters are not Latin, and not with Shift", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections });
    const { canvas, router } = await openHarbour("?node=nd_quay");
    canvas.focus();
    fireEvent.keyDown(canvas, {
      key: "C",
      code: "KeyC",
      ctrlKey: true,
      shiftKey: true,
    });
    fireEvent.keyDown(canvas, { key: "с", code: "KeyC", ctrlKey: true });
    await waitFor(() =>
      expect(bodiesOf(calls, "copyNodes")).toEqual([{ node_ids: ["nd_quay"] }]),
    );
    await screen.findByText("1 node on the clipboard");

    const other = await openLighthouse(router);
    other.focus();
    fireEvent.keyDown(other, { key: "v", code: "KeyV", metaKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0].map((c) => c.type)).toEqual(["clipboard.paste"]);
    // The pasted node, selected once the page shows it, as Cut, which is
    // unavailable until then, says, is cut, as a copy and its deletion.
    await waitFor(() => {
      expect(other.dataset.nodes).toBe("1");
      expect(other.dataset.selected).toBe("1");
      expect(clipboardButton("Cut").getAttribute("aria-disabled")).toBeNull();
    });
    fireEvent.keyDown(other, { key: "x", code: "KeyX", ctrlKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1].map((c) => c.type)).toEqual(["node.delete"]);
    expect(bodiesOf(calls, "copyNodes")).toHaveLength(2);
  });

  it("copies the node that the tree's focus is on, as the tree's Delete deletes it, and pastes from the tree", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections });
    await openHarbour("?node=nd_quay");
    const tree = await screen.findByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    const dock = await within(tree).findByRole("treeitem", {
      name: "Dock, Narration",
    });
    dock.focus();
    fireEvent.keyDown(dock, { key: "c", code: "KeyC", ctrlKey: true });
    await waitFor(() =>
      expect(bodiesOf(calls, "copyNodes")).toEqual([{ node_ids: ["nd_dock"] }]),
    );
    await screen.findByText("1 node on the clipboard");
    fireEvent.keyDown(dock, { key: "v", code: "KeyV", ctrlKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0].map((c) => c.type)).toEqual(["clipboard.paste"]);
  });

  it("offers Copy alone on a board that may not be changed, whose keys cut and paste nothing", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections, role: "viewer" });
    const { canvas } = await openHarbour("?node=nd_quay");
    expect(screen.queryByRole("button", { name: "Cut" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Paste" })).toBeNull();
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "x", code: "KeyX", ctrlKey: true });
    await userEvent.click(clipboardButton("Copy"));
    await screen.findByText("1 node on the clipboard");
    fireEvent.keyDown(canvas, { key: "v", code: "KeyV", ctrlKey: true });
    expect(bodiesOf(calls, "copyNodes")).toHaveLength(1);
    expect(bodiesOf(calls, "resolveClipboard")).toEqual([]);
    expect(batches(calls)).toEqual([]);
  });

  it("pastes nothing that was copied in another workspace, nor by another account", async () => {
    const { nodes, connections } = quayAndDock();
    clipboardPage({ nodes, connections });
    const { documents } = await openHarbour();
    const clipboard = clipboardOf(["nd_quay"], nodes, connections);
    act(() =>
      holdCopy(documents.clipboard, {
        userId: testSession.user_id,
        workspaceId: "ws_other",
        clipboard,
      }),
    );
    expect(clipboardButton("Paste")).toHaveProperty("disabled", true);
    expect(screen.queryByText(/on the clipboard/)).toBeNull();
    act(() =>
      holdCopy(documents.clipboard, {
        userId: "usr_other",
        workspaceId: testProject.workspace_id,
        clipboard,
      }),
    );
    expect(clipboardButton("Paste")).toHaveProperty("disabled", true);
    act(() =>
      holdCopy(documents.clipboard, {
        userId: testSession.user_id,
        workspaceId: testProject.workspace_id,
        clipboard,
      }),
    );
    expect(clipboardButton("Paste")).toHaveProperty("disabled", false);
    expect(screen.getByText("1 node on the clipboard")).toBeDefined();
  });

  it("pastes at the point of the board where its menu was asked for", async () => {
    const { nodes, connections } = quayAndDock();
    const calls = clipboardPage({ nodes, connections });
    const { canvas } = await openHarbour("?node=nd_quay");
    await userEvent.click(clipboardButton("Copy"));
    await screen.findByText("1 node on the clipboard");
    // The board's menu, near the canvas's corner, below and right of the
    // nodes.
    const zoom = Number(canvas.dataset.zoom);
    const at = {
      x: Number(canvas.dataset.x) + 700 / zoom,
      y: Number(canvas.dataset.y) + 560 / zoom,
    };
    fireEvent.contextMenu(canvas, { button: 2, clientX: 700, clientY: 560 });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the board here",
    });
    await userEvent.click(
      within(menu).getByRole("menuitem", { name: "Paste here" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // Quay, 240 by 128, has its middle at 120, 64.
    const snap = (n: number) => Math.round(n / 8) * 8;
    expect(
      (batches(calls)[0][0].payload as { offset: unknown }).offset,
    ).toEqual({ x: snap(at.x - 120), y: snap(at.y - 64) });
  });

  it("copies a node that the page has just added once the batch that creates it has applied, as the server copies what it has", async () => {
    const { nodes, connections } = quayAndDock();
    let release = () => {};
    const created = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = clipboardPage({ nodes, connections, apply: () => created });
    const { canvas } = await openHarbour();
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Element" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const id = (batches(calls)[0][0].payload as { id: string }).id;
    // The new node, selected at once, as the page shows it at once.
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    await userEvent.click(clipboardButton("Copy"));
    await act(async () => {});
    expect(bodiesOf(calls, "copyNodes")).toEqual([]);
    release();
    await waitFor(() =>
      expect(bodiesOf(calls, "copyNodes")).toEqual([{ node_ids: [id] }]),
    );
    expect(await screen.findByText("1 node on the clipboard")).toBeDefined();
  });

  it("duplicates one at a time, which its controls say, and keeps a selection that the user made while the copy was under way", async () => {
    const { nodes, connections } = quayAndDock();
    let release = () => {};
    const answered = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = clipboardPage({
      nodes,
      connections,
      copy: async () => {
        await answered;
        return clipboardOf(["nd_quay", "nd_dock"], nodes, connections);
      },
    });
    const { canvas } = await openHarbour();
    await selectAll(canvas, 2);
    const duplicate = within(
      screen.getByRole("group", { name: "Edit the board" }),
    ).getByRole("button", { name: "Duplicate" });
    await userEvent.click(duplicate);
    // A second Duplicate, before the first's copies show, which it would
    // put its own over, duplicates nothing, and Duplicate, Copy, Cut, and
    // Paste say that they are unavailable meanwhile, keeping the focus.
    await userEvent.click(duplicate);
    await waitFor(() => expect(bodiesOf(calls, "copyNodes")).toHaveLength(1));
    for (const button of [
      duplicate,
      clipboardButton("Copy"),
      clipboardButton("Cut"),
      clipboardButton("Paste"),
    ]) {
      expect(button.getAttribute("aria-disabled")).toBe("true");
    }
    expect(document.activeElement).toBe(duplicate);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Escape", code: "Escape" });
    await waitFor(() => expect(canvas.dataset.selected).toBe("0"));
    release();
    await waitFor(() => expect(canvas.dataset.nodes).toBe("4"));
    expect(batches(calls)).toHaveLength(1);
    expect(bodiesOf(calls, "copyNodes")).toHaveLength(1);
    expect(canvas.dataset.selected).toBe("0");
    await waitFor(() =>
      expect(duplicate.getAttribute("aria-disabled")).toBeNull(),
    );
  });

  it("pastes nothing while a copy is under way, rather than what the clipboard held before it, and then what it copied", async () => {
    const { nodes, connections } = quayAndDock();
    let release = () => {};
    const answered = new Promise<void>((resolve) => {
      release = resolve;
    });
    let slow = false;
    const calls = clipboardPage({
      nodes,
      connections,
      copy: async () => {
        if (slow) {
          await answered;
          return clipboardOf(["nd_quay", "nd_dock"], nodes, connections);
        }
        return clipboardOf(["nd_quay"], nodes, connections);
      },
    });
    const { canvas } = await openHarbour("?node=nd_quay");
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "c", code: "KeyC", ctrlKey: true });
    await screen.findByText("1 node on the clipboard");
    // Quay and Dock, copied in place of Quay alone, and pasted before
    // their copy is answered.
    slow = true;
    await selectAll(canvas, 2);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "c", code: "KeyC", ctrlKey: true });
    await waitFor(() => expect(bodiesOf(calls, "copyNodes")).toHaveLength(2));
    fireEvent.keyDown(canvas, { key: "v", code: "KeyV", ctrlKey: true });
    await act(async () => {});
    expect(bodiesOf(calls, "resolveClipboard")).toEqual([]);
    release();
    await waitFor(() =>
      expect(clipboardButton("Paste").getAttribute("aria-disabled")).toBeNull(),
    );
    expect(screen.getByText("2 nodes on the clipboard")).toBeDefined();
    fireEvent.keyDown(canvas, { key: "v", code: "KeyV", ctrlKey: true });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const paste = batches(calls)[0][0].payload as { clipboard: Clipboard };
    expect(paste.clipboard.nodes.map((n) => n.id)).toEqual([
      "nd_quay",
      "nd_dock",
    ]);
  });

  it("says that the change could not be saved of a Duplicate whose copy is refused, and gives the inspector's heading the focus of the inspector's Duplicate", async () => {
    const { nodes, connections } = quayAndDock();
    let refuse = true;
    const calls = clipboardPage({
      nodes,
      connections,
      copy: () => {
        if (refuse) {
          throw refused(422, {
            code: "validation_failed",
            message: "The nodes are too large to copy.",
          });
        }
        return clipboardOf(["nd_quay"], nodes, connections);
      },
    });
    // The link opens Quay with its inspector.
    const { canvas } = await openHarbour("?node=nd_quay");
    const inspector = await screen.findByRole("region", { name: "Quay" });
    const heading = within(inspector).getByRole("heading", { name: "Quay" });
    await userEvent.click(
      within(inspector).getByRole("button", { name: "Duplicate" }),
    );
    expect(document.activeElement).toBe(heading);
    expect(
      await screen.findByText(
        "The change could not be saved: The nodes are too large to copy.",
      ),
    ).toBeDefined();
    expect(batches(calls)).toEqual([]);

    refuse = false;
    await waitFor(() =>
      expect(
        within(inspector).getByRole("button", { name: "Duplicate" }),
      ).toHaveProperty("disabled", false),
    );
    await userEvent.click(
      within(inspector).getByRole("button", { name: "Duplicate" }),
    );
    await waitFor(() => expect(canvas.dataset.nodes).toBe("3"));
    expect(batches(calls)[0].map((c) => c.type)).toEqual(["clipboard.paste"]);
    expect(document.activeElement).toBe(heading);
  });

  it("says that the nodes could not be pasted of a paste's batch refused", async () => {
    const { nodes, connections } = quayAndDock();
    clipboardPage({
      nodes,
      connections,
      apply: async (commands) => {
        if (commands.some((c) => c.type === "clipboard.paste")) {
          throw refused(422, {
            code: "validation_failed",
            message: "A node would be too far away.",
          });
        }
      },
    });
    const { router } = await openHarbour("?node=nd_quay");
    await userEvent.click(clipboardButton("Copy"));
    await screen.findByText("1 node on the clipboard");
    await openLighthouse(router);
    await userEvent.click(clipboardButton("Paste"));
    expect(
      await screen.findByText(
        "The nodes could not be pasted: A node would be too far away.",
      ),
    ).toBeDefined();
  });
});
