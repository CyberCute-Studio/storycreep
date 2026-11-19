import type { NotificationBody } from "@storycreep/shared/api";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "./test/app";
import { fakeApi, startPage, testProject, testWorkspace } from "./test/fake";
import { fakeRealtime } from "./test/realtime";

// The account's notification centre (PRD FR-COMMENT-02, plan item 75), as
// the router shows it, over a fake of the API's client that keeps the
// account's notifications as the server does: the header's count of the
// unread ones, which follows the real-time channel's message that they
// changed, and the page that lists them, marks one read, as its own button
// and its link do, and marks every one read up to the newest that it lists,
// each shown read as the server answers, without reading them again, which
// the server's message of the change has the page do.

afterEach(() => {
  vi.restoreAllMocks();
});

const at = "2026-11-19T10:00:00Z";

/** Returns a notification of a mention of the account, by Mira unless another is given, on the node nd_1 of the board brd_1. */
function notification(
  id: string,
  more: Partial<NotificationBody> = {},
): NotificationBody {
  return {
    id,
    kind: "mention",
    created_at: at,
    workspace_id: testWorkspace.id,
    project: { id: testProject.id, name: "Harbour" },
    comment_id: `cmt_${id}`,
    thread_id: `cmt_${id}`,
    target_type: "node",
    target_id: "nd_1",
    board_id: "brd_1",
    node_id: "nd_1",
    actor: { id: "usr_mira", display_name: "Mira" },
    preview: "Ask @Jo about the lamp.",
    ...more,
  };
}

/**
 * Answers the pages of a signed-in account with the account's
 * notifications, newest first, as the server keeps them: reading one marks
 * it read, and reading every one through one marks it and those after it in
 * the list read.
 */
