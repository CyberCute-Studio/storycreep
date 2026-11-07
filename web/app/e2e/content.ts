// What the end-to-end tests of the workspaces and the projects start from,
// made through the API, as the account of a request context signed in with
// apiSignIn (fixtures.ts): an account that a page is signed in as, a
// workspace, a project, the commands of a batch, and an invitation that
// another account accepts; and dragging a control to another, as the
// project's device does, with a mouse or a finger.

import { randomUUID } from "node:crypto";
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import {
  type ApiSession,
  atStart,
  expect,
  registerAccount,
  signIn,
  test,
} from "./fixtures.ts";

/**
 * Returns the session that request has, as a page's request, which shares
 * the page's cookie, with its CSRF token: signing it in again would give
 * the page's context a new session, whose CSRF token the page does not have.
 */
export async function sessionOf(
  request: APIRequestContext,
): Promise<ApiSession> {
  const res = await request.get("/api/v1/auth/session");
  expect(res.status(), await res.text()).toBe(200);
  const body = (await res.json()) as { csrf_token: string; user_id: string };
  return { csrf: body.csrf_token, userID: body.user_id };
}

/** Registers an account, signs the page in as it, and returns the page's session. */
export async function signedIn(page: Page, name: string): Promise<ApiSession> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return sessionOf(page.request);
}

/** A command of a batch, without its ID, which apply gives it. */
export interface Command {
  type: string;
  payload: Record<string, unknown>;
}

/** The most commands that the API takes in one batch. */
const batchSize = 1000;

/** Applies commands to projectId through the API, as request's account, in batches as large as the API takes. */
export async function apply(
  request: APIRequestContext,
  session: ApiSession,
  projectId: string,
  commands: Command[],
): Promise<void> {
  for (let i = 0; i < commands.length; i += batchSize) {
    const res = await request.post(`/api/v1/projects/${projectId}/commands`, {
      headers: { "X-CSRF-Token": session.csrf },
      data: {
        commands: commands
          .slice(i, i + batchSize)
          .map((c) => ({ command_id: randomUUID(), ...c })),
      },
      timeout: 120_000,
    });
    expect(res.status(), await res.text()).toBe(200);
  }
}

/** A workspace as the API lists it, with the role of the account's membership. */
export interface WorkspaceSummary {
  id: string;
  name: string;
  role: string;
}

/** Returns the first of the workspaces of the account that request is signed in as. */
export async function firstWorkspace(
  request: APIRequestContext,
): Promise<WorkspaceSummary> {
  const res = await request.get("/api/v1/workspaces");
  expect(res.status(), await res.text()).toBe(200);
  const { items } = (await res.json()) as { items: WorkspaceSummary[] };
  expect(items.length).toBeGreaterThan(0);
  return items[0];
}

/** Creates a project named name in the workspace workspaceId, and returns its ID. */
export async function createProject(
  request: APIRequestContext,
  session: ApiSession,
  workspaceId: string,
  name: string,
): Promise<string> {
  const res = await request.post(`/api/v1/workspaces/${workspaceId}/projects`, {
    headers: { "X-CSRF-Token": session.csrf },
    data: { name },
  });
  expect(res.status(), await res.text()).toBe(201);
  return ((await res.json()) as { id: string }).id;
}

/** Makes an invitation of whoever holds its link to the workspace with role, and returns the link. */
export async function inviteByLink(
  request: APIRequestContext,
  session: ApiSession,
  workspaceId: string,
  role: string,
): Promise<string> {
  const res = await request.post(`/api/v1/workspaces/${workspaceId}/invites`, {
    headers: { "X-CSRF-Token": session.csrf },
    data: { role },
  });
  expect(res.status(), await res.text()).toBe(201);
  const { url } = (await res.json()) as { url?: string };
  expect(url).toBeDefined();
  return url ?? "";
}

/** Accepts the invitation of link as the account that request is signed in as. */
export async function acceptInvite(
  request: APIRequestContext,
  session: ApiSession,
  link: string,
): Promise<void> {
  const token = new URLSearchParams(new URL(link).hash.slice(1)).get("token");
  const res = await request.post("/api/v1/invites/accept", {
    headers: { "X-CSRF-Token": session.csrf },
    data: { token },
  });
  expect(res.status(), await res.text()).toBe(200);
}

/** Returns the point of box at the fraction across and down of it. */
function pointOf(
  box: { x: number; y: number; width: number; height: number },
  down: number,
) {
  return { x: box.x + box.width / 2, y: box.y + box.height * down };
}

/**
 * Drags from, as a handle, to the point of to that is down of its height
 * from its top, as the project's device does: with the mouse, or, on the
 * tablet, a finger, whose touches Chromium's DevTools protocol makes, since
 * Playwright has none that move. It moves in steps, as a hand does, past
 * the few pixels that a drag starts after.
 */
export async function drag(
  page: Page,
  from: Locator,
  to: Locator,
  down = 0.5,
): Promise<void> {
  const fromBox = await from.boundingBox();
  const toBox = await to.boundingBox();
  if (fromBox === null || toBox === null) {
    throw new Error("drag: a control that is not shown");
  }
  const start = pointOf(fromBox, 0.5);
  const end = pointOf(toBox, down);
  const steps = 10;
  const at = (i: number) => ({
    x: start.x + ((end.x - start.x) * i) / steps,
    y: start.y + ((end.y - start.y) * i) / steps,
  });
  if (test.info().project.use.hasTouch) {
    const cdp = await page.context().newCDPSession(page);
    try {
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchStart",
        touchPoints: [start],
      });
      for (let i = 1; i <= steps; i++) {
        await cdp.send("Input.dispatchTouchEvent", {
          type: "touchMove",
          touchPoints: [at(i)],
        });
      }
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchEnd",
        touchPoints: [],
      });
    } finally {
      await cdp.detach();
    }
    return;
  }
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  for (let i = 1; i <= steps; i++) {
    const point = at(i);
    await page.mouse.move(point.x, point.y);
  }
  await page.mouse.up();
}
