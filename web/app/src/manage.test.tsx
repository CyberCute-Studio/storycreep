import {
  type Board,
  type MemberBody,
  type MembershipBody,
  NetworkError,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "./test/app";
import {
  fakeApi,
  refused,
  startPage,
  testProject,
  testWorkspace,
} from "./test/fake";
import { fakeRealtime } from "./test/realtime";

// The management screens as the router shows them, over a fake of the API's
// client: the workspace that the project list opens, the switcher, what an
// admin may do to the members and their invitations, and the tree of a
// project's boards, whose deletion lists what it would break.

afterEach(() => {
  vi.restoreAllMocks();
});

/** Returns a membership of the test account in a workspace of its own. */
function workspace(
  id: string,
  name: string,
  role: MembershipBody["role"] = "owner",
): MembershipBody {
  return {
    ...testWorkspace,
    membership_id: `mem_${id}`,
    id,
    name,
    slug: id,
    role,
  };
}

/** Returns a member of a workspace. */
function member(
  id: string,
  name: string,
  role: MemberBody["role"],
): MemberBody {
  return {
    id,
    user_id: `usr_${id}`,
    display_name: name,
    email: `${id}@example.com`,
    role,
    joined_at: "2026-11-01T10:00:00Z",
  };
}

/** Returns a board of the test project. */
function board(id: string, name: string): Board {
  return {
    id,
    project_id: testProject.id,
    name,
    slug: id,
    description: "",
    color: "none",
    order: id === "bd_a" ? "V" : "W",
    rev: 3,
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
  };
}

/** Answers what every page of the test project reads: the project, its workspace, and its tree. */
function projectPages(operation: string, boards: Board[]): unknown {
  switch (operation) {
    case "getProject":
      return testProject;
    case "listFolders":
      return { items: [], next_cursor: null };
    case "listBoards":
      return { items: boards, next_cursor: null };
  }
  return startPage(operation as Parameters<typeof startPage>[0]);
}

/** What a board's page says once its form is saved. */
const saved = "Saved.";

/**
 * Returns the server's refusal of a batch of one command of type for the
 * revision of the board state, which changed elsewhere since the page read
 * it at rev 3, with the board as it is now.
 */
function conflict(type: string, state: Board) {
  return refused(409, {
    code: "revision_conflict",
    details: {
      conflicts: [
        {
          command: 0,
          command_id: "00000000-0000-7000-8000-000000000001",
          type,
          id: state.id,
          expected_rev: 3,
          exists: true,
          current_rev: state.rev,
        },
      ],
      states: { [state.id]: state },
    },
  });
}

/** Returns the answer to a batch that applied as seq, leaving revisions. */
function applied(seq: number, revisions: { id: string; rev: number }[]) {
  return { status: "applied", seq, revisions, not_reconnected: [], undo: [] };
}

/** Answers with answer, or fails the test's request of an operation that it does not expect. */
function expected(answer: unknown, operation: string): unknown {
  if (answer === undefined) {
    throw new Error(`unexpected ${operation}`);
  }
  return answer;
}

describe("the project list", () => {
  it("opens the workspace that the device last showed the account", async () => {
    const workspaces = [
      workspace("ws_a", "Studio A"),
      workspace("ws_b", "Studio B"),
    ];
    fakeApi((operation) =>
      expected(startPage(operation, workspaces), operation),
    );
    localStorage.setItem("storycreep.workspace.usr_test", "ws_b");
    const { router } = renderApp("/projects", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(router.state.location.pathname).toBe("/workspaces/ws_b");
  });

  it("opens the first workspace once the account has left the one that the device last showed", async () => {
    fakeApi((operation) => {
      if (operation === "getWorkspace") {
        throw refused(404, { code: "not_found" });
      }
      return expected(startPage(operation), operation);
    });
    localStorage.setItem("storycreep.workspace.usr_test", "ws_left");
    const { router, log } = renderApp("/projects", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(router.state.location.pathname).toBe("/workspaces/ws_studio");
    // Not finding it is no error of the app's.
    expect(log.entries()).toEqual([]);
  });
});

describe("a workspace's pages", () => {
  it("show the account's membership whatever the case that the address spells the workspace's ID in", async () => {
    const id = newId("workspace");
    fakeApi((operation) =>
      expected(
        startPage(operation, [{ ...testWorkspace, id, slug: "studio" }]),
        operation,
      ),
    );
    renderApp(`/workspaces/${id.toLowerCase()}`, { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    // The project list opens it next, by its ID as the server writes it.
    await waitFor(() =>
      expect(localStorage.getItem("storycreep.workspace.usr_test")).toBe(id),
    );
  });

  it("are the page of no page once the user has been removed from the workspace since the page read it, which is no error of the app's", async () => {
    fakeApi((operation) => {
      if (operation === "listTrashedProjects") {
        throw refused(404, { code: "not_found" });
      }
      return expected(startPage(operation), operation);
    });
    const { log } = renderApp("/workspaces/ws_studio/trash", {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Page not found" });
    expect(log.entries()).toEqual([]);
  });

  it("send only the settings that changed, and keep what the user typed once the workspace has changed elsewhere", async () => {
    const user = userEvent.setup();
    let studio = testWorkspace;
    const calls = fakeApi((operation, input) => {
      if (operation === "updateWorkspace") {
        studio = { ...studio, ...(input as { body: object }).body };
        return studio;
      }
      return expected(startPage(operation, [studio]), operation);
    });
    const { queryClient } = renderApp("/workspaces/ws_studio/settings", {
      session: testSession,
    });
    const name = await screen.findByLabelText("Name");
    await user.clear(name);
    await user.type(name, "North");
    // Another admin changes the slug, which the page reads again.
    studio = { ...studio, slug: "harbour-studio" };
    await queryClient.invalidateQueries({ queryKey: ["workspaces"] });
    expect(screen.getByLabelText("Name")).toHaveProperty("value", "North");
    await user.click(screen.getByRole("button", { name: "Save the settings" }));
    await screen.findByText("Saved.");
    expect(
      calls.mock.calls
        .filter(([operation]) => operation === "updateWorkspace")
        .map(([, input]) => (input as { body: unknown }).body),
    ).toEqual([{ name: "North" }]);
    // Saved, the form shows the workspace as it is now.
    expect(screen.getByLabelText("Slug")).toHaveProperty(
      "value",
      "harbour-studio",
    );
  });
});

describe("the workspace switcher", () => {
  it("is hidden while the account has one workspace", async () => {
    fakeApi((operation) => expected(startPage(operation), operation));
    renderApp("/workspaces/ws_studio", { session: testSession });
    await screen.findByRole("heading", { level: 1, name: "Projects" });
    expect(
      screen.queryByRole("button", { name: /switch to another workspace/ }),
    ).toBeNull();
    expect(screen.getByText("Studio")).toBeDefined();
  });

  it("lists the account's other workspaces to switch to, as links", async () => {
    const user = userEvent.setup();
    const workspaces = [
      workspace("ws_studio", "Studio"),
      workspace("ws_b", "Studio B"),
    ];
    fakeApi((operation) =>
      expected(startPage(operation, workspaces), operation),
    );
    const { router } = renderApp("/workspaces/ws_studio", {
      session: testSession,
    });
    await user.click(
      await screen.findByRole("button", {
        name: "Studio, switch to another workspace",
      }),
    );
    const menu = await screen.findByRole("menu");
    expect(
      within(menu).getByRole("menuitem", { name: "Studio" }),
    ).toHaveProperty("ariaCurrent", "page");
    await user.click(within(menu).getByRole("menuitem", { name: "Studio B" }));
    await screen.findByRole("button", {
      name: "Studio B, switch to another workspace",
    });
    expect(router.state.location.pathname).toBe("/workspaces/ws_b");
  });

  it("lists an instance admin's own workspace in one that they act in without being a member, which the project list does not open again", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "getWorkspace") {
        return { ...testWorkspace, id: "ws_other", name: "Other" };
      }
      return expected(startPage(operation), operation);
    });
    renderApp("/workspaces/ws_other", { session: testSession });
    await user.click(
      await screen.findByRole("button", {
        name: "Other, switch to another workspace",
      }),
    );
    const menu = await screen.findByRole("menu");
    expect(
      within(menu).getByRole("menuitem", { name: "Studio" }),
    ).toBeDefined();
    expect(localStorage.getItem("storycreep.workspace.usr_test")).toBeNull();
  });
});

describe("the members", () => {
  it("are the page of no page to a guest, without asking for them", async () => {
    fakeApi((operation) =>
      expected(
        startPage(operation, [workspace("ws_inn", "Inn", "guest")]),
        operation,
      ),
    );
    const { log } = renderApp("/workspaces/ws_inn/members", {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Page not found" });
    expect(log.entries()).toEqual([]);
  });

  it("offer an admin roles up to admin, and nothing to change of an owner", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "listMembers":
          return {
            items: [
              member("mem_o", "Olga Owner", "owner"),
              member("mem_e", "Ed Editor", "editor"),
            ],
            next_cursor: null,
          };
        case "listInvites":
          return { items: [], next_cursor: null };
      }
      return expected(
        startPage(operation, [workspace("ws_studio", "Studio", "admin")]),
        operation,
      );
    });
    renderApp("/workspaces/ws_studio/members", { session: testSession });
    const members = await screen.findByRole("list", { name: "Members" });
    const [owner, editor] = within(members).getAllByRole("listitem");
    expect(within(owner).queryByRole("button")).toBeNull();
    await user.click(
      within(editor).getByRole("button", { name: "Change the role" }),
    );
    const dialog = await screen.findByRole("dialog");
    const roles = within(within(dialog).getByLabelText("Role")).getAllByRole(
      "option",
    );
    expect(roles.map((option) => option.textContent)).toEqual([
      "Admin",
      "Editor",
      "Commenter",
      "Viewer",
    ]);
  });

  it("show an invitation's link once, in a dialog that a click outside it does not close", async () => {
    // The dialog takes the pointer events of the page outside it, which a
    // click there goes to all the same.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    fakeApi((operation) => {
      switch (operation) {
        case "listMembers":
        case "listInvites":
          return { items: [], next_cursor: null };
        case "getWorkspaceUsage":
          return {
            projects: { used: 0, warning: false },
            members: { used: 4, limit: 5, warning: true },
            storage_bytes: { used: 0, warning: false },
            boards: { used: 0 },
            nodes: { used: 0 },
            snapshots: { used: 0 },
            api_requests: { used: 0 },
          };
        case "createInvite":
          return {
            id: "inv_1",
            kind: "link",
            role: "editor",
            status: "pending",
            expires_at: "2026-11-18T10:00:00Z",
            created_at: "2026-11-04T10:00:00Z",
            url: "http://localhost/app/invite#token=secret",
          };
      }
      return expected(startPage(operation), operation);
    });
    renderApp("/workspaces/ws_studio/members", { session: testSession });
    await user.click(await screen.findByRole("button", { name: "Invite" }));
    const dialog = await screen.findByRole("dialog");
    // The workspace is close to its limit of members, which the dialog says.
    await within(dialog).findByText(
      "The workspace has 4 of the 5 members that it may have, guests included. Close to the limit.",
    );
    await user.click(within(dialog).getByLabelText("Anyone with the link"));
    expect(within(dialog).queryByLabelText("Email address")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Invite" }));
    const link = await within(dialog).findByLabelText("Link of the invitation");
    expect(link).toHaveProperty(
      "value",
      "http://localhost/app/invite#token=secret",
    );
    await user.click(document.body);
    expect(dialog.isConnected).toBe(true);
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      screen.queryByDisplayValue("http://localhost/app/invite#token=secret"),
    ).toBeNull();
  });

  it("say that an address that an admin invites belongs to a member already", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "listMembers":
        case "listInvites":
          return { items: [], next_cursor: null };
        case "getWorkspaceUsage":
          throw refused(503, { code: "not_ready" });
        case "createInvite":
          throw refused(409, { code: "already_member" });
      }
      return expected(startPage(operation), operation);
    });
    renderApp("/workspaces/ws_studio/members", { session: testSession });
    await user.click(await screen.findByRole("button", { name: "Invite" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(
      within(dialog).getByLabelText("Email address"),
      "ed@example.com",
    );
    await user.click(within(dialog).getByRole("button", { name: "Invite" }));
    expect(await within(dialog).findByRole("alert")).toHaveProperty(
      "textContent",
      "This email address belongs to a member of the workspace already.",
    );
  });

  it("read the invitations and the members again once an invitation that an admin sends anew has been accepted elsewhere", async () => {
    const user = userEvent.setup();
    let accepted = false;
    fakeApi((operation) => {
      switch (operation) {
        case "listMembers":
          return {
            items: [
              member("mem_o", "Olga Owner", "owner"),
              ...(accepted ? [member("mem_j", "Jo Joiner", "editor")] : []),
            ],
            next_cursor: null,
          };
        case "listInvites":
          return {
            items: accepted
              ? []
              : [
                  {
                    id: "inv_jo",
                    kind: "email",
                    email: "jo@example.com",
                    role: "editor",
                    status: "pending",
                    expires_at: "2026-11-18T10:00:00Z",
                    created_at: "2026-11-04T10:00:00Z",
                  },
                ],
            next_cursor: null,
          };
        case "resendInvite":
          // Jo accepted the invitation once the page had listed it.
          accepted = true;
          throw refused(410, { code: "invite_accepted" });
      }
      return expected(startPage(operation), operation);
    });
    renderApp("/workspaces/ws_studio/members", { session: testSession });
    await user.click(await screen.findByRole("button", { name: "Send again" }));
    await screen.findByText(
      "The invitation of jo@example.com is no longer open: someone accepted or revoked it in the meantime.",
    );
    await screen.findByText("No open invitations.");
    const members = screen.getByRole("list", { name: "Members" });
    expect(within(members).getByText("Jo Joiner")).toBeDefined();
  });
});

