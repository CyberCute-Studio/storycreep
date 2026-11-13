import type {
  Board,
  Component,
  ComponentType,
  Connection,
  Node,
  RichTextDoc,
  Variable,
} from "@storycreep/shared/api";
import { compile, encode, Project } from "@storycreep/shared/expr/compile";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Engine } from "../canvas/engine";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";

// A board's script view (PRD FR-TEXT-04, NFR-A11Y-02): the board as a
// linear document, in the order of its rule, which the view says, whose
// edits are the canvas's commands, and which a keyboard works alone.

// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of: the
// canvas that the tests switch to draws with an engine that draws nothing.
vi.mock("../canvas/engine", () => ({
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

const at = "2026-11-01T10:00:00Z";
const goldId = "var_01HQ8Z3XK4F7NAWR5TCQ9M2BV1";
const elaraId = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BV3";

const harbour: Board = {
  id: "bd_a",
  project_id: testProject.id,
  name: "Harbour",
  slug: "harbour",
  description: "",
  color: "none",
  order: "V",
  entry_node_id: "nd_quay",
  rev: 3,
  created_at: at,
  updated_at: at,
};

const gold: Variable = {
  id: goldId,
  project_id: testProject.id,
  key: "gold",
  type: "int",
  default: 0,
  scope: "global",
  reset_on_enter: false,
  description: "",
  order: "V",
  rev: 1,
  created_at: at,
  updated_at: at,
};

const character: ComponentType = {
  id: "ct_character",
  project_id: testProject.id,
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [],
  rev: 1,
  created_at: at,
  updated_at: at,
};

const elara: Omit<Component, "attributes" | "description"> = {
  id: elaraId,
  project_id: testProject.id,
  component_type_id: character.id,
  key: "elara",
  name: "Elara",
  order: "V",
  rev: 1,
  created_at: at,
  updated_at: at,
};

/** Returns a text of one paragraph of words. */
function textOf(words: string): RichTextDoc {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text: words }] }],
  };
}

/** The condition gold >= 3, compiled, as the server stores it. */
function goldCondition() {
  const ast = compile(
    "gold >= 3",
    "condition",
    Project.of([{ id: goldId, key: "gold", type: "int" }], [], []),
    "",
  );
  return {
    source: "gold >= 3",
    is_valid: true,
    ast_version: 1,
    ast: JSON.parse(encode(ast)),
  };
}

/** Returns an element created at the minute minute. */
function element(
  id: string,
  title: string,
  minute: number,
  extra: Partial<Node> = {},
): Node {
  return testNode(id, minute * 400, 0, {
    title,
    kind: "dialogue",
    created_at: `2026-11-01T10:${String(minute).padStart(2, "0")}:00Z`,
    ...extra,
  });
}

/**
 * The board Harbour: the quay, its entry, where Elara speaks, which offers
 * two choices, to the market, whose condition is gold >= 3, and to the
 * pier; the market leads on to the pier; and a cellar that nothing reaches,
 * created before them all.
 */
function harbourBoard() {
  const nodes = [
    element("nd_quay", "Quay", 2, {
      speaker_component_id: elaraId,
      text: textOf("Welcome to the harbour."),
    }),
    element("nd_market", "Market", 3, { text: textOf("Stalls everywhere.") }),
    element("nd_pier", "Pier", 4, {
      kind: "narration",
      text: textOf("The tide is out."),
      is_ending: true,
    }),
    element("nd_cellar", "Cellar", 1),
  ];
  const connections: Connection[] = [
    testConnection("cn_pier", "nd_quay", "nd_pier", {
      order: "W",
      label: textOf("Walk to the pier"),
    }),
    testConnection("cn_market", "nd_quay", "nd_market", {
      order: "V",
      label: textOf("Buy a map"),
      condition: goldCondition(),
    }),
    testConnection("cn_on", "nd_market", "nd_pier"),
  ];
  return { nodes, connections };
}

type Sent = { type: string; payload: unknown; expected_rev?: unknown }[];

/** Returns the IDs that command changes or makes. */
function touched(command: Sent[number]): string[] {
  const p = command.payload as { id?: string };
  return p.id === undefined ? [] : [p.id];
}

/**
 * Applies command to the board's nodes and connections, as the server
 * does, of the commands that the view sends, at the revision rev of what
 * it changes, so that the board read anew holds it.
 */
