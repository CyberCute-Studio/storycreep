// The tree of a project's boards (PRD FR-PROJ-02): its folders of boards,
// nested, and its boards, each among the children of its folder, or of the
// top, in the order of their keys (compareChildren), as the server lists
// them, and where moving one of them lands it: in a folder, after its last
// child, or before or after another child, with a key between the keys of
// the children that it lands between (keyBetween).

import type { Board, Folder } from "@storycreep/shared/api";
import { compareChildren, keyBetween } from "./orderkey";

/** A folder of the tree, with its children in order. */
export interface FolderNode {
  kind: "folder";
  id: string;
  folder: Folder;
  /** The folder's parent, or undefined at the top. */
  parentId: string | undefined;
  order: string;
  children: TreeNode[];
}

/** A board of the tree. */
export interface BoardNode {
  kind: "board";
  id: string;
  board: Board;
  /** The board's folder, or undefined at the top. */
  parentId: string | undefined;
  order: string;
}

export type TreeNode = FolderNode | BoardNode;

/** The tree of a project's boards, and its nodes by their IDs. */
export interface Tree {
  /** The children of the top, in order. */
  top: TreeNode[];
  byId: ReadonlyMap<string, TreeNode>;
}

/**
 * Returns the tree of folders, a project's folders of boards, and boards. A
 * child whose parent is not among folders, as one that a change made while
 * the lists were read a page at a time moved into a folder of a page read
 * before it, shows at the top, rather than nowhere.
 */
export function buildTree(
  folders: readonly Folder[],
  boards: readonly Board[],
): Tree {
  const byId = new Map<string, TreeNode>();
  for (const folder of folders) {
    byId.set(folder.id, {
      kind: "folder",
      id: folder.id,
      folder,
      parentId: folder.parent_id,
      order: folder.order,
      children: [],
    });
  }
  for (const board of boards) {
    byId.set(board.id, {
      kind: "board",
      id: board.id,
      board,
      parentId: board.folder_id,
      order: board.order,
    });
  }
  const top: TreeNode[] = [];
  for (const node of byId.values()) {
    const parent =
      node.parentId === undefined ? undefined : byId.get(node.parentId);
    if (parent?.kind === "folder" && !within(byId, parent.id, node.id)) {
      parent.children.push(node);
    } else {
      node.parentId = undefined;
      top.push(node);
    }
  }
  const sort = (children: TreeNode[]) => {
    children.sort(compareChildren);
    for (const child of children) {
      if (child.kind === "folder") {
        sort(child.children);
      }
    }
  };
  sort(top);
  return { top, byId };
}

/**
 * Reports whether the folder folderId is the folder ancestorId or within it,
 * by the parents that byId gives, which the server keeps free of cycles; a
 * cycle, as one that lists read a page at a time could give while folders
 * moved, is taken for within, so that buildTree breaks it at the top.
 */
function within(
  byId: ReadonlyMap<string, TreeNode>,
  folderId: string,
  ancestorId: string,
): boolean {
  const seen = new Set<string>();
  for (
    let id: string | undefined = folderId;
    id !== undefined;
    id = byId.get(id)?.parentId
  ) {
    if (id === ancestorId || seen.has(id)) {
      return true;
    }
    seen.add(id);
  }
  return false;
}

/** Reports whether node is the folder folderId or within it. */
export function isWithin(
  tree: Tree,
  node: TreeNode,
  folderId: string,
): boolean {
  for (let n: TreeNode | undefined = node; n !== undefined; ) {
    if (n.id === folderId) {
      return true;
    }
    n = n.parentId === undefined ? undefined : tree.byId.get(n.parentId);
  }
  return false;
}

/** Returns the children of the folder parentId, or of the top for undefined. */
export function childrenOf(
  tree: Tree,
  parentId: string | undefined,
): TreeNode[] {
  if (parentId === undefined) {
    return tree.top;
  }
  const parent = tree.byId.get(parentId);
  return parent?.kind === "folder" ? parent.children : [];
}

/** Where a move lands a child of the tree, relative to another node. */
export type Drop =
  | { position: "inside"; folderId: string | undefined }
  | { position: "before" | "after"; targetId: string };

/** What a move of a child sends: its new parent, and its key there. */
export interface Placement {
  /** The new parent, or undefined for the top. */
  parentId: string | undefined;
  /**
   * The key among the new parent's children, or undefined for after the
   * last of them, where the server places a child that a move gives no key.
   */
  order: string | undefined;
}

/**
 * Returns where moving node to drop places it, or undefined if it cannot go
 * there: a folder into itself or a folder within it, or before or after
 * itself or a node that the tree does not have. A child that lands between
 * two children with the same key goes after or before all of those that
 * share it, as no key goes between two that are the same.
 */
export function placementOf(
  tree: Tree,
  node: TreeNode,
  drop: Drop,
): Placement | undefined {
  if (drop.position === "inside") {
    if (drop.folderId !== undefined) {
      const folder = tree.byId.get(drop.folderId);
      if (folder?.kind !== "folder" || isWithin(tree, folder, node.id)) {
        return undefined;
      }
    }
    return { parentId: drop.folderId, order: undefined };
  }
  const target = tree.byId.get(drop.targetId);
  if (target === undefined || target.id === node.id) {
    return undefined;
  }
  const parentId = target.parentId;
  if (parentId !== undefined) {
    const parent = tree.byId.get(parentId);
    if (parent === undefined || isWithin(tree, parent, node.id)) {
      return undefined;
    }
  }
  const siblings = childrenOf(tree, parentId).filter((c) => c.id !== node.id);
  const at = siblings.indexOf(target);
  if (drop.position === "before") {
    const earlier = siblings
      .slice(0, at)
      .findLast((c) => c.order < target.order);
    return { parentId, order: keyBetween(earlier?.order, target.order) };
  }
  const later = siblings.slice(at + 1).find((c) => c.order > target.order);
  return { parentId, order: keyBetween(target.order, later?.order) };
}

/** A folder of the tree as a choice of where to move or create something. */
export interface FolderChoice {
  id: string;
  /** The folder's name, after the names of the folders that it is in. */
  path: string[];
}

/**
 * Returns the folders of the tree, in the tree's order, each with its path,
 * but node and the folders within it, which node cannot go into.
 */
export function folderChoices(tree: Tree, node?: TreeNode): FolderChoice[] {
  const out: FolderChoice[] = [];
  const walk = (children: TreeNode[], path: string[]) => {
    for (const child of children) {
      if (child.kind === "folder" && child.id !== node?.id) {
        const own = [...path, child.folder.name];
        out.push({ id: child.id, path: own });
        walk(child.children, own);
      }
    }
  };
  walk(tree.top, []);
  return out;
}
