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
import { apply, type Command, signedIn } from "./content.ts";
import {
  type ApiSession,
  en,
  expect,
  message,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// The expression editor (PRD FR-EXPR-01, FR-DEP-01 AC3, FR-EDIT-09 AC1,
// plan note N74), on the desktop with a mouse and on the tablet with
// fingers alone: the suite of FR-EDIT-09 AC1, which creates a node, edits
// its text, connects it, sets the connection's condition, deletes the node,
// and undoes that, with no mouse; a variable's usages and its panel within
// two presses of a condition that names it; the guided picker, whose
// condition the server compiles to the AST of the one typed; and axe's
// checks and the size of the targets with the editor's list and its picker
// open.

/** What a test's project holds: the board Harbour, of the element Quay, and the global variables met_elara and gold. */
interface Harbour {
  projectId: string;
  session: ApiSession;
  board: string;
  quay: string;
  metElara: string;
  gold: string;
}

/** Makes the project of a test, as an account that the page signs in as, with more commands of its own after Harbour's. */
async function makeHarbour(
  page: Page,
  more: (h: Harbour) => Command[] = () => [],
): Promise<Harbour> {
  const session = await signedIn(page, "Ezra Expression");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const harbour: Harbour = {
    projectId,
    session,
    board: newId("board"),
    quay: newId("node"),
    metElara: newId("variable"),
    gold: newId("variable"),
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
        position: { x: 0, y: 0 },
      },
    },
    {
      type: "variable.create",
      payload: {
        id: harbour.metElara,
        key: "met_elara",
        type: "bool",
        scope: "global",
        description: "Whether the player has met Elara.",
      },
    },
    {
      type: "variable.create",
      payload: { id: harbour.gold, key: "gold", type: "int", scope: "global" },
    },
    ...more(harbour),
  ]);
  return harbour;
}

/** Opens Harbour, on the node of id if one is given, and returns its canvas once it has drawn the board. */
async function openHarbour(
  page: Page,
  harbour: Harbour,
  node?: string,
): Promise<Locator> {
  await page.goto(
    `/app/projects/${harbour.projectId}/boards/${harbour.board}${
      node === undefined ? "" : `?node=${node}`
    }`,
  );
  const canvas = canvasOf(page, "Harbour");
  await drawn(canvas);
  await settled(canvas);
  return canvas;
}

/** Returns the inspector's panel of the thing named name: a region beside the canvas on the desktop, and a sheet on the tablet, which the toolbar's button opens where it is not open. */
async function inspectorOf(page: Page, name: string): Promise<Locator> {
  const inspect = page.getByRole("button", {
    name: en["canvas.inspector"],
    exact: true,
  });
  if ((await inspect.getAttribute("aria-pressed")) !== "true") {
    await press(inspect);
  }
  const panel = page.getByRole(
    test.info().project.use.hasTouch ? "dialog" : "region",
    { name },
  );
  await expect(panel).toBeVisible();
  return panel;
}

/** Returns what the API has of the connection of id, of Harbour's project. */
async function connectionOf(page: Page, harbour: Harbour, id: string) {
  const res = await page.request.get(
    `/api/v1/projects/${harbour.projectId}/connections/${id}`,
  );
  expect(res.status(), await res.text()).toBe(200);
  return (await res.json()) as {
    condition?: { source: string; ast?: unknown; is_valid: boolean };
    deleted_at?: string;
  };
}

