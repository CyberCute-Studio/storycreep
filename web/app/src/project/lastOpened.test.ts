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

/** Returns the value of the last-board cookie, or undefined if the browser has none. */
function cookie(): string | undefined {
  return document.cookie
    .split("; ")
    .find((pair) => pair.startsWith("sc_last_board="))
    ?.slice("sc_last_board=".length);
}

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
