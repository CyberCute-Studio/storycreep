import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  dragOn,
  drawn,
  holdFinger,
  makeProject,
  pointOf,
  sentBatches,
  settled,
  test,
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

// The canvas's touch model and its accessibility tree (PRD FR-EDIT-09,
// NFR-TOUCH-01, NFR-TOUCH-02, NFR-TOUCH-06, NFR-A11Y-03, NFR-A11Y-04, plan
// note N70): on the tablet, with fingers alone, a finger held for 400 ms
// opens the context menu of what is under it, every control of the board's
// page is of a fingertip's size, and 8 px from its neighbours, and the
// inspector is a sheet in portrait and docked in landscape, with what was
// typed in it kept as the tablet turns; on the desktop, a right click and
// the keys open the same menu; and on both, the board's nodes and
// connections are a tree that assistive technology reads, and that the
// keyboard selects, opens, and deletes through, and axe finds nothing.

/** What a test's project holds: the board Harbour, of two elements, the first connected to the second, and a jumper to the second. */
interface Harbour {
  projectId: string;
  session: ApiSession;
  board: string;
  quay: string;
  dock: string;
  onward: string;
}

/** Where Harbour's nodes are, each 240 wide: the elements 128 high, and the jumper 72. */
const places = {
  quay: { x: 0, y: 0 },
  dock: { x: 480, y: 0 },
  onward: { x: 0, y: 320 },
};

/** A point of the board between its nodes, clear of them, of the connection's line, and of the minimap in the canvas's corner. */
const empty = { x: 360, y: 224 };

/** Makes the project of a test, as an account that the page signs in as. */
async function makeHarbour(page: Page): Promise<Harbour> {
  const session = await signedIn(page, "Tess Touch");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const harbour: Harbour = {
    projectId,
    session,
    board: newId("board"),
    quay: newId("node"),
    dock: newId("node"),
    onward: newId("node"),
  };
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: harbour.board, name: "Harbour" } },
    {
      type: "node.create",
      payload: {
        id: harbour.quay,
        board_id: harbour.board,
        type: "element",
        title: "Quay",
        position: places.quay,
      },
    },
    {
      type: "node.create",
      payload: {
        id: harbour.dock,
        board_id: harbour.board,
        type: "element",
        title: "Dock",
        position: places.dock,
      },
    },
    {
      type: "node.create",
      payload: {
        id: harbour.onward,
        board_id: harbour.board,
        type: "jumper",
        title: "Onward",
        position: places.onward,
      },
    },
    {
      type: "node.update",
      payload: {
        id: harbour.onward,
        target: { board_id: harbour.board, node_id: harbour.dock },
      },
    },
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: harbour.quay,
        to_node_id: harbour.dock,
      },
    },
  ]);
  return harbour;
}

/** Opens Harbour, fitted to the canvas, so that every node of it is in view, and returns its canvas once it has drawn the board. */
async function openHarbour(page: Page, harbour: Harbour): Promise<Locator> {
  await page.goto(`/app/projects/${harbour.projectId}/boards/${harbour.board}`);
  const canvas = canvasOf(page, "Harbour");
  await drawn(canvas);
  await settled(canvas);
  return canvas;
}

/** Returns the middle of the node at place, of a node of height, on the page. */
async function middleOf(
  canvas: Locator,
  place: { x: number; y: number },
  height = 128,
) {
  return pointOf(canvas, { x: place.x + 120, y: place.y + height / 2 });
}

/** Returns the form of the plural message of key for one. */
function one(key: string): string {
  const form = /one \{([^}]*)\}/.exec(en[key]);
  if (form === null) {
    throw new Error(`${key} has no form for one`);
  }
  return form[1];
}

/** The context menu of the one node selected. */
function nodeMenu(page: Page): Locator {
  return page.getByRole("menu", { name: one("canvas.menu.nodes") });
}