/** Returns the middle of locator's box on the page. */
async function middleOf(locator: Locator) {
  const box = await locator.boundingBox();
  if (box === null) {
    throw new Error("the element is not shown");
  }
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

/** A connection of Quay's, of the condition given. */
function choiceOf(
  h: Harbour,
  id: string,
  to: string,
  condition?: string,
): Command {
  return {
    type: "connection.create",
    payload: {
      id,
      from_node_id: h.quay,
      to_node_id: to,
      ...(condition === undefined ? {} : { condition }),
    },
  };
}

/** A node of Harbour, of the title given, at x, y. */
function nodeOf(
  h: Harbour,
  id: string,
  title: string,
  x: number,
  y: number,
): Command {
  return {
    type: "node.create",
    payload: {
      id,
      board_id: h.board,
      type: "element",
      title,
      position: { x, y },
    },
  };
}

test.describe("the touch-emulation suite", () => {
  // Verifies: FR-EDIT-09/AC1
  test("creates a node, edits its text, connects it, sets the connection's condition, deletes the node, and undoes that, with no mouse", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch !== true, "fingers alone");
    // Every press that reaches the page is a finger's: the page records
    // the kind of each pointer that goes down.
    await page.addInitScript(() => {
      const kinds: string[] = [];
      (window as unknown as { pointerKinds: string[] }).pointerKinds = kinds;
      window.addEventListener(
        "pointerdown",
        (e) => kinds.push(e.pointerType),
        true,
      );
    });
    // Quay, and Lamp, far from it, to which the board is fitted, so that
    // the empty board between them is in view.
    const harbour = await makeHarbour(page, (h) => [
      nodeOf(h, newId("node"), "Lamp", 720, 320),
    ]);
    const canvas = await openHarbour(page, harbour);
    await page
      .getByRole("button", { name: en["canvas.fit"], exact: true })
      .tap();
    await settled(canvas);
    const sent = sentBatches(page);
    // Create a node: a finger held on the empty board opens its menu, whose
    // Add an element here adds one where the finger was.
    const menu = page.getByRole("menu", { name: en["canvas.menu.board"] });
    await holdFinger(page, await pointOf(canvas, { x: 600, y: 64 }), () =>
      expect(menu).toBeVisible(),
    );
    await menu
      .getByRole("menuitem", { name: en["canvas.menu.add_element"] })
      .tap();
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      { type: "node.create", payload: { type: "element" } },
    ]);
    const dock = (sent[0][0].payload as { id: string }).id;
    await expect(canvas).toHaveAttribute("data-nodes", "3");
    await expect(canvas).toHaveAttribute("data-selected", "1");
    // Edit its text: a second tap on the node selected opens its text.
    const at = await pointOf(canvas, { x: 600, y: 64 });
    await page.touchscreen.tap(at.x, at.y);
    await page.touchscreen.tap(at.x, at.y);
    const untitled = en["canvas.node.untitled"];
    const editor = page.getByRole("form", {
      name: message("canvas.text_editor", { node: untitled }),
    });
    await expect(editor).toBeVisible();
    await editor
      .getByRole("textbox", {
        name: message("canvas.text_of", { node: untitled }),
      })
      .tap();
    await page.keyboard.type("The dock is quiet.");
    await editor.getByRole("button", { name: en["canvas.text_save"] }).tap();
    await expect(editor).toBeHidden();
    await expect.poll(() => sent.length).toBe(2);
    expect(sent[1]).toMatchObject([
      { type: "node.update", payload: { id: dock } },
    ]);
    // Create a connection: a tap selects Quay, and a finger drags its
    // port's handle to the new node.
    const quay = await pointOf(canvas, { x: 120, y: 64 });
    await page.touchscreen.tap(quay.x, quay.y);
    const handle = page.locator('[data-link="new"]');
    await expect(handle).toBeVisible();
    await dragOn(
      page,
      await middleOf(handle),
      await pointOf(canvas, { x: 600, y: 64 }),
    );
    await expect.poll(() => sent.length).toBe(3);
    expect(sent[2]).toMatchObject([
      {
        type: "connection.create",
        payload: { from_node_id: harbour.quay, to_node_id: dock },
      },
    ]);
    const choice = (sent[2][0].payload as { id: string }).id;
    await expect(canvas).toHaveAttribute("data-connections", "1");
    // Set its condition: a tap on its line selects it, and its inspector's
    // condition takes what the on-screen keyboard types, and a tap on what
    // the editor offers writes it.
    const line = await pointOf(canvas, { x: 360, y: 64 });
    await page.touchscreen.tap(line.x, line.y);
    await expect(canvas).toHaveAttribute("data-connection", choice);
    const panel = await inspectorOf(page, en["inspector.connection"]);
    const condition = panel.getByRole("textbox", {
      name: en["inspector.condition"],
    });
    await condition.tap();
    await page.keyboard.type("met");
    const offer = panel
      .getByRole("listbox", { name: en["expr.complete.label"] })
      .getByRole("option", { name: /^met_elara/ });
    await offer.tap();
    await expect(condition).toHaveValue("met_elara");
    await expect(panel.getByText("is true", { exact: false })).toBeVisible();
    await panel
      .getByRole("button", { name: en["inspector.save_connection"] })
      .tap();
    await expect.poll(() => sent.length).toBe(4);
    expect(sent[3]).toMatchObject([
      {
        type: "connection.update",
        payload: { id: choice, condition: "met_elara" },
      },
    ]);
    await expect
      .poll(
        async () =>
          (await connectionOf(page, harbour, choice)).condition?.is_valid,
      )
      .toBe(true);
    await panel.getByRole("button", { name: en["panel.close"] }).tap();
    await expect(panel).toBeHidden();
    // Delete the node: a finger held on it opens its menu, whose Delete
    // takes it, and its connection with it, to the trash.
    const nodeMenu = page.getByRole("menu").filter({
      has: page.getByRole("menuitem", { name: en["canvas.delete"] }),
    });
    await holdFinger(page, at, () => expect(nodeMenu).toBeVisible());
    await nodeMenu.getByRole("menuitem", { name: en["canvas.delete"] }).tap();
    await expect.poll(() => sent.length).toBe(5);
    expect(sent[4]).toMatchObject([
      { type: "node.delete", payload: { id: dock } },
    ]);
    await expect(canvas).toHaveAttribute("data-nodes", "2");
    await expect(canvas).toHaveAttribute("data-connections", "0");
    // Undo: the toolbar's Undo brings the node back, and its connection,
    // with its condition.
    await page
      .getByRole("button", { name: en["history.undo"], exact: true })
      .tap();
    await expect(canvas).toHaveAttribute("data-nodes", "3");
    await expect(canvas).toHaveAttribute("data-connections", "1");
    await expect
      .poll(async () => (await connectionOf(page, harbour, choice)).deleted_at)
      .toBeUndefined();
    expect((await connectionOf(page, harbour, choice)).condition?.source).toBe(
      "met_elara",
    );
    // And no mouse went down at any point.
    const kinds = await page.evaluate(
      () => (window as unknown as { pointerKinds: string[] }).pointerKinds,
    );
    expect(kinds.length).toBeGreaterThan(0);
    expect(new Set(kinds)).toEqual(new Set(["touch"]));
  });
});

