// What the device last showed an account of its projects (PRD FR-PROJ-04,
// FR-PUB-05, plan note N67): the board, and what its canvas showed of it,
// the viewport and the selection, which the canvas opens with again. The
// browser's storage keeps it under the account's ID, so that it is the
// device's and the account's alone, and a cookie keeps the board, for the
// server, which sends a signed-in request to / or /login to it
// (server/internal/httpserver/landing.go), as the app's start page sends
// its own (lastBoard). It is a hint, never more: a board that is gone, or
// that the account may no longer read, opens the project list instead, and
// a browser whose storage throws, as in a private window, or holds what this
// code did not write, keeps nothing, rather than keep the app from starting.

import type { QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { notFoundBy } from "../api";
import type { CanvasView, SavedView } from "../canvas/controller";
import { maxZoom, minZoom } from "../canvas/viewport";
import type { Documents } from "../document/documents";
import { projectQuery } from "./queries";

/** What the device last showed an account: a board of a project, and what the canvas showed of it, once it had. */
export interface LastOpened {
  projectId: string;
  boardId: string;
  view?: SavedView;
}

/** Returns the key of the browser's storage that keeps what the device last showed the account userId. */
function storageKey(userId: string): string {
  return `storycreep.last.${userId}`;
}

/**
 * The name of the cookie that keeps, for the server, the board that the
 * browser last showed each of the accounts that last used it
 * (lastBoardCookie, in server/internal/httpserver/landing.go, which reads
 * it): at most cookieEntries entries, the most recent first, separated by
 * '.', each the IDs of the account, the project, and the board, separated
 * by ':'.
 */
const cookieName = "sc_last_board";

/** How many accounts the cookie keeps a board of (maxLastBoards, of the server). */
const cookieEntries = 5;

/** How long the cookie lasts, in seconds: 400 days, the most that browsers keep one. */
const cookieAge = 400 * 24 * 60 * 60;

/**
 * Reports whether id may be an ID of the server's: a prefix and a body of
 * letters, digits, and '_', which hold neither of the cookie's separators.
 */
function isId(id: unknown): id is string {
  return typeof id === "string" && /^[A-Za-z0-9_]{1,64}$/.test(id);
}

/** Reports whether n is a finite number, and greater than 0 if positive. */
function isNumber(n: unknown, positive = false): n is number {
  return typeof n === "number" && Number.isFinite(n) && (!positive || n > 0);
}

/** Returns the view that value holds, as savedOf writes it, or undefined for one that holds none. */
function viewOf(value: unknown): SavedView | undefined {
  if (typeof value !== "object" || value === null) {
    return undefined;
  }
  const { viewport, size, selection } = value as Record<string, unknown>;
  if (typeof viewport !== "object" || viewport === null) {
    return undefined;
  }
  if (typeof size !== "object" || size === null) {
    return undefined;
  }
  const { x, y, zoom } = viewport as Record<string, unknown>;
  const { width, height } = size as Record<string, unknown>;
  if (
    !isNumber(x) ||
    !isNumber(y) ||
    !isNumber(zoom) ||
    zoom < minZoom ||
    zoom > maxZoom ||
    !isNumber(width, true) ||
    !isNumber(height, true)
  ) {
    return undefined;
  }
  return {
    viewport: { x, y, zoom },
    size: { width, height },
    selection: Array.isArray(selection) ? selection.filter(isId) : [],
  };
}

/**
 * Returns what the device last showed the account userId, as the browser's
 * storage keeps it, or undefined if it keeps nothing of it that this code
 * wrote: the view alone is left out of a record whose view is not one.
 */
export function lastOpened(userId: string): LastOpened | undefined {
  let value: unknown;
  try {
    const kept = localStorage.getItem(storageKey(userId));
    value = kept === null ? undefined : JSON.parse(kept);
  } catch {
    return undefined;
  }
  if (typeof value !== "object" || value === null) {
    return undefined;
  }
  const { projectId, boardId, view } = value as Record<string, unknown>;
  if (!isId(projectId) || !isId(boardId)) {
    return undefined;
  }
  const saved = viewOf(view);
  return saved === undefined
    ? { projectId, boardId }
    : { projectId, boardId, view: saved };
}

/** Keeps last as what the device last showed the account userId, and its board in the cookie. */
function keep(userId: string, last: LastOpened): void {
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(last));
  } catch {
    // A browser that keeps nothing, as a private window's, opens the
    // project list next, as a device that never showed the board does.
  }
  writeCookie(userId, `${userId}:${last.projectId}:${last.boardId}`);
}

