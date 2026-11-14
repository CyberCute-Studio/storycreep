import type { Board, ProjectStatistics } from "@storycreep/shared/api";
import { QueryClient } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "./test/app";
import { fakeApi, startPage, testProject } from "./test/fake";
import { fakeRealtime } from "./test/realtime";

// A project's writing statistics (PRD FR-TEXT-06, plan note N76), as the
// router shows them, over a fake of the API's client.

afterEach(() => {
  vi.restoreAllMocks();
});

/** Returns a board of the test project, at order among its siblings. */
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
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** The statistics of the project of the boards Harbour, bd_a, and Lighthouse, bd_b, in the order of their IDs, as the server lists them. */
function statistics(more: Partial<ProjectStatistics> = {}): ProjectStatistics {
  return {
    seq: testProject.seq,
    nodes: 5,
    words: 1234,
    characters: 5678,
    choices: 3,
    conditions: 2,
    complete: true,
    boards: [
      {
        id: "bd_a",
        name: "Harbour",
        nodes: 2,
        words: 1200,
        characters: 5600,
        choices: 1,
        conditions: 0,
        complete: true,
      },
      {
        id: "bd_b",
        name: "Lighthouse",
        nodes: 3,
        words: 34,
        characters: 78,
        choices: 2,
        conditions: 2,
        complete: true,
      },
    ],
    ...more,
  };
}

