import { describe, expect, it } from "vitest";
import { docOf, gridNodes, testNode } from "../test/canvas";
import {
  animationTime,
  CanvasController,
  type CanvasView,
  type Clock,
} from "./controller";
import type { Engine, Frame, FrameStats } from "./engine";
import type { Theme } from "./theme";

const theme: Theme = {
  canvas: 0xffffff,
  surface: 0xf3f4f6,
  border: 0x7c8494,
  fg: 0x15171c,
  muted: 0x4b5260,
  accent: 0x2b50c8,
  dark: false,
};

/** A clock whose frames the test runs, and whose time it sets. */
function fakeClock() {
  let now = 0;
  let next = 1;
  const frames = new Map<number, (time: number) => void>();
  const clock: Clock = {
    request: (frame) => {
      frames.set(next, frame);
      return next++;
    },
    cancel: (handle) => frames.delete(handle),
    now: () => now,
  };
  return {
    clock,
    /** Moves the time on by ms and runs the frames that were asked for. */
    tick(ms = 16) {
      now += ms;
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
  };
  const c = new CanvasController(
    "bd_a",
    {
      selection: (ids) => events.selections.push(ids.size),
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

  it("starts no resize while another is under way, as of a second pointer on the handle", () => {
    const { c, time } = setUp();
    editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    expect(c.resizeStart("nd_1")).toBe(true);
    expect(c.resizeStart("nd_1")).toBe(false);
    c.resizeEnd(false);
    expect(c.resizeStart("nd_1")).toBe(true);
    c.resizeEnd(false);
  });

  it("starts no move while a node's handle resizes it, as under another finger, whose preview the move's would take the place of", () => {
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

  it("keeps selected the nodes that the page's batches create until they come, though the scene changes before, and drops those that never came once they settle", async () => {
    const { c, time } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    let settle = () => {};
    const done = new Promise<void>((resolve) => {
      settle = resolve;
    });
    c.selectComing(["nd_2", "nd_3"], done);
    expect(c.selection().size).toBe(2);
    // The first batch shows its copy, and the scene changes, before the
    // second shows its own.
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 400, 0)]), false);
    expect([...c.selection()]).toEqual(["nd_2", "nd_3"]);
    // The second batch was refused.
    settle();
    await done;
    expect([...c.selection()]).toEqual(["nd_2"]);
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

  it("resizes a node by its handle's move, in pixels of the canvas at its zoom, and asks for its size once the handle is let go", () => {
    const { c, time } = setUp();
    const asked = editsOf(c);
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setReady();
    time.tick();
    c.setViewport({ x: 0, y: 0, zoom: 2 });
    expect(c.resizeStart("nd_1")).toBe(true);
    // 60 pixels at 200% are 30 units, which snap to 32.
    c.resizeTo({ x: 60, y: 0 });
    expect(c.scene.nodes.rect("nd_1")).toEqual({
      x: 0,
      y: 0,
      width: 272,
      height: 128,
    });
    c.resizeEnd(true);
    expect(asked.sizes).toEqual([
      { id: "nd_1", size: { width: 272, height: 128 } },
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
    });
  });

  it("opens on a node that a link names, selected and shown whole, in place of the view that it would restore", () => {
    const { c, time, events } = setUp();
    c.restore({
      viewport: { x: 100, y: 50, zoom: 2 },
      size: { width: 1000, height: 600 },
      selection: [],
    });
    c.focus("nd_2");
    c.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 4000, 0)]), false);
    c.setReady();
    time.tick();
    expect([...c.selection()]).toEqual(["nd_2"]);
    expect(events.selections.at(-1)).toBe(1);
    // At 100%, the node's centre at the canvas's.
    expect(c.viewport()).toEqual({ x: 4000 + 120 - 500, y: 64 - 300, zoom: 1 });
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
