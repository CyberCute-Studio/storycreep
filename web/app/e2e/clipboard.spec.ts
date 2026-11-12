import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  dragOn,
  drawn,
  makeProject,
  pointOf,
  settled,
  test,
} from "./boards.ts";
import { apply, createProject, firstWorkspace, signedIn } from "./content.ts";
import {
  type ApiSession,
  en,
  expect,
  message,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// The canvas's clipboard (PRD FR-EDIT-07, plan note N71), on the desktop
// with a mouse and the keys, and on the tablet with fingers: the selected
// nodes, copied, and pasted onto another board of the project, or into
// another project of the workspace, through the app's own links, as the
// page keeps the clipboard in its memory, keep their places relative to
// each other and the order of their connections, and what the nodes name
// that the other project lacks is listed, and created, before the paste.

/** What a test's project holds: Harbour, of Quay, Dock, and Lamp, and the empty Lighthouse. */
interface Shore {
  projectId: string;
  session: ApiSession;
  harbour: string;
  lighthouse: string;
  quay: string;
  dock: string;
  lamp: string;
  trust: string;
  elara: string;
}

/** Where Harbour's elements are, each 240 by 128. */
const places = {
  quay: { x: 0, y: 0 },
  dock: { x: 480, y: 0 },
  lamp: { x: 480, y: 320 },
};

/**
 * Makes the project of a test, as an account that the page signs in as:
 * Harbour's Quay names the global variable trust and the component Elara,
 * whom its text mentions and whose trust it shows, and its script raises
 * trust; Quay's port leads to Dock and then to Lamp, and Dock's to Lamp.
 */
async function makeShore(page: Page): Promise<Shore> {
  const session = await signedIn(page, "Coral Copier");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const shore: Shore = {
    projectId,
    session,
    harbour: newId("board"),
    lighthouse: newId("board"),
    quay: newId("node"),
    dock: newId("node"),
    lamp: newId("node"),
    trust: newId("variable"),
    elara: newId("component"),
  };
  const person = newId("component_type");
  const element = (
    id: string,
    title: string,
    at: { x: number; y: number },
  ) => ({
    type: "node.create",
    payload: {
      id,
      board_id: shore.harbour,
      type: "element",
      title,
      position: at,
    },
  });
  const connect = (from: string, to: string) => ({
    type: "connection.create",
    payload: { id: newId("connection"), from_node_id: from, to_node_id: to },
  });
  const quay = element(shore.quay, "Quay", places.quay);
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: shore.harbour, name: "Harbour" } },
    {
      type: "board.create",
      payload: { id: shore.lighthouse, name: "Lighthouse" },
    },
    {
      type: "variable.create",
      payload: {
        id: shore.trust,
        key: "trust",
        type: "int",
        scope: "global",
        default: 1,
      },
    },
    {
      type: "component_type.create",
      payload: { id: person, name: "Person", key: "person" },
    },
    {
      type: "component.create",
      payload: {
        id: shore.elara,
        component_type_id: person,
        name: "Elara",
        key: "elara",
      },
    },
    {
      ...quay,
      payload: {
        ...quay.payload,
        on_enter: "trust += 1",
        text: {
          type: "doc",
          content: [
            {
              type: "paragraph",
              content: [
                { type: "text", text: "You have " },
                { type: "valueRef", attrs: { ref: shore.trust } },
                { type: "text", text: " trust with " },
                { type: "mention", attrs: { id: shore.elara } },
              ],
            },
          ],
        },
      },
    },
    element(shore.dock, "Dock", places.dock),
    element(shore.lamp, "Lamp", places.lamp),
    connect(shore.quay, shore.dock),
    connect(shore.quay, shore.lamp),
    connect(shore.dock, shore.lamp),
  ]);
  return shore;
}

/** Returns what the API lists at path of the project of projectId. */
async function listed<T>(
  page: Page,
  projectId: string,
  path: string,
): Promise<T[]> {
  const res = await page.request.get(`/api/v1/projects/${projectId}/${path}`);
  expect(res.status(), await res.text()).toBe(200);
  return ((await res.json()) as { items: T[] }).items;
}

