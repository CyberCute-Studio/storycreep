import type {
  Board,
  BranchOutput,
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
import { keyBetween } from "../project/orderkey";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import type { Engine } from "./engine";
import { linkHandleSides } from "./Links";

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
// corner, so that a pointer's point on it is its point in the window.
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
  vi.unstubAllGlobals();
});

const at = "2026-11-01T10:00:00Z";

/** Returns a board of the test project. */
function board(id: string, name: string): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: name.toLowerCase(),
    description: "",
    color: "none",
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
  };
}

/** Returns a key after the last of keys, as the server places what a command gives no key. */
function keyAfter(keys: (string | undefined)[]): string {
  const last = keys
    .filter((key): key is string => key !== undefined)
    .sort()
    .at(-1);
  return keyBetween(last, undefined) ?? "V";
}

const harbour = board("bd_a", "Harbour");
const lighthouse = board("bd_b", "Lighthouse");

/** Returns an output of the branch of nodeId. */
function output(
  id: string,
  nodeId: string,
  kind: BranchOutput["kind"],
  order?: string,
): BranchOutput {
  return {
    id,
    node_id: nodeId,
    kind,
    ...(order === undefined ? {} : { order }),
    ...(kind === "else" ? {} : { condition: { source: "", is_valid: false } }),
    rev: 1,
    created_at: at,
    updated_at: at,
  } as BranchOutput;
}

type Sent = { type: string; payload: Record<string, unknown> }[];

/**
 * Answers the board's page from a project of nodes and connections that it
 * keeps, as the server does: it applies the commands of each batch that the
 * page sends to them, as far as the canvas's edits of connections, of
 * outputs, and of a jumper's target go, and answers with the revisions that
 * they left, so that what the page shows after a batch is what the project
 * holds. A board, or a node, that the project does not have is not found,
 * as on the server, and a batch is answered once sent has resolved, where
 * the test holds the server's answers back.
 */
