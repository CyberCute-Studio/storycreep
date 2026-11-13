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
 * Answers the board's page, of nodes and connections, the first failReads
 * reads of its nodes failing, and applies each batch that the page sends
 * to them, but for those that refuse says to refuse, by their index among
 * those sent, with what refusal makes, each once gate, if it is given, lets
 * it.
 */
function boardPage(
  board: { nodes: Node[]; connections: Connection[] },
  {
    role = "owner",
    refuse = [],
    refusal = () => refused(422, { code: "validation_failed" }),
    gate,
    failReads = 0,
  }: {
    role?: string;
    refuse?: number | readonly number[];
    refusal?: () => Error;
    gate?: Promise<void>;
    failReads?: number;
  } = {},
) {
  const nodes = [...board.nodes];
  const connections = [...board.connections];
  const refusing = new Set(typeof refuse === "number" ? [refuse] : refuse);
  let seq = testProject.seq;
  let sent = 0;
  let reads = 0;
  const revs = new Map<string, number>([
    ...nodes.map((n) => [n.id, n.rev] as const),
    ...connections.map((c) => [c.id, c.rev] as const),
  ]);
  // Applies a batch, as the server does, and answers as it does.
  function answerBatch(input: unknown) {
    const { commands } = (input as { body: { commands: Sent } }).body;
    seq++;
    const ids = new Set(commands.flatMap(touched));
    for (const id of ids) {
      revs.set(id, (revs.get(id) ?? 0) + 1);
    }
    for (const command of commands) {
      applied(command, nodes, connections, revs.get(touched(command)[0]) ?? 1);
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
        if (reads++ < failReads) {
          throw refused(500, { code: "internal" });
        }
        return { items: [...nodes], next_cursor: null };
      case "listBoardConnections":
        return { items: [...connections], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "applyCommands": {
        const index = sent++;
        return (gate ?? Promise.resolve()).then(() => {
          if (refusing.has(index)) {
            throw refusal();
          }
          return answerBatch(input);
        });
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

/** Puts the caret of the rich-text box at at, a place of its document, as the user does with the arrows, which changes nothing. */
function caretAt(textbox: HTMLElement, at: number) {
  act(() => {
    (
      textbox as unknown as {
        editor: { commands: { setTextSelection: (at: number) => void } };
      }
    ).editor.commands.setTextSelection(at);
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
      name: "Edit label: Walk to the pier",
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
  it("names the form of each choice's label by its choice, as its button, so that two open at once are told apart", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    await openScript();
    const quay = entryOf("Quay");
    await user.click(
      within(quay).getByRole("button", { name: "Edit label: Buy a map" }),
    );
    await user.click(
      within(quay).getByRole("button", {
        name: "Edit label: Walk to the pier",
      }),
    );
    await within(quay).findByRole("form", {
      name: "Edit label: Walk to the pier",
    });
    expect(
      within(quay)
        .getAllByRole("form")
        .map((f) => f.getAttribute("aria-label")),
    ).toEqual(["Edit label: Buy a map", "Edit label: Walk to the pier"]);
  });

  // Verifies: FR-TEXT-04/AC3
  it("gives the focus to a label that the editor cannot change, as of a newer version, as its editor opens", async () => {
    const user = userEvent.setup();
    const board = harbourBoard();
    // A mark that this version does not have, as a newer server's.
    board.connections[2] = {
      ...board.connections[2],
      label: {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [
              { type: "text", text: "Onward", marks: [{ type: "underline" }] },
            ],
          },
        ],
      } as unknown as RichTextDoc,
    };
    boardPage(board);
    await openScript();
    within(entryOf("Market"))
      .getByRole("button", { name: "Edit label: Onward" })
      .focus();
    await user.keyboard("{Enter}");
    const form = await screen.findByRole("form", {
      name: "Edit label: Onward",
    });
    const label = within(form).getByRole("textbox", { name: "Label" });
    expect(label.getAttribute("contenteditable")).toBe("false");
    await waitFor(() => expect(document.activeElement).toBe(label));
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
  it("leaves an editor that holds no change open on the Escape that an input method took, and closes it on the next", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    await openScript();
    await user.click(
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }),
    );
    const form = await screen.findByRole("form", { name: "Edit Pier" });
    const title = within(form).getByRole("textbox", { name: "Title" });
    await waitFor(() => expect(document.activeElement).toBe(title));
    // Safari's, of the composition that it cancelled, which comes once the
    // composition has ended, of keyCode 229.
    fireEvent.keyDown(title, { key: "Escape", code: "Escape", keyCode: 229 });
    expect(screen.getByRole("form", { name: "Edit Pier" })).toBe(form);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
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
      name: "Edit label: Choice to Untitled",
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

  it("keeps a choice's label open where it was, with what it holds, as a node is added after its element, whose text then has the focus", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    await user.click(
      within(entryOf("Market")).getByRole("button", {
        name: "Edit label: Choice to Pier",
      }),
    );
    const form = await screen.findByRole("form", {
      name: "Edit label: Choice to Pier",
    });
    const label = within(form).getByRole("textbox", { name: "Label" });
    await waitFor(() => expect(document.activeElement).toBe(label));
    typeInto(label, "Head on");
    // The node added after Market takes the choice, which leaves it now.
    await user.click(
      within(entryOf("Market")).getByRole("button", {
        name: "Add node after Market",
      }),
    );
    const added = await screen.findByRole("form", { name: "Edit Untitled" });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(added).getByRole("textbox", { name: "Text of Untitled" }),
      ),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // The same form, in the entry where it was, with what was typed.
    expect(
      within(entryOf("Market")).getByRole("form", {
        name: "Edit label: Choice to Pier",
      }),
    ).toBe(form);
    expect(label.textContent).toBe("Head on");
    await user.click(within(form).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    expect(batches(calls)[1]).toMatchObject([
      {
        type: "connection.update",
        payload: { id: "cn_on", label: textOf("Head on") },
      },
    ]);
    // Once it closes, the choice is in the entry of the node that it
    // leaves, whose button of it has the focus.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(entryOf("Untitled")).getByRole("button", {
          name: "Edit label: Head on",
        }),
      ),
    );
  });

  it("keeps a branch's choice's label open under its output, with what it holds, as another session deletes the output, with the choice", async () => {
    const user = userEvent.setup();
    const output = (id: string, kind: "if" | "else") => ({
      id,
      node_id: "nd_gate",
      kind,
      rev: 1,
      created_at: at,
      updated_at: at,
    });
    const board = harbourBoard();
    board.nodes.push(
      testNode("nd_gate", 0, 0, {
        type: "branch",
        kind: undefined,
        title: "Gate",
        outputs: [output("bo_if", "if"), output("bo_else", "else")],
        created_at: "2026-11-01T10:05:00Z",
      }),
    );
    board.connections.push(
      testConnection("cn_toll", "nd_gate", "nd_market", {
        from_port: "bo_if",
        label: textOf("Pay the toll"),
      }),
      testConnection("cn_back", "nd_gate", "nd_pier", { from_port: "bo_else" }),
    );
    boardPage(board);
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { name: "Gate" });
    await user.click(
      within(entryOf("Gate")).getByRole("button", {
        name: "Edit label: Pay the toll",
      }),
    );
    const form = await screen.findByRole("form", {
      name: "Edit label: Pay the toll",
    });
    const label = within(form).getByRole("textbox", { name: "Label" });
    await waitFor(() => expect(document.activeElement).toBe(label));
    typeInto(label, "Pay up");
    act(() =>
      realtime.send(
        testProject.id,
        [
          {
            type: "branch_output.delete",
            payload: { id: "bo_if", confirm: true },
          },
        ],
        [
          { id: "bo_if", rev: 2 },
          { id: "nd_gate", rev: 2 },
        ],
      ),
    );
    // The same form, under the output where it was, with what was typed,
    // saying that it cannot be saved.
    expect(
      await within(form).findByText(/^This is not on the board/),
    ).toBeDefined();
    const outputs = within(entryOf("Gate")).getByRole("list", {
      name: "Outputs of Gate",
    });
    expect(
      within(outputs).getByRole("form", { name: "Edit label: Pay the toll" }),
    ).toBe(form);
    expect(label.textContent).toBe("Pay up");
    expect(document.activeElement).toBe(label);
  });

  it("keeps an editor open, with what it holds, in its part, as another session takes away what reached its node", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { name: "Quay" });
    await user.click(
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }),
    );
    const form = await screen.findByRole("form", { name: "Edit Pier" });
    const title = within(form).getByRole("textbox", { name: "Title" });
    await user.type(title, " end");
    act(() =>
      realtime.send(
        testProject.id,
        [
          { type: "connection.delete", payload: { id: "cn_pier" } },
          { type: "connection.delete", payload: { id: "cn_on" } },
        ],
        [
          { id: "cn_pier", rev: 2 },
          { id: "cn_on", rev: 2 },
        ],
      ),
    );
    await waitFor(() =>
      expect(
        within(entryOf("Quay")).queryByText("Walk to the pier"),
      ).toBeNull(),
    );
    // Each part counts the entries that it lists.
    expect(
      screen.getByRole("region", {
        name: "Not reached from the entry node (1 node)",
      }),
    ).toBeDefined();
    const reached = screen.getByRole("region", {
      name: "Reached from the entry node (3 nodes)",
    });
    expect(within(reached).getByRole("form", { name: "Edit Pier" })).toBe(form);
    expect(title).toHaveProperty("value", "Pier end");
    // Once it closes, the entry is where the order puts it.
    await user.click(within(form).getByRole("button", { name: "Cancel" }));
    const unreached = await screen.findByRole("region", {
      name: "Not reached from the entry node (2 nodes)",
    });
    await waitFor(() =>
      expect(
        within(unreached)
          .getAllByRole("heading", { level: 4 })
          .map((h) => h.textContent),
      ).toEqual(["Cellar", "Pier"]),
    );
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
      await within(kept).findByText(/^This is not on the board/),
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

  it("names and counts its parts as it shows them while it keeps the ghost of the entry node that another session deleted", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { name: "Quay" });
    await user.click(
      within(entryOf("Quay")).getByRole("button", { name: "Edit Quay" }),
    );
    const form = await screen.findByRole("form", { name: "Edit Quay" });
    await user.type(within(form).getByRole("textbox", { name: "Title" }), "s");
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.delete", payload: { id: "nd_quay" } }],
        [{ id: "nd_quay", rev: 2 }],
      ),
    );
    expect(
      await within(form).findByText(/^This is not on the board/),
    ).toBeDefined();
    const region = screen.getByRole("region", { name: "Script" });
    expect(
      within(region).getByText(/^Nodes are listed breadth-first/),
    ).toBeDefined();
    const reached = within(region).getByRole("region", {
      name: "Reached from the entry node (1 node)",
    });
    expect(within(reached).getByRole("form", { name: "Edit Quay" })).toBe(form);
    expect(
      within(region).getByRole("region", {
        name: "Not reached from the entry node (3 nodes)",
      }),
    ).toBeDefined();
  });

  it("closes an editor that holds no change once another session deleted its node, as the canvas's editor of a text does", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?view=script`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { name: "Quay" });
    await user.click(
      within(entryOf("Cellar")).getByRole("button", { name: "Edit Cellar" }),
    );
    await screen.findByRole("form", { name: "Edit Cellar" });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.delete", payload: { id: "nd_cellar" } }],
        [{ id: "nd_cellar", rev: 2 }],
      ),
    );
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(screen.queryByRole("article", { name: "Cellar" })).toBeNull();
    // The focus was in the form, and goes to the view's heading, as the
    // entry went with it.
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Script" }),
    );
  });

  it("adds one node at a time after an element, and says each refused addition, whatever else is under way", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = boardPage(harbourBoard(), { refuse: 0, gate });
    await openScript();
    const after = within(entryOf("Market")).getByRole("button", {
      name: "Add node after Market",
    });
    await user.dblClick(after);
    // The element's additions wait for the batch under way.
    expect(after).toHaveProperty("disabled", true);
    expect(
      within(entryOf("Market")).getByRole("button", {
        name: "Add choice to Market",
      }),
    ).toHaveProperty("disabled", true);
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Add choice to Quay",
      }),
    );
    await act(async () => release());
    await waitFor(() => expect(batches(calls)).toHaveLength(2));
    // The first, refused, says so in its element's entry, and its new
    // node's editor, of nothing typed, closes.
    await waitFor(() =>
      expect(
        within(entryOf("Market")).getByText("validation_failed"),
      ).toBeDefined(),
    );
    await waitFor(() =>
      expect(screen.queryByRole("form", { name: "Edit Untitled" })).toBeNull(),
    );
    expect(screen.queryByText(/^This is not on the board/)).toBeNull();
    expect(after).toHaveProperty("disabled", false);
    // The second applied, of the other element: its choice's label is open,
    // and keeps the focus that the user took there, rather than give it to
    // the button of the refused addition.
    const label = within(entryOf("Quay")).getByRole("form", {
      name: "Edit label: Choice to Untitled",
    });
    expect(document.activeElement).toBe(
      within(label).getByRole("textbox", { name: "Label" }),
    );
    expect(batches(calls).map((b) => b.map((c) => c.type))).toEqual([
      ["node.create", "connection.reconnect", "connection.create"],
      ["node.create", "connection.create"],
    ]);
  });

  it("says each element's refused addition in its own entry, until that element's next addition, and reads out each", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    boardPage(harbourBoard(), { refuse: [0, 1], gate });
    await openScript();
    const add = (title: string, what: "Add node after" | "Add choice to") =>
      user.click(
        within(entryOf(title)).getByRole("button", {
          name: `${what} ${title}`,
        }),
      );
    await add("Market", "Add node after");
    await add("Quay", "Add choice to");
    await act(async () => release());
    const says = (title: string) =>
      within(entryOf(title)).queryByText("validation_failed") !== null;
    await waitFor(() => expect(says("Quay")).toBe(true));
    expect(says("Market")).toBe(true);
    // Each is read out, though their words are the same.
    expect(
      screen
        .getAllByText("validation_failed")
        .filter((m) => m.closest('[role="alert"]') !== null),
    ).toHaveLength(2);
    // Another element's addition leaves both; the element's own its own.
    await add("Pier", "Add choice to");
    expect(says("Market")).toBe(true);
    expect(says("Quay")).toBe(true);
    await add("Market", "Add choice to");
    expect(says("Market")).toBe(false);
    expect(says("Quay")).toBe(true);
  });

  it("leaves the focus where the user went as an editor closes once its save applies", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = boardPage(harbourBoard(), { gate });
    await openScript();
    await user.click(
      within(entryOf("Market")).getByRole("button", { name: "Edit Market" }),
    );
    const market = await screen.findByRole("form", { name: "Edit Market" });
    await user.type(
      within(market).getByRole("textbox", { name: "Title" }),
      " square{Enter}",
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // While the save is under way, the user goes on to another node.
    await user.click(
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }),
    );
    const pier = await screen.findByRole("form", { name: "Edit Pier" });
    const title = within(pier).getByRole("textbox", { name: "Title" });
    await waitFor(() => expect(document.activeElement).toBe(title));
    await act(async () => release());
    await waitFor(() =>
      expect(within(entryOf("Market square")).queryByRole("form")).toBeNull(),
    );
    expect(document.activeElement).toBe(title);
  });

  it("leaves the focus where the user went as an editor's save is refused for a field of it", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = boardPage(harbourBoard(), {
      gate,
      refuse: 0,
      refusal: () =>
        refused(422, {
          code: "validation_failed",
          details: {
            errors: [
              {
                location: "body.commands[0].payload.title",
                message: "Not that title",
              },
            ],
          },
        }),
    });
    await openScript();
    await user.click(
      within(entryOf("Market")).getByRole("button", { name: "Edit Market" }),
    );
    const market = await screen.findByRole("form", { name: "Edit Market" });
    await user.type(
      within(market).getByRole("textbox", { name: "Title" }),
      " square{Enter}",
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // While the save is under way, the user goes on to another node.
    await user.click(
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }),
    );
    const pier = await screen.findByRole("form", { name: "Edit Pier" });
    const title = within(pier).getByRole("textbox", { name: "Title" });
    await waitFor(() => expect(document.activeElement).toBe(title));
    await act(async () => release());
    // The refused field says so, and the focus stays where the user is.
    await waitFor(() =>
      expect(
        within(market)
          .getByRole("textbox", { name: "Title" })
          .getAttribute("aria-invalid"),
      ).toBe("true"),
    );
    expect(document.activeElement).toBe(title);
  });

  it("takes the repeats of a key held as an editor opens for nothing typed, until the key goes up", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" }).focus();
    // Enter held on Edit: the editor opens, and the key's repeat, in its
    // title, saves nothing, nor closes it.
    await user.keyboard("{Enter>2/}");
    const form = screen.getByRole("form", { name: "Edit Pier" });
    const title = within(form).getByRole("textbox", { name: "Title" });
    expect(document.activeElement).toBe(title);
    // A key held in the editor repeats as the user types.
    await user.keyboard("{s>3/}");
    expect((title as HTMLInputElement).value).toContain("sss");
    expect(batches(calls)).toEqual([]);
  });

  it("takes the repeats of a key held as an editor closes, on the button that the focus goes back to, for nothing", async () => {
    const user = userEvent.setup();
    const calls = boardPage(harbourBoard());
    await openScript();
    const edit = () =>
      within(entryOf("Pier")).getByRole("button", { name: "Edit Pier" });
    edit().focus();
    await user.keyboard("{Enter}");
    const form = await screen.findByRole("form", { name: "Edit Pier" });
    expect(document.activeElement).toBe(
      within(form).getByRole("textbox", { name: "Title" }),
    );
    // Enter held in the title of no change: the editor closes, with the
    // focus back on Edit, where the key's repeats open nothing.
    await user.keyboard("{Enter>3/}");
    expect(screen.queryByRole("form")).toBeNull();
    expect(document.activeElement).toBe(edit());
    expect(batches(calls)).toEqual([]);
  });

  it("says that a jumper's target was deleted, of a board that the page has read, rather than link it", async () => {
    const board = harbourBoard();
    board.nodes.push(
      testNode("nd_jump", 0, 0, {
        type: "jumper",
        kind: undefined,
        title: "Back to the dock",
        target: { board_id: "bd_a", node_id: "nd_gone" },
        created_at: "2026-11-01T10:05:00Z",
      }),
    );
    boardPage(board);
    await openScript();
    const jumper = entryOf("Back to the dock");
    expect(within(jumper).getByText("Target deleted")).toBeDefined();
    expect(within(jumper).queryByText(/^Continues at/)).toBeNull();
  });

  it("shows the board's nodes that the canvas read again, after the loader's read failed, rather than say again that they could not be loaded", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard(), { failReads: 1 });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const failed = () =>
      screen.queryByText("The board's nodes could not be loaded.", {
        exact: false,
      });
    await waitFor(() => expect(failed()).not.toBeNull());
    await user.click(
      within(failed() as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    );
    await waitFor(() => expect(failed()).toBeNull());
    await user.click(screen.getByRole("link", { name: "Script" }));
    expect(await screen.findByRole("heading", { name: "Quay" })).toBeDefined();
    expect(failed()).toBeNull();
  });

  // Verifies: FR-TEXT-04/AC3
  it("marks the view shown, and it alone, as the page's current one", async () => {
    const user = userEvent.setup();
    boardPage(harbourBoard());
    renderApp(
      `/projects/${testProject.id}/boards/bd_a?view=script&node=nd_market`,
      { session: testSession },
    );
    await screen.findByRole("heading", { name: "Quay" });
    const views = screen.getByRole("navigation", {
      name: "Views of the board",
    });
    const canvas = within(views).getByRole("link", { name: "Canvas" });
    const script = within(views).getByRole("link", { name: "Script" });
    expect(script.getAttribute("aria-current")).toBe("page");
    expect(canvas.getAttribute("aria-current")).toBeNull();
    // Nor is a node's link to the canvas this page, though the address
    // names its node.
    expect(
      within(entryOf("Market"))
        .getByRole("link", { name: "Show on canvas: Market" })
        .getAttribute("aria-current"),
    ).toBeNull();
    // Nor a link to the node's entry, which is no page of its own.
    expect(
      within(entryOf("Quay"))
        .getByRole("link", { name: "Market" })
        .getAttribute("aria-current"),
    ).toBeNull();
    await user.click(canvas);
    await screen.findByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    expect(canvas.getAttribute("aria-current")).toBe("page");
    expect(script.getAttribute("aria-current")).toBeNull();
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

  it("keeps the editors of what refused additions created, with what the user typed in them meanwhile, until they are cancelled, the focus then back on the buttons that asked for them", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const calls = boardPage(harbourBoard(), { refuse: [0, 1], gate });
    await openScript();
    await user.click(
      within(entryOf("Market")).getByRole("button", {
        name: "Add node after Market",
      }),
    );
    const node = await screen.findByRole("form", { name: "Edit Untitled" });
    const text = within(node).getByRole("textbox", {
      name: "Text of Untitled",
    });
    await waitFor(() => expect(document.activeElement).toBe(text));
    typeInto(text, "The gulls cry.");
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Add choice to Quay",
      }),
    );
    const choice = await screen.findByRole("form", {
      name: "Edit label: Choice to Untitled",
    });
    const label = within(choice).getByRole("textbox", { name: "Label" });
    await waitFor(() => expect(document.activeElement).toBe(label));
    typeInto(label, "Hail the ferry");
    await act(async () => release());
    await waitFor(() =>
      expect(
        within(entryOf("Quay")).getByText("validation_failed"),
      ).toBeDefined(),
    );
    expect(
      within(entryOf("Market")).getByText("validation_failed"),
    ).toBeDefined();
    // The same editors, of what was typed, each saying that it cannot be
    // saved, the focus where the user is.
    for (const [form, typed] of [
      [node, "The gulls cry."],
      [choice, "Hail the ferry"],
    ] as const) {
      expect(
        await within(form).findByText(/^This is not on the board/),
      ).toBeDefined();
      expect(within(form).getByText(typed)).toBeDefined();
      expect(within(form).getByRole("button", { name: "Save" })).toHaveProperty(
        "disabled",
        true,
      );
    }
    expect(screen.getByRole("form", { name: "Edit Untitled" })).toBe(node);
    // The choice's form keeps the name that it opened with, though the
    // board no longer has the node that the choice led to either.
    expect(
      screen.getByRole("form", { name: "Edit label: Choice to Untitled" }),
    ).toBe(choice);
    expect(document.activeElement).toBe(label);
    await user.click(within(choice).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("form", { name: "Edit label: Choice to Untitled" }),
      ).toBeNull(),
    );
    expect(document.activeElement).toBe(
      within(entryOf("Quay")).getByRole("button", {
        name: "Add choice to Quay",
      }),
    );
    await user.click(within(node).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
    expect(document.activeElement).toBe(
      within(entryOf("Market")).getByRole("button", {
        name: "Add node after Market",
      }),
    );
    expect(batches(calls)).toHaveLength(2);
  });

  // Verifies: FR-TEXT-04/AC3
  it("closes with Escape the list of an @ that the caret is moved after, and the editor of no change only with the next", async () => {
    const user = userEvent.setup();
    const board = harbourBoard();
    board.connections[0] = {
      ...board.connections[0],
      label: textOf("Walk to the pier @ dawn"),
    };
    boardPage(board);
    await openScript();
    await user.click(
      within(entryOf("Quay")).getByRole("button", {
        name: "Edit label: Walk to the pier @ dawn",
      }),
    );
    const form = await screen.findByRole("form", {
      name: "Edit label: Walk to the pier @ dawn",
    });
    const label = within(form).getByRole("textbox", { name: "Label" });
    await waitFor(() => expect(document.activeElement).toBe(label));
    // After the @, of the paragraph that starts at 1: the list of the
    // components, of nothing changed.
    caretAt(label, 1 + "Walk to the pier @".length);
    expect(
      await within(form).findByRole("listbox", {
        name: "Components to mention",
      }),
    ).toBeDefined();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(within(form).queryByRole("listbox")).toBeNull());
    expect(
      screen.getByRole("form", { name: "Edit label: Walk to the pier @ dawn" }),
    ).toBe(form);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("form")).toBeNull());
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