function applied(
  command: Sent[number],
  nodes: Node[],
  connections: Connection[],
  rev: number,
) {
  const p = command.payload as Record<string, unknown> & { id: string };
  const at = (list: { id: string }[]) => list.findIndex((e) => e.id === p.id);
  switch (command.type) {
    case "node.create":
      nodes.push(
        testNode(p.id, 0, 0, {
          title: "",
          kind: "dialogue",
          rev,
          created_at: "2026-11-01T11:00:00Z",
        }),
      );
      break;
    case "node.update": {
      const i = at(nodes);
      nodes[i] = { ...nodes[i], ...(p as Partial<Node>), rev };
      break;
    }
    case "connection.create":
      connections.push(
        testConnection(p.id, p.from_node_id as string, p.to_node_id as string, {
          order: "z",
          rev,
        }),
      );
      break;
    case "connection.reconnect":
    case "connection.update": {
      const i = at(connections);
      connections[i] = {
        ...connections[i],
        ...(p as Partial<Connection>),
        rev,
      };
      break;
    }
  }
}

/**
 * Answers the board's page, of nodes and connections, and applies each
 * batch that the page sends to them, but for those that refuse says to
 * refuse, by their index among those sent.
 */
function boardPage(
  board: { nodes: Node[]; connections: Connection[] },
  { role = "owner", refuse }: { role?: string; refuse?: number } = {},
) {
  const nodes = [...board.nodes];
  const connections = [...board.connections];
  let seq = testProject.seq;
  let sent = 0;
  const revs = new Map<string, number>([
    ...nodes.map((n) => [n.id, n.rev] as const),
    ...connections.map((c) => [c.id, c.rev] as const),
  ]);
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, role, seq };
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listVariables":
        return { items: [gold], next_cursor: null };
      case "listComponentTypes":
        return { items: [character], next_cursor: null };
      case "listComponents":
        return { items: [elara], next_cursor: null };
      case "listBoards":
        return { items: [harbour], next_cursor: null };
      case "listBoardNodes":
        return { items: [...nodes], next_cursor: null };
      case "listBoardConnections":
        return { items: [...connections], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "applyCommands": {
        if (sent++ === refuse) {
          throw refused(422, { code: "validation_failed" });
        }
        const { commands } = (input as { body: { commands: Sent } }).body;
        seq++;
        const ids = new Set(commands.flatMap(touched));
        for (const id of ids) {
          revs.set(id, (revs.get(id) ?? 0) + 1);
        }
        for (const command of commands) {
          applied(
            command,
            nodes,
            connections,
            revs.get(touched(command)[0]) ?? 1,
          );
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

/** Opens the board's script view, and returns its region once it shows the board's nodes. */
async function openScript() {
  renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
    session: testSession,
  });
  const region = await screen.findByRole("region", { name: "Script" });
  await within(region).findByRole("heading", { name: "Quay" });
  return region;
}

/** Returns the entry of the node of title. */
function entryOf(title: string): HTMLElement {
  return screen.getByRole("article", { name: title });
}

/** Types words into the rich-text box, as the user does. */
function typeInto(textbox: HTMLElement, words: string) {
  act(() => {
    (
      textbox as unknown as {
        editor: {
          commands: {
            selectAll: () => void;
            insertContent: (t: string) => void;
          };
        };
      }
    ).editor.commands.selectAll();
    (
      textbox as unknown as {
        editor: { commands: { insertContent: (t: string) => void } };
      }
    ).editor.commands.insertContent(words);
  });
}

describe("the script view", () => {
  // Verifies: FR-TEXT-04/AC2
  it("says its rule, and lists the nodes breadth-first from the entry node, then the rest in the order they were created", async () => {
    boardPage(harbourBoard());
    const region = await openScript();
    expect(
      within(region).getByText(/^Nodes are listed breadth-first/),
    ).toBeDefined();
    const reached = within(region).getByRole("region", {
      name: "Reached from the entry node (3 nodes)",
    });
    expect(
      within(reached)
        .getAllByRole("heading", { level: 4 })
        .map((h) => h.textContent),
    ).toEqual(["Quay", "Market", "Pier"]);
    const unreached = within(region).getByRole("region", {
      name: "Not reached from the entry node (1 node)",
    });
    expect(
      within(unreached)
        .getAllByRole("heading", { level: 4 })
        .map((h) => h.textContent),
    ).toEqual(["Cellar"]);
  });

  // Verifies: FR-TEXT-04/AC2
  it("shows each node's speaker, text, and choices, each with its condition in plain words and the node that it leads to", async () => {
    boardPage(harbourBoard());
    await openScript();
    const quay = entryOf("Quay");
    expect(within(quay).getByText("Dialogue · Elara")).toBeDefined();
    expect(within(quay).getByText("Welcome to the harbour.")).toBeDefined();
    const choices = within(quay).getByRole("list", { name: "Choices of Quay" });
    const [map, pier] = within(choices).getAllByRole("listitem");
    expect(map.textContent).toContain("Buy a map");
    expect(map.textContent).toContain("Only if gold is at least 3");
    expect(within(map).getByRole("link", { name: "Market" })).toBeDefined();
    expect(pier.textContent).toContain("Walk to the pier");
    expect(within(pier).getByRole("link", { name: "Pier" })).toBeDefined();
    expect(
      within(entryOf("Pier")).getByText("Narration · Ending"),
    ).toBeDefined();
    // An unlabelled connection continues to its node.
    expect(within(entryOf("Market")).getByText(/Continues to/)).toBeDefined();
    // A choice's link gives its node's entry the focus, in the page.
    fireEvent.click(within(map).getByRole("link", { name: "Market" }));
    expect(document.activeElement).toBe(entryOf("Market"));
  });

  // Verifies: FR-TEXT-04/AC1, FR-TEXT-04/AC3
  it("edits a node's title and text with the keyboard, in one node.update, as the canvas does, which the canvas then shows", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    const edit = within(entryOf("Market")).getByRole("button", {
      name: "Edit Market",
    });
    edit.focus();
    await user.keyboard("{Enter}");
    const form = await screen.findByRole("form", { name: "Edit Market" });
    const title = within(form).getByRole("textbox", { name: "Title" });
    expect(document.activeElement).toBe(title);
    await user.clear(title);
    await user.type(title, "Fish market");
    typeInto(
      within(form).getByRole("textbox", { name: "Text of Market" }),
      "Gulls circle the stalls.",
    );
    title.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "node.update",
        payload: {
          id: "nd_market",
          title: "Fish market",
          text: textOf("Gulls circle the stalls."),
        },
        expected_rev: { nd_market: 1 },
      },
    ]);
    // The editor closes, and the focus is back on its button.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(entryOf("Fish market")).getByRole("button", {
          name: "Edit Fish market",
        }),
      ),
    );
    expect(
      within(entryOf("Fish market")).getByText("Gulls circle the stalls."),
    ).toBeDefined();
    // The canvas shows the change without a reload: its tree names the
    // node anew.
    await user.click(screen.getByRole("link", { name: "Canvas" }));
    const tree = await screen.findByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    await waitFor(() =>
      expect(
        within(tree)
          .getAllByRole("treeitem")
          .some((i) =>
            i
              .getAttribute("aria-label")
              ?.startsWith("Fish market, Dialogue: Gulls circle the stalls."),
          ),
      ).toBe(true),
    );
  });

  // Verifies: FR-TEXT-04/AC1
  it("edits a choice's label in one connection.update, as the connection's inspector does", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Edit label: Walk to the pier",
      }),
    );
    const form = await screen.findByRole("form", {
      name: "Edit the choice's label",
    });
    const label = within(form).getByRole("textbox", { name: "Label" });
    await waitFor(() => expect(document.activeElement).toBe(label));
    typeInto(label, "Stroll to the pier");
    await user.click(within(form).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      {
        type: "connection.update",
        payload: { id: "cn_pier", label: textOf("Stroll to the pier") },
        expected_rev: { cn_pier: 1 },
      },
    ]);
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(entryOf("Quay")).getByRole("button", {
          name: "Edit label: Stroll to the pier",
        }),
      ),
    );
  });

  // Verifies: FR-TEXT-04/AC3
  it("closes an editor that holds no change with Escape, and one that holds a change only with Cancel, the focus back on its button either way", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    const edit = () =>
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" });
    edit().focus();
    await user.keyboard("{Enter}");
    let form = await screen.findByRole("form", { name: "Edit Pier" });
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(document.activeElement).toBe(edit());
    await user.keyboard("{Enter}");
    form = await screen.findByRole("form", { name: "Edit Pier" });
    await user.type(within(form).getByRole("textbox", { name: "Title" }), "s");
    await user.keyboard("{Escape}");
    expect(screen.getByRole("form", { name: "Edit Pier" })).toBeDefined();
    await user.click(within(form).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(document.activeElement).toBe(edit());
    expect(batches(calls)).toEqual([]);
  });

  // Verifies: FR-TEXT-04/AC3
  it("adds a node after an element, between it and what followed it, in one batch, and opens the new node's text", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    const add = within(entryOf("Market")).getByRole("button", {
      name: "Add node after Market",
    });
    add.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [batch] = batches(calls);
    expect(batch.map((c) => c.type)).toEqual([
      "node.create",
      "connection.reconnect",
      "connection.create",
    ]);
    const created = (batch[0].payload as { id: string }).id;
    expect(batch[1].payload).toEqual({
      id: "cn_on",
      from_node_id: created,
      from_port: "out",
    });
    expect(batch[2].payload).toMatchObject({
      from_node_id: "nd_market",
      to_node_id: created,
    });
    // The new node's editor is open, with the focus in its text.
    const form = await screen.findByRole("form", { name: "Edit Untitled" });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(form).getByRole("textbox", { name: "Text of Untitled" }),
      ),
    );
  });

  // Verifies: FR-TEXT-04/AC3
  it("adds a choice to an element, to a new node, in one batch, and opens the new choice's label", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Add choice to Quay",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [batch] = batches(calls);
    expect(batch.map((c) => c.type)).toEqual([
      "node.create",
      "connection.create",
    ]);
    expect(batch[1].payload).toMatchObject({
      from_node_id: "nd_quay",
      from_port: "out",
      to_node_id: (batch[0].payload as { id: string }).id,
    });
    const form = await screen.findByRole("form", {
      name: "Edit the choice's label",
    });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(form).getByRole("textbox", { name: "Label" }),
      ),
    );
    // Escape leaves the label, of no change, and the focus goes to the
    // new choice's own button.
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(document.activeElement?.getAttribute("aria-label")).toBe(
      `Edit label: Choice to Untitled`,
    );
  });

  it("keeps an editor open, with what it holds, as another is opened, or a node added", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    await openScript();
    await user.click(
      within(entryOf("Market")).getByRole("button", { name: "Edit Market" }),
    );
    const market = await screen.findByRole("form", { name: "Edit Market" });
    const title = within(market).getByRole("textbox", { name: "Title" });
    await user.type(title, " square");
    await user.click(
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }),
    );
    expect(
      await screen.findByRole("form", { name: "Edit Pier" }),
    ).toBeDefined();
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Add node after Quay",
      }),
    );
    expect(
      await screen.findByRole("form", { name: "Edit Untitled" }),
    ).toBeDefined();
    expect(screen.getByRole("form", { name: "Edit Market" })).toBe(market);
    expect(title).toHaveProperty("value", "Market square");
  });

  it("keeps an editor whose node another session deleted, with what it holds, saying that it cannot be saved, until it is cancelled", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { name: "Quay" });
    await user.click(
      within(entryOf("Cellar")).getByRole("button", { name: "Edit Cellar" }),
    );
    const title = within(
      await screen.findByRole("form", { name: "Edit Cellar" }),
    ).getByRole("textbox", { name: "Title" });
    await user.type(title, " door");
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.delete", payload: { id: "nd_cellar" } }],
        [{ id: "nd_cellar", rev: 2 }],
      ),
    );
    // The entry stays where it was, of the same editor, saying why it
    // cannot be saved, and offering nothing else.
    const kept = screen.getByRole("form", { name: "Edit Cellar" });
    expect(
      await within(kept).findByText(/^This is no longer on the board/),
    ).toBeDefined();
    expect(
      within(entryOf("Cellar")).queryByRole("link", {
        name: "Show on canvas: Cellar",
      }),
    ).toBeNull();
    expect(within(kept).getByRole("textbox", { name: "Title" })).toHaveProperty(
      "value",
      "Cellar door",
    );
    expect(within(kept).getByRole("button", { name: "Save" })).toHaveProperty(
      "disabled",
      true,
    );
    await user.click(within(kept).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Script" }),
    );
    expect(batches(calls)).toEqual([]);
  });

  it("says why an addition was refused, and gives the focus back to its button", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard(), { refuse: 0 });
    await openScript();
    const add = within(entryOf("Quay")).getByRole("button", {
      name: "Add choice to Quay",
    });
    await user.click(add);
    // The server's message, which the fake gives as the refusal's code,
    // read out, and shown in the node's entry.
    await waitFor(() =>
      expect(
        within(entryOf("Quay")).getByText("validation_failed"),
      ).toBeDefined(),
    );
    expect(
      screen
        .getAllByText("validation_failed")
        .some((m) => m.closest('[role="alert"]') !== null),
    ).toBe(true);
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(entryOf("Quay")).getByRole("button", {
          name: "Add choice to Quay",
        }),
      ),
    );
  });

  it("offers a viewer no edits, but each node on the canvas", async () => {
    boardPage(harbourBoard(), { role: "viewer" });
    await openScript();
    const quay = entryOf("Quay");
    expect(within(quay).queryByRole("button")).toBeNull();
    expect(
      within(quay).getByRole("link", { name: "Show on canvas: Quay" }),
    ).toBeDefined();
  });

  it("says that a board without an entry node lists its nodes in the order they were created", async () => {
    const board = harbourBoard();
    boardPage(board);
    harbour.entry_node_id = undefined;
    try {
      renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
        session: testSession,
      });
      const region = await screen.findByRole("region", { name: "Script" });
      expect(
        await within(region).findByText(/^The board has no entry node/),
      ).toBeDefined();
      const all = within(region).getByRole("region", {
        name: "All nodes (4 nodes)",
      });
      expect(
        within(all)
          .getAllByRole("heading", { level: 4 })
          .map((h) => h.textContent),
      ).toEqual(["Cellar", "Quay", "Market", "Pier"]);
    } finally {
      harbour.entry_node_id = "nd_quay";
    }
  });
});
