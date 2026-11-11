import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  dragOn,
  drawn,
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
  violations,
} from "./fixtures.ts";

// The canvas's connections, branch outputs, and jumpers (PRD FR-EDIT-03,
// FR-EDIT-04, FR-EDIT-05, FR-EDIT-09, plan note N69), on the desktop with a
// mouse and on the tablet with fingers alone: a drag of a port's handle
// connects two nodes, and one dropped on the empty board offers to create a
// node there and connect it, which cancelling leaves nothing of; a tap on a
// connection selects it, and a drag of its end's handle reconnects it; the
// inspector adds, reorders, and removes a branch's outputs; and a jumper's
// link opens its target, and a jumper whose target was deleted is drawn as
// broken.

/** What a test's project holds: the board Harbour, of two elements, a branch, and a jumper, and the board Lighthouse, of one element. */
interface Flow {
  projectId: string;
  session: ApiSession;
  harbour: string;
  lighthouse: string;
  quay: string;
  dock: string;
  fork: string;
  onward: string;
  lamp: string;
}

/** Where Harbour's nodes are, each 240 wide: the elements 128 high, the branch 96, and the jumper 72. */
const places = {
  quay: { x: 0, y: 0 },
  dock: { x: 480, y: 0 },
  fork: { x: 0, y: 320 },
  onward: { x: 480, y: 320 },
};

/** Makes the project of a test, as an account that the page signs in as. */
async function makeFlow(page: Page): Promise<Flow> {
  const session = await signedIn(page, "Edda Editor");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const flow: Flow = {
    projectId,
    session,
    harbour: newId("board"),
    lighthouse: newId("board"),
    quay: newId("node"),
    dock: newId("node"),
    fork: newId("node"),
    onward: newId("node"),
    lamp: newId("node"),
  };
  const node = (
    id: string,
    boardId: string,
    type: string,
    title: string,
    position: { x: number; y: number },
  ) => ({
    type: "node.create",
    payload: { id, board_id: boardId, type, title, position },
  });
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: flow.harbour, name: "Harbour" } },
    {
      type: "board.create",
      payload: { id: flow.lighthouse, name: "Lighthouse" },
    },
    node(flow.quay, flow.harbour, "element", "Quay", places.quay),
    node(flow.dock, flow.harbour, "element", "Dock", places.dock),
    node(flow.fork, flow.harbour, "branch", "Fork", places.fork),
    node(flow.onward, flow.harbour, "jumper", "Onward", places.onward),
    node(flow.lamp, flow.lighthouse, "element", "Lamp", { x: 0, y: 0 }),
  ]);
  return flow;
}

/** Opens Harbour on the node of id, selected, at 100%, and returns its canvas once it has drawn the board. */
async function openOn(page: Page, flow: Flow, id: string): Promise<Locator> {
  await page.goto(
    `/app/projects/${flow.projectId}/boards/${flow.harbour}?node=${id}`,
  );
  const canvas = canvasOf(page, "Harbour");
  await drawn(canvas);
  await expect(canvas).toHaveAttribute("data-selected", "1");
  return canvas;
}

/**
 * Opens Harbour on the node of id, as openOn does, and fits the board to
 * the canvas, so that every node of it is in view, on the tablet's narrower
 * canvas too, which shows the board below 100% then.
 */
async function openFitted(
  page: Page,
  flow: Flow,
  id: string,
): Promise<Locator> {
  const canvas = await openOn(page, flow, id);
  await press(
    page.getByRole("button", { name: en["canvas.fit"], exact: true }),
  );
  await settled(canvas);
  return canvas;
}