/** A node as the API lists it, of what the tests read. */
interface ListedNode {
  id: string;
  title: string;
  position: { x: number; y: number };
  on_enter?: { source: string; is_valid?: boolean };
  text?: unknown;
}

/** A connection as the API lists it, of what the tests read. */
interface ListedConnection {
  id: string;
  from_node_id: string;
  from_port: string;
  to_node_id: string;
  order: string;
}

/** Returns the titles of the nodes that each node's port leads to, in the order of its choices, of the connections of a board. */
function choicesOf(
  nodes: ListedNode[],
  connections: ListedConnection[],
): Record<string, string[]> {
  const titles = new Map(nodes.map((n) => [n.id, n.title]));
  const out: Record<string, string[]> = {};
  const sorted = [...connections].sort((a, b) =>
    a.order === b.order
      ? a.id.slice(3) < b.id.slice(3)
        ? -1
        : 1
      : a.order < b.order
        ? -1
        : 1,
  );
  for (const c of sorted) {
    const from = titles.get(c.from_node_id) ?? c.from_node_id;
    out[from] = [
      ...(out[from] ?? []),
      titles.get(c.to_node_id) ?? c.to_node_id,
    ];
  }
  return out;
}

/** Opens Harbour with every node in view, selected by the select mode's rubber band, and returns its canvas. */
async function openSelected(page: Page, shore: Shore): Promise<Locator> {
  await page.goto(`/app/projects/${shore.projectId}/boards/${shore.harbour}`);
  const canvas = canvasOf(page, "Harbour");
  await drawn(canvas);
  await press(
    page.getByRole("button", { name: en["canvas.fit"], exact: true }),
  );
  await settled(canvas);
  await press(
    page.getByRole("button", { name: en["canvas.mode_select"], exact: true }),
  );
  await dragOn(
    page,
    await pointOf(canvas, { x: -40, y: -40 }),
    await pointOf(canvas, { x: 760, y: 488 }),
  );
  await expect(canvas).toHaveAttribute("data-selected", "3");
  return canvas;
}

/**
 * What the toolbar says of a clipboard of three nodes, as English's plural
 * of canvas.on_clipboard has it, which message, of a message's arguments
 * alone, does not format.
 */
const threeOnClipboard = "3 nodes on the clipboard";

/** Returns the toolbar's button named name of the page's clipboard. */
function clipboardButton(page: Page, name: string): Locator {
  return page
    .getByRole("group", { name: en["canvas.clipboard"] })
    .getByRole("button", { name, exact: true });
}

/** Opens the board named board of the project shown, by the project's links, as a user does, which keeps the page and its clipboard. */
async function openByLinks(page: Page, board: string): Promise<Locator> {
  await press(
    page.getByRole("link", { name: en["project.nav.boards"], exact: true }),
  );
  await press(page.getByRole("link", { name: board, exact: true }));
  const canvas = canvasOf(page, board);
  await drawn(canvas);
  return canvas;
}

