import type { Result } from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import { commandIds } from "./commands/catalog";
import { Palette } from "./commands/Palette";
import {
  CommandRegistry,
  CommandsProvider,
  keysOfPage,
  useCommandHandlers,
  useRegistry,
} from "./commands/registry";
import { parseSearch } from "./router";
import { askSearchFocus } from "./search/focus";
import { renderApp, testSession } from "./test/app";
import { fakeApi, refused, startPage, testProject } from "./test/fake";

// The command palette, the table of the shortcuts, the keys of the page,
// and the project's search (PRD FR-EDIT-10, FR-PROJ-03, plan note N72), as
// the router shows them, over a fake of the API's client.

afterEach(() => {
  vi.restoreAllMocks();
});

function result(more: Partial<Result> & Pick<Result, "type" | "id">): Result {
  return { title: [], snippet: [], ...more };
}

/**
 * Answers a project's pages, and its search with the pages of results that
 * pages gives for each query, keyed by the query's q and type.
 */
function projectPages(
  pages: (q: string, type?: string, cursor?: string) => unknown = () => ({
    items: [],
    next_cursor: null,
  }),
) {
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
      case "listBoards":
      case "listVariables":
      case "listComponentTypes":
      case "listComponents":
        return { items: [], next_cursor: null };
      case "searchProject": {
        const { q, type, cursor } = (
          input as { query: { q: string; type?: string; cursor?: string } }
        ).query;
        return pages(q, type, cursor);
      }
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

function searches(calls: ReturnType<typeof fakeApi>) {
  return calls.mock.calls
    .filter(([operation]) => operation === "searchProject")
    .map(([, input]) => (input as { query: Record<string, unknown> }).query);
}

async function openVariables() {
  renderApp(`/projects/${testProject.id}/variables`, { session: testSession });
  return screen.findByRole("heading", { level: 1, name: "Variables" });
}

describe("the command palette", () => {
  // Verifies: FR-EDIT-10/AC1
  it("lists each command of the page by an ID of the catalog, which the table of the shortcuts lists with the same keys", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    const entries = within(palette)
      .getAllByRole("option")
      .map((o) => ({
        id: o.dataset.command as string,
        keys: o.querySelector("kbd")?.textContent,
      }));
    expect(entries.length).toBeGreaterThan(0);
    for (const { id } of entries) {
      expect(commandIds).toContain(id);
    }
    expect(entries.map((e) => e.id)).toEqual(
      expect.arrayContaining([
        "palette.open",
        "shortcuts.show",
        "project.search",
        "go.projects",
        "go.boards",
        "go.variables",
        "go.trash",
      ]),
    );
    // The table, which the palette opens, lists every command that has
    // keys, the palette's among them, with the same keys.
    await user.type(within(palette).getByRole("combobox"), "keyboard short");
    await user.keyboard("{Enter}");
    const table = await screen.findByRole("dialog", {
      name: "Keyboard shortcuts",
    });
    for (const { id, keys } of entries) {
      const row = table.querySelector(`[data-command="${id}"]`);
      if (keys === undefined) {
        expect(row).toBeNull();
      } else {
        expect(row?.querySelector("kbd")?.textContent).toBe(keys);
      }
    }
    for (const row of table.querySelectorAll<HTMLElement>("[data-command]")) {
      expect(commandIds).toContain(row.dataset.command);
    }
    expect(
      table.querySelector('[data-command="palette.open"] kbd')?.textContent,
    ).toBe("Ctrl+K");
    expect(
      table.querySelector('[data-command="canvas.add_element"] kbd')
        ?.textContent,
    ).toBe("E");
  });

  it("narrows to the commands whose names have words that start as those typed, whatever their case and accents, and runs one by a click", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    await user.click(screen.getByRole("button", { name: "Commands" }));
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    await user.type(
      within(palette).getByRole("combobox"),
      "GO PRÓJECT'S COMPO",
    );
    const options = within(palette).getAllByRole("option");
    expect(options.map((o) => o.dataset.command)).toEqual([
      "go.components",
      "go.component_types",
      undefined,
    ]);
    await user.click(options[0]);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Components" }),
    ).toBeDefined();
  });

  it("closes by Escape, or by its own key, and gives the focus back to what had it", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    const button = screen.getByRole("button", { name: "Commands" });
    button.focus();
    await user.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Commands" });
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Commands" })).toBeNull(),
    );
    expect(document.activeElement).toBe(button);
    await user.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Commands" });
    await user.keyboard("{Control>}k{/Control}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Commands" })).toBeNull(),
    );
    expect(document.activeElement).toBe(button);
  });

  it("gives the page's content the focus as it closes, where nothing had it as it opened", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    expect(document.activeElement).toBe(document.body);
    await user.keyboard("{Control>}k{/Control}");
    await screen.findByRole("dialog", { name: "Commands" });
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Commands" })).toBeNull(),
    );
    expect(document.activeElement?.id).toBe("main");
  });

  it("shows itself anew, open, as its own command is chosen in it", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    const field = within(palette).getByRole("combobox") as HTMLInputElement;
    await user.type(field, "command palette");
    await user.keyboard("{Enter}");
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.getByRole("dialog", { name: "Commands" })).toBe(palette);
    expect(field.value).toBe("");
    expect(document.activeElement).toBe(field);
  });

  it("says whether each command can run as the page says it, while it is open, where the list stays scrolled", () => {
    // jsdom scrolls nothing into view.
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    let enable: (enabled: boolean) => void = () => {};
    function Trash() {
      const [enabled, setEnabled] = useState(false);
      enable = setEnabled;
      useCommandHandlers("page", { "go.trash": { enabled, run: () => {} } });
      return null;
    }
    function Host() {
      const registry = useRegistry();
      const opener = useRef<HTMLElement | null>(null);
      return registry === undefined ? null : (
        <Palette
          registry={registry}
          open
          opener={opener}
          onOpenChange={() => {}}
        />
      );
    }
    render(
      <I18nextProvider i18n={createI18n("en")}>
        <CommandsProvider>
          <Trash />
          <Host />
        </CommandsProvider>
      </I18nextProvider>,
    );
    const option = () =>
      screen.getByRole("option", { name: /Go to the project's trash/ });
    try {
      expect(option().getAttribute("aria-disabled")).toBe("true");
      expect(scrolled).toHaveBeenCalledTimes(1);
      act(() => enable(true));
      expect(option().getAttribute("aria-disabled")).toBeNull();
      expect(option().textContent).not.toContain("(unavailable now)");
      // The highlight did not move: the list stays where the wheel left it.
      expect(scrolled).toHaveBeenCalledTimes(1);
    } finally {
      delete (Element.prototype as Partial<Element>).scrollIntoView;
    }
  });

  it("keeps the entry moved to highlighted as the page's commands change, and none once it is gone, which Enter runs nothing of", () => {
    let show: (ids: ("go.projects" | "go.trash")[]) => void = () => {};
    function Commands() {
      const [ids, setIds] = useState<("go.projects" | "go.trash")[]>([
        "go.projects",
        "go.trash",
      ]);
      show = setIds;
      useCommandHandlers(
        "page",
        Object.fromEntries([
          ...ids.map((id) => [id, { run: () => {} }]),
          ["go.settings", { run: () => {} }],
        ]),
      );
      return null;
    }
    const closed = vi.fn();
    function Host() {
      const registry = useRegistry();
      const opener = useRef<HTMLElement | null>(null);
      return registry === undefined ? null : (
        <Palette
          registry={registry}
          open
          opener={opener}
          onOpenChange={closed}
        />
      );
    }
    render(
      <I18nextProvider i18n={createI18n("en")}>
        <CommandsProvider>
          <Commands />
          <Host />
        </CommandsProvider>
      </I18nextProvider>,
    );
    const field = screen.getByRole("combobox");
    const selected = () =>
      screen
        .getAllByRole("option")
        .filter((o) => o.getAttribute("aria-selected") === "true")
        .map((o) => o.dataset.command);
    expect(selected()).toEqual(["go.projects"]);
    fireEvent.keyDown(field, { key: "ArrowDown" });
    expect(selected()).toEqual(["go.trash"]);
    // The entry before it goes: the one moved to stays highlighted, rather
    // than the one that takes its place.
    act(() => show(["go.trash"]));
    expect(selected()).toEqual(["go.trash"]);
    // It goes itself: none is highlighted, and Enter chooses nothing.
    act(() => show([]));
    expect(selected()).toEqual([]);
    expect(field.getAttribute("aria-activedescendant")).toBeNull();
    fireEvent.keyDown(field, { key: "Enter" });
    expect(closed).not.toHaveBeenCalled();
    fireEvent.keyDown(field, { key: "ArrowDown" });
    expect(selected()).toEqual(["go.settings"]);
  });

  it("is told of a change of what the page can run, rather than of each render of the page", () => {
    const registry = new CommandRegistry();
    let undoable = false;
    const handlers = {
      current: {
        "history.undo": {
          get enabled() {
            return undoable;
          },
          run: () => {},
        },
      },
    };
    const told = vi.fn();
    const stop = registry.subscribe(told);
    registry.add("page", handlers);
    expect(told).toHaveBeenCalledTimes(1);
    registry.changed();
    registry.changed();
    expect(told).toHaveBeenCalledTimes(1);
    undoable = true;
    registry.changed();
    expect(told).toHaveBeenCalledTimes(2);
    const remove = registry.add("app", {
      current: { "palette.open": { run: () => {} } },
    });
    expect(told).toHaveBeenCalledTimes(3);
    remove();
    expect(told).toHaveBeenCalledTimes(4);
    stop();
    // While none listens, each change counts.
    const before = registry.revision();
    registry.changed();
    expect(registry.revision()).toBe(before + 1);
  });

  it("stays open for the repeats of its key held down, and leaves an input method's Enter to it", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    const field = within(palette).getByRole("combobox");
    await waitFor(() => expect(document.activeElement).toBe(field));
    // The repeat of the key that opened it, held down a little long, comes
    // to its field, which it has given the focus.
    fireEvent.keyDown(field, {
      key: "k",
      code: "KeyK",
      ctrlKey: true,
      repeat: true,
    });
    expect(screen.getByRole("dialog", { name: "Commands" })).toBe(palette);
    await user.type(field, "go components");
    // The Enter that ends what an input method composed chooses nothing.
    fireEvent.keyDown(field, {
      key: "Enter",
      code: "Enter",
      isComposing: true,
    });
    fireEvent.keyDown(field, { key: "ArrowDown", isComposing: true });
    // Safari's, which comes once the composition has ended, of keyCode 229.
    fireEvent.keyDown(field, { key: "Enter", code: "Enter", keyCode: 229 });
    expect(screen.getByRole("dialog", { name: "Commands" })).toBe(palette);
    expect(
      within(palette)
        .getAllByRole("option")
        .map((o) => o.getAttribute("aria-selected")),
    ).toEqual(["true", "false"]);
    fireEvent.keyDown(field, { key: "Enter", code: "Enter" });
    expect(
      await screen.findByRole("heading", { level: 1, name: "Components" }),
    ).toBeDefined();
  });

  it("searches the project for what was typed", async () => {
    const user = userEvent.setup();
    const calls = projectPages();
    await openVariables();
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    await user.type(within(palette).getByRole("combobox"), "elara");
    await user.click(
      within(palette).getByRole("option", {
        name: "Search the project for “elara”",
      }),
    );
    expect(
      await screen.findByRole("heading", { level: 1, name: "Search" }),
    ).toBeDefined();
    await waitFor(() =>
      expect(searches(calls)).toEqual([
        expect.objectContaining({ q: "elara" }),
      ]),
    );
  });
});