/**
 * Records that the device shows the account userId the board boardId of the
 * project projectId, which the start page and the server open next, with
 * what its canvas showed of it if the device last showed this board, and
 * without it if another.
 */
export function rememberBoard(
  userId: string,
  projectId: string,
  boardId: string,
): void {
  const last = lastOpened(userId);
  keep(
    userId,
    last?.boardId === boardId && last.view !== undefined
      ? { projectId, boardId, view: last.view }
      : { projectId, boardId },
  );
}

/** Records what the canvas of the board boardId of the project projectId shows the account userId. */
export function rememberView(
  userId: string,
  projectId: string,
  boardId: string,
  view: CanvasView,
): void {
  keep(userId, {
    projectId,
    boardId,
    view: {
      viewport: {
        x: view.viewport.x,
        y: view.viewport.y,
        zoom: view.viewport.zoom,
      },
      size: { width: view.size.width, height: view.size.height },
      selection: [...view.selection],
    },
  });
}

/**
 * Forgets that the device last showed the account userId the board boardId,
 * as one that it no longer finds: the browser's storage keeps nothing of it,
 * and the cookie no entry of the account's, if they named that board.
 */
export function forgetLastOpened(userId: string, boardId: string): void {
  if (lastOpened(userId)?.boardId === boardId) {
    try {
      localStorage.removeItem(storageKey(userId));
    } catch {
      // A browser whose storage throws keeps nothing to forget.
    }
  }
  if (cookieEntriesOf().some((e) => isEntryOf(e, userId, boardId))) {
    writeCookie(userId, undefined);
  }
}

/** Reports whether entry is the account userId's entry of the cookie, and, if boardId is given, of that board. */
function isEntryOf(entry: string, userId: string, boardId?: string): boolean {
  const [user, , board] = entry.split(":");
  return user === userId && (boardId === undefined || board === boardId);
}

/** Returns the cookie's entries, as the browser has them, but for those that are not three IDs. */
function cookieEntriesOf(): string[] {
  let cookies: string;
  try {
    cookies = document.cookie;
  } catch {
    return [];
  }
  for (const pair of cookies.split(";")) {
    const at = pair.indexOf("=");
    if (at >= 0 && pair.slice(0, at).trim() === cookieName) {
      return pair
        .slice(at + 1)
        .trim()
        .split(".")
        .filter((e) => {
          const parts = e.split(":");
          return parts.length === 3 && parts.every(isId);
        });
    }
  }
  return [];
}

/**
 * Writes the cookie with entry, if it is not undefined, as the account
 * userId's, first, and the entries of the other accounts after it, the
 * most recent of them, or removes the cookie once it has none. The page's
 * own scheme says whether it is Secure, as an https page's is: the cookie
 * holds no secret, and goes to the page's own host alone.
 */
function writeCookie(userId: string, entry: string | undefined): void {
  const was = cookieEntriesOf();
  const others = was.filter((e) => !isEntryOf(e, userId));
  const entries = (entry === undefined ? others : [entry, ...others]).slice(
    0,
    cookieEntries,
  );
  if (entries.join(".") === was.join(".")) {
    return;
  }
  const secure = location.protocol === "https:" ? "; Secure" : "";
  const value =
    entries.length === 0
      ? `${cookieName}=; Path=/; Max-Age=0; SameSite=Lax${secure}`
      : `${cookieName}=${entries.join(".")}; Path=/; Max-Age=${cookieAge}; SameSite=Lax${secure}`;
  try {
    // biome-ignore lint/suspicious/noDocumentCookie: the Cookie Store API is asynchronous, and missing in browsers that the app supports, while the page writes the cookie as it leaves.
    document.cookie = value;
  } catch {
    // A page whose cookies are blocked opens the project list from /.
  }
}

