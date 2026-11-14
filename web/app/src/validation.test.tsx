import type { Finding, Report } from "@storycreep/shared/api";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "./test/app";
import { fakeApi, startPage, testProject } from "./test/fake";
import { fakeRealtime } from "./test/realtime";

// A project's validation (PRD FR-DEP-03, plan note N77), as the router
// shows it, over a fake of the API's client.

afterEach(() => {
  vi.restoreAllMocks();
});

/** A finding of the board Harbour, bd_a. */
function finding(more: Partial<Finding> & Pick<Finding, "code">): Finding {
  return {
    severity: "warning",
    message: `The message of ${more.code}.`,
    entity_type: "node",
    entity_id: "nd_a",
    board_id: "bd_a",
    board_name: "Harbour",
    node_id: "nd_a",
    node_title: "Greet",
    ...more,
  };
}

const findings: Finding[] = [
  finding({
    code: "expr_invalid",
    severity: "error",
    entity_type: "connection",
    entity_id: "cn_a",
    field: "condition",
  }),
  finding({
    code: "ref_dangling",
    severity: "error",
    entity_type: "project",
    entity_id: testProject.id,
    field: "entry",
    board_id: undefined,
    board_name: undefined,
    node_id: undefined,
    node_title: undefined,
  }),
  finding({ code: "dead_end" }),
  finding({
    code: "port_unconnected",
    entity_type: "branch_output",
    entity_id: "bo_a",
    node_title: "",
  }),
  finding({
    code: "board_entry_missing",
    entity_type: "board",
    entity_id: "bd_b",
    board_id: "bd_b",
    board_name: "Lighthouse",
    node_id: undefined,
    node_title: undefined,
  }),
  finding({
    code: "var_written_never_read",
    entity_type: "variable",
    entity_id: "var_gold",
    board_id: undefined,
    board_name: undefined,
    node_id: undefined,
    node_title: undefined,
  }),
  finding({ code: "speaker_missing", severity: "info" }),
];

/** The report of the findings that are not muted, and of muted. */
function report(not: Finding[], muted: Finding[] = [], seq = 1): Report {
  return {
    seq,
    findings: not,
    muted,
    counts: {
      errors: not.filter((f) => f.severity === "error").length,
      warnings: not.filter((f) => f.severity === "warning").length,
      infos: not.filter((f) => f.severity === "info").length,
      muted: muted.length,
    },
  };
}

/** Answers the validation's page, of the role, with the report that answer returns, and its batches as applied. */
function validationPage(
  answer: () => Report,
  role: (typeof testProject)["role"] = "owner",
) {
  let seq = testProject.seq;
  return fakeApi((operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, role };
      case "validateProject":
        return answer();
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
              created_at: "2026-11-01T10:00:00Z",
              updated_at: "2026-11-01T10:00:00Z",
            },
          ],
          next_cursor: null,
        };
      case "listFolders":
        return { items: [], next_cursor: null };
      case "applyCommands": {
        const { commands } = (
          input as { body: { commands: { payload: { entity_id: string } }[] } }
        ).body;
        seq++;
        return {
          status: "applied",
          seq,
          revisions: commands.map((c) => ({ id: c.payload.entity_id, rev: 2 })),
          not_reconnected: [],
          undo: [],
        };
      }
    }
    const start = startPage(operation);
    if (start === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return start;
  });
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

/** Returns the region of the heading named name. */
function section(name: RegExp): HTMLElement {
  const heading = screen.getByRole("heading", { level: 2, name });
  const region = heading.closest("section");
  if (region === null) {
    throw new Error(`no section of ${name}`);
  }
  return region;
}

