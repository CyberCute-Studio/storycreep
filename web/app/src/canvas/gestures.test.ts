import { describe, expect, it } from "vitest";
import {
  type GestureHost,
  Gestures,
  type Mode,
  type PointerInput,
  type SelectHow,
} from "./gestures";
import { type Rect, toScreen, toWorld, type Viewport } from "./viewport";

/** A canvas of 800 by 600 pixels, of one node at 100, 100, 240 by 120. */
function host(mode: Mode = "pan") {
  const state = {
    v: { x: 0, y: 0, zoom: 1 } as Viewport,
    mode,
    selected: [] as string[],
    selects: [] as { ids: string[]; how: SelectHow }[],
    bands: [] as (Rect | undefined)[],
  };
  const node = { x: 100, y: 100, width: 240, height: 120 };
  const inNode = (r: Rect) =>
    r.x <= node.x + node.width &&
    node.x <= r.x + r.width &&
    r.y <= node.y + node.height &&
    node.y <= r.y + r.height;
  const h: GestureHost = {
    viewport: () => state.v,
    size: () => ({ width: 800, height: 600 }),
    setViewport: (v) => {
      state.v = v;
    },
    mode: () => state.mode,
    nodeAt: (p, slop) =>
      inNode({
        x: p.x - slop,
        y: p.y - slop,
        width: 2 * slop,
        height: 2 * slop,
      })
        ? "nd_a"
        : undefined,
    nodesIn: (r) => (inNode(r) ? ["nd_a"] : []),
    select: (ids, how) => {
      state.selects.push({ ids, how });
    },
    band: (r) => {
      state.bands.push(r);
    },
  };
  return { state, gestures: new Gestures(h) };
}

function touch(id: number, x: number, y: number): PointerInput {
  return { id, x, y, kind: "touch", button: 0, shift: false, additive: false };
}

function mouse(
  x: number,
  y: number,
  extra: Partial<PointerInput> = {},
): PointerInput {
  return {
    id: 1,
    x,
    y,
    kind: "mouse",
    button: 0,
    shift: false,
    additive: false,
    ...extra,
  };
}

