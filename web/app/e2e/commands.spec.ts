import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  drawn,
  makeProject,
  sentBatches,
  settled,
  test,
  viewOf,
} from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import {
  type ApiSession,
  en,
  expect,
  message,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// The command palette, the table of the shortcuts, the canvas's keys, and
// the project's search (PRD FR-EDIT-10, FR-PROJ-03, plan note N72), in the
// browser, against the server: a desktop's keys, and a tablet's taps.

/** What a test's project holds: the board Harbour, of two elements far apart. */
interface Harbour {
  projectId: string;
  session: ApiSession;
  board: string;
  quay: string;
}

async function makeHarbour(page: Page): Promise<Harbour> {
  const session = await signedIn(page, "Kim Keys");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const harbour = {
    projectId,
    session,
    board: newId("board"),
    quay: newId("node"),
  };
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: harbour.board, name: "Harbour" } },
    {
      type: "node.create",
      payload: {
        id: harbour.quay,
        board_id: harbour.board,
        type: "element",
        title: "Elara trusts the keeper",
        position: { x: 0, y: 0 },
      },
    },
    {
      type: "node.create",
      payload: {
        id: newId("node"),
        board_id: harbour.board,
        type: "element",
        title: "Far shore",
        position: { x: 4000, y: 2400 },
      },
    },
  ]);
  return harbour;
}

async function openHarbour(page: Page, harbour: Harbour): Promise<Locator> {
  await page.goto(`/app/projects/${harbour.projectId}/boards/${harbour.board}`);
  const canvas = canvasOf(page, "Harbour");
  await drawn(canvas);
  await settled(canvas);
  return canvas;
}

const palette = (page: Page) =>
  page.getByRole("dialog", { name: en["palette.title"] });

test.describe("the command palette and the shortcuts", () => {
  test("open by their keys, run a canvas's command from the palette, and list every shortcut in the table", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a keyboard");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    await canvas.focus();
    await page.keyboard.press("+");
    await page.keyboard.press("+");
    const zoomed = await settled(canvas);
    await page.keyboard.press("ControlOrMeta+K");
    await expect(palette(page)).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await page.keyboard.type("fit board");
    await expect(palette(page).getByRole("option")).toHaveCount(2);
    await page.keyboard.press("Enter");
    await expect(palette(page)).toBeHidden();
    await expect(canvas).toBeFocused();
    await expect
      .poll(async () => (await viewOf(canvas)).zoom)
      .toBeLessThan(zoomed.zoom);

    await page.keyboard.press("?");
    const table = page.getByRole("dialog", { name: en["shortcuts.title"] });
    await expect(table).toBeVisible();
    await expect(
      table.locator('[data-command="canvas.add_element"] kbd'),
    ).toHaveText("E");
    expect(await violations(page)).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(table).toBeHidden();
    await expect(canvas).toBeFocused();
  });

  test("add an element by E on the canvas, and undo it by the undo's keys", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a keyboard");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const sent = sentBatches(page);
    await canvas.focus();
    await page.keyboard.press("e");
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].map((c) => c.type)).toEqual(["node.create"]);
    await expect(
      page.getByRole("button", { name: en["history.undo"] }),
    ).toBeEnabled();
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+Z" : "Control+Z",
    );
    await expect.poll(() => sent.length).toBe(2);
    await expect(page.getByText(en["history.undone"])).toBeVisible();
  });

  test("open from the header by a tap, with every control a fingertip's size", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a touch screen");
    const harbour = await makeHarbour(page);
    await openHarbour(page, harbour);
    await press(page.getByRole("button", { name: en["shell.commands"] }));
    await expect(palette(page)).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    await press(
      palette(page).getByRole("option", {
        name: en["command.shortcuts.show"],
      }),
    );
    const table = page.getByRole("dialog", { name: en["shortcuts.title"] });
    await expect(table).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    expect(await violations(page)).toEqual([]);
  });
});

test.describe("the project's search", () => {
  test("opens by its key with the focus in its field, marks what matched, and opens the result on its board", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a keyboard");
    const harbour = await makeHarbour(page);
    await page.goto(`/app/projects/${harbour.projectId}/variables`);
    await expect(
      page.getByRole("heading", { level: 1, name: en["variables.title"] }),
    ).toBeVisible();
    await page.keyboard.press("Control+/");
    const field = page.getByRole("searchbox", { name: en["search.field"] });
    await expect(field).toBeFocused();
    await page.keyboard.type("ELA tru");
    await page.keyboard.press("Enter");
    const results = page.getByRole("region", { name: en["search.results"] });
    const link = results.getByRole("link", {
      name: "Elara trusts the keeper",
    });
    await expect(link).toBeVisible();
    await expect(link.locator("mark")).toHaveText(["Elara", "trusts"]);
    expect(await violations(page)).toEqual([]);
    await link.click();
    const canvas = canvasOf(page, "Harbour");
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await expect(page).toHaveURL(new RegExp(`node=${harbour.quay}`));
  });

  test("filters by kind, and gives its controls a fingertip's size", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a touch screen");
    const harbour = await makeHarbour(page);
    await page.goto(
      `/app/projects/${harbour.projectId}/search?q=elara&type=variable`,
    );
    await expect(
      page.getByText(message("search.none", { query: "elara" })),
    ).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    await page
      .getByRole("combobox", { name: en["search.kind_filter"] })
      .selectOption("node");
    await expect(
      page
        .getByRole("region", { name: en["search.results"] })
        .getByRole("link", { name: "Elara trusts the keeper" }),
    ).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    expect(await violations(page)).toEqual([]);
  });
});
