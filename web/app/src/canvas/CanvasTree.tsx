// The canvas's accessibility tree (PRD NFR-A11Y-03, NFR-A11Y-04, plan note
// N70): the board's nodes and connections, which the canvas draws as
// pixels, as a tree of the page's own, of roles and names, that assistive
// technology reads, and that the keyboard moves through, as it cannot
// through pixels. It is the stop after the canvas in the order of the Tab
// key, one stop, whose focus the arrow keys move, as in any tree: up and
// down through the nodes, in the order that a reader reads the board in
// (tree.ts), right into the connections that leave a node, and left back
// out of them, Home and End to the first and the last, and a letter typed
// to the next node whose title starts with it. Space selects what the focus
// is on, in place of the selection, or, with Shift, Ctrl, or Cmd, adds a
// node to it or takes it out, as a click does; Enter opens it, Delete
// deletes it, and Shift+F10, or the menu key, opens its context menu, each
// with the rest of the selection where it is of it, as a long press on it
// does, and none of them does anything of an item whose node, or whose
// connection, the board no longer has, though the tree, which is made again
// a moment after the board changes, shows it still. A letter typed is of
// itself alone, whatever was typed before it: the same letter again goes on
// to the next node that starts with it, and Space selects, and Backspace
// deletes, right after one as at any time. The canvas's other keys, which
// zoom, and Escape, do here what they do on the canvas.
// The tree is not drawn: the canvas shows what the focus is on, moving to
// it where it is out of view, with the focus ring around its node, or
// around its connection's line, which the canvas places in each frame.

