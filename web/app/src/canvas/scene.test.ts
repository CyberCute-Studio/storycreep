import { describe, expect, it } from "vitest";
import { docOf, gridNodes, testConnection, testNode } from "../test/canvas";
import { elementHeight, nodeWidth } from "./layout";
import { Scene } from "./scene";
import { GridIndex } from "./spatial";

describe("the scene of a board", () => {
  it("holds the board's nodes and connections, but not another board's, nor those in the trash", () => {
    const scene = new Scene("bd_a");
    scene.sync(
      docOf(
        [
          testNode("nd_1", 0, 0),
          testNode("nd_2", 400, 0),
          testNode("nd_3", 0, 0, { board_id: "bd_b" }),
          testNode("nd_4", 0, 0, { deleted_at: "2026-01-02T00:00:00Z" }),
        ],
        [
          testConnection("cn_1", "nd_1", "nd_2"),
          testConnection("cn_2", "nd_1", "nd_4"),
        ],
      ),
    );
    expect(scene.nodes.size).toBe(2);
    expect(scene.connections.size).toBe(1);
    expect(scene.contentBounds()).toEqual({
      x: 0,
      y: 0,
      width: 400 + nodeWidth,
      height: elementHeight,
    });
  });

  it("follows a node that moves with its connections, and changes nothing for a document that is the same", () => {
    const scene = new Scene("bd_a");
    const a = testNode("nd_1", 0, 0);
    const b = testNode("nd_2", 400, 0);
    const doc = docOf([a, b], [testConnection("cn_1", "nd_1", "nd_2")]);
    expect(scene.sync(doc)).toBe(true);
    expect(scene.sync(doc)).toBe(false);
    const moved = { ...b, position: { x: 400, y: 1000 } };
    const next = {
      ...doc,
      nodes: new Map([
        [a.id, a],
        [b.id, moved],
      ]),
    };
    expect(scene.sync(next)).toBe(true);
    const curve = scene.connections.get("cn_1")?.curve;
    expect(curve?.p3).toEqual({ x: 400, y: 1000 + elementHeight / 2 });
    expect(scene.nodeAt({ x: 410, y: 1010 })).toBe("nd_2");
    expect(scene.nodeAt({ x: 410, y: 10 })).toBeUndefined();
  });

  it("draws a connection once both of its nodes are there", () => {
    const scene = new Scene("bd_a");
    const a = testNode("nd_1", 0, 0);
    const b = testNode("nd_2", 400, 0);
    const doc = docOf([a], [testConnection("cn_1", "nd_1", "nd_2")]);
    scene.sync(doc);
    expect(scene.connections.size).toBe(0);
    scene.sync({
      ...doc,
      nodes: new Map([
        [a.id, a],
        [b.id, b],
      ]),
    });
    expect(scene.connections.size).toBe(1);
    scene.sync({ ...doc, nodes: new Map([[a.id, a]]) });
    expect(scene.connections.size).toBe(0);
  });

  it("does not draw a connection that went while it waited for a node, once the node comes", () => {
    const scene = new Scene("bd_a");
    const a = testNode("nd_1", 0, 0);
    const b = testNode("nd_2", 400, 0);
    const waiting = docOf([a], [testConnection("cn_1", "nd_1", "nd_2")]);
    scene.sync(waiting);
    const deleted = { ...waiting, connections: new Map() };
    scene.sync(deleted);
    scene.sync({
      ...deleted,
      nodes: new Map([
        [a.id, a],
        [b.id, b],
      ]),
    });
    expect(scene.connections.size).toBe(0);
  });

  it("tells once the title of a node that a jumper goes to changes, of its board or of another", () => {
    const scene = new Scene("bd_a");
    const jumper = testNode("nd_j", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_b", node_id: "nd_t" },
    });
    const other = testNode("nd_o", 400, 0);
    scene.sync(docOf([jumper, other]));
    const before = scene.targetsVersion;
    // The target, of another board, comes.
    const target = testNode("nd_t", 0, 0, { board_id: "bd_b" });
    scene.sync(docOf([jumper, other, target]));
    expect(scene.targetsVersion).toBe(before + 1);
    // A node that no jumper goes to moves, and the target is the same.
    scene.sync(
      docOf([jumper, { ...other, position: { x: 0, y: 400 } }, target]),
    );
    expect(scene.targetsVersion).toBe(before + 1);
    scene.sync(docOf([jumper, other, { ...target, title: "Harbour" }]));
    expect(scene.targetsVersion).toBe(before + 2);
  });

  // Verifies: FR-EDIT-11/AC1
  it("shows, at one viewport, the same content of a board of 200 nodes as of one of 2,000", () => {
    const small = new Scene("bd_a");
    small.sync(docOf(gridNodes(200, 18)));
    const large = new Scene("bd_a");
    large.sync(docOf(gridNodes(2000, 55)));
    // A canvas of 1280 by 800 at 100%, over the corner that both share.
    const r = { x: 600, y: 300, width: 1280, height: 800 };
    const places = (scene: Scene) =>
      scene
        .shown(r)
        .nodes.map((n) => `${n.rect.x},${n.rect.y}`)
        .sort();
    expect(places(large)).toEqual(places(small));
    expect(small.shown(r).nodes.length).toBeLessThan(40);
  });

  it("finds the topmost node under a point, and the nodes that a rectangle overlaps", () => {
    const scene = new Scene("bd_a");
    scene.sync(docOf([testNode("nd_1", 0, 0), testNode("nd_2", 100, 50)]));
    expect(scene.nodeAt({ x: 150, y: 60 })).toBe("nd_2");
    expect(scene.nodeAt({ x: -4, y: 10 }, 5)).toBe("nd_1");
    expect(scene.nodesIn({ x: 300, y: 60, width: 100, height: 10 })).toEqual([
      "nd_2",
    ]);
  });
});

describe("the spatial index", () => {
  it("finds what overlaps a rectangle, once each, as entries move and go", () => {
    const index = new GridIndex<number>();
    index.set("a", { x: 0, y: 0, width: 10, height: 10 }, 1);
    index.set("b", { x: 5000, y: 5000, width: 10, height: 10 }, 2);
    // Across many cells, and too large for the grid.
    index.set("c", { x: -1000, y: -1000, width: 3000, height: 3000 }, 3);
    index.set("d", { x: -1e9, y: 0, width: 2e9, height: 10 }, 4);
    const found = (r: {
      x: number;
      y: number;
      width: number;
      height: number;
    }) => {
      const ids: string[] = [];
      index.query(r, (id) => ids.push(id));
      return ids.sort();
    };
    expect(found({ x: 0, y: 0, width: 1, height: 1 })).toEqual(["a", "c", "d"]);
    expect(found({ x: 4990, y: 4990, width: 20, height: 20 })).toEqual(["b"]);
    index.set("a", { x: 4995, y: 4995, width: 10, height: 10 }, 1);
    expect(found({ x: 4990, y: 4990, width: 20, height: 20 })).toEqual([
      "a",
      "b",
    ]);
    index.delete("b");
    index.delete("d");
    expect(found({ x: -1e9, y: -1e9, width: 2e9, height: 2e9 })).toEqual([
      "a",
      "c",
    ]);
    expect(index.size).toBe(2);
  });
});