function flowProject(initial: {
  nodes: Node[];
  connections?: Connection[];
  boards?: Board[];
  sent?: Promise<void>;
}) {
  let seq = testProject.seq;
  const nodes = new Map(initial.nodes.map((n) => [n.id, n]));
  const connections = new Map(
    (initial.connections ?? []).map((c) => [c.id, c]),
  );
  const boards = initial.boards ?? [harbour, lighthouse];
  const changed = new Map<string, number>();
  const touch = (id: string, rev: number) => changed.set(id, rev);
  /** Gives the outputs of node the kinds of their places, and a revision more to each whose kind changed. */
  const outputsOf = (node: Node, outputs: BranchOutput[]) => {
    const conditional = outputs
      .filter((o) => o.kind !== "else")
      .sort((a, b) => ((a.order ?? "") < (b.order ?? "") ? -1 : 1));
    const next = [
      ...conditional.map((o, i): BranchOutput => {
        const kind = i === 0 ? "if" : "elseif";
        if (o.kind === kind) {
          return o;
        }
        touch(o.id, o.rev + 1);
        return { ...o, kind, rev: o.rev + 1 };
      }),
      ...outputs.filter((o) => o.kind === "else"),
    ];
    nodes.set(node.id, { ...node, outputs: next });
  };
  const apply = ({ type, payload: p }: Sent[number]) => {
    const id = String(p.id);
    switch (type) {
      case "node.create": {
        const position = p.position as { x: number; y: number };
        nodes.set(
          id,
          testNode(id, position.x, position.y, {
            board_id: String(p.board_id),
            type: p.type as Node["type"],
            title: "",
          }),
        );
        touch(id, 1);
        return;
      }
      case "node.update": {
        const node = nodes.get(id) as Node;
        const target = p.target as Node["target"];
        const { target: _, ...rest } = node;
        nodes.set(id, {
          ...rest,
          ...(target?.node_id === undefined ? {} : { target }),
          rev: node.rev + 1,
        });
        touch(id, node.rev + 1);
        return;
      }
      case "connection.create": {
        const port = String(p.from_port ?? "out");
        const from = nodes.get(String(p.from_node_id)) as Node;
        connections.set(
          id,
          testConnection(id, from.id, String(p.to_node_id), {
            board_id: from.board_id,
            from_port: port,
            order: keyAfter(
              [...connections.values()]
                .filter(
                  (c) => c.from_node_id === from.id && c.from_port === port,
                )
                .map((c) => c.order),
            ),
          }),
        );
        touch(id, 1);
        return;
      }
      case "connection.update":
      case "connection.reconnect":
      case "connection.reorder": {
        const c = connections.get(id) as Connection;
        connections.set(id, {
          ...c,
          ...(p.label === undefined ? {} : { label: p.label as never }),
          ...(p.from_node_id === undefined
            ? {}
            : { from_node_id: String(p.from_node_id) }),
          ...(p.from_port === undefined
            ? {}
            : { from_port: String(p.from_port) }),
          ...(p.to_node_id === undefined
            ? {}
            : { to_node_id: String(p.to_node_id) }),
          ...(p.order === undefined ? {} : { order: String(p.order) }),
          rev: c.rev + 1,
        });
        touch(id, c.rev + 1);
        return;
      }
      case "connection.delete": {
        const c = connections.get(id) as Connection;
        connections.delete(id);
        touch(id, c.rev + 1);
        return;
      }
      case "branch_output.create": {
        const node = nodes.get(String(p.node_id)) as Node;
        const outputs = node.outputs ?? [];
        const made = output(
          id,
          node.id,
          p.else === true ? "else" : "elseif",
          p.else === true ? undefined : keyAfter(outputs.map((o) => o.order)),
        );
        touch(id, 1);
        outputsOf(node, [...outputs, made]);
        return;
      }
      case "branch_output.reorder":
      case "branch_output.delete": {
        const node = [...nodes.values()].find((n) =>
          n.outputs?.some((o) => o.id === id),
        ) as Node;
        const was = (node.outputs ?? []).find((o) => o.id === id);
        const rev = (was?.rev ?? 0) + 1;
        touch(id, rev);
        if (type === "branch_output.delete") {
          for (const c of [...connections.values()]) {
            if (c.from_port === id) {
              connections.delete(c.id);
            }
          }
          outputsOf(
            node,
            (node.outputs ?? []).filter((o) => o.id !== id),
          );
        } else {
          outputsOf(
            node,
            (node.outputs ?? []).map((o) =>
              o.id === id ? { ...o, order: String(p.order), rev } : o,
            ),
          );
        }
        return;
      }
      default:
        throw new Error(`the fake applies no ${type}`);
    }
  };
  const calls = fakeApi((operation, input) => {
    const path =
      (input as { path?: Record<string, string> } | undefined)?.path ?? {};
    switch (operation) {
      case "getProject":
        return { ...testProject, seq };
      case "listFolders":
      case "listComponentTypes":
      case "listComponents":
      case "listVariables":
        return { items: [], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "listBoards":
        return { items: boards, next_cursor: null };
      case "listBoardNodes":
        if (!boards.some((b) => b.id === path.board_id)) {
          throw refused(404, { code: "not_found" });
        }
        return {
          items: [...nodes.values()].filter(
            (n) => n.board_id === path.board_id,
          ),
          next_cursor: null,
        };
      case "listBoardConnections":
        return {
          items: [...connections.values()].filter(
            (c) => c.board_id === path.board_id,
          ),
          next_cursor: null,
        };
      case "getNode": {
        const node = nodes.get(path.node_id);
        if (node === undefined) {
          throw refused(404, { code: "not_found" });
        }
        return node;
      }
      case "getConnection": {
        const connection = connections.get(path.connection_id);
        if (connection === undefined) {
          throw refused(404, { code: "not_found" });
        }
        return connection;
      }
      case "listNodeReferrers":
        if (!nodes.has(path.node_id)) {
          throw refused(404, { code: "not_found" });
        }
        return {
          items: [...nodes.values()].filter(
            (n) => n.type === "jumper" && n.target?.node_id === path.node_id,
          ),
          next_cursor: null,
        };
      case "applyCommands":
        return (async () => {
          await initial.sent;
          const { commands } = (input as { body: { commands: Sent } }).body;
          changed.clear();
          for (const command of commands) {
            apply(command);
          }
          seq++;
          return {
            status: "applied",
            seq,
            revisions: [...changed].map(([id, rev]) => ({ id, rev })),
            not_reconnected: [],
            undo: commands.map(({ type, payload }) => ({
              type,
              payload,
              expected_rev: {},
            })),
          };
        })();
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
  return { calls, nodes, connections };
}

/** Returns the commands of the batches that calls sent, without their IDs. */
function batches(calls: ReturnType<typeof fakeApi>) {
  return calls.mock.calls
    .filter(([operation]) => operation === "applyCommands")
    .map(([, input]) =>
      (
        input as {
          body: {
            commands: {
              command_id: string;
              type: string;
              payload: Record<string, unknown>;
              expected_rev?: Record<string, number>;
            }[];
          };
        }
      ).body.commands.map(({ command_id: _, ...command }) => command),
    );
}

/** Opens the page of the board of boardId at search, and returns its canvas once it is placed on the board. */
async function openBoard(search = "", boardId = "bd_a", name = "Harbour") {
  renderApp(`/projects/${testProject.id}/boards/${boardId}${search}`, {
    session: testSession,
  });
  const canvas = await screen.findByRole("application", {
    name: `Nodes of ${name}`,
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

/** Returns the link handle of kind, of port where it is given, once the page shows it. */
async function linkHandle(kind: string, port?: string) {
  const selector = `[data-link="${kind}"]${
    port === undefined ? "" : `[data-port="${port}"]`
  }`;
  await waitFor(() =>
    expect(document.querySelector<HTMLElement>(selector)?.hidden).toBe(false),
  );
  return document.querySelector(selector) as HTMLElement;
}

/**
 * Drags handle, which is at from of the board, to to, as a mouse does: the
 * press goes down on the handle, and the canvas, which captures its
 * pointer, hears the rest.
 */
function dragHandle(
  canvas: HTMLElement,
  handle: HTMLElement,
  from: { x: number; y: number },
  to: { x: number; y: number },
) {
  fireEvent.pointerDown(handle, {
    ...mouse,
    buttons: 1,
    ...screenOf(canvas, from),
  });
  fireEvent.pointerMove(canvas, {
    ...mouse,
    buttons: 1,
    ...screenOf(canvas, to),
  });
  fireEvent.pointerUp(canvas, {
    ...mouse,
    buttons: 0,
    ...screenOf(canvas, to),
  });
}

/** Clicks the board's point p of canvas. */
function clickAt(canvas: HTMLElement, p: { x: number; y: number }) {
  fireEvent.pointerDown(canvas, {
    ...mouse,
    buttons: 1,
    ...screenOf(canvas, p),
  });
  fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...screenOf(canvas, p) });
}

describe("the canvas's connections", () => {
  it("connects the node selected to the node that a drag of its port's handle lands on, with one command", async () => {
    const { calls } = flowProject({
      nodes: [testNode("nd_1", 0, 0), testNode("nd_2", 480, 0)],
    });
    const canvas = await openBoard("?node=nd_1");
    const handle = await linkHandle("new", "out");
    // A mouse's handle is smaller than a finger's (FR-EDIT-09).
    expect(handle.style.width).toBe(`${linkHandleSides.fine}px`);
    // A drag let go on the handle that it left, 6 px of the canvas from its
    // port, connects nothing, to its own node or to a new one, and neither
    // does one let go outside the canvas, which is 800 wide.
    const port = screenOf(canvas, { x: 240, y: 64 });
    for (const to of [
      { clientX: port.clientX + 6, clientY: port.clientY },
      { clientX: 900, clientY: port.clientY },
    ]) {
      fireEvent.pointerDown(handle, { ...mouse, buttons: 1, ...port });
      fireEvent.pointerMove(canvas, { ...mouse, buttons: 1, ...to });
      fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...to });
      expect(screen.queryByRole("dialog")).toBeNull();
    }
    // The canvas shows the board up to 520 across: the other node's edge.
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 500, y: 64 });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "connection.create",
        payload: {
          id: expect.stringMatching(/^cn_/),
          from_node_id: "nd_1",
          from_port: "out",
          to_node_id: "nd_2",
        },
      },
    ]);
    await waitFor(() => expect(canvas.dataset.connections).toBe("1"));
    // The node stays selected, for its next connection.
    expect(canvas.dataset.selected).toBe("1");
  });

  // Verifies: FR-EDIT-03/AC3
  it("offers to create a node and connect it where a connection is dropped on the empty board, in one batch, and leaves nothing behind once cancelled", async () => {
    const user = userEvent.setup();
    const { calls, nodes, connections } = flowProject({
      nodes: [testNode("nd_1", 0, 0), testNode("nd_2", 480, 0)],
    });
    const canvas = await openBoard("?node=nd_1");
    const handle = await linkHandle("new", "out");
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    const offer = await screen.findByRole("dialog", {
      name: "Create a node here and connect it",
    });
    // Cancelled, it creates nothing: no batch, and the board as it was.
    await user.click(within(offer).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", {
          name: "Create a node here and connect it",
        }),
      ).toBeNull(),
    );
    expect(batches(calls)).toEqual([]);
    expect(nodes.size).toBe(2);
    expect(connections.size).toBe(0);
    expect(canvas.dataset.nodes).toBe("2");
    expect(canvas.dataset.connections).toBe("0");
    // Escape cancels it alike.
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    await screen.findByRole("dialog", {
      name: "Create a node here and connect it",
    });
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(batches(calls)).toEqual([]);
    // Chosen, the node and its connection are one batch, the node with its
    // entry where the connection was dropped, on the grid.
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    const again = await screen.findByRole("dialog", {
      name: "Create a node here and connect it",
    });
    await user.click(within(again).getByRole("button", { name: "Branch" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[create, connect]] = batches(calls);
    expect(create).toEqual({
      type: "node.create",
      payload: {
        board_id: "bd_a",
        id: expect.stringMatching(/^nd_/),
        type: "branch",
        // A branch without outputs is 96 high: its entry is at its middle.
        position: { x: 304, y: 256 },
      },
    });
    expect(connect).toEqual({
      type: "connection.create",
      payload: {
        id: expect.stringMatching(/^cn_/),
        from_node_id: "nd_1",
        from_port: "out",
        to_node_id: create.payload.id,
      },
    });
    await waitFor(() => expect(canvas.dataset.nodes).toBe("3"));
    await waitFor(() => expect(canvas.dataset.connections).toBe("1"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("leaves the focus where a press outside what a dropped connection offers took it, as to a field of the inspector, and gives the canvas the focus that the box had", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [testNode("nd_1", 0, 0), testNode("nd_2", 480, 0)],
    });
    const canvas = await openBoard("?node=nd_1");
    const inspector = await screen.findByRole("region", { name: "nd_1" });
    const handle = await linkHandle("new", "out");
    const offered = () =>
      screen.findByRole("dialog", {
        name: "Create a node here and connect it",
      });
    /** Waits until the box has gone, and for the turn after, in which it says where the focus goes. */
    const gone = async () => {
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    };
    // The box has the focus, which Escape gives the canvas, whose keys the
    // user goes on with.
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    expect((await offered()).contains(document.activeElement)).toBe(true);
    await user.keyboard("{Escape}");
    await gone();
    expect(document.activeElement).toBe(canvas);
    // A press on a field of the inspector ends the offer, and the field
    // keeps the focus that the press gave it, rather than the canvas, where
    // Backspace would delete the node selected.
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    await offered();
    const field = within(inspector).getByLabelText("Connect to");
    await user.click(field);
    await gone();
    expect(document.activeElement).toBe(field);
    expect(batches(calls)).toEqual([]);
  });

  it("offers a node no longer once another session deleted the node that the connection would leave", async () => {
    const realtime = fakeRealtime();
    const { calls } = flowProject({
      nodes: [testNode("nd_1", 0, 0), testNode("nd_2", 480, 0)],
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a?node=nd_1`, {
      session: testSession,
      realtime,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
    const handle = await linkHandle("new", "out");
    dragHandle(canvas, handle, { x: 240, y: 64 }, { x: 300, y: 300 });
    await screen.findByRole("dialog", {
      name: "Create a node here and connect it",
    });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.delete", payload: { id: "nd_1" } }],
        [{ id: "nd_1", rev: 2 }],
      ),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The canvas says so in the frame after.
    await waitFor(() => expect(canvas.dataset.nodes).toBe("1"));
    expect(batches(calls)).toEqual([]);
  });

  it("selects a connection by a click on its line, says so, reconnects it by a drag of its end's handle, and deletes it with Delete", async () => {
    const { calls } = flowProject({
      nodes: [
        testNode("nd_1", 0, 0),
        testNode("nd_2", 480, 0),
        testNode("nd_3", 480, 300),
      ],
      connections: [testConnection("cn_1", "nd_1", "nd_2", { rev: 4 })],
    });
    const canvas = await openBoard("?node=nd_1");
    clickAt(canvas, { x: 360, y: 64 });
    await waitFor(() => expect(canvas.dataset.connection).toBe("cn_1"));
    expect(canvas.dataset.selected).toBe("0");
    expect(await screen.findByText("A connection is selected.")).toBeDefined();
    // Its inspector takes the place of the node's.
    expect(
      await screen.findByRole("region", { name: "Connection" }),
    ).toBeDefined();
    const end = await linkHandle("to");
    // Onto the corner of the third node that the canvas shows.
    dragHandle(canvas, end, { x: 480, y: 64 }, { x: 500, y: 330 });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "connection.reconnect",
        payload: { id: "cn_1", to_node_id: "nd_3" },
        expected_rev: { cn_1: 4 },
      },
    ]);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Delete" });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1]).toEqual([
      {
        type: "connection.delete",
        payload: { id: "cn_1" },
        expected_rev: { cn_1: 5 },
      },
    ]);
    await waitFor(() => expect(canvas.dataset.connection).toBe(""));
    await waitFor(() => expect(canvas.dataset.connections).toBe("0"));
  });

  it("selects no connection, and shows no link handle, on a board that the user may not change", async () => {
    flowProject({
      nodes: [testNode("nd_1", 0, 0), testNode("nd_2", 480, 0)],
      connections: [testConnection("cn_1", "nd_1", "nd_2")],
    });
    vi.spyOn(testProject, "role", "get").mockReturnValue("viewer");
    const canvas = await openBoard("?node=nd_1");
    expect(canvas.dataset.selected).toBe("1");
    expect(document.querySelector("[data-link]")).toBeNull();
    clickAt(canvas, { x: 360, y: 64 });
    await waitFor(() => expect(canvas.dataset.selected).toBe("0"));
    expect(canvas.dataset.connection).toBe("");
  });
});

describe("the inspector of a board's flow", () => {
  // Verifies: FR-EDIT-03/AC2
  it("lists an element's choices in their order, reorders one by its button with one command of its key alone, and by a drag of its handle, and connects without a drag", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_1", 0, 0),
        testNode("nd_2", 480, 0, { title: "The dock" }),
        testNode("nd_3", 480, 300, { title: "The stair" }),
      ],
      connections: [
        testConnection("cn_1", "nd_1", "nd_2", { order: "V" }),
        testConnection("cn_2", "nd_1", "nd_3", { order: "k" }),
      ],
    });
    await openBoard("?node=nd_1");
    const inspector = await screen.findByRole("region", { name: "nd_1" });
    const list = await within(inspector).findByRole("list", {
      name: "Choices, in the order that the player is offered them",
    });
    const rows = () =>
      within(list)
        .getAllByRole("listitem")
        .map((li) => li.textContent);
    expect(rows()[0]).toContain("1. No label → The dock");
    expect(rows()[1]).toContain("2. No label → The stair");
    // The first cannot go up, nor the last down.
    expect(
      within(list).getByRole("button", { name: "Move choice 1 up" }),
    ).toHaveProperty("disabled", true);
    expect(
      within(list).getByRole("button", { name: "Move choice 2 down" }),
    ).toHaveProperty("disabled", true);
    await user.click(
      within(list).getByRole("button", { name: "Move choice 2 up" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[reorder]] = batches(calls);
    expect(reorder).toEqual({
      type: "connection.reorder",
      payload: { id: "cn_2", order: expect.any(String) },
      expected_rev: { cn_2: 1 },
    });
    expect((reorder.payload.order as string) < "V").toBe(true);
    await waitFor(() => expect(rows()[0]).toContain("1. No label → The stair"));
    // A drag of the first row's handle below the other row lands it last:
    // jsdom lays every row out at the window's top.
    const handle = list.querySelector("[data-drag]") as HTMLElement;
    fireEvent.pointerDown(handle, { ...mouse, buttons: 1, clientY: 0 });
    fireEvent.pointerMove(handle, { ...mouse, buttons: 1, clientY: 4 });
    // Within 8 px of its press, it is no drag yet.
    expect(list.querySelector(".opacity-60")).toBeNull();
    fireEvent.pointerMove(handle, { ...mouse, buttons: 1, clientY: 30 });
    expect(list.querySelector(".opacity-60")).not.toBeNull();
    fireEvent.pointerUp(handle, { ...mouse, buttons: 0, clientY: 30 });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1]).toEqual([
      {
        type: "connection.reorder",
        payload: { id: "cn_2", order: expect.any(String) },
        expected_rev: { cn_2: 2 },
      },
    ]);
    expect((batches(calls)[1][0].payload.order as string) > "V").toBe(true);
    await waitFor(() => expect(rows()[0]).toContain("1. No label → The dock"));
    // A drag that Escape ends moves nothing.
    fireEvent.pointerDown(handle, { ...mouse, buttons: 1, clientY: 0 });
    fireEvent.pointerMove(handle, { ...mouse, buttons: 1, clientY: 30 });
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.pointerUp(handle, { ...mouse, buttons: 0, clientY: 30 });
    // Without a drag, the list of the board's nodes connects.
    await user.selectOptions(
      within(inspector).getByLabelText("Connect to"),
      "nd_3",
    );
    await user.click(
      within(inspector).getByRole("button", { name: "Connect" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(3));
    expect(batches(calls)[2]).toEqual([
      {
        type: "connection.create",
        payload: {
          id: expect.stringMatching(/^cn_/),
          from_node_id: "nd_1",
          from_port: "out",
          to_node_id: "nd_3",
        },
      },
    ]);
    await waitFor(() => expect(rows()).toHaveLength(3));
  });

  it("moves the choice whose row's handle a drag holds, wherever the list has it by the drag's end, as once another change moved it", async () => {
    const { calls } = flowProject({
      nodes: [
        testNode("nd_1", 0, 0),
        testNode("nd_2", 480, 0, { title: "The dock" }),
        testNode("nd_3", 480, 300, { title: "The stair" }),
        testNode("nd_4", 480, 600, { title: "The lamp" }),
      ],
      connections: [
        testConnection("cn_1", "nd_1", "nd_2", { order: "V" }),
        testConnection("cn_2", "nd_1", "nd_3", { order: "k" }),
        testConnection("cn_3", "nd_1", "nd_4", { order: "t" }),
      ],
    });
    await openBoard("?node=nd_1");
    const inspector = await screen.findByRole("region", { name: "nd_1" });
    const list = await within(inspector).findByRole("list", {
      name: "Choices, in the order that the player is offered them",
    });
    const rows = () =>
      [...list.querySelectorAll<HTMLElement>("[data-choice]")].map(
        (li) => li.dataset.choice,
      );
    // The first row, held by a drag of its handle.
    const handle = list.querySelector(
      '[data-choice="cn_1"] [data-drag]',
    ) as HTMLElement;
    fireEvent.pointerDown(handle, { ...mouse, buttons: 1, clientY: 0 });
    fireEvent.pointerMove(handle, { ...mouse, buttons: 1, clientY: 30 });
    // The second choice goes above it meanwhile, so that the row held is
    // the second.
    fireEvent.click(
      within(list).getByRole("button", { name: "Move choice 2 up" }),
    );
    await waitFor(() => expect(rows()).toEqual(["cn_2", "cn_1", "cn_3"]));
    // Let go below every row, the choice held lands last, and no other.
    fireEvent.pointerUp(handle, { ...mouse, buttons: 0, clientY: 30 });
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1]).toEqual([
      {
        type: "connection.reorder",
        payload: { id: "cn_1", order: expect.any(String) },
        expected_rev: { cn_1: 1 },
      },
    ]);
    expect((batches(calls)[1][0].payload.order as string) > "t").toBe(true);
    await waitFor(() => expect(rows()).toEqual(["cn_2", "cn_3", "cn_1"]));
  });

  // Verifies: FR-EDIT-04/AC3
  it("adds a branch's outputs and its else, reorders them, and removes one, asking first where connections leave it, which the removal deletes in its command", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_b", 0, 0, { type: "branch", kind: undefined }),
        testNode("nd_2", 480, 0, { title: "The dock" }),
      ],
    });
    const canvas = await openBoard("?node=nd_b");
    const inspector = await screen.findByRole("region", { name: "nd_b" });
    const add = within(inspector).getByRole("button", {
      name: "Add an output",
    });
    const addElse = within(inspector).getByRole("button", {
      name: "Add the else",
    });
    // A branch has an else only once it has a conditional output.
    expect(addElse).toHaveProperty("disabled", true);
    await user.click(add);
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "branch_output.create",
        payload: { id: expect.stringMatching(/^bo_/), node_id: "nd_b" },
      },
    ]);
    await user.click(add);
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    await waitFor(() => expect(addElse).toHaveProperty("disabled", false));
    await user.click(addElse);
    await waitFor(() => expect(batches(calls)).toHaveLength(3));
    expect(batches(calls)[2][0].payload).toMatchObject({ else: true });
    await waitFor(() => expect(addElse).toHaveProperty("disabled", true));
    const first = String(batches(calls)[0][0].payload.id);
    const second = String(batches(calls)[1][0].payload.id);
    const outputs = () =>
      [...inspector.querySelectorAll<HTMLElement>("[data-output]")].map(
        (li) => li.dataset.output,
      );
    await waitFor(() => expect(outputs()).toHaveLength(3));
    expect(outputs().slice(0, 2)).toEqual([first, second]);
    const row = (id: string) =>
      inspector.querySelector(`[data-output="${id}"]`) as HTMLElement;
    // The else has no place among the conditional outputs.
    expect(within(row(outputs()[2] as string)).queryByText("↑")).toBeNull();
    // Each output has a link handle on the canvas once the branch is drawn
    // large enough for them.
    expect(document.querySelectorAll('[data-link="new"]').length).toBe(3);
    // Its buttons name it by its place in the list, and its row's words.
    await user.click(
      within(row(second)).getByRole("button", {
        name: "Move output 2 (else if) up",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(4));
    expect(batches(calls)[3]).toEqual([
      {
        type: "branch_output.reorder",
        payload: { id: second, order: expect.any(String) },
        expected_rev: { [second]: 1 },
      },
    ]);
    await waitFor(() => expect(outputs().slice(0, 2)).toEqual([second, first]));
    // An output without connections is removed at once.
    await user.click(
      within(row(first)).getByRole("button", {
        name: "Remove output 2 (else if)",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(5));
    expect(batches(calls)[4]).toEqual([
      {
        type: "branch_output.delete",
        payload: { id: first },
        expected_rev: { [first]: expect.any(Number) },
      },
    ]);
    await waitFor(() => expect(outputs()).toHaveLength(2));
    // The last conditional output stays while the branch has an else.
    expect(
      within(row(second)).getByRole("button", { name: "Remove output 1 (if)" }),
    ).toHaveProperty("disabled", true);
    // The else, connected, asks before it goes, with its connection.
    const other = outputs()[1] as string;
    await user.selectOptions(
      within(row(other)).getByLabelText("Connect to"),
      "nd_2",
    );
    await user.click(
      within(row(other)).getByRole("button", { name: "Connect" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(6));
    await waitFor(() => expect(canvas.dataset.connections).toBe("1"));
    await user.click(
      within(row(other)).getByRole("button", {
        name: "Remove output 2 (else)",
      }),
    );
    const confirm = await screen.findByRole("alertdialog", {
      name: "Remove the output?",
    });
    expect(
      within(confirm).getByText(
        /1 connection leaves this output, and is deleted with it\./,
      ),
    ).toBeDefined();
    expect(batches(calls)).toHaveLength(6);
    await user.click(
      within(confirm).getByRole("button", { name: "Remove the output" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(7));
    expect(batches(calls)[6]).toEqual([
      {
        type: "branch_output.delete",
        payload: { id: other, confirm: true },
        expected_rev: { [other]: 1 },
      },
    ]);
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    await waitFor(() => expect(canvas.dataset.connections).toBe("0"));
    await waitFor(() => expect(outputs()).toEqual([second]));
  });

  it("names the buttons of two outputs of the same condition apart, by their places", async () => {
    flowProject({
      nodes: [
        testNode("nd_b", 0, 0, {
          type: "branch",
          kind: undefined,
          outputs: [
            output("bo_1", "nd_b", "if", "A"),
            output("bo_2", "nd_b", "elseif", "B"),
            output("bo_3", "nd_b", "elseif", "C"),
          ],
        }),
      ],
    });
    await openBoard("?node=nd_b");
    const inspector = await screen.findByRole("region", { name: "nd_b" });
    const row = (id: string) =>
      inspector.querySelector(`[data-output="${id}"]`) as HTMLElement;
    for (const [id, place] of [
      ["bo_2", 2],
      ["bo_3", 3],
    ] as const) {
      for (const name of [
        `Move output ${place} (else if) up`,
        `Move output ${place} (else if) down`,
        `Remove output ${place} (else if)`,
      ]) {
        // One button of the inspector has the name, the row's.
        expect(within(inspector).getByRole("button", { name })).toBe(
          within(row(id)).getByRole("button", { name }),
        );
      }
    }
  });

  it("keeps the node chosen for a connection though another session deletes it meanwhile, rather than show, and save, another of the list in its place", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_1", 0, 0, { title: "The quay" }),
        testNode("nd_2", 480, 0, { title: "The dock" }),
        testNode("nd_3", 480, 300, { title: "The stair" }),
        testNode("nd_4", 480, 600, { title: "A cellar" }),
        testNode("nd_5", 480, 900, { title: "The lamp" }),
      ],
      connections: [testConnection("cn_1", "nd_1", "nd_2")],
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a?node=nd_1`, {
      session: testSession,
      realtime,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
    /** Deletes the node of id, as another session does. */
    const deleted = (id: string) =>
      act(() =>
        realtime.send(
          testProject.id,
          [{ type: "node.delete", payload: { id } }],
          [{ id, rev: 2 }],
        ),
      );
    // The node chosen to connect to, once it has gone, is chosen no longer:
    // the list shows that none is, and Connect sends nothing.
    const node = await screen.findByRole("region", { name: "The quay" });
    const connectTo = within(node).getByLabelText(
      "Connect to",
    ) as HTMLSelectElement;
    await user.selectOptions(connectTo, "nd_5");
    const connect = within(node).getByRole("button", { name: "Connect" });
    expect(connect).toHaveProperty("disabled", false);
    deleted("nd_5");
    await waitFor(() => expect(connect).toHaveProperty("disabled", true));
    expect(connectTo.value).toBe("");
    // The node chosen for the connection to enter stays what its form
    // shows, as one that was deleted, rather than the first of the list.
    await user.click(
      within(node).getByRole("button", { name: "Edit choice 1" }),
    );
    const inspector = await screen.findByRole("region", { name: "Connection" });
    const enters = () =>
      within(inspector).getByLabelText("Enters") as HTMLSelectElement;
    await user.selectOptions(enters(), "nd_3");
    deleted("nd_3");
    await waitFor(() => expect(canvas.dataset.nodes).toBe("3"));
    expect(enters().value).toBe("nd_3");
    expect(enters().selectedOptions[0].textContent).toBe(
      "A node that was deleted",
    );
    // Another chosen, Save sends that one.
    await user.selectOptions(enters(), "nd_4");
    await user.click(
      within(inspector).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "connection.reconnect",
        payload: { id: "cn_1", to_node_id: "nd_4" },
        expected_rev: { cn_1: 1 },
      },
    ]);
  });

  it("changes the connection selected in its inspector: its label, the node that it enters, and its place, and deletes it", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_1", 0, 0, { title: "The quay" }),
        testNode("nd_2", 480, 0, { title: "The dock" }),
        testNode("nd_3", 480, 300, { title: "The stair" }),
      ],
      connections: [
        testConnection("cn_1", "nd_1", "nd_2", { order: "V", rev: 2 }),
        testConnection("cn_2", "nd_1", "nd_3", { order: "k" }),
      ],
    });
    const canvas = await openBoard("?node=nd_1");
    const node = await screen.findByRole("region", { name: "The quay" });
    await user.click(
      await within(node).findByRole("button", { name: "Edit choice 1" }),
    );
    const inspector = await screen.findByRole("region", { name: "Connection" });
    expect(canvas.dataset.connection).toBe("cn_1");
    // The inspector's heading has the focus that the choice's button had,
    // which went with the node's form.
    expect(document.activeElement).toBe(
      within(inspector).getByRole("heading", { name: "Connection" }),
    );
    expect(within(inspector).getByText("The quay → The dock")).toBeDefined();
    expect(within(inspector).getByText("Choice 1 of 2")).toBeDefined();
    const label = within(inspector).getByRole("textbox", { name: "Label" });
    act(() => {
      (
        label as unknown as {
          editor: { commands: { insertContent: (text: string) => void } };
        }
      ).editor.commands.insertContent("Ask about the lighthouse");
    });
    await user.selectOptions(
      within(inspector).getByLabelText("Enters"),
      "nd_3",
    );
    await user.click(
      within(inspector).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "connection.update",
        payload: {
          id: "cn_1",
          label: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: "Ask about the lighthouse" }],
              },
            ],
          },
        },
        expected_rev: { cn_1: 2 },
      },
      {
        type: "connection.reconnect",
        payload: { id: "cn_1", to_node_id: "nd_3" },
        expected_rev: { cn_1: 2 },
      },
    ]);
    await user.click(
      within(inspector).getByRole("button", { name: "Move later" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1][0].type).toBe("connection.reorder");
    expect(await within(inspector).findByText("Choice 2 of 2")).toBeDefined();
    await user.click(
      within(inspector).getByRole("button", { name: "Delete the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(3));
    expect(batches(calls)[2][0]).toMatchObject({
      type: "connection.delete",
      payload: { id: "cn_1" },
    });
    await waitFor(() => expect(canvas.dataset.connection).toBe(""));
    // The heading has the focus that the button of the connection had.
    const emptied = await screen.findByRole("region", { name: "Inspector" });
    expect(document.activeElement).toBe(
      within(emptied).getByRole("heading", { name: "Inspector" }),
    );
  });
});

