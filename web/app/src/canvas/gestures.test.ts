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
    /** The rectangles of the board that a rubber band selected in. */
    banded: [] as Rect[],
    /** The pointers that the gestures gave back, as they no longer follow them. */
    released: [] as number[],
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
    nodesIn: (r) => {
      state.banded.push(r);
      return inNode(r) ? ["nd_a"] : [];
    },
    select: (ids, how) => {
      state.selects.push({ ids, how });
    },
    band: (r) => {
      state.bands.push(r);
    },
    release: (id) => {
      state.released.push(id);
    },
  };
  return { state, gestures: new Gestures(h) };
}

function touch(id: number, x: number, y: number): PointerInput {
  return {
    id,
    x,
    y,
    kind: "touch",
    button: 0,
    buttons: 1,
    shift: false,
    additive: false,
  };
}

/** The bit of the browser's `buttons` of each button that `button` names. */
const held: Record<number, number> = { 0: 1, 1: 4, 2: 2, 5: 32 };

/** A mouse's event, or a pen's, of its button held, unless extra says otherwise. */
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
    buttons: held[extra.button ?? 0],
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

  it("keeps a rubber band on the board's point where it started, as the wheel moves the board under it", () => {
    const { state, gestures } = host("select");
    gestures.down(mouse(50, 50));
    gestures.move(mouse(200, 160));
    // The wheel brings 100 px more of the board below into view.
    gestures.wheel({
      x: 200,
      y: 160,
      dx: 0,
      dy: 100,
      deltaMode: 0,
      zoom: false,
      shift: false,
    });
    gestures.refresh();
    expect(state.bands.at(-1)).toEqual({
      x: 50,
      y: -50,
      width: 150,
      height: 210,
    });
    gestures.up(mouse(200, 160));
    expect(state.banded).toEqual([{ x: 50, y: 50, width: 150, height: 210 }]);
  });

  it("ends a rubber band, and finds a click's node, where the pointer went up, though no move told of it", () => {
    const { state, gestures } = host("select");
    gestures.down(mouse(50, 50));
    gestures.move(mouse(120, 90));
    gestures.up(mouse(200, 160));
    expect(state.banded).toEqual([{ x: 50, y: 50, width: 150, height: 110 }]);
    // Pressed 2 px beside the node, beyond a mouse's slop, and let go of on
    // its edge, within a mouse's threshold of a drag.
    gestures.down(mouse(342, 150));
    gestures.up(mouse(340, 150));
    expect(state.selects.at(-1)).toEqual({ ids: ["nd_a"], how: "replace" });
  });

  it("selects nothing with a click of the middle button, which pans", () => {
    const { state, gestures } = host("select");
    gestures.down(mouse(150, 150, { button: 1 }));
    gestures.up(mouse(150, 150, { button: 1 }));
    gestures.down(mouse(700, 500, { button: 1 }));
    gestures.up(mouse(700, 500, { button: 1 }));
    expect(state.selects).toEqual([]);
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
  it("needs no right click: the mouse's other buttons, and a pen's barrel button and eraser, do nothing", () => {
    const { state, gestures } = host();
    expect(gestures.down(mouse(100, 100, { button: 2 }))).toBe(false);
    gestures.move(mouse(300, 300, { button: 2 }));
    gestures.up(mouse(300, 300, { button: 2 }));
    for (const button of [2, 5]) {
      const pen = mouse(100, 100, { id: 2, kind: "pen", button });
      expect(gestures.down(pen)).toBe(false);
      gestures.move({ ...pen, x: 300, y: 300 });
      gestures.up({ ...pen, x: 300, y: 300 });
    }
    expect(state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(state.selects).toEqual([]);
  });

  it("ends a press whose release it did not hear of once its pointer goes down again, rather than refuse the press", () => {
    const { state, gestures } = host("select");
    // A Ctrl+click on a Mac, whose release a menu of the system's took,
    // after a drag of a rubber band.
    expect(gestures.down(mouse(10, 10, { additive: true }))).toBe(true);
    gestures.move(mouse(60, 60));
    expect(state.bands.at(-1)).toBeDefined();
    // The next click, on the node, is followed, and selects it.
    expect(gestures.down(mouse(200, 150))).toBe(true);
    expect(state.bands.at(-1)).toBeUndefined();
    gestures.up(mouse(200, 150));
    expect(state.selects).toEqual([{ ids: ["nd_a"], how: "replace" }]);
    expect(gestures.tracks(1)).toBe(false);
  });

  it("ends a press, as cancelled, once its mouse or its pen moves without the button that pressed it", () => {
    const { state, gestures } = host("select");
    // A rubber band, whose mouse's release a menu of the system's took.
    gestures.down(mouse(10, 10));
    gestures.move(mouse(60, 60));
    gestures.move(mouse(80, 80, { buttons: 0 }));
    expect(gestures.tracks(1)).toBe(false);
    expect(state.bands.at(-1)).toBeUndefined();
    // A pen's, which hovers after its lost release, over the node.
    const pen = mouse(10, 10, { id: 2, kind: "pen" });
    gestures.down(pen);
    gestures.move({ ...pen, x: 60, y: 60 });
    gestures.move({ ...pen, x: 200, y: 150, buttons: 0 });
    expect(gestures.tracks(2)).toBe(false);
    // A chord: the right button went down after the primary, which went up
    // as it stays down, and the right one draws no rubber band, nor goes up
    // to a click that selects the node under it.
    gestures.down(mouse(200, 150));
    gestures.move(mouse(200, 150, { buttons: 3 }));
    gestures.move(mouse(180, 140, { buttons: 2 }));
    gestures.move(mouse(10, 10, { buttons: 2 }));
    gestures.up(mouse(10, 10, { button: 2, buttons: 0 }));
    // A middle button's pan, whose button went up as the primary stays down.
    gestures.down(mouse(500, 500, { button: 1 }));
    gestures.move(mouse(500, 500, { button: 1, buttons: 5 }));
    gestures.move(mouse(540, 500, { button: 1, buttons: 1 }));
    expect(gestures.tracks(1)).toBe(false);
    expect(state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(state.banded).toEqual([]);
    expect(state.selects).toEqual([]);
    // A finger is down as long as it moves, whatever its buttons say.
    gestures.down(touch(3, 500, 500));
    gestures.move({ ...touch(3, 540, 500), buttons: 0 });
    expect(gestures.tracks(3)).toBe(true);
    expect(state.bands.at(-1)).toBeDefined();
  });

  it("pinches with two fingers alone, lets a press of another kind of pointer take the canvas from the one before, but a palm's under a pen, and gives back each pointer that it no longer follows", () => {
    const { state, gestures } = host();
    // A mouse whose release the canvas did not hear of, and that stays
    // still, and a finger that touches the screen: the finger pans.
    gestures.down(mouse(100, 100));
    expect(gestures.down(touch(2, 500, 300))).toBe(true);
    expect(gestures.tracks(1)).toBe(false);
    gestures.move(touch(2, 560, 300));
    expect(state.v).toEqual({ x: -60, y: 0, zoom: 1 });
    // A pen's press takes the canvas from the finger in turn.
    const pen = mouse(300, 300, { id: 3, kind: "pen" });
    expect(gestures.down(pen)).toBe(true);
    expect(gestures.tracks(2)).toBe(false);
    gestures.move(touch(2, 700, 300));
    // A palm that rests on the screen as the pen draws is ignored, and the
    // pen goes on.
    expect(gestures.down(touch(6, 500, 500))).toBe(false);
    gestures.move(touch(6, 600, 500));
    expect(gestures.tracks(3)).toBe(true);
    gestures.move({ ...pen, x: 300, y: 400 });
    expect(state.v).toEqual({ x: -60, y: -100, zoom: 1 });
    gestures.up({ ...pen, x: 300, y: 400 });
    // A mouse's press while two fingers pinch takes it from them both.
    gestures.down(touch(4, 200, 300));
    gestures.down(touch(5, 400, 300));
    expect(gestures.down(mouse(300, 300))).toBe(true);
    expect(gestures.tracks(4) || gestures.tracks(5)).toBe(false);
    gestures.move(touch(5, 600, 300));
    expect(state.v.zoom).toBe(1);
    // Each pointer that a press took the canvas from, and the pen that went
    // up, but not the palm, which the canvas never followed.
    expect(state.released).toEqual([1, 2, 3, 4, 5]);
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
    expect(gestures.tracks(1)).toBe(false);
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

  it("pans a wheel's page by the canvas's width across and its height down", () => {
    const { state, gestures } = host();
    const page = { x: 0, y: 0, deltaMode: 2, zoom: false, shift: false };
    gestures.wheel({ ...page, dx: 1, dy: 0 });
    expect(state.v).toEqual({ x: 800, y: 0, zoom: 1 });
    gestures.wheel({ ...page, dx: 0, dy: 1 });
    expect(state.v).toEqual({ x: 800, y: 600, zoom: 1 });
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

  it("zooms with no gesture of Safari's of fingers that it ignores under a pen, as a palm's", () => {
    const { state, gestures } = host("select");
    const pen = mouse(100, 100, { id: 1, kind: "pen" });
    gestures.down(pen);
    gestures.move({ ...pen, x: 300, y: 300 });
    expect(gestures.down(touch(2, 500, 300))).toBe(false);
    expect(gestures.down(touch(3, 700, 300))).toBe(false);
    gestures.scale({ x: 600, y: 300 }, 1, 2);
    expect(state.v).toEqual({ x: 0, y: 0, zoom: 1 });
    expect(state.bands.at(-1)).toEqual({
      x: 100,
      y: 100,
      width: 200,
      height: 200,
    });
  });

  it("ends a pen's press whose release it did not hear of once the pen leaves the range where it hovers, so that a finger takes the canvas again", () => {
    const { state, gestures } = host();
    const pen = mouse(100, 100, { id: 1, kind: "pen" });
    gestures.down(pen);
    gestures.move({ ...pen, x: 140, y: 100 });
    // Lifted straight out of the range, with no move after its release.
    gestures.leave({ ...pen, x: 140, y: 100, buttons: 0 });
    expect(gestures.tracks(1)).toBe(false);
    expect(state.released).toEqual([1]);
    expect(gestures.down(touch(2, 500, 300))).toBe(true);
    gestures.move(touch(2, 560, 300));
    expect(state.v).toEqual({ x: -100, y: 0, zoom: 1 });
    // A pointer that leaves with its button down, as a mouse whose capture
    // the browser did not take, goes on.
    gestures.down(mouse(100, 100, { id: 3 }));
    gestures.leave(mouse(150, 100, { id: 3 }));
    expect(gestures.tracks(3)).toBe(true);
    expect(state.v).toEqual({ x: -150, y: 0, zoom: 1 });
  });

  it("goes on with a gesture from where the board is once the canvas moved it by itself, as with its fit", () => {
    const { state, gestures } = host("select");
    // A pinch: its next move zooms from the new viewport, by the fingers'
    // ratio since, about the board's point that is between them now.
    gestures.down(touch(1, 300, 300));
    gestures.down(touch(2, 500, 300));
    gestures.move(touch(2, 600, 300));
    let from = state.v;
    state.v = { x: 1000, y: 2000, zoom: 0.5 };
    gestures.replaced(from);
    const between = toWorld(state.v, { x: 450, y: 300 });
    gestures.move(touch(2, 900, 300));
    expect(state.v.zoom).toBeCloseTo(0.5 * (600 / 300));
    const held = toScreen(state.v, between);
    expect(held.x).toBeCloseTo(600);
    expect(held.y).toBeCloseTo(300);
    gestures.up(touch(1, 300, 300));
    gestures.up(touch(2, 900, 300));
    // A rubber band keeps its corner where it is on the canvas.
    gestures.down(mouse(100, 100));
    gestures.move(mouse(200, 200));
    from = state.v;
    state.v = { x: -50, y: -50, zoom: 2 };
    gestures.replaced(from);
    expect(state.bands.at(-1)).toEqual({
      x: 100,
      y: 100,
      width: 100,
      height: 100,
    });
    gestures.up(mouse(200, 200));
    expect(state.banded.at(-1)).toEqual({ x: 0, y: 0, width: 50, height: 50 });
  });
});
