import type { Board, Connection, Node } from "@storycreep/shared/api";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, startPage, testProject } from "../test/fake";
import { animationTime } from "./controller";
import type { Engine } from "./engine";
import { longPressTime } from "./gestures";

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
  // What a test gave the browser to vibrate with, which jsdom has not.
  Reflect.deleteProperty(navigator, "vibrate");
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
 * Answers the board's page, of a board of nodes and connections, and
 * applies each batch that the page sends, which leaves what it names at its
 * next revision, as the server does.
 */
function boardPage(nodes: Node[], connections: Connection[] = []) {
  let seq = testProject.seq;
  const revs = new Map<string, number>();
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, seq };
      case "listFolders":
      case "listComponentTypes":
      case "listComponents":
      case "listVariables":
        return { items: [], next_cursor: null };
      case "listBoardConnections":
        return { items: connections, next_cursor: null };
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
        const ids = commands.flatMap((command) => {
          const p = command.payload as { id?: string; ids?: string[] };
          return [...(p.id === undefined ? [] : [p.id]), ...(p.ids ?? [])];
        });
        for (const id of ids) {
          revs.set(id, (revs.get(id) ?? 1) + 1);
        }
        return {
          status: "applied",
          seq,
          revisions: ids.map((id) => ({ id, rev: revs.get(id) })),
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
const finger = { pointerId: 7, pointerType: "touch", button: 0 };

/** A board of two elements side by side, the first connected to the second, and a third under them. */
function quay() {
  const nodes = [
    testNode("nd_1", 0, 0, { title: "Harbour" }),
    testNode("nd_2", 400, 0, { title: "Quay" }),
    testNode("nd_3", 0, 400, { title: "Lighthouse" }),
  ];
  const connections = [testConnection("cn_1", "nd_1", "nd_2")];
  return { nodes, connections };
}

/** Returns the canvas's tree, once it lists the board's nodes. */
async function treeOf() {
  const tree = await screen.findByRole("tree", {
    name: "Nodes and connections of Harbour",
  });
  await waitFor(() =>
    expect(within(tree).getAllByRole("treeitem").length).toBeGreaterThan(0),
  );
  return tree;
}

/** Returns the names of the items that tree shows, in their order. */
function namesOf(tree: HTMLElement): (string | null)[] {
  return within(tree)
    .getAllByRole("treeitem")
    .map((item) => item.getAttribute("aria-label"));
}

/** Returns the live region of the toolbar that says what is selected. */
function said(): HTMLElement {
  const group = screen.getByRole("group", { name: "Canvas controls" });
  const region = group.querySelector<HTMLElement>("[aria-live=polite]");
  if (region === null) {
    throw new Error("the toolbar has no live region");
  }
  return region;
}

/** Returns what a screen reader says of region: its text, without what is hidden from it. */
function spoken(region: HTMLElement): string {
  const copy = region.cloneNode(true) as HTMLElement;
  for (const hidden of copy.querySelectorAll("[aria-hidden=true]")) {
    hidden.remove();
  }
  return (copy.textContent ?? "").trim();
}

describe("the canvas's accessibility tree", () => {
  // Verifies: NFR-A11Y-03
  it("exposes the board's nodes and connections as a tree of roles and names, after the canvas, with what is selected", async () => {
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    expect(namesOf(tree)).toEqual([
      "Harbour, Narration",
      "Quay, Narration",
      "Lighthouse, Narration",
    ]);
    expect(tree.getAttribute("aria-multiselectable")).toBe("true");
    // After the canvas, and one stop of the Tab key.
    expect(
      canvas.compareDocumentPosition(tree) &
        globalThis.Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    const items = within(tree).getAllByRole("treeitem");
    expect(items.filter((item) => item.tabIndex === 0)).toEqual([items[0]]);
    // A node that connections leave can be expanded, and one that none
    // leaves cannot.
    expect(items[0].getAttribute("aria-expanded")).toBe("false");
    expect(items[1].hasAttribute("aria-expanded")).toBe(false);
    expect(items.map((item) => item.getAttribute("aria-selected"))).toEqual([
      "false",
      "false",
      "false",
    ]);
    // A click on the canvas selects a node, which the tree tells.
    const second = screenOf(canvas, { x: 520, y: 64 });
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...second });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...second });
    await waitFor(() =>
      expect(items[1].getAttribute("aria-selected")).toBe("true"),
    );
    expect(items[0].getAttribute("aria-selected")).toBe("false");
  });

  // Verifies: NFR-A11Y-03
  it("says each change of the selection in a live region: the node selected and the ends of the connection selected by name, how many nodes, and that nothing is selected", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const region = said();
    expect(region.getAttribute("aria-atomic")).toBe("true");
    // Nothing is in it before the selection first changes, which a reader
    // might else say of a board that has only opened.
    expect(region.textContent).toBe("");
    const first = screenOf(canvas, { x: 120, y: 64 });
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...first });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...first });
    await waitFor(() =>
      expect(spoken(region)).toBe("Selected: Harbour, Narration."),
    );
    // What the page shows of it stays as it was.
    expect(within(region).getByText("1 node selected")).toBeTruthy();
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
    await waitFor(() => expect(spoken(region)).toBe("2 nodes selected"));
    // The connection, selected in the tree.
    const [harbourItem] = within(tree).getAllByRole("treeitem");
    harbourItem.focus();
    await user.keyboard("{ArrowRight}{ArrowRight} ");
    await waitFor(() =>
      expect(spoken(region)).toBe(
        "Selected: the connection from Harbour to Quay.",
      ),
    );
    await user.keyboard("{Escape}");
    await waitFor(() => expect(spoken(region)).toBe("Nothing is selected."));
  });

  // Verifies: NFR-A11Y-04
  it("moves through the nodes and their connections with the arrow keys, Home, End, and a letter typed, with the focus shown on the canvas, and selects with Space", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const [first, second, third] = within(tree).getAllByRole("treeitem");
    const ring = document.querySelector<HTMLElement>("[data-tree-focus]");
    expect(ring?.hidden).toBe(true);
    // The stop after the canvas.
    canvas.focus();
    await user.tab();
    expect(document.activeElement).toBe(first);
    await waitFor(() => expect(ring?.hidden).toBe(false));
    // The canvas's keys that zoom do so here.
    const zoom = canvas.dataset.zoom;
    await user.keyboard("+");
    await waitFor(() => expect(canvas.dataset.zoom).not.toBe(zoom));
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(second);
    expect(second.tabIndex).toBe(0);
    expect(first.tabIndex).toBe(-1);
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(third);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(third);
    await user.keyboard("{Home}");
    expect(document.activeElement).toBe(first);
    await user.keyboard("q");
    expect(document.activeElement).toBe(second);
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(first);
    // The arrows moved the focus, and neither the board nor the selection.
    expect(canvas.dataset.selected).toBe("0");
    // Right opens the node's connections, and goes into them; left goes
    // back out, and closes them.
    await user.keyboard("{ArrowRight}");
    expect(first.getAttribute("aria-expanded")).toBe("true");
    expect(namesOf(tree)).toEqual([
      "Harbour, Narration",
      "Connection to Quay",
      "Quay, Narration",
      "Lighthouse, Narration",
    ]);
    await user.keyboard("{ArrowRight}");
    const choice = within(tree).getByRole("treeitem", {
      name: "Connection to Quay",
    });
    expect(document.activeElement).toBe(choice);
    await user.keyboard(" ");
    await waitFor(() => expect(canvas.dataset.connection).toBe("cn_1"));
    expect(choice.getAttribute("aria-selected")).toBe("true");
    await user.keyboard("{ArrowLeft}");
    expect(document.activeElement).toBe(first);
    await user.keyboard("{ArrowLeft}");
    expect(first.getAttribute("aria-expanded")).toBe("false");
    expect(namesOf(tree)).toHaveLength(3);
    // Space selects the node in place of the selection, and with Shift
    // adds another, or takes it out.
    await user.keyboard(" ");
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    expect(canvas.dataset.connection).toBe("");
    expect(first.getAttribute("aria-selected")).toBe("true");
    await user.keyboard("{ArrowDown}{Shift>} {/Shift}");
    await waitFor(() => expect(canvas.dataset.selected).toBe("2"));
    await user.keyboard("{Shift>} {/Shift}");
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    expect(second.getAttribute("aria-selected")).toBe("false");
    // Escape clears the selection, as on the canvas.
    await user.keyboard("{Escape}");
    await waitFor(() => expect(canvas.dataset.selected).toBe("0"));
    // The focus leaves the tree, and the ring goes.
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(canvas);
    await waitFor(() => expect(ring?.hidden).toBe(true));
  });

  // Verifies: NFR-A11Y-04
  it("opens what the focus is on with Enter, and deletes it with Delete, after which the focus is on what is in its place", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    const calls = boardPage(nodes, connections);
    await openBoard();
    const tree = await treeOf();
    const [first, second] = within(tree).getAllByRole("treeitem");
    second.focus();
    await user.keyboard("{Enter}");
    const editor = await screen.findByRole("form", {
      name: "The text of Quay",
    });
    await user.click(within(editor).getByRole("button", { name: "Cancel" }));
    // Delete is of the node that the focus is on, though another is
    // selected.
    first.focus();
    await user.keyboard(" ");
    second.focus();
    await user.keyboard("{Delete}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.delete",
        payload: { id: "nd_2" },
        expected_rev: { nd_2: 1 },
      },
    ]);
    await waitFor(() =>
      expect(namesOf(tree)).toEqual([
        "Harbour, Narration",
        "Lighthouse, Narration",
      ]),
    );
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("aria-label")).toBe(
        "Lighthouse, Narration",
      ),
    );
  });

  // Verifies: NFR-A11Y-04
  it("has the focus, after a Delete of several nodes, on the node after the one that it was on, though nodes before it went with it", async () => {
    const user = userEvent.setup();
    const calls = boardPage([
      testNode("nd_1", 0, 0, { title: "Harbour" }),
      testNode("nd_2", 0, 400, { title: "Hall" }),
      testNode("nd_3", 0, 800, { title: "Quay" }),
      testNode("nd_4", 0, 1200, { title: "Dock" }),
      testNode("nd_5", 0, 1600, { title: "Lighthouse" }),
    ]);
    const canvas = await openBoard();
    const tree = await treeOf();
    const [first] = within(tree).getAllByRole("treeitem");
    first.focus();
    // The first three, selected one after another, down the tree, which
    // leaves the focus on the last of them.
    await user.keyboard(
      " {ArrowDown}{Shift>} {/Shift}{ArrowDown}{Shift>} {/Shift}",
    );
    await waitFor(() => expect(canvas.dataset.selected).toBe("3"));
    await user.keyboard("{Delete}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    await waitFor(() =>
      expect(namesOf(tree)).toEqual([
        "Dock, Narration",
        "Lighthouse, Narration",
      ]),
    );
    // Dock is in Quay's place, and not Lighthouse, two nodes on, where
    // Quay was among the five.
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("aria-label")).toBe(
        "Dock, Narration",
      ),
    );
    // And the node before, where none is after the one that went.
    await user.keyboard("{ArrowDown}{Delete}");
    await waitFor(() => expect(namesOf(tree)).toEqual(["Dock, Narration"]));
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("aria-label")).toBe(
        "Dock, Narration",
      ),
    );
  });

  it("has no tree of a board without nodes", async () => {
    boardPage([]);
    await openBoard();
    expect(screen.queryByRole("tree")).toBeNull();
  });

  it("tells of no connection selected, selects none, and opens no menu of one, on a board that may not be changed", async () => {
    const user = userEvent.setup();
    vi.spyOn(testProject, "role", "get").mockReturnValue("viewer");
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const [first] = within(tree).getAllByRole("treeitem");
    first.focus();
    await user.keyboard("{ArrowRight}{ArrowRight}");
    const choice = within(tree).getByRole("treeitem", {
      name: "Connection to Quay",
    });
    expect(document.activeElement).toBe(choice);
    expect(choice.hasAttribute("aria-selected")).toBe(false);
    await user.keyboard(" {Enter}");
    fireEvent.keyDown(choice, { key: "F10", shiftKey: true });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(canvas.dataset.connection).toBe("");
    expect(choice.hasAttribute("aria-selected")).toBe(false);
    expect(screen.queryByRole("menu")).toBeNull();
    // Nor does the browser's own menu open of the item, which the menu key
    // asks for as it goes up, where the focus is still.
    expect(fireEvent.contextMenu(choice)).toBe(false);
    // A node is selected there, as on the canvas.
    await user.keyboard("{ArrowLeft} ");
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    expect(first.getAttribute("aria-selected")).toBe("true");
  });

  // Verifies: NFR-A11Y-04
  it("goes by a letter typed to the next node whose title starts with it, by the same letter again to the one after it, and keeps none of the letters typed: Space right after one selects, and Backspace deletes", async () => {
    const user = userEvent.setup();
    const calls = boardPage([
      testNode("nd_1", 0, 0, { title: "Harbour" }),
      testNode("nd_2", 0, 400, { title: "Hall" }),
      testNode("nd_3", 0, 800, { title: "Quay" }),
      testNode("nd_4", 0, 1200, { title: "hut" }),
    ]);
    const canvas = await openBoard();
    const tree = await treeOf();
    const [first, second, third, fourth] =
      within(tree).getAllByRole("treeitem");
    first.focus();
    // The same letter, as fast as it is typed, goes on to the next node
    // that starts with it, whatever its case, and around the list's end.
    await user.keyboard("h");
    expect(document.activeElement).toBe(second);
    await user.keyboard("hh");
    expect(document.activeElement).toBe(first);
    // Another letter right after it starts anew, of a title that does not
    // start with the two.
    await user.keyboard("q");
    expect(document.activeElement).toBe(third);
    await user.keyboard("h");
    expect(document.activeElement).toBe(fourth);
    // A character that is a key of the canvas's is that key right after a
    // letter, as the one that zooms.
    const zoom = canvas.dataset.zoom;
    await user.keyboard("q+");
    expect(document.activeElement).toBe(third);
    await waitFor(() => expect(canvas.dataset.zoom).not.toBe(zoom));
    // Space right after a letter selects the node that the letter found.
    await user.keyboard("h ");
    expect(document.activeElement).toBe(fourth);
    await waitFor(() =>
      expect(fourth.getAttribute("aria-selected")).toBe("true"),
    );
    expect(canvas.dataset.selected).toBe("1");
    // And Backspace right after a letter deletes it.
    await user.keyboard("q{Backspace}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      { type: "node.delete", payload: { id: "nd_3" } },
    ]);
  });

  // Verifies: NFR-A11Y-03
  it("says the selection again as it changes to another node of the same name, which the same words would else say nothing of", async () => {
    const user = userEvent.setup();
    boardPage([
      testNode("nd_1", 0, 0, { title: "" }),
      testNode("nd_2", 0, 400, { title: "" }),
    ]);
    await openBoard();
    const tree = await treeOf();
    const [first, second] = within(tree).getAllByRole("treeitem");
    const region = said();
    first.focus();
    await user.keyboard(" ");
    await waitFor(() =>
      expect(spoken(region)).toBe("Selected: Untitled, Narration."),
    );
    // A live region is read as its content changes: what it holds is put
    // there anew for the selection that came after, in the same words.
    let changes = 0;
    const observer = new MutationObserver((records) => {
      changes += records.length;
    });
    observer.observe(region, {
      childList: true,
      characterData: true,
      subtree: true,
    });
    try {
      await user.keyboard("{ArrowDown} ");
      await waitFor(() =>
        expect(second.getAttribute("aria-selected")).toBe("true"),
      );
      expect(first.getAttribute("aria-selected")).toBe("false");
      await waitFor(() => expect(changes).toBeGreaterThan(0));
      expect(spoken(region)).toBe("Selected: Untitled, Narration.");
      // What was said stays as it is while the selection does.
      changes = 0;
      await user.keyboard("{ArrowUp}{ArrowDown}");
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(changes).toBe(0);
    } finally {
      observer.disconnect();
    }
  });

  it("names a node of a type that the page does not know by its title alone, and says it selected as one node, with no name of a type", async () => {
    const user = userEvent.setup();
    boardPage([
      testNode("nd_1", 0, 0, { title: "Harbour", type: "portal" as never }),
    ]);
    await openBoard();
    const tree = await treeOf();
    expect(namesOf(tree)).toEqual(["Harbour"]);
    within(tree).getByRole("treeitem").focus();
    await user.keyboard(" ");
    await waitFor(() => expect(spoken(said())).toBe("1 node selected"));
  });

  it("does nothing by Delete, Enter, or Space of an item whose node the board no longer has, as it would else to what was selected before", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    const calls = boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const [first] = within(tree).getAllByRole("treeitem");
    first.focus();
    await user.keyboard(" ");
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    // Items as the tree shows them of what another session deleted, in the
    // moment before it is made again.
    const gone = document.createElement("div");
    gone.setAttribute("role", "treeitem");
    gone.dataset.node = "nd_gone";
    const goneChoice = document.createElement("div");
    goneChoice.setAttribute("role", "treeitem");
    goneChoice.dataset.node = "nd_1";
    goneChoice.dataset.connection = "cn_gone";
    tree.append(gone, goneChoice);
    try {
      for (const item of [gone, goneChoice]) {
        fireEvent.keyDown(item, { key: "Delete" });
        fireEvent.keyDown(item, { key: "Enter" });
        fireEvent.keyDown(item, { key: " " });
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(batches(calls)).toHaveLength(0);
      expect(screen.queryByRole("form")).toBeNull();
      expect(canvas.dataset.selected).toBe("1");
      expect(first.getAttribute("aria-selected")).toBe("true");
    } finally {
      gone.remove();
      goneChoice.remove();
    }
  });

  it("takes no focus, and shows none on the canvas, in the tree of the node that a board gets after its last went by the tree's Delete", async () => {
    const user = userEvent.setup();
    const calls = boardPage([testNode("nd_1", 0, 0, { title: "Harbour" })]);
    await openBoard();
    const tree = await treeOf();
    within(tree).getByRole("treeitem").focus();
    await user.keyboard("{Delete}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("tree")).toBeNull());
    const edit = screen.getByRole("group", { name: "Edit the board" });
    const add = within(edit).getByRole("button", { name: "Add" });
    await user.click(add);
    await user.click(await screen.findByRole("menuitem", { name: "Element" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    const again = await treeOf();
    await waitFor(() => expect(said().hasAttribute("aria-busy")).toBe(false));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(again.contains(document.activeElement)).toBe(false);
    expect(
      document.querySelector<HTMLElement>("[data-tree-focus]")?.hidden,
    ).toBe(true);
  });

  // Verifies: NFR-A11Y-04
  it("shows the item that the menu key opens the menu of, where the canvas has moved away from it since the focus came to it", async () => {
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const third = within(tree).getByRole("treeitem", {
      name: "Lighthouse, Narration",
    });
    third.focus();
    const inView = () => {
      const at = screenOf(canvas, { x: 120, y: 464 });
      return (
        at.clientX >= 0 &&
        at.clientX <= 800 &&
        at.clientY >= 0 &&
        at.clientY <= 600
      );
    };
    await waitFor(() => expect(inView()).toBe(true));
    fireEvent.wheel(canvas, { deltaY: 6000 });
    await waitFor(() => expect(inView()).toBe(false));
    fireEvent.keyDown(third, { key: "ContextMenu" });
    await screen.findByRole("menu", { name: "Actions on the selected node" });
    await waitFor(() => expect(inView()).toBe(true));
  });
});

describe("the canvas's context menu", () => {
  // Verifies: NFR-TOUCH-02
  it("opens by a finger held for 400 ms on a node, which it selects, and does from it what the toolbar does, as deleting the node", async () => {
    const user = userEvent.setup();
    const vibrate = vi.fn(() => true);
    Object.defineProperty(navigator, "vibrate", {
      value: vibrate,
      configurable: true,
    });
    const { nodes, connections } = quay();
    const calls = boardPage(nodes, connections);
    const canvas = await openBoard();
    const at = screenOf(canvas, { x: 520, y: 64 });
    // A tap opens none.
    fireEvent.pointerDown(canvas, { ...finger, buttons: 1, ...at });
    fireEvent.pointerUp(canvas, { ...finger, buttons: 0, ...at });
    await new Promise((resolve) => setTimeout(resolve, longPressTime + 100));
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.keyDown(canvas, { key: "Escape" });
    await waitFor(() => expect(canvas.dataset.selected).toBe("0"));
    // A press held still does.
    fireEvent.pointerDown(canvas, { ...finger, buttons: 1, ...at });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the selected node",
    });
    // The canvas tells of its selection in the frame that draws it, which
    // may be after the page shows the menu.
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    expect(vibrate).toHaveBeenCalledTimes(1);
    fireEvent.pointerUp(canvas, { ...finger, buttons: 0, ...at });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual([
      "Edit the text",
      "Open the inspector",
      "Zoom to selection",
      "Copy",
      "Cut",
      "Duplicate",
      "Delete",
    ]);
    await user.click(within(menu).getByRole("menuitem", { name: "Delete" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.delete",
        payload: { id: "nd_2" },
        expected_rev: { nd_2: 1 },
      },
    ]);
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(canvas));
  });

  // Verifies: NFR-TOUCH-02
  it("opens by a right click too, of the selection that holds the node under it, and of the board where no node is, which adds a node there", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    const calls = boardPage(nodes, connections);
    const canvas = await openBoard();
    const first = screenOf(canvas, { x: 120, y: 64 });
    const second = screenOf(canvas, { x: 520, y: 64 });
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...first });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...first });
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
    fireEvent.contextMenu(canvas, { button: 2, ...second });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the 2 selected nodes",
    });
    expect(canvas.dataset.selected).toBe("2");
    // Of two nodes, there is no text to edit.
    expect(
      within(menu).queryByRole("menuitem", { name: "Edit the text" }),
    ).toBeNull();
    // Escape closes it, and the canvas has the focus again.
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(canvas));
    expect(canvas.dataset.selected).toBe("2");
    // On the empty board, the menu is the board's.
    const empty = { x: 800, y: 320 };
    fireEvent.contextMenu(canvas, { button: 2, ...screenOf(canvas, empty) });
    const board = await screen.findByRole("menu", {
      name: "Actions on the board here",
    });
    expect(
      within(board)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual([
      "Add an element here",
      "Add a branch here",
      "Add a jumper here",
      "Fit to content",
    ]);
    await user.click(
      within(board).getByRole("menuitem", { name: "Add a jumper here" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[create]] = batches(calls);
    expect(create.type).toBe("node.create");
    // A jumper is 240 by 72: its middle is where the menu was asked for.
    expect(create.payload).toMatchObject({
      type: "jumper",
      position: { x: 680, y: 288 },
    });
    // A click that is not the right button's, as a Ctrl+click on a Mac,
    // and the browser's own menu of a finger's long press, open none.
    fireEvent.contextMenu(canvas, { button: 0, ctrlKey: true, ...first });
    fireEvent(
      canvas,
      new PointerEvent("contextmenu", {
        bubbles: true,
        cancelable: true,
        button: 2,
        pointerType: "touch",
        ...first,
      }),
    );
    expect(screen.queryByRole("menu")).toBeNull();
  });

  // Verifies: NFR-A11Y-04
  it("opens by Shift+F10, and by the menu key, on the canvas, of what is selected, and in the tree, of what the focus is on, which takes the focus back", async () => {
    const user = userEvent.setup();
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "F10", shiftKey: true });
    const ofBoard = await screen.findByRole("menu", {
      name: "Actions on the board here",
    });
    // The menu that the keys open has the focus on its first item, which
    // Enter would choose.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(ofBoard).getAllByRole("menuitem")[0],
      ),
    );
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(canvas));
    // In the tree, of the node that the focus is on, which it selects.
    const third = within(tree).getByRole("treeitem", {
      name: "Lighthouse, Narration",
    });
    third.focus();
    fireEvent.keyDown(third, { key: "ContextMenu" });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the selected node",
    });
    expect(third.getAttribute("aria-selected")).toBe("true");
    // The inspector opens from it, with the focus.
    await user.click(
      within(menu).getByRole("menuitem", { name: "Open the inspector" }),
    );
    const inspector = await screen.findByRole("region", { name: "Lighthouse" });
    const heading = within(inspector).getByRole("heading", {
      name: "Lighthouse",
      level: 2,
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    // Of a connection, in the tree.
    const firstItem = within(tree).getByRole("treeitem", {
      name: "Harbour, Narration",
    });
    firstItem.focus();
    await user.keyboard("{ArrowRight}{ArrowRight}");
    const choice = within(tree).getByRole("treeitem", {
      name: "Connection to Quay",
    });
    fireEvent.keyDown(choice, { key: "F10", shiftKey: true });
    const ofConnection = await screen.findByRole("menu", {
      name: "Actions on the selected connection",
    });
    await waitFor(() => expect(canvas.dataset.connection).toBe("cn_1"));
    expect(
      within(ofConnection)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Open the inspector", "Delete"]);
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(ofConnection).getAllByRole("menuitem")[0],
      ),
    );
    await user.keyboard("{Escape}");
    await waitFor(() => expect(document.activeElement).toBe(choice));
  });

  it("has the focus on the menu itself, and on none of its items, where a pointer opened it", async () => {
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    // A key before it, as one that moved the canvas, and then the press of
    // the right button, which the menu's root hears as it goes down.
    fireEvent.keyDown(canvas, { key: "ArrowDown" });
    fireEvent.pointerDown(canvas, { ...mouse, button: 2, buttons: 2 });
    fireEvent.contextMenu(canvas, {
      button: 2,
      ...screenOf(canvas, { x: 120, y: 64 }),
    });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the selected node",
    });
    await waitFor(() => expect(document.activeElement).toBe(menu));
  });

  it("opens no menu by the keys, on the canvas or in the tree, while the canvas follows a pointer, as in the middle of a drag", async () => {
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const tree = await treeOf();
    const third = within(tree).getByRole("treeitem", {
      name: "Lighthouse, Narration",
    });
    const at = screenOf(canvas, { x: 120, y: 64 });
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...at });
    fireEvent.pointerMove(canvas, {
      ...mouse,
      buttons: 1,
      clientX: at.clientX + 40,
      clientY: at.clientY,
    });
    fireEvent.keyDown(canvas, { key: "F10", shiftKey: true });
    third.focus();
    fireEvent.keyDown(third, { key: "ContextMenu" });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.queryByRole("menu")).toBeNull();
    // The key selected nothing in place of what the drag moves.
    expect(third.getAttribute("aria-selected")).toBe("false");
    fireEvent.pointerUp(canvas, {
      ...mouse,
      buttons: 0,
      clientX: at.clientX + 40,
      clientY: at.clientY,
    });
    // Once the drag has ended, the key opens the menu.
    fireEvent.keyDown(third, { key: "ContextMenu" });
    await screen.findByRole("menu", { name: "Actions on the selected node" });
  });

  it("leaves the canvas where an item of the menu moved it, as Go to target, as the tree's item takes the focus back", async () => {
    const user = userEvent.setup();
    vi.spyOn(testProject, "role", "get").mockReturnValue("viewer");
    boardPage([
      testNode("nd_j", 0, 0, {
        type: "jumper",
        kind: undefined,
        title: "Back to the dock",
        target: { board_id: "bd_a", node_id: "nd_dock" },
      }),
      testNode("nd_dock", 4000, 3000, { title: "The dock" }),
    ]);
    // The address names the target, as once the jumper's link was followed.
    const canvas = await openBoard("?node=nd_dock");
    const shows = (p: { x: number; y: number }) => {
      const at = screenOf(canvas, p);
      return (
        at.clientX >= 0 &&
        at.clientX <= 800 &&
        at.clientY >= 0 &&
        at.clientY <= 600
      );
    };
    const tree = await treeOf();
    const jumper = within(tree).getAllByRole("treeitem")[0];
    expect(jumper.dataset.node).toBe("nd_j");
    jumper.focus();
    await waitFor(() => expect(shows({ x: 60, y: 30 })).toBe(true));
    expect(shows({ x: 4060, y: 3030 })).toBe(false);
    fireEvent.keyDown(jumper, { key: "ContextMenu" });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the selected node",
    });
    await user.click(
      within(menu).getByRole("menuitem", { name: "Go to target" }),
    );
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(jumper));
    // The canvas goes to the target, and stays on it, though the focus is
    // back on the jumper's item, which it is not shown again for.
    await waitFor(() => expect(shows({ x: 4060, y: 3030 })).toBe(true));
    await new Promise((resolve) => setTimeout(resolve, 2 * animationTime));
    expect(shows({ x: 4060, y: 3030 })).toBe(true);
    expect(shows({ x: 60, y: 30 })).toBe(false);
    // The next key of the tree that moves its focus shows its item again.
    await user.keyboard("{End}{Home}");
    await waitFor(() => expect(shows({ x: 60, y: 30 })).toBe(true));
  });

  it("closes by a press on the minimap, which moves the board, as by one on the board", async () => {
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const minimap = canvas.querySelector<HTMLCanvasElement>(
      "[aria-hidden=true] > canvas.cursor-pointer",
    );
    if (minimap === null) {
      throw new Error("the canvas has no minimap");
    }
    for (const pointer of [mouse, finger]) {
      fireEvent.contextMenu(canvas, {
        button: 2,
        ...screenOf(canvas, { x: 120, y: 64 }),
      });
      await screen.findByRole("menu", { name: "Actions on the selected node" });
      fireEvent.pointerDown(minimap, { ...pointer, buttons: 1 });
      fireEvent.pointerUp(minimap, { ...pointer, buttons: 0 });
      await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
      await waitFor(() => expect(document.activeElement).toBe(canvas));
    }
  });

  it("has only what changes nothing on a board that may not be changed", async () => {
    vi.spyOn(testProject, "role", "get").mockReturnValue("viewer");
    const { nodes, connections } = quay();
    boardPage(nodes, connections);
    const canvas = await openBoard();
    const first = screenOf(canvas, { x: 120, y: 64 });
    fireEvent.contextMenu(canvas, { button: 2, ...first });
    const menu = await screen.findByRole("menu", {
      name: "Actions on the selected node",
    });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Zoom to selection", "Copy"]);
    fireEvent.keyDown(menu, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    fireEvent.contextMenu(canvas, {
      button: 2,
      ...screenOf(canvas, { x: 800, y: 320 }),
    });
    const board = await screen.findByRole("menu", {
      name: "Actions on the board here",
    });
    expect(
      within(board)
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["Fit to content"]);
    // The browser's own menu does not open over the canvas's, as for the
    // menu key, which asks for it as it goes up, once the focus is here.
    expect(fireEvent.contextMenu(board)).toBe(false);
    fireEvent.keyDown(board, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    // Delete in the tree deletes nothing here, and selects nothing, and
    // neither does Enter, which opens nothing.
    const tree = await treeOf();
    const quayItem = within(tree).getByRole("treeitem", {
      name: "Quay, Narration",
    });
    quayItem.focus();
    fireEvent.keyDown(quayItem, { key: "Delete" });
    fireEvent.keyDown(quayItem, { key: "Enter" });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(quayItem.getAttribute("aria-selected")).toBe("false");
    expect(canvas.dataset.selected).toBe("1");
  });
});
