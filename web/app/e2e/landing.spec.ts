import type { BrowserContext, Page, Response } from "@playwright/test";
import {
  canvasOf,
  drawn,
  makeBoard,
  makeProject,
  onPage,
  settled,
  test,
  type View,
  viewOf,
} from "./boards.ts";
import { apply, sessionOf } from "./content.ts";
import {
  atStart,
  en,
  expect,
  press,
  registerAccount,
  signIn,
  watchPolicy,
} from "./fixtures.ts";

// The last-opened state and the authenticated landing (PRD FR-PROJ-04,
// FR-PUB-05): a device opens the board that it last showed the account,
// where its canvas was left, with its selection, as the page is reloaded,
// from the app's start page, and from / and /login, which the server sends
// on with one redirect; another device, and a board that has gone, open the
// project list instead, without an error.

/** The views that the canvas drew once it had read the board's content, as its data attributes told, in order. */
declare global {
  interface Window {
    shownViews?: View[];
  }
}

/**
 * Has each page that page opens record the views that its canvas draws once
 * the board's content has been read, in window.shownViews, so that a test
 * reads the first of them.
 */
async function recordViews(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.shownViews = [];
    new MutationObserver((records) => {
      for (const r of records) {
        const el = r.target as HTMLElement;
        if (el.dataset.ready === "true") {
          window.shownViews?.push({
            x: Number(el.dataset.x),
            y: Number(el.dataset.y),
            zoom: Number(el.dataset.zoom),
          });
        }
      }
    }).observe(document, {
      subtree: true,
      attributes: true,
      attributeFilter: ["data-ready", "data-x", "data-y", "data-zoom"],
    });
  });
}

/** Expects view to show what want shows, within a pixel of the screen. */
function expectSame(view: View, want: View, what: string): void {
  expect(view.zoom, what).toBeCloseTo(want.zoom, 4);
  expect(Math.abs(view.x - want.x) * want.zoom, what).toBeLessThan(1);
  expect(Math.abs(view.y - want.y) * want.zoom, what).toBeLessThan(1);
}

/** Expects that the page shows the board's canvas at want, from its first frame of the board's content, with one node selected. */
async function expectRestored(
  page: Page,
  board: string,
  want: View,
  what: string,
): Promise<void> {
  const canvas = canvasOf(page, board);
  await drawn(canvas);
  const first = await page.evaluate(() => window.shownViews?.[0]);
  if (first === undefined) {
    throw new Error(`${what}: the canvas drew no view of the board's content`);
  }
  expectSame(first, want, `${what}, the first frame`);
  expectSame(await viewOf(canvas), want, what);
  await expect(canvas, what).toHaveAttribute("data-selected", "1");
}

/** Expects that response, of a page, came by one redirect, a 302 of the server's, from path. */
async function expectOneRedirect(
  response: Response | null,
  path: string,
): Promise<void> {
  expect(response?.status()).toBe(200);
  const from = response?.request().redirectedFrom();
  expect(from?.url()).toBe(new URL(path, response?.url()).href);
  expect((await from?.response())?.status()).toBe(302);
  expect(from?.redirectedFrom()).toBeNull();
}

