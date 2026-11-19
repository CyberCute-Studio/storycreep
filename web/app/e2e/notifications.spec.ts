import type {
  APIRequestContext,
  BrowserContext,
  Locator,
  Page,
} from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, test } from "./boards.ts";
import {
  acceptInvite,
  apply,
  createProject,
  firstWorkspace,
  inviteByLink,
  sessionOf,
} from "./content.ts";
import { peerURL } from "./environment.ts";
import {
  type Account,
  type ApiSession,
  apiSignIn,
  atStart,
  en,
  expect,
  message,
  press,
  registerAccount,
  signIn,
  smallTargets,
  violations,
  watchPolicy,
} from "./fixtures.ts";

// Mentions of a project's members and the account's notification centre
// (PRD FR-COMMENT-02, plan item 75), in the browser, against the server, on
// the desktop and the tablet: a mention of someone who has lost the
// project's access since the comment's form read the members is refused,
// which the form says, naming them (AC1); and a comment that mentions a
// member shows in the notifications of each of their open sessions, the
// page of the notifications in one, on the server, and the header's count
// in the other, on the peer, the instance's second process, within 2
// seconds of the comment's post (AC2), whose read state, as one marks it
// read, the other follows.

/** Registers an account, signs page in as it, and returns it with its session. */
async function signedIn(
  page: Page,
  name: string,
): Promise<{ account: Account; session: ApiSession }> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return { account, session: await sessionOf(page.request) };
}

/** Makes a project named name in the first workspace of the account of page, with a board named Harbour, and returns their IDs. */
async function harbourOf(
  page: Page,
  session: ApiSession,
  name: string,
): Promise<{ workspaceId: string; projectId: string; boardId: string }> {
  const workspace = await firstWorkspace(page.request);
  const projectId = await createProject(
    page.request,
    session,
    workspace.id,
    name,
  );
  const boardId = newId("board");
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: boardId, name: "Harbour" } },
  ]);
  return { workspaceId: workspace.id, projectId, boardId };
}

/** Has the account of request, signed in with session, join the workspace of the invitation of link. */
async function joins(
  request: APIRequestContext,
  session: ApiSession,
  link: string,
): Promise<void> {
  await acceptInvite(request, session, link);
}

/** The board's own comments, under its canvas, as a link to them opens its page. */
async function boardComments(
  page: Page,
  projectId: string,
  boardId: string,
): Promise<Locator> {
  await page.goto(`/app/projects/${projectId}/boards/${boardId}?comments=true`);
  await drawn(canvasOf(page, "Harbour"));
  const comments = page
    .getByRole("region", { name: en["comments.title"] })
    .last();
  await expect(comments.getByText(en["comments.none"])).toBeVisible();
  return comments;
}

/** Types text into the rich text of textbox, which takes the focus as a tap or a click would give it. */
async function typeInto(textbox: Locator, text: string): Promise<void> {
  await press(textbox);
  await expect(textbox).toBeFocused();
  await textbox.page().keyboard.type(text);
}

/** Mentions the member named name in textbox, as what @ and the start of their name offer, and returns once the mention shows. */
async function mention(
  textbox: Locator,
  start: string,
  name: string,
): Promise<void> {
  await textbox.page().keyboard.type(`@${start}`);
  const members = textbox
    .page()
    .getByRole("listbox", { name: en["richtext.suggest.members"] });
  await expect(members.getByRole("option")).toHaveText([`@${name}`]);
  await press(members.getByRole("option", { name: `@${name}` }));
  await expect(members).toBeHidden();
  await expect(textbox).toBeFocused();
  await expect(textbox).toContainText(`@${name}`);
}

/** Reports whether response answers a batch of commands of projectId. */
function answersBatch(projectId: string) {
  return (response: { request(): { method(): string }; url(): string }) =>
    response.request().method() === "POST" &&
    new URL(response.url()).pathname ===
      `/api/v1/projects/${projectId}/commands`;
}

