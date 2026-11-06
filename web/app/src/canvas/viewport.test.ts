import { describe, expect, it } from "vitest";
import {
  between,
  clampZoom,
  fitTo,
  maxZoom,
  minZoom,
  panBy,
  stepZoom,
  toScreen,
  toWorld,
  zoomAt,
} from "./viewport";

describe("the viewport", () => {
  it("maps the board to the screen and back", () => {
    const v = { x: 100, y: -50, zoom: 2 };
    const p = { x: 130, y: 0 };
    expect(toScreen(v, p)).toEqual({ x: 60, y: 100 });
    expect(toWorld(v, toScreen(v, p))).toEqual(p);
  });

  // Verifies: FR-EDIT-01/AC4
  it("zooms from 10% to 400%, and no further", () => {
    expect(clampZoom(0.01)).toBe(minZoom);
    expect(clampZoom(10)).toBe(maxZoom);
    expect(minZoom).toBe(0.1);
    expect(maxZoom).toBe(4);
    expect(clampZoom(Number.NaN)).toBe(1);
  });

  it("keeps the point under the pointer where it is as it zooms", () => {
    const v = { x: 10, y: 20, zoom: 1 };
    const at = { x: 300, y: 200 };
    const before = toWorld(v, at);
    for (const zoom of [0.1, 0.37, 1.5, 4, 9]) {
      const after = toWorld(zoomAt(v, at, zoom), at);
      expect(after.x).toBeCloseTo(before.x, 9);
      expect(after.y).toBeCloseTo(before.y, 9);
    }
    expect(zoomAt(v, at, 9).zoom).toBe(4);
  });

  it("moves the board with a drag", () => {
    const v = panBy({ x: 0, y: 0, zoom: 2 }, 100, -40);
    expect(v).toEqual({ x: -50, y: 20, zoom: 2 });
  });

  it("fits a rectangle whole, centred, up to 100%", () => {
    const size = { width: 1000, height: 600 };
    const v = fitTo({ x: 0, y: 0, width: 4000, height: 1000 }, size, 50);
    expect(v.zoom).toBeCloseTo(900 / 4000);
    const centre = toScreen(v, { x: 2000, y: 500 });
    expect(centre.x).toBeCloseTo(500);
    expect(centre.y).toBeCloseTo(300);
    expect(fitTo({ x: 0, y: 0, width: 10, height: 10 }, size).zoom).toBe(1);
  });

  it("steps through round zooms", () => {
    expect(stepZoom(1, 1)).toBe(1.25);
    expect(stepZoom(1, -1)).toBe(0.75);
    expect(stepZoom(0.9, -1)).toBe(0.75);
    expect(stepZoom(4, 1)).toBe(4);
    expect(stepZoom(0.1, -1)).toBe(0.1);
  });

  it("animates from one viewport to another, landing on it", () => {
    const size = { width: 800, height: 600 };
    const a = { x: 0, y: 0, zoom: 1 };
    const b = { x: 5000, y: 2000, zoom: 0.25 };
    const end = between(a, b, 1, size);
    expect(end.zoom).toBeCloseTo(b.zoom);
    expect(end.x).toBeCloseTo(b.x);
    expect(end.y).toBeCloseTo(b.y);
    const mid = between(a, b, 0.5, size);
    expect(mid.zoom).toBeLessThan(1);
    expect(mid.zoom).toBeGreaterThan(0.25);
  });
});
