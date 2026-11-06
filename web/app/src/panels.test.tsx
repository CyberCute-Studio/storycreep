import type {
  Attribute,
  Board,
  Component,
  ComponentType,
  Folder,
  ProjectBody,
  Variable,
} from "@storycreep/shared/api";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { convertedTo, optionsOf, parseValue } from "./content/values";
import { renderApp, testSession } from "./test/app";
import { fakeApi, refused, startPage, testProject } from "./test/fake";

// The panels of a project's variables, components, and component types, as
// the router shows them, over a fake of the API's client: the lists and
// their filters, what each panel's form sends, the rename's preview, the
// guarded deletion, the usage lists, the conversion of an attribute's
// values, and the panel's sheet below 900 pixels (PRD NFR-TOUCH-06).

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const at = "2026-11-01T10:00:00Z";

function board(id: string, name: string, order = "V"): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: id,
    description: "",
    color: "none",
    order,
    rev: 1,
    created_at: at,
    updated_at: at,
  };
}

function folder(id: string, name: string, kind: Folder["kind"]): Folder {
  return {
    id,
    project_id: testProject.id,
    kind,
    name,
    order: "V",
    rev: 1,
    created_at: at,
    updated_at: at,
  };
}

function variable(
  id: string,
  key: string,
  more: Partial<Variable> = {},
): Variable {
  return {
    id,
    project_id: testProject.id,
    key,
    type: "bool",
    default: false,
    scope: "global",
    reset_on_enter: false,
    description: "",
    order: id,
    rev: 3,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

function attribute(
  id: string,
  key: string,
  more: Partial<Attribute> = {},
): Attribute {
  return {
    id,
    component_type_id: "ct_character",
    key,
    name: key,
    type: "int",
    default: 0,
    runtime: true,
    order: id,
    rev: 2,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

const character: ComponentType = {
  id: "ct_character",
  project_id: testProject.id,
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [
    attribute("atr_trust", "trust", { order: "V" }),
    attribute("atr_mood", "mood", {
      type: "enum",
      options: ["calm", "angry"],
      default: "calm",
      runtime: false,
      order: "X",
    }),
  ],
  rev: 1,
  created_at: at,
  updated_at: at,
};

const item: ComponentType = {
  ...character,
  id: "ct_item",
  key: "item",
  name: "Item",
  icon: "item",
  attributes: [],
};

/** The empty description, one empty paragraph, as the editor's empty document is. */
const emptyDescription = { type: "doc", content: [{ type: "paragraph" }] };

function component(
  id: string,
  name: string,
  key: string,
  type = character,
  more: Partial<Component> = {},
): Component {
  return {
    id,
    project_id: testProject.id,
    component_type_id: type.id,
    name,
    key,
    description: emptyDescription as unknown as Component["description"],
    attributes: Object.fromEntries(
      type.attributes.map((a) => [a.id, a.default]),
    ),
    order: id,
    rev: 4,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

/** The component as the list of components answers it, without its description and values. */
function summary(entry: Component) {
  const { description: _, attributes: __, ...rest } = entry;
  return rest;
}

interface Content {
  project?: ProjectBody;
  boards?: Board[];
  folders?: Folder[];
  variables?: Variable[];
  types?: ComponentType[];
  components?: Component[];
}

/** Answers what the project's pages read of content, or undefined for another operation. */
function contentPages(
  operation: string,
  input: unknown,
  content: Content,
): unknown {
  const kind = (input as { query?: { kind?: string } } | undefined)?.query
    ?.kind;
  switch (operation) {
    case "getProject":
      return content.project ?? testProject;
    case "listFolders":
      return {
        items: (content.folders ?? []).filter((f) => f.kind === kind),
        next_cursor: null,
      };
    case "listBoards":
      return { items: content.boards ?? [], next_cursor: null };
    case "listVariables":
      return { items: content.variables ?? [], next_cursor: null };
    case "listComponentTypes":
      return { items: content.types ?? [], next_cursor: null };
    case "listComponents":
      return {
        items: (content.components ?? []).map(summary),
        next_cursor: null,
      };
    case "getComponent": {
      const id = (input as { path: { component_id: string } }).path
        .component_id;
      return content.components?.find((c) => c.id === id);
    }
  }
  return startPage(operation as Parameters<typeof startPage>[0]);
}

/** Answers with answer, or fails the test's request of an operation that it does not expect. */
function expected(answer: unknown, operation: string): unknown {
  if (answer === undefined) {
    throw new Error(`unexpected ${operation}`);
  }
  return answer;
}

/** Returns the answer to a batch that applied as seq, leaving revisions. */
function applied(seq: number, revisions: { id: string; rev: number }[] = []) {
  return { status: "applied", seq, revisions, not_reconnected: [], undo: [] };
}

type Sent = { type: string; payload: unknown; expected_rev?: unknown }[];

/** Returns the commands of the batches that calls, a fake of the client, sent. */
function batches(calls: ReturnType<typeof fakeApi>): Sent[] {
  return calls.mock.calls
    .filter(([operation]) => operation === "applyCommands")
    .map(([, input]) => (input as { body: { commands: Sent } }).body.commands);
}

/** Makes the window as wide as width, as matchMedia tells the page. */
function windowOf(width: number) {
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === "(min-width: 900px)" && width >= 900,
    media: query,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  }));
  return {
    resize: (next: number) => {
      width = next;
      for (const listener of listeners) {
        listener();
      }
    },
  };
}

describe("typed values", () => {
  it("reads a value of each type as the server does, or none", () => {
    expect(parseValue("bool", "true")).toBe(true);
    expect(parseValue("bool", "yes")).toBeUndefined();
    expect(parseValue("int", " -12 ")).toBe(-12);
    expect(parseValue("int", "1.5")).toBeUndefined();
    expect(parseValue("int", "1e3")).toBeUndefined();
    expect(parseValue("int", "9007199254740991")).toBe(9007199254740991);
    expect(parseValue("int", "9007199254740992")).toBeUndefined();
    expect(parseValue("int", "")).toBeUndefined();
    expect(parseValue("float", "0.5")).toBe(0.5);
    expect(parseValue("float", ".5")).toBe(0.5);
    expect(parseValue("float", "1e3")).toBe(1000);
    expect(parseValue("float", "1e400")).toBeUndefined();
    expect(parseValue("float", "inf")).toBeUndefined();
    expect(parseValue("float", "0x10")).toBeUndefined();
    expect(parseValue("string", " a\tb ")).toBe(" a\tb ");
    expect(parseValue("enum", "calm", ["calm", "angry"])).toBe("calm");
    expect(parseValue("enum", "sad", ["calm", "angry"])).toBeUndefined();
  });

  it("keeps a value that the new type spells as its type changes, and gives it the zero value otherwise", () => {
    expect(convertedTo("float", 3)).toBe(3);
    expect(convertedTo("string", 3)).toBe("3");
    expect(convertedTo("int", "12")).toBe(12);
    expect(convertedTo("int", "twelve")).toBe(0);
    expect(convertedTo("bool", 1)).toBe(false);
    expect(convertedTo("enum", "angry", ["calm", "angry"])).toBe("angry");
    expect(convertedTo("enum", "sad", ["calm", "angry"])).toBe("calm");
  });

  it("reads an enum's options one to a line, without the white space around them and the blank lines", () => {
    expect(optionsOf(" calm \n\nangry\n  \n")).toEqual(["calm", "angry"]);
  });
});

describe("the variables", () => {
  const content: Content = {
    boards: [board("bd_harbour", "Harbour")],
    folders: [folder("fld_quests", "Quests", "variable")],
    variables: [
      variable("var_met", "met_elara", { description: "Elara spoke to us" }),
      variable("var_rep", "rep", { type: "int", default: 0 }),
      variable("var_tries", "tries", {
        type: "int",
        default: 3,
        scope: "board",
        board_id: "bd_harbour",
        folder_id: "fld_quests",
      }),
    ],
  };

  it("lists them in their folders, and narrows the list to those whose key or description holds the filter's text, and to a scope", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/variables`, {
      session: testSession,
    });
    const list = await screen.findByRole("list", { name: "All variables" });
    expect(within(list).getByRole("button", { name: /^met_elara/ })).toBeDefined();
    expect(
      within(list).getByRole("button", { name: "Quests", expanded: true }),
    ).toBeDefined();
    expect(within(list).getByRole("button", { name: /^tries/ })).toBeDefined();
    expect(screen.getByText("3 variables")).toBeDefined();
    await user.type(screen.getByLabelText("Filter by key or description"), "SPOKE");
    await waitFor(() =>
      expect(screen.getByText("1 of 3 variables matches.")).toBeDefined(),
    );
    expect(
      within(screen.getByRole("list", { name: "All variables" })).getAllByRole(
        "button",
      ),
    ).toHaveLength(1);
    await user.clear(screen.getByLabelText("Filter by key or description"));
    await user.selectOptions(screen.getByLabelText("Scope"), "bd_harbour");
    const narrowed = screen.getByRole("list", { name: "All variables" });
    await waitFor(() =>
      expect(within(narrowed).getAllByRole("button")).toHaveLength(1),
    );
    // A variable that a filter finds says which folder it is in.
    expect(within(narrowed).getByText("In Quests")).toBeDefined();
  });

  // Verifies: FR-VAR-02/AC5
  it("says in plain words what becomes of a variable's value, next to its reset_on_enter, as the flag and the scope change", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_tries`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "tries" });
    const flag = within(panel).getByRole("checkbox", {
      name: "Reset to its default each time the story enters its board",
    });
    expect(flag).toHaveProperty("checked", false);
    const said = () =>
      document.getElementById(flag.getAttribute("aria-describedby") ?? "")
        ?.textContent;
    expect(said()).toBe(
      "Off: it keeps its value, and saved games keep it too. Each time the story comes back to Harbour, it finds the value as the story last left it.",
    );
    await user.click(flag);
    expect(said()).toBe(
      "On: it goes back to its default each time the story enters Harbour, so it holds only what happens on the current visit. Saved games keep its value until then.",
    );
    await user.click(within(panel).getByRole("radio", { name: "Global" }));
    expect(flag).toHaveProperty("disabled", true);
    expect(flag).toHaveProperty("checked", false);
    expect(said()).toBe(
      "A global variable keeps its value for the whole story, and saved games keep it too. Only a board's variable can be reset as its board is entered.",
    );
  });

  it("sends what of a variable changed in one batch, unsetting reset_on_enter before it becomes global", async () => {
    const user = userEvent.setup();
    const resetting: Content = {
      ...content,
      variables: [
        variable("var_tries", "tries", {
          type: "int",
          default: 3,
          scope: "board",
          board_id: "bd_harbour",
          reset_on_enter: true,
        }),
      ],
    };
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1, [{ id: "var_tries", rev: 4 }])
        : expected(contentPages(operation, input, resetting), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_tries`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "tries" });
    await user.click(within(panel).getByRole("radio", { name: "Global" }));
    await user.selectOptions(within(panel).getByLabelText("Type"), "float");
    const value = within(panel).getByLabelText("Default");
    expect(value).toHaveProperty("value", "3");
    await user.clear(value);
    await user.type(value, "2.5");
    await user.type(within(panel).getByLabelText("Description"), "Tries left");
    await user.selectOptions(within(panel).getByLabelText("Folder"), "fld_quests");
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    await within(panel).findByText("Saved.");
    const expected_rev = { var_tries: 3 };
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "variable.set_reset_on_enter",
          payload: { id: "var_tries", reset_on_enter: false },
          expected_rev,
        }),
        expect.objectContaining({
          type: "variable.set_scope",
          payload: { id: "var_tries", scope: "global" },
          expected_rev,
        }),
        expect.objectContaining({
          type: "variable.retype",
          payload: { id: "var_tries", type: "float", default: 2.5 },
          expected_rev,
        }),
        expect.objectContaining({
          type: "variable.describe",
          payload: { id: "var_tries", description: "Tries left" },
          expected_rev,
        }),
        expect.objectContaining({
          type: "variable.move",
          payload: { id: "var_tries", folder_id: "fld_quests" },
          expected_rev,
        }),
      ],
    ]);
  });

  it("refuses a default that is none of the variable's type before it sends anything", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    const value = within(panel).getByLabelText("Default");
    await user.clear(value);
    await user.type(value, "3.5");
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    expect(
      await within(panel).findByText("Enter a whole number, such as 3 or -12."),
    ).toBeDefined();
    await waitFor(() => expect(document.activeElement).toBe(value));
    expect(value.getAttribute("aria-invalid")).toBe("true");
    expect(batches(calls)).toEqual([]);
  });

  it("previews what a rename rewrites, and renames once the preview of the key is read", async () => {
    const user = userEvent.setup();
    const site = {
      board_id: "bd_harbour",
      board_name: "Harbour",
      node_id: "nd_gate",
      node_title: "The gate",
      site: "connection_condition",
      connection_id: "cn_open",
      source: "rep >= 3",
      rewritten: "trust >= 3",
      in_trash: false,
      too_long: false,
      names: [],
    };
    const calls = fakeApi((operation, input) => {
      switch (operation) {
        case "previewVariableRename": {
          const { rewrite } = (input as { query: { rewrite: string } }).query;
          return {
            items: rewrite === "renamed" ? [site] : [],
            next_cursor: null,
          };
        }
        case "applyCommands":
          return applied(testProject.seq + 1, [{ id: "var_rep", rev: 4 }]);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(within(panel).getByRole("button", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog", { name: "Rename rep" });
    const key = within(dialog).getByLabelText("New key");
    await user.clear(key);
    await user.type(key, "trust");
    await user.click(
      within(dialog).getByRole("button", { name: "Show what changes" }),
    );
    expect(await within(dialog).findByText("trust >= 3")).toBeDefined();
    expect(within(dialog).getByText("rep >= 3")).toBeDefined();
    expect(
      within(dialog).getByText(
        "The condition of a choice from The gate · Harbour",
      ),
    ).toBeDefined();
    expect(
      within(dialog).getByText("No other name needs a qualifier."),
    ).toBeDefined();
    expect(batches(calls)).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "variable.rename",
          payload: { id: "var_rep", key: "trust" },
          expected_rev: { var_rep: 3 },
        }),
      ],
    ]);
  });

  it("does not rename while a rewritten expression would be too long", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      if (operation === "previewVariableRename") {
        const { rewrite } = (input as { query: { rewrite: string } }).query;
        if (rewrite === "qualified") {
          return { items: [], next_cursor: null };
        }
        return {
          items: [
            {
              board_id: "bd_harbour",
              board_name: "Harbour",
              node_id: "nd_gate",
              node_title: "",
              site: "node_on_enter",
              source: "rep = 1",
              rewritten: "a_much_longer_key = 1",
              in_trash: true,
              too_long: true,
              names: [],
            },
          ],
          next_cursor: null,
        };
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(within(panel).getByRole("button", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const key = within(dialog).getByLabelText("New key");
    await user.clear(key);
    await user.type(key, "a_much_longer_key");
    await user.click(
      within(dialog).getByRole("button", { name: "Show what changes" }),
    );
    expect(
      await within(dialog).findByText(
        "Renamed, this expression would be too long.",
      ),
    ).toBeDefined();
    expect(
      within(dialog).getByText(
        "The on-enter script of A node without a title · Harbour · in the trash",
      ),
    ).toBeDefined();
    expect(
      within(dialog).getByRole("button", { name: "Rename" }),
    ).toHaveProperty("disabled", true);
  });

  it("lists what uses a variable when its deletion is refused, and deletes it anyway for a role that may publish", async () => {
    const user = userEvent.setup();
    const usage = {
      use: "read",
      site: "connection_condition",
      board_id: "bd_harbour",
      board_name: "Harbour",
      node_id: "nd_gate",
      node_title: "The gate",
      connection_id: "cn_open",
    };
    const calls = fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const [command] = (
          input as { body: { commands: { payload: { force?: boolean } }[] } }
        ).body.commands;
        if (command.payload.force !== true) {
          throw refused(409, {
            code: "variable_in_use",
            details: {
              usage_count: 101,
              counts: { read: 100, write: 1 },
              usages: [usage],
            },
          });
        }
        return applied(testProject.seq + 1);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(
      within(panel).getByRole("button", { name: "Delete the variable" }),
    );
    const dialog = await screen.findByRole("alertdialog", {
      name: "Delete rep?",
    });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(
      await within(dialog).findByText(
        "101 parts of the project use it: 100 reading it, and 1 changing it. So it was not deleted.",
      ),
    ).toBeDefined();
    const listed = within(dialog).getByRole("list", { name: "What uses rep" });
    expect(listed.textContent).toBe(
      "Reads · The condition of a choice from The gate · Harbour",
    );
    expect(
      within(dialog).getByText(
        "Only the first 1 are listed. Where it is used lists them all.",
      ),
    ).toBeDefined();
    await user.click(
      within(dialog).getByRole("button", { name: "Delete it anyway" }),
    );
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(screen.queryByRole("region", { name: "rep" })).toBeNull();
    expect(batches(calls).map((batch) => batch[0].payload)).toEqual([
      { id: "var_rep" },
      { id: "var_rep", force: true },
    ]);
  });

  it("offers no deletion anyway to a role that may not publish", async () => {
    const user = userEvent.setup();
    const editor: ProjectBody = {
      ...testProject,
      role: "editor",
      publish_min_role: "admin",
    };
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        throw refused(409, {
          code: "variable_in_use",
          details: { usage_count: 1, counts: { write: 1 }, usages: [] },
        });
      }
      return expected(
        contentPages(operation, input, { ...content, project: editor }),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(
      within(panel).getByRole("button", { name: "Delete the variable" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(
      await within(dialog).findByText(
        "Only a role that may publish the project can delete it anyway. Change those parts first, or ask someone who may.",
      ),
    ).toBeDefined();
    expect(
      within(dialog).queryByRole("button", { name: "Delete it anyway" }),
    ).toBeNull();
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeDefined();
  });

  it("lists where a variable is read and where it is written, once asked", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      if (operation === "listVariableUsages") {
        const { access } = (input as { query: { access: string } }).query;
        return {
          items:
            access === "read"
              ? [
                  {
                    site: "node_text",
                    board_id: "bd_harbour",
                    board_name: "Harbour",
                    node_id: "nd_gate",
                    node_title: "The gate",
                  },
                ]
              : [],
          next_cursor: null,
        };
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(
      within(panel).getByRole("button", { name: "Show where it is used" }),
    );
    const reads = await within(panel).findByRole("list", { name: "Read by" });
    expect(reads.textContent).toBe("The text of The gate · Harbour");
    expect(within(reads).getByRole("link", { name: "Harbour" })).toHaveProperty(
      "pathname",
      `/app/projects/${testProject.id}/boards/bd_harbour`,
    );
    expect(await within(panel).findByText("Nothing changes it.")).toBeDefined();
  });

  it("creates a variable in the folder that the folder's menu was opened for, and opens its panel", async () => {
    const user = userEvent.setup();
    // The variable as the server creates it, which the document reads
    // once its batch has applied, for the key of order that the server
    // picks.
    let created: Variable | undefined;
    const calls = fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const [command] = (
          input as {
            body: { commands: { payload: Partial<Variable> & { id: string } }[] };
          }
        ).body.commands;
        created = variable(command.payload.id, command.payload.key ?? "", {
          ...command.payload,
          rev: 1,
        });
        return applied(testProject.seq + 1, [{ id: created.id, rev: 1 }]);
      }
      if (operation === "getVariable") {
        return created;
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables`, {
      session: testSession,
    });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Quests" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "New variable here…" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "New variable" });
    await user.type(within(dialog).getByLabelText("Key"), "door_open");
    await user.click(within(dialog).getByRole("radio", { name: /^One board/ }));
    await user.click(
      within(dialog).getByRole("checkbox", { name: /^Reset to its default/ }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create the variable" }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "New variable" })).toBeNull(),
    );
    expect(await screen.findByRole("region", { name: "door_open" })).toBeDefined();
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "variable.create",
          payload: expect.objectContaining({
            key: "door_open",
            type: "bool",
            default: false,
            scope: "board",
            board_id: "bd_harbour",
            reset_on_enter: true,
            folder_id: "fld_quests",
          }),
        }),
      ],
    ]);
  });

  it("shows a variable as a viewer reads it, with what becomes of its value", async () => {
    fakeApi((operation, input) =>
      expected(
        contentPages(operation, input, {
          ...content,
          project: { ...testProject, role: "viewer" },
        }),
        operation,
      ),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_tries`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "tries" });
    expect(within(panel).queryByRole("textbox")).toBeNull();
    expect(
      within(panel).getByText(
        "Off: it keeps its value, and saved games keep it too. Each time the story comes back to Harbour, it finds the value as the story last left it.",
      ),
    ).toBeDefined();
    expect(within(panel).queryByRole("button", { name: "Rename…" })).toBeNull();
  });
});

describe("the panel", () => {
  const content: Content = {
    variables: [variable("var_met", "met_elara"), variable("var_rep", "rep")],
  };

  it("is docked beside the list from 900 pixels, and a modal sheet over the page below, the same element either way", async () => {
    const user = userEvent.setup();
    const window = windowOf(640);
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const sheet = await screen.findByRole("dialog", { name: "rep" });
    expect(sheet.getAttribute("aria-modal")).toBe("true");
    await waitFor(() =>
      expect(document.activeElement?.textContent).toBe("rep"),
    );
    // The rest of the page is inert while the sheet is open.
    const list = screen.getByRole("list", { hidden: true, name: "All variables" });
    expect(list.closest("[inert]")).not.toBeNull();
    const description = within(sheet).getByLabelText("Description");
    await user.type(description, "kept");
    // The tablet turns: the sheet is docked now, with what was typed.
    act(() => window.resize(1024));
    const region = screen.getByRole("region", { name: "rep" });
    expect(region).toBe(sheet);
    expect(within(region).getByLabelText("Description")).toHaveProperty(
      "value",
      "kept",
    );
    expect(list.closest("[inert]")).toBeNull();
    act(() => window.resize(640));
    // Escape closes the sheet, and the focus goes back to the variable's item.
    fireEvent.keyDown(within(sheet).getByLabelText("Description"), {
      key: "Escape",
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "rep" })).toBeNull(),
    );
    expect(document.activeElement?.getAttribute("data-item-id")).toBe(
      "var_rep",
    );
    expect(list.closest("[inert]")).toBeNull();
  });
});

describe("the components", () => {
  const elara = component("cmp_elara", "Elara", "elara");
  const content: Content = {
    types: [character, item],
    folders: [folder("fld_cast", "Cast", "component")],
    components: [
      elara,
      component("cmp_lamp", "Lamp", "lamp", item, { folder_id: "fld_cast" }),
    ],
  };

  it("lists them, and narrows the list by name or key, without regard to case, and by type", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/components`, {
      session: testSession,
    });
    const list = await screen.findByRole("list", { name: "All components" });
    expect(within(list).getByRole("button", { name: /^Elara/ })).toBeDefined();
    await user.type(screen.getByLabelText("Filter by name or key"), "LAM");
    await waitFor(() =>
      expect(screen.getByText("1 of 2 components matches.")).toBeDefined(),
    );
    await user.clear(screen.getByLabelText("Filter by name or key"));
    await user.selectOptions(screen.getByLabelText("Type"), "ct_character");
    await waitFor(() =>
      expect(
        within(
          screen.getByRole("list", { name: "All components" }),
        ).getAllByRole("button"),
      ).toHaveLength(1),
    );
  });

  it("reads a component whole as its panel opens, and sends the values that changed", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1, [{ id: "cmp_elara", rev: 5 }])
        : expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    const trust = await within(panel).findByLabelText("trust");
    expect(
      calls.mock.calls.filter(([operation]) => operation === "getComponent"),
    ).toHaveLength(1);
    await user.clear(trust);
    await user.type(trust, "5");
    await user.selectOptions(within(panel).getByLabelText("mood"), "angry");
    await user.click(
      within(panel).getByRole("button", { name: "Save the component" }),
    );
    await within(panel).findByText("Saved.");
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "component.set_attribute",
          payload: { id: "cmp_elara", attribute_id: "atr_trust", value: 5 },
          expected_rev: { cmp_elara: 4 },
        }),
        expect.objectContaining({
          type: "component.set_attribute",
          payload: { id: "cmp_elara", attribute_id: "atr_mood", value: "angry" },
          expected_rev: { cmp_elara: 4 },
        }),
      ],
    ]);
  });

  it("shows the server's refusal of a value at the field of its attribute", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        throw refused(422, {
          code: "validation_failed",
          details: {
            errors: [
              {
                location: "body.commands[0].payload.value",
                message: "is too large",
              },
            ],
          },
        });
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    const trust = await within(panel).findByLabelText("trust");
    await user.clear(trust);
    await user.type(trust, "7");
    await user.click(
      within(panel).getByRole("button", { name: "Save the component" }),
    );
    expect(await within(panel).findByText("Is too large.")).toBeDefined();
    expect(
      within(panel).getByLabelText("trust").getAttribute("aria-invalid"),
    ).toBe("true");
  });

  it("lists what uses a component when its deletion is refused, by use", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        throw refused(409, {
          code: "component_in_use",
          details: {
            usage_count: 2,
            counts: { speaker: 1, read: 1 },
            usages: [
              {
                use: "speaker",
                site: "node_speaker",
                board_id: "bd_harbour",
                board_name: "Harbour",
                node_id: "nd_hello",
                node_title: "Hello",
              },
              {
                use: "read",
                site: "component_description",
                component_id: "cmp_lamp",
                attribute_id: "atr_trust",
              },
            ],
          },
        });
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    await user.click(
      within(panel).getByRole("button", { name: "Delete the component" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(
      await within(dialog).findByText(
        "2 parts of the project use it: 1 as a speaker, 0 in mentions, 1 reading its attributes, and 0 changing them. So it was not deleted.",
      ),
    ).toBeDefined();
    const listed = within(dialog).getByRole("list", { name: "What uses Elara" });
    expect(within(listed).getAllByRole("listitem").map((i) => i.textContent))
      .toEqual([
        "Speaker · The speaker of Hello · Harbour",
        "Reads · The description of Lamp · Open the component",
      ]);
  });
});

