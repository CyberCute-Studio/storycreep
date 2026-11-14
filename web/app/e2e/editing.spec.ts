import type { Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  dragOn,
  drawn,
  makeBoard,
  makeProject,
  onPage,
  pointOf,
  sentBatches,
  settled,
  type TestBoard,
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
  violations,
} from "./fixtures.ts";

// The canvas's edits of a board's nodes (PRD FR-EDIT-02, FR-EDIT-06,
// FR-TEXT-01, FR-EDIT-09, plan note N68), on the desktop with a mouse and
// on the tablet with fingers alone: a drag of the selection moves it in one
// command, and a bulk move offers its undo; the tag filter dims the nodes
// without its tag and changes nothing; the editor of an element's text
// stays in view above the on-screen keyboard, and keeps the caret in view as
// the user types; and a usage list's site opens its node on the canvas.

/** A board of six elements in two rows of three, of a project of its own, which the page opens. */
async function openBoard(
  page: Page,
): Promise<TestBoard & { projectId: string; session: ApiSession }> {
  const session = await signedIn(page, "Edda Editor");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const board = await makeBoard(
    page.request,
    session,
    projectId,
    "Harbour",
    6,
    { cols: 3 },
  );
  await page.goto(`/app/projects/${projectId}/boards/${board.boardId}`);
  await drawn(canvasOf(page, board.name));
  return { ...board, projectId, session };
}

/** The middle of the node of the grid's col and row, 240 by 128 at 320 by 200 apart. */
function middleOf(col: number, row: number) {
  return { x: col * 320 + 120, y: row * 200 + 64 };
}

/** Returns the position of the node of id, as the server has it. */
async function positionOf(page: Page, projectId: string, id: string) {
  const res = await page.request.get(
    `/api/v1/projects/${projectId}/nodes/${id}`,
  );
  expect(res.status()).toBe(200);
  return ((await res.json()) as { position: { x: number; y: number } })
    .position;
}