/** Returns the middle of locator's box on the page. */
async function middleOf(locator: Locator) {
  const box = await locator.boundingBox();
  if (box === null) {
    throw new Error("the element is not shown");
  }
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

/** Returns what the API lists at path of the project of flow. */
async function listed<T>(page: Page, flow: Flow, path: string): Promise<T[]> {
  const res = await page.request.get(
    `/api/v1/projects/${flow.projectId}/${path}`,
  );
  expect(res.status(), await res.text()).toBe(200);
  return ((await res.json()) as { items: T[] }).items;
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

test.describe("a board's canvas, connecting its nodes", () => {
  test("connects the node selected to another by a drag of its port's handle, of a fingertip's size on a touch screen, with one command", async ({
    page,
  }) => {
    const flow = await makeFlow(page);
    const canvas = await openFitted(page, flow, flow.quay);
    const sent = sentBatches(page);
    const handle = page.locator('[data-link="new"]');
    await expect(handle).toBeVisible();
    // The handle is explicit, and larger on a device with a touch screen
    // (FR-EDIT-09).
    const box = await handle.boundingBox();
    expect(box?.width).toBeCloseTo(
      test.info().project.use.hasTouch ? 44 : 24,
      1,
    );
    await dragOn(
      page,
      await middleOf(handle),
      await pointOf(canvas, { x: 600, y: 64 }),
    );
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      {
        type: "connection.create",
        payload: {
          from_node_id: flow.quay,
          from_port: "out",
          to_node_id: flow.dock,
        },
      },
    ]);
    await expect(canvas).toHaveAttribute("data-connections", "1");
    await expect
      .poll(
        async () =>
          (await listed(page, flow, `boards/${flow.harbour}/connections`))
            .length,
      )
      .toBe(1);
  });

  // Verifies: FR-EDIT-03/AC3
  test("offers to create a node and connect it where a connection is dropped on the empty board, and leaves nothing behind once cancelled", async ({
    page,
  }) => {
    const flow = await makeFlow(page);
    const canvas = await openFitted(page, flow, flow.quay);
    const sent = sentBatches(page);
    const handle = page.locator('[data-link="new"]');
    await expect(handle).toBeVisible();
    const empty = { x: 360, y: 220 };
    await dragOn(page, await middleOf(handle), await pointOf(canvas, empty));
    const offer = page.getByRole("dialog", { name: en["canvas.connect_new"] });
    await expect(offer).toBeVisible();
    // Cancelled, by a tap right after the drag, which the browser takes for
    // no fling's end, nothing was sent, and the board is as it was.
    await press(
      offer.getByRole("button", { name: en["canvas.connect_cancel"] }),
    );
    await expect(offer).toBeHidden();
    expect(sent).toEqual([]);
    expect(
      await listed(page, flow, `boards/${flow.harbour}/nodes`),
    ).toHaveLength(4);
    expect(
      await listed(page, flow, `boards/${flow.harbour}/connections`),
    ).toHaveLength(0);
    await expect(canvas).toHaveAttribute("data-nodes", "4");
    // Chosen, the node and its connection are one batch.
    await expect(handle).toBeVisible();
    await dragOn(page, await middleOf(handle), await pointOf(canvas, empty));
    await expect(offer).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(offer.getByRole("button", { name: en["canvas.add_element"] }));
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0].map((c) => c.type)).toEqual([
      "node.create",
      "connection.create",
    ]);
    const created = (sent[0][0].payload as { id: string }).id;
    expect(sent[0][1].payload).toMatchObject({
      from_node_id: flow.quay,
      to_node_id: created,
    });
    await expect(canvas).toHaveAttribute("data-nodes", "5");
    await expect(canvas).toHaveAttribute("data-connections", "1");
    await expect
      .poll(
        async () =>
          (await listed(page, flow, `boards/${flow.harbour}/nodes`)).length,
      )
      .toBe(5);
    await expect
      .poll(
        async () =>
          (await listed(page, flow, `boards/${flow.harbour}/connections`))
            .length,
      )
      .toBe(1);
  });

  test("selects a connection by a tap on its line, reconnects it by a drag of its end's handle, and deletes it", async ({
    page,
  }) => {
    const flow = await makeFlow(page);
    const connection = newId("connection");
    await apply(page.request, flow.session, flow.projectId, [
      {
        type: "connection.create",
        payload: {
          id: connection,
          from_node_id: flow.quay,
          to_node_id: flow.dock,
        },
      },
    ]);
    const canvas = await openFitted(page, flow, flow.quay);
    const sent = sentBatches(page);
    // The connection goes level from 240, 64 to 480, 64.
    const line = await pointOf(canvas, { x: 360, y: 64 });
    if (test.info().project.use.hasTouch) {
      await page.touchscreen.tap(line.x, line.y);
    } else {
      await page.mouse.click(line.x, line.y);
    }
    await expect(canvas).toHaveAttribute("data-connection", connection);
    await expect(canvas).toHaveAttribute("data-selected", "0");
    const end = page.locator('[data-link="to"]');
    await expect(end).toBeVisible();
    // To the jumper, which a connection may enter.
    await dragOn(
      page,
      await middleOf(end),
      await pointOf(canvas, { x: 600, y: 356 }),
    );
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toMatchObject([
      {
        type: "connection.reconnect",
        payload: { id: connection, to_node_id: flow.onward },
      },
    ]);
    await expect(canvas).toHaveAttribute("data-connection", connection);
    await press(
      page.getByRole("button", { name: en["canvas.delete"], exact: true }),
    );
    await expect.poll(() => sent.length).toBe(2);
    expect(sent[1]).toMatchObject([
      { type: "connection.delete", payload: { id: connection } },
    ]);
    await expect(canvas).toHaveAttribute("data-connections", "0");
    await expect(canvas).toHaveAttribute("data-connection", "");
  });
});

