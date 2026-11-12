import { describe, expect, it } from "vitest";
import { docOf, gridNodes, testConnection, testNode } from "../test/canvas";
import {
  animationTime,
  CanvasController,
  type CanvasView,
  type Clock,
  type PortOf,
} from "./controller";
import type { Engine, Frame, FrameStats } from "./engine";
import type { MenuTarget } from "./gestures";
import type { Theme } from "./theme";

const theme: Theme = {
  canvas: 0xffffff,
  surface: 0xf3f4f6,
  border: 0x7c8494,
  fg: 0x15171c,
  muted: 0x4b5260,
  accent: 0x2b50c8,
  danger: 0xb3261e,
  dark: false,
};

/** A clock whose frames the test runs, and whose time it sets. */
function fakeClock() {
  let now = 0;
  let next = 1;
  const frames = new Map<number, (time: number) => void>();
  /** What the clock calls once its time is up, as the end of a long press's time. */
  const timers = new Set<{ at: number; then: () => void }>();
  const clock: Clock = {
    request: (frame) => {
      frames.set(next, frame);
      return next++;
    },
    cancel: (handle) => frames.delete(handle),
    now: () => now,
    after: (ms, then) => {
      const timer = { at: now + ms, then };
      timers.add(timer);
      return () => timers.delete(timer);
    },
  };
  return {
    clock,
    /** Moves the time on by ms and runs the frames that were asked for. */
    tick(ms = 16) {
      now += ms;
      for (const timer of [...timers]) {
        if (timer.at <= now && timers.delete(timer)) {
          timer.then();
        }
      }
      const due = [...frames.values()];
      frames.clear();
      for (const frame of due) {
        frame(now);
      }
    },
    pending: () => frames.size,
  };
}

/** An engine that records the frames that it is asked to draw, and whose context the test gives back. */
function fakeEngine() {
  const frames: Frame[] = [];
  const restores = new Set<() => void>();
  const engine: Engine = {
    kind: "webgl",
    resize: () => {},
    render: (f): FrameStats => {
      frames.push(f);
      return {
        lod: f.viewport.zoom < 0.4 ? "blocks" : "detail",
        nodes: f.scene.nodes.size,
        visible: f.scene.nodes.size,
        texts: 0,
        connections: 0,
      };
    },
    textures: () => 4,
    onRestore: (listener) => {
      restores.add(listener);
      return () => {
        restores.delete(listener);
      };
    },
    destroy: () => {},
  };
  /** Gives back the context that the browser took. */
  const restore = () => {
    for (const listener of restores) {
      listener();
    }
  };
  return { engine, frames, restore };
}

function setUp() {
  const time = fakeClock();
  const events = {
    selections: [] as number[],
    zooms: [] as number[],
    rendered: 0,
    views: [] as CanvasView[],
    /** The connection selected, each time that it changed. */
    connections: [] as (string | undefined)[],
    /** What the canvas offered, to create a node and connect it, each time that it changed. */
    offers: [] as (
      | { from: PortOf; at: { x: number; y: number } }
      | undefined
    )[],
    /** The context menus that the canvas asked the page for, and whether a long press asked for each. */
    menus: [] as { target: MenuTarget; held: boolean }[],
  };
  const c = new CanvasController(
    "bd_a",
    {
      selection: (ids) => events.selections.push(ids.size),
      connection: (id) => events.connections.push(id),
      offer: (offer) => events.offers.push(offer),
      menu: (target, held) => events.menus.push({ target, held }),
      zoom: (z) => events.zooms.push(z),
      rendered: () => {
        events.rendered++;
      },
      view: (v) => events.views.push(v),
    },
    time.clock,
  );
  const host = document.createElement("div");
  const band = document.createElement("div");
  c.attach({ host, band });
  const { engine, frames, restore } = fakeEngine();
  c.setEngine(engine);
  c.setTheme(theme);
  c.setSize({ width: 1000, height: 600 }, 1);
  return { c, time, events, host, band, frames, engine, restore };
}

/**
 * Reads a board of a node into the canvas, which places it on the node,
 * fitted, so that what moves the canvas after places it there.
 */
function placed(c: CanvasController, time: ReturnType<typeof fakeClock>) {
  c.sync(docOf([testNode("nd_1", 0, 0)]), false);
  c.setReady();
  time.tick();
}