test.describe("mentions and the notification centre", () => {
  // Verifies: FR-COMMENT-02/AC1
  test("refuses a mention of a member who has lost the project's access since the form read the members, and says why, naming them", async ({
    page,
    playwright,
    baseURL,
    extraHTTPHeaders,
  }) => {
    const { session } = await signedIn(page, "Ada Owner");
    const { workspaceId, projectId, boardId } = await harbourOf(
      page,
      session,
      "Lighthouse",
    );
    // Tomas joins the workspace, and so reads the project.
    const tomas = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders,
    });
    try {
      const account = await registerAccount(tomas, "Tomas Keeper");
      const tomasSession = await apiSignIn(tomas, account.email);
      await joins(
        tomas,
        tomasSession,
        await inviteByLink(page.request, session, workspaceId, "editor"),
      );

      // The form reads the members as it shows, Tomas among them.
      const membersRead = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname ===
            `/api/v1/projects/${projectId}/members` && r.ok(),
      );
      const comments = await boardComments(page, projectId, boardId);
      const listed = (await (await membersRead).json()) as {
        items: { id: string }[];
      };
      expect(listed.items.map((m) => m.id)).toContain(tomasSession.userID);

      // Tomas leaves the workspace after that, through the API.
      const members = await page.request.get(
        `/api/v1/workspaces/${workspaceId}/members?limit=200`,
      );
      expect(members.status(), await members.text()).toBe(200);
      const membership = (
        (await members.json()) as { items: { id: string; user_id: string }[] }
      ).items.find((m) => m.user_id === tomasSession.userID);
      expect(membership).toBeDefined();
      const removed = await page.request.delete(
        `/api/v1/workspaces/${workspaceId}/members/${membership?.id}`,
        { headers: { "X-CSRF-Token": session.csrf } },
      );
      expect(removed.status(), await removed.text()).toBe(204);

      // The form still offers him, as it read the members before.
      const textbox = comments.getByRole("textbox", {
        name: en["comments.new"],
      });
      await typeInto(textbox, "Ask ");
      await mention(textbox, "Tom", "Tomas Keeper");
      await page.keyboard.type(" to light the lamp.");
      await expect(textbox).toHaveText("Ask @Tomas Keeper to light the lamp.");
      const answered = page.waitForResponse(answersBatch(projectId));
      await press(comments.getByRole("button", { name: en["comments.post"] }));
      const response = await answered;
      expect(response.status()).toBe(422);
      expect(
        ((await response.json()) as { error: { code: string } }).error.code,
      ).toBe("mention_no_access");
      // The form says why under its field, naming him as his mention does,
      // and keeps what was typed.
      await expect(
        comments.getByText(
          message("comments.error.mention_no_access", {
            name: "Tomas Keeper",
          }),
        ),
      ).toBeVisible();
      await expect(textbox).toHaveAttribute("aria-invalid", "true");
      await expect(textbox).toHaveText("Ask @Tomas Keeper to light the lamp.");
      await expect(comments.getByText(en["comments.none"])).toBeVisible();
      expect(await violations(page)).toEqual([]);
    } finally {
      await tomas.dispose();
    }
  });

  // Verifies: FR-COMMENT-02/AC2
  test("shows a comment that mentions a member in the notifications of both of their open sessions within 2 s of its post, whose read state each follows", async ({
    page,
    browser,
    baseURL,
    extraHTTPHeaders,
    refused,
  }) => {
    const touch = test.info().project.use.hasTouch === true;
    const contexts: BrowserContext[] = [];
    /** Opens a page of a new context, of the project's device, at url, whose violations of the Content-Security-Policy refused records. */
    const pageAt = async (url: string | undefined): Promise<Page> => {
      const { viewport, userAgent, deviceScaleFactor, isMobile, hasTouch } =
        test.info().project.use;
      const context = await browser.newContext({
        viewport,
        userAgent,
        deviceScaleFactor,
        isMobile,
        hasTouch,
        baseURL: url,
        extraHTTPHeaders,
      });
      contexts.push(context);
      const opened = await context.newPage();
      await watchPolicy(opened, refused);
      return opened;
    };
    try {
      // Ada, whom the comment mentions, owns the workspace.
      const ada = await signedIn(page, "Ada Mentioned");
      const { workspaceId, projectId, boardId } = await harbourOf(
        page,
        ada.session,
        "Lighthouse",
      );
      // Bram, a commenter of the workspace, posts the comment.
      const bram = await pageAt(baseURL);
      const bramAccount = await registerAccount(bram.request, "Bram Commenter");
      await signIn(bram, bramAccount.email);
      await atStart(bram, bramAccount.name);
      await joins(
        bram.request,
        await sessionOf(bram.request),
        await inviteByLink(page.request, ada.session, workspaceId, "commenter"),
      );

      // Ada's first session shows the page of her notifications, on the
      // server, and her second the project list, on the peer, each once
      // its socket of the real-time channel is open.
      const firstSocket = page.waitForEvent("websocket", (s) =>
        s.url().endsWith("/api/v1/ws"),
      );
      await page.goto("/app/notifications");
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        en["notifications.title"],
      );
      await expect(page.getByText(en["notifications.none"])).toBeVisible();
      await firstSocket;
      const second = await pageAt(peerURL);
      const secondSocket = second.waitForEvent("websocket", (s) =>
        s.url().endsWith("/api/v1/ws"),
      );
      await signIn(second, ada.account.email);
      await atStart(second, ada.account.name);
      await secondSocket;
      const badge = (p: Page, name: string) =>
        p.getByRole("banner").getByRole("link", { name, exact: true });
      await expect(badge(second, en["shell.notifications"])).toBeVisible();

      // Bram mentions Ada in a comment of the board's own.
      const comments = await boardComments(bram, projectId, boardId);
      const textbox = comments.getByRole("textbox", {
        name: en["comments.new"],
      });
      await typeInto(textbox, "Is the lamp lit, ");
      await mention(textbox, "Ada", "Ada Mentioned");
      await bram.keyboard.type("?");
      const answered = bram.waitForResponse(answersBatch(projectId));
      await press(comments.getByRole("button", { name: en["comments.post"] }));
      const response = await answered;
      expect(response.ok(), await response.text()).toBe(true);

      // Both of Ada's sessions show it within 2 seconds of the answer,
      // which comes once the batch has committed.
      const item = page.getByRole("listitem").filter({
        hasText: "Is the lamp lit, @Ada Mentioned?",
      });
      // The header's link says the count in words, which the catalog's
      // plural of one writes "1 unread".
      const oneUnread = `${en["shell.notifications"]} 1 unread`;
      await Promise.all([
        expect(item).toBeVisible({ timeout: 2_000 }),
        expect(badge(second, oneUnread)).toBeVisible({ timeout: 2_000 }),
      ]);
      await expect(
        item.getByText(
          message("notifications.mentioned_by", {
            name: "Bram Commenter",
            project: "Lighthouse",
          }),
        ),
      ).toBeVisible();
      await expect(item.getByText(en["notifications.unread"])).toBeVisible();
      expect(await violations(page)).toEqual([]);
      if (touch) {
        expect(await smallTargets(page)).toEqual([]);
      }

      // Marked read in one session, it is read in the other too.
      await press(
        item.getByRole("button", { name: en["notifications.mark_read"] }),
      );
      await expect(item.getByText(en["notifications.unread"])).toBeHidden();
      await expect(badge(second, en["shell.notifications"])).toBeVisible();
      expect(await violations(page)).toEqual([]);

      // Its link opens the board's page at its own threads, which show the
      // comment.
      await press(
        item.getByRole("link", { name: en["notifications.open_board"] }),
      );
      await drawn(canvasOf(page, "Harbour"));
      const heading = page.getByRole("heading", {
        name: en["comments.title"],
        level: 2,
      });
      await expect(heading).toBeFocused();
      await expect(
        page.getByText("Is the lamp lit, @Ada Mentioned?"),
      ).toBeVisible();
    } finally {
      for (const context of contexts) {
        await context.close();
      }
    }
  });
});