describe("the keys of the page", () => {
  it("open the project's search with the focus in its field by Ctrl+/, and give it the focus there", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    await user.keyboard("{Control>}/{/Control}");
    const field = await screen.findByRole("searchbox", { name: "Search for" });
    await waitFor(() => expect(document.activeElement).toBe(field));
    screen.getByRole("link", { name: "Search" }).focus();
    await user.keyboard("{Control>}/{/Control}");
    expect(document.activeElement).toBe(field);
    // The palette's search of another query, on the search's page, keeps
    // the focus in the field, which shows it.
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    await user.type(within(palette).getByRole("combobox"), "keeper");
    await user.click(
      within(palette).getByRole("option", {
        name: "Search the project for “keeper”",
      }),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("searchbox", { name: "Search for" }),
      ),
    );
    expect(
      (
        screen.getByRole("searchbox", {
          name: "Search for",
        }) as HTMLInputElement
      ).value,
    ).toBe("keeper");
    // The palette's search of the query that the page shows already shows
    // it in the field too, in place of what was typed there and not sent.
    const shown = screen.getByRole("searchbox", {
      name: "Search for",
    }) as HTMLInputElement;
    await user.clear(shown);
    await user.type(shown, "harbour");
    await user.keyboard("{Control>}k{/Control}");
    const again = await screen.findByRole("dialog", { name: "Commands" });
    await user.type(within(again).getByRole("combobox"), "keeper");
    await user.click(
      within(again).getByRole("option", {
        name: "Search the project for “keeper”",
      }),
    );
    await waitFor(() => expect(document.activeElement).toBe(shown));
    expect(shown.value).toBe("keeper");
  });

  it("forget the search's focus once a page other than the search's was shown in its place", async () => {
    const user = userEvent.setup();
    projectPages();
    await openVariables();
    // As of a search's key whose navigation another overtook.
    askSearchFocus();
    await user.click(screen.getByRole("link", { name: "Components" }));
    await screen.findByRole("heading", { level: 1, name: "Components" });
    const link = screen.getByRole("link", { name: "Search" });
    await user.click(link);
    const field = await screen.findByRole("searchbox", { name: "Search for" });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(document.activeElement).not.toBe(field);
  });

  it("are the page's but in a field whose own they are, and run nothing while a dialog is open, while a part of the page holds them, once the page took the key, nor for the repeats of a key that runs once", () => {
    const registry = new CommandRegistry();
    const ran: string[] = [];
    let undoable = true;
    const remove = registry.add("app", {
      current: {
        "palette.open": { run: () => void ran.push("palette") },
        "history.undo": {
          get enabled() {
            return undoable;
          },
          run: () => void ran.push("undo"),
        },
      },
    });
    const listen = keysOfPage(registry, "windows");
    const press = (
      target: Element,
      key: string,
      taken = false,
      repeat = false,
    ) => {
      const e = new KeyboardEvent("keydown", {
        key,
        code: `Key${key.toUpperCase()}`,
        ctrlKey: true,
        repeat,
        bubbles: true,
        cancelable: true,
      });
      if (taken) {
        e.preventDefault();
      }
      Object.defineProperty(e, "target", { value: target });
      listen(e);
      return e.defaultPrevented;
    };
    const input = document.createElement("input");
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    const inDialog = document.createElement("button");
    const fieldInDialog = document.createElement("input");
    dialog.append(inDialog, fieldInDialog);
    const sheet = document.createElement("section");
    sheet.setAttribute("role", "dialog");
    sheet.setAttribute("aria-modal", "true");
    // A confirmation of Radix's, which says nothing of being modal.
    const confirmation = document.createElement("div");
    confirmation.setAttribute("role", "alertdialog");
    document.body.append(input);
    try {
      expect(press(document.body, "z")).toBe(true);
      expect(press(input, "z")).toBe(false);
      expect(press(input, "k")).toBe(true);
      // In a dialog, a key of the page's runs nothing, but is the page's,
      // rather than the browser's, which Safari would reopen a closed tab
      // by, but in a field whose own key it is.
      document.body.append(dialog);
      expect(press(inDialog, "k")).toBe(true);
      expect(press(inDialog, "z")).toBe(true);
      expect(press(fieldInDialog, "z")).toBe(false);
      dialog.remove();
      expect(press(document.body, "k", true)).toBe(true);
      expect(ran).toEqual(["undo", "palette"]);
      // Held down, the undo's key undoes on, and the palette's is the
      // page's, but opens the palette no more.
      expect(press(document.body, "z", false, true)).toBe(true);
      expect(press(document.body, "k", false, true)).toBe(true);
      expect(ran).toEqual(["undo", "palette", "undo"]);
      // A command that cannot run now, as an undo while a step is under
      // way, takes its key all the same, which Safari would reopen a closed
      // tab by, and runs nothing.
      undoable = false;
      expect(press(document.body, "z")).toBe(true);
      expect(press(document.body, "z", false, true)).toBe(true);
      expect(ran).toEqual(["undo", "palette", "undo"]);
      // While a dialog is open, as a sheet whose text a click left the
      // focus on the body by, or a confirmation whose button that had it
      // is disabled while its request is answered, the keys run nothing,
      // and are the page's.
      undoable = true;
      for (const open of [sheet, confirmation]) {
        document.body.append(open);
        expect(press(document.body, "z")).toBe(true);
        expect(press(document.body, "k")).toBe(true);
        open.remove();
      }
      expect(ran).toEqual(["undo", "palette", "undo"]);
      // While a part of the page holds a command, as the canvas in the
      // middle of a drag all but those that run then, its key runs nothing,
      // and is the page's, and the others run.
      const release = registry.hold((id) => id === "palette.open");
      expect(press(document.body, "k")).toBe(true);
      expect(registry.can("palette.open")).toBe(false);
      expect(registry.run("palette.open")).toBe(false);
      expect(press(document.body, "z")).toBe(true);
      release();
      expect(registry.can("palette.open")).toBe(true);
      expect(ran).toEqual(["undo", "palette", "undo", "undo"]);
      // A command that the page has no handler of leaves the key to the
      // browser, and the repeats of its key too.
      remove();
      expect(press(document.body, "z")).toBe(false);
      expect(press(document.body, "k", false, true)).toBe(false);
    } finally {
      input.remove();
      dialog.remove();
      sheet.remove();
      confirmation.remove();
    }
  });
});