describe("the canvas's controller", () => {
  it("fits the board's content once it has been read, and tells of its first frame", () => {
    const { c, time, events, host } = setUp();
    c.sync(docOf(gridNodes(100, 10)), false);
    time.tick();
    expect(events.rendered).toBe(0);
    c.setReady();
    time.tick();
    expect(events.rendered).toBe(1);
    expect(host.dataset.ready).toBe("true");
    expect(host.dataset.renderer).toBe("webgl");
    // 10 columns of 320 and 10 rows of 200, in 1000 by 600 less the padding.
    expect(c.viewport().zoom).toBeCloseTo(504 / (9 * 200 + 128));
    time.tick();
    expect(events.rendered).toBe(1);
  });

  // Verifies: FR-PROJ-04/AC1
  it("opens where the device left the board, with the nodes that it selected that the board still has, in the first frame of the board's content", () => {
    const { c, time, events, frames } = setUp();
    c.restore({
      viewport: { x: 100, y: 50, zoom: 2 },
      size: { width: 1000, height: 600 },
      selection: ["nd_1", "nd_gone"],
    });
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    time.tick();
    const before = frames.length;
    c.setReady();
    time.tick();
    time.tick();
    expect(events.rendered).toBe(1);
    // The first frame once the board's content has been read is drawn at
    // the view, never fitted first.
    const ready = frames.slice(before);
    expect(ready.map((f) => f.viewport)).toEqual([
      { x: 100, y: 50, zoom: 2 },
      { x: 100, y: 50, zoom: 2 },
    ]);
    expect([...ready[0].selection]).toEqual(["nd_1"]);
    expect(events.selections).toEqual([1]);
    // A view restored after the canvas was placed moves nothing.
    c.restore({
      viewport: { x: 0, y: 0, zoom: 1 },
      size: { width: 1000, height: 600 },
      selection: [],
    });
    time.tick();
    expect(c.viewport()).toEqual({ x: 100, y: 50, zoom: 2 });
  });

  it("opens a view of a canvas of another size about the centre that it had", () => {
    const { c, time } = setUp();
    c.restore({
      viewport: { x: 100, y: 50, zoom: 2 },
      size: { width: 500, height: 300 },
      selection: [],
    });
    placed(c, time);
    // Its centre, 225 by 125 of the board, at the centre of 1000 by 600.
    expect(c.viewport()).toEqual({ x: -25, y: -25, zoom: 2 });
  });

  it("asks for no frame to restore a view, and keeps a selection that the user made before it was placed", () => {
    for (const selection of [["nd_1"], []]) {
      const { c, time, frames } = setUp();
      time.tick();
      c.restore({
        viewport: { x: 100, y: 50, zoom: 2 },
        size: { width: 1000, height: 600 },
        selection,
      });
      // The page restores the view as it renders.
      expect(time.pending()).toBe(0);
      // The document had the board's nodes, which the canvas shows while
      // it reads them anew, and the user selects one meanwhile.
      c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
      c.select(["nd_2"], "replace");
      time.tick();
      c.setReady();
      time.tick();
      expect(c.viewport(), `${selection}`).toEqual({ x: 100, y: 50, zoom: 2 });
      expect([...(frames.at(-1)?.selection ?? [])], `${selection}`).toEqual([
        "nd_2",
      ]);
    }
  });

  it("tells the page what it shows from the frame that placed it on, once a frame, as its view, its size, or its selection changes", () => {
    const { c, time, events } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    time.tick();
    c.setViewport({ x: 5, y: 5, zoom: 1 });
    time.tick();
    expect(events.views).toEqual([]);
    c.setReady();
    time.tick();
    expect(events.views).toHaveLength(1);
    expect(events.views[0].viewport).toEqual(c.viewport());
    time.tick();
    c.setTheme({ ...theme, canvas: 0 });
    time.tick();
    expect(events.views).toHaveLength(1);
    c.setViewport({ x: 10, y: 10, zoom: 1 });
    c.setViewport({ x: 20, y: 10, zoom: 1 });
    time.tick();
    expect(events.views.map((v) => v.viewport.x)).toEqual([
      events.views[0].viewport.x,
      20,
    ]);
    c.select(["nd_1"], "replace");
    time.tick();
    expect([...events.views[2].selection]).toEqual(["nd_1"]);
    c.setSize({ width: 800, height: 600 }, 1);
    expect(events.views[3].size).toEqual({ width: 800, height: 600 });
  });

  it("draws only once something changed", () => {
    const { c, time, frames } = setUp();
    c.sync(docOf(gridNodes(4, 2)), false);
    c.setReady();
    time.tick();
    const drawn = frames.length;
    time.tick();
    time.tick();
    expect(frames.length).toBe(drawn);
    c.setViewport({ x: 10, y: 10, zoom: 1 });
    time.tick();
    expect(frames.length).toBe(drawn + 1);
  });

  it("tells the benchmark which frames the browser's frame drew, and which the page's own work did, as once the canvas's size changed", () => {
    const { c, time } = setUp();
    placed(c, time);
    const scheduled: boolean[] = [];
    const stop = c.handle.onFrame((f) => scheduled.push(f.scheduled));
    c.setViewport({ x: 10, y: 10, zoom: 1 });
    time.tick();
    c.setSize({ width: 800, height: 600 }, 1);
    stop();
    expect(scheduled).toEqual([true, false]);
  });

  it("draws again for a theme of other colours, but not for the same colours read again, as once the window gets the focus back in forced colours", () => {
    const { c, time, frames } = setUp();
    c.sync(docOf(gridNodes(4, 2)), false);
    c.setReady();
    time.tick();
    const drawn = frames.length;
    c.setTheme({ ...theme });
    time.tick();
    expect(frames.length).toBe(drawn);
    expect(frames.at(-1)?.theme).toBe(theme);
    const night = { ...theme, canvas: 0, surface: 0, dark: true };
    c.setTheme(night);
    time.tick();
    expect(frames.length).toBe(drawn + 1);
    expect(frames.at(-1)?.theme).toBe(night);
  });

  // Verifies: NFR-A11Y-07
  it("animates a zoom that the toolbar asks for, unless the system asks for reduced motion", () => {
    const animated = setUp();
    animated.c.setViewport({ x: 0, y: 0, zoom: 1 });
    animated.c.zoomTo(2);
    animated.time.tick();
    const mid = animated.c.viewport().zoom;
    expect(mid).toBeGreaterThan(1);
    expect(mid).toBeLessThan(2);
    animated.time.tick(animationTime);
    expect(animated.c.viewport().zoom).toBe(2);

    const reduced = setUp();
    reduced.c.reducedMotion = true;
    reduced.c.setViewport({ x: 0, y: 0, zoom: 1 });
    reduced.c.zoomTo(2);
    expect(reduced.c.viewport().zoom).toBe(2);
    reduced.time.tick();
    expect(reduced.time.pending()).toBe(0);
  });

  it("zooms to the selection, which a node gone from the board leaves", () => {
    const { c, time, events } = setUp();
    c.reducedMotion = true;
    const nodes = gridNodes(100, 10);
    c.sync(docOf(nodes), false);
    expect(c.zoomToSelection()).toBe(false);
    c.select([nodes[55].id], "replace");
    expect(events.selections).toEqual([1]);
    expect(c.zoomToSelection()).toBe(true);
    expect(c.viewport().zoom).toBe(1);
    time.tick();
    c.sync(docOf(nodes.filter((n) => n.id !== nodes[55].id)), false);
    expect(events.selections).toEqual([1, 0]);
  });

  it("pans and zooms with the keys, and leaves the browser's own combinations alone", () => {
    const { c } = setUp();
    c.reducedMotion = true;
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    const key = (k: string, extra: Partial<KeyboardEvent> = {}) =>
      c.key({
        key: k,
        code: "",
        shiftKey: false,
        ctrlKey: false,
        metaKey: false,
        altKey: false,
        ...extra,
      });
    expect(key("ArrowRight")).toBe(true);
    expect(c.viewport().x).toBeCloseTo(100);
    expect(key("ArrowDown", { shiftKey: true })).toBe(true);
    expect(c.viewport().y).toBeCloseTo(300);
    expect(key("+")).toBe(true);
    expect(c.viewport().zoom).toBe(1.25);
    expect(key("0")).toBe(true);
    expect(c.viewport().zoom).toBe(1);
    expect(key("+", { ctrlKey: true })).toBe(false);
    expect(key("a")).toBe(false);
    // Shift and 1 type + on a Swiss keyboard, and fit the content still.
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    expect(key("+", { code: "Digit1", shiftKey: true })).toBe(true);
    expect(c.viewport().zoom).toBe(1);
    expect(c.viewport().x).toBeCloseTo(120 - 500);
    // Escape is the canvas's while there is a selection that it clears, and
    // the page's else.
    c.select(["nd_1"], "replace");
    expect(key("Escape")).toBe(true);
    expect(key("Escape")).toBe(false);
  });

  it("draws at once as the canvas is resized, rather than a frame of nothing first", () => {
    const { c, time, frames } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    const drawn = frames.length;
    c.setSize({ width: 800, height: 600 }, 1);
    expect(frames.length).toBe(drawn + 1);
    expect(frames[frames.length - 1].size).toEqual({ width: 800, height: 600 });
    time.tick();
    expect(frames.length).toBe(drawn + 1);
  });

  it("asks for no frame once it stopped, until the canvas is attached again", () => {
    const { c, time, host, band } = setUp();
    time.tick();
    c.stop();
    c.attach(undefined);
    c.setEngine(undefined);
    expect(time.pending()).toBe(0);
    c.attach({ host, band });
    expect(time.pending()).toBe(1);
  });

  it("reads every card's labels again once a name that cards show changes, but not for a node that moves", () => {
    const { c, time, frames } = setUp();
    const jumper = testNode("nd_j", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_b", node_id: "nd_t" },
    });
    const other = testNode("nd_o", 400, 0);
    const target = testNode("nd_t", 0, 0, { board_id: "bd_b" });
    c.sync(docOf([jumper, other, target]), false);
    c.setReady();
    time.tick();
    const labels = () => frames[frames.length - 1].labelsVersion;
    const first = labels();
    c.sync(
      docOf([jumper, { ...other, position: { x: 0, y: 400 } }, target]),
      false,
    );
    time.tick();
    expect(labels()).toBe(first);
    c.sync(docOf([jumper, other, { ...target, title: "Harbour" }]), false);
    time.tick();
    expect(labels()).toBe(first + 1);
    c.sync(docOf([jumper, other, { ...target, title: "Harbour" }]), true);
    time.tick();
    expect(labels()).toBe(first + 2);
  });

  it("keeps the board's centre where it is as the canvas is resized", () => {
    const { c, time } = setUp();
    placed(c, time);
    c.setViewport({ x: 100, y: 100, zoom: 2 });
    c.setSize({ width: 600, height: 1000 }, 2);
    // The centre was 100 + 500 / 2, 100 + 300 / 2.
    expect(c.viewport().x + 300 / 2).toBeCloseTo(350);
    expect(c.viewport().y + 500 / 2).toBeCloseTo(250);
  });

  it("keeps the board's centre where it is through a size of none, as of a host that the page does not display, and a move's meanwhile", () => {
    const { c, time } = setUp();
    placed(c, time);
    c.setViewport({ x: 100, y: 100, zoom: 2 });
    c.setSize({ width: 0, height: 0 }, 1);
    c.setSize({ width: 600, height: 1000 }, 1);
    expect(c.viewport().x + 300 / 2).toBeCloseTo(350);
    expect(c.viewport().y + 500 / 2).toBeCloseTo(250);
    // A move while the canvas has no size, as the minimap's, is about the
    // centre that it shows again, not about the corner of none.
    c.setSize({ width: 0, height: 0 }, 1);
    c.centerOn({ x: 1000, y: 2000 });
    c.setSize({ width: 800, height: 400 }, 1);
    expect(c.viewport().x + 400 / 2).toBeCloseTo(1000);
    expect(c.viewport().y + 200 / 2).toBeCloseTo(2000);
  });

  it("opens fitted to the content once it has been read, however the wheel moved the canvas while it was read", () => {
    const { c, time } = setUp();
    c.sync(docOf(gridNodes(100, 10)), false);
    const wheel = { x: 500, y: 300, deltaMode: 0, zoom: false, shift: false };
    c.gestures.wheel({ ...wheel, dx: 0, dy: 120 });
    c.gestures.wheel({ ...wheel, dx: 0, dy: 40, zoom: true });
    time.tick();
    expect(c.viewport().zoom).toBeLessThan(1);
    c.setReady();
    time.tick();
    expect(c.viewport().zoom).toBeCloseTo(504 / (9 * 200 + 128));
  });

  it("keeps its fit once the content has been read, though a pinch that began while it was read goes on", () => {
    const { c, time } = setUp();
    c.sync(docOf(gridNodes(100, 10)), false);
    const finger = (id: number, x: number) => ({
      id,
      x,
      y: 300,
      kind: "touch" as const,
      button: 0,
      buttons: 1,
      shift: false,
      additive: false,
    });
    c.down(finger(1, 400));
    c.down(finger(2, 600));
    c.gestures.move(finger(2, 700));
    time.tick();
    c.setReady();
    time.tick();
    const fitted = c.viewport();
    expect(fitted.zoom).toBeCloseTo(504 / (9 * 200 + 128));
    // The fingers' next move zooms from the fit, by the ratio of their
    // distance since, rather than back to where they held the board.
    c.gestures.move(finger(2, 730));
    expect(c.viewport().zoom).toBeCloseTo(fitted.zoom * (330 / 300));
  });

  it("fits the content, though a pinch moves after it has been read and before the frame that fits it, as a browser sends a frame's moves before its callbacks", () => {
    const { c, time } = setUp();
    c.sync(docOf(gridNodes(100, 10)), false);
    const finger = (id: number, x: number) => ({
      id,
      x,
      y: 300,
      kind: "touch" as const,
      button: 0,
      buttons: 1,
      shift: false,
      additive: false,
    });
    c.down(finger(1, 400));
    c.down(finger(2, 600));
    time.tick();
    c.setReady();
    // The fingers' move of the frame that the read asked for, which the
    // browser sends before the frame's callback.
    c.gestures.move(finger(2, 700));
    time.tick();
    const fitted = c.viewport();
    expect(fitted.zoom).toBeCloseTo(504 / (9 * 200 + 128));
    c.gestures.move(finger(2, 730));
    expect(c.viewport().zoom).toBeCloseTo(fitted.zoom * (330 / 300));
  });

  it("goes on with an animation, and with the fit to come, as a wheel's event moves nothing", () => {
    const { c, time } = setUp();
    c.sync(docOf(gridNodes(100, 10)), false);
    c.setReady();
    const still = { x: 500, y: 300, dx: 0, dy: 0, deltaMode: 0, shift: false };
    c.gestures.wheel({ ...still, zoom: false });
    // Safari's pinch, as it starts, is of a scale of 1.
    c.gestures.scale({ x: 500, y: 300 }, 1, 1);
    time.tick();
    const fitted = c.viewport();
    expect(fitted.zoom).toBeCloseTo(504 / (9 * 200 + 128));
    c.zoomTo(1);
    time.tick();
    c.gestures.wheel({ ...still, zoom: true });
    time.tick(animationTime);
    expect(c.viewport().zoom).toBe(1);
  });

  it("goes on with an animation from where the canvas is, as it is resized meanwhile", () => {
    const { c, time } = setUp();
    placed(c, time);
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    // To 200% about the board's point at the centre, 500, 300.
    c.zoomTo(2);
    time.tick();
    c.setSize({ width: 600, height: 1000 }, 1);
    time.tick(animationTime);
    expect(c.viewport().zoom).toBe(2);
    expect(c.viewport().x + 300 / 2).toBeCloseTo(500);
    expect(c.viewport().y + 500 / 2).toBeCloseTo(300);
  });

  it("goes on with an animation as a pointer that it ignores goes down, and stops it for one that it follows", () => {
    const { c, time } = setUp();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    c.zoomTo(2);
    time.tick();
    const right = {
      id: 1,
      x: 100,
      y: 100,
      kind: "mouse" as const,
      button: 2,
      buttons: 2,
      shift: false,
      additive: false,
    };
    expect(c.down(right)).toBe(false);
    time.tick(animationTime);
    expect(c.viewport().zoom).toBe(2);
    c.zoomTo(1);
    time.tick();
    expect(c.down({ ...right, button: 0, buttons: 1 })).toBe(true);
    const held = c.viewport();
    expect(held.zoom).toBeGreaterThan(1);
    time.tick(animationTime);
    expect(c.viewport()).toEqual(held);
  });

  it("centres the canvas, as the minimap does, at the zoom that an animation in progress goes to", () => {
    const { c, time } = setUp();
    c.setViewport({ x: 0, y: 0, zoom: 0.5 });
    c.zoomStep(1);
    time.tick();
    expect(c.viewport().zoom).toBeLessThan(0.67);
    c.centerOn({ x: 2000, y: 1000 });
    expect(c.viewport().zoom).toBe(0.67);
    expect(c.viewport().x + 500 / 0.67).toBeCloseTo(2000);
    expect(c.viewport().y + 300 / 0.67).toBeCloseTo(1000);
    time.tick(animationTime);
    expect(c.viewport().zoom).toBe(0.67);
  });

  it("draws a resize that the renderer throws on in the next frame, rather than throw into the page", () => {
    const { c, time } = setUp();
    placed(c, time);
    let lost = true;
    const drawn: { width: number; height: number }[] = [];
    c.setEngine({
      kind: "webgl",
      resize: () => {},
      render: (f) => {
        if (lost) {
          lost = false;
          throw new Error("the GPU's device is lost");
        }
        drawn.push(f.size);
        return {
          lod: "detail",
          nodes: 1,
          visible: 1,
          texts: 0,
          connections: 0,
        };
      },
      textures: () => undefined,
      onRestore: () => () => {},
      destroy: () => {},
    });
    expect(() => c.setSize({ width: 800, height: 600 }, 1)).not.toThrow();
    time.tick();
    expect(drawn).toEqual([{ width: 800, height: 600 }]);
  });

  it("sizes a renderer that throws as it is sized in the next frame, rather than throw into the page, or into the start that gives it", () => {
    const { c, time } = setUp();
    placed(c, time);
    let lost = true;
    const sized: { width: number; height: number }[] = [];
    const drawn: { width: number; height: number }[] = [];
    const engine: Engine = {
      kind: "webgl",
      resize: (size) => {
        if (lost) {
          throw new Error("the GPU's device is lost");
        }
        sized.push(size);
      },
      render: (f) => {
        drawn.push(f.size);
        return {
          lod: "detail",
          nodes: 1,
          visible: 1,
          texts: 0,
          connections: 0,
        };
      },
      textures: () => undefined,
      onRestore: () => () => {},
      destroy: () => {},
    };
    expect(() => c.setEngine(engine)).not.toThrow();
    expect(() => time.tick()).toThrow("the GPU's device is lost");
    expect(() => c.setSize({ width: 800, height: 600 }, 1)).not.toThrow();
    expect(drawn).toEqual([]);
    lost = false;
    time.tick();
    expect(sized).toEqual([{ width: 800, height: 600 }]);
    expect(drawn).toEqual([{ width: 800, height: 600 }]);
    time.tick();
    expect(sized).toHaveLength(1);
  });

  it("draws in the frame after one whose renderer threw, but asks for none after two that threw", () => {
    const { c, time } = setUp();
    placed(c, time);
    let throws = 1;
    const drawn: Frame[] = [];
    c.setEngine({
      kind: "webgl",
      resize: () => {},
      render: (f) => {
        if (throws > 0) {
          throws--;
          throw new Error("the GPU's device is lost");
        }
        drawn.push(f);
        return {
          lod: "detail",
          nodes: 1,
          visible: 1,
          texts: 0,
          connections: 0,
        };
      },
      textures: () => undefined,
      onRestore: () => () => {},
      destroy: () => {},
    });
    // The frame that setEngine asked for throws, and the next draws, with
    // nothing else asking for it.
    expect(() => time.tick()).toThrow("the GPU's device is lost");
    time.tick();
    expect(drawn).toHaveLength(1);
    // A renderer that throws in each frame is tried once more, and then
    // once something asks for a frame.
    throws = Number.POSITIVE_INFINITY;
    c.setViewport({ x: 10, y: 0, zoom: 1 });
    expect(() => time.tick()).toThrow("the GPU's device is lost");
    expect(() => time.tick()).toThrow("the GPU's device is lost");
    expect(time.pending()).toBe(0);
    throws = 0;
    c.setViewport({ x: 20, y: 0, zoom: 1 });
    time.tick();
    expect(drawn).toHaveLength(2);
    expect(drawn[1]?.viewport.x).toBe(20);
  });

  it("draws again once the browser gives back the renderer's context that it took, and not once the renderer has ended", () => {
    const { c, time, frames, engine, restore } = setUp();
    placed(c, time);
    time.tick();
    const drawn = frames.length;
    expect(time.pending()).toBe(0);
    restore();
    time.tick();
    expect(frames.length).toBe(drawn + 1);
    c.setEngine(undefined);
    time.tick();
    restore();
    expect(time.pending()).toBe(0);
    // A renderer taken again is followed again.
    c.setEngine(engine);
    time.tick();
    restore();
    expect(time.pending()).toBe(1);
  });

  it("tells the zoom as it changes by a percent", () => {
    const { c, time, events } = setUp();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    time.tick();
    c.setViewport({ x: 0, y: 0, zoom: 1.001 });
    time.tick();
    c.setViewport({ x: 0, y: 0, zoom: 0.5 });
    time.tick();
    expect(events.zooms).toEqual([1, 0.5]);
  });
});

