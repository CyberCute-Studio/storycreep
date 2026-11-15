import type {
  Attribute,
  Board,
  ComponentType,
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
import type { Engine, Frame } from "../canvas/engine";
import type { ComponentEntry } from "../document/doc";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import {
  expression,
  richText,
  testBoard,
  testVariable,
  text,
} from "../test/play";
import { fakeRealtime } from "../test/realtime";

// Play mode on a board's page (PRD FR-PLAY-01, plan note N78): started from
// the canvas, its panel beside it, which takes the choices, the canvas's
// highlights of where the flow is, the board's page following the flow to
// another board, its runtime errors, what it stands in for, a change of
// the draft while it plays, and its stop.

// The frames that the canvas drew, whose play marks a test reads: PixiJS
// needs WebGL or a 2D canvas, which jsdom has neither of, so the page's
// tests draw with an engine that draws nothing.
const frames: Frame[] = [];
vi.mock("../canvas/engine", () => ({
  createEngine: async (): Promise<Engine> => ({
    kind: "canvas",
    resize: () => {},
    render: (f) => {
      frames.push(f);
      return {
        lod: "detail",
        nodes: f.scene.nodes.size,
        visible: f.scene.nodes.size,
        texts: 0,
        connections: f.scene.connections.size,
      };
    },
    textures: () => undefined,
    onRestore: () => () => {},
    destroy: () => {},
  }),
}));

beforeEach(() => {
  frames.length = 0;
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

const id = (prefix: string, body: string) =>
  `${prefix}_${body.padStart(26, "0")}`;
const project = { ...testProject, id: id("prj", "P") };
const at = "2026-11-01T10:00:00Z";
const inProject = { project_id: project.id };
const harbour: Board = testBoard(id("bd", "1"), "Harbour", {
  ...inProject,
  entry_node_id: id("nd", "A"),
});
const elara: Board = testBoard(id("bd", "2"), "Elara", {
  ...inProject,
  entry_node_id: id("nd", "E"),
});
const gold = testVariable(id("var", "1"), "gold", "int", 3, inProject);
const trust: Attribute = {
  id: id("atr", "1"),
  component_type_id: id("ct", "1"),
  key: "trust",
  name: "Trust",
  type: "int",
  default: 0,
  runtime: true,
  order: "a",
  rev: 1,
  created_at: at,
  updated_at: at,
};
const character: ComponentType = {
  id: id("ct", "1"),
  ...inProject,
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [trust],
  rev: 1,
  created_at: at,
  updated_at: at,
};
const elaraComponent: ComponentEntry = {
  id: id("cmp", "1"),
  ...inProject,
  component_type_id: character.id,
  key: "elara",
  name: "Elara",
  order: "a",
  rev: 1,
  created_at: at,
  updated_at: at,
};

const onBoard = (b: Board) => ({ board_id: b.id, ...inProject });

const nodes: Node[] = [
  testNode(id("nd", "A"), 0, 0, {
    ...onBoard(harbour),
    kind: "dialogue",
    title: "Quay",
    speaker_component_id: elaraComponent.id,
    text: richText(
      text("Trust "),
      {
        type: "valueRef",
        attrs: { ref: elaraComponent.id, attribute: trust.id },
      },
      text("."),
    ),
    on_exit: expression("elara.trust += 1"),
  }),
  testNode(id("nd", "B"), 400, 0, {
    ...onBoard(harbour),
    title: "Wait",
    text: richText(text("You wait.")),
  }),
  {
    ...testNode(id("nd", "J"), 400, 200, onBoard(harbour)),
    title: "To Elara",
    type: "jumper",
    target: { board_id: elara.id, node_id: id("nd", "E") },
  } as Node,
  testNode(id("nd", "E"), 0, 0, {
    ...onBoard(elara),
    title: "Meeting",
    text: richText(
      text("Elara trusts you "),
      {
        type: "valueRef",
        attrs: { ref: elaraComponent.id, attribute: trust.id },
      },
      text("."),
    ),
  }),
  testNode(id("nd", "D"), 0, 300, {
    ...onBoard(harbour),
    title: "Ledger",
    on_enter: expression("gold = gold / 0"),
  }),
  testNode(id("nd", "X"), 0, 500, {
    ...onBoard(harbour),
    title: "Broken",
    on_enter: expression("gold +="),
  }),
  // A gate whose one choice the gold, 3, affords, which a value set by hand
  // can hide.
  testNode(id("nd", "G"), 800, 0, {
    ...onBoard(harbour),
    title: "Gate",
    text: richText(text("A gate.")),
  }),
];

const connections: Connection[] = [
  testConnection(id("cn", "1"), id("nd", "A"), id("nd", "B"), {
    ...onBoard(harbour),
    order: "b",
    label: richText(text("Wait here")),
  }),
  testConnection(id("cn", "2"), id("nd", "A"), id("nd", "J"), {
    ...onBoard(harbour),
    order: "a",
    label: richText(text("Visit Elara")),
  }),
  // A choice that the gold, 3, does not afford, which the flow hides.
  testConnection(id("cn", "3"), id("nd", "A"), id("nd", "B"), {
    ...onBoard(harbour),
    order: "c",
    label: richText(text("Pay the toll")),
    condition: expression("gold > 5"),
  }),
  testConnection(id("cn", "5"), id("nd", "G"), id("nd", "B"), {
    ...onBoard(harbour),
    label: richText(text("Go through")),
    condition: expression("gold < 5"),
  }),
];

/**
 * Answers the board's pages of the project, its boards those that boards
 * returns as they are read, and records what they ask; refuse throws the
 * refusal of an operation, if any, before it is answered.
 */
function playProject(
  boards: () => Board[] = () => [harbour, elara],
  refuse: (operation: string) => void = () => {},
) {
  return fakeApi((operation, input) => {
    refuse(operation);
    const path = (input as { path?: Record<string, string> } | undefined)?.path;
    switch (operation) {
      case "getProject":
        return project;
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listVariables":
        return { items: [gold], next_cursor: null };
      case "listComponentTypes":
        return { items: [character], next_cursor: null };
      case "listComponents":
        return { items: [elaraComponent], next_cursor: null };
      case "getComponent":
        return {
          ...elaraComponent,
          attributes: { [trust.id]: 1 },
          description: richText(),
        };
      case "listBoards":
        return { items: boards(), next_cursor: null };
      case "listBoardNodes":
        return {
          items: nodes.filter((n) => n.board_id === path?.board_id),
          next_cursor: null,
        };
      case "listBoardConnections":
        return {
          items: connections.filter((c) => c.board_id === path?.board_id),
          next_cursor: null,
        };
      case "listTags":
        return { items: [] };
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

/** The reads of the project's content that calls holds. */
function contentReads(calls: ReturnType<typeof playProject>): string[] {
  return calls.mock.calls
    .map(([operation]) => operation as string)
    .filter((op) => /^(list|get)(Board|Variable|Component|Folder)/.test(op));
}

async function openBoard(board: Board) {
  const canvas = await screen.findByRole("application", {
    name: `Nodes of ${board.name}`,
  });
  await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
  return canvas;
}

/** The play marks of the canvas's last frame. */
function lastMarks() {
  return frames.at(-1)?.play;
}

/** Makes the window as wide as width, as matchMedia tells the page, which resize changes as a tablet that turns does. */
function windowOf(width: number) {
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    get matches() {
      return query === "(min-width: 900px)" && width >= 900;
    },
    media: query,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  }));
  return {
    // The listeners that there were as the window changed, as a browser
    // tells them, rather than one that a listener adds anew meanwhile, as
    // the canvas's of the screen's density does.
    resize: (next: number) => {
      width = next;
      for (const listener of [...listeners]) {
        listener();
      }
    },
  };
}

describe("play mode", () => {
  // Verifies: FR-PLAY-01/AC3
  it("plays from the board's entry node by P, beside the canvas, which highlights where the flow is, and follows a jumper to the other board's page", async () => {
    const user = userEvent.setup();
    playProject();
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    expect(within(panel).getByText("Speaker: Elara")).toBeDefined();
    // The current node's heading has the focus, which says its speaker and
    // its text as its description.
    const quay = within(panel).getByRole("heading", { name: "Quay" });
    await waitFor(() => expect(document.activeElement).toBe(quay));
    expect(
      (quay.getAttribute("aria-describedby") ?? "")
        .split(" ")
        .map((d) => document.getElementById(d)?.textContent),
    ).toEqual(["Speaker: Elara", "Trust 1."]);
    // The choices in their author's order.
    expect(
      within(within(panel).getByRole("region", { name: "Choices" }))
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["Visit Elara", "Wait here"]);
    await waitFor(() =>
      expect(lastMarks()).toMatchObject({ current: id("nd", "A") }),
    );
    const tree = screen.getByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    expect(
      within(tree)
        .getAllByRole("treeitem")
        .find((i) => i.getAttribute("aria-current") === "step")
        ?.getAttribute("data-node"),
    ).toBe(id("nd", "A"));
    // The jumper's target is on the other board, whose page the flow goes
    // to, with Elara's trust that the harbour's on-exit raised.
    await user.click(
      within(panel).getByRole("button", { name: "Visit Elara" }),
    );
    await openBoard(elara);
    const there = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(there).getByText("Elara trusts you 2.")).toBeDefined(),
    );
    await waitFor(() =>
      expect(lastMarks()).toMatchObject({ current: id("nd", "E") }),
    );
    expect(lastMarks()?.visited.has(id("nd", "A"))).toBe(true);
    // The page that the flow went to gives its node's heading the focus.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(there).getByRole("heading", { name: "Meeting" }),
      ),
    );
    expect(
      within(
        within(there).getByRole("region", { name: "Where the flow is" }),
      ).getByText(
        "The flow ended here, at a dead end: no connection leads on.",
      ),
    ).toBeDefined();
  });

  // The page that the flow goes to draws with the renderer that the page
  // that it left was asked to, as by the reference tablet's checklist.
  it("keeps the renderer asked for on the board's page that the flow goes to", async () => {
    const user = userEvent.setup();
    playProject();
    const { router } = renderApp(
      `/projects/${project.id}/boards/${harbour.id}?renderer=canvas`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await user.click(
      await within(panel).findByRole("button", { name: "Visit Elara" }),
    );
    await openBoard(elara);
    expect(router.state.location.search).toEqual({ renderer: "canvas" });
  });

  // A board that the page no longer holds, as one that a collaborator
  // deleted while playing, which the snapshot still holds, has no page to
  // open: the page shown stays, and its panel says where the flow is, as
  // the snapshot names it, with the focus on the current node's heading.
  it("stays on the board's page where the flow goes to a board that the page no longer holds", async () => {
    const user = userEvent.setup();
    let boards = [harbour, elara];
    playProject(() => boards);
    const realtime = fakeRealtime();
    const { router } = renderApp(
      `/projects/${project.id}/boards/${harbour.id}`,
      { session: testSession, realtime },
    );
    const canvas = await openBoard(harbour);
    await waitFor(() => expect(realtime.subscribed(project.id)).toBe(true));
    act(() => realtime.hold(project.id));
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    boards = [harbour];
    act(() =>
      realtime.send(
        project.id,
        [{ type: "board.delete", payload: { id: elara.id } }],
        [{ id: elara.id, rev: 2 }],
      ),
    );
    await within(panel).findByText(
      "The draft has changed since play started. Restart to play it as it is now.",
    );
    await user.click(
      within(panel).getByRole("button", { name: "Visit Elara" }),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(panel).getByRole("heading", { name: "Meeting" }),
      ),
    );
    expect(panel.isConnected).toBe(true);
    expect(within(panel).getByText("Elara trusts you 2.")).toBeDefined();
    expect(within(panel).getByText("Elara")).toBeDefined();
    expect(router.state.location.pathname).toBe(
      `/projects/${project.id}/boards/${harbour.id}`,
    );
    expect(
      screen.getByRole("application", { name: "Nodes of Harbour" }),
    ).toBeDefined();
  });

  // Below 900 px the panel is a sheet over the canvas, which waits while
  // the editor of a text is open, as the inspector's sheet closes to show
  // it, rather than hold the editor inert, as once a tablet turned while
  // the inspector was open beside the canvas.
  it("shows the editor of a text that the inspector's sheet asked for while playing, and the play sheet once the editor closes", async () => {
    const size = windowOf(1200);
    const user = userEvent.setup();
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "B")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await screen.findByRole("region", { name: "Wait" });
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await within(panel).findByText("You wait.");
    const edit = screen.getByRole("group", { name: "Edit the board" });
    await user.click(within(edit).getByRole("button", { name: "Inspector" }));
    await screen.findByRole("region", { name: "Wait" });
    expect(screen.queryByRole("region", { name: "Play" })).toBeNull();
    act(() => size.resize(800));
    const sheet = await screen.findByRole("dialog", { name: "Wait" });
    await user.click(
      within(sheet).getByRole("button", { name: "Edit the text" }),
    );
    const editor = await screen.findByRole("form", {
      name: "The text of Wait",
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Wait" })).toBeNull(),
    );
    expect(screen.queryByRole("dialog", { name: "Play" })).toBeNull();
    expect(editor.closest("[inert]")).toBeNull();
    await user.click(within(editor).getByRole("button", { name: "Cancel" }));
    const play = await screen.findByRole("dialog", { name: "Play" });
    expect(within(play).getByText("You wait.")).toBeDefined();
  });

  // The editor of a text holds what the user typed, which the board's page
  // does not take away unsaved as it follows the flow to another board's
  // page: it waits for the editor to close, while the panel gives the
  // current node's heading the focus, and then follows the flow.
  it("waits for the editor of a text to close before it follows the flow to another board's page", async () => {
    const user = userEvent.setup();
    playProject();
    const { router } = renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "A")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await screen.findByRole("region", { name: "Quay" });
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await within(panel).findByText("Trust 1.");
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Enter" });
    const editor = await screen.findByRole("form", {
      name: "The text of Quay",
    });
    const textbox = within(editor).getByRole("textbox", {
      name: "Text of Quay",
    });
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    act(() => {
      (
        textbox as unknown as {
          editor: { commands: { insertContent: (text: string) => void } };
        }
      ).editor.commands.insertContent("Typed. ");
    });
    await user.click(
      within(panel).getByRole("button", { name: "Visit Elara" }),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(panel).getByRole("heading", { name: "Meeting" }),
      ),
    );
    expect(router.state.location.pathname).toBe(
      `/projects/${project.id}/boards/${harbour.id}`,
    );
    expect(editor.isConnected).toBe(true);
    expect(textbox.textContent).toContain("Typed.");
    await user.click(within(editor).getByRole("button", { name: "Cancel" }));
    await openBoard(elara);
    const there = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(there).getByRole("heading", { name: "Meeting" }),
      ),
    );
  });

  // A tablet's turn that makes room for the panel beside the canvas, while
  // its sheet waits for the editor of a text, leaves the focus in the text.
  it("leaves the focus in the editor of a text as the window grows wide enough for the panel beside the canvas", async () => {
    const size = windowOf(800);
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "B")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Enter" });
    const editor = await screen.findByRole("form", {
      name: "The text of Wait",
    });
    const textbox = within(editor).getByRole("textbox", {
      name: "Text of Wait",
    });
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    await waitFor(() =>
      expect(lastMarks()).toMatchObject({ current: id("nd", "B") }),
    );
    expect(screen.queryByRole("dialog", { name: "Play" })).toBeNull();
    fireEvent.keyDown(canvas, { key: "Enter" });
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    act(() => size.resize(1200));
    const panel = await screen.findByRole("region", { name: "Play" });
    expect(within(panel).getByText("You wait.")).toBeDefined();
    expect(document.activeElement).toBe(textbox);
  });

  // A read that the server refused says why, as that the account made too
  // many reads in a minute, and when Try again can read on.
  it("says why a read of the project failed, and reads on with Try again", async () => {
    const user = userEvent.setup();
    let limited = true;
    playProject(undefined, (operation) => {
      if (operation === "getComponent" && limited) {
        throw refused(429, { code: "rate_limited" }, { "Retry-After": "30" });
      }
    });
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    expect((await within(panel).findByRole("alert")).textContent).toBe(
      "Play mode could not read the project's draft. Too many requests. Try again in 30 seconds.",
    );
    limited = false;
    await user.click(within(panel).getByRole("button", { name: "Try again" }));
    await within(panel).findByText("Trust 1.");
  });

  // Play reads the project's content as it starts, and nothing for a step.
  // Verifies: FR-PLAY-01/AC1
  it("takes a step in the page, with no request to the server", async () => {
    const user = userEvent.setup();
    const calls = playProject();
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    expect(contentReads(calls)).toContain("getComponent");
    const before = calls.mock.calls.length;
    await user.click(within(panel).getByRole("button", { name: "Wait here" }));
    await waitFor(() =>
      expect(within(panel).getByText("You wait.")).toBeDefined(),
    );
    expect(calls.mock.calls.slice(before)).toEqual([]);
    // The choice pressed went with the node that it left; the focus is on
    // the node that it took the flow to.
    expect(document.activeElement).toBe(
      within(panel).getByRole("heading", { name: "Wait" }),
    );
  });

  it("plays from a node from its context menu, and stops by Shift and P, giving the canvas the focus back", async () => {
    const user = userEvent.setup();
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "B")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "F10", shiftKey: true });
    const menu = await screen.findByRole("menu");
    await user.click(
      within(menu).getByRole("menuitem", { name: "Play from here" }),
    );
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("You wait.")).toBeDefined(),
    );
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "P", code: "KeyP", shiftKey: true });
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Play" })).toBeNull(),
    );
    // The canvas draws its next frame without the marks.
    await waitFor(() => expect(lastMarks()).toBeUndefined());
  });

  it("lists a runtime error with where it is and its expression's source, and halts there", async () => {
    const user = userEvent.setup();
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "D")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    const errors = await within(panel).findByRole("heading", {
      name: "1 runtime error",
    });
    const item = within(errors.parentElement as HTMLElement).getByRole(
      "listitem",
    );
    expect(item.textContent).toContain(
      "A division by zero. The expression gave its type's zero value.",
    );
    expect(item.textContent).toContain("gold = gold / 0");
    expect(
      within(item).getByRole("link", {
        name: "Ledger on Harbour, its on-enter script",
      }),
    ).toBeDefined();
    // It says each way on, Step back and Reset among them, rather than
    // Restart alone, which reads the draft anew and loses the history.
    expect(within(panel).getByRole("alert").textContent).toContain(
      "Play stopped at a runtime error, as strict mode does. Step back, reset, or restart to play again.",
    );
    // What the projection stood in for, the script that does not compile.
    await user.click(
      within(panel).getByText(
        "Play mode stands in for 1 thing that a published snapshot cannot hold",
      ),
    );
    expect(
      within(panel).getByRole("link", {
        name: "The on-enter script of Broken on Harbour does not compile, so it does nothing.",
      }),
    ).toBeDefined();
  });

  it("says once the draft changed while it played, and restarts on the draft as it is", async () => {
    const user = userEvent.setup();
    playProject();
    const realtime = fakeRealtime();
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
      realtime,
    });
    const canvas = await openBoard(harbour);
    await waitFor(() => expect(realtime.subscribed(project.id)).toBe(true));
    act(() => realtime.hold(project.id));
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    act(() =>
      realtime.send(
        project.id,
        [
          {
            type: "node.update",
            payload: {
              id: id("nd", "A"),
              title: "Pier",
              text: richText(text("Changed.")),
            },
          },
        ],
        [{ id: id("nd", "A"), rev: 2 }],
      ),
    );
    await waitFor(() =>
      expect(
        within(panel).getByText(
          "The draft has changed since play started. Restart to play it as it is now.",
        ),
      ).toBeDefined(),
    );
    // The current node is named as the page names it, as where an error or
    // a stand-in is, and its text is the snapshot's until Restart.
    expect(within(panel).getByRole("heading", { name: "Pier" })).toBeDefined();
    expect(within(panel).getByText("Trust 1.")).toBeDefined();
    await user.click(within(panel).getByRole("button", { name: "Restart" }));
    await waitFor(() =>
      expect(within(panel).getByText("Changed.")).toBeDefined(),
    );
    // Restart kept the focus in the panel, on the node that play started at.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(panel).getByRole("heading", { name: "Pier" }),
      ),
    );
  });

  // P plays from the one node selected, or with nothing selected from the
  // board's entry node, and not while a connection is selected.
  it("does not play by P while a connection is selected", async () => {
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "A")}&connection=${id("cn", "1")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await screen.findByRole("region", { name: "Connection" });
    expect(canvas.dataset.connection).toBe(id("cn", "1"));
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    expect(screen.queryByRole("region", { name: "Play" })).toBeNull();
    expect(screen.getByRole("region", { name: "Connection" })).toBeDefined();
  });
});