describe("the access to a project", () => {
  it("takes away a grant that another admin gave since the page read the grants", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation) => {
      switch (operation) {
        case "getProject":
          return testProject;
        case "listProjectGrants":
          return { items: [], next_cursor: null };
        case "listMembers":
          return {
            items: [member("mem_e", "Ed Editor", "editor")],
            next_cursor: null,
          };
        case "removeProjectGrant":
          return undefined;
      }
      return expected(startPage(operation), operation);
    });
    renderApp(`/projects/${testProject.id}/access`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Change access" }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByLabelText("Role on this project"),
    ).toHaveProperty("value", "");
    await user.click(
      within(dialog).getByRole("button", { name: "Save the access" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      calls.mock.calls.filter(
        ([operation]) => operation === "removeProjectGrant",
      ),
    ).toHaveLength(1);
  });
});

describe("the tree of boards", () => {
  it("lists the jumpers that a board's deletion would break, and confirms it", async () => {
    const user = userEvent.setup();
    const calls = fakeApi((operation) => {
      switch (operation) {
        case "listBoardReferrers":
          return {
            items: [{ id: "nd_door", board_id: "bd_b", title: "The door" }],
            next_cursor: null,
          };
        case "applyCommands":
          return {
            status: "applied",
            seq: 2,
            revisions: [],
            not_reconnected: [],
            undo: [],
          };
      }
      return expected(
        projectPages(operation, [
          board("bd_a", "Harbour"),
          board("bd_b", "Lighthouse"),
        ]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
    const dialog = await screen.findByRole("alertdialog");
    await within(dialog).findByText("The door, on Lighthouse");
    await user.click(
      within(dialog).getByRole("button", {
        name: "Delete it, and leave the jumper broken",
      }),
    );
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    const sent = calls.mock.calls.find(
      ([operation]) => operation === "applyCommands",
    )?.[1] as {
      body: {
        commands: { type: string; payload: unknown; expected_rev: unknown }[];
      };
    };
    expect(sent.body.commands).toMatchObject([
      {
        type: "board.delete",
        payload: { id: "bd_a", confirm: true },
        expected_rev: { bd_a: 3 },
      },
    ]);
  });

  it("lists the jumpers that lead to a board since the deletion read them, to confirm it then", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    fakeApi((operation) => {
      switch (operation) {
        case "listBoardReferrers":
          return { items: [], next_cursor: null };
        case "applyCommands":
          attempts++;
          throw refused(409, {
            code: "board_referenced",
            details: {
              jumpers: [{ id: "nd_door", board_id: "bd_b", title: "The door" }],
              jumper_count: 1,
            },
          });
      }
      return expected(
        projectPages(operation, [
          board("bd_a", "Harbour"),
          board("bd_b", "Lighthouse"),
        ]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(
      await within(dialog).findByRole("button", { name: "Delete the board" }),
    );
    await within(dialog).findByText("The door, on Lighthouse");
    expect(
      within(dialog).getByRole("button", {
        name: "Delete it, and leave the jumper broken",
      }),
    ).toBeDefined();
    expect(attempts).toBe(1);
  });

  it("sends a rename again with the IDs of its commands once no answer came to it", async () => {
    const user = userEvent.setup();
    const sent: string[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { command_id: string }[] };
        };
        sent.push(body.commands[0].command_id);
        if (sent.length === 1) {
          throw new NetworkError(new TypeError("Failed to fetch"));
        }
        return {
          status: "already_applied",
          seq: 2,
          revisions: [],
          not_reconnected: [],
          undo: [],
        };
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("New name");
    await user.clear(name);
    await user.type(name, "Harbour at night");
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await within(dialog).findByRole("alert");
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(sent).toHaveLength(2);
    expect(sent[1]).toBe(sent[0]);
  });

  it("sends a rename again with the IDs of its commands once no answer came to it, though another was sent meanwhile", async () => {
    const user = userEvent.setup();
    const sent: { name: string; id: string }[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { command_id: string; payload: unknown }[] };
        };
        const [command] = body.commands;
        sent.push({
          name: (command.payload as { name: string }).name,
          id: command.command_id,
        });
        if (sent.length < 3) {
          throw new NetworkError(new TypeError("Failed to fetch"));
        }
        return {
          status: "already_applied",
          seq: 2,
          revisions: [],
          not_reconnected: [],
          undo: [],
        };
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("New name");
    const rename = async (to: string) => {
      await user.clear(name);
      await user.type(name, to);
      await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    };
    await rename("Harbour at night");
    await within(dialog).findByRole("alert");
    await rename("Harbour at dawn");
    await waitFor(() => expect(sent).toHaveLength(2));
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "Rename" }),
      ).toHaveProperty("disabled", false),
    );
    await rename("Harbour at night");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(sent.map((s) => s.name)).toEqual([
      "Harbour at night",
      "Harbour at dawn",
      "Harbour at night",
    ]);
    expect(sent[1].id).not.toBe(sent[0].id);
    expect(sent[2].id).toBe(sent[0].id);
  });

  it("sends a rename again by itself, expecting the board's revision now, once it was refused for another change of the board elsewhere", async () => {
    const user = userEvent.setup();
    const revisions: unknown[] = [];
    let current = board("bd_a", "Harbour");
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { expected_rev: unknown }[] };
        };
        revisions.push(body.commands[0].expected_rev);
        if (revisions.length === 1) {
          // Another editor gave the board a colour since the tree read it,
          // which the rename does not change.
          current = { ...current, color: "blue", rev: 4 };
          throw conflict("board.rename", current);
        }
        current = { ...current, name: "Harbour at night", rev: 5 };
        return applied(3, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(projectPages(operation, [current]), operation);
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("New name");
    await user.clear(name);
    await user.type(name, "Harbour at night");
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(revisions).toEqual([{ bd_a: 3 }, { bd_a: 4 }]);
    expect(
      await screen.findByRole("link", { name: "Harbour at night" }),
    ).toBeDefined();
  });

  it("shows a rename refused for a rename of the board elsewhere, and sends it again, once asked, expecting the revision that the refusal gave", async () => {
    const user = userEvent.setup();
    const revisions: unknown[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { expected_rev: unknown }[] };
        };
        revisions.push(body.commands[0].expected_rev);
        if (revisions.length === 1) {
          throw conflict("board.rename", {
            ...board("bd_a", "Old harbour"),
            rev: 4,
          });
        }
        return applied(3, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("New name");
    await user.clear(name);
    await user.type(name, "Harbour at night");
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await within(dialog).findByRole("alert");
    // The tree, behind the dialog, shows the other editor's name meanwhile.
    expect(
      await screen.findByRole("link", { name: "Old harbour", hidden: true }),
    ).toBeDefined();
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(revisions).toEqual([{ bd_a: 3 }, { bd_a: 4 }]);
  });

  it("sends a rename expecting the revision of the board that the dialog opened with, rather than the one that another session's rename gave it meanwhile, which it would write over unasked", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const revisions: Record<string, number>[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { expected_rev: Record<string, number> }[] };
        };
        revisions.push(body.commands[0].expected_rev);
        if (body.commands[0].expected_rev.bd_a !== 4) {
          throw conflict("board.rename", {
            ...board("bd_a", "Old harbour"),
            rev: 4,
          });
        }
        return applied(testProject.seq + 2, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, {
      session: testSession,
      realtime,
    });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename…" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("New name");
    await user.clear(name);
    await user.type(name, "Harbour at night");
    // Another editor renames the board meanwhile, which the tree shows.
    act(() =>
      realtime.send(
        testProject.id,
        [
          {
            type: "board.rename",
            payload: { id: "bd_a", name: "Old harbour" },
          },
        ],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await within(dialog).findByRole("alert");
    expect(revisions).toEqual([{ bd_a: 3 }]);
    // Asked again, once the refusal has said so, it renames it over theirs.
    await user.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(revisions).toEqual([{ bd_a: 3 }, { bd_a: 4 }]);
  });

  it("lets a board's deletion be cancelled while it reads the jumpers that the deletion would break", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "listBoardReferrers") {
        // No answer comes, as while the browser has no connection.
        return new Promise(() => {});
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(
      await screen.findByRole("button", { name: "Actions of Harbour" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("button", { name: "Delete the board" }),
    ).toHaveProperty("disabled", true);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("reads the tree again once no answer came to a board's creation, which shows the board if it was created", async () => {
    const user = userEvent.setup();
    const boards = [board("bd_a", "Harbour")];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { payload: { id: string; name: string } }[] };
        };
        // The server created the board, but its answer did not come.
        const [{ payload }] = body.commands;
        boards.push(board(payload.id, payload.name));
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return expected(projectPages(operation, boards), operation);
    });
    renderApp(`/projects/${testProject.id}`, { session: testSession });
    await user.click(await screen.findByRole("button", { name: "New board" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(
      within(dialog).getByLabelText("Name of the board"),
      "Lighthouse",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create the board" }),
    );
    await within(dialog).findByRole("alert");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(
      await screen.findByRole("link", { name: "Lighthouse" }),
    ).toBeDefined();
  });
});

describe("a board's page", () => {
  // Verifies: FR-RT-02/AC3
  it("keeps what the user typed while another session's change of the board applies, and sends it again by itself once it was refused for that change", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const harbour = board("bd_a", "Harbour");
    const sent: unknown[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { commands } = (input as { body: { commands: unknown[] } }).body;
        sent.push(commands);
        if (sent.length === 1) {
          throw conflict("board.describe", {
            ...harbour,
            color: "blue",
            rev: 4,
          });
        }
        return applied(testProject.seq + 2, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(projectPages(operation, [harbour]), operation);
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
      realtime,
    });
    await user.type(
      await screen.findByLabelText("Description"),
      "Where the story starts",
    );
    // Another editor gives the board a colour, which comes over the channel.
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.set_color", payload: { id: "bd_a", color: "blue" } }],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    expect(screen.getByLabelText("Description")).toHaveProperty(
      "value",
      "Where the story starts",
    );
    expect(screen.getByLabelText("Colour")).toHaveProperty("value", "none");
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    await screen.findByText(saved);
    expect(sent).toEqual([
      [
        {
          type: "board.describe",
          payload: { id: "bd_a", description: "Where the story starts" },
          expected_rev: { bd_a: 3 },
          command_id: expect.any(String),
        },
      ],
      [
        {
          type: "board.describe",
          payload: { id: "bd_a", description: "Where the story starts" },
          expected_rev: { bd_a: 4 },
          command_id: expect.any(String),
        },
      ],
    ]);
    // Saved, the form shows the board as it is now, with both changes.
    await waitFor(() =>
      expect(screen.getByLabelText("Colour")).toHaveProperty("value", "blue"),
    );
    expect(screen.getByLabelText("Description")).toHaveProperty(
      "value",
      "Where the story starts",
    );
  });

  // Verifies: FR-RT-02/AC3
  it("keeps the focus in a field that the user is in, before typing in it, while another session's change of the board applies, and shows the change once the focus leaves the form", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    fakeApi((operation) =>
      expected(projectPages(operation, [board("bd_a", "Harbour")]), operation),
    );
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
      realtime,
    });
    const description = await screen.findByLabelText("Description");
    await user.click(description);
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.set_color", payload: { id: "bd_a", color: "blue" } }],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    expect(document.activeElement).toBe(description);
    expect(description.isConnected).toBe(true);
    await user.click(document.body);
    await waitFor(() =>
      expect(screen.getByLabelText("Colour")).toHaveProperty("value", "blue"),
    );
  });

  it("names a field that another editor changed too with both values, and keeps the user's once saved again, without what the user did not change", async () => {
    const user = userEvent.setup();
    const harbour = board("bd_a", "Harbour");
    // The other editor changed the colour too, which the user did not, and
    // which saving again leaves as they made it.
    const theirs = {
      ...harbour,
      description: "Where the tale ends",
      color: "blue" as const,
      rev: 4,
    };
    const sent: unknown[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { commands } = (input as { body: { commands: unknown[] } }).body;
        sent.push(commands);
        if (sent.length === 1) {
          throw conflict("board.describe", theirs);
        }
        return applied(testProject.seq + 1, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(projectPages(operation, [harbour]), operation);
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    await user.type(
      await screen.findByLabelText("Description"),
      "Where the story starts",
    );
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    expect(
      await screen.findByText(
        "Description: now “Where the tale ends”, yours “Where the story starts”",
      ),
    ).toBeDefined();
    expect(screen.getByLabelText("Description")).toHaveProperty(
      "value",
      "Where the story starts",
    );
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    await screen.findByText(saved);
    expect(sent).toHaveLength(2);
    expect(sent[1]).toEqual([
      {
        type: "board.describe",
        payload: { id: "bd_a", description: "Where the story starts" },
        expected_rev: { bd_a: 4 },
        command_id: expect.any(String),
      },
    ]);
  });

  it("shows the board as the server has it once a save was refused for a field that another editor changed too, but for what the user changed, so that what the user changes after is over what it shows", async () => {
    const user = userEvent.setup();
    const harbour = board("bd_a", "Harbour");
    // The other editor described the board too, which the user did not.
    const theirs = {
      ...harbour,
      name: "Old harbour",
      description: "Where the tale ends",
      rev: 4,
    };
    const sent: unknown[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { commands } = (input as { body: { commands: unknown[] } }).body;
        sent.push(commands);
        if (sent.length === 1) {
          throw conflict("board.rename", theirs);
        }
        return applied(testProject.seq + 1, [{ id: "bd_a", rev: 5 }]);
      }
      return expected(projectPages(operation, [harbour]), operation);
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    const name = await screen.findByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Harbour at night");
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    expect(
      await screen.findByText(
        "Name: now “Old harbour”, yours “Harbour at night”",
      ),
    ).toBeDefined();
    expect(screen.getByLabelText("Name")).toHaveProperty(
      "value",
      "Harbour at night",
    );
    expect(screen.getByLabelText("Description")).toHaveProperty(
      "value",
      "Where the tale ends",
    );
    await user.type(screen.getByLabelText("Description"), " and begins");
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    await screen.findByText(saved);
    expect(sent[1]).toEqual([
      {
        type: "board.rename",
        payload: { id: "bd_a", name: "Harbour at night" },
        expected_rev: { bd_a: 4 },
        command_id: expect.any(String),
      },
      {
        type: "board.describe",
        payload: { id: "bd_a", description: "Where the tale ends and begins" },
        expected_rev: { bd_a: 4 },
        command_id: expect.any(String),
      },
    ]);
  });

  // Verifies: FR-RT-02/AC3
  it("keeps the focus in the form once a save with nothing changed shows the board as another session's change left it, and shows the next change once the focus leaves the form", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    fakeApi((operation) =>
      expected(projectPages(operation, [board("bd_a", "Harbour")]), operation),
    );
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
      realtime,
    });
    await user.click(await screen.findByLabelText("Description"));
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.set_color", payload: { id: "bd_a", color: "blue" } }],
        [{ id: "bd_a", rev: 4 }],
      ),
    );
    expect(screen.getByLabelText("Colour")).toHaveProperty("value", "none");
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    expect(
      await screen.findByText("Nothing has changed, so nothing was saved."),
    ).toBeDefined();
    expect(screen.getByLabelText("Colour")).toHaveProperty("value", "blue");
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Save the board" }),
    );
    await user.click(document.body);
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "board.set_color", payload: { id: "bd_a", color: "red" } }],
        [{ id: "bd_a", rev: 5 }],
      ),
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Colour")).toHaveProperty("value", "red"),
    );
  });

  it("shows a board that another session made since the tree was read, which the document reads anew while its channel is not live", async () => {
    let boards = [board("bd_a", "Harbour")];
    fakeApi((operation) =>
      expected(projectPages(operation, boards), operation),
    );
    const { router } = renderApp(`/projects/${testProject.id}`, {
      session: testSession,
    });
    await screen.findByRole("link", { name: "Harbour" });
    boards = [board("bd_a", "Harbour"), board("bd_b", "Lighthouse")];
    await router.navigate({
      to: "/projects/$projectId/boards/$boardId",
      params: { projectId: testProject.id, boardId: "bd_b" },
    });
    expect(
      await screen.findByRole("heading", { name: "Lighthouse" }),
    ).toBeDefined();
  });

  it("says that it could not show a board that the tree does not have once the project's read anew failed, rather than that there is no such page", async () => {
    // The wait before the document reads the project again is longer than
    // the test.
    vi.spyOn(Math, "random").mockReturnValue(1);
    let projectReads = 0;
    fakeApi((operation) => {
      if (operation === "getProject" && ++projectReads > 1) {
        throw refused(503, { code: "unavailable" });
      }
      return expected(
        projectPages(operation, [board("bd_a", "Harbour")]),
        operation,
      );
    });
    renderApp(`/projects/${testProject.id}/boards/bd_b`, {
      session: testSession,
    });
    expect(
      await screen.findByRole("heading", { name: "Something went wrong" }),
    ).toBeDefined();
    expect(screen.queryByRole("heading", { name: "Page not found" })).toBe(
      null,
    );
  });

  it("shows the other editor's version of a field that both changed once the user takes theirs", async () => {
    const user = userEvent.setup();
    const harbour = board("bd_a", "Harbour");
    fakeApi((operation) => {
      if (operation === "applyCommands") {
        throw conflict("board.describe", {
          ...harbour,
          description: "Where the tale ends",
          rev: 4,
        });
      }
      return expected(projectPages(operation, [harbour]), operation);
    });
    renderApp(`/projects/${testProject.id}/boards/bd_a`, {
      session: testSession,
    });
    await user.type(
      await screen.findByLabelText("Description"),
      "Where the story starts",
    );
    await user.click(screen.getByRole("button", { name: "Save the board" }));
    await user.click(
      await screen.findByRole("button", { name: "Use their version" }),
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Description")).toHaveProperty(
        "value",
        "Where the tale ends",
      ),
    );
  });
});

describe("a project's settings", () => {
  // Verifies: FR-RT-02/AC3
  it("keep the focus in a field that the user is in, before typing in it, while another session's change of the project applies, and show the change once the focus leaves the form", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    fakeApi((operation) => expected(projectPages(operation, []), operation));
    renderApp(`/projects/${testProject.id}/settings`, {
      session: testSession,
      realtime,
    });
    const description = await screen.findByLabelText("Description");
    await user.click(description);
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "project.rename", payload: { name: "Harbour at night" } }],
        [{ id: testProject.id, rev: 2 }],
      ),
    );
    // The page shows the project as the change left it.
    await waitFor(() => expect(document.title).toContain("Harbour at night"));
    expect(document.activeElement).toBe(description);
    expect(description.isConnected).toBe(true);
    await user.click(document.body);
    await waitFor(() =>
      expect(screen.getByLabelText("Name")).toHaveProperty(
        "value",
        "Harbour at night",
      ),
    );
  });
});