describe("the canvas's gestures", () => {
  // Verifies: FR-EDIT-01/AC1
  it("keeps the point between two pinching fingers under them, within 2 px", () => {
    const { state, gestures } = host();
    state.v = { x: 37.5, y: -12, zoom: 0.8 };
    // The fingers start around 400, 300, and spread apart, and turn, about it.
    const mid = { x: 400, y: 300 };
    const anchor = toWorld(state.v, mid);
    gestures.down(touch(1, 350, 300));
    gestures.down(touch(2, 450, 300));
    for (let step = 1; step <= 20; step++) {
      const r = 50 + step * 12;
      const angle = step * 0.05;
      gestures.move(
        touch(1, mid.x - r * Math.cos(angle), mid.y - r * Math.sin(angle)),
      );
      gestures.move(
        touch(2, mid.x + r * Math.cos(angle), mid.y + r * Math.sin(angle)),
      );
      const shown = toScreen(state.v, anchor);
      expect(Math.hypot(shown.x - mid.x, shown.y - mid.y)).toBeLessThan(2);
    }
    expect(state.v.zoom).toBeGreaterThan(2.5);
  });

  // Verifies: FR-EDIT-01/AC1
  it("keeps the point between the fingers under them as they pinch and move together", () => {
    const { state, gestures } = host();
    const anchor = toWorld(state.v, { x: 300, y: 300 });
    gestures.down(touch(1, 200, 300));
    gestures.down(touch(2, 400, 300));
    // Closer together, and both 100 px to the right and 50 down.
    gestures.move(touch(1, 350, 350));
    gestures.move(touch(2, 450, 350));
    const shown = toScreen(state.v, anchor);
    expect(Math.hypot(shown.x - 400, shown.y - 350)).toBeLessThan(2);
    expect(state.v.zoom).toBeCloseTo(0.5);
  });

  it("holds the point where the zoom stops at its range", () => {
    const { state, gestures } = host();
    state.v = { x: 0, y: 0, zoom: 3.5 };
    const anchor = toWorld(state.v, { x: 400, y: 300 });
    gestures.down(touch(1, 390, 300));
    gestures.down(touch(2, 410, 300));
    gestures.move(touch(1, 100, 300));
    gestures.move(touch(2, 700, 300));
    expect(state.v.zoom).toBe(4);
    const shown = toScreen(state.v, anchor);
    expect(Math.hypot(shown.x - 400, shown.y - 300)).toBeLessThan(2);
  });

  // Verifies: NFR-TOUCH-07
  it("ignores a third finger", () => {
    const { state, gestures } = host();
    expect(gestures.down(touch(1, 300, 300))).toBe(true);
    expect(gestures.down(touch(2, 500, 300))).toBe(true);
    expect(gestures.down(touch(3, 400, 100))).toBe(false);
    const before = state.v;
    gestures.move(touch(3, 0, 0));
    expect(state.v).toBe(before);
  });

  // Verifies: NFR-TOUCH-03
  it("pans with one finger by default, and selects in a rectangle in the select mode", () => {
    const pan = host("pan");
    pan.gestures.down(touch(1, 500, 500));
    pan.gestures.move(touch(1, 520, 500));
    pan.gestures.move(touch(1, 600, 450));
    pan.gestures.up(touch(1, 600, 450));
    expect(pan.state.v).toEqual({ x: -100, y: 50, zoom: 1 });
    expect(pan.state.selects).toEqual([]);

    const select = host("select");
    select.gestures.down(touch(1, 50, 50));
    select.gestures.move(touch(1, 200, 160));
    expect(select.state.bands.at(-1)).toEqual({
      x: 50,
      y: 50,
      width: 150,
      height: 110,
    });
    select.gestures.up(touch(1, 200, 160));
    expect(select.state.bands.at(-1)).toBeUndefined();
    expect(select.state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(select.state.selects).toEqual([{ ids: ["nd_a"], how: "replace" }]);
  });

  it("pans in the select mode with the middle button, or with Space held", () => {
    const { state, gestures } = host("select");
    gestures.down(mouse(500, 500, { button: 1 }));
    gestures.move(mouse(540, 500, { button: 1 }));
    gestures.up(mouse(540, 500, { button: 1 }));
    expect(state.v.x).toBe(-40);
    gestures.setSpace(true);
    gestures.down(mouse(500, 500));
    gestures.move(mouse(500, 560));
    gestures.up(mouse(500, 560));
    expect(state.v.y).toBe(-60);
    expect(state.bands).toEqual([]);
  });

  it("does not take a finger's small move for a drag, nor its tap for a pan", () => {
    const { state, gestures } = host();
    gestures.down(touch(1, 200, 150));
    gestures.move(touch(1, 205, 154));
    gestures.up(touch(1, 205, 154));
    expect(state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(state.selects).toEqual([{ ids: ["nd_a"], how: "replace" }]);
  });

  it("selects the node that a tap or a click finds, adds it with Shift, and clears the selection on the empty canvas", () => {
    const { state, gestures } = host();
    gestures.down(mouse(150, 150));
    gestures.up(mouse(150, 150));
    gestures.down(mouse(150, 150, { shift: true }));
    gestures.up(mouse(150, 150, { shift: true }));
    gestures.down(mouse(700, 500));
    gestures.up(mouse(700, 500));
    gestures.down(mouse(700, 500, { additive: true }));
    gestures.up(mouse(700, 500, { additive: true }));
    expect(state.selects).toEqual([
      { ids: ["nd_a"], how: "replace" },
      { ids: ["nd_a"], how: "toggle" },
      { ids: [], how: "replace" },
    ]);
  });

  // Verifies: FR-EDIT-01/AC2
  it("needs no right click: the mouse's other buttons do nothing", () => {
    const { state, gestures } = host();
    expect(gestures.down(mouse(100, 100, { button: 2 }))).toBe(false);
    gestures.move(mouse(300, 300, { button: 2 }));
    gestures.up(mouse(300, 300, { button: 2 }));
    expect(state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(state.selects).toEqual([]);
  });

  it("goes on panning with the finger that stays once the other lifts", () => {
    const { state, gestures } = host();
    gestures.down(touch(1, 200, 300));
    gestures.down(touch(2, 400, 300));
    gestures.up(touch(2, 400, 300));
    const before = state.v;
    gestures.move(touch(1, 260, 300));
    expect(state.v.x).toBeCloseTo(before.x - 60 / before.zoom);
    gestures.up(touch(1, 260, 300));
    expect(gestures.active).toBe(false);
  });

  it("pans with the wheel, sideways with Shift, and zooms about the pointer with Ctrl or a trackpad's pinch", () => {
    const { state, gestures } = host();
    gestures.wheel({
      x: 0,
      y: 0,
      dx: 0,
      dy: 100,
      deltaMode: 0,
      zoom: false,
      shift: false,
    });
    expect(state.v).toEqual({ x: 0, y: 100, zoom: 1 });
    gestures.wheel({
      x: 0,
      y: 0,
      dx: 0,
      dy: 3,
      deltaMode: 1,
      zoom: false,
      shift: true,
    });
    expect(state.v).toEqual({ x: 48, y: 100, zoom: 1 });
    const at = { x: 400, y: 300 };
    const anchor = toWorld(state.v, at);
    gestures.wheel({
      ...at,
      dx: 0,
      dy: -10,
      deltaMode: 0,
      zoom: true,
      shift: false,
    });
    expect(state.v.zoom).toBeCloseTo(Math.exp(0.1));
    const shown = toScreen(state.v, anchor);
    expect(shown.x).toBeCloseTo(400);
    expect(shown.y).toBeCloseTo(300);
    // A mouse's notch zooms by a step, not to the end of the range.
    gestures.wheel({
      ...at,
      dx: 0,
      dy: 1000,
      deltaMode: 0,
      zoom: true,
      shift: false,
    });
    expect(state.v.zoom).toBeCloseTo(Math.exp(0.1) * Math.exp(-0.25));
  });

  it("zooms with Safari's trackpad pinch", () => {
    const { state, gestures } = host();
    gestures.scale({ x: 0, y: 0 }, 1, 1.5);
    gestures.scale({ x: 0, y: 0 }, 1.5, 3);
    expect(state.v.zoom).toBeCloseTo(3);
  });

  it("leaves the gesture that Safari sends of an iPad's pinch to the fingers' own", () => {
    const { state, gestures } = host();
    gestures.down(touch(1, 300, 300));
    gestures.down(touch(2, 500, 300));
    gestures.move(touch(2, 700, 300));
    // The fingers moved apart from 200 px to 400: twice the zoom, once.
    gestures.scale({ x: 500, y: 300 }, 1, 2);
    expect(state.v.zoom).toBeCloseTo(2);
    gestures.up(touch(1, 300, 300));
    gestures.up(touch(2, 700, 300));
    gestures.scale({ x: 500, y: 300 }, 2, 3);
    expect(state.v.zoom).toBeCloseTo(3);
  });
});