describe("the project's search", () => {
  // Verifies: FR-PROJ-03/AC2
  it("lists each result with its kind, its board, and the words that matched marked, each a link to where it is", async () => {
    projectPages((q) =>
      q === "ela"
        ? {
            items: [
              result({
                type: "node",
                id: "nd_quay",
                board_id: "bd_harbour",
                board_name: "Harbour",
                node_id: "nd_quay",
                node_title: "Elara at the quay",
                title: [
                  { text: "Elara", match: true },
                  { text: " at the quay", match: false },
                ],
                snippet: [
                  { text: "She waits for ", match: false },
                  { text: "Elary", match: true },
                ],
              }),
              result({
                type: "connection",
                id: "cn_1",
                board_id: "bd_harbour",
                board_name: "Harbour",
                node_id: "nd_quay",
                node_title: "Elara at the quay",
                snippet: [{ text: "Elara", match: true }],
              }),
              result({
                type: "variable",
                id: "var_trust",
                title: [{ text: "elara_trust", match: false }],
              }),
              result({
                type: "component",
                id: "cmp_elara",
                title: [{ text: "Elara", match: true }],
              }),
            ],
            next_cursor: null,
          }
        : { items: [], next_cursor: null },
    );
    renderApp(`/projects/${testProject.id}/search?q=ela`, {
      session: testSession,
    });
    expect(await screen.findByText("4 results.")).toBeDefined();
    const [node, connection, variable, component] = within(
      screen.getByRole("region", { name: "Results" }),
    ).getAllByRole("listitem");
    const nodeLink = within(node).getByRole("link", {
      name: "Elara at the quay",
    });
    expect(nodeLink.getAttribute("href")).toBe(
      `/app/projects/${testProject.id}/boards/bd_harbour?node=nd_quay`,
    );
    expect(
      [...node.querySelectorAll("mark")].map((m) => m.textContent),
    ).toEqual(["Elara", "Elary"]);
    expect(node.textContent).toContain("Node · On Harbour");
    expect(
      within(connection)
        .getByRole("link", { name: "Connection from Elara at the quay" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/boards/bd_harbour?node=nd_quay`);
    expect(connection.textContent).toContain("Connection · On Harbour");
    expect(
      within(variable)
        .getByRole("link", { name: "elara_trust" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/variables?variable=var_trust`);
    expect(variable.textContent).toContain("Variable · Global");
    expect(
      within(component)
        .getByRole("link", { name: "Elara" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/components?component=cmp_elara`);
  });

  it("searches for what is typed, of the kind that the filter says, and shows more as asked", async () => {
    const user = userEvent.setup();
    const calls = projectPages((q, type, cursor) => ({
      items: [
        result({
          type: "node",
          id: cursor === undefined ? "nd_1" : "nd_2",
          board_id: "bd_a",
          board_name: "Harbour",
          node_id: cursor === undefined ? "nd_1" : "nd_2",
          node_title: `${q} ${type ?? "any"} ${cursor ?? "first"}`,
          title: [
            { text: `${q} ${type ?? "any"} ${cursor ?? "first"}`, match: true },
          ],
        }),
      ],
      next_cursor: cursor === undefined ? "next" : null,
    }));
    renderApp(`/projects/${testProject.id}/search`, { session: testSession });
    expect(
      await screen.findByText(
        "Search the project's nodes, connections, variables, and components.",
      ),
    ).toBeDefined();
    const field = screen.getByRole("searchbox", { name: "Search for" });
    await user.type(field, "quay{Enter}");
    expect(
      await screen.findByText("1 result, and more to show."),
    ).toBeDefined();
    // The field that sent the search keeps the focus.
    expect(document.activeElement).toBe(field);
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Find" }),
      "node",
    );
    expect(await screen.findByText("quay node first")).toBeDefined();
    await user.click(screen.getByRole("button", { name: "Show more" }));
    expect(await screen.findByText("quay node next")).toBeDefined();
    expect(screen.getByText("2 results.")).toBeDefined();
    expect(searches(calls)).toEqual([
      expect.objectContaining({ q: "quay" }),
      expect.objectContaining({ q: "quay", type: "node" }),
      expect.objectContaining({ q: "quay", type: "node", cursor: "next" }),
    ]);
    expect(searches(calls)[0]).not.toHaveProperty("type");
  });

  it("searches for the query that the address spells, as 1e3, null, or true", async () => {
    expect({
      ...parseSearch("?q=1e3&a=null&b=true&c=1.50&d=%22true%22&e=%5Bx"),
    }).toEqual({ q: "1e3", a: "null", b: true, c: "1.50", d: "true", e: "[x" });
    const calls = projectPages();
    renderApp(`/projects/${testProject.id}/search?q=true`, {
      session: testSession,
    });
    expect(
      await screen.findByText("Nothing in the project matches “true”."),
    ).toBeDefined();
    expect(searches(calls)).toEqual([expect.objectContaining({ q: "true" })]);
  });

  it("searches for a query as the server takes it, of at most 200 characters, without control characters", async () => {
    const user = userEvent.setup();
    const calls = projectPages();
    // An address of a query too long, with a tab, as one pasted from a
    // table: 11 characters and 189 of 300 that are one code point each.
    const want = `quay north ${"ž".repeat(189)}`;
    renderApp(
      `/projects/${testProject.id}/search?q=${encodeURIComponent(
        `quay\tnorth ${"ž".repeat(300)}`,
      )}`,
      { session: testSession },
    );
    await waitFor(() =>
      expect(searches(calls)).toEqual([expect.objectContaining({ q: want })]),
    );
    const field = screen.getByRole<HTMLInputElement>("searchbox", {
      name: "Search for",
    });
    expect(field.value).toBe(want);
    // The palette's search, whose field has no limit, as a paste makes it
    // longer, searches for what the server takes of it, as it says.
    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Commands" });
    const typed = within(palette).getByRole("combobox");
    fireEvent.change(typed, {
      target: { value: `dock\u0007${"a".repeat(250)}` },
    });
    const searched = `dock ${"a".repeat(195)}`;
    await user.click(
      within(palette).getByRole("option", {
        name: `Search the project for “${searched}”`,
      }),
    );
    await waitFor(() =>
      expect(searches(calls)).toEqual([
        expect.objectContaining({ q: want }),
        expect.objectContaining({ q: searched }),
      ]),
    );
    await waitFor(() => expect(field.value).toBe(searched));
  });

  it("says that nothing matches, and why the server refused a search", async () => {
    projectPages((q) => {
      if (q === "bad") {
        throw refused(422, {
          code: "validation_failed",
          details: { fields: [{ location: "query.q", code: "invalid" }] },
        });
      }
      return { items: [], next_cursor: null };
    });
    renderApp(`/projects/${testProject.id}/search?q=nothing`, {
      session: testSession,
    });
    expect(
      await screen.findByText("Nothing in the project matches “nothing”."),
    ).toBeDefined();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search for" }), {
      target: { value: "bad" },
    });
    fireEvent.submit(
      screen.getByRole("searchbox").closest("form") as HTMLFormElement,
    );
    const results = await screen.findByRole("region", { name: "Results" });
    await waitFor(() =>
      expect(within(results).getByRole("alert").textContent).not.toBe(""),
    );
  });
});