/** Gives c edits that record what it asks the page to change. */
function editsOf(c: CanvasController) {
  const asked = {
    moves: [] as Map<string, { x: number; y: number }>[],
    /** The revisions that each move expects, of the nodes as its drag started. */
    expected: [] as Map<string, number>[],
    sizes: [] as { id: string; size: { width: number; height: number } }[],
    /** The revision that each resize expects, of its node as its drag started. */
    sizeRevs: [] as number[],
    removed: [] as string[][],
    opened: [] as string[],
    /** What the drags of link handles asked for, and the keys of a connection selected. */
    connected: [] as { from: PortOf; to: string }[],
    reconnected: [] as {
      id: string;
      ends: { from?: PortOf; to?: string };
      rev: number;
    }[],
    removedConnections: [] as string[],
    openedConnections: [] as string[],
  };
  c.edits = {
    move: (places, expected) => {
      asked.moves.push(new Map(places));
      asked.expected.push(new Map(expected));
    },
    resize: (id, size, rev) => {
      asked.sizes.push({ id, size });
      asked.sizeRevs.push(rev);
    },
    remove: (ids) => asked.removed.push([...ids]),
    open: (id) => asked.opened.push(id),
    connect: (from, to) => asked.connected.push({ from, to }),
    reconnect: (id, ends, rev) => asked.reconnected.push({ id, ends, rev }),
    removeConnection: (id) => asked.removedConnections.push(id),
    openConnection: (id) => asked.openedConnections.push(id),
  };
  return asked;
}

