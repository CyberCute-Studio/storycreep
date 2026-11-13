import type { Board, Connection, Usage } from "@storycreep/shared/api";
import { encode, parse } from "@storycreep/shared/expr/compile";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Engine } from "../canvas/engine";
import { renderApp, testSession } from "../test/app";
import { testConnection } from "../test/canvas";
import "../test/editor";
import { fakeApi, startPage, testProject } from "../test/fake";
import { character, elara, ids, node, variables } from "./testdata";

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

/** Answers the board's page, of Quay and Dock, connected by a choice of the condition given, and records what it is asked. */
function boardPage(condition?: string) {
  const connection: Connection = testConnection(choice, ids.quay, dock, {
    board_id: ids.harbour,
    ...(condition === undefined
      ? {}
      : { condition: { source: condition, is_valid: true } }),
  });
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listVariables":
        return { items: variables, next_cursor: null };
      case "listComponentTypes":
        return { items: [character], next_cursor: null };
      case "listComponents":
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
      case "applyCommands":
        return {
          status: "applied",
          seq: 2,
          revisions: [],
          not_reconnected: [],
          undo: [],
        };
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

/** Opens Harbour's page, with Quay's choice's inspector, and returns its form. */
async function openChoice(user: ReturnType<typeof userEvent.setup>) {
  renderApp(
    `/projects/${testProject.id}/boards/${ids.harbour}?node=${ids.quay}`,
    {
      session: testSession,
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
    boardPage();
    const { condition } = await openChoice(user);
    await user.type(condition, "go");
    const list = await screen.findByRole("listbox", { name: "Suggestions" });
    const gold = within(list).getByRole("option", { name: /^gold/ });
    expect(gold.getAttribute("aria-selected")).toBe("true");
    expect(gold.textContent).toContain("Global variable, whole number");
    await waitFor(() =>
      expect(gold.textContent).toContain("Changed in Harbour: Quay"),
    );
    expect(condition.getAttribute("aria-activedescendant")).toBe(gold.id);
    await user.keyboard("{Enter}");
    expect(fieldValue(condition)).toBe("gold");
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
        name: "Create met_mira, a global true or false",
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
});
