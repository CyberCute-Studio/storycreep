import { describe, expect, it } from "vitest";
import { docOf, gridNodes, testNode } from "../test/canvas";
import { animationTime, CanvasController, type Clock } from "./controller";
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
  };
  const c = new CanvasController(
    "bd_a",
    {
      selection: (ids) => events.selections.push(ids.size),
      zoom: (z) => events.zooms.push(z),
      rendered: () => {
        events.rendered++;
      },
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

  it("keeps the board's centre where it is through a size of none, as of a host that the page does not display", () => {
    const { c, time } = setUp();
    placed(c, time);
    c.setViewport({ x: 100, y: 100, zoom: 2 });
    c.setSize({ width: 0, height: 0 }, 1);
    c.setSize({ width: 600, height: 1000 }, 1);
    expect(c.viewport().x + 300 / 2).toBeCloseTo(350);
    expect(c.viewport().y + 500 / 2).toBeCloseTo(250);
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
    expect(c.pointer("down", right)).toBe(false);
    time.tick(animationTime);
    expect(c.viewport().zoom).toBe(2);
    c.zoomTo(1);
    time.tick();
    expect(c.pointer("down", { ...right, button: 0, buttons: 1 })).toBe(true);
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
