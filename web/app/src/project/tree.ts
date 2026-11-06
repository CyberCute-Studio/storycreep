// The tree of a project's folders of one kind (PRD FR-PROJ-02, FR-VAR-01,
// FR-COMP-02): its folders, nested, and what they hold, its boards, its
// variables, or its components, each among the children of its folder, or
// of the top, in the order of their keys (compareChildren), as the server
// lists them, and where moving one of them lands it: in a folder, after its
// last child, or before or after another child, with a key between the keys
// of the children that it lands between (keyBetween).

import type { Board, Folder } from "@storycreep/shared/api";
import { compareChildren, keyBetween } from "./orderkey";

/** What a folder holds besides folders: a board, a variable, or a component. */
export interface Item {
  id: string;
  /** Its folder, or none at the top. */
  folder_id?: string;
  order: string;
  rev: number;
}

/** A folder of the tree, with its children in order. */
export interface FolderNode<T extends Item = Board> {
  kind: "folder";
  id: string;
  folder: Folder;
  /** The folder's parent, or undefined at the top. */
  parentId: string | undefined;
  order: string;
  children: TreeNode<T>[];
}

/** What a folder of the tree holds besides folders, as a board. */
export interface ItemNode<T extends Item = Board> {
  kind: "item";
  id: string;
  item: T;
  /** What the tree names it, as a board's name or a variable's key. */
  name: string;
  /** Its folder, or undefined at the top. */
  parentId: string | undefined;
  order: string;
}

export type TreeNode<T extends Item = Board> = FolderNode<T> | ItemNode<T>;

/** The tree of a project's folders of one kind, and its nodes by their IDs. */
export interface Tree<T extends Item = Board> {
  /** The children of the top, in order. */
  top: TreeNode<T>[];
  byId: ReadonlyMap<string, TreeNode<T>>;
}

/** Returns the name of a node of a tree. */
export function nameOf(node: TreeNode<Item>): string {
  return node.kind === "folder" ? node.folder.name : node.name;
}

/** Returns the revision of a node of a tree. */
export function revOf(node: TreeNode<Item>): number {
  return node.kind === "folder" ? node.folder.rev : node.item.rev;
}

/**
 * Returns the tree of folders, a project's folders of one kind, and items,
 * what they hold, each named by name: a project's boards unless it says
 * otherwise. A child whose parent is not among folders, as one that a
 * change made while the lists were read a page at a time moved into a
 * folder of a page read before it, shows at the top, rather than nowhere.
 */
export function buildTree(
  folders: readonly Folder[],
  items: readonly Board[],
): Tree;
export function buildTree<T extends Item>(
  folders: readonly Folder[],
  items: readonly T[],
  name: (item: T) => string,
): Tree<T>;
export function buildTree<T extends Item>(
  folders: readonly Folder[],
  items: readonly T[],
  name: (item: T) => string = (item) => (item as unknown as Board).name,
): Tree<T> {
  const byId = new Map<string, TreeNode<T>>();
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
  for (const item of items) {
    byId.set(item.id, {
      kind: "item",
      id: item.id,
      item,
      name: name(item),
      parentId: item.folder_id,
      order: item.order,
    });
  }
  const top: TreeNode<T>[] = [];
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
  const sort = (children: TreeNode<T>[]) => {
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
  byId: ReadonlyMap<string, TreeNode<Item>>,
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
  tree: Tree<Item>,
  node: TreeNode<Item>,
  folderId: string,
): boolean {
  for (let n: TreeNode<Item> | undefined = node; n !== undefined; ) {
    if (n.id === folderId) {
      return true;
    }
    n = n.parentId === undefined ? undefined : tree.byId.get(n.parentId);
  }
  return false;
}

/** Returns the children of the folder parentId, or of the top for undefined. */
export function childrenOf<T extends Item>(
  tree: Tree<T>,
  parentId: string | undefined,
): TreeNode<T>[] {
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
  tree: Tree<Item>,
  node: TreeNode<Item>,
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
export function folderChoices(
  tree: Tree<Item>,
  node?: TreeNode<Item>,
): FolderChoice[] {
  const out: FolderChoice[] = [];
  const walk = (children: TreeNode<Item>[], path: string[]) => {
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

/** Returns the items of tree, without its folders, in its order, as a filter lists those that match it. */
export function itemsInOrder<T extends Item>(tree: Tree<T>): ItemNode<T>[] {
  const out: ItemNode<T>[] = [];
  const walk = (nodes: TreeNode<T>[]) => {
    for (const node of nodes) {
      if (node.kind === "folder") {
        walk(node.children);
      } else {
        out.push(node);
      }
    }
  };
  walk(tree.top);
  return out;
}