describe("the component types", () => {
  const content: Content = { types: [character] };

  it("lists the components whose values would not convert before an attribute's type changes", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation, input) => {
      switch (operation) {
        case "listUnconvertibleValues":
          return {
            items: [
              {
                component_id: "cmp_elara",
                key: "elara",
                name: "Elara",
                deleted_at: at,
              },
            ],
            next_cursor: null,
          };
        case "applyCommands":
          return applied(testProject.seq + 1);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/component-types?type=ct_character`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Character" });
    await user.click(
      within(panel).getByRole("button", { name: "Actions of mood" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Change the type…" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Change the type of mood",
    });
    await user.selectOptions(
      within(dialog).getByLabelText("New type"),
      "bool",
    );
    expect(
      await within(dialog).findByText("Elara (elara), in the trash"),
    ).toBeDefined();
    const asked = calls.mock.calls.find(
      ([operation]) => operation === "listUnconvertibleValues",
    )?.[1] as { query: { type: string } };
    expect(asked.query.type).toBe("bool");
    await user.click(
      within(dialog).getByRole("radio", {
        name: "Give every value the new default",
      }),
    );
    expect(within(dialog).queryByText("Elara (elara), in the trash")).toBeNull();
    await user.click(
      within(dialog).getByRole("button", { name: "Change the type" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "attribute.retype",
          payload: {
            id: "atr_mood",
            type: "bool",
            conversion: "reset",
            default: false,
          },
          expected_rev: { atr_mood: 2 },
        }),
      ],
    ]);
  });

  it("moves an attribute up, between the keys of those that it goes between", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1)
        : expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/component-types?type=ct_character`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Character" });
    await user.click(
      within(panel).getByRole("button", { name: "Actions of mood" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Move up" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[move]] = batches(calls);
    expect(move.type).toBe("attribute.move");
    const { order } = move.payload as { order: string };
    // Before trust, whose key is V.
    expect(order < "V").toBe(true);
  });

  it("adds an attribute of an enum, whose default is one of its options", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1)
        : expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/component-types?type=ct_character`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Character" });
    await user.click(
      within(panel).getByRole("button", { name: "Add an attribute…" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "New attribute of Character",
    });
    await user.type(within(dialog).getByLabelText("Name"), "Rank");
    await user.selectOptions(within(dialog).getByLabelText("Type"), "enum");
    await user.type(within(dialog).getByLabelText("Options"), "low\nhigh");
    await user.selectOptions(within(dialog).getByLabelText("Default"), "high");
    expect(
      within(dialog).getByRole("checkbox", { name: "Part of the game's state" }),
    ).toHaveProperty("disabled", false);
    await user.click(
      within(dialog).getByRole("button", { name: "Add the attribute" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "attribute.create",
          payload: expect.objectContaining({
            component_type_id: "ct_character",
            name: "Rank",
            type: "enum",
            options: ["low", "high"],
            default: "high",
          }),
        }),
      ],
    ]);
  });
});
