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
import { fakeApi, startPage, testProject } from "../test/fake";
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
];

/** Answers the board's pages of the project, and records what they ask. */
function playProject() {
  return fakeApi((operation, input) => {
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
        return { items: [harbour, elara], next_cursor: null };
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
    expect(within(panel).getByRole("heading", { name: "Quay" })).toBeDefined();
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
    expect(
      within(there).getByText(
        "The flow ended here, at a dead end: no connection leads on.",
      ),
    ).toBeDefined();
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
    expect(within(panel).getByRole("alert").textContent).toContain(
      "Play stopped at a runtime error",
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
    await user.click(
      within(panel).getAllByRole("button", { name: "Restart" })[0],
    );
    await waitFor(() =>
      expect(within(panel).getByText("Changed.")).toBeDefined(),
    );
  });
});
