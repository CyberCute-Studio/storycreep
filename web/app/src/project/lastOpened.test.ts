import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CanvasView } from "../canvas/controller";
import {
  forgetLastOpened,
  lastOpened,
  rememberBoard,
  rememberView,
  useLastOpened,
} from "./lastOpened";

// The last-opened state (PRD FR-PROJ-04): what the device last showed each
// account, in the browser's storage, and the board alone in the cookie that
// the server reads (server/internal/httpserver/landing.go).

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** Returns the value of the last-board cookie of the name name, or undefined if the browser has none. */
function cookie(name = "sc_last_board"): string | undefined {
  return document.cookie
    .split("; ")
    .find((pair) => pair.startsWith(`${name}=`))
    ?.slice(name.length + 1);
}

/** The JSDOM of the tests, which Vitest's environment gives them. */
const dom = (
  globalThis as unknown as {
    jsdom: { reconfigure(options: { url: string }): void };
  }
).jsdom;

/** Returns what a canvas of 800 by 600 shows at x, 20, and zoom, with the nodes of selection selected. */
function shown(x: number, zoom = 1, selection: string[] = []): CanvasView {
  return {
    viewport: { x, y: 20, zoom },
    size: { width: 800, height: 600 },
    selection: new Set(selection),
  };
}

