// The tree of a project's boards (PRD FR-PROJ-02): its folders, which open
// and close, and its boards, each of which opens its page, with a menu of
// what the user's role may do to it, and, where the user may change the
// tree, a handle that drags it to another place, by mouse, pen, or touch:
// into a folder, or before or after another child. The menu's Move does the
// same for a keyboard, a screen reader, or a single tap (WCAG 2.5.7).

import type { ProjectBody } from "@storycreep/shared/api";
import {
  type QueryClient,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { Menu, MenuItem, MenuSeparator } from "../ui/Menu";
import { Alert, Status, textLink } from "../ui/Page";
import {
  CreateDialog,
  DeleteBoardDialog,
  DeleteFolderDialog,
  MoveDialog,
  moveCommands,
  nameOf,
  RenameDialog,
} from "./dialogs";
import { keyBetween } from "./orderkey";
import {
  conflictOf,
  readProjectAgain,
  type TreeData,
  treeQuery,
  useCommands,
} from "./queries";
import {
  buildTree,
  childrenOf,
  type Drop,
  type Placement,
  placementOf,
  type Tree,
  type TreeNode,
} from "./tree";

/** A dialog that the tree shows, with the control that opened it. */
type Open =
  | {
      dialog: "create";
      kind: "board" | "folder";
      parentId: string | undefined;
      opener: Opener;
    }
  | {
      dialog: "rename" | "move" | "delete";
      node: TreeNode;
      opener: Opener;
    };

/** How far, in pixels, a handle moves before it drags, so that a tap does not (PRD NFR-TOUCH-04). */
const dragThreshold = 8;

/** A drag of a child of the tree that has started, and where it would drop it. */
interface Dragging {
  node: TreeNode;
  /** Where the child would land if dropped now, or undefined for nowhere it can go. */
  drop: Drop | undefined;
}

export function BoardTree({
  project,
  editable,
}: {
  project: ProjectBody;
  editable: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const data = useSuspenseQuery(treeQuery(project.id)).data;
  const tree = useMemo(() => buildTree(data.folders, data.boards), [data]);
  const [closed, setClosed] = useState<ReadonlySet<string>>(new Set());
  const [open, setOpen] = useState<Open>();
  const [created, setCreated] = useState<string>();
  const move = useCommands(project.id);
  const [moved, setMoved] = useState<string>();
  const dropped = async (node: TreeNode, placement: Placement) => {
    setMoved(nameOf(node));
    const key = treeQuery(project.id).queryKey;
    // A read of the tree under way, as one that the window's focus began,
    // would answer with where the child was, over where it was dropped.
    await client.cancelQueries({ queryKey: key });
    const before = client.getQueryData(key);
    showMoved(client, project.id, tree, node, placement);
    move.mutate(moveCommands(node, placement), {
      // A move that was refused for a revision has the project read again
      // by useCommands. One refused otherwise, or that no answer came to,
      // shows the child where it was, until the project, read again, shows
      // where it is, rather than where it was dropped.
      onError: (error) => {
        if (!conflictOf(error)) {
          client.setQueryData(key, before);
          void readProjectAgain(client, project.id);
        }
      },
    });
  };
  // A drag waits for the move before it, whose batch, once it has applied,
  // has the tree read again, with the revisions that the next one expects.
  const drag = useTreeDrag(tree, move.isPending, (node, placement) => {
    void dropped(node, placement);
  });
  const toggle = (id: string) =>
    setClosed((before) => {
      const after = new Set(before);
      if (!after.delete(id)) {
        after.add(id);
      }
      return after;
    });
  const opens = (next: Open) => setOpen(next);
  const close = () => setOpen(undefined);
  // What a dialog renames, moves, or deletes, as the tree has it now, with
  // the revision that it read last, as once a refusal of the revision that
  // the dialog sent had it read again, so that the dialog's next attempt
  // expects that one, or, once it has gone from the tree, as it was.
  const node =
    open === undefined || open.dialog === "create"
      ? undefined
      : (tree.byId.get(open.node.id) ?? open.node);
  const listId = useId();
  return (
    <section className="space-y-4" aria-labelledby={listId}>
      <div className="flex flex-wrap items-center gap-2">
        <h2 id={listId} className="flex-1 text-lg font-semibold">
          {t("boards.title")}
        </h2>
        {editable && (
          <>
            <Button
              variant="primary"
              aria-haspopup="dialog"
              onClick={(event) =>
                opens({
                  dialog: "create",
                  kind: "board",
                  parentId: undefined,
                  opener: event.currentTarget,
                })
              }
            >
              {t("boards.new_board")}
            </Button>
            <Button
              aria-haspopup="dialog"
              onClick={(event) =>
                opens({
                  dialog: "create",
                  kind: "folder",
                  parentId: undefined,
                  opener: event.currentTarget,
                })
              }
            >
              {t("boards.new_folder")}
            </Button>
          </>
        )}
      </div>
      {editable && tree.top.length > 0 && (
        <p className="text-fg-muted">{t("boards.drag_hint")}</p>
      )}
      <Status
        message={
          created === undefined
            ? undefined
            : t("boards.created", { name: created })
        }
      />
      <Alert
        message={
          move.error === null
            ? undefined
            : t("boards.move_failed", {
                name: moved ?? "",
                reason: refusalOf(move.error, t).message ?? "",
              })
        }
      />
      {tree.top.length === 0 ? (
        <p className="text-fg-muted">{t("boards.empty")}</p>
      ) : (
        <div {...drag.area}>
          <TreeList
            nodes={tree.top}
            labelledBy={listId}
            projectId={project.id}
            editable={editable}
            closed={closed}
            onToggle={toggle}
            onOpen={opens}
            drag={drag}
          />
        </div>
      )}
      {open?.dialog === "create" && (
        <CreateDialog
          projectId={project.id}
          tree={tree}
          kind={open.kind}
          parentId={open.parentId}
          opener={open.opener}
          onClose={close}
          onCreated={setCreated}
        />
      )}
      {open?.dialog === "rename" && node !== undefined && (
        <RenameDialog
          projectId={project.id}
          node={node}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "move" && node !== undefined && (
        <MoveDialog
          projectId={project.id}
          tree={tree}
          node={node}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "delete" &&
        node !== undefined &&
        (node.kind === "folder" ? (
          <DeleteFolderDialog
            projectId={project.id}
            folder={node.folder}
            opener={open.opener}
            onClose={close}
          />
        ) : (
          <DeleteBoardDialog
            projectId={project.id}
            board={node.board}
            boards={data.boards}
            opener={open.opener}
            onClose={close}
          />
        ))}
    </section>
  );
}

/**
 * Shows in the tree's cache the child that a drag dropped where it landed,
 * at once, rather than where it was until the tree has been read again,
 * which the move's batch does once it has applied, or been refused.
 */
function showMoved(
  client: QueryClient,
  projectId: string,
  tree: Tree,
  node: TreeNode,
  placement: Placement,
): void {
  const last = childrenOf(tree, placement.parentId).findLast(
    (c) => c.id !== node.id,
  );
  const order =
    placement.order ?? keyBetween(last?.order, undefined) ?? node.order;
  client.setQueryData(
    treeQuery(projectId).queryKey,
    (data: TreeData | undefined) =>
      data === undefined
        ? data
        : {
            folders: data.folders.map((f) =>
              f.id === node.id
                ? withParent(f, "parent_id", placement.parentId, order)
                : f,
            ),
            boards: data.boards.map((b) =>
              b.id === node.id
                ? withParent(b, "folder_id", placement.parentId, order)
                : b,
            ),
          },
  );
}

/** Returns child with its parent's field set to parentId, or none, and its key to order. */
function withParent<T extends { order: string }>(
  child: T,
  field: "parent_id" | "folder_id",
  parentId: string | undefined,
  order: string,
): T {
  const { [field]: _, ...rest } = child as T & Record<string, unknown>;
  return {
    ...(rest as T),
    order,
    ...(parentId === undefined ? {} : { [field]: parentId }),
  };
}

interface ListProps {
  nodes: TreeNode[];
  labelledBy?: string;
  projectId: string;
  editable: boolean;
  closed: ReadonlySet<string>;
  onToggle: (id: string) => void;
  onOpen: (open: Open) => void;
  drag: TreeDrag;
}

/** What each item of the tree is given, as its list is. */
type ItemProps = Omit<ListProps, "nodes" | "labelledBy">;

/** A list of the tree's children, each folder with its own under it. */
function TreeList({ nodes, labelledBy, ...props }: ListProps) {
  return (
    <ul className="space-y-1" aria-labelledby={labelledBy}>
      {nodes.map((node) => (
        <TreeItem key={node.id} node={node} {...props} />
      ))}
    </ul>
  );
}

/** The classes that show where a drag would drop what it drags, by where. */
const dropMarks: Record<Drop["position"], string> = {
  before: "shadow-[inset_0_3px_0_var(--color-accent)]",
  after: "shadow-[inset_0_-3px_0_var(--color-accent)]",
  inside: "bg-surface-hover shadow-[inset_0_0_0_2px_var(--color-accent)]",
};

function TreeItem({ node, ...props }: ItemProps & { node: TreeNode }) {
  const { t } = useTranslation();
  const { projectId, editable, closed, onToggle, onOpen, drag } = props;
  const nameId = useId();
  const name = nameOf(node);
  const isOpen = node.kind === "folder" && !closed.has(node.id);
  const target = drag.dragging?.drop;
  const mark =
    target === undefined
      ? ""
      : target.position === "inside"
        ? target.folderId === node.id
          ? dropMarks.inside
          : ""
        : target.targetId === node.id
          ? dropMarks[target.position]
          : "";
  const dragged = drag.dragging?.node.id === node.id;
  // The item's menu, where the tree shows it once a dialog has closed: in
  // another place, once the dialog moved it, whose button is another.
  const opener = () =>
    document.querySelector<HTMLElement>(
      `[data-tree-actions="${CSS.escape(node.id)}"]`,
    );
  return (
    <li>
      <div
        data-tree-id={node.id}
        className={`flex min-h-11 items-center gap-1 rounded-md ${mark} ${dragged ? "opacity-60" : ""}`}
      >
        {editable && (
          // Dragging is for a pointer alone: a keyboard or a screen reader
          // moves with the menu's Move, so the handle is none of theirs.
          <span
            aria-hidden="true"
            className={`flex min-h-11 min-w-11 touch-none items-center justify-center rounded-md text-fg-muted select-none hover:bg-surface-hover ${drag.busy ? "cursor-progress" : "cursor-grab"}`}
            title={t("boards.drag", { name })}
            data-drag-handle
            onPointerDown={(event) => drag.start(event, node)}
          >
            ⠿
          </span>
        )}
        {node.kind === "folder" ? (
          <button
            type="button"
            id={nameId}
            className="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-start font-medium hover:bg-surface-hover"
            aria-expanded={isOpen}
            onClick={() => onToggle(node.id)}
          >
            <span aria-hidden="true">{isOpen ? "▾" : "▸"}</span>
            <span className="min-w-0 break-words">{name}</span>
          </button>
        ) : (
          <Link
            id={nameId}
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: node.id }}
            className={`flex min-h-11 min-w-0 flex-1 items-center px-2 break-words ${textLink}`}
          >
            {name}
          </Link>
        )}
        {editable && (
          <Menu
            trigger={
              <Button
                data-tree-actions={node.id}
                aria-label={t("boards.actions", { name })}
              >
                <span aria-hidden="true">⋯</span>
              </Button>
            }
          >
            {node.kind === "folder" && (
              <>
                <MenuItem
                  onSelect={() =>
                    onOpen({
                      dialog: "create",
                      kind: "board",
                      parentId: node.id,
                      opener,
                    })
                  }
                >
                  {t("boards.new_board_here")}
                </MenuItem>
                <MenuItem
                  onSelect={() =>
                    onOpen({
                      dialog: "create",
                      kind: "folder",
                      parentId: node.id,
                      opener,
                    })
                  }
                >
                  {t("boards.new_folder_here")}
                </MenuItem>
                <MenuSeparator />
              </>
            )}
            <MenuItem
              onSelect={() => onOpen({ dialog: "rename", node, opener })}
            >
              {t("boards.rename")}
            </MenuItem>
            <MenuItem onSelect={() => onOpen({ dialog: "move", node, opener })}>
              {t("boards.move")}
            </MenuItem>
            <MenuSeparator />
            <MenuItem
              danger
              onSelect={() => onOpen({ dialog: "delete", node, opener })}
            >
              {t("boards.delete")}
            </MenuItem>
          </Menu>
        )}
      </div>
      {node.kind === "folder" && isOpen && node.children.length > 0 && (
        <div className="ms-6 border-s border-border ps-2">
          <TreeList nodes={node.children} {...props} />
        </div>
      )}
    </li>
  );
}

/** What useTreeDrag gives the tree: the drag under way, and what starts one. */
interface TreeDrag {
  dragging: Dragging | undefined;
  /** Whether a drag waits for the move before it, while which none starts. */
  busy: boolean;
  /** Starts following a pointer that went down on node's handle. */
  start: (event: ReactPointerEvent<HTMLElement>, node: TreeNode) => void;
  /** The props of the element around the tree, within which a pointer is over the row at its height. */
  area: { "data-tree-area": true };
}

/** A drag that a pointer went down for, which has started once it has moved. */
interface DragUnderWay {
  node: TreeNode;
  /** Where the pointer went down. */
  x: number;
  y: number;
  /** Whether the pointer has moved dragThreshold pixels, which starts the drag. */
  moving: boolean;
  drop?: Drop;
}

/** How near to the window's top or bottom edge, in pixels, a drag scrolls the page. */
const scrollEdge = 48;

/**
 * Follows a drag of a child of tree by its handle, with the pointer that
 * went down on it, which the handle captures, so that a touch that moves
 * does not scroll the page instead: once it has moved dragThreshold pixels,
 * the row at its height says where the child would land, before or after
 * the row's child, by the row's top or bottom quarter, or, for a folder,
 * its middle half, inside it, and onDrop is told where it landed once the
 * pointer goes up. Escape, or a pointer that the browser takes back, as for
 * a gesture of its own, or once the handle has gone from the page, cancels
 * it. While busy, as while the move of the drag before is under way, no
 * drag starts.
 */
function useTreeDrag(
  tree: Tree,
  busy: boolean,
  onDrop: (node: TreeNode, placement: Placement) => void,
): TreeDrag {
  const [dragging, setDragging] = useState<Dragging>();
  const current = useRef<DragUnderWay>(null);
  const latest = useRef({ tree, busy, onDrop });
  latest.current = { tree, busy, onDrop };
  useEffect(() => {
    const cancel = (event: KeyboardEvent) => {
      if (event.key === "Escape" && current.current !== null) {
        current.current = null;
        setDragging(undefined);
      }
    };
    document.addEventListener("keydown", cancel);
    return () => document.removeEventListener("keydown", cancel);
  }, []);
  const start = (event: ReactPointerEvent<HTMLElement>, node: TreeNode) => {
    if (event.button !== 0 || latest.current.busy) {
      return;
    }
    event.preventDefault();
    const handle = event.currentTarget;
    handle.setPointerCapture(event.pointerId);
    // This drag, which Escape, or another drag, as of a second finger, ends
    // by putting another in its place.
    const drag: DragUnderWay = {
      node,
      x: event.clientX,
      y: event.clientY,
      moving: false,
    };
    current.current = drag;
    const move = (e: PointerEvent) => {
      if (current.current !== drag) {
        return;
      }
      if (
        !drag.moving &&
        Math.hypot(e.clientX - drag.x, e.clientY - drag.y) < dragThreshold
      ) {
        return;
      }
      const started = !drag.moving;
      drag.moving = true;
      if (e.clientY < scrollEdge) {
        window.scrollBy(0, -scrollEdge / 4);
      } else if (e.clientY > window.innerHeight - scrollEdge) {
        window.scrollBy(0, scrollEdge / 4);
      }
      const drop = dropAt(latest.current.tree, drag.node, e.clientX, e.clientY);
      // The tree shows the drag anew once it starts, and then once where it
      // would drop changes, rather than at every move of the pointer, which
      // would draw every row of the tree again, many times a second.
      if (started || !sameDrop(drop, drag.drop)) {
        drag.drop = drop;
        setDragging({ node: drag.node, drop });
      }
    };
    // The pointer went up, which drops the child, or the browser took it
    // back, for a gesture of its own, or since the handle went from the
    // page, which cancels the drag.
    const end = (e: PointerEvent) => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", end);
      handle.removeEventListener("pointercancel", end);
      handle.removeEventListener("lostpointercapture", end);
      if (current.current !== drag) {
        return;
      }
      current.current = null;
      setDragging(undefined);
      if (e.type !== "pointerup" || !drag.moving || drag.drop === undefined) {
        return;
      }
      const placement = placementOf(latest.current.tree, drag.node, drag.drop);
      if (placement !== undefined) {
        latest.current.onDrop(drag.node, placement);
      }
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", end);
    handle.addEventListener("pointercancel", end);
    handle.addEventListener("lostpointercapture", end);
  };
  return { dragging, busy, start, area: { "data-tree-area": true } };
}

/** Reports whether a and b drop a child in the same place, or both nowhere. */
function sameDrop(a: Drop | undefined, b: Drop | undefined): boolean {
  if (a === undefined || b === undefined) {
    return a === b;
  }
  return a.position === "inside"
    ? b.position === "inside" && b.folderId === a.folderId
    : b.position !== "inside" &&
        b.position === a.position &&
        b.targetId === a.targetId;
}

/**
 * Returns where node would land if dropped at the point x, y of the window,
 * within the tree, or undefined if nowhere it can go: before or after the
 * child of the row at the point's height, or inside a folder. The row is
 * the one whose height the point is at, whatever its indent, as over the
 * space before a folder's children, so that a pointer that goes straight
 * down from a handle meets every row on its way, or else the nearest, as
 * across the gap between two rows, rather than a place that no row shows.
 */
function dropAt(
  tree: Tree,
  node: TreeNode,
  x: number,
  y: number,
): Drop | undefined {
  const area = document
    .elementFromPoint(x, y)
    ?.closest<HTMLElement>("[data-tree-area]");
  if (!area) {
    return undefined;
  }
  let row: { id: string; box: DOMRect } | undefined;
  let nearest = Number.POSITIVE_INFINITY;
  for (const r of area.querySelectorAll<HTMLElement>("[data-tree-id]")) {
    const id = r.dataset.treeId;
    const box = r.getBoundingClientRect();
    const distance = Math.max(box.top - y, y - box.bottom, 0);
    if (id !== undefined && distance < nearest) {
      nearest = distance;
      row = { id, box };
    }
  }
  if (row === undefined) {
    return undefined;
  }
  const { id, box } = row;
  const at = (y - box.top) / Math.max(box.height, 1);
  const drop: Drop =
    tree.byId.get(id)?.kind === "folder" && at >= 0.25 && at <= 0.75
      ? { position: "inside", folderId: id }
      : { position: at < 0.5 ? "before" : "after", targetId: id };
  return placementOf(tree, node, drop) !== undefined ? drop : undefined;
}
