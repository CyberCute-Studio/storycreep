import { describe, expect, it } from "vitest";
import { testNode } from "../test/canvas";
import {
  aligned,
  distributed,
  freePlace,
  hasTag,
  minSize,
  movedBy,
  type Placed,
  positionLimit,
  resizedBy,
  round2,
  snapped,
  tagsOf,
  withoutTag,
  withTag,
} from "./edit";

/** Returns a node at x, y of width and height, as align and distribute take it. */
function at(
  id: string,
  x: number,
  y: number,
  width = 240,
  height = 128,
): Placed {
  return { id, rect: { x, y, width, height } };
}

describe("the canvas's edits", () => {
  // Verifies: FR-EDIT-02/AC4
  it("rounds a position to 2 decimal places, half away from zero, as the server keeps it", () => {
    expect(round2(0.125)).toBe(0.13);
    expect(round2(-0.125)).toBe(-0.13);
    expect(round2(12.344)).toBe(12.34);
    expect(round2(-0.001)).toBe(0);
    expect(Object.is(round2(-0.001), -0)).toBe(false);
  });

  it("snaps to the grid of 8 units, or keeps the place as it is, rounded, where it does not snap", () => {
    expect(snapped(13, true)).toBe(16);
    expect(snapped(11.9, true)).toBe(8);
    expect(snapped(-13, true)).toBe(-16);
    expect(snapped(13.456, false)).toBe(13.46);
  });

  // Verifies: FR-EDIT-02/AC2
  it("moves every node of a selection by as much as the one that the drag holds, on the grid, so that they keep their places relative to it", () => {
    const start = new Map([
      ["nd_a", { x: 3, y: 5 }],
      ["nd_b", { x: 403, y: 205 }],
    ]);
    // The held node, at 3, 5, goes to 3 + 30, 5 + 29, which snaps to 32, 32.
    const moved = movedBy(start, { x: 3, y: 5 }, { x: 30, y: 29 }, true);
    expect(moved).toEqual(
      new Map([
        ["nd_a", { x: 32, y: 32 }],
        ["nd_b", { x: 432, y: 232 }],
      ]),
    );
    // Without the grid, by the delta itself.
    expect(movedBy(start, { x: 3, y: 5 }, { x: 30.004, y: -1 }, false)).toEqual(
      new Map([
        ["nd_a", { x: 33, y: 4 }],
        ["nd_b", { x: 433, y: 204 }],
      ]),
    );
    // A drag that comes back where it started moves none.
    expect(movedBy(start, { x: 3, y: 5 }, { x: 1, y: -1 }, false).size).toBe(2);
    expect(movedBy(start, { x: 8, y: 8 }, { x: 1, y: -2 }, true).size).toBe(0);
  });

  it("keeps a move within the board's limits", () => {
    const start = new Map([["nd_a", { x: positionLimit - 8, y: 0 }]]);
    const moved = movedBy(start, { x: 0, y: 0 }, { x: 800, y: 0 }, true);
    expect(moved.get("nd_a")).toEqual({ x: positionLimit, y: 0 });
  });

  it("resizes by the corner's move, on the grid, but no smaller than a line of the title needs", () => {
    expect(
      resizedBy({ width: 240, height: 128 }, { x: 29, y: -3 }, true),
    ).toEqual({ width: 272, height: 128 });
    expect(
      resizedBy({ width: 240, height: 128 }, { x: -500, y: -500 }, true),
    ).toEqual(minSize);
    expect(
      resizedBy({ width: 240, height: 128 }, { x: 0.556, y: 0 }, false),
    ).toEqual({ width: 240.56, height: 128 });
  });

  it("aligns the nodes' edges, centres, and middles, moving only those that are not there", () => {
    const nodes = [
      at("nd_a", 0, 0),
      at("nd_b", 400, 100, 100, 50),
      at("nd_c", 100, 300),
    ];
    expect(aligned(nodes, "left")).toEqual(
      new Map([
        ["nd_b", { x: 0, y: 100 }],
        ["nd_c", { x: 0, y: 300 }],
      ]),
    );
    // The right edge of the rightmost, 500.
    expect(aligned(nodes, "right")).toEqual(
      new Map([
        ["nd_a", { x: 260, y: 0 }],
        ["nd_c", { x: 260, y: 300 }],
      ]),
    );
    // Between 0 and 500, about 250.
    expect(aligned(nodes, "center").get("nd_b")).toEqual({ x: 200, y: 100 });
    expect(aligned(nodes, "top").get("nd_c")).toEqual({ x: 100, y: 0 });
    // From 0 to 428, the bottom of the third.
    expect(aligned(nodes, "bottom").get("nd_b")).toEqual({ x: 400, y: 378 });
    expect(aligned(nodes, "middle").get("nd_a")).toEqual({ x: 0, y: 150 });
    expect(aligned([at("nd_a", 0, 0)], "left").size).toBe(0);
  });

  it("distributes three nodes or more so that the gaps between them are the same, keeping the first and the last", () => {
    const nodes = [
      at("nd_c", 1000, 0, 100),
      at("nd_a", 0, 0, 100),
      at("nd_b", 150, 40, 100),
      at("nd_d", 700, 0, 200),
    ];
    // From 0 to 1100, of 500 of nodes: three gaps of 200.
    expect(distributed(nodes, "across")).toEqual(
      new Map([
        ["nd_b", { x: 300, y: 40 }],
        ["nd_d", { x: 600, y: 0 }],
      ]),
    );
    expect(distributed(nodes.slice(0, 2), "across").size).toBe(0);
    const down = distributed(
      [
        at("nd_a", 0, 0, 10, 100),
        at("nd_b", 0, 10, 10, 100),
        at("nd_c", 0, 400, 10, 100),
      ],
      "down",
    );
    expect(down).toEqual(new Map([["nd_b", { x: 0, y: 200 }]]));
  });

  it("places a new node at the centre, on the grid, and beside one whose corner is there already", () => {
    const taken = new Set(["-120,-64", "-88,-32"]);
    expect(
      freePlace({ x: 0, y: 0 }, { width: 240, height: 128 }, () => false, true),
    ).toEqual({ x: -120, y: -64 });
    expect(
      freePlace(
        { x: 0, y: 0 },
        { width: 240, height: 128 },
        (p) => taken.has(`${p.x},${p.y}`),
        true,
      ),
    ).toEqual({ x: -56, y: 0 });
  });

  // Verifies: FR-EDIT-06/AC1
  it("compares tags without regard to case, as the server does, ß and ss among them, and lists each once, in the spelling that most of its nodes use", () => {
    const nodes = [
      testNode("nd_1", 0, 0, { tags: ["Quest", "Straße"] }),
      testNode("nd_2", 0, 0, { tags: ["quest"] }),
      testNode("nd_3", 0, 0, { tags: ["quest", "STRASSE"] }),
      testNode("nd_4", 0, 0, { tags: ["Mira"] }),
    ];
    expect(tagsOf(nodes)).toEqual(["quest", "STRASSE", "Mira"]);
    expect(hasTag(nodes[0], "QUEST")).toBe(true);
    expect(hasTag(nodes[0], "strasse")).toBe(true);
    expect(hasTag(nodes[3], "quest")).toBe(false);
    expect(withTag(["Quest"], "quest")).toEqual(["Quest"]);
    expect(withTag(["Quest"], "Mira")).toEqual(["Quest", "Mira"]);
    expect(withoutTag(["Quest", "Mira"], "QUEST")).toEqual(["Mira"]);
  });
});