test.describe("a board's canvas, copying and pasting its nodes", () => {
  // Verifies: FR-EDIT-07/AC2
  test("pastes the selected nodes onto another board, keeping their places relative to each other and the order of their connections", async ({
    page,
  }) => {
    const shore = await makeShore(page);
    await openSelected(page, shore);
    await press(clipboardButton(page, en["canvas.copy"]));
    await expect(page.getByText(threeOnClipboard)).toBeVisible();

    const lighthouse = await openByLinks(page, "Lighthouse");
    await expect(page.getByText(threeOnClipboard)).toBeVisible();
    await press(clipboardButton(page, en["canvas.paste"]));
    // The pasted nodes, selected once the board has been read again.
    await expect(lighthouse).toHaveAttribute("data-nodes", "3");
    await expect(lighthouse).toHaveAttribute("data-selected", "3");
    expect(await violations(page)).toEqual([]);
    if (test.info().project.use.hasTouch) {
      expect(await smallTargets(page)).toEqual([]);
    }

    const pasted = await listed<ListedNode>(
      page,
      shore.projectId,
      `boards/${shore.lighthouse}/nodes`,
    );
    const at = new Map(pasted.map((n) => [n.title, n.position]));
    expect([...at.keys()].sort()).toEqual(["Dock", "Lamp", "Quay"]);
    const quay = at.get("Quay") ?? { x: NaN, y: NaN };
    const offset = { x: quay.x - places.quay.x, y: quay.y - places.quay.y };
    expect(at.get("Dock")).toEqual({
      x: places.dock.x + offset.x,
      y: places.dock.y + offset.y,
    });
    expect(at.get("Lamp")).toEqual({
      x: places.lamp.x + offset.x,
      y: places.lamp.y + offset.y,
    });
    // New nodes, of the same connections, in the same order.
    expect(pasted.map((n) => n.id)).not.toContain(shore.quay);
    expect(
      choicesOf(
        pasted,
        await listed<ListedConnection>(
          page,
          shore.projectId,
          `boards/${shore.lighthouse}/connections`,
        ),
      ),
    ).toEqual({ Quay: ["Dock", "Lamp"], Dock: ["Lamp"] });
    // And Harbour is as it was.
    expect(
      await listed<ListedNode>(
        page,
        shore.projectId,
        `boards/${shore.harbour}/nodes`,
      ),
    ).toHaveLength(3);
  });

  // Verifies: FR-EDIT-07/AC1
  test("reports what the nodes name that another project of the workspace lacks, and creates it before the paste, which then names it", async ({
    page,
  }) => {
    // The keys on the desktop, and the toolbar's buttons on the tablet.
    const touch = test.info().project.use.hasTouch === true;
    const shore = await makeShore(page);
    const workspace = await firstWorkspace(page.request);
    const cove = await createProject(
      page.request,
      shore.session,
      workspace.id,
      "Cove",
    );
    const beach = newId("board");
    await apply(page.request, shore.session, cove, [
      { type: "board.create", payload: { id: beach, name: "Beach" } },
    ]);
    const canvas = await openSelected(page, shore);
    if (touch) {
      await press(clipboardButton(page, en["canvas.copy"]));
    } else {
      await canvas.focus();
      await page.keyboard.press("ControlOrMeta+c");
    }
    await expect(page.getByText(threeOnClipboard)).toBeVisible();

    // Into Cove, by the workspace's and the project's links.
    await press(
      page.getByRole("link", {
        name: message("project.back", { workspace: workspace.name }),
      }),
    );
    await press(page.getByRole("link", { name: "Cove", exact: true }));
    await press(page.getByRole("link", { name: "Beach", exact: true }));
    const target = canvasOf(page, "Beach");
    await drawn(target);
    if (touch) {
      await press(clipboardButton(page, en["canvas.paste"]));
    } else {
      await target.focus();
      await page.keyboard.press("ControlOrMeta+v");
    }
    const dialog = page.getByRole("dialog", { name: en["paste.title"] });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(/^trust: global/)).toBeVisible();
    await expect(dialog.getByText(/^Elara \(elara\)/)).toBeVisible();
    expect(await violations(page)).toEqual([]);
    if (touch) {
      expect(await smallTargets(page)).toEqual([]);
    }
    await press(dialog.getByRole("button", { name: en["paste.create"] }));
    await expect(dialog).toBeHidden();
    await expect(target).toHaveAttribute("data-nodes", "3");
    // The focus is back where the paste was asked for.
    await expect(
      touch ? clipboardButton(page, en["canvas.paste"]) : target,
    ).toBeFocused();

    const variables = await listed<{ id: string; key: string }>(
      page,
      cove,
      "variables",
    );
    const components = await listed<{ id: string; key: string }>(
      page,
      cove,
      "components",
    );
    const trust = variables.find((v) => v.key === "trust");
    const elara = components.find((c) => c.key === "elara");
    expect(trust).toBeDefined();
    expect(elara).toBeDefined();
    const quay = (
      await listed<ListedNode>(page, cove, `boards/${beach}/nodes`)
    ).find((n) => n.title === "Quay");
    // The script names the variable created, valid, and the text names the
    // variable and the component created, not Lighthouse's.
    expect(quay?.on_enter?.source).toBe("trust += 1");
    expect(quay?.on_enter?.is_valid).toBe(true);
    const text = JSON.stringify(quay?.text);
    expect(text).toContain(trust?.id);
    expect(text).toContain(elara?.id);
    expect(text).not.toContain(shore.trust);
    expect(text).not.toContain(shore.elara);
  });
});