/** The canvas's tree of the board's nodes and connections. */
function treeOf(page: Page): Locator {
  return page.getByRole("tree", {
    name: message("canvas.tree.label", { board: "Harbour" }),
  });
}

test.describe("the canvas's touch model", () => {
  // Verifies: NFR-TOUCH-02
  test("opens the context menu of the node under a finger held for 400 ms, and deletes the node from it, with no mouse", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a finger's press");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const sent = sentBatches(page);
    const dock = await middleOf(canvas, places.dock);
    // A tap selects, and opens no menu.
    await page.touchscreen.tap(dock.x, dock.y);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await page.waitForTimeout(600);
    await expect(page.getByRole("menu")).toHaveCount(0);
    // Nor does a finger that pans, from the empty board between the nodes.
    const between = await pointOf(canvas, empty);
    await dragOn(page, between, { x: between.x + 40, y: between.y + 30 });
    await page.waitForTimeout(600);
    await expect(page.getByRole("menu")).toHaveCount(0);
    // A finger held still on another node opens that node's menu, while
    // the finger is still down, and selects it in place of the selection.
    const quay = await middleOf(canvas, places.quay);
    const menu = nodeMenu(page);
    const started = Date.now();
    await holdFinger(page, quay, async () => {
      await expect(menu).toBeVisible();
      await expect(canvas).toHaveAttribute("data-selected", "1");
    });
    // Once it has been held for 400 ms, and no sooner.
    expect(Date.now() - started).toBeGreaterThanOrEqual(400);
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem")).toHaveText([
      en["canvas.menu.edit_text"],
      en["canvas.menu.inspect"],
      en["canvas.zoom_selection"],
      en["canvas.duplicate"],
      en["canvas.delete"],
    ]);
    expect(await violations(page)).toEqual([]);
    await menu.getByRole("menuitem", { name: en["canvas.delete"] }).tap();
    await expect(menu).toBeHidden();
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      { type: "node.delete", payload: { id: harbour.quay } },
    ]);
    await expect(canvas).toHaveAttribute("data-nodes", "2");
  });

  // Verifies: NFR-TOUCH-02
  test("opens the board's menu by a finger held on the empty board, and adds a node where it was held", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a finger's press");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const sent = sentBatches(page);
    const at = await pointOf(canvas, empty);
    const menu = page.getByRole("menu", { name: en["canvas.menu.board"] });
    await holdFinger(page, at, () => expect(menu).toBeVisible());
    await menu
      .getByRole("menuitem", { name: en["canvas.menu.add_element"] })
      .tap();
    await expect.poll(() => sent.length).toBe(1);
    // An element is 240 by 128: its middle is where the finger was, on the
    // board's grid.
    expect(sent[0]).toMatchObject([
      {
        type: "node.create",
        payload: { type: "element", position: { x: 240, y: 160 } },
      },
    ]);
    await expect(canvas).toHaveAttribute("data-nodes", "4");
    await expect(canvas).toHaveAttribute("data-selected", "1");
  });

  // Verifies: NFR-TOUCH-02
  test("opens the same menu by a right click, and by Shift+F10, so that nothing is a right click's alone", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a mouse and keys");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const quay = await middleOf(canvas, places.quay);
    await page.mouse.click(quay.x, quay.y, { button: "right" });
    const menu = nodeMenu(page);
    await expect(menu).toBeVisible();
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(canvas).toBeFocused();
    // The keys open it of what is selected.
    await page.keyboard.press("Shift+F10");
    await expect(menu).toBeVisible();
    expect(await violations(page)).toEqual([]);
    // And its items are the keyboard's: the first, Edit the text, has the
    // focus as the keys open the menu, and Enter chooses it.
    await expect(
      menu.getByRole("menuitem", { name: en["canvas.menu.edit_text"] }),
    ).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(
      page.getByRole("form", {
        name: message("canvas.text_editor", { node: "Quay" }),
      }),
    ).toBeVisible();
  });

  // Verifies: NFR-TOUCH-01
  test("gives every control of the board's page a fingertip's size, 8 px from its neighbours, with a node selected, in its menus, and in the inspector's sheet", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a touch screen");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    expect(await smallTargets(page)).toEqual([]);
    // A jumper selected, with its handle and the link to its target.
    const onward = await middleOf(canvas, places.onward, 72);
    await page.touchscreen.tap(onward.x, onward.y);
    await expect(
      page.getByRole("link", { name: en["canvas.go_to_target"] }),
    ).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    // An element selected, with its handles.
    const quay = await middleOf(canvas, places.quay);
    await page.touchscreen.tap(quay.x, quay.y);
    await expect(page.locator("[data-link=new]")).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    // The toolbar's menu, and the context menu.
    const add = page.getByRole("button", {
      name: en["canvas.add"],
      exact: true,
    });
    await press(add);
    await expect(page.getByRole("menu")).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toBeHidden();
    await holdFinger(page, quay, () => expect(nodeMenu(page)).toBeVisible());
    expect(await smallTargets(page)).toEqual([]);
    // The inspector, a sheet over the page.
    await nodeMenu(page)
      .getByRole("menuitem", { name: en["canvas.menu.inspect"] })
      .tap();
    const sheet = page.getByRole("dialog", { name: "Quay" });
    await expect(sheet).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
  });

  // Verifies: NFR-TOUCH-06
  test("shows the inspector as a sheet in portrait and docked in landscape, with what was typed in it kept as the tablet turns", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "a tablet's turn");
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const quay = await middleOf(canvas, places.quay);
    await page.touchscreen.tap(quay.x, quay.y);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await press(page.getByRole("button", { name: en["canvas.inspector"] }));
    const sheet = page.getByRole("dialog", { name: "Quay" });
    await expect(sheet).toBeVisible();
    const title = sheet.getByLabel(en["inspector.node_title"], { exact: true });
    await title.fill("Quay at dusk");
    const size = page.viewportSize();
    if (size === null) {
      throw new Error("the page has no viewport");
    }
    // The tablet turns to landscape, 1,024 px wide: the panel docks beside
    // the canvas, the same element, with the title as it was typed.
    await page.setViewportSize({ width: size.height, height: size.width });
    const docked = page.getByRole("region", { name: "Quay" });
    await expect(docked).toBeVisible();
    await expect(page.getByRole("dialog", { name: "Quay" })).toHaveCount(0);
    await expect(
      docked.getByLabel(en["inspector.node_title"], { exact: true }),
    ).toHaveValue("Quay at dusk");
    await drawn(canvas);
    expect(await smallTargets(page)).toEqual([]);
    expect(await violations(page)).toEqual([]);
    // And back to portrait.
    await page.setViewportSize(size);
    await expect(sheet).toBeVisible();
    await expect(title).toHaveValue("Quay at dusk");
  });
});

