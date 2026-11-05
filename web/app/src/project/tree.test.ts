import type { Board, Folder } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import {
  buildTree,
  folderChoices,
  placementOf,
  type Tree,
  type TreeNode,
} from "./tree";

const at = "2026-11-01T10:00:00Z";

function folder(id: string, order: string, parent?: string): Folder {
  return {
    id,
    project_id: "prj_1",
    kind: "board",
    name: id,
    order,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...(parent === undefined ? {} : { parent_id: parent }),
  };
}

function board(id: string, order: string, folderId?: string): Board {
  return {
    id,
    project_id: "prj_1",
    name: id,
    slug: id,
    description: "",
    color: "none",
    order,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...(folderId === undefined ? {} : { folder_id: folderId }),
  };
}

/** Returns the IDs of children, each folder's with its own after it. */
function shape(children: TreeNode[]): unknown[] {
  return children.map((c) =>
    c.kind === "folder" ? [c.id, shape(c.children)] : c.id,
  );
}

function node(tree: Tree, id: string): TreeNode {
  const n = tree.byId.get(id);
  if (n === undefined) {
    throw new Error(`no ${id}`);
  }
  return n;
}

describe("buildTree", () => {
  it("nests the folders and their boards, each folder's children in order", () => {
    const tree = buildTree(
      [
        folder("fld_b", "W"),
        folder("fld_a", "V"),
        folder("fld_c", "V", "fld_a"),
      ],
      [
        board("bd_2", "U"),
        board("bd_1", "X", "fld_a"),
        board("bd_3", "V", "fld_c"),
      ],
    );
    expect(shape(tree.top)).toEqual([
      "bd_2",
      ["fld_a", [["fld_c", ["bd_3"]], "bd_1"]],
      ["fld_b", []],
    ]);
  });

  it("shows at the top a child whose folder is not listed, and breaks a cycle", () => {
    const tree = buildTree(
      [folder("fld_a", "V", "fld_b"), folder("fld_b", "W", "fld_a")],
      [board("bd_1", "V", "fld_gone")],
    );
    expect(shape(tree.top)).toEqual(["bd_1", ["fld_a", [["fld_b", []]]]]);
  });
});

describe("placementOf", () => {
  const tree = buildTree(
    [folder("fld_a", "V"), folder("fld_c", "V", "fld_a")],
    [
      board("bd_1", "U"),
      board("bd_2", "W"),
      board("bd_3", "W"),
      board("bd_4", "X"),
      board("bd_5", "V", "fld_a"),
    ],
  );

  it("places a child in a folder after its last child", () => {
    expect(
      placementOf(tree, node(tree, "bd_1"), {
        position: "inside",
        folderId: "fld_a",
      }),
    ).toEqual({ parentId: "fld_a", order: undefined });
    expect(
      placementOf(tree, node(tree, "bd_5"), {
        position: "inside",
        folderId: undefined,
      }),
    ).toEqual({ parentId: undefined, order: undefined });
  });

  it("places a child before or after another, between it and its neighbour", () => {
    expect(
      placementOf(tree, node(tree, "bd_4"), {
        position: "before",
        targetId: "bd_1",
      }),
    ).toEqual({
      parentId: undefined,
      order: "F",
    });
    expect(
      placementOf(tree, node(tree, "bd_1"), {
        position: "after",
        targetId: "fld_a",
      }),
    ).toEqual({
      parentId: undefined,
      order: "VV",
    });
    expect(
      placementOf(tree, node(tree, "bd_1"), {
        position: "before",
        targetId: "bd_5",
      }),
    ).toEqual({
      parentId: "fld_a",
      order: "G",
    });
  });

  it("places a child after or before every child that shares the key of the one it lands by", () => {
    // bd_2 and bd_3 share W, which no key goes between.
    expect(
      placementOf(tree, node(tree, "bd_1"), {
        position: "after",
        targetId: "bd_2",
      }),
    ).toEqual({
      parentId: undefined,
      order: "WV",
    });
    expect(
      placementOf(tree, node(tree, "bd_4"), {
        position: "before",
        targetId: "bd_3",
      }),
    ).toEqual({
      parentId: undefined,
      order: "VV",
    });
  });

  it("does not move a folder into itself or a folder within it, nor a child by itself", () => {
    const a = node(tree, "fld_a");
    expect(
      placementOf(tree, a, { position: "inside", folderId: "fld_a" }),
    ).toBeUndefined();
    expect(
      placementOf(tree, a, { position: "inside", folderId: "fld_c" }),
    ).toBeUndefined();
    expect(
      placementOf(tree, a, { position: "after", targetId: "bd_5" }),
    ).toBeUndefined();
    expect(
      placementOf(tree, a, { position: "before", targetId: "fld_a" }),
    ).toBeUndefined();
  });
});

describe("folderChoices", () => {
  it("lists the folders in the tree's order with their paths, but the one that moves and those within it", () => {
    const tree = buildTree(
      [
        folder("fld_a", "V"),
        folder("fld_c", "V", "fld_a"),
        folder("fld_b", "W"),
      ],
      [],
    );
    expect(folderChoices(tree)).toEqual([
      { id: "fld_a", path: ["fld_a"] },
      { id: "fld_c", path: ["fld_a", "fld_c"] },
      { id: "fld_b", path: ["fld_b"] },
    ]);
    expect(folderChoices(tree, node(tree, "fld_a"))).toEqual([
      { id: "fld_b", path: ["fld_b"] },
    ]);
  });
});
