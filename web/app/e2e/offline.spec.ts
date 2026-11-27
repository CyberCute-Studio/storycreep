import type { BrowserContext, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, makeProject } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import { type ApiSession, en, expect, press, test } from "./fixtures.ts";

// Projects kept offline on the device (PRD FR-OFF-02, plan note N97), in the
// browser, against the server: a project made available offline, once its
// copy is written, opens offline in a page loaded anew, which the app's
// service worker answers, as the account that the device keeps, with its
// boards, its variables, and play mode from the copy, and the pages of the
// server's saying that they need a connection (AC1); a copy that the
// browser's quota refuses, which the header names, and of which nothing is
// kept (AC2); and a project removed in the storage panel, whose records go
// from the device at once (AC3).

test.use({ serviceWorkers: "allow" });

/** A rich text of one paragraph of text. */
function paragraph(text: string) {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }],
  };
}

interface Story {
  session: ApiSession;
  projectId: string;
  harbour: string;
  elara: string;
}

/** Makes a project whose board Harbour leads by a jumper to the board Elara, and a variable, gold, which the flow raises. */
async function makeStory(page: Page, name: string, nodes = 0): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Harbour");
  const harbour = newId("board");
  const elara = newId("board");
  const quay = newId("node");
  const jumper = newId("node");
  const meeting = newId("node");
  const gold = newId("variable");
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: harbour, name: "Harbour" } },
    { type: "board.create", payload: { id: elara, name: "Elara" } },
    {
      type: "variable.create",
      payload: { id: gold, key: "gold", type: "int", scope: "global" },
    },
    {
      type: "node.create",
      payload: {
        id: quay,
        board_id: harbour,
        type: "element",
        kind: "narration",
        title: "Quay",
        position: { x: 0, y: 0 },
        text: paragraph("The boats come in."),
        on_exit: "gold += 1",
      },
    },
    {
      type: "node.create",
      payload: {
        id: meeting,
        board_id: elara,
        type: "element",
        kind: "narration",
        title: "Meeting",
        position: { x: 0, y: 0 },
        is_ending: true,
        text: paragraph("Elara waits."),
      },
    },
    {
      type: "node.create",
      payload: {
        id: jumper,
        board_id: harbour,
        type: "jumper",
        title: "To Elara",
        position: { x: 400, y: 0 },
        target: { board_id: elara, node_id: meeting },
      },
    },
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: quay,
        to_node_id: jumper,
        label: paragraph("Visit Elara"),
      },
    },
    { type: "board.set_entry_node", payload: { id: harbour, node_id: quay } },
    { type: "board.set_entry_node", payload: { id: elara, node_id: meeting } },
  ]);
  // Nodes of a long text each, which a copy of the project holds.
  for (let from = 0; from < nodes; from += 100) {
    await apply(
      page.request,
      session,
      projectId,
      Array.from({ length: Math.min(100, nodes - from) }, (_, i) => ({
        type: "node.create",
        payload: {
          id: newId("node"),
          board_id: elara,
          type: "element",
          kind: "narration",
          title: `Note ${from + i}`,
          position: {
            x: 400 + ((from + i) % 20) * 300,
            y: 300 * Math.floor((from + i) / 20),
          },
          text: paragraph("The tide turns. ".repeat(60)),
        },
      })),
    );
  }
  return { session, projectId, harbour, elara };
}

/** Returns how many records the device's database holds of projectId, of every store but the sessions. */
async function recordsOf(page: Page, projectId: string): Promise<number> {
  return page.evaluate(async (projectId) => {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const open = indexedDB.open("storycreep-offline");
      open.onsuccess = () => resolve(open.result);
      open.onerror = () => reject(open.error);
    });
    try {
      let count = 0;
      for (const name of ["pins", "copies", "entities"]) {
        const store = db.transaction(name).objectStore(name);
        const keys = await new Promise<IDBValidKey[]>((resolve, reject) => {
          const all = store.getAllKeys();
          all.onsuccess = () => resolve(all.result);
          all.onerror = () => reject(all.error);
        });
        count += keys.filter(
          (key) => Array.isArray(key) && key[1] === projectId,
        ).length;
      }
      return count;
    } finally {
      db.close();
    }
  }, projectId);
}

/** The project's switch of keeping it offline. */
function offlineSwitch(page: Page) {
  return page.getByRole("checkbox", { name: en["offline.switch"] });
}

/** Waits until the project's switch says that the device keeps its copy. */
async function kept(page: Page): Promise<void> {
  await expect(page.getByText(/^On this device/)).toBeVisible({
    timeout: 30_000,
  });
}

/** Runs within with the browser offline, and online again after. */
async function offline(
  context: BrowserContext,
  within: () => Promise<void>,
): Promise<void> {
  await context.setOffline(true);
  try {
    await within();
  } finally {
    await context.setOffline(false);
  }
}