test.describe("the canvas's accessibility tree", () => {
  // Verifies: NFR-A11Y-03
  test("exposes the board's nodes and connections with roles and names, and says the selection as it changes", async ({
    page,
  }) => {
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const tree = treeOf(page);
    await expect(tree).toMatchAriaSnapshot(`
      - tree "Nodes and connections of Harbour":
        - treeitem "Quay, Dialogue"
        - treeitem "Dock, Dialogue"
        - 'treeitem "Onward, Jumper: To Dock"'
    `);
    const quay = tree.getByRole("treeitem", { name: "Quay, Dialogue" });
    await expect(quay).toHaveAttribute("aria-expanded", "false");
    await expect(quay).toHaveAttribute("aria-selected", "false");
    // An item has a box, in the tree's, which the page does not show,
    // rather than none, which a screen reader that goes by where things
    // are may pass over.
    expect((await quay.boundingBox())?.height).toBeGreaterThanOrEqual(1);
    // The selection, made on the canvas, is the tree's, and the toolbar
    // says it, by the node's name, in a live region.
    const at = await middleOf(canvas, places.quay);
    if (test.info().project.use.hasTouch) {
      await page.touchscreen.tap(at.x, at.y);
    } else {
      await page.mouse.click(at.x, at.y);
    }
    await expect(quay).toHaveAttribute("aria-selected", "true");
    const said = page
      .getByRole("group", { name: en["canvas.controls"] })
      .locator("[aria-live=polite]");
    await expect(said).toContainText(
      message("canvas.selected_node", { title: "Quay", type: "Dialogue" }),
    );
    // The connections that leave a node are under it, once it is opened.
    await quay.focus();
    await page.keyboard.press("ArrowRight");
    await expect(tree).toMatchAriaSnapshot(`
      - tree "Nodes and connections of Harbour":
        - treeitem "Quay, Dialogue" [expanded] [selected]:
          - group:
            - treeitem "Connection to Dock"
        - treeitem "Dock, Dialogue"
        - 'treeitem "Onward, Jumper: To Dock"'
    `);
    expect(await violations(page)).toEqual([]);
  });

  // Verifies: NFR-A11Y-04
  test("selects, opens, and deletes a node with the keyboard alone, through the tree after the canvas, with the focus shown on the canvas", async ({
    page,
  }) => {
    const harbour = await makeHarbour(page);
    const canvas = await openHarbour(page, harbour);
    const sent = sentBatches(page);
    const tree = treeOf(page);
    const quay = tree.getByRole("treeitem", { name: "Quay, Dialogue" });
    const dock = tree.getByRole("treeitem", { name: "Dock, Dialogue" });
    const ring = page.locator("[data-tree-focus]");
    await expect(ring).toBeHidden();
    // The stop after the canvas is the tree's first node.
    await canvas.focus();
    await page.keyboard.press("Tab");
    await expect(quay).toBeFocused();
    // The focus is shown around the node on the canvas, at 3:1 or more
    // against it, as the page's focus rings are (tokens.test.ts).
    await expect(ring).toBeVisible();
    const node = await pointOf(canvas, places.quay);
    const box = await ring.boundingBox();
    expect(box?.x).toBeCloseTo(node.x, 0);
    expect(box?.y).toBeCloseTo(node.y, 0);
    await page.keyboard.press("ArrowDown");
    await expect(dock).toBeFocused();
    await expect(canvas).toHaveAttribute("data-selected", "0");
    await page.keyboard.press("Space");
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await expect(dock).toHaveAttribute("aria-selected", "true");
    // Enter opens the node's text, with the focus, and its Cancel, by the
    // keys too, closes it.
    await page.keyboard.press("Enter");
    const editor = page.getByRole("form", {
      name: message("canvas.text_editor", { node: "Dock" }),
    });
    await expect(
      editor.getByRole("textbox", {
        name: message("canvas.text_of", { node: "Dock" }),
      }),
    ).toBeFocused();
    await editor
      .getByRole("button", { name: en["canvas.text_cancel"] })
      .press("Enter");
    await expect(editor).toBeHidden();
    // The menu of what the focus is on, and its Delete, by the keys.
    await quay.focus();
    await page.keyboard.press("Shift+F10");
    const menu = nodeMenu(page);
    await expect(menu).toBeVisible();
    // The menu opens with the focus on its first item, from which End goes
    // to its last, a moment after the key, as Radix moves the focus of a
    // menu's items: Enter waits for it.
    await page.keyboard.press("End");
    await expect(
      menu.getByRole("menuitem", { name: en["canvas.delete"] }),
    ).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      { type: "node.delete", payload: { id: harbour.quay } },
    ]);
    await expect(quay).toHaveCount(0);
    // The focus is on what is in the deleted node's place, in the tree.
    await expect(dock).toBeFocused();
    // No trap: Tab goes on from the tree, and Shift+Tab back to the canvas.
    await page.keyboard.press("Shift+Tab");
    await expect(canvas).toBeFocused();
  });
});
