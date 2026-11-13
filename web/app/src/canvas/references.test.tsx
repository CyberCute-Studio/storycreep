import type {
  Board,
  Component,
  ComponentType,
  RichTextDoc,
  Variable,
} from "@storycreep/shared/api";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { testConnection, testNode } from "../test/canvas";
import "../test/editor";
import { fakeApi, startPage, testProject } from "../test/fake";
import { fakeRealtime } from "../test/realtime";
import type { Engine } from "./engine";

// The value references and the mentions of a board's texts, on its canvas
// (PRD FR-TEXT-02, FR-TEXT-03, plan note N73): what they show follows a
// rename of what they refer to, made in another session, in every text,
// the cards' and the editor's, while the texts, which store IDs, stay as
// they are.

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
const trustId = "atr_01HQ8Z3XK4F7NAWR5TCQ9M2BV5";

const harbour: Board = {
  id: "bd_a",
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
  attributes: [
    {
      id: trustId,
      component_type_id: "ct_character",
      key: "trust",
      name: "Trust",
      type: "int",
      default: 0,
      runtime: true,
      order: "V",
      rev: 1,
      created_at: at,
      updated_at: at,
    },
  ],
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

/** A text that shows gold, Elara's trust, and mentions Elara. */
const text: RichTextDoc = {
  type: "doc",
  content: [
    {
      type: "paragraph",
      content: [
        { type: "text", text: "You owe " },
        { type: "valueRef", attrs: { ref: goldId } },
        { type: "text", text: " to " },
        { type: "mention", attrs: { id: elaraId } },
        { type: "text", text: ", who trusts you " },
        { type: "valueRef", attrs: { ref: elaraId, attribute: trustId } },
      ],
    },
  ],
};

/** Answers the board's page, of a node whose text is text, and records what it is asked. */
function boardPage() {
  return fakeApi((operation) => {
    switch (operation) {
      case "getProject":
        return testProject;
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
        return {
          items: [testNode("nd_1", 0, 0, { text }), testNode("nd_2", 400, 0)],
          next_cursor: null,
        };
      case "listBoardConnections":
        return {
          items: [testConnection("cn_1", "nd_1", "nd_2")],
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

/** Returns the name of the canvas's tree's item of the node of title. */
function treeItemOf(title: string): string {
  const tree = screen.getByRole("tree", {
    name: "Nodes and connections of Harbour",
  });
  const item = within(tree)
    .getAllByRole("treeitem")
    .find((i) => i.getAttribute("aria-label")?.startsWith(title));
  return item?.getAttribute("aria-label") ?? "";
}

describe("the references of a board's texts", () => {
  // Verifies: FR-TEXT-02/AC1
  it("show what a rename in another session calls what they refer to, on the cards and in the editor of a text, without a change of the texts", async () => {
    const calls = boardPage();
    const realtime = fakeRealtime();
    renderApp(`/projects/${testProject.id}/boards/bd_a?node=nd_1`, {
      session: testSession,
      realtime,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
    await waitFor(() =>
      expect(treeItemOf("nd_1")).toContain(
        "You owe {gold} to @Elara, who trusts you {elara.trust}",
      ),
    );
    // The editor of the node's text shows the same names.
    canvas.focus();
    fireEvent.keyDown(canvas, { key: "Enter" });
    const textbox = await screen.findByRole("textbox", {
      name: "Text of nd_1",
    });
    await waitFor(() =>
      expect(textbox.textContent).toBe(
        "You owe {gold} to @Elara, who trusts you {elara.trust}",
      ),
    );
    act(() =>
      realtime.send(
        testProject.id,
        [
          { type: "variable.rename", payload: { id: goldId, key: "debt" } },
          {
            type: "component.rename",
            payload: { id: elaraId, name: "Elara Vey" },
          },
          { type: "component.set_key", payload: { id: elaraId, key: "vey" } },
          { type: "attribute.set_key", payload: { id: trustId, key: "faith" } },
        ],
        [
          { id: goldId, rev: 2 },
          { id: elaraId, rev: 3 },
          { id: trustId, rev: 2 },
        ],
      ),
    );
    await waitFor(() =>
      expect(treeItemOf("nd_1")).toContain(
        "You owe {debt} to @Elara Vey, who trusts you {vey.faith}",
      ),
    );
    await waitFor(() =>
      expect(textbox.textContent).toBe(
        "You owe {debt} to @Elara Vey, who trusts you {vey.faith}",
      ),
    );
    // The texts are as they were: the editor holds nothing to save, which
    // its Save closes without sending a command, and the page sent none.
    const editor = screen.getByRole("form", { name: "The text of nd_1" });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Save the text" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("textbox", { name: "Text of nd_1" }),
      ).toBeNull(),
    );
    expect(
      calls.mock.calls.filter(([operation]) => operation === "applyCommands"),
    ).toEqual([]);
  });
});
