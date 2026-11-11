import type { Board, Node } from "@storycreep/shared/api";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { testNode } from "../test/canvas";
import { type LabelContext, labelsOf, sameLabels, targetLabel } from "./labels";
import { type TargetLookup, TargetReads, targetOf } from "./targets";

const board = (id: string, name: string) => ({ id, name }) as unknown as Board;

/** A document's nodes and boards: the jumper's own board, Harbour, with The dock, and the board Lighthouse. */
const view = {
  nodes: new Map<string, Node>(
    [
      testNode("nd_dock", 0, 0, { title: "The dock" }),
      testNode("nd_lamp", 0, 0, { title: "The lamp", board_id: "bd_b" }),
    ].map((n) => [n.id, n]),
  ),
  boards: new Map([
    ["bd_a", board("bd_a", "Harbour")],
    ["bd_b", board("bd_b", "Lighthouse")],
  ]),
};

/** Returns a jumper of the board bd_a that targets the node of nodeId on the board of boardId. */
function jumper(nodeId?: string, boardId?: string): Node {
  return testNode("nd_j", 0, 0, {
    type: "jumper",
    ...(nodeId === undefined
      ? {}
      : { target: { node_id: nodeId, board_id: boardId } }),
  });
}

/** Returns a lookup of what was read: the boards of read, the tree where tree says so, and the targets of reads. */
function lookupOf(
  read: string[] = [],
  tree = true,
  reads: Record<string, { title: string; boardId: string } | "gone"> = {},
): TargetLookup & { asked: string[] } {
  const asked: string[] = [];
  return {
    asked,
    boardRead: (id) => read.includes(id),
    treeRead: () => tree,
    read: (id) => {
      asked.push(id);
      return reads[id];
    },
  };
}

describe("a jumper's target", () => {
  it("is none, a node that the document holds, of the jumper's board or of another, or unknown without what tells of the rest", () => {
    expect(targetOf(jumper(), view)).toEqual({ state: "none" });
    expect(targetOf(jumper("nd_dock", "bd_a"), view)).toEqual({
      state: "found",
      nodeId: "nd_dock",
      boardId: "bd_a",
      title: "The dock",
      elsewhere: false,
    });
    expect(targetOf(jumper("nd_lamp", "bd_b"), view)).toMatchObject({
      state: "found",
      boardId: "bd_b",
      elsewhere: true,
    });
    expect(targetOf(jumper("nd_far", "bd_c"), view)).toEqual({
      state: "unknown",
      nodeId: "nd_far",
      boardId: "bd_c",
    });
  });

  // Verifies: FR-EDIT-05/AC1
  it("is gone once the document has read its board without it, once the tree of boards lacks its board, or once a read of it found none", () => {
    // A node of a board whose content was read, which lacks it.
    const read = lookupOf(["bd_b"]);
    expect(targetOf(jumper("nd_gone", "bd_b"), view, read)).toEqual({
      state: "gone",
      nodeId: "nd_gone",
    });
    expect(read.asked).toEqual([]);
    // A node of a board that the tree, which was read, lacks, as one in the
    // trash.
    const tree = lookupOf();
    expect(targetOf(jumper("nd_far", "bd_c"), view, tree).state).toBe("gone");
    expect(tree.asked).toEqual([]);
    // A node of a board that was not read, which a read of the node finds
    // gone, or finds.
    const gone = lookupOf([], true, { nd_x: "gone" });
    expect(targetOf(jumper("nd_x", "bd_b"), view, gone).state).toBe("gone");
    const found = lookupOf([], true, {
      nd_x: { title: "The stair", boardId: "bd_b" },
    });
    expect(targetOf(jumper("nd_x", "bd_b"), view, found)).toEqual({
      state: "found",
      nodeId: "nd_x",
      boardId: "bd_b",
      title: "The stair",
      elsewhere: true,
    });
    // Until that read answers, and while the tree is not read, it is not
    // known.
    const asking = lookupOf();
    expect(targetOf(jumper("nd_x", "bd_b"), view, asking).state).toBe(
      "unknown",
    );
    expect(asking.asked).toEqual(["nd_x"]);
    expect(
      targetOf(jumper("nd_far", "bd_c"), view, lookupOf([], false)).state,
    ).toBe("unknown");
    // A node that the document holds in the trash.
    const trashed = {
      ...view,
      nodes: new Map([
        [
          "nd_old",
          testNode("nd_old", 0, 0, { deleted_at: "2026-01-02T00:00:00Z" }),
        ],
      ]),
    };
    expect(targetOf(jumper("nd_old", "bd_a"), trashed).state).toBe("gone");
  });

  // Verifies: FR-EDIT-05/AC1
  it("is named on its jumper's card by its title, with its board where that is another, and as deleted, which draws the card as broken", () => {
    const t: LabelContext["t"] = (key, args) =>
      args === undefined ? key : `${key}(${Object.values(args).join(",")})`;
    const context = (lookup?: TargetLookup): LabelContext => ({
      t,
      componentName: () => undefined,
      variableKey: () => undefined,
      target: (node) => targetOf(node, view, lookup),
      boardName: (id) => view.boards.get(id)?.name,
    });
    expect(labelsOf(jumper("nd_dock", "bd_a"), context())).toEqual({
      title: "nd_j",
      kind: "canvas.node.jumper",
      body: "canvas.node.jumper_to(The dock)",
    });
    expect(labelsOf(jumper("nd_lamp", "bd_b"), context()).body).toBe(
      "canvas.node.jumper_to_on(The lamp,Lighthouse)",
    );
    expect(labelsOf(jumper("nd_x", "bd_b"), context()).body).toBe(
      "canvas.node.jumper_to_board(Lighthouse)",
    );
    const broken = labelsOf(
      jumper("nd_gone", "bd_b"),
      context(lookupOf(["bd_b"])),
    );
    expect(broken).toEqual({
      title: "nd_j",
      kind: "canvas.node.jumper",
      body: "canvas.node.jumper_broken",
      broken: true,
    });
    // A card drawn as whole is drawn again once it is broken, and back.
    expect(sameLabels(broken, { ...broken, broken: undefined })).toBe(false);
    expect(sameLabels(broken, { ...broken })).toBe(true);
    expect(targetLabel({ state: "none" }, context())).toBe(
      "canvas.node.jumper_none",
    );
  });
});