describe("the last-opened state", () => {
  it("keeps the board and what its canvas showed for each account of the device, and the board in the server's cookie", () => {
    rememberBoard("usr_a", "prj_a", "bd_a");
    rememberView("usr_a", "prj_a", "bd_a", shown(10, 2, ["nd_1", "nd_2"]));
    rememberBoard("usr_b", "prj_b", "bd_b");
    expect(lastOpened("usr_a")).toEqual({
      projectId: "prj_a",
      boardId: "bd_a",
      view: {
        viewport: { x: 10, y: 20, zoom: 2 },
        size: { width: 800, height: 600 },
        selection: ["nd_1", "nd_2"],
      },
    });
    expect(lastOpened("usr_b")).toEqual({
      projectId: "prj_b",
      boardId: "bd_b",
    });
    expect(lastOpened("usr_c")).toBeUndefined();
    // The most recent account's first.
    expect(cookie()).toBe("usr_b:prj_b:bd_b.usr_a:prj_a:bd_a");
  });

  it("keeps the view of the board that the device shows again, and drops it once the device shows another", () => {
    rememberView("usr_a", "prj_a", "bd_a", shown(10));
    rememberBoard("usr_a", "prj_a", "bd_a");
    expect(lastOpened("usr_a")?.view?.viewport).toEqual({
      x: 10,
      y: 20,
      zoom: 1,
    });
    rememberBoard("usr_a", "prj_a", "bd_b");
    expect(lastOpened("usr_a")).toEqual({
      projectId: "prj_a",
      boardId: "bd_b",
    });
    expect(cookie()).toBe("usr_a:prj_a:bd_b");
  });

  it("writes the cookie anew each time that a board's page shows the board, so that it lasts from then, but not as the canvas's view changes", () => {
    rememberBoard("usr_a", "prj_a", "bd_a");
    const writes = vi.spyOn(Document.prototype, "cookie", "set");
    // Safari keeps a cookie that a script writes for 7 days at most.
    rememberBoard("usr_a", "prj_a", "bd_a");
    expect(writes).toHaveBeenCalledTimes(1);
    expect(writes.mock.calls[0][0]).toContain(`Max-Age=${400 * 24 * 60 * 60}`);
    rememberView("usr_a", "prj_a", "bd_a", shown(10));
    expect(writes).toHaveBeenCalledTimes(1);
    expect(cookie()).toBe("usr_a:prj_a:bd_a");
  });

  it("names the cookie __Host- on an https page, as the session's, so that one that another host of the domain planted hides nothing of it", () => {
    const was = location.href;
    dom.reconfigure({ url: "https://localhost:3000/" });
    try {
      // As another host of the domain would plant it for the whole domain.
      // biome-ignore lint/suspicious/noDocumentCookie: jsdom has no Cookie Store API, and the app writes its cookie so.
      document.cookie = "sc_last_board=usr_a:prj_x:bd_x; Path=/";
      rememberBoard("usr_a", "prj_a", "bd_a");
      expect(cookie("__Host-sc_last_board")).toBe("usr_a:prj_a:bd_a");
      rememberBoard("usr_b", "prj_b", "bd_b");
      expect(cookie("__Host-sc_last_board")).toBe(
        "usr_b:prj_b:bd_b.usr_a:prj_a:bd_a",
      );
      forgetLastOpened("usr_a", "bd_x");
      expect(cookie("__Host-sc_last_board")).toBe(
        "usr_b:prj_b:bd_b.usr_a:prj_a:bd_a",
      );
      forgetLastOpened("usr_a", "bd_a");
      forgetLastOpened("usr_b", "bd_b");
      // Removed, as only a Secure cookie of the path / may be.
      expect(cookie("__Host-sc_last_board")).toBeUndefined();
      expect(cookie()).toBe("usr_a:prj_x:bd_x");
    } finally {
      dom.reconfigure({ url: was });
    }
  });

  it("keeps in the cookie the boards of the five accounts that last used the browser", () => {
    for (const n of [1, 2, 3, 4, 5, 6]) {
      rememberBoard(`usr_${n}`, "prj_a", `bd_${n}`);
    }
    expect(cookie()).toBe(
      "usr_6:prj_a:bd_6.usr_5:prj_a:bd_5.usr_4:prj_a:bd_4.usr_3:prj_a:bd_3.usr_2:prj_a:bd_2",
    );
    // An account that comes back comes first again, once.
    rememberBoard("usr_3", "prj_a", "bd_7");
    expect(cookie()).toBe(
      "usr_3:prj_a:bd_7.usr_6:prj_a:bd_6.usr_5:prj_a:bd_5.usr_4:prj_a:bd_4.usr_2:prj_a:bd_2",
    );
  });

  // Verifies: FR-PROJ-04/AC2
  it("reads nothing that it did not write, and keeps nothing where the browser's storage throws, without throwing", () => {
    for (const kept of [
      "not json",
      "null",
      "[]",
      JSON.stringify({ projectId: "prj_a" }),
      JSON.stringify({ projectId: "prj:a", boardId: "bd_a" }),
      JSON.stringify({ projectId: 1, boardId: "bd_a" }),
    ]) {
      localStorage.setItem("storycreep.last.usr_a", kept);
      expect(lastOpened("usr_a"), kept).toBeUndefined();
    }
    // A view that is none is left out, and the board kept.
    for (const view of [
      { viewport: { x: 0, y: 0, zoom: 9 }, size: { width: 1, height: 1 } },
      { viewport: { x: 0, y: 0, zoom: 1 }, size: { width: 0, height: 1 } },
      { viewport: { x: "0", y: 0, zoom: 1 }, size: { width: 1, height: 1 } },
      { viewport: null, size: { width: 1, height: 1 } },
    ]) {
      localStorage.setItem(
        "storycreep.last.usr_a",
        JSON.stringify({ projectId: "prj_a", boardId: "bd_a", view }),
      );
      expect(lastOpened("usr_a")).toEqual({
        projectId: "prj_a",
        boardId: "bd_a",
      });
    }
    // A selection keeps the IDs alone.
    localStorage.setItem(
      "storycreep.last.usr_a",
      JSON.stringify({
        projectId: "prj_a",
        boardId: "bd_a",
        view: {
          viewport: { x: 0, y: 0, zoom: 1 },
          size: { width: 1, height: 1 },
          selection: ["nd_1", 2, "nd:2"],
        },
      }),
    );
    expect(lastOpened("usr_a")?.view?.selection).toEqual(["nd_1"]);

    // Nor does it keep what is no ID, which could end the cookie's value.
    localStorage.clear();
    rememberBoard("usr_a", "prj_a; Domain=example.com", "bd_a");
    rememberView("usr_a", "prj_a", "bd:a", shown(1));
    expect(lastOpened("usr_a")).toBeUndefined();
    expect(cookie()).toBeUndefined();

    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied", "QuotaExceededError");
    });
    expect(lastOpened("usr_a")).toBeUndefined();
    expect(() => rememberBoard("usr_a", "prj_a", "bd_a")).not.toThrow();
    expect(() =>
      rememberView("usr_a", "prj_a", "bd_a", shown(1)),
    ).not.toThrow();
  });

  it("forgets the board that it no longer finds, and the cookie's entry of the account, but no other board", () => {
    rememberBoard("usr_b", "prj_b", "bd_b");
    rememberView("usr_a", "prj_a", "bd_a", shown(1));
    forgetLastOpened("usr_a", "bd_other");
    expect(lastOpened("usr_a")?.boardId).toBe("bd_a");
    expect(cookie()).toBe("usr_a:prj_a:bd_a.usr_b:prj_b:bd_b");
    forgetLastOpened("usr_a", "bd_a");
    expect(lastOpened("usr_a")).toBeUndefined();
    expect(lastOpened("usr_b")?.boardId).toBe("bd_b");
    expect(cookie()).toBe("usr_b:prj_b:bd_b");
    forgetLastOpened("usr_b", "bd_b");
    expect(cookie()).toBeUndefined();
  });

  it("gives a board's page what the device last showed of the board, and keeps what its canvas shows twice a second at most, and as the page is hidden or left", () => {
    vi.useFakeTimers();
    rememberView("usr_a", "prj_a", "bd_a", shown(10, 2));
    const { result, rerender, unmount } = renderHook(
      ({ board }) => useLastOpened("usr_a", "prj_a", board),
      { initialProps: { board: "bd_a" } },
    );
    expect(result.current.initial?.viewport).toEqual({ x: 10, y: 20, zoom: 2 });
    const keptX = () => lastOpened("usr_a")?.view?.viewport.x;
    act(() => result.current.onView(shown(11)));
    act(() => result.current.onView(shown(12)));
    expect(keptX()).toBe(10);
    act(() => vi.advanceTimersByTime(500));
    expect(keptX()).toBe(12);
    // Hidden, as a tablet's browser may end the page without a word.
    act(() => result.current.onView(shown(13)));
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(keptX()).toBe(13);
    act(() => result.current.onView(shown(14)));
    act(() => {
      window.dispatchEvent(new Event("pagehide"));
    });
    expect(keptX()).toBe(14);

    // Another board's page reads nothing of the one before, and keeps the
    // last view of the one before as it goes.
    act(() => result.current.onView(shown(15)));
    rerender({ board: "bd_b" });
    expect(lastOpened("usr_a")?.boardId).toBe("bd_b");
    expect(result.current.initial).toBeUndefined();
    act(() => result.current.onView(shown(16, 1, ["nd_1"])));
    unmount();
    expect(lastOpened("usr_a")).toEqual({
      projectId: "prj_a",
      boardId: "bd_b",
      view: {
        viewport: { x: 16, y: 20, zoom: 1 },
        size: { width: 800, height: 600 },
        selection: ["nd_1"],
      },
    });
  });

  it("gives a canvas mounted again on the page what the canvas before it showed last, rather than what the page read as it opened", () => {
    vi.useFakeTimers();
    rememberView("usr_a", "prj_a", "bd_a", shown(10, 2));
    const { result, rerender, unmount } = renderHook(() =>
      useLastOpened("usr_a", "prj_a", "bd_a"),
    );
    expect(result.current.initial?.viewport.x).toBe(10);
    act(() => result.current.onView(shown(11, 1, ["nd_1"])));
    // The page shown anew, as once it shows its script view in place of
    // the canvas, before the view was kept.
    rerender();
    expect(result.current.initial).toEqual({
      viewport: { x: 11, y: 20, zoom: 1 },
      size: { width: 800, height: 600 },
      selection: ["nd_1"],
    });
    expect(lastOpened("usr_a")?.view?.viewport.x).toBe(10);
    unmount();
  });

  it("gives a canvas mounted again once the session is of another account what that account's device showed it, rather than what the page showed the account before", () => {
    vi.useFakeTimers();
    rememberView("usr_d", "prj_a", "bd_a", shown(30));
    const { result, rerender, unmount } = renderHook(
      ({ user }) => useLastOpened(user, "prj_a", "bd_a"),
      { initialProps: { user: "usr_a" } },
    );
    act(() => result.current.onView(shown(11, 1, ["nd_1"])));
    rerender({ user: "usr_d" });
    expect(result.current.initial?.viewport.x).toBe(30);
    expect(result.current.initial?.selection).toEqual([]);
    unmount();
  });

  it("forgets the board that its page deleted, and keeps nothing more of it as the page is left", () => {
    vi.useFakeTimers();
    rememberBoard("usr_b", "prj_b", "bd_b");
    const { result, unmount } = renderHook(() =>
      useLastOpened("usr_a", "prj_a", "bd_a"),
    );
    expect(lastOpened("usr_a")?.boardId).toBe("bd_a");
    act(() => result.current.onView(shown(10)));
    act(() => result.current.forget());
    expect(lastOpened("usr_a")).toBeUndefined();
    act(() => result.current.onView(shown(11)));
    act(() => vi.advanceTimersByTime(1000));
    act(() => {
      window.dispatchEvent(new Event("pagehide"));
    });
    unmount();
    expect(lastOpened("usr_a")).toBeUndefined();
    expect(cookie()).toBe("usr_b:prj_b:bd_b");
  });

  it("reads and keeps nothing without an account, as while the canvas's benchmark runs", () => {
    vi.useFakeTimers();
    rememberView("usr_a", "prj_a", "bd_a", shown(10));
    const { result, unmount } = renderHook(() =>
      useLastOpened(undefined, "prj_a", "bd_b"),
    );
    expect(result.current.initial).toBeUndefined();
    act(() => result.current.onView(shown(20)));
    act(() => vi.advanceTimersByTime(1000));
    unmount();
    expect(lastOpened("usr_a")?.boardId).toBe("bd_a");
    expect(lastOpened("usr_a")?.view?.viewport.x).toBe(10);
  });
});
