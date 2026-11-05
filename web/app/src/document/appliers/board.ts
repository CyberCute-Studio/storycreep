// The appliers of the commands of the project, its folders, and its boards
// (PRD FR-PROJ-01, FR-PROJ-02), as their server's packages apply them: a
// value of a payload is stored as it is sent, since the server refuses what
// it would have to mend; a key of order that the payload leaves out is the
// server's to pick, after every sibling, those in the trash too, which the
// document does not hold, so the applier's is not sure, but for a child that
// moves within its folder after every other, which keeps its own; and a
// board that goes to the trash takes its nodes and connections out of every
// read with it, without a revision of theirs.

import type { Board, Folder } from "@storycreep/shared/api";
import { type Appliers, canonical } from "../apply";
import { boardPart, type Draft, type Project } from "../doc";
import { folderChildren, keyAfter, placed, renames, withField } from "./node";

type Types =
  | `board.${"create" | "rename" | "describe" | "set_color" | "set_slug" | "move" | "delete" | "restore" | "set_entry_node"}`
  | `folder.${"create" | "rename" | "move" | "delete" | "restore"}`
  | `project.${"rename" | "set_slug" | "describe" | "update_settings" | "archive" | "unarchive" | "delete" | "restore"}`;

/** How long a project stays in the trash before it is deleted for good (FR-PROJ-01). */
const trashDays = 30;

/**
 * Returns a key after the last of the children of the folder of kind of
 * parentId, or of the top, that draft holds (folderChildren): the server
 * picks one after those in the trash too, which the document does not hold,
 * so it is not sure.
 */
function keyAfterChildren(
  draft: Draft,
  kind: Folder["kind"],
  parentId: string | undefined,
): string {
  return keyAfter(folderChildren(draft, kind, parentId).map((c) => c.order));
}

/**
 * Returns the key that child, of a folder of kind, moving from the folder of
 * from, or the top, to the one of parentId, or the top, is placed at, and
 * whether it is sure: the payload's order, if it gives one; or else, within
 * its folder, its own if it comes after every other child, and otherwise one
 * after the last of them (placed), as the server places it (board.Place);
 * or else one after the last child of the folder that it goes into.
 */
function movedTo(
  draft: Draft,
  child: { id: string; order: string },
  kind: Folder["kind"],
  from: string | undefined,
  parentId: string | undefined,
  order: string | undefined,
): { order: string; sure: boolean } {
  if (order !== undefined) {
    return { order, sure: true };
  }
  return from === parentId
    ? placed(child, folderChildren(draft, kind, parentId))
    : { order: keyAfterChildren(draft, kind, parentId), sure: false };
}

/**
 * Returns the slug that the server derives from a board's name, as far as
 * the document can tell: its words in lowercase, without their accents,
 * joined by hyphens, which the server makes unique among the boards that
 * the trash holds too, so it is not sure.
 */
function derivedSlug(name: string): string {
  const slug = name
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64)
    .replace(/-+$/, "");
  return slug === "" ? "board" : slug;
}

/** Changes the board of id with change, if draft holds it. */
function changeBoard(
  draft: Draft,
  id: string,
  change: (board: Board) => Board,
  sure = true,
): void {
  const board = draft.get("boards", canonical(id));
  if (board !== undefined) {
    draft.put("boards", { ...change(board), updated_at: draft.time }, sure);
  }
}

/** Changes the folder of id with change, if draft holds it. */
function changeFolder(
  draft: Draft,
  id: string,
  change: (folder: Folder) => Folder,
  sure = true,
): void {
  const folder = draft.get("folders", canonical(id));
  if (folder !== undefined) {
    draft.put("folders", { ...change(folder), updated_at: draft.time }, sure);
  }
}

/** Changes the project with change, if draft holds it. */
function changeProject(
  draft: Draft,
  change: (project: Project) => Project,
): void {
  if (draft.project !== undefined) {
    draft.setProject({ ...change(draft.project), updated_at: draft.time });
  }
}