/** What the start page reads the last board through: the page's query client and documents. */
interface StartContext {
  queryClient: QueryClient;
  documents: Documents;
}

/**
 * Returns the board that the device last showed the account userId, which
 * the start page opens (PRD FR-PROJ-04, FR-PUB-05), while the account may
 * still read it, or undefined: for none, as on another device, and for one
 * that is gone, as to the trash, or of a project that the account may no
 * longer read, which it forgets too, so that stale state never keeps the
 * app from starting (FR-PROJ-04 AC2). It reads what the board's page reads
 * first, the project and the tree of its boards, which that page then finds
 * read, and reads the project anew where the tree does not have the board,
 * as the page does, since it may be of a document that the real-time
 * channel did not keep current. A read that fails otherwise, as without the
 * network, leaves what the device keeps as it is, and opens the project
 * list, which says what fails.
 */
export async function lastBoard(
  { queryClient, documents }: StartContext,
  userId: string,
): Promise<{ projectId: string; boardId: string } | undefined> {
  const last = lastOpened(userId);
  if (last === undefined) {
    return undefined;
  }
  const { projectId, boardId } = last;
  try {
    await queryClient.ensureQueryData(projectQuery(projectId));
    const doc = documents.get(projectId);
    await doc.load("tree");
    const has = () => doc.store.getState().view.boards.has(boardId);
    if (!has()) {
      await doc.refresh();
    }
    if (has()) {
      return { projectId, boardId };
    }
  } catch (error) {
    if (!notFoundBy(error)) {
      return undefined;
    }
  }
  forgetLastOpened(userId, boardId);
  return undefined;
}

/** How long the canvas's view waits before it is kept, in milliseconds, at most, so that a pan writes the storage twice a second at most. */
const keepDelay = 500;

/**
 * What a board's page reads and keeps of the last-opened state: what the
 * device last showed the account userId of the board, if it did, which its
 * canvas opens with (initial), read as the page shows the board, before it
 * records that it shows it; and onView, which keeps what the canvas shows,
 * at most every keepDelay, and as the page is hidden or left, as a tablet's
 * browser may end a hidden page without a word. A userId of undefined, as
 * while the canvas's benchmark runs, whose runs start where they choose,
 * reads and keeps nothing.
 */
export function useLastOpened(
  userId: string | undefined,
  projectId: string,
  boardId: string,
): { initial: SavedView | undefined; onView: (view: CanvasView) => void } {
  const initial = useMemo(() => {
    if (userId === undefined) {
      return undefined;
    }
    const last = lastOpened(userId);
    return last?.boardId === boardId ? last.view : undefined;
  }, [userId, boardId]);
  const keeper = useRef<((view: CanvasView) => void) | undefined>(undefined);
  useEffect(() => {
    if (userId === undefined) {
      return;
    }
    rememberBoard(userId, projectId, boardId);
    let pending: CanvasView | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      clearTimeout(timer);
      timer = undefined;
      if (pending !== undefined) {
        rememberView(userId, projectId, boardId, pending);
        pending = undefined;
      }
    };
    keeper.current = (view) => {
      pending = view;
      timer ??= setTimeout(flush, keepDelay);
    };
    const hidden = () => {
      if (document.visibilityState === "hidden") {
        flush();
      }
    };
    window.addEventListener("pagehide", flush);
    document.addEventListener("visibilitychange", hidden);
    return () => {
      keeper.current = undefined;
      window.removeEventListener("pagehide", flush);
      document.removeEventListener("visibilitychange", hidden);
      flush();
    };
  }, [userId, projectId, boardId]);
  const onView = useCallback((view: CanvasView) => keeper.current?.(view), []);
  return { initial, onView };
}