// The variable inspector, the author view of hidden choices, and the
// history, in play's panel (PRD FR-PLAY-02, FR-PLAY-03, FR-PLAY-04, plan
// note N79).
describe("play mode's state", () => {
  // startPlay plays the harbour from its entry node by P, and returns the
  // panel once it shows the entry node.
  async function startPlay() {
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    return panel;
  }

  // The labels of the choices' buttons, in their order.
  const choicesOf = (panel: HTMLElement) =>
    within(within(panel).getByRole("region", { name: "Choices" }))
      .getAllByRole("button")
      .map((b) => b.textContent);

  // Verifies: FR-PLAY-02/AC1, FR-PLAY-03/AC1, FR-PLAY-03/AC2
  it("sets a value in the inspector, which the choices read at once, and shows a hidden choice with the part of its condition that failed", async () => {
    const user = userEvent.setup();
    playProject();
    const panel = await startPlay();
    // Unticked, the hidden choice is not listed at all, as the runtime
    // offers it to no player.
    expect(choicesOf(panel)).toEqual(["Visit Elara", "Wait here"]);
    expect(within(panel).queryByText("Pay the toll")).toBeNull();
    await user.click(
      within(panel).getByRole("checkbox", { name: "Show hidden choices" }),
    );
    const hidden = within(panel).getByRole("region", {
      name: "1 hidden choice",
    });
    expect(within(hidden).getByText("Pay the toll")).toBeDefined();
    expect(within(hidden).getByText("Its condition is false:")).toBeDefined();
    expect(within(hidden).getAllByText("gold > 5")).toHaveLength(2);
    expect(within(hidden).getByText("gold is 3")).toBeDefined();
    // The choices that a player has are the same either way.
    expect(choicesOf(panel)).toEqual(["Visit Elara", "Wait here"]);
    const field = within(panel).getByRole("textbox", { name: "gold" });
    await user.clear(field);
    await user.type(field, "7{Enter}");
    await waitFor(() =>
      expect(choicesOf(panel)).toEqual([
        "Visit Elara",
        "Wait here",
        "Pay the toll",
      ]),
    );
    expect(within(panel).getByText("No choice here is hidden.")).toBeDefined();
    expect((field as HTMLInputElement).value).toBe("7");
    expect(document.activeElement).toBe(field);
  });

  // Verifies: FR-PLAY-02/AC3
  it("takes no value that is none of the type, and says why", async () => {
    const user = userEvent.setup();
    playProject();
    const panel = await startPlay();
    const field = within(panel).getByRole("textbox", { name: "gold" });
    await user.clear(field);
    await user.type(field, "1.5{Enter}");
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(
      within(panel).getByText("Enter a whole number, such as 3 or -12."),
    ).toBeDefined();
    // The flow did not take it: the toll's condition reads 3 still.
    await user.click(
      within(panel).getByRole("checkbox", { name: "Show hidden choices" }),
    );
    expect(within(panel).getByText("gold is 3")).toBeDefined();
    // Escape goes back to the value, and leaves the panel open.
    field.focus();
    await user.keyboard("{Escape}");
    expect((field as HTMLInputElement).value).toBe("3");
    expect(field.getAttribute("aria-invalid")).toBeNull();
    expect(screen.getByRole("region", { name: "Play" })).toBe(panel);
  });

  // Verifies: FR-PLAY-02/AC2
  it("marks the values that the last step changed, and filters them by name", async () => {
    const user = userEvent.setup();
    playProject();
    const panel = await startPlay();
    const values = within(panel).getByRole("region", { name: "Values" });
    const trustField = within(values).getByRole("textbox", {
      name: "elara.trust",
    });
    expect(trustField.getAttribute("aria-describedby")).not.toBeNull();
    const description = (f: HTMLElement) =>
      (f.getAttribute("aria-describedby") ?? "")
        .split(" ")
        .map((d) => document.getElementById(d)?.textContent)
        .join(" ");
    expect(description(trustField)).not.toContain("Changed by the last step");
    await user.click(within(panel).getByRole("button", { name: "Wait here" }));
    await waitFor(() =>
      expect(within(panel).getByText("You wait.")).toBeDefined(),
    );
    // The quay's on-exit raised Elara's trust, and nothing else.
    const raised = within(values).getByRole("textbox", { name: "elara.trust" });
    expect((raised as HTMLInputElement).value).toBe("2");
    expect(description(raised)).toContain("Changed by the last step");
    expect(
      description(within(values).getByRole("textbox", { name: "gold" })),
    ).not.toContain("Changed by the last step");
    expect(within(values).getByText("Global variables")).toBeDefined();
    expect(
      within(values).getByText("Runtime attributes of Elara"),
    ).toBeDefined();
    await user.type(
      within(values).getByRole("searchbox", { name: "Filter the values" }),
      "trust",
    );
    expect(within(values).queryByRole("textbox", { name: "gold" })).toBeNull();
    expect(
      within(values).getByRole("textbox", { name: "elara.trust" }),
    ).toBeDefined();
    await user.type(
      within(values).getByRole("searchbox", { name: "Filter the values" }),
      "zzz",
    );
    expect(
      within(values).getByText("No value matches the filter."),
    ).toBeDefined();
  });

  // Verifies: FR-PLAY-04/AC1
  it("lists the history, steps back to the state before the last step, a value set by hand among it, and resets", async () => {
    const user = userEvent.setup();
    playProject();
    const panel = await startPlay();
    const stepBack = within(panel).getByRole("button", { name: "Step back" });
    expect((stepBack as HTMLButtonElement).disabled).toBe(true);
    const field = within(panel).getByRole("textbox", { name: "gold" });
    await user.clear(field);
    await user.type(field, "8{Enter}");
    await user.click(within(panel).getByRole("button", { name: "Wait here" }));
    await waitFor(() =>
      expect(within(panel).getByText("You wait.")).toBeDefined(),
    );
    const history = within(panel).getByRole("region", { name: "History" });
    const steps = () =>
      within(history)
        .getAllByRole("listitem")
        .filter((li) => li.parentElement?.tagName === "OL");
    expect(steps()).toHaveLength(2);
    expect(within(steps()[0]).getByText("Started")).toBeDefined();
    expect(
      within(steps()[0]).getByText("Came to Quay on Harbour"),
    ).toBeDefined();
    expect(within(steps()[0]).getByText("Set gold to 8 by hand")).toBeDefined();
    expect(within(steps()[1]).getByText("Chose “Wait here”")).toBeDefined();
    await user.click(stepBack);
    await waitFor(() =>
      expect(within(panel).getByText("Trust 1.")).toBeDefined(),
    );
    expect(steps()).toHaveLength(1);
    expect(
      (within(panel).getByRole("textbox", { name: "gold" }) as HTMLInputElement)
        .value,
    ).toBe("8");
    expect((stepBack as HTMLButtonElement).disabled).toBe(true);
    // The current node's heading has the focus after the step back, as
    // after a choice.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(panel).getByRole("heading", { name: "Quay" }),
      ),
    );
    await user.click(within(panel).getByRole("button", { name: "Reset" }));
    await waitFor(() =>
      expect(
        (
          within(panel).getByRole("textbox", {
            name: "gold",
          }) as HTMLInputElement
        ).value,
      ).toBe("3"),
    );
    expect(within(steps()[0]).queryByText("Set gold to 8 by hand")).toBeNull();
  });

  // Verifies: FR-PLAY-04/AC2
  it("copies the history as text for a bug report", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn(async (_: string) => {});
    vi.stubGlobal("isSecureContext", true);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    try {
      playProject();
      const panel = await startPlay();
      await user.click(
        within(panel).getByRole("button", { name: "Wait here" }),
      );
      await waitFor(() =>
        expect(within(panel).getByText("You wait.")).toBeDefined(),
      );
      await user.click(
        within(panel).getByRole("button", { name: "Copy as text" }),
      );
      await waitFor(() =>
        expect(
          within(panel).getByText("Copied the history as text."),
        ).toBeDefined(),
      );
      expect(writeText.mock.calls[0]?.[0]).toBe(
        [
          `Play history of ${project.name}`,
          "",
          "1. Started",
          "   Came to Quay on Harbour",
          "2. Chose “Wait here”",
          "   Came to Wait on Harbour",
          "   The flow ended here, at a dead end: no connection leads on.",
          "",
          "Values now:",
          "   Global variables",
          "      gold = 3",
          "   Runtime attributes of Elara",
          "      elara.trust = 2",
          "",
        ].join("\n"),
      );
    } finally {
      Reflect.deleteProperty(navigator, "clipboard");
    }
  });

  // A value set by hand that hides every choice of the current node leaves
  // the flow awaiting a choice of none, which the panel says under the
  // node, with how to go on, rather than showing nothing; a value set after
  // it offers the choice again.
  it("says why the flow offers no choice once a value set by hand hides every one", async () => {
    const user = userEvent.setup();
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "G")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await screen.findByRole("region", { name: "Gate" });
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await within(panel).findByText("A gate.");
    expect(choicesOf(panel)).toEqual(["Go through"]);
    const field = within(panel).getByRole("textbox", { name: "gold" });
    await user.clear(field);
    await user.type(field, "9{Enter}");
    const none = await within(panel).findByText(
      /^No choice here holds with the values now/,
    );
    expect(within(panel).queryByRole("region", { name: "Choices" })).toBeNull();
    // Said as it comes, as the focus stays in the field, and as the
    // current node's heading describes it.
    expect(none.closest('[role="status"]')).not.toBeNull();
    expect(
      within(panel)
        .getByRole("heading", { name: "Gate" })
        .getAttribute("aria-describedby"),
    ).toContain(none.id);
    await user.clear(field);
    await user.type(field, "1{Enter}");
    await waitFor(() => expect(choicesOf(panel)).toEqual(["Go through"]));
    expect(
      within(panel).queryByText(/^No choice here holds with the values now/),
    ).toBeNull();
  });

  // Below 900 px the panel is a sheet, which Escape closes, stopping play;
  // in the inspector's filter, Escape empties the filter first, as a
  // search box's does, and keeps play, and with the filter empty closes
  // the sheet, as in its other fields.
  it("empties the inspector's filter with Escape on a sheet, rather than stopping play", async () => {
    windowOf(800);
    const user = userEvent.setup();
    playProject();
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const sheet = await screen.findByRole("dialog", { name: "Play" });
    await within(sheet).findByText("Trust 1.");
    const filter = within(sheet).getByRole("searchbox", {
      name: "Filter the values",
    });
    await user.type(filter, "gold");
    expect(
      within(sheet).queryByRole("textbox", { name: "elara.trust" }),
    ).toBeNull();
    await user.keyboard("{Escape}");
    expect((filter as HTMLInputElement).value).toBe("");
    expect(screen.getByRole("dialog", { name: "Play" })).toBe(sheet);
    expect(
      within(sheet).getByRole("textbox", { name: "elara.trust" }),
    ).toBeDefined();
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Play" })).toBeNull(),
    );
  });

  // A key that an input method takes as it composes, as the Enter that ends
  // what was typed in Japanese or Chinese, or the Escape that cancels it, is
  // the input method's: a value's field neither sets what is typed nor goes
  // back to the value, which would lose it, the filter empties nothing, and
  // neither closes the sheet, of such a key as the event says it is, or as
  // Safari tells of one, once the composition has ended, by its keyCode
  // alone, 229.
  it("leaves the keys of an input method's composition to it in the inspector's fields on a sheet", async () => {
    windowOf(800);
    const user = userEvent.setup();
    playProject();
    renderApp(`/projects/${project.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await openBoard(harbour);
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const sheet = await screen.findByRole("dialog", { name: "Play" });
    await within(sheet).findByText("Trust 1.");
    const field = within(sheet).getByRole("textbox", { name: "gold" });
    await user.clear(field);
    await user.type(field, "7");
    fireEvent.keyDown(field, { key: "Enter", isComposing: true });
    fireEvent.keyDown(field, { key: "Enter", code: "Enter", keyCode: 229 });
    fireEvent.keyDown(field, { key: "Escape", isComposing: true });
    fireEvent.keyDown(field, { key: "Escape", code: "Escape", keyCode: 229 });
    expect((field as HTMLInputElement).value).toBe("7");
    expect(screen.getByRole("dialog", { name: "Play" })).toBe(sheet);
    // Nothing was set: the toll that 7 would afford is still hidden.
    expect(choicesOf(sheet)).toEqual(["Visit Elara", "Wait here"]);
    await user.keyboard("{Enter}");
    await waitFor(() =>
      expect(choicesOf(sheet)).toEqual([
        "Visit Elara",
        "Wait here",
        "Pay the toll",
      ]),
    );
    const filter = within(sheet).getByRole("searchbox", {
      name: "Filter the values",
    });
    await user.type(filter, "gold");
    fireEvent.keyDown(filter, { key: "Escape", isComposing: true });
    fireEvent.keyDown(filter, { key: "Escape", code: "Escape", keyCode: 229 });
    expect((filter as HTMLInputElement).value).toBe("gold");
    expect(screen.getByRole("dialog", { name: "Play" })).toBe(sheet);
  });

  // The panel says that it copied the history until the history changes,
  // and not again once a step back gives back the very history that it
  // copied before the step; the inspector says how to go on of a flow that
  // a step ended, by a step back.
  it("says that it copied the history only until the history changes", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn(async (_: string) => {});
    vi.stubGlobal("isSecureContext", true);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    try {
      playProject();
      const panel = await startPlay();
      await user.click(
        within(panel).getByRole("button", { name: "Copy as text" }),
      );
      await waitFor(() =>
        expect(
          within(panel).getByText("Copied the history as text."),
        ).toBeDefined(),
      );
      await user.click(
        within(panel).getByRole("button", { name: "Wait here" }),
      );
      await waitFor(() =>
        expect(within(panel).getByText("You wait.")).toBeDefined(),
      );
      expect(
        within(panel).queryByText("Copied the history as text."),
      ).toBeNull();
      expect(
        within(panel).getByText(
          "The flow has ended: a value set here changes the text, but no choice. Step back to before the step that ended it.",
        ),
      ).toBeDefined();
      await user.click(
        within(panel).getByRole("button", { name: "Step back" }),
      );
      await waitFor(() =>
        expect(within(panel).getByText("Trust 1.")).toBeDefined(),
      );
      expect(
        within(panel).queryByText("Copied the history as text."),
      ).toBeNull();
    } finally {
      Reflect.deleteProperty(navigator, "clipboard");
    }
  });

  // A flow that ended in the start's step has no step to step back from,
  // which the inspector does not tell the author to take.
  it("says that the flow ended as play started, without a step back to take", async () => {
    playProject();
    renderApp(
      `/projects/${project.id}/boards/${harbour.id}?node=${id("nd", "B")}`,
      { session: testSession },
    );
    const canvas = await openBoard(harbour);
    await screen.findByRole("region", { name: "Wait" });
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "p", code: "KeyP" });
    const panel = await screen.findByRole("region", { name: "Play" });
    await within(panel).findByText("You wait.");
    expect(
      within(panel).getByText(
        "The flow ended as play started: a value set here changes the text, but no choice.",
      ),
    ).toBeDefined();
    expect(
      within(panel).queryByText(/Step back to before the step that ended it/),
    ).toBeNull();
    expect(
      (
        within(panel).getByRole("button", {
          name: "Step back",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
});