describe("the canvas's controller of a board that may be changed", () => {
  // Verifies: FR-EDIT-02/AC2
  it("shows a move of the selection at once, without a command, and asks for one batch of where it left the nodes, on the grid", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    const doc = docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]);
    c.sync(doc, false);
    c.setReady();
    time.tick();
    c.select(["nd_1", "nd_2"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 13, y: 3 });
    // The scene has the nodes where the drag has them, on the grid.
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 16, y: 0 });
    expect(c.scene.nodes.get("nd_2")?.node.position).toEqual({ x: 416, y: 0 });
    expect(asked.moves).toEqual([]);
    c.moveEnd(true);
    expect(asked.moves).toEqual([
      new Map([
        ["nd_1", { x: 16, y: 0 }],
        ["nd_2", { x: 416, y: 0 }],
      ]),
    ]);
    // Until the document has the move, the scene shows it as the document
    // has it.
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 0, y: 0 });
  });

  it("moves freely where it does not snap, and back where it started once cancelled", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.snap = false;
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    c.moveStart("nd_1");
    c.moveTo({ x: 13.333, y: 3 });
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({
      x: 13.33,
      y: 3,
    });
    // The same move again, as each frame's under a pointer that is still,
    // leaves the node where it is, and once the grid snaps, puts it there.
    c.moveTo({ x: 13.333, y: 3 });
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({
      x: 13.33,
      y: 3,
    });
    c.snap = true;
    c.moveTo({ x: 13.333, y: 3 });
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({
      x: 16,
      y: 0,
    });
    c.moveEnd(false);
    expect(asked.moves).toEqual([]);
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 0, y: 0 });
  });

  it("draws no frame while a drag of the selection is held still, and builds anew only the nodes that it moves", () => {
    const { c, time, frames } = setUp();
    editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    time.tick();
    const other = c.scene.nodes.get("nd_2");
    const v = c.viewport();
    // The pointer at x of the canvas, level with the first node's middle.
    const at = (x: number) => ({
      id: 1,
      x,
      y: (64 - v.y) * v.zoom,
      kind: "mouse" as const,
      button: 0,
      buttons: 1,
      shift: false,
      additive: false,
    });
    const from = (120 - v.x) * v.zoom;
    expect(c.down(at(from))).toBe(true);
    c.gestures.move(at(from + 40));
    time.tick();
    expect(c.scene.nodes.get("nd_1")?.node.position.x).toBeGreaterThan(0);
    const drawn = frames.length;
    // The pointer is still: no frame, and none asked for.
    for (let i = 0; i < 5; i++) {
      time.tick();
    }
    expect(frames.length).toBe(drawn);
    expect(time.pending()).toBe(0);
    // The node that the drag does not move is the scene's as it was.
    expect(c.scene.nodes.get("nd_2")).toBe(other);
    c.gestures.up(at(from + 40));
  });

  it("draws a frame as a move ends, though it left the scene as it was, so that what a frame of the move hid, as the resize handle, shows again", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    // What the resize handle does in each frame: hides while a move is
    // under way.
    const hides: boolean[] = [];
    c.follow(() => hides.push(c.moves()));
    time.tick();
    expect(hides.at(-1)).toBe(false);
    // A drag a step of the grid away and back, which a frame between shows.
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 16, y: 0 });
    time.tick();
    c.moveTo({ x: 0, y: 0 });
    time.tick();
    expect(hides.at(-1)).toBe(true);
    c.moveEnd(true);
    time.tick();
    expect(hides.at(-1)).toBe(false);
    expect(asked.moves).toEqual([]);
    // A move that never left its place, of a frame drawn while it was
    // under way, as of the wheel's pan.
    expect(c.moveStart("nd_1")).toBe(true);
    c.setViewport({ ...c.viewport(), x: c.viewport().x + 10 });
    time.tick();
    expect(hides.at(-1)).toBe(true);
    c.moveEnd(true);
    time.tick();
    expect(hides.at(-1)).toBe(false);
  });

  it("starts no resize while another drag is under way, a move's or a resize's, whose nodes its own would take the place of, nor of a node that is not the one selected, whose handle the page has yet to take away", () => {
    const { c, time } = setUp();
    editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    expect(c.resizeStart("nd_1")).toBe(false);
    c.select(["nd_1", "nd_2"], "replace");
    expect(c.resizeStart("nd_1")).toBe(false);
    c.select(["nd_1"], "replace");
    expect(c.resizeStart("nd_1")).toBe(true);
    expect(c.resizeStart("nd_1")).toBe(false);
    c.resizeEnd(false);
    expect(c.resizeStart("nd_1")).toBe(true);
    c.resizeEnd(false);
    c.select(["nd_1"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    expect(c.resizeStart("nd_1")).toBe(false);
    // The end of a resize that is not under way leaves the move as it is.
    c.moveTo({ x: 16, y: 0 });
    c.resizeEnd(false);
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 16, y: 0 });
    c.moveEnd(false);
  });

  it("starts no move while a node's handle resizes it, whose nodes the move's would take the place of", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    expect(c.resizeStart("nd_1")).toBe(true);
    c.resizeTo({ x: 80, y: 0 });
    expect(c.moveStart("nd_1")).toBe(false);
    c.moveTo({ x: 100, y: 100 });
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 0, y: 0 });
    c.resizeEnd(true);
    expect(asked.sizes).toHaveLength(1);
    expect(asked.moves).toEqual([]);
  });

  it("selects nothing by a drag that picks a node while another's handle resizes it, which would take the handle away", () => {
    const { c, time } = setUp();
    editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    expect(c.resizeStart("nd_1")).toBe(true);
    expect(c.moveStart("nd_2", "replace")).toBe(false);
    expect([...c.selection()]).toEqual(["nd_1"]);
    c.resizeEnd(false);
    // Once the resize has ended, the pick selects the node and moves it.
    expect(c.moveStart("nd_2", "replace")).toBe(true);
    expect([...c.selection()]).toEqual(["nd_2"]);
    c.moveEnd(false);
  });

  it("asks for a move, and a resize, expecting the revisions that the nodes had as the drag started, though the document changed them meanwhile", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1", "nd_2"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 16, y: 0 });
    // Another session changes the second node while the drag holds it.
    c.sync(
      docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 80, { rev: 2 })]),
      false,
    );
    c.moveEnd(true);
    expect(asked.expected).toEqual([
      new Map([
        ["nd_1", 1],
        ["nd_2", 1],
      ]),
    ]);
    c.select(["nd_1"], "replace");
    expect(c.resizeStart("nd_1")).toBe(true);
    c.resizeTo({ x: 80, y: 0 });
    c.sync(
      docOf([
        testNode("nd_1", 0, 0, { rev: 3 }),
        testNode("nd_2", 400, 80, { rev: 2 }),
      ]),
      false,
    );
    c.resizeEnd(true);
    expect(asked.sizeRevs).toEqual([1]);
  });

  it("drops from the selection a node that another session deletes while a drag moves it, and moves the rest", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1", "nd_2"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 16, y: 0 });
    c.sync(
      docOf([
        testNode("nd_1", 0, 0),
        testNode("nd_2", 400, 0, { deleted_at: "2026-11-08T10:00:00Z" }),
      ]),
      false,
    );
    expect([...c.selection()]).toEqual(["nd_1"]);
    c.moveTo({ x: 32, y: 0 });
    expect([...c.selection()]).toEqual(["nd_1"]);
    c.moveEnd(true);
    expect(asked.moves).toEqual([new Map([["nd_1", { x: 32, y: 0 }]])]);
  });

  it("keeps selected the nodes that the page's batch creates until it shows them, though the scene changes before", async () => {
    const { c, time } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    let settle = () => {};
    const done = new Promise<boolean>((resolve) => {
      settle = () => resolve(true);
    });
    c.selectComing(["nd_2", "nd_3"], done);
    expect(c.selection().size).toBe(2);
    // Another session's change changes the scene before the batch, which
    // its mutation hands to the document a moment after, shows the copies.
    c.sync(docOf([testNode("nd_1", 8, 0)]), false);
    expect([...c.selection()]).toEqual(["nd_2", "nd_3"]);
    c.sync(
      docOf([
        testNode("nd_1", 8, 0),
        testNode("nd_2", 400, 0),
        testNode("nd_3", 800, 0),
      ]),
      false,
    );
    settle();
    await done;
    expect([...c.selection()]).toEqual(["nd_2", "nd_3"]);
  });

  it("selects again what was selected before the nodes of a batch that was refused, rather than none, unless the user changed the selection meanwhile", async () => {
    const { c, time } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_4", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1", "nd_4"], "replace");
    let refuse = () => {};
    const done = new Promise<boolean>((resolve) => {
      refuse = () => resolve(false);
    });
    c.selectComing(["nd_2"], done);
    expect([...c.selection()]).toEqual(["nd_2"]);
    refuse();
    await done;
    expect([...c.selection()].sort()).toEqual(["nd_1", "nd_4"]);
    // The user adds a node to the copy that is coming, which the selection
    // keeps once the copy's batch is refused.
    let refuseAgain = () => {};
    const again = new Promise<boolean>((resolve) => {
      refuseAgain = () => resolve(false);
    });
    c.selectComing(["nd_3"], again);
    c.select(["nd_4"], "add");
    refuseAgain();
    await again;
    expect([...c.selection()]).toEqual(["nd_4"]);
  });

  it("selects none of what was selected before the nodes of a batch that applied, though they went before it settled, as once the user deleted them", async () => {
    const { c, time } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_4", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1", "nd_4"], "replace");
    let apply = () => {};
    const done = new Promise<boolean>((resolve) => {
      apply = () => resolve(true);
    });
    c.selectComing(["nd_2"], done);
    // The copy shows, and the user deletes it before the batch's answer.
    c.sync(
      docOf([
        testNode("nd_1", 0, 0),
        testNode("nd_2", 32, 32),
        testNode("nd_4", 400, 0),
      ]),
      false,
    );
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_4", 400, 0)]), false);
    apply();
    await done;
    expect([...c.selection()]).toEqual([]);
  });

  it("starts no move of a node that is not selected, nor on a board that may not be changed", () => {
    const { c, time } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    expect(c.moveStart("nd_1")).toBe(false);
    editsOf(c);
    c.select([], "replace");
    expect(c.moveStart("nd_1")).toBe(false);
  });

  it("resizes a node by a drag of its handle, by the board's units under the pointer, which the corner follows as the board moves, and asks for its size once the handle is let go", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    c.setViewport({ x: 0, y: 0, zoom: 2 });
    // A mouse on the handle, at the node's corner, 240 by 128 units.
    const at = (x: number, buttons = 1) => ({
      id: 1,
      x,
      y: 256,
      kind: "mouse" as const,
      button: 0,
      buttons,
      shift: false,
      additive: false,
    });
    expect(c.down(at(480), "nd_1")).toBe(true);
    // 60 pixels at 200% are 30 units, which snap to 32.
    c.gestures.move(at(540));
    expect(c.scene.nodes.rect("nd_1")).toEqual({
      x: 0,
      y: 0,
      width: 272,
      height: 128,
    });
    // A key's pan of the board, 40 pixels at 200%, under the pointer, which
    // is still: the corner stays under it, 20 units on, at 290, which snaps
    // to 288.
    c.setViewport({ x: 20, y: 0, zoom: 2 });
    time.tick();
    expect(c.scene.nodes.rect("nd_1")?.width).toBe(288);
    c.gestures.up(at(540, 0));
    expect(asked.sizes).toEqual([
      { id: "nd_1", size: { width: 288, height: 128 } },
    ]);
    // A resize that ends where it started asks for nothing.
    c.resizeStart("nd_1");
    c.resizeTo({ x: 1, y: 1 });
    c.resizeEnd(true);
    expect(asked.sizes).toHaveLength(1);
  });

  // Verifies: FR-EDIT-06/AC1
  it("dims the nodes that the tag filter does not match, in the frame and in what it tells, and changes nothing of the board", () => {
    const { c, time, host, frames } = setUp();
    const asked = editsOf(c);
    const doc = docOf([
      testNode("nd_1", 0, 0, { tags: ["Quest"] }),
      testNode("nd_2", 400, 0),
      testNode("nd_3", 800, 0),
    ]);
    c.sync(doc, false);
    c.setReady();
    time.tick();
    expect(host.dataset.dimmed).toBe("0");
    let asks = 0;
    const dims = (node: { tags: string[] }) => {
      asks++;
      return !node.tags.includes("Quest");
    };
    c.setDimmed(dims);
    time.tick();
    expect(host.dataset.dimmed).toBe("2");
    const f = frames.at(-1);
    expect(f?.dimmed).toBe(dims);
    expect(host.dataset.nodes).toBe("3");
    // A drag, whose preview changes where nodes are and none of their
    // tags, does not count them again in each of its frames.
    const counted = asks;
    c.select(["nd_1"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 16, y: 0 });
    time.tick();
    c.moveTo({ x: 32, y: 0 });
    time.tick();
    c.moveEnd(false);
    time.tick();
    expect(asks).toBe(counted);
    expect(host.dataset.dimmed).toBe("2");
    c.setDimmed(undefined);
    time.tick();
    expect(host.dataset.dimmed).toBe("0");
    expect(asked).toEqual({
      moves: [],
      expected: [],
      sizes: [],
      sizeRevs: [],
      removed: [],
      opened: [],
      connected: [],
      reconnected: [],
      removedConnections: [],
      openedConnections: [],
    });
  });

  it("opens on a node that a link names, selected and shown whole, in place of the view that it would restore", () => {
    const { c, time, events } = setUp();
    c.restore({
      viewport: { x: 100, y: 50, zoom: 2 },
      size: { width: 1000, height: 600 },
      selection: [],
    });
    let found = 0;
    c.focus("nd_2", () => found++);
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 4000, 0)]), false);
    c.setReady();
    time.tick();
    expect([...c.selection()]).toEqual(["nd_2"]);
    expect(events.selections.at(-1)).toBe(1);
    expect(found).toBe(1);
    // At 100%, the node's centre at the canvas's.
    expect(c.viewport()).toEqual({ x: 4000 + 120 - 500, y: 64 - 300, zoom: 1 });
    // Of a canvas placed already, at once.
    c.focus("nd_1", () => found++);
    expect([...c.selection()]).toEqual(["nd_1"]);
    expect(found).toBe(2);
  });

  it("opens as without a link to a node that the board does not have, which it tells nothing of", () => {
    const { c, time } = setUp();
    let found = 0;
    c.focus("nd_gone", () => found++);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    expect([...c.selection()]).toEqual([]);
    c.focus("nd_gone", () => found++);
    time.tick();
    expect(found).toBe(0);
  });

  it("ends a move or a resize under way, cancelled, with Escape, which leaves the selection, and once the board may no longer be changed", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    const v = c.viewport();
    const at = (x: number, y = (64 - v.y) * v.zoom) => ({
      id: 1,
      x,
      y,
      kind: "mouse" as const,
      button: 0,
      buttons: 1,
      shift: false,
      additive: false,
    });
    const escapeKey = {
      key: "Escape",
      code: "Escape",
      shiftKey: false,
      ctrlKey: false,
      metaKey: false,
      altKey: false,
    };
    const from = (120 - v.x) * v.zoom;
    c.down(at(from));
    c.gestures.move(at(from + 80));
    expect(c.moves()).toBe(true);
    expect(c.key(escapeKey)).toBe(true);
    expect(c.moves()).toBe(false);
    expect([...c.selection()]).toEqual(["nd_1"]);
    expect(c.scene.nodes.get("nd_1")?.node.position).toEqual({ x: 0, y: 0 });
    c.gestures.move(at(from + 160));
    c.gestures.up(at(from + 160));
    expect(asked.moves).toEqual([]);
    // Escape with no drag under way clears the selection.
    expect(c.key(escapeKey)).toBe(true);
    expect([...c.selection()]).toEqual([]);
    // A resize by the handle of the node, selected again, once the board may
    // not be changed.
    c.select(["nd_1"], "replace");
    const corner = { x: (240 - v.x) * v.zoom, y: (128 - v.y) * v.zoom };
    c.down(at(corner.x, corner.y), "nd_1");
    c.gestures.move(at(corner.x + 80, corner.y));
    expect(c.scene.nodes.get("nd_1")?.rect.width).toBeGreaterThan(240);
    c.edits = undefined;
    expect(c.scene.nodes.get("nd_1")?.rect.width).toBe(240);
    c.gestures.up(at(corner.x + 80, corner.y));
    expect(asked.sizes).toEqual([]);
  });

  it("hides the resize handle in the frame after a move starts, though the grid keeps the node where it is", () => {
    const { c, time } = setUp();
    editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    const hides: boolean[] = [];
    c.follow(() => hides.push(c.moves()));
    time.tick();
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: -3, y: 0 });
    time.tick();
    expect(hides.at(-1)).toBe(true);
    c.moveEnd(false);
  });

  it("deletes the selection with Delete, and opens the one selected with Enter, on a board that may be changed", () => {
    const { c, time } = setUp();
    const key = (k: string) => ({
      key: k,
      code: k,
      shiftKey: false,
      ctrlKey: false,
      metaKey: false,
      altKey: false,
    });
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    c.setReady();
    time.tick();
    c.select(["nd_1"], "replace");
    expect(c.key(key("Delete"))).toBe(false);
    const asked = editsOf(c);
    expect(c.key(key("Enter"))).toBe(true);
    expect(asked.opened).toEqual(["nd_1"]);
    c.select(["nd_2"], "add");
    expect(c.key(key("Enter"))).toBe(false);
    expect(c.key(key("Backspace"))).toBe(true);
    expect(asked.removed).toEqual([["nd_1", "nd_2"]]);
  });
});

