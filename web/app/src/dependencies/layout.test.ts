import { describe, expect, it } from "vitest";
import { type Layout, type LayoutSizes, layoutGraph } from "./layout";

// The layout of a project's dependency graph (PRD FR-DEP-02, plan note N77).

const sizes: LayoutSizes = {
  width: 100,
  height: 40,
  gap: 20,
  rowGap: 30,
  perRow: 4,
  margin: 10,
};

/** Returns the box of id in layout. */
function boxOf(layout: Layout, id: string) {
  const box = layout.boxes.find((b) => b.id === id);
  if (box === undefined) {
    throw new Error(`no box of ${id}`);
  }
  return box;
}

/** Reports whether any two boxes of layout overlap. */
function overlapping(layout: Layout): boolean {
  return layout.boxes.some((a, i) =>
    layout.boxes
      .slice(i + 1)
      .some(
        (b) =>
          a.x < b.x + sizes.width &&
          b.x < a.x + sizes.width &&
          a.y < b.y + sizes.height &&
          b.y < a.y + sizes.height,
      ),
  );
}

/** Returns the numbers of a path's data. */
function numbersOf(path: string): number[] {
  return (path.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);
}

describe("the dependency graph's layout", () => {
  it("puts each board below the boards that it depends on", () => {
    const layout = layoutGraph(
      ["a", "b", "c", "d"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "c" },
        { from: "a", to: "c" },
        { from: "d", to: "c" },
      ],
      sizes,
    );
    expect(boxOf(layout, "a").layer).toBe(0);
    expect(boxOf(layout, "d").layer).toBe(0);
    expect(boxOf(layout, "b").layer).toBe(1);
    expect(boxOf(layout, "c").layer).toBe(2);
    expect(boxOf(layout, "b").y).toBeGreaterThan(boxOf(layout, "a").y);
    expect(boxOf(layout, "c").y).toBeGreaterThan(boxOf(layout, "b").y);
    expect(layout.routes.map((r) => r.up)).toEqual([
      false,
      false,
      false,
      false,
    ]);
    expect(overlapping(layout)).toBe(false);
  });

  // Verifies: FR-DEP-02/AC4
  it("lays out cycles as any other graph, the edge that closes one going up", () => {
    const layout = layoutGraph(
      ["a", "b", "c", "x", "y"],
      [
        { from: "a", to: "b" },
        { from: "b", to: "c" },
        { from: "c", to: "a" },
        // A cycle of two boards, each depending on the other.
        { from: "x", to: "y" },
        { from: "y", to: "x" },
      ],
      sizes,
    );
    expect(layout.boxes).toHaveLength(5);
    for (const box of layout.boxes) {
      expect(Number.isFinite(box.x) && Number.isFinite(box.y)).toBe(true);
      expect(box.layer).toBeGreaterThanOrEqual(0);
    }
    expect(overlapping(layout)).toBe(false);
    const up = layout.routes.filter((r) => r.up);
    expect(up.map((r) => `${r.from}>${r.to}`)).toEqual(["c>a", "y>x"]);
    // Every route ends where it says, and those of x and y, which join the
    // same two boards both ways, are drawn apart.
    for (const r of layout.routes) {
      expect(numbersOf(r.path).every(Number.isFinite)).toBe(true);
    }
    const xy = layout.routes.find((r) => r.from === "x" && r.to === "y");
    const yx = layout.routes.find((r) => r.from === "y" && r.to === "x");
    expect(xy?.path).not.toBe(yx?.path);
    // The graph is within its size, with the margin that up-going routes
    // curve into on the right.
    for (const box of layout.boxes) {
      expect(box.x + sizes.width).toBeLessThanOrEqual(layout.width);
      expect(box.y + sizes.height).toBeLessThanOrEqual(layout.height);
    }
  });

  it("leaves out edges of a board to itself, of a board it lacks, and repeated", () => {
    const layout = layoutGraph(
      ["a", "b"],
      [
        { from: "a", to: "a" },
        { from: "a", to: "gone" },
        { from: "a", to: "b" },
        { from: "a", to: "b" },
      ],
      sizes,
    );
    expect(layout.routes.map((r) => `${r.from}>${r.to}`)).toEqual(["a>b"]);
  });

  it("wraps a wide layer into rows, and puts the boards that no edge joins after the layers", () => {
    const sources = Array.from({ length: 6 }, (_, i) => `s${i}`);
    const layout = layoutGraph(
      [...sources, "t", "alone1", "alone2"],
      sources.map((s) => ({ from: s, to: "t" })),
      sizes,
    );
    const rows = [...new Set(layout.boxes.map((b) => b.y))].sort(
      (a, b) => a - b,
    );
    // Four sources, two, the target, and the two boards that no edge joins.
    expect(rows).toHaveLength(4);
    const at = (y: number) => layout.boxes.filter((b) => b.y === y);
    expect(at(rows[0])).toHaveLength(4);
    expect(at(rows[1])).toHaveLength(2);
    expect(at(rows[2]).map((b) => b.id)).toEqual(["t"]);
    expect(at(rows[3]).map((b) => b.id)).toEqual(["alone1", "alone2"]);
    expect(boxOf(layout, "alone1").layer).toBe(-1);
    expect(overlapping(layout)).toBe(false);
  });

  it("lays out the same graph alike, from the boards' order", () => {
    const boards = Array.from({ length: 60 }, (_, i) => `b${i}`);
    const edges = boards.flatMap((b, i) => [
      { from: b, to: boards[(i + 1) % 60] },
      { from: b, to: boards[(i * 7 + 3) % 60] },
    ]);
    const first = layoutGraph(boards, edges, sizes);
    expect(layoutGraph(boards, edges, sizes)).toEqual(first);
    expect(first.boxes).toHaveLength(60);
    expect(overlapping(first)).toBe(false);
  });

  it("lays out nothing for no boards", () => {
    expect(layoutGraph([], [], sizes)).toEqual({
      boxes: [],
      routes: [],
      width: 0,
      height: 0,
    });
  });
});