export const boardAppliers: Appliers<Types> = {
  "board.create": (draft, p) => {
    const folderId = canonical(p.folder_id);
    draft.put(
      "boards",
      {
        id: canonical(p.id),
        project_id: draft.project?.id ?? "",
        name: p.name,
        slug: p.slug ?? derivedSlug(p.name),
        description: p.description ?? "",
        color: p.color ?? "none",
        order: p.order ?? keyAfterChildren(draft, "board", folderId),
        ...(folderId === undefined ? {} : { folder_id: folderId }),
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.slug !== undefined && p.order !== undefined,
    );
  },
  "board.rename": (draft, p) =>
    changeBoard(draft, p.id, (board) => ({ ...board, name: p.name })),
  "board.describe": (draft, p) =>
    changeBoard(draft, p.id, (board) => ({
      ...board,
      description: p.description,
    })),
  "board.set_color": (draft, p) =>
    changeBoard(draft, p.id, (board) => ({ ...board, color: p.color })),
  "board.set_slug": (draft, p) =>
    changeBoard(draft, p.id, (board) => ({ ...board, slug: p.slug })),
  "board.set_entry_node": (draft, p) =>
    changeBoard(draft, p.id, (board) =>
      withField(board, "entry_node_id", canonical(p.node_id)),
    ),
  "board.move": (draft, p) => {
    const board = draft.get("boards", canonical(p.id));
    if (board === undefined) {
      return;
    }
    const folderId = canonical(p.folder_id);
    const { order, sure } = movedTo(
      draft,
      board,
      "board",
      board.folder_id,
      folderId,
      p.order,
    );
    draft.put(
      "boards",
      {
        ...withField(board, "folder_id", folderId),
        order,
        updated_at: draft.time,
      },
      sure,
    );
  },
  "board.delete": (draft, p) => {
    const id = canonical(p.id);
    draft.remove("boards", id);
    // The board's nodes and connections are in the trash with it, without
    // a revision of their own, and the server's reads leave them out.
    draft.drop(boardPart(id), id);
    // So are its variables, of its scope, which the server's reads leave
    // out too (live_variable).
    for (const variable of [...draft.all("variables").values()]) {
      if (variable.scope === "board" && variable.board_id === id) {
        draft.removeQuietly("variables", variable.id, [id]);
      }
    }
    // The expressions on other boards that name its nodes, or its
    // variables, compile anew.
    renames(draft);
  },
  // The board in the trash is none that the document holds, nor are the
  // folders that its restoration brings back: the store reads them, as its
  // operation's revisions name them, and its variables, which come back
  // with it without revisions of their own, with the variables' lists. The
  // expressions that name its nodes, or its variables, compile anew.
  "board.restore": (draft) => {
    draft.reread("variables");
    renames(draft);
  },
  "folder.create": (draft, p) => {
    const parentId = canonical(p.parent_id);
    draft.put(
      "folders",
      {
        id: canonical(p.id),
        project_id: draft.project?.id ?? "",
        kind: p.kind,
        name: p.name,
        order: p.order ?? keyAfterChildren(draft, p.kind, parentId),
        ...(parentId === undefined ? {} : { parent_id: parentId }),
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.order !== undefined,
    );
  },
  "folder.rename": (draft, p) =>
    changeFolder(draft, p.id, (folder) => ({ ...folder, name: p.name })),
  "folder.move": (draft, p) => {
    const folder = draft.get("folders", canonical(p.id));
    if (folder === undefined) {
      return;
    }
    const parentId = canonical(p.parent_id);
    const { order, sure } = movedTo(
      draft,
      folder,
      folder.kind,
      folder.parent_id,
      parentId,
      p.order,
    );
    draft.put(
      "folders",
      {
        ...withField(folder, "parent_id", parentId),
        order,
        updated_at: draft.time,
      },
      sure,
    );
  },
  "folder.delete": (draft, p) => {
    draft.remove("folders", canonical(p.id));
  },
  // The folder in the trash, and those that it is in, which its restoration
  // brings back, are none that the document holds.
  "folder.restore": () => undefined,
  "project.rename": (draft, p) =>
    changeProject(draft, (project) => ({ ...project, name: p.name })),
  "project.set_slug": (draft, p) =>
    changeProject(draft, (project) => ({ ...project, slug: p.slug })),
  "project.describe": (draft, p) =>
    changeProject(draft, (project) => ({
      ...project,
      description: p.description,
    })),
  "project.update_settings": (draft, p) =>
    changeProject(draft, (project) => ({
      ...project,
      ...(p.default_locale === undefined
        ? {}
        : { default_locale: p.default_locale }),
      ...(p.publish_min_role === undefined
        ? {}
        : { publish_min_role: p.publish_min_role }),
      // The entry is kept as the payload spells it, as the server keeps it.
      ...(p.entry === undefined ? {} : { entry: p.entry }),
    })),
  "project.archive": (draft) =>
    changeProject(draft, (project) => ({
      ...project,
      archived_at: draft.time,
    })),
  "project.unarchive": (draft) =>
    changeProject(draft, (project) =>
      withField(project, "archived_at", undefined),
    ),
  "project.delete": (draft) =>
    changeProject(draft, (project) => ({
      ...project,
      deleted_at: draft.time,
      hard_delete_after: new Date(
        Date.parse(draft.time) + trashDays * 86_400_000,
      ).toISOString(),
    })),
  "project.restore": (draft) =>
    changeProject(draft, (project) =>
      withField(
        withField(project, "deleted_at", undefined),
        "hard_delete_after",
        undefined,
      ),
    ),
};