test.describe("a project kept offline", () => {
  // Verifies: FR-OFF-02/AC1
  test("opens offline in a page loaded anew, with its boards, its variables, and play mode from the device's copy", async ({
    page,
    context,
  }) => {
    const story = await makeStory(page, "Olive Offline");
    await page.goto(`/app/projects/${story.projectId}`);
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready;
    });
    await press(offlineSwitch(page));
    await expect(offlineSwitch(page)).toBeChecked();
    await kept(page);

    await offline(context, async () => {
      const loaded = await page.goto(
        `/app/projects/${story.projectId}/boards/${story.harbour}`,
      );
      expect(loaded?.fromServiceWorker()).toBe(true);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        "Harbour",
      );
      await expect(
        page.getByText(
          /^You are offline\. The project is shown as this device kept it, as of/,
        ),
      ).toBeVisible();
      await drawn(canvasOf(page, "Harbour"));

      // Play runs on the copy, across the jumper to the other board.
      await press(page.getByRole("button", { name: en["shell.commands"] }));
      const palette = page.getByRole("dialog", { name: en["palette.title"] });
      await press(
        palette.getByRole("option", { name: en["command.play.start"] }),
      );
      const play = page.getByRole(
        test.info().project.use.hasTouch === true ? "dialog" : "region",
        { name: en["play.title"] },
      );
      await expect(play.getByText("The boats come in.")).toBeVisible();
      await expect(play.getByText(en["play.saved.offline"])).toBeVisible();
      await press(play.getByRole("button", { name: "Visit Elara" }));
      await expect(page).toHaveURL(
        new RegExp(`/projects/${story.projectId}/boards/${story.elara}$`),
      );
      await expect(
        page
          .getByRole(
            test.info().project.use.hasTouch === true ? "dialog" : "region",
            { name: en["play.title"] },
          )
          .getByText("Elara waits."),
      ).toBeVisible();
      await press(
        page
          .getByRole(
            test.info().project.use.hasTouch === true ? "dialog" : "region",
            { name: en["play.title"] },
          )
          .getByRole("button", { name: en["play.stop"] }),
      );

      // The variables are the copy's, and a page of the server's needs a
      // connection.
      await press(
        page.getByRole("link", { name: en["project.nav.variables"] }),
      );
      await expect(
        page.getByText("gold", { exact: true }).first(),
      ).toBeVisible();
      await press(
        page.getByRole("link", { name: en["project.nav.statistics"] }),
      );
      await expect(
        page.getByRole("heading", {
          level: 1,
          name: en["offline.needs_connection.title"],
        }),
      ).toBeVisible();
    });

    // Online again, the page of the server's reads it.
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: en["offline.needs_connection.title"],
      }),
    ).toBeHidden({ timeout: 15_000 });
  });

  // Verifies: FR-OFF-02/AC3
  test("goes from the device at once once it is removed in the storage panel", async ({
    page,
  }) => {
    const story = await makeStory(page, "Remy Remove");
    await page.goto(`/app/projects/${story.projectId}`);
    // The project that the account opened last is kept, as the switch says.
    await kept(page);
    expect(await recordsOf(page, story.projectId)).toBeGreaterThan(2);
    await page.goto("/app/account/offline");
    const item = page.getByRole("listitem").filter({ hasText: "Harbour" });
    await expect(
      item.getByText(en["offline.panel.last_opened"], { exact: false }),
    ).toBeVisible();
    await press(
      item.getByRole("button", { name: "Remove Harbour from this device" }),
    );
    await expect(page.getByText(en["offline.panel.none"])).toBeVisible();
    expect(await recordsOf(page, story.projectId)).toBe(0);
  });
});

test.describe("a copy that the browser's quota refuses", () => {
  // Verifies: FR-OFF-02/AC2
  test("is named in the header, and nothing of it is kept", async ({
    page,
    context,
    browserName,
  }) => {
    test.skip(browserName !== "chromium", "The quota is Chromium's to set.");
    // A project of 200 long texts takes a while to make, and to refuse.
    test.setTimeout(120_000);
    const story = await makeStory(page, "Quinn Quota", 200);
    // The device's database opens, as any signed-in page opens it, before
    // the quota is set just above what the site uses already, which the
    // project's copy, of 200 long texts, is far beyond.
    await page.goto("/app/account/offline");
    await expect(page.getByText(en["offline.panel.none"])).toBeVisible();
    const usage = await page.evaluate(
      async () => (await navigator.storage.estimate()).usage ?? 0,
    );
    const cdp = await context.newCDPSession(page);
    const origin = new URL(page.url()).origin;
    await cdp.send("Storage.overrideQuotaForOrigin", {
      origin,
      quotaSize: usage + 64 * 1024,
    });
    try {
      await page.goto(`/app/projects/${story.projectId}`);
      const header = page.getByRole("banner");
      await expect(
        header.getByText(
          "This device's storage is full, so “Harbour” could not be kept offline.",
          { exact: false },
        ),
      ).toBeVisible({ timeout: 30_000 });
      await expect(
        page.getByText(en["offline.status.full"], { exact: false }),
      ).toBeVisible();
      // The pin is kept, to say so, but none of the copy.
      expect(await recordsOf(page, story.projectId)).toBe(1);
      await press(
        header.getByRole("link", { name: en["offline.full_notice_link"] }),
      );
      await expect(page).toHaveURL(/\/app\/account\/offline$/);
    } finally {
      await cdp.send("Storage.overrideQuotaForOrigin", { origin });
    }
  });
});