describe("the canvas's controller of a board's connections", () => {
  /** A board of two elements, a branch of two outputs, and a jumper, with a connection from the first element to the second. */
  function flowBoard() {
    const times = { rev: 1, created_at: "", updated_at: "" };
    const nodes = [
      testNode("nd_1", 0, 0),
      testNode("nd_2", 480, 0),
      testNode("nd_b", 0, 400, {
        type: "branch",
        outputs: [
          { id: "bo_1", node_id: "nd_b", kind: "if", order: "A", ...times },
          { id: "bo_2", node_id: "nd_b", kind: "else", ...times },
        ],
      }),
      testNode("nd_j", 480, 400, { type: "jumper" }),
    ];
    const connections = [testConnection("cn_1", "nd_1", "nd_2", { rev: 5 })];
    return { nodes, connections, doc: docOf(nodes, connections) };
  }

  function setUpFlow() {
    const s = setUp();
    const asked = editsOf(s.c);
    const board = flowBoard();
    s.c.sync(board.doc, false);
    s.c.setReady();
    s.time.tick();
    return { ...s, asked, board };
  }

  it("selects a connection in place of the nodes selected, and nodes in place of the connection, and draws it selected", () => {
    const { c, time, events, frames, host } = setUpFlow();
    c.select(["nd_1"], "replace");
    c.selectConnection("cn_1");
    expect(c.selection().size).toBe(0);
    expect(c.selectedConnection()).toBe("cn_1");
    expect(events.connections).toEqual(["cn_1"]);
    time.tick();
    expect(frames.at(-1)?.connection).toBe("cn_1");
    expect(host.dataset.connection).toBe("cn_1");
    const version = frames.at(-1)?.selectionVersion;
    c.select(["nd_2"], "replace");
    expect(c.selectedConnection()).toBeUndefined();
    expect(events.connections).toEqual(["cn_1", undefined]);
    time.tick();
    expect(frames.at(-1)?.connection).toBeUndefined();
    expect(frames.at(-1)?.selectionVersion).not.toBe(version);
    expect(host.dataset.connection).toBe("");
    // One that the board does not have is not selected.
    c.selectConnection("cn_none");
    expect(c.selectedConnection()).toBeUndefined();
    expect(c.selection().size).toBe(1);
  });

  it("finds a connection under a tap as it draws it, and none on a board that may not be changed, where none is selected either", () => {
    const { c, time } = setUpFlow();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    time.tick();
    // From 240, 64 to 480, 64.
    expect(c.connectionAt({ x: 360, y: 66 }, 4)).toBe("cn_1");
    c.selectConnection("cn_1");
    c.edits = undefined;
    expect(c.selectedConnection()).toBeUndefined();
    expect(c.connectionAt({ x: 360, y: 66 }, 4)).toBeUndefined();
    c.selectConnection("cn_1");
    expect(c.selectedConnection()).toBeUndefined();
  });

  it("deletes the connection selected with Delete, opens it with Enter, and clears it with Escape", () => {
    const { c, asked } = setUpFlow();
    c.selectConnection("cn_1");
    expect(c.key({ key: "Enter", code: "Enter" } as never)).toBe(true);
    expect(asked.openedConnections).toEqual(["cn_1"]);
    expect(c.key({ key: "Delete", code: "Delete" } as never)).toBe(true);
    expect(asked.removedConnections).toEqual(["cn_1"]);
    expect(asked.removed).toEqual([]);
    expect(c.key({ key: "Escape", code: "Escape" } as never)).toBe(true);
    expect(c.selectedConnection()).toBeUndefined();
    expect(c.key({ key: "Escape", code: "Escape" } as never)).toBe(false);
  });

  it("selects a connection no longer once the board no longer has it", () => {
    const { c, events, board } = setUpFlow();
    c.selectConnection("cn_1");
    c.sync(docOf(board.nodes, []), false);
    expect(c.selectedConnection()).toBeUndefined();
    expect(events.connections).toEqual(["cn_1", undefined]);
  });

  // Verifies: FR-EDIT-03/AC3
  it("connects a port to the node that its link's drag lands on, and offers to create a node where it lands on the empty board, showing its line until the offer ends", () => {
    const { c, time, asked, events, frames, host } = setUpFlow();
    const link = { kind: "new", node: "nd_1", port: "out" } as const;
    expect(c.linkStart(link)).toBe(true);
    expect(c.links()).toBe(true);
    // Over the empty board, the line goes from the port to the pointer.
    c.linkTo({ x: 300, y: 300 }, 0);
    time.tick();
    expect(host.dataset.linking).toBe("true");
    expect(frames.at(-1)?.link).toMatchObject({
      p0: { x: 240, y: 64 },
      p3: { x: 300, y: 300 },
    });
    // Over a node, it enters the node, at the middle of its left edge.
    c.linkTo({ x: 600, y: 420 }, 0);
    time.tick();
    expect(frames.at(-1)?.link).toMatchObject({ p3: { x: 480, y: 436 } });
    c.linkEnd(true);
    expect(asked.connected).toEqual([
      { from: { node: "nd_1", port: "out" }, to: "nd_j" },
    ]);
    time.tick();
    expect(frames.at(-1)?.link).toBeUndefined();
    expect(host.dataset.linking).toBe("false");
    // Dropped on the empty board, it connects nothing, and offers a node
    // there, whose line stays until the page ends the offer.
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 300, y: 300 }, 0);
    c.linkEnd(true);
    expect(asked.connected).toHaveLength(1);
    const offer = {
      from: { node: "nd_1", port: "out" },
      at: { x: 300, y: 300 },
    };
    expect(events.offers).toEqual([offer]);
    time.tick();
    expect(frames.at(-1)?.link).toMatchObject({ p3: { x: 300, y: 300 } });
    c.endOffer();
    expect(events.offers).toEqual([offer, undefined]);
    time.tick();
    expect(frames.at(-1)?.link).toBeUndefined();
    // Cancelled, it asks for nothing.
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 300, y: 300 }, 0);
    c.linkEnd(false);
    expect(events.offers).toHaveLength(2);
    // A node may connect to itself, by a loop.
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 100, y: 64 }, 0);
    c.linkEnd(true);
    expect(asked.connected.at(-1)).toEqual({
      from: { node: "nd_1", port: "out" },
      to: "nd_1",
    });
  });

  it("tells the page that it offers nothing once the next link starts, or the board may no longer be changed, so that the page shows no offer without its line", () => {
    const { c, time, events, frames } = setUpFlow();
    const link = { kind: "new", node: "nd_1", port: "out" } as const;
    const offer = {
      from: { node: "nd_1", port: "out" },
      at: { x: 300, y: 300 },
    };
    expect(c.linkStart(link)).toBe(true);
    c.linkTo(offer.at, 0);
    c.linkEnd(true);
    expect(events.offers).toEqual([offer]);
    // The next link ends the offer before it.
    expect(c.linkStart(link)).toBe(true);
    expect(events.offers).toEqual([offer, undefined]);
    c.linkTo(offer.at, 0);
    c.linkEnd(true);
    expect(events.offers).toEqual([offer, undefined, offer]);
    c.edits = undefined;
    expect(events.offers).toEqual([offer, undefined, offer, undefined]);
    time.tick();
    expect(frames.at(-1)?.link).toBeUndefined();
  });

  it("connects nothing, and offers nothing, where a link is let go outside the canvas, whose node there is not shown", () => {
    const { c, time, asked, events, frames } = setUpFlow();
    // The canvas, 1000 wide, shows the board up to 500 at twice its size:
    // the second element, from 480, is under its edge, and beyond it.
    c.setViewport({ x: 0, y: 0, zoom: 2 });
    time.tick();
    const link = { kind: "new", node: "nd_1", port: "out" } as const;
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 600, y: 64 }, 0);
    time.tick();
    // The line goes to the pointer, and enters no node there.
    expect(frames.at(-1)?.link).toMatchObject({ p3: { x: 600, y: 64 } });
    c.linkEnd(true);
    expect(asked.connected).toEqual([]);
    expect(events.offers).toEqual([]);
    // Nor over the empty board beyond the canvas, above it.
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 300, y: -40 }, 0);
    c.linkEnd(true);
    expect(events.offers).toEqual([]);
    // An end of a connection let go there stays as it was.
    expect(c.linkStart({ kind: "to", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 600, y: 420 }, 0);
    c.linkEnd(true);
    expect(asked.reconnected).toEqual([]);
    // Back on the canvas, the node under the pointer takes it.
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 600, y: 64 }, 0);
    c.linkTo({ x: 490, y: 64 }, 0);
    c.linkEnd(true);
    expect(asked.connected).toEqual([
      { from: { node: "nd_1", port: "out" }, to: "nd_2" },
    ]);
  });

  it("connects nothing, and offers nothing, where a connection to come is let go on the handle that it left, and makes a loop only beyond it", () => {
    const { c, time, asked, events } = setUpFlow();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    time.tick();
    // A finger's handle, 44 px about the port at 240, 64.
    const link = { kind: "new", node: "nd_1", port: "out", side: 44 } as const;
    // On the half of the handle that is over the node, which a tap's slop
    // would else find, and on the half beside it, over the empty board.
    for (const at of [
      { x: 230, y: 70 },
      { x: 262, y: 42 },
    ]) {
      expect(c.linkStart(link)).toBe(true);
      c.linkTo(at, 12);
      c.linkEnd(true);
    }
    expect(asked.connected).toEqual([]);
    expect(events.offers).toEqual([]);
    // Zoomed in, the handle covers less of the board.
    c.setViewport({ x: 0, y: 0, zoom: 2 });
    time.tick();
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 225, y: 64 }, 6);
    c.linkEnd(true);
    expect(asked.connected).toEqual([
      { from: { node: "nd_1", port: "out" }, to: "nd_1" },
    ]);
    // Beside the handle, over the empty board, it offers a node.
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    time.tick();
    expect(c.linkStart(link)).toBe(true);
    c.linkTo({ x: 300, y: 64 }, 12);
    c.linkEnd(true);
    expect(events.offers).toHaveLength(1);
  });

  it("leaves an end of a connection as it was where it is let go on the handle that it left, though the row of another output is nearer there, or another node is under it", () => {
    const { c, time, asked, board } = setUpFlow();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    time.tick();
    // A connection that leaves the branch by its first output, at 240, 470,
    // and a node whose edge is 10 units from where the first connection
    // enters the second element, at 480, 64.
    c.sync(
      docOf(
        [...board.nodes, testNode("nd_3", 230, 0)],
        [
          ...board.connections,
          testConnection("cn_2", "nd_b", "nd_2", { from_port: "bo_1", rev: 3 }),
        ],
      ),
      false,
    );
    // A finger's handle, 44 px about the end: the row of the second output,
    // at 498, is the nearer 20 units below the first's, within the handle.
    const from = { kind: "from", connection: "cn_2", side: 44 } as const;
    expect(c.linkStart(from)).toBe(true);
    c.linkTo({ x: 236, y: 490 }, 12);
    c.linkEnd(true);
    expect(asked.reconnected).toEqual([]);
    // Beyond the handle, the output of the nearest row takes it.
    expect(c.linkStart(from)).toBe(true);
    c.linkTo({ x: 236, y: 496 }, 12);
    c.linkEnd(true);
    expect(asked.reconnected).toEqual([
      {
        id: "cn_2",
        ends: { from: { node: "nd_b", port: "bo_2" } },
        rev: 3,
      },
    ]);
    // The end that enters a node, let go on its handle over the node beside
    // it, and then beyond the handle, on that node.
    const to = { kind: "to", connection: "cn_1", side: 44 } as const;
    expect(c.linkStart(to)).toBe(true);
    c.linkTo({ x: 465, y: 64 }, 12);
    c.linkEnd(true);
    expect(asked.reconnected).toHaveLength(1);
    expect(c.linkStart(to)).toBe(true);
    c.linkTo({ x: 440, y: 64 }, 12);
    c.linkEnd(true);
    expect(asked.reconnected.at(-1)).toEqual({
      id: "cn_1",
      ends: { to: "nd_3" },
      rev: 5,
    });
  });

  it("offers nothing once the board no longer has the port that the offer would connect, as of an output, or of a node, that another session deleted", () => {
    const { c, time, events, frames, board } = setUpFlow();
    const link = { kind: "new", node: "nd_b", port: "bo_1" } as const;
    const offer = {
      from: { node: "nd_b", port: "bo_1" },
      at: { x: 300, y: 300 },
    };
    expect(c.linkStart(link)).toBe(true);
    c.linkTo(offer.at, 0);
    c.linkEnd(true);
    expect(events.offers).toEqual([offer]);
    // Another node's going leaves the offer.
    const rest = board.nodes.filter((n) => n.id !== "nd_j");
    c.sync(docOf(rest, board.connections), false);
    expect(events.offers).toEqual([offer]);
    // The branch's output goes, and the offer, and its line, with it.
    c.sync(
      docOf(
        rest.map((n) =>
          n.id === "nd_b"
            ? { ...n, outputs: n.outputs?.filter((o) => o.id !== "bo_1") }
            : n,
        ),
        board.connections,
      ),
      false,
    );
    expect(events.offers).toEqual([offer, undefined]);
    time.tick();
    expect(frames.at(-1)?.link).toBeUndefined();
    // An offer of a node's port ends as the node goes.
    const second = { ...offer, from: { node: "nd_1", port: "out" } };
    c.sync(docOf(rest, board.connections), false);
    expect(c.linkStart({ kind: "new", node: "nd_1", port: "out" })).toBe(true);
    c.linkTo(second.at, 0);
    c.linkEnd(true);
    expect(events.offers).toEqual([offer, undefined, second]);
    c.sync(
      docOf(
        rest.filter((n) => n.id !== "nd_1"),
        [],
      ),
      false,
    );
    expect(events.offers).toEqual([offer, undefined, second, undefined]);
  });

  it("starts no link of a port that its node does not have, of a jumper, while another drag is under way, or on a board that may not be changed", () => {
    const { c } = setUpFlow();
    expect(c.linkStart({ kind: "new", node: "nd_j", port: "out" })).toBe(false);
    expect(c.linkStart({ kind: "new", node: "nd_b", port: "bo_9" })).toBe(
      false,
    );
    expect(c.linkStart({ kind: "new", node: "nd_9", port: "out" })).toBe(false);
    expect(c.linkStart({ kind: "to", connection: "cn_9" })).toBe(false);
    c.select(["nd_1"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    expect(c.linkStart({ kind: "new", node: "nd_1", port: "out" })).toBe(false);
    c.moveEnd(false);
    expect(c.linkStart({ kind: "new", node: "nd_b", port: "bo_2" })).toBe(true);
    // The board may no longer be changed: the link ends, cancelled.
    c.gestures.down(
      {
        id: 1,
        x: 0,
        y: 0,
        kind: "mouse",
        button: 0,
        buttons: 1,
        shift: false,
        additive: false,
      },
      { kind: "new", node: "nd_1", port: "out" },
    );
    c.edits = undefined;
    expect(c.links()).toBe(true);
    c.linkEnd(false);
    expect(c.linkStart({ kind: "new", node: "nd_1", port: "out" })).toBe(false);
  });

  it("reconnects the end of a connection that its handle's drag takes to another node, or to another port, expecting its revision as the drag started, and leaves it as it was elsewhere", () => {
    const { c, asked, events, board } = setUpFlow();
    // The end that enters a node, to another node.
    expect(c.linkStart({ kind: "to", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 600, y: 420 }, 0);
    // The connection changes elsewhere while the drag is under way.
    c.sync(
      docOf(board.nodes, [testConnection("cn_1", "nd_1", "nd_2", { rev: 6 })]),
      false,
    );
    c.linkEnd(true);
    expect(asked.reconnected).toEqual([
      { id: "cn_1", ends: { to: "nd_j" }, rev: 5 },
    ]);
    // On its own node, or on the empty board, it stays, and offers nothing.
    expect(c.linkStart({ kind: "to", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 500, y: 64 }, 0);
    c.linkEnd(true);
    expect(c.linkStart({ kind: "to", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 300, y: 300 }, 0);
    c.linkEnd(true);
    expect(asked.reconnected).toHaveLength(1);
    expect(events.offers).toEqual([]);
    // The end that leaves a node, to the output of a branch whose row is
    // nearest the pointer: the first's at 470, the second's at 498.
    expect(c.linkStart({ kind: "from", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 100, y: 515 }, 0);
    c.linkEnd(true);
    expect(asked.reconnected.at(-1)).toEqual({
      id: "cn_1",
      ends: { from: { node: "nd_b", port: "bo_2" } },
      rev: 6,
    });
    // A jumper, which no connection leaves, takes none, nor its own port.
    expect(c.linkStart({ kind: "from", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 600, y: 420 }, 0);
    c.linkEnd(true);
    expect(c.linkStart({ kind: "from", connection: "cn_1" })).toBe(true);
    c.linkTo({ x: 100, y: 64 }, 0);
    c.linkEnd(true);
    expect(asked.reconnected).toHaveLength(2);
  });

  it("connects nothing to a node that went while the link was under way, nor from one", () => {
    const { c, asked, events, board } = setUpFlow();
    expect(c.linkStart({ kind: "new", node: "nd_1", port: "out" })).toBe(true);
    c.linkTo({ x: 600, y: 64 }, 0);
    c.sync(
      docOf(
        board.nodes.filter((n) => n.id !== "nd_2"),
        [],
      ),
      false,
    );
    c.linkEnd(true);
    expect(asked.connected).toEqual([]);
    expect(events.offers).toEqual([]);
    expect(c.linkStart({ kind: "new", node: "nd_1", port: "out" })).toBe(true);
    c.linkTo({ x: 600, y: 420 }, 0);
    c.sync(
      docOf(
        board.nodes.filter((n) => n.id !== "nd_1" && n.id !== "nd_2"),
        [],
      ),
      false,
    );
    c.linkEnd(true);
    expect(asked.connected).toEqual([]);
  });

  it("tells how many of the board's jumpers are broken, as the page says of each", () => {
    const { c, time, host } = setUpFlow();
    expect(host.dataset.broken).toBe("0");
    c.broken = (node) => node.id === "nd_j";
    c.relabel();
    time.tick();
    expect(host.dataset.broken).toBe("1");
  });
});

describe("the canvas's controller of the context menu and the accessibility tree", () => {
  function setUpBoard(editable = true) {
    const s = setUp();
    const asked = editable ? editsOf(s.c) : undefined;
    const nodes = [
      testNode("nd_1", 0, 0),
      testNode("nd_2", 480, 0),
      testNode("nd_3", 480, 400),
    ];
    const connections = [testConnection("cn_1", "nd_1", "nd_2")];
    s.c.sync(docOf(nodes, connections), false);
    s.c.setReady();
    s.time.tick();
    return { ...s, asked, nodes, connections };
  }

  // Verifies: NFR-TOUCH-02
  it("opens the context menu of the selection that holds the node asked for, and else of that node alone, which it selects, and of a connection, which it selects too", () => {
    const { c, events } = setUpBoard();
    c.select(["nd_1", "nd_2"], "replace");
    const at = { x: 10, y: 10 };
    c.menu({ kind: "node", node: "nd_2", at }, true);
    expect([...c.selection()]).toEqual(["nd_1", "nd_2"]);
    c.menu({ kind: "node", node: "nd_3", at });
    expect([...c.selection()]).toEqual(["nd_3"]);
    c.menu({ kind: "connection", connection: "cn_1", at });
    expect(c.selectedConnection()).toBe("cn_1");
    expect(c.selection().size).toBe(0);
    c.menu({ kind: "board", at });
    expect(c.selectedConnection()).toBe("cn_1");
    expect(events.menus).toEqual([
      { target: { kind: "node", node: "nd_2", at }, held: true },
      { target: { kind: "node", node: "nd_3", at }, held: false },
      { target: { kind: "connection", connection: "cn_1", at }, held: false },
      { target: { kind: "board", at }, held: false },
    ]);
    // None of what the board no longer has.
    c.menu({ kind: "node", node: "nd_gone", at });
    c.menu({ kind: "connection", connection: "cn_gone", at });
    expect(events.menus).toHaveLength(4);
  });

  it("opens no menu of a connection on a board that may not be changed, which selects none", () => {
    const { c, events } = setUpBoard(false);
    c.menu({ kind: "connection", connection: "cn_1", at: { x: 0, y: 0 } });
    expect(events.menus).toEqual([]);
    c.menu({ kind: "node", node: "nd_1", at: { x: 0, y: 0 } });
    expect(events.menus).toHaveLength(1);
    expect([...c.selection()]).toEqual(["nd_1"]);
  });

  // Verifies: NFR-A11Y-04
  it("opens the menu that the keys ask for of the connection selected, of the nodes selected, or of the board, over the canvas", () => {
    const { c, events } = setUpBoard();
    c.setViewport({ x: 0, y: 0, zoom: 1 });
    c.menuOfSelection();
    expect(events.menus.at(-1)?.target).toEqual({
      kind: "board",
      at: { x: 500, y: 300 },
    });
    c.select(["nd_2", "nd_3"], "replace");
    c.menuOfSelection();
    // The middle of the two nodes, 480 to 720 across and 0 to 528 down.
    expect(events.menus.at(-1)?.target).toEqual({
      kind: "node",
      node: "nd_2",
      at: { x: 600, y: 264 },
    });
    expect(c.selection().size).toBe(2);
    c.selectConnection("cn_1");
    c.menuOfSelection();
    const target = events.menus.at(-1)?.target;
    expect(target?.kind).toBe("connection");
    expect(target?.at.x).toBeGreaterThan(240);
    expect(target?.at.x).toBeLessThan(480);
    // What is selected out of view has its menu at the canvas's centre.
    c.select(["nd_3"], "replace");
    c.setViewport({ x: 5000, y: 5000, zoom: 1 });
    c.menuOfSelection();
    expect(events.menus.at(-1)?.target).toEqual({
      kind: "node",
      node: "nd_3",
      at: { x: 5500, y: 5300 },
    });
  });

  it("shows what the tree's focus is on, moving only where the canvas does not show it whole, at the zoom that it is at", () => {
    const { c, time } = setUpBoard();
    c.reducedMotion = true;
    c.setViewport({ x: 0, y: 0, zoom: 0.5 });
    time.tick();
    // In view, with room around it: nothing moves.
    c.reveal({ x: 480, y: 400, width: 240, height: 128 });
    expect(c.viewport()).toEqual({ x: 0, y: 0, zoom: 0.5 });
    // Out of view: its middle goes to the canvas's centre.
    c.reveal({ x: 4000, y: 3000, width: 240, height: 128 });
    expect(c.viewport().zoom).toBe(0.5);
    expect(c.viewport().x).toBeCloseTo(4120 - 1000);
    expect(c.viewport().y).toBeCloseTo(3064 - 600);
    expect(
      c.screenRectOf({ x: 4000, y: 3000, width: 240, height: 128 }),
    ).toEqual({ x: 440, y: 268, width: 120, height: 64 });
  });

  it("tells what watches the board's content of each change of it, and of what its cards say, but not of the moves of a drag, whose end it tells of", () => {
    const { c, nodes, connections } = setUpBoard();
    let told = 0;
    const stop = c.watch(() => {
      told++;
    });
    c.sync(
      docOf(
        [...nodes.slice(0, 2), testNode("nd_3", 480, 400, { title: "Quay" })],
        connections,
      ),
      false,
    );
    expect(told).toBe(1);
    c.relabel();
    expect(told).toBe(2);
    c.select(["nd_1"], "replace");
    expect(c.moveStart("nd_1")).toBe(true);
    c.moveTo({ x: 16, y: 0 });
    c.moveTo({ x: 32, y: 0 });
    expect(told).toBe(2);
    c.moveEnd(false);
    expect(told).toBe(3);
    stop();
    c.relabel();
    expect(told).toBe(3);
  });
});
