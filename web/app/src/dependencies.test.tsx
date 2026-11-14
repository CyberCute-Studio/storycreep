import type {
  Board,
  Contribution,
  Graph,
  PageContribution,
} from "@storycreep/shared/api";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "./test/app";
import { fakeApi, startPage, testProject } from "./test/fake";
import { fakeRealtime } from "./test/realtime";

// A project's dependency view (PRD FR-DEP-02, plan note N77), as the router
// shows it, over a fake of the API's client.

afterEach(() => {
  vi.restoreAllMocks();
});

const at = "2026-11-01T10:00:00Z";

/** A board of the test project, at order among its siblings. */
function board(id: string, name: string, order: string): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: id,
    description: "",
    color: "none",
    order,
    rev: 3,
    created_at: at,
    updated_at: at,
  };
}

/**
 * The graph of Harbour (bd_a), Lighthouse (bd_b), and Mill (bd_c), of which
 * Lighthouse reads a variable that Harbour writes, Harbour a jumper's target
 * of Lighthouse, so that each depends on the other, a cycle, and Mill reads
 * whether a node of Lighthouse was visited.
 */
function graph(more: Partial<Graph> = {}): Graph {
  return {
    seq: testProject.seq,
    boards: [
      { id: "bd_a", name: "Harbour", inbound: 1, outbound: 1 },
      { id: "bd_b", name: "Lighthouse", inbound: 1, outbound: 2 },
      { id: "bd_c", name: "Mill", inbound: 1, outbound: 0 },
    ],
    edges: [
      {
        from_board_id: "bd_a",
        to_board_id: "bd_b",
        kind: "variable",
        contributions: 2,
      },
      {
        from_board_id: "bd_b",
        to_board_id: "bd_a",
        kind: "jumper",
        contributions: 1,
      },
      {
        from_board_id: "bd_b",
        to_board_id: "bd_c",
        kind: "visit",
        contributions: 1,
      },
    ],
    ...more,
  };
}

const goldWrite: Contribution = {
  variable_id: "var_gold",
  from: {
    site: "node_on_exit",
    node_id: "nd_greet",
    node_title: "Greet",
  },
  to: {
    site: "connection_condition",
    node_id: "nd_keeper",
    node_title: "Keeper",
    connection_id: "cn_ask",
  },
};

/** Answers the dependency view's pages, with the graph that answer returns for a request's query, and the contributions of contributions. */
function dependencyPages(
  answer: (query: Record<string, string>) => Graph,
  contributions: (query: Record<string, string>) => PageContribution = () => ({
    items: [goldWrite],
    next_cursor: null,
  }),
) {
  return fakeApi((operation, input) => {
    const query = ((input as { query?: Record<string, string> }).query ??
      {}) as Record<string, string>;
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listBoards":
        return {
          items: [
            board("bd_a", "Harbour", "V"),
            board("bd_b", "Lighthouse", "W"),
            board("bd_c", "Mill", "X"),
          ],
          next_cursor: null,
        };
      case "listVariables":
        return {
          items: [
            {
              id: "var_gold",
              project_id: testProject.id,
              key: "gold",
              type: "int",
              default: 0,
              description: "",
              scope: "global",
              reset_on_enter: false,
              order: "V",
              rev: 1,
              created_at: at,
              updated_at: at,
            },
          ],
          next_cursor: null,
        };
      case "listComponentTypes":
      case "listComponents":
        return { items: [], next_cursor: null };
      case "listBoardNodes":
      case "listBoardConnections":
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "getDependencyGraph":
        return answer(query);
      case "listDependencyContributions":
        return contributions(query);
    }
    const start = startPage(operation);
    if (start === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return start;
  });
}