describe("the reads of the targets that the document does not tell of", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  /** Reads whose answers the test gives, with what was asked for, in order. */
  function reads() {
    const asked: string[] = [];
    const answers = new Map<
      string,
      { resolve: (node: Node | undefined) => void; reject: () => void }
    >();
    let told = 0;
    const r = new TargetReads(
      (id) =>
        new Promise((resolve, reject) => {
          asked.push(id);
          answers.set(id, { resolve, reject: () => reject(new Error("no")) });
        }),
      () => {
        told++;
      },
    );
    r.start();
    return { r, asked, answers, told: () => told };
  }

  it("reads a target once, as it is first asked for, and tells of the answers of a moment together", async () => {
    const { r, asked, answers, told } = reads();
    expect(r.read("nd_1")).toBeUndefined();
    expect(r.read("nd_1")).toBeUndefined();
    expect(r.read("nd_2")).toBeUndefined();
    expect(asked).toEqual(["nd_1", "nd_2"]);
    answers.get("nd_1")?.resolve(testNode("nd_1", 0, 0, { title: "One" }));
    answers.get("nd_2")?.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(told()).toBe(0);
    await vi.advanceTimersByTimeAsync(50);
    expect(told()).toBe(1);
    expect(r.read("nd_1")).toEqual({ title: "One", boardId: "bd_a" });
    expect(r.read("nd_2")).toBe("gone");
    expect(asked).toEqual(["nd_1", "nd_2"]);
  });

  it("reads four at once, and the rest as those answer", async () => {
    const { r, asked, answers } = reads();
    for (let i = 0; i < 6; i++) {
      r.read(`nd_${i}`);
    }
    expect(asked).toHaveLength(4);
    answers.get("nd_0")?.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(asked).toEqual(["nd_0", "nd_1", "nd_2", "nd_3", "nd_4"]);
  });

  it("does not read again what failed until the reads are asked for again, as once the page is shown again, and keeps what it found until the new answers come", async () => {
    const { r, asked, answers, told } = reads();
    r.read("nd_1");
    r.read("nd_2");
    answers.get("nd_1")?.reject();
    answers.get("nd_2")?.resolve(undefined);
    await vi.advanceTimersByTimeAsync(50);
    expect(told()).toBe(1);
    // What failed is not known, nor asked for again.
    expect(r.read("nd_1")).toBeUndefined();
    expect(r.peek("nd_2")).toBe("gone");
    expect(asked).toEqual(["nd_1", "nd_2"]);
    r.readAgain();
    expect(told()).toBe(2);
    // What it knew, it knows still, without asking; asked for, each is read
    // again, once.
    expect(r.peek("nd_2")).toBe("gone");
    expect(asked).toEqual(["nd_1", "nd_2"]);
    expect(r.read("nd_1")).toBeUndefined();
    expect(r.read("nd_2")).toBe("gone");
    expect(r.read("nd_2")).toBe("gone");
    expect(asked).toEqual(["nd_1", "nd_2", "nd_1", "nd_2"]);
    // A read again that fails leaves what the read before it found, and
    // one that finds another thing tells of it.
    answers.get("nd_2")?.reject();
    answers.get("nd_1")?.resolve(testNode("nd_1", 0, 0, { title: "One" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(told()).toBe(3);
    expect(r.read("nd_2")).toBe("gone");
    expect(r.read("nd_1")).toEqual({ title: "One", boardId: "bd_a" });
    expect(asked).toHaveLength(4);
    // One that finds the same tells of nothing.
    r.readAgain();
    expect(told()).toBe(4);
    r.read("nd_1");
    answers.get("nd_1")?.resolve(testNode("nd_1", 0, 0, { title: "One" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(told()).toBe(4);
    // With nothing found, there is nothing to read again, or to tell of.
    const empty = reads();
    empty.r.readAgain();
    expect(empty.told()).toBe(0);
  });

  it("reads nothing, and tells of nothing, once it is stopped, until it is started again", async () => {
    const { r, asked, answers, told } = reads();
    r.read("nd_1");
    answers.get("nd_1")?.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    r.stop();
    await vi.advanceTimersByTimeAsync(100);
    expect(told()).toBe(0);
    r.read("nd_2");
    expect(asked).toEqual(["nd_1"]);
    r.start();
    r.read("nd_2");
    expect(asked).toEqual(["nd_1", "nd_2"]);
  });
});