describe("a project's validation", () => {
  // Verifies: FR-DEP-03/AC3
  it("lists the findings by severity, each where it is, linked to the editor of what it is about", async () => {
    validationPage(() => report(findings));
    renderApp(`/projects/${testProject.id}/validation`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Validation" });
    expect(document.title).toContain("Validation of Harbour");
    const errors = section(/^Errors \(2\)$/);
    const warnings = section(/^Warnings \(4\)$/);
    const infos = section(/^Info \(1\)$/);
    expect(
      within(errors).getByText("The message of expr_invalid."),
    ).toBeDefined();
    const href = (region: HTMLElement, name: string) =>
      within(region).getByRole("link", { name }).getAttribute("href");
    // A connection's condition opens the connection, selected, in its
    // inspector, on its board.
    expect(
      href(errors, "The condition of a choice from Greet, on Harbour"),
    ).toBe(
      `/app/projects/${testProject.id}/boards/bd_a?node=nd_a&connection=cn_a`,
    );
    // The project's entry, which no page edits yet, has no link.
    expect(within(errors).getByText("The project's entry").tagName).toBe(
      "SPAN",
    );
    expect(href(warnings, "Greet, on Harbour")).toBe(
      `/app/projects/${testProject.id}/boards/bd_a?node=nd_a`,
    );
    // An output opens its branch, untitled here.
    expect(
      href(warnings, "An output of A node without a title, on Harbour"),
    ).toBe(`/app/projects/${testProject.id}/boards/bd_a?node=nd_a`);
    expect(href(warnings, "The board Lighthouse")).toBe(
      `/app/projects/${testProject.id}/boards/bd_b`,
    );
    // A variable, named by its key once the document has read them.
    await waitFor(() =>
      expect(href(warnings, "The global variable gold")).toBe(
        `/app/projects/${testProject.id}/variables?variable=var_gold`,
      ),
    );
    expect(
      within(infos).getByText("The message of speaker_missing."),
    ).toBeDefined();
    // A warning and an info may be muted, and an error may not.
    expect(within(errors).queryAllByRole("button", { name: /^Mute/ })).toEqual(
      [],
    );
    expect(
      within(warnings).getAllByRole("button", { name: /^Mute/ }),
    ).toHaveLength(4);
    expect(
      within(infos).getAllByRole("button", { name: /^Mute/ }),
    ).toHaveLength(1);
    expect(section(/^Muted \(0\)$/).textContent).toContain("Nothing is muted.");
  });

  it("names a variable whose key the project's document lacks as a variable of its scope", async () => {
    const variable = findings[5];
    const calls = validationPage(() =>
      report([
        { ...variable, entity_id: "var_gone" },
        {
          ...variable,
          entity_id: "var_gone_too",
          board_id: "bd_a",
          board_name: "Harbour",
        },
      ]),
    );
    renderApp(`/projects/${testProject.id}/validation`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Validation" });
    await waitFor(() =>
      expect(calls.mock.calls.some(([op]) => op === "listVariables")).toBe(
        true,
      ),
    );
    const warnings = section(/^Warnings \(2\)$/);
    expect(
      within(warnings)
        .getByRole("link", { name: "A global variable" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/variables?variable=var_gone`);
    expect(
      within(warnings).getByRole("link", { name: "A variable of Harbour" }),
    ).toBeDefined();
    expect(
      within(warnings).getByRole("button", { name: "Mute: A global variable" }),
    ).toBeDefined();
  });

  // Verifies: FR-DEP-03/AC4
  it("mutes a warning with a reason, which it then lists apart with its mute, and unmutes it", async () => {
    const user = userEvent.setup();
    const deadEnd = findings[2];
    const mute = {
      reason: "The scene ends the demo.",
      muted_by: testSession.user_id,
      muted_at: "2026-11-14T10:00:00Z",
    };
    let answer = report(findings);
    const calls = validationPage(() => answer);
    renderApp(`/projects/${testProject.id}/validation`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Validation" });
    await user.click(
      within(section(/^Warnings/)).getByRole("button", {
        name: "Mute: Greet, on Harbour",
      }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Mute: Greet, on Harbour",
    });
    await user.type(
      within(dialog).getByRole("textbox", { name: "Reason" }),
      "  The scene ends the demo.  ",
    );
    answer = report(
      findings.filter((f) => f !== deadEnd),
      [{ ...deadEnd, mute }],
      2,
    );
    await user.click(within(dialog).getByRole("button", { name: "Mute" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(batches(calls)).toEqual([
      [
        {
          type: "validation.mute",
          payload: {
            entity_id: "nd_a",
            code: "dead_end",
            reason: "The scene ends the demo.",
          },
        },
      ],
    ]);
    // The report is read again as the batch applies, and lists the
    // finding under Muted, with its reason, never hidden.
    const muted = await waitFor(() => section(/^Muted \(1\)$/));
    expect(within(muted).getByText("The message of dead_end.")).toBeDefined();
    expect(
      within(muted).getByText("Reason: The scene ends the demo."),
    ).toBeDefined();
    expect(within(muted).getByText(/^Muted by you/)).toBeDefined();
    expect(
      within(section(/^Warnings \(3\)$/)).queryByText(
        "The message of dead_end.",
      ),
    ).toBeNull();
    expect(
      screen
        .getByText("Muted. It is listed under Muted.")
        .closest("[role=status]"),
    ).not.toBeNull();
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("heading", { level: 2, name: /^Muted/ }),
      ),
    );

    // Its reason changes, in the same dialog, as validation.mute again.
    await user.click(
      within(muted).getByRole("button", {
        name: "Change the reason: Greet, on Harbour",
      }),
    );
    const change = await screen.findByRole("dialog", {
      name: "Change the reason: Greet, on Harbour",
    });
    const reason = within(change).getByRole("textbox", { name: "Reason" });
    expect((reason as HTMLTextAreaElement).value).toBe(mute.reason);
    await user.clear(reason);
    await user.type(reason, "Kept for the sequel.");
    await user.click(
      within(change).getByRole("button", { name: "Save the reason" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    answer = report(findings, [], 4);
    await user.click(
      within(section(/^Muted/)).getByRole("button", {
        name: "Unmute: Greet, on Harbour",
      }),
    );
    expect(batches(calls).slice(1)).toEqual([
      [
        {
          type: "validation.mute",
          payload: {
            entity_id: "nd_a",
            code: "dead_end",
            reason: "Kept for the sequel.",
          },
        },
      ],
      [
        {
          type: "validation.unmute",
          payload: { entity_id: "nd_a", code: "dead_end" },
        },
      ],
    ]);
    await waitFor(() =>
      expect(section(/^Muted \(0\)$/).textContent).toContain(
        "Nothing is muted.",
      ),
    );
    expect(
      within(section(/^Warnings \(4\)$/)).getByText("The message of dead_end."),
    ).toBeDefined();
  });

  // Verifies: FR-DEP-03/AC4
  it("shows the muted findings to a viewer, without the buttons that change them", async () => {
    const muted = {
      ...findings[2],
      mute: { reason: "Fine.", muted_at: "2026-11-14T10:00:00Z" },
    };
    validationPage(
      () =>
        report(
          findings.filter((f) => f !== findings[2]),
          [muted],
        ),
      "viewer",
    );
    renderApp(`/projects/${testProject.id}/validation`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Validation" });
    const list = section(/^Muted \(1\)$/);
    expect(within(list).getByText("Reason: Fine.")).toBeDefined();
    expect(within(list).getByText(/^Muted Nov/)).toBeDefined();
    expect(within(list).queryByText(/^Muted by you/)).toBeNull();
    expect(
      screen.queryAllByRole("button", { name: /^(Mute|Unmute|Change)/ }),
    ).toEqual([]);
  });

  it("is read again as an operation of the project applies, as another session's fix", async () => {
    const realtime = fakeRealtime();
    let answer = report(findings);
    const calls = validationPage(() => answer);
    renderApp(`/projects/${testProject.id}/validation`, {
      session: testSession,
      realtime,
    });
    await screen.findByRole("heading", { level: 1, name: "Validation" });
    const reads = () =>
      calls.mock.calls.filter(([op]) => op === "validateProject").length;
    const before = reads();
    answer = report([], [], testProject.seq + 1);
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.update", payload: { id: "nd_a", title: "Greet" } }],
        [{ id: "nd_a", rev: 2 }],
      ),
    );
    await screen.findByText("Validation finds nothing to report.");
    expect(reads()).toBe(before + 1);
  });
});
