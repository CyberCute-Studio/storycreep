import type { Page } from "@playwright/test";
import {
  canvasOf,
  drawn,
  makeBoard,
  makeProject,
  onPage,
  settled,
  type TestBoard,
  test,
  touchGesture,
  viewOf,
} from "./boards.ts";
import { signedIn } from "./content.ts";
import { en, expect, press, violations } from "./fixtures.ts";

// A board's canvas (PRD FR-EDIT-01, FR-EDIT-11, ADR-013), on the desktop,
// with a mouse, a wheel, and keys, and on the tablet, with fingers alone:
// pinching holds the point between the fingers, one finger pans or selects
// as the toolbar's toggle says, nothing needs hover or a right click, nodes
// are blocks without text below 40%, the browser's pull-to-refresh is off
// over the canvas, reduced motion stops the canvas's animations, and the
// Canvas 2D renderer draws where the browser has no WebGL. The canvas's
// frame rates, memory, and load times are its benchmark's (bench/).

/** Signs in, and opens a board of count nodes of a project of its own. */
async function openBoard(
  page: Page,
  count = 60,
): Promise<TestBoard & { projectId: string }> {
  const session = await signedIn(page, "Cass Canvas");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const board = await makeBoard(
    page.request,
    session,
    projectId,
    "Harbour",
    count,
  );
  await page.goto(`/app/projects/${projectId}/boards/${board.boardId}`);
  await drawn(canvasOf(page, board.name));
  return { ...board, projectId };
}

/** Returns the zoom, as the toolbar's button shows it, of a percent. */
function zoomButton(page: Page) {
  return page.getByRole("button", { name: /^Zoom \d+%: show at 100%$/ });
}

