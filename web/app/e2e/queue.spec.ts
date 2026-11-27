import type { BrowserContext, Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  dragOn,
  drawn,
  makeProject,
  pointOf,
  settled,
} from "./boards.ts";
import { apply, sessionOf, signedIn } from "./content.ts";
import {
  atStart,
  en,
  expect,
  message,
  press,
  registerAccount,
  signIn,
  test,
} from "./fixtures.ts";

// The device's queue of the changes that the server has yet to answer (PRD
// FR-OFF-03, FR-OFF-06, plan note N98), in the browser, against the server:
// a board renamed while the browser is offline is kept on the device, which
// the sync indicator says; undo takes the latest change out of the queue,
// and redo puts it back, offline; a page of the project opened anew offline,
// after its tab closed, which the app's service worker answers, shows the
// change as it was made, from the device's queue; and once the browser is
// online again, the change reaches the server, and the indicator says that
// every change is saved. And what a user makes offline on a pinned project
// (FR-OFF-03's "all editing operations"): boards, a variable, and on the
// boards elements, titled, moved, and deleted, a text, and a jumper,
// connected by a choice whose condition names the variable, to the other
// board made offline, which play mode walks offline, which a reload offline
// shows, the script view with the condition compiled on the device, and
// which the server has once online. And a change that the user signed out
// anyway with, whose tab closed as the server signed it out, which the
// next sign-in does not send.

test.use({ serviceWorkers: "allow" });

/** The sync indicator, once it says text. */
function indicator(page: Page, text: string) {
  return page.getByRole("button", { name: text, exact: true });
}

/** Renames the board named from to to, through its row of the tree of the project's boards. */
async function renameBoard(page: Page, from: string, to: string) {
  await press(
    page.getByRole("button", {
      name: message("boards.actions", { name: from }),
    }),
  );
  await press(
    page.getByRole("menu").getByRole("menuitem", { name: en["boards.rename"] }),
  );
  const rename = page.getByRole("dialog");
  await rename.getByLabel(en["boards.new_name"], { exact: true }).fill(to);
  await press(rename.getByRole("button", { name: en["boards.rename_submit"] }));
  await expect(rename).toBeHidden();
}