describe("the canvas's jumpers", () => {
  // Verifies: FR-EDIT-05/AC1
  it("draws a jumper whose target was deleted as broken, says so in its inspector, and offers no link to it", async () => {
    flowProject({
      nodes: [
        testNode("nd_1", 0, 0),
        testNode("nd_j", 480, 0, {
          type: "jumper",
          kind: undefined,
          title: "Back to the dock",
          target: { board_id: "bd_a", node_id: "nd_gone" },
        }),
      ],
    });
    const canvas = await openBoard("?node=nd_j");
    await waitFor(() => expect(canvas.dataset.broken).toBe("1"));
    const inspector = await screen.findByRole("region", {
      name: "Back to the dock",
    });
    expect(within(inspector).getByText("Target deleted")).toBeDefined();
    expect(screen.queryByRole("link", { name: "Go to target" })).toBeNull();
    // A jumper has no port that connections leave.
    expect(document.querySelector("[data-link]")).toBeNull();
  });

  // Verifies: FR-EDIT-05/AC1
  it("reads the target of a jumper to a board that the page has not opened, and draws the jumper as broken once that finds none", async () => {
    flowProject({
      nodes: [
        testNode("nd_j", 0, 0, {
          type: "jumper",
          kind: undefined,
          title: "To the lamp",
          target: { board_id: "bd_b", node_id: "nd_purged" },
        }),
      ],
    });
    const canvas = await openBoard("?node=nd_j");
    expect(canvas.dataset.broken).toBe("0");
    const inspector = await screen.findByRole("region", {
      name: "To the lamp",
    });
    expect(await within(inspector).findByText("Target deleted")).toBeDefined();
    await waitFor(() => expect(canvas.dataset.broken).toBe("1"));
    expect(screen.queryByRole("link", { name: "Go to target" })).toBeNull();
  });

  it("names a jumper's target on another board, and opens it on that board's canvas by one click of the jumper's link", async () => {
    const user = userEvent.setup();
    flowProject({
      nodes: [
        testNode("nd_j", 0, 0, {
          type: "jumper",
          kind: undefined,
          title: "To the lamp",
          target: { board_id: "bd_b", node_id: "nd_lamp" },
        }),
        testNode("nd_lamp", 0, 0, { board_id: "bd_b", title: "The lamp" }),
      ],
    });
    await openBoard("?node=nd_j");
    const inspector = await screen.findByRole("region", {
      name: "To the lamp",
    });
    expect(
      await within(inspector).findByText(/To The lamp on Lighthouse/),
    ).toBeDefined();
    // The link above the jumper's card, and the inspector's.
    const links = await screen.findAllByRole("link", { name: "Go to target" });
    expect(links).toHaveLength(2);
    const onCanvas = links.find((link) => !inspector.contains(link));
    expect(onCanvas?.getAttribute("href")).toContain(
      "/boards/bd_b?node=nd_lamp",
    );
    await user.click(onCanvas as HTMLElement);
    const there = await screen.findByRole("application", {
      name: "Nodes of Lighthouse",
    });
    await waitFor(() => expect(there.dataset.selected).toBe("1"));
  });

  it("shows the node that a jumper targets on a board that the page had not read, once it has read the board's nodes, and saves nothing of it unchanged", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_j", 0, 0, {
          type: "jumper",
          kind: undefined,
          title: "To the lamp",
          target: { board_id: "bd_b", node_id: "nd_lamp" },
        }),
        testNode("nd_lamp", 0, 0, { board_id: "bd_b", title: "The lamp" }),
        testNode("nd_stair", 0, 300, { board_id: "bd_b", title: "The stair" }),
      ],
    });
    await openBoard("?node=nd_j");
    const inspector = await screen.findByRole("region", {
      name: "To the lamp",
    });
    const list = () =>
      within(inspector).getByLabelText("Node") as HTMLSelectElement;
    await waitFor(() =>
      expect(
        within(list()).queryByRole("option", { name: "The stair (Narration)" }),
      ).not.toBeNull(),
    );
    // The list, whose options the read replaced, shows the target still.
    expect(list().value).toBe("nd_lamp");
    expect(list().selectedOptions[0]?.textContent).toBe("The lamp (Narration)");
    expect(
      (within(inspector).getByLabelText("Board") as HTMLSelectElement).value,
    ).toBe("bd_b");
    await user.click(
      within(inspector).getByRole("button", { name: "Save the target" }),
    );
    expect(
      await within(inspector).findByText(
        "Nothing has changed, so nothing was saved.",
      ),
    ).toBeDefined();
    expect(batches(calls)).toEqual([]);
    // Another node of the board, chosen, is the target that Save sends.
    await user.selectOptions(list(), "nd_stair");
    await user.click(
      within(inspector).getByRole("button", { name: "Save the target" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0][0].payload).toEqual({
      id: "nd_j",
      target: { board_id: "bd_b", node_id: "nd_stair" },
    });
  });

  it("reads no nodes of a board that the project no longer has, of a jumper whose target was on it", async () => {
    const { calls } = flowProject({
      nodes: [
        testNode("nd_j", 0, 0, {
          type: "jumper",
          kind: undefined,
          title: "To the wreck",
          target: { board_id: "bd_gone", node_id: "nd_wreck" },
        }),
      ],
    });
    const canvas = await openBoard("?node=nd_j");
    const inspector = await screen.findByRole("region", {
      name: "To the wreck",
    });
    expect(await within(inspector).findByText("Target deleted")).toBeDefined();
    await waitFor(() => expect(canvas.dataset.broken).toBe("1"));
    // The lists show what the target was, and nothing failed to be read.
    expect(
      (within(inspector).getByLabelText("Board") as HTMLSelectElement)
        .selectedOptions[0]?.textContent,
    ).toBe("A board that was deleted");
    expect(
      (within(inspector).getByLabelText("Node") as HTMLSelectElement)
        .selectedOptions[0]?.textContent,
    ).toBe("A node that was deleted");
    expect(
      within(inspector).queryByText("The board's nodes could not be read."),
    ).toBeNull();
    expect(
      calls.mock.calls.filter(
        ([operation, input]) =>
          operation === "listBoardNodes" &&
          (input as { path: { board_id: string } }).path.board_id === "bd_gone",
      ),
    ).toEqual([]);
  });

  it("opens a jumper's target on its own board by its link though the address names the target already, as once the link was followed before", async () => {
    const user = userEvent.setup();
    flowProject({
      nodes: [
        testNode("nd_dock", 0, 0, { title: "The dock" }),
        testNode("nd_j", 480, 0, {
          type: "jumper",
          kind: undefined,
          title: "Back to the dock",
          target: { board_id: "bd_a", node_id: "nd_dock" },
        }),
      ],
    });
    // The address names the target, as once its jumper's link was followed.
    const canvas = await openBoard("?node=nd_dock");
    await screen.findByRole("region", { name: "The dock" });
    const jumper = async () => {
      clickAt(canvas, { x: 600, y: 64 });
      return await screen.findByRole("region", { name: "Back to the dock" });
    };
    // The link above the jumper's card leads where the address is already,
    // and the canvas opens the target again.
    const first = await jumper();
    const links = await screen.findAllByRole("link", { name: "Go to target" });
    expect(links).toHaveLength(2);
    await user.click(
      links.find((link) => !first.contains(link)) as HTMLElement,
    );
    expect(
      await screen.findByRole("region", { name: "The dock" }),
    ).toBeDefined();
    expect(canvas.dataset.selected).toBe("1");
    // And so does the inspector's.
    const second = await jumper();
    await user.click(
      await within(second).findByRole("link", { name: "Go to target" }),
    );
    expect(
      await screen.findByRole("region", { name: "The dock" }),
    ).toBeDefined();
  });

  it("reads the jumpers that target a node that the page creates once the server has answered the batch that creates it", async () => {
    const user = userEvent.setup();
    let answer = () => {};
    const { calls } = flowProject({
      nodes: [testNode("nd_1", 0, 0, { title: "The quay" })],
      sent: new Promise<void>((resolve) => {
        answer = resolve;
      }),
    });
    await openBoard("?node=nd_1");
    await screen.findByRole("region", { name: "The quay" });
    const referrers = () =>
      calls.mock.calls
        .filter(([operation]) => operation === "listNodeReferrers")
        .map(
          ([, input]) => (input as { path: { node_id: string } }).path.node_id,
        );
    await waitFor(() => expect(referrers()).toEqual(["nd_1"]));
    const edit = screen.getByRole("group", { name: "Edit the board" });
    await user.click(within(edit).getByRole("button", { name: "Add" }));
    await user.click(await screen.findByRole("menuitem", { name: "Element" }));
    // The page shows the node at once, selected, before the server has it.
    const inspector = await screen.findByRole("region", { name: "Untitled" });
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const created = String(batches(calls)[0][0].payload.id);
    expect(
      within(
        within(inspector).getByRole("region", { name: "Referenced by" }),
      ).getByText("Reading…"),
    ).toBeDefined();
    expect(referrers()).toEqual(["nd_1"]);
    answer();
    expect(
      await within(inspector).findByText("No jumper goes to this node."),
    ).toBeDefined();
    expect(referrers()).toEqual(["nd_1", created]);
    expect(
      within(
        within(inspector).getByRole("region", { name: "Referenced by" }),
      ).queryByRole("alert"),
    ).toBeNull();
  });

  it("gives a jumper the target chosen in its inspector, a node of a board whose nodes it reads then, or none", async () => {
    const user = userEvent.setup();
    const { calls } = flowProject({
      nodes: [
        testNode("nd_j", 0, 0, { type: "jumper", kind: undefined, rev: 3 }),
        testNode("nd_lamp", 0, 0, { board_id: "bd_b", title: "The lamp" }),
      ],
    });
    await openBoard("?node=nd_j");
    const inspector = await screen.findByRole("region", { name: "nd_j" });
    // What the jumper's target is now, and the choice of none.
    expect(within(inspector).getAllByText("No target")).toHaveLength(2);
    // The form is shown anew once it has saved, with a button of its own.
    const save = () =>
      within(inspector).getByRole("button", { name: "Save the target" });
    await user.selectOptions(
      within(inspector).getByLabelText("Board"),
      "Lighthouse",
    );
    // Without a node, the form says to choose one, and sends nothing.
    await user.click(save());
    expect(
      await within(inspector).findByText(
        "Choose the node that the jumper goes to.",
      ),
    ).toBeDefined();
    expect(batches(calls)).toEqual([]);
    await waitFor(() =>
      expect(
        within(within(inspector).getByLabelText("Node")).queryByRole("option", {
          name: "The lamp (Narration)",
        }),
      ).not.toBeNull(),
    );
    await user.selectOptions(
      within(inspector).getByLabelText("Node"),
      "nd_lamp",
    );
    await user.click(save());
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.update",
        payload: {
          id: "nd_j",
          target: { board_id: "bd_b", node_id: "nd_lamp" },
        },
        expected_rev: { nd_j: 3 },
      },
    ]);
    expect(
      await within(inspector).findByText(/To The lamp on Lighthouse/),
    ).toBeDefined();
    await user.selectOptions(within(inspector).getByLabelText("Board"), "");
    await user.click(save());
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1][0].payload).toEqual({ id: "nd_j", target: {} });
  });

  it("lists the jumpers that target a node, each a link that opens it on its board", async () => {
    flowProject({
      nodes: [
        testNode("nd_dock", 0, 0, { title: "The dock" }),
        testNode("nd_j", 0, 0, {
          board_id: "bd_b",
          type: "jumper",
          kind: undefined,
          title: "Down to the dock",
          target: { board_id: "bd_a", node_id: "nd_dock" },
        }),
      ],
    });
    await openBoard("?node=nd_dock");
    const inspector = await screen.findByRole("region", { name: "The dock" });
    const list = await within(inspector).findByRole("list", {
      name: "Referenced by",
    });
    const link = within(list).getByRole("link", { name: "Down to the dock" });
    expect(link.getAttribute("href")).toContain("/boards/bd_b?node=nd_j");
    expect(within(list).getByText(/Lighthouse/)).toBeDefined();
  });
});