function notificationServer(stored: NotificationBody[]) {
  const notifications = stored.map((n) => ({ ...n }));
  const calls = fakeApi((operation, input) => {
    const call = (input ?? {}) as {
      path?: { notification_id?: string };
      body?: { through?: string };
    };
    switch (operation) {
      case "listNotifications":
        return {
          items: notifications.map((n) => ({ ...n })),
          next_cursor: null,
        };
      case "countUnreadNotifications":
        return {
          unread: notifications.filter((n) => n.read_at === undefined).length,
        };
      case "readNotification": {
        const n = notifications.find(
          (n) => n.id === call.path?.notification_id,
        );
        if (n !== undefined) {
          n.read_at ??= "2026-11-19T11:00:00Z";
        }
        return undefined;
      }
      case "readAllNotifications": {
        const from = notifications.findIndex(
          (n) => n.id === call.body?.through,
        );
        for (const n of notifications.slice(from)) {
          n.read_at ??= "2026-11-19T11:00:00Z";
        }
        return undefined;
      }
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
  return {
    calls,
    notifications,
    /** The inputs of the calls of operation that the page made. */
    callsOf: (operation: string) =>
      calls.mock.calls
        .filter(([op]) => op === operation)
        .map(([, input]) => input),
  };
}

describe("the header's notifications", () => {
  it("counts the unread ones, as the real-time channel tells that they changed, in words for assistive technology", async () => {
    const realtime = fakeRealtime();
    const server = notificationServer([]);
    renderApp("/projects", { session: testSession, realtime });
    const banner = await screen.findByRole("banner");
    const link = await within(banner).findByRole("link", {
      name: "Notifications",
    });
    expect(link.getAttribute("href")).toBe("/app/notifications");
    // The shell listens while the page is signed in, which keeps the
    // channel's socket open.
    expect(realtime.listening()).toBe(1);
    server.notifications.push(
      notification("ntf_2"),
      notification("ntf_1", { read_at: at }),
    );
    act(() => realtime.notify());
    expect(
      await within(banner).findByRole("link", {
        name: "Notifications 1 unread",
      }),
    ).toBe(link);
    server.notifications.unshift(notification("ntf_3"));
    act(() => realtime.notify());
    expect(
      await within(banner).findByRole("link", {
        name: "Notifications 2 unread",
      }),
    ).toBe(link);
  });

  it("does not listen on a signed-out page", async () => {
    const realtime = fakeRealtime();
    notificationServer([]);
    renderApp("/login", { realtime });
    expect(
      await screen.findByRole("heading", { level: 1, name: "Sign in" }),
    ).toBeDefined();
    expect(realtime.listening()).toBe(0);
    expect(screen.queryByRole("link", { name: /^Notifications/ })).toBeNull();
  });
});

describe("the page of the notifications", () => {
  it("lists them newest first, each with who mentioned the account, where, and when, the unread ones said so in words, and marks one read by its button", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const server = notificationServer([
      notification("ntf_2"),
      notification("ntf_1", {
        read_at: at,
        actor: undefined,
        target_type: "variable",
        target_id: "var_gold",
        board_id: undefined,
        node_id: undefined,
        preview: "Is gold ever spent, @Jo?",
      }),
    ]);
    renderApp("/notifications", { session: testSession, realtime });
    const list = await screen.findByRole("list", { name: "Notifications" });
    const banner = screen.getByRole("banner");
    expect(
      await within(banner).findByRole("link", {
        name: "Notifications 1 unread",
      }),
    ).toBeDefined();
    const [newest, older] = within(list).getAllByRole("listitem");
    expect(within(newest).getByText("Unread")).toBeDefined();
    expect(
      within(newest).getByText("Mira mentioned you in Harbour"),
    ).toBeDefined();
    expect(within(newest).getByText("Ask @Jo about the lamp.")).toBeDefined();
    const toNode = within(newest).getByRole("link", {
      name: "Show the thread on the node",
    });
    expect(toNode.getAttribute("href")).toBe(
      `/app/projects/${testProject.id}/boards/brd_1?node=nd_1&comments=true`,
    );
    expect(toNode.getAttribute("aria-describedby")).toBe(
      within(newest).getByText("Mira mentioned you in Harbour").parentElement
        ?.id,
    );
    expect(
      within(older).getByText("A former member mentioned you in Harbour"),
    ).toBeDefined();
    expect(within(older).queryByText("Unread")).toBeNull();
    expect(
      within(older)
        .getByRole("link", { name: "Show the thread on the variable" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/variables?variable=var_gold`);
    expect(
      within(older).queryByRole("button", { name: "Mark as read" }),
    ).toBeNull();

    const reads = server.callsOf("listNotifications").length;
    const counts = server.callsOf("countUnreadNotifications").length;
    await user.click(
      within(newest).getByRole("button", { name: "Mark as read" }),
    );
    expect(server.callsOf("readNotification")).toEqual([
      {
        path: { notification_id: "ntf_2" },
        headers: { "X-CSRF-Token": testSession.csrf_token },
      },
    ]);
    await waitFor(() => expect(within(list).queryByText("Unread")).toBeNull());
    expect(
      within(list).queryByRole("button", { name: "Mark as read" }),
    ).toBeNull();
    // Neither is read again, as the server's message of the change has them
    // read, and the header's count is the server's, not one that the page
    // takes the marked one from, which the message reads again.
    expect(server.callsOf("listNotifications")).toHaveLength(reads);
    expect(server.callsOf("countUnreadNotifications")).toHaveLength(counts);
    expect(
      within(banner).getByRole("link", { name: "Notifications 1 unread" }),
    ).toBeDefined();
    act(() => realtime.notify());
    expect(
      await within(banner).findByRole("link", { name: "Notifications" }),
    ).toBeDefined();
  });

  it("marks every one read up to the newest that it lists, so that one that came after stays unread", async () => {
    const user = userEvent.setup();
    const realtime = fakeRealtime();
    const server = notificationServer([
      notification("ntf_2"),
      notification("ntf_1", { actor: { id: "usr_bo", display_name: "Bo" } }),
    ]);
    renderApp("/notifications", { session: testSession, realtime });
    const list = await screen.findByRole("list", { name: "Notifications" });
    expect(within(list).getAllByText("Unread")).toHaveLength(2);
    // One comes after the page listed them, which it has not read yet.
    server.notifications.unshift(notification("ntf_3"));
    await user.click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(server.callsOf("readAllNotifications")).toEqual([
      {
        body: { through: "ntf_2" },
        headers: { "X-CSRF-Token": testSession.csrf_token },
      },
    ]);
    expect(
      await screen.findByText("Every notification listed is marked as read."),
    ).toBeDefined();
    // Those listed show read, without being read again; the header's count
    // is the server's, which the message reads again.
    expect(within(list).queryByText("Unread")).toBeNull();
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    const banner = screen.getByRole("banner");
    expect(
      within(banner).getByRole("link", { name: "Notifications 2 unread" }),
    ).toBeDefined();
    // Read again, as the server's message of the change has them, the list
    // shows the one that came after, unread alone, and the count it alone.
    act(() => realtime.notify());
    await waitFor(() =>
      expect(within(list).getAllByRole("listitem")).toHaveLength(3),
    );
    const [came] = within(list).getAllByRole("listitem");
    expect(within(list).getAllByText("Unread")).toEqual([
      within(came).getByText("Unread"),
    ]);
    expect(
      await within(banner).findByRole("link", {
        name: "Notifications 1 unread",
      }),
    ).toBeDefined();
  });

  it("marks one read as its link is followed", async () => {
    const user = userEvent.setup();
    const server = notificationServer([notification("ntf_2")]);
    renderApp("/notifications", { session: testSession });
    const list = await screen.findByRole("list", { name: "Notifications" });
    await user.click(
      within(list).getByRole("link", { name: "Show the thread on the node" }),
    );
    await waitFor(() =>
      expect(server.callsOf("readNotification")).toEqual([
        {
          path: { notification_id: "ntf_2" },
          headers: { "X-CSRF-Token": testSession.csrf_token },
        },
      ]),
    );
  });

  it("marks one read as its link is opened in a new tab by the middle button, and not by another", async () => {
    const server = notificationServer([notification("ntf_2")]);
    renderApp("/notifications", { session: testSession });
    const list = await screen.findByRole("list", { name: "Notifications" });
    const link = within(list).getByRole("link", {
      name: "Show the thread on the node",
    });
    fireEvent(link, new MouseEvent("auxclick", { bubbles: true, button: 2 }));
    expect(server.callsOf("readNotification")).toEqual([]);
    fireEvent(link, new MouseEvent("auxclick", { bubbles: true, button: 1 }));
    await waitFor(() =>
      expect(server.callsOf("readNotification")).toEqual([
        {
          path: { notification_id: "ntf_2" },
          headers: { "X-CSRF-Token": testSession.csrf_token },
        },
      ]),
    );
    await waitFor(() => expect(within(list).queryByText("Unread")).toBeNull());
  });

  it("says that nobody has mentioned the account yet, and offers nothing to mark", async () => {
    notificationServer([]);
    renderApp("/notifications", { session: testSession });
    expect(
      await screen.findByText(
        "No one has mentioned you yet. A comment that mentions you shows here.",
      ),
    ).toBeDefined();
    expect(
      screen.queryByRole("button", { name: "Mark all as read" }),
    ).toBeNull();
  });
});