/** Returns the names of the boards of projectId, as the server lists them. */
async function serverBoards(page: Page, projectId: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/projects/${projectId}/boards`);
  expect(res.status(), await res.text()).toBe(200);
  const body = (await res.json()) as { items: { name: string }[] };
  return body.items.map((b) => b.name).sort();
}

/** Opens the tree of projectId's boards in a new page of context, as a tab opened anew. */
async function openAnew(context: BrowserContext, projectId: string) {
  const page = await context.newPage();
  const loaded = await page.goto(`/app/projects/${projectId}`);
  return { page, loaded };
}

/** Creates a board named name through the tree of the project's boards. */
async function createBoard(page: Page, name: string) {
  await press(page.getByRole("button", { name: en["boards.new_board"] }));
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel(en["boards.board_name"], { exact: true }).fill(name);
  await press(dialog.getByRole("button", { name: en["boards.create_board"] }));
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("link", { name, exact: true })).toBeVisible();
}

/** Opens the inspector beside the canvas, where it is not open. */
async function openInspector(page: Page) {
  const inspect = page.getByRole("button", {
    name: en["canvas.inspector"],
    exact: true,
  });
  if ((await inspect.getAttribute("aria-pressed")) !== "true") {
    await press(inspect);
  }
}

/** Titles the node that the inspector shows title, and waits until it shows it so. */
async function titleNode(page: Page, title: string) {
  await openInspector(page);
  await page
    .getByLabel(en["inspector.node_title"], { exact: true })
    .fill(title);
  await press(page.getByRole("button", { name: en["inspector.save"] }));
  await expect(page.getByRole("region", { name: title })).toBeVisible();
}

/** Adds an element by its key in the middle of canvas, which selects it, titled title, and returns where its middle is on the page. */
async function addElement(page: Page, canvas: Locator, title: string) {
  const before = Number(await canvas.getAttribute("data-nodes"));
  await canvas.focus();
  await page.keyboard.press("e");
  await expect(canvas).toHaveAttribute("data-nodes", String(before + 1));
  await expect(canvas).toHaveAttribute("data-selected", "1");
  await titleNode(page, title);
  await settled(canvas);
  const box = await canvas.boundingBox();
  if (box === null) {
    throw new Error("the canvas is not shown");
  }
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

/** Returns what the API lists at path of projectId. */
async function listed<T>(page: Page, projectId: string, path: string) {
  const res = await page.request.get(`/api/v1/projects/${projectId}/${path}`);
  expect(res.status(), await res.text()).toBe(200);
  return ((await res.json()) as { items: T[] }).items;
}

/** The panel of play mode. */
function playOf(page: Page): Locator {
  return page.getByRole(
    test.info().project.use.hasTouch === true ? "dialog" : "region",
    { name: en["play.title"] },
  );
}

/** Plays from the node whose middle is at at of the page, through its context menu, across the jumper to the board of dock. */
async function playAcross(
  page: Page,
  at: { x: number; y: number },
  dock: string,
) {
  await page.mouse.click(at.x, at.y, { button: "right" });
  await press(
    page.getByRole("menuitem", { name: en["canvas.menu.play_here"] }),
  );
  await expect(playOf(page).getByText("The planks creak.")).toBeVisible();
  await press(playOf(page).getByRole("button", { name: en["play.continue"] }));
  await expect(page).toHaveURL(new RegExp(`/boards/${dock}$`));
  await expect(
    playOf(page)
      .getByRole("region", { name: en["play.current"] })
      .getByText(en["play.ended.dead_end"]),
  ).toBeVisible();
  await press(playOf(page).getByRole("button", { name: en["play.stop"] }));
}

test.describe("the changes made offline", () => {
  // Verifies: FR-OFF-03/AC1, FR-OFF-03/AC4
  test("are kept on the device, undone and redone there, shown by a page opened anew after its tab closed, and sent once online", async ({
    page,
    context,
  }) => {
    const session = await signedIn(page, "Queenie Queue");
    const projectId = await makeProject(page.request, session, "Harbour");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: newId("board"), name: "Quay" } },
    ]);
    await page.goto(`/app/projects/${projectId}`);
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready;
    });
    // The project opened last is on the device, as the switch says.
    await expect(page.getByText(/^On this device/)).toBeVisible({
      timeout: 30_000,
    });
    await expect(indicator(page, en["sync.state.synced"])).toBeVisible();

    await context.setOffline(true);
    await expect(indicator(page, "Offline")).toBeVisible();
    await renameBoard(page, "Quay", "Pier");
    await expect(page.getByRole("link", { name: "Pier" })).toBeVisible();
    await renameBoard(page, "Pier", "Dock");
    await expect(
      indicator(page, "Offline, 2 changes on this device"),
    ).toBeVisible();
    // Undo takes the latest change out of the queue, and redo puts it back.
    await press(page.getByRole("button", { name: en["history.undo"] }));
    await expect(page.getByRole("link", { name: "Pier" })).toBeVisible();
    await expect(
      indicator(page, "Offline, 1 change on this device"),
    ).toBeVisible();
    await press(page.getByRole("button", { name: en["history.redo"] }));
    await expect(page.getByRole("link", { name: "Dock" })).toBeVisible();
    await expect(
      indicator(page, "Offline, 2 changes on this device"),
    ).toBeVisible();

    // The tab closes, and the project is opened anew, offline.
    await page.close();
    const again = await openAnew(context, projectId);
    expect(again.loaded?.fromServiceWorker()).toBe(true);
    await expect(again.page.getByRole("link", { name: "Dock" })).toBeVisible({
      timeout: 15_000,
    });
    await expect(
      indicator(again.page, "Offline, 2 changes on this device"),
    ).toBeVisible();
    expect(await serverBoards(again.page, projectId)).toEqual(["Quay"]);

    await context.setOffline(false);
    await expect(indicator(again.page, en["sync.state.synced"])).toBeVisible({
      timeout: 30_000,
    });
    expect(await serverBoards(again.page, projectId)).toEqual(["Dock"]);
  });

  // Verifies: FR-OFF-03/AC1, FR-OFF-03/AC2
  test("make boards, their nodes, a connection, and a jumper between them, which play mode walks offline, a reload offline keeps, and the server has once online", async ({
    page,
    context,
  }) => {
    test.skip(
      test.info().project.use.hasTouch === true,
      "a mouse's right click and the canvas's keys",
    );
    const session = await signedIn(page, "Otto Offline");
    const projectId = await makeProject(page.request, session, "Harbour");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: newId("board"), name: "Quay" } },
    ]);
    await page.goto(`/app/projects/${projectId}`);
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready;
    });
    await expect(page.getByText(/^On this device/)).toBeVisible({
      timeout: 30_000,
    });
    await expect(indicator(page, en["sync.state.synced"])).toBeVisible();

    await context.setOffline(true);
    await expect(indicator(page, "Offline")).toBeVisible();
    await createBoard(page, "Dock");
    await createBoard(page, "Pier");
    // A variable, which a condition names below.
    await press(page.getByRole("link", { name: en["project.nav.variables"] }));
    await press(page.getByRole("button", { name: en["variables.new"] }));
    const create = page.getByRole("dialog", {
      name: en["variable.create_title"],
    });
    await create.getByLabel(en["variable.key"], { exact: true }).fill("planks");
    await create
      .getByLabel(en["variable.type"], { exact: true })
      .selectOption("int");
    await create.getByLabel(en["variable.default"], { exact: true }).fill("3");
    await press(create.getByRole("button", { name: en["variable.create"] }));
    await expect(create).toBeHidden();
    await press(page.getByRole("link", { name: en["project.nav.boards"] }));

    // A board made offline opens, with no read of the server, and takes a
    // node.
    await press(page.getByRole("link", { name: "Dock", exact: true }));
    const dockCanvas = canvasOf(page, "Dock");
    await drawn(dockCanvas);
    const dock = new URL(page.url()).pathname.split("/").at(-1) ?? "";
    await addElement(page, dockCanvas, "Landing");
    // Moved, by the inspector.
    await page.getByLabel(en["inspector.x"], { exact: true }).fill("400");
    await press(page.getByRole("button", { name: en["inspector.save"] }));
    // And another, deleted.
    await addElement(page, dockCanvas, "Spare");
    await dockCanvas.focus();
    await page.keyboard.press("Delete");
    await expect(dockCanvas).toHaveAttribute("data-nodes", "1");

    await press(page.getByRole("link", { name: en["project.nav.boards"] }));
    await press(page.getByRole("link", { name: "Pier", exact: true }));
    const canvas = canvasOf(page, "Pier");
    await drawn(canvas);
    const pier = page.url();
    const gangway = await addElement(page, canvas, "Gangway");
    // Where its middle is on the board, which a reload may show elsewhere.
    const shown = await settled(canvas);
    const canvasBox = await canvas.boundingBox();
    if (canvasBox === null) {
      throw new Error("the canvas is not shown");
    }
    const middle = {
      x: shown.x + (gangway.x - canvasBox.x) / shown.zoom,
      y: shown.y + (gangway.y - canvasBox.y) / shown.zoom,
    };
    // Its text, in the canvas's editor.
    await page.mouse.dblclick(gangway.x, gangway.y);
    const editor = page.getByRole("form", {
      name: message("canvas.text_editor", { node: "Gangway" }),
    });
    await expect(editor).toBeVisible();
    await editor
      .getByRole("textbox", {
        name: message("canvas.text_of", { node: "Gangway" }),
      })
      .focus();
    await page.keyboard.type("The planks creak.");
    await press(editor.getByRole("button", { name: en["canvas.text_save"] }));
    await expect(editor).toBeHidden();
    // A jumper, made and connected by a drag of the element's handle onto
    // the empty board, which leads to the other board made offline.
    await page.mouse.click(gangway.x, gangway.y);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    const handle = page.locator('[data-link="new"]');
    await expect(handle).toBeVisible();
    const from = await handle.boundingBox();
    const box = await canvas.boundingBox();
    if (from === null || box === null) {
      throw new Error("the handle is not shown");
    }
    await dragOn(
      page,
      { x: from.x + from.width / 2, y: from.y + from.height / 2 },
      { x: gangway.x, y: box.y + box.height * 0.85 },
    );
    const offer = page.getByRole("dialog", { name: en["canvas.connect_new"] });
    await press(offer.getByRole("button", { name: en["canvas.add_jumper"] }));
    await expect(canvas).toHaveAttribute("data-nodes", "2");
    await expect(canvas).toHaveAttribute("data-connections", "1");
    await openInspector(page);
    await page
      .getByLabel(en["inspector.target_board"], { exact: true })
      .selectOption({ label: "Dock" });
    await page
      .getByLabel(en["inspector.target_node"], { exact: true })
      .selectOption({ label: "Landing (Dialogue)" });
    await press(
      page.getByRole("button", { name: en["inspector.save_target"] }),
    );
    await expect(
      page.getByRole("link", { name: en["canvas.go_to_target"] }).first(),
    ).toBeVisible();
    // The choice to it, of a condition that names the variable.
    await page.mouse.click(gangway.x, gangway.y);
    await press(
      page.getByRole("button", {
        name: message("inspector.choice_edit", { place: "1" }),
      }),
    );
    await page
      .getByRole("textbox", { name: en["inspector.condition"] })
      .fill("planks > 2");
    await press(
      page.getByRole("button", { name: en["inspector.save_connection"] }),
    );

    // Play walks what was made offline, across the jumper.
    await playAcross(page, gangway, dock);

    // A reload offline shows it all, from the device's copy and its queue:
    // the script view, which reads the condition as it compiles on the
    // device, which the server has yet to; and the canvas, which play walks
    // again.
    expect((await page.goto(`${pier}?view=script`))?.fromServiceWorker()).toBe(
      true,
    );
    const script = page.getByRole("region", {
      name: en["script.heading"],
      exact: true,
    });
    await expect(
      script.getByText("Only if planks is more than 2", { exact: false }),
    ).toBeVisible();
    await expect(script.getByText(en["script.condition_invalid"])).toHaveCount(
      0,
    );
    const reloaded = await page.goto(pier);
    expect(reloaded?.fromServiceWorker()).toBe(true);
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-nodes", "2");
    await expect(canvas).toHaveAttribute("data-connections", "1");
    await expect(
      page.getByRole("button", {
        name: /^Offline, \d+ changes on this device$/,
      }),
    ).toBeVisible();
    await playAcross(page, await pointOf(canvas, middle), dock);
    expect(await serverBoards(page, projectId)).toEqual(["Quay"]);

    // Online, the server has it all.
    await context.setOffline(false);
    await expect(indicator(page, en["sync.state.synced"])).toBeVisible({
      timeout: 30_000,
    });
    expect(await serverBoards(page, projectId)).toEqual([
      "Dock",
      "Pier",
      "Quay",
    ]);
    const pierId = new URL(pier).pathname.split("/").at(-1) ?? "";
    const nodes = await listed<{
      title?: string;
      type: string;
      target?: { board_id: string; node_id: string };
      text?: unknown;
    }>(page, projectId, `boards/${pierId}/nodes`);
    const landing = await listed<{ id: string; title?: string }>(
      page,
      projectId,
      `boards/${dock}/nodes`,
    );
    expect(landing.map((n) => n.title)).toEqual(["Landing"]);
    expect(
      (landing[0] as unknown as { position: { x: number } }).position.x,
    ).toBe(400);
    expect(nodes).toHaveLength(2);
    const element = nodes.find((n) => n.type === "element");
    expect(element?.title).toBe("Gangway");
    expect(JSON.stringify(element?.text)).toContain("The planks creak.");
    expect(nodes.find((n) => n.type === "jumper")?.target).toEqual({
      board_id: dock,
      node_id: landing[0].id,
    });
    const connections = await listed<{
      condition?: { source: string; is_valid: boolean };
    }>(page, projectId, `boards/${pierId}/connections`);
    expect(connections.map((c) => c.condition)).toMatchObject([
      { source: "planks > 2", is_valid: true },
    ]);
  });

  test("that the user signed out anyway with stay off the server, though the tab closed as the server signed it out", async ({
    page,
    context,
  }) => {
    const account = await registerAccount(page.request, "Cal Closer");
    await signIn(page, account.email);
    await atStart(page, account.name);
    const session = await sessionOf(page.request);
    const projectId = await makeProject(page.request, session, "Harbour");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: newId("board"), name: "Quay" } },
    ]);
    await page.goto(`/app/projects/${projectId}`);
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready;
    });
    await expect(page.getByText(/^On this device/)).toBeVisible({
      timeout: 30_000,
    });
    await context.setOffline(true);
    await expect(indicator(page, "Offline")).toBeVisible();
    await renameBoard(page, "Quay", "Pier");
    await expect(
      indicator(page, "Offline, 1 change on this device"),
    ).toBeVisible();
    // Online, the server fails on every change, which the device keeps.
    const commands = `**/api/v1/projects/${projectId}/commands`;
    await context.route(commands, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "unavailable", message: "Unavailable." },
        }),
      }),
    );
    await context.setOffline(false);
    await expect(indicator(page, "Saving 1 change…")).toBeVisible({
      timeout: 30_000,
    });
    await press(page.getByRole("button", { name: en["shell.sign_out"] }));
    const asked = page.getByRole("alertdialog");
    const signedOut = page.waitForResponse((res) =>
      res.url().endsWith("/api/v1/auth/logout"),
    );
    await press(
      asked.getByRole("button", { name: en["sign_out.pending.confirm"] }),
    );
    // The tab closes as the server's answer comes.
    await signedOut;
    await page.close({ runBeforeUnload: false });
    await context.unroute(commands);

    const next = await context.newPage();
    await signIn(next, account.email);
    await atStart(next, account.name);
    await next.goto(`/app/projects/${projectId}`);
    await expect(next.getByRole("link", { name: "Quay" })).toBeVisible();
    await expect(indicator(next, en["sync.state.synced"])).toBeVisible();
    expect(await serverBoards(next, projectId)).toEqual(["Quay"]);
  });
});
