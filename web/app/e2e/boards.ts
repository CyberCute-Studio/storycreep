// What the end-to-end tests of the canvas, and its benchmark, start from: a
// project and boards of many nodes, made through the API, as an account
// signed in (content.ts), how they read what the canvas shows, which
// PixiJS draws as pixels, from the data attributes of its element
// (src/canvas/controller.ts), and the test that they run in, which fails
// on an error that the page threw and nothing caught.

import type {
  APIRequestContext,
  CDPSession,
  Locator,
  Page,
  Request,
} from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  apply,
  type Command,
  createProject,
  firstWorkspace,
} from "./content.ts";
import { type ApiSession, test as base, expect, message } from "./fixtures.ts";

/**
 * The test of fixtures.ts, which fails too on an error that the page threw
 * and nothing caught, as a frame of the canvas's throws of a bug of its
 * renderer: the canvas draws again in the next frame, whose data
 * attributes, which the tests read, would not tell of it. The tests of the
 * canvas and its benchmark run in it; those of the error console, which
 * throw such errors on purpose, do not. thrown holds those so far.
 */
export const test = base.extend<{ thrown: string[] }>({
  thrown: [
    async ({ page }, use) => {
      const thrown: string[] = [];
      page.on("pageerror", (error) => {
        thrown.push(error.stack ?? String(error));
      });
      await use(thrown);
      expect(thrown, "errors that the page threw and nothing caught").toEqual(
        [],
      );
    },
    { auto: true },
  ],
});

/** The words of the nodes' texts, of which each text takes some, by its node's place. */
const words =
  "the keeper climbs the stair and lights the lamp while the storm comes in from the sea and the bell rings twice".split(
    " ",
  );

/** Returns the text of the node at index i, of a sentence or two. */
function textOf(i: number) {
  const count = 8 + (i % 17);
  const sentence = Array.from(
    { length: count },
    (_, j) => words[(i * 7 + j * 3) % words.length],
  ).join(" ");
  return {
    type: "doc",
    content: [
      { type: "paragraph", content: [{ type: "text", text: `${sentence}.` }] },
    ],
  };
}

/** A board that a test made, with its nodes' IDs in their order. */
export interface TestBoard {
  boardId: string;
  name: string;
  nodeIds: string[];
  /** The columns of its grid. */
  cols: number;
}

/** How far right of the first grid a board's second grid is, in units of the board. */
export const farGrid = 40_000;

/**
 * Makes a board named name in projectId of count elements, each with a
 * title and a text, in a grid of cols columns 320 by 200 units apart, as
 * the fixtures lay theirs out, each connected to the next of its row, and
 * some to the one below it. What a node is follows its place in its grid
 * alone. A board of near nodes and more has its first near nodes in that
 * grid and the rest in a second grid, of the same columns, farGrid to its
 * right, beyond any view of the first: two boards of the same near show the
 * same nodes there, whatever their sizes, as FR-EDIT-11 AC1 compares them.
 */
export async function makeBoard(
  request: APIRequestContext,
  session: ApiSession,
  projectId: string,
  name: string,
  count: number,
  { cols = Math.ceil(Math.sqrt(count * 1.5)), near = count } = {},
): Promise<TestBoard> {
  const boardId = newId("board");
  const nodeIds = Array.from({ length: count }, () => newId("node"));
  /** Returns the grid of the node at index i, its first node's index, and its place in it. */
  const placeOf = (i: number) => {
    const first = i < near ? 0 : near;
    const j = i - first;
    return {
      first,
      col: j % cols,
      row: Math.floor(j / cols),
      end: i < near ? near : count,
    };
  };
  const commands: Command[] = [
    { type: "board.create", payload: { id: boardId, name } },
    ...nodeIds.map((id, i) => {
      const { first, col, row } = placeOf(i);
      const place = row * 1009 + col;
      return {
        type: "node.create",
        payload: {
          id,
          board_id: boardId,
          type: "element",
          kind: place % 3 === 0 ? "narration" : "dialogue",
          title: `Scene ${row + 1}.${col + 1}`,
          color: (["none", "blue", "green", "orange"] as const)[place % 4],
          position: {
            x: (first === 0 ? 0 : farGrid) + col * 320,
            y: row * 200,
          },
          text: textOf(place),
        },
      };
    }),
    ...nodeIds.flatMap((id, i) => {
      const { col, row, end } = placeOf(i);
      const link = (to: number) => ({
        type: "connection.create",
        payload: {
          id: newId("connection"),
          from_node_id: id,
          to_node_id: nodeIds[to],
        },
      });
      return [
        ...(col + 1 < cols && i + 1 < end ? [link(i + 1)] : []),
        ...((col + row) % 5 === 0 && i + cols < end ? [link(i + cols)] : []),
      ];
    }),
  ];
  await apply(request, session, projectId, commands);
  return { boardId, name, nodeIds, cols };
}

/** Makes a project of the account of session in its first workspace, and returns its ID. */
export async function makeProject(
  request: APIRequestContext,
  session: ApiSession,
  name: string,
): Promise<string> {
  const workspace = await firstWorkspace(request);
  return createProject(request, session, workspace.id, name);
}

