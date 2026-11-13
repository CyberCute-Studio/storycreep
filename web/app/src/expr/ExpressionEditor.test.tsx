import type {
  Board,
  Connection,
  Expression,
  Usage,
  Variable,
} from "@storycreep/shared/api";
import { encode, parse } from "@storycreep/shared/expr/compile";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Engine } from "../canvas/engine";
import { renderApp, testSession } from "../test/app";
import { testConnection } from "../test/canvas";
import "../test/editor";
import { fakeApi, refused, startPage, testProject } from "../test/fake";
import { type FakeRealtime, fakeRealtime } from "../test/realtime";
import { character, elara, ids, node, variable, variables } from "./testdata";

// The expression editor on a board's page (PRD FR-EXPR-01, FR-VAR-02 AC1,
// FR-DEP-01 AC3, plan note N74): the condition of a connection, in its
// inspector, with its error marked at the parser's range, what it offers
// to complete, its guided picker, its words, its names and their usages,
// and the variable that it offers to create.

// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of: the page's
// tests draw with an engine that draws nothing.
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

const harbour: Board = {
  id: ids.harbour,
  project_id: testProject.id,
  name: "Harbour",
  slug: "harbour",
  description: "",
  color: "none",
  order: "V",
  rev: 3,
  created_at: at,
  updated_at: at,
};

const dock = "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BF4";
const choice = "cn_01HQ8Z3XK4F7NAWR5TCQ9M2BG1";

/** A site of Harbour's Quay that changes gold. */
const goldWrite: Usage = {
  site: "node_on_enter",
  board_id: ids.harbour,
  board_name: "Harbour",
  node_id: ids.quay,
  node_title: "Quay",
};

/** A site of Harbour's Quay that reads elara.trust. */
const trustRead: Usage = { ...goldWrite, attribute_id: ids.trust };

/** A site of Harbour's Dock that reads elara.mood. */
const moodRead: Usage = {
  site: "node_on_exit",
  board_id: ids.harbour,
  board_name: "Harbour",
  node_id: dock,
  node_title: "Dock",
  attribute_id: ids.mood,
};

/**
 * Answers the board's page, of Quay and Dock, connected by a choice of the
 * condition given, as the server compiled it, valid unless stored says
 * otherwise, each connection.update of which takes it to its next
 * revision, once sending allows, or is refused for its condition where
 * refuseUpdate says so, and refuses each variable.create where
 * refuseCreate says so, or answers it once creating allows, and the read
 * of the components where refuseComponents does; and records what it is
 * asked. Elara's reads are two pages: Dock's of elara.mood, and then
 * Quay's of elara.trust.
 */