test.describe("a board's canvas, its branches and jumpers", () => {
  test("adds a branch's outputs and its else, reorders them, and removes one, in the inspector", async ({
    page,
  }) => {
    const flow = await makeFlow(page);
    await openOn(page, flow, flow.fork);
    const panel = await inspectorOf(page, "Fork");
    const outputs = async () =>
      (
        (await (
          await page.request.get(
            `/api/v1/projects/${flow.projectId}/nodes/${flow.fork}`,
          )
        ).json()) as { outputs?: { id: string; kind: string }[] | null }
      ).outputs ?? [];
    const add = panel.getByRole("button", { name: en["inspector.add_output"] });
    await press(add);
    await expect.poll(async () => (await outputs()).length).toBe(1);
    await press(add);
    await expect.poll(async () => (await outputs()).length).toBe(2);
    await press(panel.getByRole("button", { name: en["inspector.add_else"] }));
    await expect
      .poll(async () => (await outputs()).map((o) => o.kind))
      .toEqual(["if", "elseif", "else"]);
    const [first, second] = await outputs();
    expect(await violations(page)).toEqual([]);
    // The second conditional output goes first, and is the branch's if.
    await press(
      panel
        .locator(`[data-output="${second.id}"]`)
        .getByRole("button", { name: /↑|up/i })
        .first(),
    );
    await expect
      .poll(async () => (await outputs()).map((o) => o.id).slice(0, 2))
      .toEqual([second.id, first.id]);
    expect((await outputs())[0].kind).toBe("if");
    // An output that no connection leaves is removed at once.
    await press(
      panel
        .locator(`[data-output="${first.id}"]`)
        .getByRole("button", { name: en["inspector.remove"] }),
    );
    await expect
      .poll(async () => (await outputs()).map((o) => o.kind))
      .toEqual(["if", "else"]);
  });

  // Verifies: FR-EDIT-05/AC1
  test("gives a jumper its target, opens the target by the jumper's link, and draws the jumper as broken once its target is deleted", async ({
    page,
  }) => {
    const flow = await makeFlow(page);
    const canvas = await openOn(page, flow, flow.onward);
    const panel = await inspectorOf(page, "Onward");
    await panel
      .getByLabel(en["inspector.target_board"], { exact: true })
      .selectOption({ label: "Lighthouse" });
    const node = panel.getByLabel(en["inspector.target_node"], { exact: true });
    await expect(node.locator(`option[value="${flow.lamp}"]`)).toBeAttached();
    await node.selectOption(flow.lamp);
    await press(
      panel.getByRole("button", { name: en["inspector.save_target"] }),
    );
    await expect(
      panel.getByText(
        message("canvas.node.jumper_to_on", {
          target: "Lamp",
          board: "Lighthouse",
        }),
      ),
    ).toBeVisible();
    await expect(canvas).toHaveAttribute("data-broken", "0");
    expect(await violations(page)).toEqual([]);
    // A sheet, on the tablet, covers the canvas: closed, the jumper's link
    // is above its card, and one tap of it opens the target.
    if (test.info().project.use.hasTouch) {
      await press(panel.getByRole("button", { name: en["panel.close"] }));
    }
    const link = page
      .getByRole("link", { name: en["canvas.go_to_target"] })
      .last();
    await press(link);
    await expect(page).toHaveURL(
      new RegExp(`/boards/${flow.lighthouse}\\?node=${flow.lamp}`),
    );
    const there = canvasOf(page, "Lighthouse");
    await drawn(there);
    await expect(there).toHaveAttribute("data-selected", "1");
    // The target deleted, the jumper is drawn as broken, and has no link.
    await apply(page.request, flow.session, flow.projectId, [
      { type: "node.delete", payload: { id: flow.lamp } },
    ]);
    const again = await openOn(page, flow, flow.onward);
    await expect(again).toHaveAttribute("data-broken", "1");
    await expect(
      page.getByRole("link", { name: en["canvas.go_to_target"] }),
    ).toHaveCount(0);
  });
});
