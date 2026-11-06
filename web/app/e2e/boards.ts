// What the end-to-end tests of the canvas, and its benchmark, start from: an
// account signed in, a project, and boards of many nodes, made through the
// API, and how they read what the canvas shows, which PixiJS draws as
// pixels, from the data attributes of its element (src/canvas/controller.ts).

import { randomUUID } from "node:crypto";
import type {
  APIRequestContext,
  CDPSession,
  Locator,
  Page,
} from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { createProject, firstWorkspace, sessionOf } from "./content.ts";
import {
  type ApiSession,
  atStart,
  expect,
  message,
  registerAccount,
  signIn,
} from "./fixtures.ts";

/** Registers an account, signs the page in as it, and returns the page's session. */
export async function signedIn(page: Page, name: string): Promise<ApiSession> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return sessionOf(page.request);
}

/** A command of a batch, without its ID, which apply gives it. */
export interface Command {
  type: string;
  payload: Record<string, unknown>;
}

/** The most commands that the API takes in one batch. */
const batchSize = 1000;

/** Applies commands to projectId through the API, as request's account, in batches as large as the API takes. */
export async function apply(
  request: APIRequestContext,
  session: ApiSession,
  projectId: string,
  commands: Command[],
): Promise<void> {
  for (let i = 0; i < commands.length; i += batchSize) {
    const res = await request.post(`/api/v1/projects/${projectId}/commands`, {
      headers: { "X-CSRF-Token": session.csrf },
      data: {
        commands: commands
          .slice(i, i + batchSize)
          .map((c) => ({ command_id: randomUUID(), ...c })),
      },
      timeout: 120_000,
    });
    expect(res.status(), await res.text()).toBe(200);
  }
}

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
 * animation of a zoom or a fit, which a test that reads it waits for.
 */
export async function settled(canvas: Locator): Promise<View> {
  let last = await viewOf(canvas);
  for (let tries = 0; tries < 20; tries++) {
    await canvas.page().waitForTimeout(300);
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