function boardPage(
  condition?: string,
  stored: Partial<Expression> = {},
  {
    sending,
    creating,
    refuseCreate = false,
    refuseComponents = false,
    refuseUpdate = false,
  }: {
    sending?: Promise<void>;
    creating?: Promise<void>;
    refuseCreate?: boolean;
    refuseComponents?: boolean;
    refuseUpdate?: boolean;
  } = {},
) {
  let connection: Connection = testConnection(choice, ids.quay, dock, {
    board_id: ids.harbour,
    ...(condition === undefined
      ? {}
      : { condition: { source: condition, is_valid: true, ...stored } }),
  });
  // The variables that the page's batches created, which a read of the
  // variables brings too.
  const created: Variable[] = [];
  return fakeApi(async (operation, input) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listVariables":
        return { items: [...variables, ...created], next_cursor: null };
      case "getVariable": {
        const { path } = input as { path: { variable_id: string } };
        const found = created.find((v) => v.id === path.variable_id);
        if (found === undefined) {
          throw new Error(`unexpected getVariable of ${path.variable_id}`);
        }
        return found;
      }
      case "listComponentTypes":
        return { items: [character], next_cursor: null };
      case "listComponents":
        if (refuseComponents) {
          throw refused(503, { code: "unavailable" });
        }
        return { items: [elara], next_cursor: null };
      case "listBoards":
        return { items: [harbour], next_cursor: null };
      case "listBoardNodes":
        return {
          items: [
            node(ids.quay, ids.harbour, "Quay"),
            node(dock, ids.harbour, "Dock", { position: { x: 400, y: 0 } }),
          ],
          next_cursor: null,
        };
      case "listBoardConnections":
        return { items: [connection], next_cursor: null };
      case "getConnection":
        return connection;
      case "listComponentUsages": {
        const { query } = input as { query: { use: string; cursor?: string } };
        if (query.use !== "read") {
          return { items: [], next_cursor: null };
        }
        return query.cursor === undefined
          ? { items: [moodRead], next_cursor: "trust" }
          : { items: [trustRead], next_cursor: null };
      }
      case "listTags":
        return { items: [] };
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "listVariableUsages": {
        const { path, query } = input as {
          path: { variable_id: string };
          query: { access: string };
        };
        return {
          items:
            path.variable_id === ids.gold && query.access === "write"
              ? [goldWrite]
              : [],
          next_cursor: null,
        };
      }
      case "applyCommands": {
        const { body } = input as {
          body: {
            commands: { type: string; payload: Record<string, unknown> }[];
          };
        };
        if (body.commands.some((c) => c.type === "connection.update")) {
          await sending;
          if (refuseUpdate) {
            throw refused(422, {
              code: "validation_failed",
              details: {
                errors: [
                  {
                    location: "body.commands[0].payload.condition",
                    message: "has more than 16384 characters",
                  },
                ],
              },
            });
          }
        }
        if (body.commands.some((c) => c.type === "variable.create")) {
          await creating;
        }
        if (
          refuseCreate &&
          body.commands.some((c) => c.type === "variable.create")
        ) {
          throw refused(422, { code: "validation_failed" });
        }
        const made = body.commands
          .filter((c) => c.type === "variable.create")
          .map(({ payload }) => {
            const { id = "", key = "", ...more } = payload as Partial<Variable>;
            return variable(id, key, more);
          });
        created.push(...made);
        // The connection of its next revision, of the condition that the
        // batch gave it, as the server compiles it.
        const updates = body.commands.filter(
          (c) => c.type === "connection.update",
        );
        for (const { payload } of updates) {
          const source = payload.condition;
          connection = {
            ...connection,
            rev: connection.rev + 1,
            ...(typeof source === "string"
              ? {
                  condition:
                    source === "" ? undefined : { source, is_valid: true },
                }
              : {}),
          };
        }
        return {
          status: "applied",
          seq: 2,
          // The revisions of what a batch created or changed, which the
          // document keeps then.
          revisions: [
            ...made.map((v) => ({ id: v.id, rev: v.rev })),
            ...(updates.length === 0
              ? []
              : [{ id: connection.id, rev: connection.rev }]),
          ],
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

/** Returns the value of a field. */
function fieldValue(field: HTMLElement): string {
  return (field as HTMLInputElement).value;
}

/** Returns what a list shows as chosen. */
function shownOf(field: HTMLElement): string {
  const select = field as HTMLSelectElement;
  return select.options[select.selectedIndex]?.textContent ?? "";
}

/** Returns the commands of the batches that calls sent, without their IDs. */
function batches(calls: ReturnType<typeof fakeApi>) {
  return calls.mock.calls
    .filter(([operation]) => operation === "applyCommands")
    .map(([, input]) =>
      (
        input as {
          body: {
            commands: { command_id: string; type: string; payload: unknown }[];
          };
        }
      ).body.commands.map(({ command_id: _, ...command }) => command),
    );
}

/** Opens Harbour's page, with Quay's choice's inspector, of the real-time channel given, and returns its form. */
async function openChoice(
  user: ReturnType<typeof userEvent.setup>,
  realtime?: FakeRealtime,
) {
  renderApp(
    `/projects/${testProject.id}/boards/${ids.harbour}?node=${ids.quay}`,
    {
      session: testSession,
      ...(realtime === undefined ? {} : { realtime }),
    },
  );
  const inspector = await screen.findByRole("region", { name: "Quay" });
  await user.click(
    await within(inspector).findByRole("button", { name: "Edit choice 1" }),
  );
  const form = await screen.findByRole("region", { name: "Connection" });
  return {
    form,
    condition: within(form).getByRole("textbox", { name: "Condition" }),
  };
}

describe("the expression editor", () => {
  // Verifies: FR-EXPR-01/AC1
  it("marks the first error of what is typed at the exact range of characters that the parser gives it, and says where it is", async () => {
    const user = userEvent.setup();
    boardPage();
    const { form, condition } = await openChoice(user);
    await user.type(condition, "gld > 1");
    const field = condition.closest(".expr-field") as HTMLElement;
    await waitFor(() =>
      expect(field.querySelector("[data-error]")?.textContent).toBe("gld"),
    );
    expect(condition.getAttribute("aria-invalid")).toBe("true");
    const message = within(form).getByText(
      "No variable or component has this name here. (characters 1 to 3) Did you mean gold?",
    );
    expect(condition.getAttribute("aria-describedby")).toContain(message.id);
    // Once it compiles, nothing is marked.
    await user.clear(condition);
    await user.type(condition, "gold > 1");
    await waitFor(() => expect(field.querySelector("[data-error]")).toBeNull());
    expect(condition.getAttribute("aria-invalid")).toBeNull();
  });

  // Verifies: FR-EXPR-01/AC2
  it("offers the names of the board as they are typed, with their types, what they are, and where a variable is changed, and writes the one chosen", async () => {
    const user = userEvent.setup();
    const calls = boardPage();
    const { condition } = await openChoice(user);
    await user.type(condition, "go");
    const list = await screen.findByRole("listbox", { name: "Suggestions" });
    const gold = within(list).getByRole("option", { name: /^gold/ });
    expect(gold.getAttribute("aria-selected")).toBe("true");
    expect(gold.textContent).toContain("Global variable, whole number");
    await waitFor(() =>
      expect(gold.textContent).toContain(
        "Changed in: The on-enter script of Quay · Harbour",
      ),
    );
    // Of a page of one more site than it tells of, rather than the list's
    // hundred.
    expect(
      calls.mock.calls.some(
        ([operation, input]) =>
          operation === "listVariableUsages" &&
          (input as { query: { limit?: number } }).query.limit === 4,
      ),
    ).toBe(true);
    expect(condition.getAttribute("aria-activedescendant")).toBe(gold.id);
    await user.keyboard("{Enter}");
    expect(fieldValue(condition)).toBe("gold");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    // The name written, shortened, offers again, as it is typed.
    await user.keyboard("{Backspace}");
    await screen.findByRole("listbox", { name: "Suggestions" });
    await user.keyboard("d");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    // A variable of the board and a global one of the same key, which the
    // bare key does not name, as global.trust; and a tap on an offer
    // writes it, leaving the focus in the field.
    await user.type(condition, " > tr");
    const offers = within(
      await screen.findByRole("listbox", { name: "Suggestions" }),
    ).getAllByRole("option");
    expect(offers.map((o) => o.querySelector("code")?.textContent)).toEqual([
      "global.trust",
      "trim(s)",
      "trust",
    ]);
    await user.click(offers[0]);
    expect(fieldValue(condition)).toBe("gold > global.trust");
    expect(document.activeElement).toBe(condition);
    // Escape closes the list, which stays closed while the name is typed on.
    await user.type(condition, " and me");
    await screen.findByRole("listbox", { name: "Suggestions" });
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    await user.type(condition, "t");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
  });

  it('offers the nodes as node("…") is chosen, as a component\'s attributes once its key is', async () => {
    const user = userEvent.setup();
    boardPage();
    const { condition } = await openChoice(user);
    await user.type(condition, "visited(no");
    const list = await screen.findByRole("listbox", { name: "Suggestions" });
    await user.click(within(list).getByRole("option", { name: /^node\(/ }));
    expect(fieldValue(condition)).toBe('visited(node("');
    const nodes = await screen.findByRole("listbox", { name: "Suggestions" });
    expect(
      within(nodes)
        .getAllByRole("option")
        .map((o) => o.querySelector("code")?.textContent),
    ).toEqual(["Dock", "Quay"]);
    expect(document.activeElement).toBe(condition);
  });

  it("scrolls the offer that the arrows highlight into view, as the list's height may hide it", async () => {
    const user = userEvent.setup();
    boardPage();
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    try {
      const { condition } = await openChoice(user);
      await user.type(condition, "tr");
      const list = await screen.findByRole("listbox", { name: "Suggestions" });
      await user.keyboard("{ArrowDown}");
      const second = within(list).getAllByRole("option")[1];
      expect(second.getAttribute("aria-selected")).toBe("true");
      expect(scrolled.mock.contexts).toContain(second);
    } finally {
      delete (Element.prototype as Partial<Element>).scrollIntoView;
    }
  });

  it("opens its list as a name is typed, rather than where the caret comes to one, and not of a name typed whole, so that Enter saves the condition", async () => {
    const user = userEvent.setup();
    const calls = boardPage();
    const { form, condition } = await openChoice(user);
    await user.type(condition, "met_elar");
    await screen.findByRole("listbox", { name: "Suggestions" });
    // The one offer of the name typed whole is what it is already.
    await user.type(condition, "a");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    // As is the board's trust typed whole, of whose offers global.trust,
    // which the list has first, is another, which its Enter would write.
    await user.type(condition, " and trus");
    await screen.findByRole("listbox", { name: "Suggestions" });
    await user.type(condition, "t");
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    await user.type(condition, " > 1");
    // The arrows that bring the caret to a name open nothing, and leave
    // Enter to save the condition.
    await user.keyboard("{Home}{ArrowRight>16/}");
    // In trust, of which tr is before the caret, as global.trust, trim, and
    // trust start.
    expect((condition as HTMLTextAreaElement).selectionStart).toBe(16);
    expect(screen.queryByRole("listbox", { name: "Suggestions" })).toBeNull();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      {
        type: "connection.update",
        payload: { id: choice, condition: "met_elara and trust > 1" },
      },
    ]);
    await within(form).findByText("Saved.");
  });

  it("sends no second save of an Enter while the first is under way, as Enter in a field of one line does not while Save is disabled", async () => {
    const user = userEvent.setup();
    let send = () => {};
    const sending = new Promise<void>((resolve) => {
      send = resolve;
    });
    const calls = boardPage(undefined, {}, { sending });
    const { form, condition } = await openChoice(user);
    await user.type(condition, "gold > 1");
    await user.keyboard("{Enter}");
    const save = within(form).getByRole("button", {
      name: "Save the connection",
    });
    await waitFor(() => expect(save).toHaveProperty("disabled", true));
    await user.keyboard("{Enter}");
    send();
    await within(form).findByText("Saved.");
    // What a second save would have sent, after the first.
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(batches(calls)).toHaveLength(1);
  });

  it("saves a condition, which is one line, of Enter with Shift too, rather than break its line", async () => {
    const user = userEvent.setup();
    const calls = boardPage();
    const { form, condition } = await openChoice(user);
    await user.type(condition, "gold > 1");
    await user.keyboard("{Shift>}{Enter}{/Shift}");
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      { type: "connection.update", payload: { condition: "gold > 1" } },
    ]);
    await within(form).findByText("Saved.");
  });

  it("reads a stored source's line breaks as its text area holds them", async () => {
    const user = userEvent.setup();
    const calls = boardPage("gold\r\n>= 3");
    const { form, condition } = await openChoice(user);
    expect(fieldValue(condition)).toBe("gold\n>= 3");
    expect(
      within(form).getByText("In words").parentElement?.textContent,
    ).toContain("gold is at least 3");
    // The caret stays where the arrows put it, as React writes the text
    // area's value anew at no render.
    await user.click(condition);
    await user.keyboard("{End}{ArrowLeft>3/}");
    expect((condition as HTMLTextAreaElement).selectionStart).toBe(6);
    // What is typed and taken back again is the source as it was, of which
    // Save saves nothing, rather than its line breaks anew.
    await user.keyboard("{End}0{Backspace}");
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await within(form).findByText("Nothing has changed, so nothing was saved.");
    expect(batches(calls)).toHaveLength(0);
  });

  // Verifies: FR-EXPR-01/AC3, FR-EXPR-01/AC4
  it("shows the same condition in the guided picker, which a switch leaves as it is, and which writes one of the same AST", async () => {
    const user = userEvent.setup();
    const calls = boardPage("gold >= 3");
    const { form, condition } = await openChoice(user);
    expect(fieldValue(condition)).toBe("gold >= 3");
    // What it says in words, from its AST.
    expect(
      within(form).getByText("In words").parentElement?.textContent,
    ).toContain("gold is at least 3");
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const picker = within(form).getByRole("group", { name: "Condition" });
    expect(
      shownOf(within(picker).getByRole("combobox", { name: "Value to test" })),
    ).toBe("gold (whole number)");
    expect(
      shownOf(within(picker).getByRole("combobox", { name: "Test" })),
    ).toBe("is at least");
    expect(
      fieldValue(
        within(picker).getByRole("textbox", { name: "Compared with" }),
      ),
    ).toBe("3");
    await user.click(within(form).getByRole("button", { name: "Text" }));
    expect(
      fieldValue(within(form).getByRole("textbox", { name: "Condition" })),
    ).toBe("gold >= 3");
    // A change in the picker writes the source of the condition that it
    // shows, which the text view then shows, and Save sends.
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    await user.selectOptions(
      within(form).getByRole("combobox", { name: "Test" }),
      "is more than",
    );
    const value = within(form).getByRole("textbox", { name: "Compared with" });
    await user.clear(value);
    await user.type(value, "-2");
    await user.click(within(form).getByRole("button", { name: "Text" }));
    const source = within(form).getByRole("textbox", { name: "Condition" });
    expect(fieldValue(source)).toBe("gold > -2");
    expect(
      encode(parse((source as HTMLTextAreaElement).value, "condition")),
    ).toBe(encode(parse("gold>-2", "condition")));
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      {
        type: "connection.update",
        payload: { id: choice, condition: "gold > -2" },
      },
    ]);
  });

  it("refuses to save a number that the picker cannot write yet, rather than save the one before it, and says what it lacks", async () => {
    const user = userEvent.setup();
    const calls = boardPage("gold >= 3");
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const value = within(form).getByRole("textbox", { name: "Compared with" });
    // The on-screen keyboard of text, whose digits have the minus.
    expect(value.getAttribute("inputmode")).toBeNull();
    await user.clear(value);
    await user.type(value, "4x");
    expect(value.getAttribute("aria-invalid")).toBe("true");
    within(form).getByText("Write a number, as 3, 2.5, or -1.");
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(document.activeElement).toBe(value));
    expect(batches(calls)).toHaveLength(0);
    expect(fieldValue(value)).toBe("4x");
    // Once it is a number, Save sends it.
    await user.type(value, "{Backspace}");
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      {
        type: "connection.update",
        payload: { id: choice, condition: "gold >= 4" },
      },
    ]);
  });

  it("saves what the picker wrote, rather than refuse the number that it no longer shows, as once another session took the value that it tests to the trash, with nothing to say why", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const calls = boardPage("gold >= 3");
    const { form } = await openChoice(user, realtime);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const value = within(form).getByRole("textbox", { name: "Compared with" });
    await user.clear(value);
    // Of 4, which writes gold >= 4, and then of 4x, which writes nothing.
    await user.type(value, "4x");
    expect(value.getAttribute("aria-invalid")).toBe("true");
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "variable.delete", payload: { id: ids.gold } }],
        [{ id: ids.gold, rev: 2 }],
      ),
    );
    await waitFor(() => expect(value.isConnected).toBe(false));
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      { type: "connection.update", payload: { condition: "gold >= 4" } },
    ]);
  });

  it("keeps the guided view, and the focus on Save, once a save from it ends and the form is shown anew", async () => {
    const user = userEvent.setup();
    const calls = boardPage("gold >= 3");
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    await user.selectOptions(
      within(form).getByRole("combobox", { name: "Test" }),
      "is more than",
    );
    const save = within(form).getByRole("button", {
      name: "Save the connection",
    });
    await user.click(save);
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // The form of the connection's next revision.
    await waitFor(() => expect(save.isConnected).toBe(false));
    const picker = within(form).getByRole("group", { name: "Condition" });
    expect(
      shownOf(within(picker).getByRole("combobox", { name: "Test" })),
    ).toBe("is more than");
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(form).getByRole("button", { name: "Save the connection" }),
      ),
    );
  });

  it("shows an option that the condition spells with an escape as the option that it tests", async () => {
    const user = userEvent.setup();
    boardPage('elara.mood == "an\\u0067ry"');
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    expect(
      shownOf(within(form).getByRole("combobox", { name: "Compared with" })),
    ).toBe("angry");
  });

  it("cannot show a test that it does not offer of the value's type, as an enum compared by <, rather than show another", async () => {
    const user = userEvent.setup();
    boardPage('elara.mood < "calm"');
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const picker = within(form).getByRole("group", { name: "Condition" });
    within(picker).getByText(/^This condition is more than/);
    expect(within(picker).queryByRole("combobox", { name: "Test" })).toBeNull();
  });

  it("gives the focus to the picker of a condition that a refusal of the save names", async () => {
    const user = userEvent.setup();
    const calls = boardPage("gold >= 3", {}, { refuseUpdate: true });
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    await user.selectOptions(
      within(form).getByRole("combobox", { name: "Test" }),
      "is more than",
    );
    await user.click(
      within(form).getByRole("button", { name: "Save the connection" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const picker = within(form).getByRole("group", { name: "Condition" });
    await waitFor(() => expect(document.activeElement).toBe(picker));
  });

  it("keeps the focus in the picker once a condition that it cannot show is cleared to build one", async () => {
    const user = userEvent.setup();
    boardPage("gold + 1 > 2");
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const picker = within(form).getByRole("group", { name: "Condition" });
    within(picker).getByText(/^This condition is more than/);
    await user.click(
      within(picker).getByRole("button", {
        name: "Clear it and build one here",
      }),
    );
    within(picker).getByRole("combobox", { name: "Value to test" });
    expect(document.activeElement).toBe(picker);
  });

  it("says that it shows a condition once it can check it against the project's names, rather than offer to clear it, where the read failed", async () => {
    const user = userEvent.setup();
    boardPage("gold >= 3", {}, { refuseComponents: true });
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const picker = within(form).getByRole("group", { name: "Condition" });
    within(picker).getByText(
      /^The guided picker shows a condition once it can check it against the project's variables and components/,
    );
    expect(within(picker).queryByRole("button")).toBeNull();
  });

  it("keeps showing what it wrote, of a source that does not compile then, as of a text longer than a string may be", async () => {
    const user = userEvent.setup();
    boardPage('keeper == "a"');
    const { form } = await openChoice(user);
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const value = within(form).getByRole("textbox", { name: "Compared with" });
    await user.clear(value);
    await user.paste("x".repeat(4097));
    expect(value.isConnected).toBe(true);
    expect(fieldValue(value)).toHaveLength(4097);
    await within(form).findByText(
      /^The text in quotes is longer than 4,096 characters\./,
    );
    expect(document.activeElement).toBe(value);
  });

  // Verifies: FR-VAR-02/AC1, FR-DEP-01/AC3
  it("shows what each name resolved to, the board's variable of a key that a global one has too, and its usages and its panel within two presses", async () => {
    const user = userEvent.setup();
    const calls = boardPage("trust >= 1 and gold > 0");
    const { form } = await openChoice(user);
    const names = within(form).getByRole("list", {
      name: "What the names refer to",
    });
    const items = within(names).getAllByRole("listitem");
    expect(items[0].textContent).toContain(
      "trust variable of this board, Harbour, whole number",
    );
    expect(items[1].textContent).toContain(
      "gold global variable, whole number",
    );
    // One press shows where gold is used, as its panel lists it.
    await user.click(
      within(names).getByRole("button", { name: "Show where gold is used" }),
    );
    await within(names).findByRole("link", {
      name: "The on-enter script of Quay",
    });
    expect(
      calls.mock.calls.some(
        ([operation, input]) =>
          operation === "listVariableUsages" &&
          (input as { path: { variable_id: string } }).path.variable_id ===
            ids.gold,
      ),
    ).toBe(true);
    // And one opens its panel, on the variables' page.
    expect(
      within(names)
        .getByRole("link", { name: "Open the variable gold" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/variables?variable=${ids.gold}`);
  });

  it("shows where an attribute is used, of its component's usages those of the attribute alone", async () => {
    const user = userEvent.setup();
    boardPage("elara.trust >= 1");
    const { form } = await openChoice(user);
    const names = within(form).getByRole("list", {
      name: "What the names refer to",
    });
    await user.click(
      within(names).getByRole("button", {
        name: "Show where elara.trust is used",
      }),
    );
    // The first page of Elara's reads has none of elara.trust, which the
    // list says, and reads on from.
    const reads = await within(names).findByRole("region", {
      name: "Read by",
    });
    await within(reads).findByText("None of those read so far.");
    await user.click(within(reads).getByRole("button", { name: "Show more" }));
    await within(reads).findByRole("link", {
      name: "The on-enter script of Quay",
    });
    expect(within(reads).queryByText("None of those read so far.")).toBeNull();
    expect(
      within(names).queryByRole("link", { name: "The on-exit script of Dock" }),
    ).toBeNull();
  });

  it("offers to create a variable of a name that names nothing, of the type that its place suggests, and checks the source against it once it is created", async () => {
    const user = userEvent.setup();
    const calls = boardPage();
    const { form, condition } = await openChoice(user);
    await user.type(condition, "met_mira");
    await user.keyboard("{Escape}");
    const offer = await within(form).findByRole("region", {
      name: "No variable is called met_mira. Create it?",
    });
    expect(shownOf(within(offer).getByRole("combobox", { name: "Type" }))).toBe(
      "true or false",
    );
    await user.click(
      within(offer).getByRole("button", {
        name: "Create met_mira (global, true or false)",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toMatchObject([
      {
        type: "variable.create",
        payload: { key: "met_mira", type: "bool", scope: "global" },
      },
    ]);
    expect(document.activeElement).toBe(condition);
  });

  it("gives the focus back to the picker once a variable is created from its view", async () => {
    const user = userEvent.setup();
    const calls = boardPage();
    const { form, condition } = await openChoice(user);
    await user.type(condition, "met_mira");
    await user.keyboard("{Escape}");
    await user.click(within(form).getByRole("button", { name: "Guided" }));
    const picker = within(form).getByRole("group", { name: "Condition" });
    await user.click(
      within(form).getByRole("button", {
        name: "Create met_mira (global, true or false)",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    await waitFor(() => expect(document.activeElement).toBe(picker));
    // Which shows the condition, of the variable created, now.
    await waitFor(() =>
      expect(
        shownOf(
          within(picker).getByRole("combobox", { name: "Value to test" }),
        ),
      ).toBe("met_mira (true or false)"),
    );
    expect(document.activeElement).toBe(picker);
  });

  it("leaves the focus where the user put it while the creation of a variable was answered", async () => {
    const user = userEvent.setup();
    let create = () => {};
    const creating = new Promise<void>((resolve) => {
      create = resolve;
    });
    const calls = boardPage(undefined, {}, { creating });
    const { form, condition } = await openChoice(user);
    await user.type(condition, "met_mira");
    await user.keyboard("{Escape}");
    await user.click(
      within(form).getByRole("button", {
        name: "Create met_mira (global, true or false)",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const traverse = within(form).getByRole("textbox", { name: "On traverse" });
    await user.click(traverse);
    await act(async () => {
      create();
      // What the answer's focus would be, after it.
      await new Promise((resolve) => setTimeout(resolve, 100));
    });
    expect(document.activeElement).toBe(traverse);
  });

  it("says a refusal of a variable's creation under the offer of the name that it was of alone", async () => {
    const user = userEvent.setup();
    boardPage(undefined, {}, { refuseCreate: true });
    const { form, condition } = await openChoice(user);
    await user.type(condition, "met_mira");
    await user.keyboard("{Escape}");
    const offerOf = (key: string) =>
      within(form).getByRole("region", {
        name: `No variable is called ${key}. Create it?`,
      });
    await user.click(
      within(offerOf("met_mira")).getByRole("button", {
        name: "Create met_mira (global, true or false)",
      }),
    );
    // The offer goes while the page shows the variable created, and the
    // focus with it, and comes again once the creation is refused, with what
    // the refusal says, and the focus on its Create.
    await waitFor(() =>
      expect(within(offerOf("met_mira")).getByRole("alert")).toBeDefined(),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(offerOf("met_mira")).getByRole("button", {
          name: "Create met_mira (global, true or false)",
        }),
      ),
    );
    await user.type(condition, "x");
    const next = await within(form).findByRole("region", {
      name: "No variable is called met_mirax. Create it?",
    });
    expect(within(next).queryByRole("alert")).toBeNull();
  });

  it("shows the server's error of the saved source where it cannot check it whole, and not once it checks it whole and finds it valid, as once the variable that it names is created", async () => {
    const user = userEvent.setup();
    const calls = boardPage("met_mira", {
      is_valid: false,
      error: { code: "unknown_identifier", start: 0, end: 8 },
    });
    const { form, condition } = await openChoice(user);
    await within(form).findByText(
      /^No variable or component has this name here\./,
    );
    await user.click(
      within(form).getByRole("button", {
        name: "Create met_mira (global, true or false)",
      }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    // The form shows the connection as it held it, of the server's error,
    // which the editor, which finds met_mira now, does not show.
    await waitFor(() =>
      expect(
        within(form).queryByText(
          /^No variable or component has this name here\./,
        ),
      ).toBeNull(),
    );
    expect(condition.getAttribute("aria-invalid")).toBeNull();
    expect(fieldValue(condition)).toBe("met_mira");
  });
});
