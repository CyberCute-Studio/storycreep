import type { BrowserContext, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { makeProject } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import { en, expect, message, press, test } from "./fixtures.ts";

// The device's queue of the changes that the server has yet to answer (PRD
// FR-OFF-03, FR-OFF-06, plan note N98), in the browser, against the server:
// a board renamed while the browser is offline is kept on the device, which
// the sync indicator says; undo takes the latest change out of the queue,
// and redo puts it back, offline; a page of the project opened anew offline,
// after its tab closed, which the app's service worker answers, shows the
// change as it was made, from the device's queue; and once the browser is
// online again, the change reaches the server, and the indicator says that
// every change is saved.

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
});