/** Returns the canvas of the board named board. */
export function canvasOf(page: Page, board: string): Locator {
  return page.getByRole("application", {
    name: message("canvas.label", { board }),
  });
}

/** What the canvas shows: the board's point at its top-left corner, and the zoom. */
export interface View {
  x: number;
  y: number;
  zoom: number;
}

/** Returns what canvas shows, as its data attributes tell. */
export async function viewOf(canvas: Locator): Promise<View> {
  const [x, y, zoom] = await Promise.all(
    ["data-x", "data-y", "data-zoom"].map(async (a) =>
      Number(await canvas.getAttribute(a)),
    ),
  );
  return { x, y, zoom };
}

/**
 * Returns what canvas shows once it has stopped moving, as after the
 * animation of a zoom or a fit, which a test that reads it waits for: the
 * same view twice, 300 ms and two of the page's frames apart, as the canvas
 * writes its view as it draws a frame, and a page that drew none meanwhile,
 * as one that the machine kept waiting in the middle of an animation, would
 * else show the view of its last frame twice, and a test take what the
 * canvas places over its nodes, as a handle, where it no longer is.
 */
export async function settled(canvas: Locator): Promise<View> {
  let last = await viewOf(canvas);
  for (let tries = 0; tries < 20; tries++) {
    await canvas.page().waitForTimeout(300);
    await canvas.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    const now = await viewOf(canvas);
    if (now.x === last.x && now.y === last.y && now.zoom === last.zoom) {
      return now;
    }
    last = now;
  }
  throw new Error("the canvas did not stop moving");
}

/** Waits until canvas has drawn its board's content. */
export async function drawn(canvas: Locator): Promise<void> {
  await expect(canvas).toHaveAttribute("data-ready", "true", {
    timeout: 30_000,
  });
  await expect(canvas).toHaveAttribute(
    "data-renderer",
    /^(webgl|webgpu|canvas)$/,
  );
}

/** Returns where on the page the board's point p shows in canvas at v. */
export async function onPage(
  canvas: Locator,
  v: View,
  p: { x: number; y: number },
): Promise<{ x: number; y: number }> {
  const box = await canvas.boundingBox();
  if (box === null) {
    throw new Error("the canvas is not shown");
  }
  return { x: box.x + (p.x - v.x) * v.zoom, y: box.y + (p.y - v.y) * v.zoom };
}

/** A touch point, on the page, of a finger. */
interface Finger {
  id: number;
  x: number;
  y: number;
}

/** Moves fingers, as a touch screen does, from their first points to their last, in steps, through Chromium's DevTools protocol. */
export async function touchGesture(
  cdp: CDPSession,
  paths: { from: { x: number; y: number }; to: { x: number; y: number } }[],
  steps = 10,
): Promise<void> {
  const at = (t: number): Finger[] =>
    paths.map((p, id) => ({
      id,
      x: p.from.x + (p.to.x - p.from.x) * t,
      y: p.from.y + (p.to.y - p.from.y) * t,
    }));
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: at(0),
  });
  for (let step = 1; step <= steps; step++) {
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: at(step / steps),
    });
  }
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
}

/** Returns where the board's point p is on the page, in canvas as it is now. */
export async function pointOf(canvas: Locator, p: { x: number; y: number }) {
  return onPage(canvas, await settled(canvas), p);
}

/** Drags from one point of the page to another: a mouse's on the desktop, and a finger's on the tablet. */
export async function dragOn(
  page: Page,
  from: { x: number; y: number },
  to: { x: number; y: number },
) {
  if (test.info().project.use.hasTouch) {
    // A session of the browser's protocol for each drag, which ends with it,
    // rather than one more left open on the page for each drag of a test.
    const cdp = await page.context().newCDPSession(page);
    try {
      await touchGesture(cdp, [{ from, to }], 12);
    } finally {
      await cdp.detach();
    }
    return;
  }
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
}

/** Returns the commands of the batches that page sends from now on, as it sends them. */
export function sentBatches(
  page: Page,
): { type: string; payload: unknown }[][] {
  const sent: { type: string; payload: unknown }[][] = [];
  page.on("request", (request: Request) => {
    if (request.method() === "POST" && request.url().endsWith("/commands")) {
      sent.push(
        (
          request.postDataJSON() as {
            commands: { type: string; payload: unknown }[];
          }
        ).commands,
      );
    }
  });
  return sent;
}

/**
 * Holds a finger still at p of the page, as a touch screen does, through
 * Chromium's DevTools protocol, until held resolves, as once the page shows
 * what a long press opens, or for the time that it resolves after, and then
 * lifts it.
 */
export async function holdFinger(
  page: Page,
  p: { x: number; y: number },
  held: () => Promise<unknown>,
): Promise<void> {
  const cdp = await page.context().newCDPSession(page);
  try {
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ id: 0, x: p.x, y: p.y }],
    });
    try {
      await held();
    } finally {
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchEnd",
        touchPoints: [],
      });
    }
  } finally {
    await cdp.detach();
  }
}