/** Answers the project's pages, of Lighthouse before Harbour in the tree, and their statistics as stats returns them, or once what it returns resolves. */
function projectPages(
  stats: () => ProjectStatistics | Promise<ProjectStatistics>,
) {
  return fakeApi((operation) => {
    switch (operation) {
      case "getProject":
        return testProject;
      case "listFolders":
        return { items: [], next_cursor: null };
      case "listBoards":
        return {
          items: [
            board("bd_a", "Harbour", "W"),
            board("bd_b", "Lighthouse", "V"),
          ],
          next_cursor: null,
        };
      case "getProjectStatistics":
        return stats();
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
}

/** Returns the value of the term of the list of the project's counts. */
function projectCount(term: string): string | null | undefined {
  const region = screen.getByRole("region", { name: "The project" });
  return within(region)
    .getByText(term, { selector: "dt" })
    .parentElement?.querySelector("dd")?.textContent;
}

describe("a project's statistics", () => {
  it("show the project's counts, and each board's in the order of the tree, linked to its canvas", async () => {
    projectPages(() => statistics());
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Statistics" });
    expect(projectCount("Nodes")).toBe("5");
    expect(projectCount("Words")).toBe("1,234");
    expect(projectCount("Characters")).toBe("5,678");
    expect(projectCount("Choices")).toBe("3");
    expect(projectCount("Conditions")).toBe("2");
    const rows = within(screen.getByRole("table")).getAllByRole("row");
    expect(
      rows.map((r) =>
        within(r)
          .queryAllByRole("cell")
          .map((c) => c.textContent),
      ),
    ).toEqual([
      [],
      ["3", "34", "78", "2", "2"],
      ["2", "1,200", "5,600", "1", "0"],
    ]);
    expect(
      within(rows[1]).getByRole("link", { name: "Lighthouse" }),
    ).toHaveProperty(
      "href",
      expect.stringMatching(`/projects/${testProject.id}/boards/bd_b$`),
    );
    expect(screen.queryByText(/still being counted/)).toBeNull();
  });

  it("say that some texts are still being counted while the server has yet to count them", async () => {
    projectPages(() => statistics({ complete: false }));
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
    });
    expect(
      (await screen.findByText(/still being counted/)).closest("[role=status]"),
    ).not.toBeNull();
  });

  // Verifies: FR-TEXT-06/AC1
  it("are read again as an operation of the project applies, as another session's edit", async () => {
    const realtime = fakeRealtime();
    let stats = statistics();
    const calls = projectPages(() => stats);
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Statistics" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "getProjectStatistics").length;
    const before = reads();
    stats = statistics({ seq: testProject.seq + 1, words: 1240 });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.update", payload: { id: "nd_a", title: "Greet" } }],
        [{ id: "nd_a", rev: 2 }],
      ),
    );
    await waitFor(() => expect(projectCount("Words")).toBe("1,240"));
    expect(reads()).toBe(before + 1);
  });

  // Verifies: FR-TEXT-06/AC1
  it("are read again once for a run of operations, and once more, soon after, for the rest of the run", async () => {
    const realtime = fakeRealtime();
    let stats = statistics();
    const calls = projectPages(() => stats);
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Statistics" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "getProjectStatistics").length;
    const before = reads();
    stats = statistics({ seq: testProject.seq + 5, words: 1250 });
    act(() => {
      for (let rev = 2; rev <= 6; rev++) {
        realtime.send(
          testProject.id,
          [
            {
              type: "node.update",
              payload: { id: "nd_a", title: `Greet ${rev}` },
            },
          ],
          [{ id: "nd_a", rev }],
        );
      }
    });
    // The first of them has them read at once, and the rest once, together.
    await waitFor(() => expect(reads()).toBeGreaterThan(before));
    expect(reads()).toBe(before + 1);
    await waitFor(() => expect(reads()).toBe(before + 2));
    await waitFor(() => expect(projectCount("Words")).toBe("1,250"));
    expect(reads()).toBe(before + 2);
  });

  // Verifies: FR-TEXT-06/AC1
  it("let a read under way finish as operations apply, and are read again once it is answered, as on a slow connection", async () => {
    const realtime = fakeRealtime();
    let answer: ProjectStatistics | Promise<ProjectStatistics> = statistics();
    const calls = projectPages(() => answer);
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Statistics" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "getProjectStatistics");
    const before = reads().length;
    let answerHeld: (stats: ProjectStatistics) => void = () => {};
    answer = new Promise((resolve) => {
      answerHeld = resolve;
    });
    const send = (rev: number) =>
      act(() =>
        realtime.send(
          testProject.id,
          [
            {
              type: "node.update",
              payload: { id: "nd_a", title: `Greet ${rev}` },
            },
          ],
          [{ id: "nd_a", rev }],
        ),
      );
    send(2);
    await waitFor(() => expect(reads()).toHaveLength(before + 1));
    // Operations go on applying for longer than the read is answered in, and
    // the read is neither begun anew nor ended.
    send(3);
    await act(() => new Promise((resolve) => setTimeout(resolve, 1_200)));
    send(4);
    expect(reads()).toHaveLength(before + 1);
    const held = reads()[before][2] as { signal: AbortSignal };
    expect(held.signal.aborted).toBe(false);
    answer = statistics({ seq: testProject.seq + 3, words: 1250 });
    act(() =>
      answerHeld(statistics({ seq: testProject.seq + 1, words: 1240 })),
    );
    await waitFor(() => expect(projectCount("Words")).toBe("1,250"));
    expect(reads()).toHaveLength(before + 2);
  });

  // Verifies: FR-TEXT-06/AC1
  it("wait for the answer of a read under way, rather than look again at the end of each half second while it is under way", async () => {
    const invalidated = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    const realtime = fakeRealtime();
    let answer: ProjectStatistics | Promise<ProjectStatistics> = statistics();
    const calls = projectPages(() => answer);
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Statistics" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "getProjectStatistics");
    const followed = () =>
      invalidated.mock.calls.filter(
        ([filters]) => filters?.queryKey?.[2] === "statistics",
      ).length;
    const before = reads().length;
    let answerHeld: (stats: ProjectStatistics) => void = () => {};
    answer = new Promise((resolve) => {
      answerHeld = resolve;
    });
    const send = (rev: number) =>
      act(() =>
        realtime.send(
          testProject.id,
          [
            {
              type: "node.update",
              payload: { id: "nd_a", title: `Greet ${rev}` },
            },
          ],
          [{ id: "nd_a", rev }],
        ),
      );
    send(2);
    await waitFor(() => expect(reads()).toHaveLength(before + 1));
    send(3);
    // The end of the wait finds the read under way, and nothing more is
    // done until it is answered, however long it takes.
    await act(() => new Promise((resolve) => setTimeout(resolve, 600)));
    const looked = followed();
    await act(() => new Promise((resolve) => setTimeout(resolve, 1_100)));
    expect(followed()).toBe(looked);
    expect(reads()).toHaveLength(before + 1);
    answer = statistics({ seq: testProject.seq + 2, words: 1250 });
    act(() =>
      answerHeld(statistics({ seq: testProject.seq + 1, words: 1240 })),
    );
    await waitFor(() => expect(projectCount("Words")).toBe("1,250"));
    expect(reads()).toHaveLength(before + 2);
  });

  // Verifies: FR-TEXT-06/AC1
  it("are read again for an operation that applies while the page's first read of them is under way", async () => {
    const realtime = fakeRealtime();
    let release: () => void = () => {};
    const first = new Promise<void>((resolve) => {
      release = resolve;
    });
    let n = 0;
    projectPages(async () => {
      n++;
      if (n === 1) {
        await first;
        return statistics();
      }
      return statistics({ seq: testProject.seq + 1, words: 1240 });
    });
    renderApp(`/projects/${testProject.id}/statistics`, {
      session: testSession,
      realtime,
    });
    await waitFor(() => {
      expect(n).toBe(1);
      expect(realtime.subscribed(testProject.id)).toBe(true);
    });
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.update", payload: { id: "nd_a", title: "Greet" } }],
        [{ id: "nd_a", rev: 2 }],
      ),
    );
    act(() => release());
    await waitFor(() => expect(projectCount("Words")).toBe("1,240"));
    expect(n).toBe(2);
  });
});
