import {
  type Attribute,
  type Board,
  type Component,
  type ComponentType,
  type Folder,
  NetworkError,
  type ProjectBody,
  type Variable,
} from "@storycreep/shared/api";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { convertedTo, optionsOf, parseValue } from "./content/values";
import { renderApp, testSession } from "./test/app";
import "./test/editor";
import { fakeApi, refused, startPage, testProject } from "./test/fake";
import { fakeRealtime } from "./test/realtime";

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

/** A description of one paragraph of text. */
function paragraph(text: string): Component["description"] {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }],
  } as unknown as Component["description"];
}

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

/**
 * Returns the server's refusal of a batch of one command of type for the
 * revision of state, which changed elsewhere since the page read it at
 * expected, with state as it is now.
 */
function conflict(
  type: string,
  state: { id: string; rev: number },
  expected: number,
) {
  return refused(409, {
    code: "revision_conflict",
    details: {
      conflicts: [
        {
          command: 0,
          command_id: "00000000-0000-7000-8000-000000000001",
          type,
          id: state.id,
          expected_rev: expected,
          exists: true,
          current_rev: state.rev,
        },
      ],
      states: { [state.id]: state },
    },
  });
}

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
    // A media query list follows the window, as a browser's does.
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
    expect(
      within(list).getByRole("button", { name: /^met_elara/ }),
    ).toBeDefined();
    expect(
      within(list).getByRole("button", { name: "Quests", expanded: true }),
    ).toBeDefined();
    expect(within(list).getByRole("button", { name: /^tries/ })).toBeDefined();
    expect(screen.getByText("3 variables")).toBeDefined();
    await user.type(
      screen.getByLabelText("Filter by key or description"),
      "SPOKE",
    );
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
    await user.selectOptions(
      within(panel).getByLabelText("Folder"),
      "fld_quests",
    );
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

  it("keeps what the user changed once a save refused for a change made elsewhere is saved again, and what the user did not change as it was made elsewhere", async () => {
    const user = userEvent.setup();
    // The other session changed the default too, which the user did not,
    // and which saving again leaves as it made it.
    const theirs = variable("var_rep", "rep", {
      type: "int",
      default: 5,
      description: "Standing with the guild",
      rev: 4,
    });
    const sent: Sent[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        sent.push((input as { body: { commands: Sent } }).body.commands);
        if (sent.length === 1) {
          throw conflict("variable.describe", theirs, 3);
        }
        return applied(testProject.seq + 1, [{ id: "var_rep", rev: 5 }]);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.type(
      within(panel).getByLabelText("Description"),
      "Standing in town",
    );
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    expect(
      await within(panel).findByText(
        "Description: now “Standing with the guild”, yours “Standing in town”",
      ),
    ).toBeDefined();
    expect(within(panel).getByLabelText("Default")).toHaveProperty(
      "value",
      "5",
    );
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    await within(panel).findByText("Saved.");
    expect(sent[1]).toEqual([
      expect.objectContaining({
        type: "variable.describe",
        payload: { id: "var_rep", description: "Standing in town" },
        expected_rev: { var_rep: 4 },
      }),
    ]);
  });

  it("leaves a variable whose folder the list does not have in it, rather than move it to the top unasked", async () => {
    const user = userEvent.setup();
    const filed: Content = {
      ...content,
      variables: [
        variable("var_rep", "rep", {
          type: "int",
          default: 0,
          folder_id: "fld_unread",
        }),
      ],
    };
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1, [{ id: "var_rep", rev: 4 }])
        : expected(contentPages(operation, input, filed), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.type(within(panel).getByLabelText("Description"), "Standing");
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    await within(panel).findByText("Saved.");
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "variable.describe",
          payload: { id: "var_rep", description: "Standing" },
        }),
      ],
    ]);
  });

  it("moves a variable whose folder the list does not have to the top once the user chooses the top", async () => {
    const user = userEvent.setup();
    const filed: Content = {
      ...content,
      variables: [
        variable("var_rep", "rep", {
          type: "int",
          default: 0,
          folder_id: "fld_unread",
        }),
      ],
    };
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1, [{ id: "var_rep", rev: 4 }])
        : expected(contentPages(operation, input, filed), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    const field = within(panel).getByLabelText("Folder");
    await user.selectOptions(field, "fld_quests");
    await user.selectOptions(field, "None, at the top");
    await user.click(
      within(panel).getByRole("button", { name: "Save the variable" }),
    );
    await within(panel).findByText("Saved.");
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "variable.move",
          payload: { id: "var_rep" },
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

  it("reads a rename's preview again once its read failed, as the user asks for it again", async () => {
    const user = userEvent.setup();
    // No answer comes to the preview's reads, as while the browser has no
    // connection, until the test says so.
    let failing = true;
    fakeApi((operation, input) => {
      if (operation === "previewVariableRename") {
        if (failing) {
          throw new NetworkError(new TypeError("Failed to fetch"));
        }
        return { items: [], next_cursor: null };
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
    expect(
      await within(dialog).findByText(
        "The server could not be reached. Check your connection, and try again.",
      ),
    ).toBeDefined();
    failing = false;
    await user.click(
      within(dialog).getByRole("button", { name: "Show what changes" }),
    );
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "Rename" }),
      ).toHaveProperty("disabled", false),
    );
  });

  it("renames only once the preview has been read again, rather than as a read of the same key before had it", async () => {
    const user = userEvent.setup();
    let reads = 0;
    let answer: (() => void) | undefined;
    fakeApi((operation, input) => {
      if (operation === "previewVariableRename") {
        reads += 1;
        const page = { items: [], next_cursor: null };
        // The reads of the dialog opened again wait for the test.
        return reads <= 2
          ? page
          : new Promise((resolve) => {
              const before = answer;
              answer = () => {
                before?.();
                resolve(page);
              };
            });
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    const preview = async () => {
      await user.click(within(panel).getByRole("button", { name: "Rename…" }));
      const dialog = await screen.findByRole("dialog", { name: "Rename rep" });
      const key = within(dialog).getByLabelText("New key");
      await user.clear(key);
      await user.type(key, "trust");
      await user.click(
        within(dialog).getByRole("button", { name: "Show what changes" }),
      );
      return dialog;
    };
    const first = await preview();
    await waitFor(() =>
      expect(
        within(first).getByRole("button", { name: "Rename" }),
      ).toHaveProperty("disabled", false),
    );
    await user.click(within(first).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const again = await preview();
    const rename = within(again).getByRole("button", { name: "Rename" });
    expect(rename).toHaveProperty("disabled", true);
    act(() => answer?.());
    await waitFor(() => expect(rename).toHaveProperty("disabled", false));
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
        "It is used 101 times in the project: read 100 times, and changed once. So it was not deleted.",
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

  it("says that a variable deleted elsewhere is gone, once a deletion left open went with the browser's Back", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    const { router } = renderApp(
      `/projects/${testProject.id}/variables?variable=var_met`,
      { session: testSession, realtime },
    );
    await screen.findByRole("region", { name: "met_elara" });
    const list = screen.getByRole("list", { name: "All variables" });
    await user.click(within(list).getByRole("button", { name: /^rep/ }));
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(
      within(panel).getByRole("button", { name: "Delete the variable" }),
    );
    await screen.findByRole("alertdialog");
    act(() => router.history.back());
    await screen.findByRole("region", { name: "met_elara" });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    // Another session deletes the variable whose panel is open now.
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "variable.delete", payload: { id: "var_met" } }],
        [{ id: "var_met", rev: 4 }],
      ),
    );
    expect(
      await screen.findByText(
        "The variable that the address names is not in the project, or is in its trash.",
      ),
    ).toBeDefined();
    expect(screen.queryByRole("region", { name: "met_elara" })).toBeNull();
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
            body: {
              commands: { payload: Partial<Variable> & { id: string } }[];
            };
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
    expect(
      await screen.findByRole("region", { name: "door_open" }),
    ).toBeDefined();
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
    const list = screen.getByRole("list", {
      hidden: true,
      name: "All variables",
    });
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

  it("leaves a dialog that the docked panel opened usable once the panel becomes a sheet", async () => {
    const user = userEvent.setup();
    const window = windowOf(1024);
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/variables?variable=var_rep`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "rep" });
    await user.click(within(panel).getByRole("button", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog", { name: "Rename rep" });
    // The tablet turns with the dialog open.
    act(() => window.resize(640));
    expect(dialog.closest("[inert]")).toBeNull();
    const list = screen.getByRole("list", {
      hidden: true,
      name: "All variables",
    });
    expect(list.closest("[inert]")).not.toBeNull();
  });
});

describe("a new variable or attribute", () => {
  it("starts a default of another type at that type's zero value", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) =>
      expected(
        contentPages(operation, input, { types: [character] }),
        operation,
      ),
    );
    renderApp(`/projects/${testProject.id}/variables`, {
      session: testSession,
    });
    await user.click(
      await screen.findByRole("button", { name: "New variable" }),
    );
    const create = await screen.findByRole("dialog", { name: "New variable" });
    await user.selectOptions(within(create).getByLabelText("Type"), "string");
    expect(within(create).getByLabelText("Default")).toHaveProperty(
      "value",
      "",
    );
    await user.click(within(create).getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("link", { name: "Component types" }));
    await user.click(await screen.findByRole("button", { name: /^Character/ }));
    const panel = await screen.findByRole("region", { name: "Character" });
    await user.click(
      within(panel).getByRole("button", { name: "Add an attribute…" }),
    );
    const add = await screen.findByRole("dialog", {
      name: "New attribute of Character",
    });
    await user.selectOptions(within(add).getByLabelText("Type"), "string");
    expect(within(add).getByLabelText("Default")).toHaveProperty("value", "");
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
          payload: {
            id: "cmp_elara",
            attribute_id: "atr_mood",
            value: "angry",
          },
          expected_rev: { cmp_elara: 4 },
        }),
      ],
    ]);
  });

  it("keeps a description that the user changed once a save refused for a change made elsewhere is saved again", async () => {
    const user = userEvent.setup();
    const theirs = { ...elara, description: paragraph("A guard"), rev: 5 };
    const sent: Sent[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        sent.push((input as { body: { commands: Sent } }).body.commands);
        if (sent.length === 1) {
          throw conflict("component.describe", theirs, 4);
        }
        return applied(testProject.seq + 1, [{ id: "cmp_elara", rev: 6 }]);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    await user.click(
      await within(panel).findByRole("textbox", { name: "Description" }),
    );
    await user.keyboard("A keeper of lamps");
    await user.click(
      within(panel).getByRole("button", { name: "Save the component" }),
    );
    await within(panel).findByText("Description: changed by someone else too");
    await user.click(
      within(panel).getByRole("button", { name: "Save the component" }),
    );
    await within(panel).findByText("Saved.");
    expect(sent[1]).toEqual([
      expect.objectContaining({
        type: "component.describe",
        payload: {
          id: "cmp_elara",
          description: paragraph("A keeper of lamps"),
        },
        expected_rev: { cmp_elara: 5 },
      }),
    ]);
  });

  it("saves a change of a component whose value is none of its attribute's options now, as once they changed elsewhere, leaving that value as it is", async () => {
    const user = userEvent.setup();
    const sleepy: Content = {
      ...content,
      components: [
        component("cmp_elara", "Elara", "elara", character, {
          attributes: { atr_trust: 0, atr_mood: "sleepy" },
        }),
      ],
    };
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1, [{ id: "cmp_elara", rev: 5 }])
        : expected(contentPages(operation, input, sleepy), operation),
    );
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    expect(await within(panel).findByLabelText("mood")).toHaveProperty(
      "value",
      "sleepy",
    );
    const name = within(panel).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Elara Vane");
    await user.click(
      within(panel).getByRole("button", { name: "Save the component" }),
    );
    await within(panel).findByText("Saved.");
    expect(batches(calls)).toEqual([
      [
        expect.objectContaining({
          type: "component.rename",
          payload: { id: "cmp_elara", name: "Elara Vane" },
        }),
      ],
    ]);
  });

  it("offers to read a component again once it kept changing while it was read", async () => {
    const user = userEvent.setup();
    // The component's own read answers it at a revision older than the
    // list's, as though another session changed it meanwhile, until the
    // user tries again.
    let reads = 0;
    fakeApi((operation, input) => {
      if (operation === "getComponent") {
        reads++;
        return reads <= 3 ? { ...elara, rev: 3 } : elara;
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    await within(panel).findByText(
      "The component kept changing while it was read.",
    );
    await user.click(within(panel).getByRole("button", { name: "Try again" }));
    expect(await within(panel).findByLabelText("trust")).toBeDefined();
  });

  it("offers no component's creation in a folder while the project has no types", async () => {
    const user = userEvent.setup();
    const untyped: Content = {
      folders: [folder("fld_cast", "Cast", "component")],
    };
    fakeApi((operation, input) =>
      expected(contentPages(operation, input, untyped), operation),
    );
    renderApp(`/projects/${testProject.id}/components`, {
      session: testSession,
    });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Cast" }),
    );
    expect(
      (
        await screen.findByRole("menuitem", { name: "New component here…" })
      ).getAttribute("aria-disabled"),
    ).toBe("true");
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

  it("sets the image that it uploaded once the panel has gone meanwhile", async () => {
    const user = userEvent.setup();
    let uploaded: ((asset: { id: string }) => void) | undefined;
    const calls = fakeApi((operation, input) => {
      switch (operation) {
        case "uploadAsset":
          return new Promise((resolve) => {
            uploaded = resolve;
          });
        case "applyCommands":
          return applied(testProject.seq + 1, [{ id: "cmp_elara", rev: 5 }]);
      }
      return expected(contentPages(operation, input, content), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    await user.upload(
      within(panel).getByLabelText("Upload an image"),
      new File(["png"], "elara.png", { type: "image/png" }),
    );
    await waitFor(() => expect(uploaded).toBeDefined());
    await user.click(within(panel).getByRole("button", { name: "Close" }));
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Elara" })).toBeNull(),
    );
    act(() => uploaded?.({ id: "ast_elara" }));
    await waitFor(() =>
      expect(batches(calls)).toEqual([
        [
          expect.objectContaining({
            type: "component.set_image",
            payload: { id: "cmp_elara", image_asset_id: "ast_elara" },
            expected_rev: { cmp_elara: 4 },
          }),
        ],
      ]),
    );
  });

  it("says what became of the image's last change alone", async () => {
    const user = userEvent.setup();
    const framed: Content = {
      ...content,
      components: [{ ...elara, image_asset_id: "ast_old" }],
    };
    fakeApi((operation, input) => {
      switch (operation) {
        case "uploadAsset":
          throw new NetworkError(new TypeError("Failed to fetch"));
        case "applyCommands":
          return applied(testProject.seq + 1, [{ id: "cmp_elara", rev: 5 }]);
      }
      return expected(contentPages(operation, input, framed), operation);
    });
    renderApp(`/projects/${testProject.id}/components?component=cmp_elara`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Elara" });
    await user.upload(
      within(panel).getByLabelText("Replace the image"),
      new File(["png"], "elara.png", { type: "image/png" }),
    );
    const failure =
      "The server could not be reached. Check your connection, and try again.";
    expect(await within(panel).findByText(failure)).toBeDefined();
    await user.click(
      within(panel).getByRole("button", { name: "Remove the image" }),
    );
    await waitFor(() => expect(within(panel).queryByRole("img")).toBeNull());
    await waitFor(() => expect(within(panel).queryByText(failure)).toBeNull());
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
        "It is used 2 times in the project: as a speaker once, in mentions nowhere, reading its attributes once, and changing them nowhere. So it was not deleted.",
      ),
    ).toBeDefined();
    const listed = within(dialog).getByRole("list", {
      name: "What uses Elara",
    });
    expect(
      within(listed)
        .getAllByRole("listitem")
        .map((i) => i.textContent),
    ).toEqual([
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
    await user.selectOptions(within(dialog).getByLabelText("New type"), "bool");
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
    expect(
      within(dialog).queryByText("Elara (elara), in the trash"),
    ).toBeNull();
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

  it("moves an attribute up past two before it that share a key", async () => {
    const user = userEvent.setup();
    const shared: Content = {
      ...content,
      types: [
        {
          ...character,
          attributes: [
            attribute("atr_a", "a", { order: "V" }),
            attribute("atr_b", "b", { order: "X" }),
            attribute("atr_c", "c", { order: "X" }),
            attribute("atr_d", "d", { order: "Z" }),
          ],
        },
      ],
    };
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 1)
        : expected(contentPages(operation, input, shared), operation),
    );
    renderApp(`/projects/${testProject.id}/component-types?type=ct_character`, {
      session: testSession,
    });
    const panel = await screen.findByRole("region", { name: "Character" });
    await user.click(
      within(panel).getByRole("button", { name: "Actions of d" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Move up" }));
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    const [[move]] = batches(calls);
    const { order } = move.payload as { order: string };
    // After a, and before c, whose key b shares.
    expect(order > "V" && order < "X").toBe(true);
  });

  it("deletes a type expecting the revision that it has as its confirmation opens", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const calls = fakeApi((operation, input) =>
      operation === "applyCommands"
        ? applied(testProject.seq + 2)
        : expected(contentPages(operation, input, content), operation),
    );
    renderApp(`/projects/${testProject.id}/component-types?type=ct_character`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("region", { name: "Character" });
    // Another session renames the type while its panel is open.
    act(() =>
      realtime.send(
        testProject.id,
        [
          {
            type: "component_type.rename",
            payload: { id: "ct_character", name: "Person" },
          },
        ],
        [{ id: "ct_character", rev: 2 }],
      ),
    );
    const panel = await screen.findByRole("region", { name: "Person" });
    await user.click(
      within(panel).getByRole("button", { name: "Delete the type" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete the type" }),
    );
    await waitFor(() => expect(batches(calls)).toHaveLength(1));
    expect(batches(calls)[0]).toEqual([
      expect.objectContaining({
        type: "component_type.delete",
        payload: { id: "ct_character" },
        expected_rev: { ct_character: 2 },
      }),
    ]);
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
      within(dialog).getByRole("checkbox", {
        name: "Part of the game's state",
      }),
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