test.describe("the expression editor", () => {
  // Verifies: FR-DEP-01/AC3
  test("shows where a variable that a condition names is used in one press, and opens its panel in the next", async ({
    page,
  }) => {
    const dock = newId("node");
    const choice = newId("connection");
    const harbour = await makeHarbour(page, (h) => [
      nodeOf(h, dock, "Dock", 480, 0),
      choiceOf(h, choice, dock, "gold >= 3"),
    ]);
    const canvas = await openHarbour(page, harbour, harbour.quay);
    const node = await inspectorOf(page, "Quay");
    await press(
      node.getByRole("button", {
        name: message("inspector.choice_edit", { place: "1" }),
      }),
    );
    await expect(canvas).toHaveAttribute("data-connection", choice);
    const panel = await inspectorOf(page, en["inspector.connection"]);
    const names = panel.getByRole("list", { name: en["expr.names.heading"] });
    await expect(names).toContainText(
      message("expr.names.global_variable", { type: en["expr.type.int"] }),
    );
    // One press: where gold is used, as its panel lists it.
    await press(
      names.getByRole("button", {
        name: message("expr.names.show_usages", { name: "gold" }),
      }),
    );
    await expect(
      names.getByRole("link", { name: "The condition of a choice from Quay" }),
    ).toBeVisible();
    // The next: its panel, on the variables' page.
    await press(
      names.getByRole("link", {
        name: message("expr.names.open_variable", { name: "gold" }),
      }),
    );
    await expect(page).toHaveURL(
      new RegExp(`/variables\\?variable=${harbour.gold}$`),
    );
    await expect(
      page.getByRole(test.info().project.use.hasTouch ? "dialog" : "region", {
        name: "gold",
      }),
    ).toBeVisible();
  });

  // Verifies: FR-EXPR-01/AC3
  test("saves a condition built in the guided picker, which the server compiles to the AST of the one typed", async ({
    page,
  }) => {
    const dock = newId("node");
    const typed = newId("connection");
    const picked = newId("connection");
    const harbour = await makeHarbour(page, (h) => [
      nodeOf(h, dock, "Dock", 480, 0),
      choiceOf(h, typed, dock, "gold >= 3"),
      choiceOf(h, picked, dock),
    ]);
    const canvas = await openHarbour(page, harbour, harbour.quay);
    const node = await inspectorOf(page, "Quay");
    await press(
      node.getByRole("button", {
        name: message("inspector.choice_edit", { place: "2" }),
      }),
    );
    await expect(canvas).toHaveAttribute("data-connection", picked);
    const panel = await inspectorOf(page, en["inspector.connection"]);
    const sent = sentBatches(page);
    await press(panel.getByRole("button", { name: en["expr.view.guided"] }));
    const picker = panel.getByRole("group", {
      name: en["inspector.condition"],
    });
    await picker
      .getByRole("combobox", { name: en["expr.picker.target_label"] })
      .selectOption({
        label: message("expr.picker.target", {
          name: "gold",
          type: en["expr.type.int"],
        }),
      });
    await picker
      .getByRole("combobox", { name: en["expr.picker.test"], exact: true })
      .selectOption({ label: en["expr.picker.gte"] });
    const value = picker.getByRole("textbox", {
      name: en["expr.picker.value"],
    });
    await value.fill("3");
    expect(await violations(page)).toEqual([]);
    if (test.info().project.use.hasTouch) {
      expect(await smallTargets(page)).toEqual([]);
    }
    // The text view shows what the picker wrote.
    await press(panel.getByRole("button", { name: en["expr.view.text"] }));
    await expect(
      panel.getByRole("textbox", { name: en["inspector.condition"] }),
    ).toHaveValue("gold >= 3");
    await press(
      panel.getByRole("button", { name: en["inspector.save_connection"] }),
    );
    await expect.poll(() => sent.length).toBe(1);
    await expect
      .poll(
        async () =>
          (await connectionOf(page, harbour, picked)).condition?.is_valid,
      )
      .toBe(true);
    expect((await connectionOf(page, harbour, picked)).condition?.ast).toEqual(
      (await connectionOf(page, harbour, typed)).condition?.ast,
    );
  });

  // Verifies: FR-EXPR-01/AC1, FR-EXPR-01/AC2, NFR-A11Y-08, NFR-TOUCH-01
  test("marks an error at the parser's range, offers the board's names, and passes axe's checks with its list open, of a fingertip's targets", async ({
    page,
  }) => {
    const dock = newId("node");
    const choice = newId("connection");
    const harbour = await makeHarbour(page, (h) => [
      nodeOf(h, dock, "Dock", 480, 0),
      choiceOf(h, choice, dock),
    ]);
    const canvas = await openHarbour(page, harbour, harbour.quay);
    const node = await inspectorOf(page, "Quay");
    await press(
      node.getByRole("button", {
        name: message("inspector.choice_edit", { place: "1" }),
      }),
    );
    await expect(canvas).toHaveAttribute("data-connection", choice);
    const panel = await inspectorOf(page, en["inspector.connection"]);
    const condition = panel.getByRole("textbox", {
      name: en["inspector.condition"],
    });
    await press(condition);
    await page.keyboard.type("gld > 1");
    await expect(panel.locator(".expr-field [data-error]")).toHaveText("gld");
    await expect(condition).toHaveAttribute("aria-invalid", "true");
    await expect(
      panel.getByText(
        message("expr.error.at_suggest", {
          problem: en["expr.error.unknown_identifier"],
          place: message("expr.place.chars", { first: "1", last: "3" }),
          suggestions: "gold",
        }),
      ),
    ).toBeVisible();
    // What it offers, of the names of the board.
    await page.keyboard.press("Control+A");
    await page.keyboard.type("met_elara and go");
    const list = panel.getByRole("listbox", {
      name: en["expr.complete.label"],
    });
    await expect(list.getByRole("option")).toHaveCount(1);
    await expect(list.getByRole("option")).toContainText("gold");
    expect(await violations(page)).toEqual([]);
    // A finger's targets, of the tablet, where the selected connection's
    // handles are of a finger's size too (plan note N69).
    if (test.info().project.use.hasTouch) {
      expect(await smallTargets(page)).toEqual([]);
    }
  });
});