test.describe("a board's canvas", () => {
  // Verifies: NFR-A11Y-08
  test("draws the board's nodes, fitted, and passes axe's checks", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      board.name,
    );
    expect(Number(await canvas.getAttribute("data-nodes"))).toBeGreaterThan(0);
    // Fitted, the whole board is in the canvas.
    await expect(canvas).toHaveAttribute(
      "data-visible",
      String(board.nodeIds.length),
    );
    expect(await violations(page)).toEqual([]);
  });

  test("opens where it was left on the board that the page showed last", async ({
    page,
  }) => {
    const board = await openBoard(page);
    await press(page.getByRole("button", { name: en["canvas.zoom_in"] }));
    const left = await settled(canvasOf(page, board.name));
    // The board's nodes are in the project's document already, so that
    // its read is done before the canvas's first frame, which places it
    // where it was left (PRD FR-PROJ-04), at the canvas's height.
    await press(page.getByRole("link", { name: en["project.nav.boards"] }));
    await expect(canvasOf(page, board.name)).toHaveCount(0);
    await page.goBack();
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    const again = await settled(canvas);
    expect(again.zoom).toBeCloseTo(left.zoom, 3);
    expect(again.x).toBeCloseTo(left.x, 0);
    expect(again.y).toBeCloseTo(left.y, 0);
  });

  // Verifies: FR-EDIT-01/AC1
  test("keeps the point between two pinching fingers under them, within 2 px", async ({
    page,
  }) => {
    test.skip(!test.info().project.use.hasTouch, "a touch screen's pinch");
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const cdp = await page.context().newCDPSession(page);
    const box = await canvas.boundingBox();
    if (box === null) {
      throw new Error("the canvas is not shown");
    }
    const mid = { x: box.x + box.width * 0.4, y: box.y + box.height * 0.55 };
    const before = await viewOf(canvas);
    const anchor = {
      x: before.x + (mid.x - box.x) / before.zoom,
      y: before.y + (mid.y - box.y) / before.zoom,
    };
    // The fingers spread apart about the point, which zooms in.
    await touchGesture(cdp, [
      {
        from: { x: mid.x - 40, y: mid.y - 10 },
        to: { x: mid.x - 160, y: mid.y - 40 },
      },
      {
        from: { x: mid.x + 40, y: mid.y + 10 },
        to: { x: mid.x + 160, y: mid.y + 40 },
      },
    ]);
    // The canvas writes what it shows in the frame after each move, which
    // may not have come yet as the last one has been sent.
    const after = await settled(canvas);
    expect(after.zoom).toBeGreaterThan(before.zoom * 3);
    const shown = await onPage(canvas, after, anchor);
    expect(Math.hypot(shown.x - mid.x, shown.y - mid.y)).toBeLessThan(2);
    // Closer together, and moving together, they zoom out and pan, and the
    // point between them stays under it.
    const v = after;
    const start = { x: mid.x, y: mid.y };
    const end = { x: mid.x + 120, y: mid.y - 60 };
    const held = {
      x: v.x + (start.x - box.x) / v.zoom,
      y: v.y + (start.y - box.y) / v.zoom,
    };
    await touchGesture(cdp, [
      {
        from: { x: start.x - 150, y: start.y },
        to: { x: end.x - 60, y: end.y },
      },
      {
        from: { x: start.x + 150, y: start.y },
        to: { x: end.x + 60, y: end.y },
      },
    ]);
    const moved = await settled(canvas);
    expect(moved.zoom).toBeLessThan(v.zoom);
    const kept = await onPage(canvas, moved, held);
    expect(Math.hypot(kept.x - end.x, kept.y - end.y)).toBeLessThan(2);
  });

  // Verifies: FR-EDIT-01/AC2, NFR-TOUCH-03
  test("pans, zooms, fits, and zooms to a selection by touch alone, with no hover or right click", async ({
    page,
  }) => {
    test.skip(!test.info().project.use.hasTouch, "a touch screen's gestures");
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const cdp = await page.context().newCDPSession(page);
    const box = await canvas.boundingBox();
    if (box === null) {
      throw new Error("the canvas is not shown");
    }
    // One finger pans, by default.
    const v0 = await viewOf(canvas);
    const from = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    await touchGesture(cdp, [
      { from, to: { x: from.x - 200, y: from.y - 100 } },
    ]);
    const v1 = await settled(canvas);
    expect((v1.x - v0.x) * v1.zoom).toBeCloseTo(200, 0);
    expect((v1.y - v0.y) * v1.zoom).toBeCloseTo(100, 0);
    // The zoom's buttons.
    await zoomButton(page).tap();
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    await page.getByRole("button", { name: en["canvas.zoom_out"] }).tap();
    await expect(canvas).toHaveAttribute("data-zoom", "0.75");
    await page.getByRole("button", { name: en["canvas.zoom_in"] }).tap();
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    // A tap selects the node under the finger, and zoom to selection
    // shows it.
    await page.getByRole("button", { name: en["canvas.fit"] }).tap();
    await expect(canvas).toHaveAttribute(
      "data-visible",
      String(board.nodeIds.length),
    );
    const fitted = await settled(canvas);
    const node = await onPage(canvas, fitted, {
      x: 320 * 2 + 100,
      y: 200 + 60,
    });
    await page.touchscreen.tap(node.x, node.y);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await expect(page.getByText("1 node selected")).toBeVisible();
    await page.getByRole("button", { name: en["canvas.zoom_selection"] }).tap();
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    await expect(canvas).toHaveAttribute("data-visible", /^\d+$/);
    // In the select mode, one finger draws a rectangle that selects the
    // nodes that it overlaps, and the board stays where it is.
    await page.getByRole("button", { name: en["canvas.fit"] }).tap();
    await expect(canvas).toHaveAttribute(
      "data-visible",
      String(board.nodeIds.length),
    );
    await page
      .getByRole("button", { name: en["canvas.mode_select"], exact: true })
      .tap();
    await expect(
      page.getByRole("button", { name: en["canvas.mode_select"], exact: true }),
    ).toHaveAttribute("aria-pressed", "true");
    const v2 = await settled(canvas);
    const corner = await onPage(canvas, v2, { x: -20, y: -20 });
    const across = await onPage(canvas, v2, { x: 320 + 100, y: 200 + 60 });
    await touchGesture(cdp, [{ from: corner, to: across }]);
    await expect(canvas).toHaveAttribute("data-selected", "4");
    expect(await viewOf(canvas)).toEqual(v2);
    // The minimap's tap centres the canvas there.
    await page.getByRole("button", { name: en["canvas.zoom_in"] }).tap();
    await page.getByRole("button", { name: en["canvas.zoom_in"] }).tap();
    const before = await settled(canvas);
    const minimap = canvas.locator("canvas").last();
    const mbox = await minimap.boundingBox();
    if (mbox === null) {
      throw new Error("the minimap is not shown");
    }
    await page.touchscreen.tap(
      mbox.x + mbox.width * 0.9,
      mbox.y + mbox.height * 0.9,
    );
    await expect
      .poll(async () => (await viewOf(canvas)).x)
      .toBeGreaterThan(before.x);
  });

  // Verifies: FR-EDIT-01/AC2
  test("pans and zooms with the mouse, the wheel, and the keys", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a mouse and keys");
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const box = await canvas.boundingBox();
    if (box === null) {
      throw new Error("the canvas is not shown");
    }
    await page.getByRole("button", { name: /^Zoom \d+%/ }).click();
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    // A drag with the primary button pans.
    const v0 = await settled(canvas);
    await page.mouse.move(box.x + 300, box.y + 200);
    await page.mouse.down();
    await page.mouse.move(box.x + 260, box.y + 150, { steps: 5 });
    await page.mouse.up();
    // The canvas writes the view in the frame after each move, which may
    // not have run yet as the release has.
    const v1 = await settled(canvas);
    expect(v1.x - v0.x).toBeCloseTo(40, 0);
    expect(v1.y - v0.y).toBeCloseTo(50, 0);
    // The wheel pans, and zooms about the pointer with Ctrl.
    await page.mouse.wheel(0, 100);
    await expect
      .poll(async () => (await viewOf(canvas)).y)
      .toBeCloseTo(v1.y + 100, 0);
    await page.keyboard.down("Control");
    await page.mouse.wheel(0, -100);
    await page.keyboard.up("Control");
    await expect
      .poll(async () => (await viewOf(canvas)).zoom)
      .toBeGreaterThan(1);
    // The keys, on the canvas, which a click focused.
    await expect(canvas).toBeFocused();
    await page.keyboard.press("0");
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    const v2 = await settled(canvas);
    await page.keyboard.press("ArrowRight");
    await expect
      .poll(async () => (await viewOf(canvas)).x)
      .toBeCloseTo(v2.x + box.width / 10, 0);
    await page.keyboard.press("-");
    await expect(canvas).toHaveAttribute("data-zoom", "0.75");
    await page.keyboard.press("Shift+Digit1");
    await expect(canvas).toHaveAttribute(
      "data-visible",
      String(board.nodeIds.length),
    );
    // A drag with the right button moves nothing of the board: its click
    // opens the canvas's own menu, which nothing needs (plan note N70), and
    // which Escape closes, with the focus on the canvas again.
    const v3 = await settled(canvas);
    await page.mouse.move(box.x + 300, box.y + 200);
    await page.mouse.down({ button: "right" });
    await page.mouse.move(box.x + 100, box.y + 100, { steps: 5 });
    await page.mouse.up({ button: "right" });
    expect(await settled(canvas)).toEqual(v3);
    await expect(page.getByRole("menu")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toBeHidden();
    await expect(canvas).toBeFocused();
    // Nor on the minimap, where a click of the primary button centres the
    // canvas.
    const minimap = canvas.locator("canvas").last();
    const mbox = await minimap.boundingBox();
    if (mbox === null) {
      throw new Error("the minimap is not shown");
    }
    const corner = {
      x: mbox.x + mbox.width * 0.9,
      y: mbox.y + mbox.height * 0.9,
    };
    await page.mouse.click(corner.x, corner.y, { button: "right" });
    expect(await settled(canvas)).toEqual(v3);
    // Nor does a press that went down elsewhere, as on the board's name
    // above the canvas, and that crosses the minimap.
    const title = await page
      .getByRole("heading", { level: 1, name: board.name })
      .boundingBox();
    if (title === null) {
      throw new Error("the board's name is not shown");
    }
    await page.mouse.move(title.x + 10, title.y + title.height / 2);
    await page.mouse.down();
    await page.mouse.move(corner.x, corner.y, { steps: 10 });
    await page.mouse.move(mbox.x + 10, mbox.y + 10, { steps: 10 });
    await page.mouse.up();
    expect(await settled(canvas)).toEqual(v3);
    await page.mouse.click(corner.x, corner.y);
    await expect
      .poll(async () => (await viewOf(canvas)).x)
      .toBeGreaterThan(v3.x);
  });

  // Verifies: FR-EDIT-01/AC4
  test("draws nodes as blocks without text below 40%, and as cards with text from 40%", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    await press(zoomButton(page));
    await expect(canvas).toHaveAttribute("data-lod", "detail");
    expect(Number(await canvas.getAttribute("data-texts"))).toBeGreaterThan(0);
    const out = page.getByRole("button", { name: en["canvas.zoom_out"] });
    // 100% to 75, 67, 50, and 40%, which still has text.
    for (const zoom of ["0.75", "0.67", "0.5", "0.4"]) {
      await press(out);
      await expect(canvas).toHaveAttribute("data-zoom", zoom);
    }
    await expect(canvas).toHaveAttribute("data-lod", "detail");
    await press(out);
    await expect(canvas).toHaveAttribute("data-zoom", "0.33");
    await expect(canvas).toHaveAttribute("data-lod", "blocks");
    await expect(canvas).toHaveAttribute("data-texts", "0");
    expect(Number(await canvas.getAttribute("data-nodes"))).toBeGreaterThan(0);
  });

  // Verifies: NFR-TOUCH-08
  test("keeps the browser's pull-to-refresh and overscroll off over the canvas", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const style = await canvas.evaluate((el) => {
      const s = getComputedStyle(el);
      return { touchAction: s.touchAction, overscroll: s.overscrollBehaviorY };
    });
    expect(style).toEqual({ touchAction: "none", overscroll: "none" });
    if (test.info().project.use.hasTouch) {
      // A finger's pull down from the canvas's top neither scrolls the page
      // nor reloads it.
      await page.evaluate(() => {
        window.scrollTo(0, 0);
        (window as { kept?: boolean }).kept = true;
      });
      const box = await canvas.boundingBox();
      if (box === null) {
        throw new Error("the canvas is not shown");
      }
      const cdp = await page.context().newCDPSession(page);
      await touchGesture(cdp, [
        {
          from: { x: box.x + box.width / 2, y: box.y + 10 },
          to: { x: box.x + box.width / 2, y: box.y + 400 },
        },
      ]);
      expect(
        await page.evaluate(() => ({
          scrolled: window.scrollY,
          kept: (window as { kept?: boolean }).kept,
        })),
      ).toEqual({ scrolled: 0, kept: true });
    }
  });

  test("leads from its toolbar to the board's settings under it, in one tap or click", async ({
    page,
  }) => {
    await openBoard(page);
    const heading = page.getByRole("heading", {
      name: en["board.settings_title"],
    });
    // The canvas fills the window under its top, and neither a swipe nor
    // the wheel over it scrolls the page down to its settings.
    await expect(heading).not.toBeInViewport();
    const link = page.getByRole("link", { name: en["canvas.settings"] });
    expect((await link.boundingBox())?.height).toBeGreaterThanOrEqual(44);
    await press(link);
    await expect(heading).toBeFocused();
    await expect(heading).toBeInViewport();
    // The page's address is the router's, and stays as it was.
    expect(new URL(page.url()).hash).toBe("");
  });

  // Verifies: NFR-TOUCH-01
  test("gives its toolbar's controls a finger's size, 8 px apart", async ({
    page,
  }) => {
    await openBoard(page);
    const boxes = await page
      .getByRole("group", { name: en["canvas.controls"] })
      .locator("button, a")
      .evaluateAll((controls) =>
        controls.map((control) => {
          const r = control.getBoundingClientRect();
          return { left: r.left, top: r.top, right: r.right, bottom: r.bottom };
        }),
      );
    expect(boxes.length).toBeGreaterThan(1);
    for (const [i, a] of boxes.entries()) {
      expect(a.right - a.left).toBeGreaterThanOrEqual(44);
      expect(a.bottom - a.top).toBeGreaterThanOrEqual(44);
      for (const b of boxes.slice(i + 1)) {
        // How far apart two controls are, across or down, as a row that
        // wraps sets them, to the layout's fraction of a pixel.
        const gap = Math.max(
          b.left - a.right,
          a.left - b.right,
          b.top - a.bottom,
          a.top - b.bottom,
        );
        expect(Math.round(gap)).toBeGreaterThanOrEqual(8);
      }
    }
  });

  // Verifies: NFR-A11Y-07
  test("moves at once, without animating, where the system asks for reduced motion", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    /** Returns how many zooms the canvas showed since fit to content last moved it from 100%. */
    const zooms = () =>
      page.evaluate(() => (window as { zooms?: string[] }).zooms?.length ?? 0);
    /** Moves the canvas to 100%, and fits it to the content, which it then shows whole. */
    const fitContent = async () => {
      await press(zoomButton(page));
      await expect(canvas).toHaveAttribute("data-zoom", "1");
      await canvas.evaluate((el) => {
        const seen: string[] = [];
        (window as { zooms?: string[] }).zooms = seen;
        new MutationObserver(() => seen.push(el.dataset.zoom ?? "")).observe(
          el,
          {
            attributeFilter: ["data-zoom"],
          },
        );
      });
      await press(page.getByRole("button", { name: en["canvas.fit"] }));
      await expect(canvas).toHaveAttribute(
        "data-visible",
        String(board.nodeIds.length),
      );
    };
    // Animated, the zoom passes through frames between where it starts and
    // where the fit goes, as many as come in the animation's time: on a
    // runner without a GPU, whose frames take much of it, as few as one, so
    // the test counts the frames that show a zoom between the two rather
    // than how many frames there were.
    await fitContent();
    const fitted = (await settled(canvas)).zoom;
    const between = await page.evaluate(
      ([from, to]) =>
        ((window as { zooms?: string[] }).zooms ?? [])
          .map(Number)
          .filter((z) => z > Math.min(from, to) && z < Math.max(from, to))
          .length,
      [1, fitted],
    );
    expect(fitted).not.toBeCloseTo(1, 3);
    expect(between).toBeGreaterThan(0);
    await page.emulateMedia({ reducedMotion: "reduce" });
    // At once, the frame after the press shows where the fit goes, and the
    // frames after it, which an animation would go on in, move nothing.
    await fitContent();
    await page.evaluate(
      () =>
        new Promise((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(resolve)),
        ),
    );
    expect(await zooms()).toBe(1);
  });

  // Verifies: FR-EDIT-11/AC1
  test("holds objects of the nodes in and around the canvas alone, however large the board", async ({
    page,
  }) => {
    const board = await openBoard(page, 600);
    const canvas = canvasOf(page, board.name);
    await press(zoomButton(page));
    await expect(canvas).toHaveAttribute("data-zoom", "1");
    const held = Number(await canvas.getAttribute("data-nodes"));
    const box = await canvas.boundingBox();
    if (box === null) {
      throw new Error("the canvas is not shown");
    }
    // The canvas and half a canvas around it, at 320 by 200 units a node.
    const most =
      Math.ceil((box.width * 2) / 320 + 1) *
      Math.ceil((box.height * 2) / 200 + 1);
    expect(held).toBeLessThanOrEqual(most);
    expect(held).toBeLessThan(board.nodeIds.length);
    // Past the content, by ten canvases' heights, where the margin around
    // the canvas holds no node, it holds no card, and throws nothing (the
    // test fails on what the page throws), as it frees the cards that it
    // showed.
    await canvas.focus();
    for (let i = 0; i < 20; i++) {
      await page.keyboard.press("Shift+ArrowDown");
    }
    await expect(canvas).toHaveAttribute("data-nodes", "0");
    await expect(canvas).toHaveAttribute("data-lod", "detail");
  });

  // ADR-013: the Canvas 2D renderer, where the browser has no WebGL.
  test("draws with the Canvas 2D renderer where the browser has no WebGL", async ({
    page,
  }) => {
    await page.addInitScript(() => {
      const getContext = HTMLCanvasElement.prototype.getContext as (
        this: HTMLCanvasElement,
        kind: string,
        ...rest: unknown[]
      ) => unknown;
      HTMLCanvasElement.prototype.getContext = function (
        this: HTMLCanvasElement,
        kind: string,
        ...rest: unknown[]
      ) {
        return kind.includes("webgl")
          ? null
          : getContext.call(this, kind, ...rest);
      } as typeof HTMLCanvasElement.prototype.getContext;
      Object.defineProperty(navigator, "gpu", { value: undefined });
    });
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    await expect(canvas).toHaveAttribute("data-renderer", "canvas");
    expect(Number(await canvas.getAttribute("data-nodes"))).toBeGreaterThan(0);
    await press(zoomButton(page));
    await expect(canvas).toHaveAttribute("data-lod", "detail");
  });
});