test.describe("a board's canvas, editing its nodes", () => {
  // Verifies: FR-EDIT-02/AC2, NFR-TOUCH-05
  test("moves the selection by a drag with one command, and offers the undo of the bulk move", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const sent = sentBatches(page);
    // The select mode's rubber band selects the first row's first two.
    await press(
      page.getByRole("button", { name: en["canvas.mode_select"], exact: true }),
    );
    await dragOn(
      page,
      await pointOf(canvas, { x: -30, y: -30 }),
      await pointOf(canvas, { x: 440, y: 64 }),
    );
    await expect(canvas).toHaveAttribute("data-selected", "2");
    // A drag of a selected node moves both, by about 100 units, on the grid.
    const from = await pointOf(canvas, middleOf(0, 0));
    const view = await settled(canvas);
    await dragOn(page, from, { x: from.x + 100 * view.zoom, y: from.y });
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toHaveLength(1);
    expect(sent[0][0].type).toBe("node.move");
    const moved = (
      sent[0][0].payload as { nodes: { id: string; position: { x: number } }[] }
    ).nodes;
    expect(moved.map((n) => n.id).sort()).toEqual(
      [board.nodeIds[0], board.nodeIds[1]].sort(),
    );
    for (const n of moved) {
      expect(n.position.x % 8).toBe(0);
    }
    await expect
      .poll(() => positionOf(page, board.projectId, board.nodeIds[1]))
      .not.toEqual({ x: 320, y: 0 });
    // The undo of the bulk move, for a while after it.
    // The catalog's plural, of two nodes, which message does not format.
    const bar = page.getByRole("status").filter({ hasText: "Moved 2 nodes." });
    await press(bar.getByRole("button", { name: en["canvas.undo_move"] }));
    await expect
      .poll(() => positionOf(page, board.projectId, board.nodeIds[1]))
      .toEqual({ x: 320, y: 0 });
    await expect
      .poll(() => positionOf(page, board.projectId, board.nodeIds[0]))
      .toEqual({ x: 0, y: 0 });
    // What became of the undo stays over the canvas's top, where a drag that
    // starts on it is the canvas's, which pans in the pan mode as beside it,
    // of the selection, as of a node under it, cleared.
    const undone = page
      .getByRole("status")
      .filter({ hasText: en["history.undone"] });
    await expect(undone).toBeVisible();
    await canvas.focus();
    await page.keyboard.press("Escape");
    await expect(canvas).toHaveAttribute("data-selected", "0");
    await press(
      page.getByRole("button", { name: en["canvas.mode_pan"], exact: true }),
    );
    const box = await undone.boundingBox();
    expect(box).not.toBeNull();
    const before = await settled(canvas);
    const start = {
      x: (box?.x ?? 0) + (box?.width ?? 0) / 2,
      y: (box?.y ?? 0) + (box?.height ?? 0) / 2,
    };
    await dragOn(page, start, { x: start.x + 80, y: start.y + 80 });
    await expect.poll(async () => (await viewOf(canvas)).x).not.toBe(before.x);
  });

  test("pans by a finger's drag that starts on a node that is not selected, in the pan mode", async ({
    page,
  }) => {
    test.skip(!test.info().project.use.hasTouch, "a finger's pan");
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const sent = sentBatches(page);
    const before = await settled(canvas);
    const from = await onPage(canvas, before, middleOf(1, 0));
    await dragOn(page, from, { x: from.x + 120, y: from.y + 40 });
    const after = await settled(canvas);
    expect(after.x).toBeLessThan(before.x);
    expect(sent).toEqual([]);
  });

  test("resizes the node selected by a drag of its handle, which the canvas follows, with one command of its size, on the grid", async ({
    page,
  }) => {
    const board = await openBoard(page);
    // A link to the node opens the canvas on it, selected, at 100%, where
    // it is drawn large enough for a handle, which a node that the fit to
    // the board draws small, as on the tablet, has none of.
    await page.goto(
      `/app/projects/${board.projectId}/boards/${board.boardId}?node=${board.nodeIds[0]}`,
    );
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    const sent = sentBatches(page);
    // The handle is centred on the node's bottom right corner; a drag of it
    // by 80 and 40 units, whose moves and release the canvas captures, sizes
    // the node from 240 by 128 to 320 by 168.
    const handle = page.locator(".cursor-nwse-resize");
    await expect(handle).toBeVisible();
    const box = await handle.boundingBox();
    expect(box).not.toBeNull();
    const view = await settled(canvas);
    const from = {
      x: (box?.x ?? 0) + (box?.width ?? 0) / 2,
      y: (box?.y ?? 0) + (box?.height ?? 0) / 2,
    };
    await dragOn(page, from, {
      x: from.x + 80 * view.zoom,
      y: from.y + 40 * view.zoom,
    });
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      {
        type: "node.update",
        payload: {
          id: board.nodeIds[0],
          size: { width: 320, height: 168 },
        },
      },
    ]);
    expect(await settled(canvas)).toEqual(view);
  });

  // Verifies: FR-EDIT-06/AC1
  test("dims the nodes without the tag that the filter names, and changes nothing of the board", async ({
    page,
  }) => {
    const session = await signedIn(page, "Tess Tagger");
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const board = await makeBoard(
      page.request,
      session,
      projectId,
      "Harbour",
      6,
      {
        cols: 3,
      },
    );
    await apply(page.request, session, projectId, [
      {
        type: "node.update",
        payload: { id: board.nodeIds[0], tags: ["Quest"] },
      },
      {
        type: "node.update",
        payload: { id: board.nodeIds[4], tags: ["quest", "Mira"] },
      },
    ]);
    await page.goto(`/app/projects/${projectId}/boards/${board.boardId}`);
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    const sent = sentBatches(page);
    const graph = {
      nodes: await canvas.getAttribute("data-nodes"),
      connections: await canvas.getAttribute("data-connections"),
    };
    const filter = page.getByLabel(en["canvas.tag_filter"]);
    await filter.selectOption("Quest");
    await expect(canvas).toHaveAttribute("data-dimmed", "4");
    await filter.selectOption("Mira");
    await expect(canvas).toHaveAttribute("data-dimmed", "5");
    expect({
      nodes: await canvas.getAttribute("data-nodes"),
      connections: await canvas.getAttribute("data-connections"),
    }).toEqual(graph);
    await filter.selectOption("");
    await expect(canvas).toHaveAttribute("data-dimmed", "0");
    expect(sent).toEqual([]);
  });

  // Verifies: FR-EDIT-09/AC3, FR-TEXT-01/AC3, NFR-TOUCH-04
  test("keeps the editor of an element's text whole in view, and its caret, with the on-screen keyboard open", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    // A second tap on a node soon after the first opens its text, near the
    // bottom of the canvas, where the keyboard will cover it.
    const at = await pointOf(canvas, middleOf(1, 1));
    if (test.info().project.use.hasTouch) {
      await page.touchscreen.tap(at.x, at.y);
      await page.touchscreen.tap(at.x, at.y);
    } else {
      await page.mouse.click(at.x, at.y);
      await page.mouse.click(at.x, at.y);
    }
    const editor = page.getByRole("form", {
      name: message("canvas.text_editor", { node: "Scene 2.2" }),
    });
    await expect(editor).toBeVisible();
    const size = page.viewportSize();
    if (size === null) {
      throw new Error("the page has no viewport");
    }
    // The on-screen keyboard leaves 45% of the window's height.
    const height = Math.round(size.height * 0.45);
    await page.setViewportSize({ width: size.width, height });
    const whole = async () => {
      const box = await editor.boundingBox();
      return (
        box !== null &&
        box.x >= 0 &&
        box.y >= 0 &&
        box.x + box.width <= size.width + 0.5 &&
        box.y + box.height <= height + 0.5
      );
    };
    await expect.poll(whole).toBe(true);
    // Lines typed at the text's end keep the caret in view, and the editor
    // whole.
    const text = editor.getByRole("textbox", {
      name: message("canvas.text_of", { node: "Scene 2.2" }),
    });
    await text.focus();
    await page.keyboard.press("Control+End");
    for (let line = 1; line <= 12; line++) {
      await page.keyboard.press("Enter");
      await page.keyboard.type(`Line ${line} of what the keeper says.`);
    }
    const caret = await page.evaluate(() => {
      const range = window.getSelection()?.getRangeAt(0);
      const r = range?.getBoundingClientRect();
      return r === undefined ? undefined : { top: r.top, bottom: r.bottom };
    });
    expect(caret).toBeDefined();
    expect(caret?.top ?? -1).toBeGreaterThanOrEqual(0);
    expect(caret?.bottom ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(
      height,
    );
    await expect.poll(whole).toBe(true);
    const sent = sentBatches(page);
    await press(editor.getByRole("button", { name: en["canvas.text_save"] }));
    await expect(editor).toBeHidden();
    expect(sent.map((b) => b.map((c) => c.type))).toEqual([["node.update"]]);
  });

  test("closes the editor of an element's text with Escape where its text is as it was, and keeps it where the text changed", async ({
    page,
  }) => {
    const board = await openBoard(page);
    const canvas = canvasOf(page, board.name);
    const open = async () => {
      const at = await pointOf(canvas, middleOf(0, 0));
      if (test.info().project.use.hasTouch) {
        await page.touchscreen.tap(at.x, at.y);
        await page.touchscreen.tap(at.x, at.y);
      } else {
        await page.mouse.click(at.x, at.y);
        await page.mouse.click(at.x, at.y);
      }
      const editor = page.getByRole("form", {
        name: message("canvas.text_editor", { node: "Scene 1.1" }),
      });
      const text = editor.getByRole("textbox", {
        name: message("canvas.text_of", { node: "Scene 1.1" }),
      });
      await expect(text).toBeFocused();
      return { editor, text };
    };
    const sent = sentBatches(page);
    // ProseMirror takes every Escape in its text as its own, which the
    // editor does not wait for.
    const first = await open();
    await page.keyboard.press("Escape");
    await expect(first.editor).toBeHidden();
    await expect(canvas).toBeFocused();
    // A text that the user changed stays, to be saved or cancelled.
    const second = await open();
    await page.keyboard.press("End");
    await page.keyboard.type(" The bell rings.");
    await page.keyboard.press("Escape");
    await expect(second.editor).toBeVisible();
    await expect(second.text).toContainText("The bell rings.");
    expect(sent).toEqual([]);
  });

  test("opens the node of a component's usage on the canvas, selected, from the component's panel", async ({
    page,
  }) => {
    const session = await signedIn(page, "Una Usage");
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const board = await makeBoard(
      page.request,
      session,
      projectId,
      "Harbour",
      6,
      {
        cols: 3,
      },
    );
    const typeId = newId("component_type");
    const componentId = newId("component");
    await apply(page.request, session, projectId, [
      {
        type: "component_type.create",
        payload: { id: typeId, name: "Character" },
      },
      {
        type: "component.create",
        payload: { id: componentId, component_type_id: typeId, name: "Elara" },
      },
      {
        type: "node.update",
        payload: { id: board.nodeIds[5], speaker_component_id: componentId },
      },
    ]);
    await page.goto(
      `/app/projects/${projectId}/components?component=${componentId}`,
    );
    await press(page.getByRole("button", { name: en["usage.show"] }));
    // The catalog's selects, of a speaker, which message does not format.
    const speakers = page.getByRole("list", { name: "Speaker of" });
    await press(
      speakers.getByRole("link", { name: "The speaker of Scene 2.3" }),
    );
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    await expect(page).toHaveURL(new RegExp(`\\?node=${board.nodeIds[5]}$`));
    await expect(canvas).toHaveAttribute("data-selected", "1");
    // The canvas shows the node whole, at 100%.
    const view = await settled(canvas);
    expect(view.zoom).toBe(1);
    const box = await canvas.boundingBox();
    const middle = await onPage(canvas, view, middleOf(2, 1));
    expect(
      Math.abs(middle.x - ((box?.x ?? 0) + (box?.width ?? 0) / 2)),
    ).toBeLessThan(2);
  });

  // Verifies: NFR-A11Y-08
  test("passes axe's checks with the inspector of a node and the editor of its text open", async ({
    page,
  }) => {
    const board = await openBoard(page);
    await page.goto(
      `/app/projects/${board.projectId}/boards/${board.boardId}?node=${board.nodeIds[0]}`,
    );
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    const inspect = page.getByRole("button", { name: en["canvas.inspector"] });
    if ((await inspect.getAttribute("aria-pressed")) !== "true") {
      await press(inspect);
    }
    const panel = page.getByRole(
      test.info().project.use.hasTouch ? "dialog" : "region",
      { name: "Scene 1.1" },
    );
    await expect(panel).toBeVisible();
    // A sheet, on the tablet, closes to show the editor.
    await press(panel.getByRole("button", { name: en["inspector.edit_text"] }));
    await expect(
      page.getByRole("form", {
        name: message("canvas.text_editor", { node: "Scene 1.1" }),
      }),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);
  });
});