describe("a project's dependency view", () => {
  // Verifies: FR-DEP-02/AC4
  it("draws every board with its badge's counts, and the dependencies of a cycle, which are no error", async () => {
    dependencyPages(() => graph());
    renderApp(`/projects/${testProject.id}/dependencies`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Dependencies" });
    expect(document.title).toContain("Dependencies of Harbour");
    // Each board, in the tree's order, with its counts.
    expect(
      screen.getByRole("button", {
        name: "Lighthouse Depends on 1 · Needed by 2",
      }),
    ).toBeDefined();
    const drawing = document.querySelector("[data-graph-pairs]");
    expect(drawing?.getAttribute("data-graph-boards")).toBe("3");
    expect(drawing?.getAttribute("data-graph-pairs")).toBe("3");
    expect(document.querySelectorAll("svg [data-pair]")).toHaveLength(3);
    // The cycle of Harbour and Lighthouse is no error: nothing alerts.
    expect(
      screen.queryAllByRole("alert").filter((a) => a.textContent !== ""),
    ).toEqual([]);
    expect(
      screen.getByText("3 boards, 3 dependencies between them."),
    ).toBeDefined();
    const rows = within(screen.getByRole("table")).getAllByRole("row");
    expect(
      rows.slice(1).map((r) => within(r).getAllByRole("cell")[0].textContent),
    ).toEqual(["Harbour", "Lighthouse", "Lighthouse"]);
    expect(within(rows[2]).getAllByRole("cell")[2].textContent).toBe("Jumpers");
  });

  // Verifies: FR-DEP-02/AC2
  it("inspects a dependency, listing its contributions, each end linked to its node", async () => {
    const user = userEvent.setup();
    const calls = dependencyPages(() => graph());
    renderApp(`/projects/${testProject.id}/dependencies`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Dependencies" });
    await user.click(
      screen.getByRole("button", {
        name: "Inspect what Lighthouse depends on in Harbour",
      }),
    );
    const inspected = await screen.findByRole("heading", {
      name: "Lighthouse depends on Harbour",
    });
    // The inspection, beside the graph, takes the focus from the list.
    await waitFor(() => expect(document.activeElement).toBe(inspected));
    const panel = inspected.closest("section") as HTMLElement;
    await within(panel).findByText("gold");
    expect(
      within(panel).getByRole("heading", { name: "Variables (2)" }),
    ).toBeDefined();
    expect(
      within(panel)
        .getByRole("link", { name: "The on-exit script of Greet" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/boards/bd_a?node=nd_greet`);
    expect(
      within(panel)
        .getByRole("link", { name: "The condition of a choice from Keeper" })
        .getAttribute("href"),
    ).toBe(
      `/app/projects/${testProject.id}/boards/bd_b?node=nd_keeper&connection=cn_ask`,
    );
    const asked = calls.mock.calls.find(
      ([op]) => op === "listDependencyContributions",
    );
    expect(asked?.[1]).toMatchObject({
      path: {
        project_id: testProject.id,
        from_board_id: "bd_a",
        to_board_id: "bd_b",
      },
      query: { kind: "variable" },
    });
  });

  it("filters the graph by kind, board, and variable, which the address holds", async () => {
    const user = userEvent.setup();
    const calls = dependencyPages((query) =>
      query.kind === "jumper"
        ? graph({
            edges: graph().edges.filter((e) => e.kind === "jumper"),
          })
        : graph(),
    );
    const { router } = renderApp(`/projects/${testProject.id}/dependencies`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Dependencies" });
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Kind" }),
      "jumper",
    );
    await screen.findByText("3 boards, 1 dependency between them.");
    expect(router.state.location.search).toEqual({ kind: "jumper" });
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Variable" }),
      "var_gold",
    );
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        kind: "jumper",
        variable: "var_gold",
      }),
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Board" }),
      "bd_b",
    );
    await waitFor(() =>
      expect(
        calls.mock.calls.some(
          ([op, input]) =>
            op === "getDependencyGraph" &&
            JSON.stringify((input as { query: unknown }).query) ===
              JSON.stringify({
                kind: "jumper",
                board_id: "bd_b",
                variable_id: "var_gold",
              }),
        ),
      ).toBe(true),
    );
    await user.click(screen.getByRole("button", { name: "Clear the filters" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("selects a board, which it offers to open and to keep the view to", async () => {
    const user = userEvent.setup();
    dependencyPages(() => graph());
    renderApp(`/projects/${testProject.id}/dependencies`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Dependencies" });
    const mill = screen.getByRole("button", {
      name: "Mill Depends on 1 · Needed by 0",
    });
    await user.click(mill);
    expect(mill.getAttribute("aria-pressed")).toBe("true");
    const panel = screen.getByRole("complementary", { name: "Selected" });
    expect(within(panel).getByText(/no board depends on it/)).toBeDefined();
    expect(
      within(panel)
        .getByRole("link", { name: "Open the board Mill" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/boards/bd_c`);
    expect(
      within(panel)
        .getByRole("link", { name: "Show only the dependencies of Mill" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/dependencies?board=bd_c`);
  });

  it("badges a board's page with its counts, linked to the view of its dependencies, which follows an edit", async () => {
    const realtime = fakeRealtime();
    let answer = graph();
    dependencyPages(() => answer);
    renderApp(`/projects/${testProject.id}/boards/bd_b`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Lighthouse" });
    const badge = await screen.findByRole("link", {
      name: "Depends on 1 other board · 2 boards depend on it",
    });
    expect(badge.getAttribute("href")).toBe(
      `/app/projects/${testProject.id}/dependencies?board=bd_b`,
    );
    answer = graph({
      seq: testProject.seq + 1,
      boards: graph().boards.map((b) =>
        b.id === "bd_b" ? { ...b, inbound: 0 } : b,
      ),
    });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.rename", payload: { id: "bd_a", name: "Harbour" } }],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    await screen.findByRole("link", {
      name: "Depends on no other board · 2 boards depend on it",
    });
  });

  it("is read again as an operation of the project applies", async () => {
    const realtime = fakeRealtime();
    let answer = graph();
    const calls = dependencyPages(() => answer);
    renderApp(`/projects/${testProject.id}/dependencies`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Dependencies" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "getDependencyGraph").length;
    const before = reads();
    answer = graph({ edges: [], seq: testProject.seq + 1 });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.rename", payload: { id: "bd_a", name: "Harbour" } }],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    await screen.findByText("No board depends on another.");
    expect(reads()).toBe(before + 1);
  });
});
