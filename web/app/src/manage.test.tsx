import {
  type Board,
  type MemberBody,
  type MembershipBody,
  NetworkError,
} from "@storycreep/shared/api";
import { screen, waitFor, within } from "@testing-library/react";
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

  it("sends a rename refused for a revision again with the revision that the tree read since", async () => {
    const user = userEvent.setup();
    let rev = 3;
    const revisions: unknown[] = [];
    fakeApi((operation, input) => {
      if (operation === "applyCommands") {
        const { body } = input as {
          body: { commands: { expected_rev: unknown }[] };
        };
        revisions.push(body.commands[0].expected_rev);
        if (revisions.length === 1) {
          // The board changed elsewhere since the tree read it.
          rev = 4;
          throw refused(409, { code: "revision_conflict" });
        }
        return {
          status: "applied",
          seq: 3,
          revisions: [],
          not_reconnected: [],
          undo: [],
        };
      }
      return expected(
        projectPages(operation, [{ ...board("bd_a", "Harbour"), rev }]),
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
    expect(revisions).toEqual([{ bd_a: 3 }, { bd_a: 4 }]);
  });
});