test.describe("the last-opened state", () => {
  // Verifies: FR-PROJ-04/AC1, FR-PUB-05/AC1, FR-PUB-05/AC4
  test("opens the board where the device left it, with its selection, as the page is reloaded, from the app's start page, and from / and /login in one redirect", async ({
    page,
  }) => {
    const account = await registerAccount(page.request, "Lou Landing");
    await signIn(page, account.email);
    await atStart(page, account.name);
    const session = await sessionOf(page.request);
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const board = await makeBoard(
      page.request,
      session,
      projectId,
      "Harbour",
      12,
    );
    const path = `/app/projects/${projectId}/boards/${board.boardId}`;
    await page.goto(path);
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    // The user zooms in, and selects the node at the canvas's centre.
    await press(page.getByRole("button", { name: en["canvas.zoom_in"] }));
    await press(page.getByRole("button", { name: en["canvas.zoom_in"] }));
    const zoomed = await settled(canvas);
    const node = await onPage(canvas, zoomed, { x: 320 * 2 + 100, y: 260 });
    if (test.info().project.use.hasTouch) {
      await page.touchscreen.tap(node.x, node.y);
    } else {
      await page.mouse.click(node.x, node.y);
    }
    await expect(canvas).toHaveAttribute("data-selected", "1");
    const left = await settled(canvas);

    await recordViews(page);
    // The page keeps what its canvas shows as it goes.
    await page.reload();
    await expectRestored(page, board.name, left, "reloaded");

    await page.goto("/app/");
    await expect(page).toHaveURL(path);
    await expectRestored(page, board.name, left, "the app's start page");

    for (const from of ["/", "/login"]) {
      await expectOneRedirect(await page.goto(from), from);
      await expect(page).toHaveURL(path);
      await expectRestored(page, board.name, left, from);
    }
  });

  // Verifies: FR-PROJ-04/AC2, FR-PUB-05/AC2
  test("is the device's own, and a board that has gone opens the project list, without an error", async ({
    page,
    browser,
    baseURL,
    extraHTTPHeaders,
    refused,
  }) => {
    const account = await registerAccount(page.request, "Dev Device");
    await signIn(page, account.email);
    await atStart(page, account.name);
    const session = await sessionOf(page.request);
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const board = await makeBoard(
      page.request,
      session,
      projectId,
      "Harbour",
      4,
    );
    await page.goto(`/app/projects/${projectId}/boards/${board.boardId}`);
    await drawn(canvasOf(page, board.name));

    // Another device of the account's has no board to go back to: its
    // sign-in, its start page, and / open the project list.
    const contexts: BrowserContext[] = [];
    try {
      const context = await browser.newContext({ baseURL, extraHTTPHeaders });
      contexts.push(context);
      const other = await context.newPage();
      await watchPolicy(other, refused);
      await signIn(other, account.email);
      await atStart(other, account.name);
      await expectOneRedirect(await other.goto("/"), "/");
      await expect(other).toHaveURL(/\/app\/workspaces\/[^/]+$/);
      await atStart(other, account.name);
    } finally {
      for (const context of contexts) {
        await context.close();
      }
    }

    // The board goes to the trash elsewhere: the device opens the project
    // list from /, which takes the board out of the cookie, and from the
    // app's start page, which forgets it.
    await apply(page.request, session, projectId, [
      { type: "board.delete", payload: { id: board.boardId } },
    ]);
    const lastBoardCookie = async () =>
      (await page.context().cookies()).find((c) => c.name === "sc_last_board");
    for (const from of ["/", "/app/", "/"]) {
      await page.goto(from);
      await expect(page).toHaveURL(/\/app\/workspaces\/[^/]+$/);
      await atStart(page, account.name);
      expect(await lastBoardCookie(), from).toBeUndefined();
    }
    expect(
      await page.evaluate(
        (key) => localStorage.getItem(key),
        `storycreep.last.${session.userID}`,
      ),
    ).toBeNull();

    // What the device keeps that is none of the app's opens the project list
    // too. The cookie is for the path /, as the app writes it, so that / and
    // the app's start page are sent it: a cookie of a page's URL is for the
    // URL's directory alone.
    await page.evaluate(
      (key) => localStorage.setItem(key, "{not json"),
      `storycreep.last.${session.userID}`,
    );
    await page.context().addCookies([
      {
        name: "sc_last_board",
        value: "garbage",
        url: new URL("/", page.url()).href,
      },
    ]);
    for (const from of ["/", "/app/"]) {
      await page.goto(from);
      await expect(page).toHaveURL(/\/app\/workspaces\/[^/]+$/);
      await atStart(page, account.name);
    }
  });

  // Verifies: FR-PUB-05/AC3
  test("leaves / and /login to a signed-out browser as they were: the sign-in screen", async ({
    page,
  }) => {
    for (const from of ["/", "/login"]) {
      await page.goto(from);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        en["login.title"],
      );
    }
    await expect(page).toHaveURL(/\/app\/login$/);
  });
});