import {
  type FocusEvent,
  type KeyboardEvent,
  type MouseEvent,
  memo,
  type Ref,
  startTransition,
  useEffect,
  useId,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { useProjectDocument } from "../document/react";
import { focusIsNowhere } from "../shell/focus";
import type { CanvasController } from "./controller";
import {
  type ClipboardAsk,
  clipboardKey,
  keyAction,
  menuKey,
} from "./keyboard";
import {
  type TreeChoice,
  type TreeRow,
  treeChoices,
  treeRows,
  typedRow,
} from "./tree";
import { middleOf, type Rect } from "./viewport";

/** What the tree's focus is on: a node, or a connection that leaves it. */
interface Item {
  node: string;
  connection?: string;
}

/** What the page asks of the canvas's tree. */
export interface TreeHandle {
  /** Gives the focus back to what the tree's focus was on last, which the canvas does not move to again, or, where the board no longer has it, to what is in its place, which the canvas shows, and reports whether it did, as it does not for a board without nodes. */
  focus(): boolean;
  /** Reports whether the focus is in the tree. */
  focused(): boolean;
}

function sameItem(a: Item | undefined, b: Item | undefined): boolean {
  return a?.node === b?.node && a?.connection === b?.connection;
}

/** Returns where item is on the board of controller: its node, or the rectangle that its connection's line takes, or undefined for one that the board no longer has. */
function rectOf(controller: CanvasController, item: Item): Rect | undefined {
  return item.connection === undefined
    ? controller.scene.nodes.rect(item.node)
    : controller.scene.connections.get(item.connection)?.bounds;
}

/** The classes of an item of the tree: at least a pixel high, with the connections under it, where it shows them. */
const itemClass = "min-h-px";

/** A node of the tree, with the connections that leave it, once it is expanded. */
const Row = memo(function Row({
  treeId,
  id,
  label,
  leaving,
  selected,
  expanded,
  tab,
  choices,
  chosen,
  tabChoice,
  selectable,
}: {
  treeId: string;
  id: string;
  label: string;
  leaving: number;
  selected: boolean;
  expanded: boolean;
  /** Whether the Tab key stops on the node. */
  tab: boolean;
  /** The connections that leave it, where it is expanded. */
  choices: readonly TreeChoice[] | undefined;
  /** The connection selected, where it is one of them. */
  chosen: string | undefined;
  /** The one of them that the Tab key stops on, if it does. */
  tabChoice: string | undefined;
  /** Whether a connection may be selected, as on a board that may be changed. */
  selectable: boolean;
}) {
  // An item has a box, a pixel high, in the tree's, which the page does not
  // show (sr-only), as what a page shows to assistive technology alone has:
  // an element without one, as an empty one is, is one that a screen
  // reader that goes by where things are may pass over.
  return (
    <div
      role="treeitem"
      id={`${treeId}-${id}`}
      data-node={id}
      aria-label={label}
      aria-selected={selected}
      aria-expanded={leaving > 0 ? expanded : undefined}
      tabIndex={tab ? 0 : -1}
      className={itemClass}
    >
      {choices !== undefined && choices.length > 0 && (
        // biome-ignore lint/a11y/useSemanticElements: the group of a tree's item, which a fieldset is not.
        <div role="group">
          {choices.map((choice) => (
            <div
              key={choice.id}
              role="treeitem"
              id={`${treeId}-${choice.id}`}
              data-node={id}
              data-connection={choice.id}
              aria-label={choice.label}
              aria-selected={selectable ? choice.id === chosen : undefined}
              tabIndex={choice.id === tabChoice ? 0 : -1}
              className={itemClass}
            />
          ))}
        </div>
      )}
    </div>
  );
});

/**
 * The accessibility tree of the canvas of controller, of the board named
 * boardName. It is rendered again for what it shows alone, the board's
 * content, the selection, and its own focus, rather than with the page
 * around it each time, as a tree of every node of the board.
 */
export const CanvasTree = memo(function CanvasTree({
  ref,
  projectId,
  boardName,
  controller,
  selection,
  connection,
  editable,
  onClipboard,
}: {
  ref?: Ref<TreeHandle>;
  projectId: string;
  boardName: string;
  controller: CanvasController;
  /** The nodes selected, and the connection selected, as the canvas tells them. */
  selection: ReadonlySet<string>;
  connection: string | undefined;
  /** Whether the user may change the board, which alone selects a connection. */
  editable: boolean;
  /** Copies, cuts, or pastes, as the clipboard's keys ask (clipboardKey), once the tree has selected what they act on. */
  onClipboard: (asked: ClipboardAsk) => void;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const treeId = useId();
  const root = useRef<HTMLDivElement>(null);
  const ring = useRef<HTMLDivElement>(null);
  // The board's content as the tree last read it, counted: the tree is
  // made again once it changes, after what the user waits for, as the frame
  // that draws the change, rather than before it (startTransition).
  const [seen, setSeen] = useState(0);
  useEffect(
    () => controller.watch(() => startTransition(() => setSeen((n) => n + 1))),
    [controller],
  );
  const [build] = useState(treeRows);
  // biome-ignore lint/correctness/useExhaustiveDependencies: seen counts the changes of what the canvas holds, which the rows are read from.
  const rows = useMemo<TreeRow[]>(
    () =>
      build(controller.scene, controller.described, controller.labelling(), t),
    [build, controller, t, seen],
  );
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  // The connections that leave each node that is expanded, and that the
  // board still has, read in one pass of the board's connections.
  // biome-ignore lint/correctness/useExhaustiveDependencies: seen counts the changes of what the canvas holds, which the connections are read from.
  const choices = useMemo(
    () => treeChoices(controller.scene, doc.store.getState().view, expanded, t),
    [controller, doc, expanded, t, seen],
  );
  // What the tree's focus was last on, and the nodes as the tree listed
  // them then, with where it was among them, by which what is in its place
  // is found once the board no longer has it.
  const [current, setCurrent] = useState<Item>();
  const place = useRef<{ rows: readonly TreeRow[]; at: number }>({
    rows: [],
    at: 0,
  });
  // Whether the focus is in the tree, which the canvas shows the ring for.
  const [inside, setInside] = useState(false);
  // Whether the item that the tree's focus is on is to take the browser's
  // focus once the tree is rendered, as after a key that moved it; and the
  // item that a key of the tree's deleted, which what is in its place takes
  // the focus of once the board no longer has it.
  const wantsFocus = useRef(false);
  const deleting = useRef<Item>(undefined);
  // Whether the focus that comes to an item is given back to the one that
  // had it last, as by the context menu as it goes (TreeHandle's focus),
  // which the canvas does not move to again.
  const givenBack = useRef(false);

  const index = useMemo(() => {
    const of = new Map<string, number>();
    rows.forEach((row, i) => {
      of.set(row.id, i);
    });
    return of;
  }, [rows]);
  // What the focus is on, of what the tree has: what it was last on, the
  // node of a connection that the tree no longer shows, as one deleted, or
  // of a node collapsed, the node in the place of one that the board no
  // longer has, or, before any, the first node selected, or the first node.
  // In the place of a node that went is the first after it, as the tree
  // listed them, that the board still has, or else the last before it: not
  // the node that is as far down the tree now as it was, which is further
  // on by each node before it that went with it, as the rest of a
  // selection that Delete deleted.
  const on = useMemo<Item | undefined>(() => {
    if (rows.length === 0) {
      return undefined;
    }
    if (current === undefined) {
      return {
        node: rows.find((row) => selection.has(row.id))?.id ?? rows[0].id,
      };
    }
    if (!index.has(current.node)) {
      const { rows: were, at } = place.current;
      for (let i = at; i < were.length; i++) {
        if (index.has(were[i].id)) {
          return { node: were[i].id };
        }
      }
      for (let i = Math.min(at, were.length) - 1; i >= 0; i--) {
        if (index.has(were[i].id)) {
          return { node: were[i].id };
        }
      }
      return { node: rows[Math.min(at, rows.length - 1)].id };
    }
    if (
      current.connection !== undefined &&
      choices.get(current.node)?.some((c) => c.id === current.connection) !==
        true
    ) {
      return { node: current.node };
    }
    return current;
  }, [rows, index, choices, current, selection]);
  useLayoutEffect(() => {
    if (on !== undefined) {
      place.current = { rows, at: index.get(on.node) ?? 0 };
    }
  }, [on, rows, index]);

  const elementOf = (item: Item): HTMLElement | null =>
    document.getElementById(`${treeId}-${item.connection ?? item.node}`);

  // The browser's focus follows the tree's, where a key moved it, and where
  // what had it went from the tree, as a node deleted, which leaves the
  // browser's focus on nothing.
  useLayoutEffect(() => {
    if (on === undefined) {
      // A board that has no node left has no tree, whose focus went with
      // its last item, as one that the tree's Delete deleted: the tree
      // that the board's next node makes takes no focus of its own, and
      // shows none on the canvas.
      wantsFocus.current = false;
      deleting.current = undefined;
      if (inside) {
        setInside(false);
      }
      return;
    }
    if (root.current === null) {
      return;
    }
    const lost = inside && focusIsNowhere(document);
    const deleted =
      deleting.current !== undefined && !sameItem(deleting.current, on);
    if (wantsFocus.current || lost || deleted) {
      wantsFocus.current = false;
      deleting.current = undefined;
      elementOf(on)?.focus({ preventScroll: true });
    }
  });

  useImperativeHandle(ref, () => ({
    focus: () => {
      const el = on === undefined ? null : elementOf(on);
      if (el === null) {
        return false;
      }
      // The item that had the focus takes it back without the canvas
      // moving to it, as it does when the focus comes to an item
      // (focusIn): what gave the focus away may have moved the canvas
      // since, as the menu's Go to target, or a press on the minimap, which
      // closes the menu, and the move would else be undone. What is in the
      // place of an item that the board no longer has is shown, as after
      // the tree's own Delete.
      givenBack.current = sameItem(on, current);
      el.focus({ preventScroll: true });
      givenBack.current = false;
      return true;
    },
    focused: () => root.current?.contains(document.activeElement) === true,
  }));

  // The focus ring, around what the focus is on, where the canvas has it,
  // which the canvas places in each frame, as it places a handle.
  const shown = useRef<Item>(undefined);
  useLayoutEffect(() => {
    shown.current = inside ? on : undefined;
    controller.invalidate();
  }, [controller, inside, on]);
  useEffect(
    () =>
      controller.follow(() => {
        const el = ring.current;
        if (el === null) {
          return;
        }
        const item = shown.current;
        const r = item === undefined ? undefined : rectOf(controller, item);
        el.hidden = r === undefined;
        if (r !== undefined) {
          const at = controller.screenRectOf(r);
          el.style.transform = `translate(${at.x}px, ${at.y}px)`;
          el.style.width = `${at.width}px`;
          el.style.height = `${at.height}px`;
        }
      }),
    [controller],
  );

  if (rows.length === 0 || on === undefined) {
    return null;
  }

  /** Returns the item of the tree that holds target, if one does. */
  const itemOf = (target: EventTarget | null): Item | undefined => {
    const el =
      target instanceof Element
        ? target.closest<HTMLElement>("[role=treeitem]")
        : null;
    const node = el?.dataset.node;
    return el == null || node === undefined
      ? undefined
      : { node, connection: el.dataset.connection };
  };

  /** Moves the tree's focus, and the browser's, to item. */
  const go = (item: Item) => {
    if (!sameItem(item, current)) {
      setCurrent(item);
    }
    if (sameItem(item, on)) {
      // The item that the tree's focus is on already is rendered so.
      elementOf(item)?.focus({ preventScroll: true });
    } else {
      wantsFocus.current = true;
    }
  };

  /** The items that the tree shows, in their order: each node, and, of one that is expanded, the connections that leave it. */
  const items = (): Item[] =>
    rows.flatMap((row) => [
      { node: row.id },
      ...(expanded.has(row.id)
        ? (choices.get(row.id) ?? []).map((c) => ({
            node: row.id,
            connection: c.id,
          }))
        : []),
    ]);

  /**
   * Selects item, and reports whether it did: in place of the selection
   * (replace); or, of a node, added to the selection, or taken out of it
   * (toggle); or, of a node of the selection, with the rest of it, as it
   * is, and else in place of it (keep), as a key of the canvas's that acts
   * on what is selected asks. A connection is selected in place of the
   * selection each way, and is not on a board that may not be changed. An
   * item that the board no longer has, as one that another session deleted
   * a moment ago, which the tree shows until it is made again, selects
   * nothing, and its key does nothing, rather than act on what was selected
   * before it, as a Delete that deleted the nodes selected in place of a
   * connection that had gone.
   */
  const select = (item: Item, how: "replace" | "toggle" | "keep"): boolean => {
    if (item.connection !== undefined) {
      controller.selectConnection(item.connection);
      return controller.selectedConnection() === item.connection;
    }
    if (!controller.has(item.node)) {
      return false;
    }
    if (how !== "keep" || !controller.isSelected(item.node)) {
      controller.select([item.node], how === "toggle" ? "toggle" : "replace");
    }
    return true;
  };
  /** Returns how a Space, or a click, of e selects: with Shift, Ctrl, or Cmd, it adds a node to the selection, or takes it out. */
  const howOf = (e: {
    shiftKey: boolean;
    ctrlKey: boolean;
    metaKey: boolean;
  }) => (e.shiftKey || e.ctrlKey || e.metaKey ? "toggle" : "replace");

  const keyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    deleting.current = undefined;
    const item = itemOf(e.target) ?? on;
    const modified = e.ctrlKey || e.metaKey || e.altKey;
    if (menuKey(e)) {
      const r = rectOf(controller, item);
      // As on the canvas, not while the canvas follows a pointer, as in the
      // middle of a drag, which the menu's items would change the nodes of.
      if (r !== undefined && !controller.gestures.following()) {
        // The menu opens at the item's middle, which the canvas shows
        // first, as it does when the focus comes to the item, where it has
        // moved away from it since, as by a wheel, or by a key that zooms:
        // the menu would else open out of view.
        controller.reveal(r);
        const at = middleOf(r);
        controller.menu(
          item.connection === undefined
            ? { kind: "node", node: item.node, at }
            : { kind: "connection", connection: item.connection, at },
        );
      }
      e.preventDefault();
      return;
    }
    // Copy and Cut act on the node that the focus is on, with the rest of
    // the selection where it is of it, as Delete does, and Cut, as Delete,
    // on a board that may be changed alone: elsewhere it selects nothing;
    // Paste pastes, wherever the focus is. None acts in the middle of a
    // drag, as the menu does not, nor for the repeats of a key held down.
    const asked = clipboardKey(e);
    if (asked !== undefined) {
      if (
        !e.repeat &&
        !controller.gestures.following() &&
        (asked === "paste" ||
          (item.connection === undefined &&
            (asked === "copy" || controller.movable()) &&
            select(item, "keep")))
      ) {
        onClipboard(asked);
      }
      e.preventDefault();
      return;
    }
    if (e.key === " " && !e.altKey) {
      select(item, howOf(e));
      e.preventDefault();
      return;
    }
    if (modified) {
      return;
    }
    const all = items();
    const at = all.findIndex((i) => sameItem(i, item));
    const row = index.get(item.node) ?? 0;
    switch (e.key) {
      case "ArrowDown":
        go(all[Math.min(at + 1, all.length - 1)]);
        break;
      case "ArrowUp":
        go(all[Math.max(at - 1, 0)]);
        break;
      case "Home":
        go(all[0]);
        break;
      case "End":
        go(all[all.length - 1]);
        break;
      case "ArrowRight":
        if (item.connection === undefined && rows[row].leaving > 0) {
          if (!expanded.has(item.node)) {
            setExpanded(new Set(expanded).add(item.node));
          } else {
            const first = choices.get(item.node)?.[0];
            if (first !== undefined) {
              go({ node: item.node, connection: first.id });
            }
          }
        }
        break;
      case "ArrowLeft":
        if (item.connection !== undefined) {
          go({ node: item.node });
        } else if (expanded.has(item.node)) {
          const next = new Set(expanded);
          next.delete(item.node);
          setExpanded(next);
        }
        break;
      case "Enter":
        // On a board that may be changed, as the canvas's own Enter is,
        // and as Delete is below: elsewhere the key selects nothing, as it
        // opens nothing.
        if (controller.movable() && select(item, "replace")) {
          controller.key(e);
        }
        break;
      case "Delete":
      case "Backspace":
        // On a board that may be changed, as the canvas's own Delete is:
        // elsewhere the key selects nothing, as it deletes nothing. The
        // focus goes to what is in the place of what the key deleted, once
        // the board no longer has it.
        if (controller.movable() && select(item, "keep") && controller.key(e)) {
          deleting.current = item;
        }
        break;
      default: {
        // The canvas's other keys, which zoom, and Escape, but not those
        // that pan, which are the tree's arrows here; and else a letter
        // typed, which goes to the next node whose title starts with it,
        // of itself alone, whatever was typed before it, so that the same
        // letter again goes on to the node after, and a character that is
        // a key of the canvas's is always that key.
        const action = keyAction(e);
        if (action !== undefined && action.kind !== "pan") {
          if (!controller.key(e)) {
            return;
          }
          break;
        }
        if (e.key.length !== 1) {
          return;
        }
        const found = typedRow(rows, row, e.key);
        if (found !== undefined) {
          go({ node: rows[found].id });
        }
        break;
      }
    }
    e.preventDefault();
  };

  const focusIn = (e: FocusEvent<HTMLDivElement>) => {
    const item = itemOf(e.target);
    if (item === undefined) {
      return;
    }
    setInside(true);
    if (!sameItem(item, current)) {
      setCurrent(item);
    }
    const r = givenBack.current ? undefined : rectOf(controller, item);
    if (r !== undefined) {
      controller.reveal(r);
    }
  };
  // The focus that an item loses as the board loses it, as a node
  // deleted, has left nothing: what is in its place takes it.
  const focusOut = (e: FocusEvent<HTMLDivElement>) => {
    if (e.relatedTarget === null && !e.target.isConnected) {
      return;
    }
    if (!e.currentTarget.contains(e.relatedTarget)) {
      setInside(false);
      deleting.current = undefined;
    }
  };
  // A click of an item, as a screen reader makes one of what its cursor is
  // on, selects it, as Space does, and gives it the focus.
  const click = (e: MouseEvent<HTMLDivElement>) => {
    const item = itemOf(e.target);
    if (item !== undefined) {
      select(item, howOf(e));
      go(item);
    }
  };

  return (
    <>
      <div
        ref={root}
        role="tree"
        aria-label={t("canvas.tree.label", { board: boardName })}
        aria-multiselectable="true"
        onKeyDown={keyDown}
        onFocus={focusIn}
        onBlur={focusOut}
        onClick={click}
        // The browser's own menu does not open of an item, as it does not
        // over the canvas: the menu key asks for it as it goes up, on
        // Windows, where the focus is by then, which is here still where
        // the key's going down opened no menu of the canvas's, as of a
        // connection on a board that may not be changed.
        onContextMenu={(e) => e.preventDefault()}
        className="sr-only top-0 left-0"
      >
        {rows.map((row) => {
          const open = expanded.has(row.id);
          return (
            <Row
              key={row.id}
              treeId={treeId}
              id={row.id}
              label={row.label}
              leaving={row.leaving}
              selected={selection.has(row.id)}
              expanded={open}
              tab={on.node === row.id && on.connection === undefined}
              choices={open ? choices.get(row.id) : undefined}
              chosen={open ? connection : undefined}
              tabChoice={on.node === row.id ? on.connection : undefined}
              selectable={editable}
            />
          );
        })}
      </div>
      {/* The focus ring of the tree's item, over the canvas, within its
      edges, as a handle is. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 overflow-hidden rounded-md"
      >
        <div
          ref={ring}
          hidden
          data-tree-focus=""
          className="absolute top-0 left-0 rounded-md outline-3 outline-offset-2 outline-focus"
        />
      </div>
    </>
  );
});
