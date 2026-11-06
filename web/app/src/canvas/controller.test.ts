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

/** An engine that records the frames that it is asked to draw. */
function fakeEngine() {
  const frames: Frame[] = [];
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
        rebuilt: true,
      };
    },
    textures: () => 4,
    destroy: () => {},
  };
  return { engine, frames };
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
  const { engine, frames } = fakeEngine();
  c.setEngine(engine);
  c.setTheme(theme);
  c.setSize({ width: 1000, height: 600 }, 1);
  return { c, time, events, host, band, frames };
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

  it("writes every card anew once a name that cards show changes, but not for a node that moves", () => {
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
    const { c } = setUp();
    c.sync(docOf([testNode("nd_1", 0, 0)]), false);
    c.setViewport({ x: 100, y: 100, zoom: 2 });
    c.setSize({ width: 600, height: 1000 }, 2);
    // The centre was 100 + 500 / 2, 100 + 300 / 2.
    expect(c.viewport().x + 300 / 2).toBeCloseTo(350);
    expect(c.viewport().y + 500 / 2).toBeCloseTo(250);
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
