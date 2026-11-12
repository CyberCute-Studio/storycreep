// A board's canvas (PRD FR-EDIT-01, FR-EDIT-11, ADR-013): the board's nodes
// and connections, as the project's document holds them, drawn by PixiJS,
// with a toolbar that switches what one finger's drag does, pan or select
// (NFR-TOUCH-03), zooms, fits the content, zooms to the selection, and leads
// to the board's settings under the canvas, and a minimap. The canvas
// suppresses the browser's own gestures over it, as pull-to-refresh and
// overscroll (NFR-TOUCH-08), and its menu, and the toolbar's controls are of
// the size of a finger, and 8 px apart, within a group as between groups,
// so that a tap a little off one does not press its neighbour
// (NFR-TOUCH-01). On a board that the user may change, it creates, moves,
// resizes, duplicates, and deletes nodes, and its inspector changes what is
// selected (FR-EDIT-02, FR-EDIT-06, plan note N68): the toolbar adds a node,
// duplicates and deletes the selection, snaps to the grid or not, dims the
// nodes without a tag, opens the inspector, and undoes and redoes the
// user's changes (FR-EDIT-08), and a bulk move offers its undo for a while
// after (NFR-TOUCH-05). The one selected node has a link handle on each
// port that connections leave, whose drag connects it, and a tap on a
// connection's line selects the connection, whose ends' handles reconnect
// it (FR-EDIT-03, plan note N69); a jumper selected has a link to its
// target, and one whose target was deleted is drawn as broken (FR-EDIT-05).
// A finger's long press, a right click, or Shift+F10 opens the context menu
// of what is selected, or of the board there (FR-EDIT-09, NFR-TOUCH-02);
// the board's nodes and connections are a tree that assistive technology
// reads, and the keyboard moves through, after the canvas (CanvasTree.tsx), and
// the toolbar says each change of the selection in a live region
// (NFR-A11Y-03, NFR-A11Y-04, plan note N70).

import type {
  Node as BoardNode,
  Clipboard,
  ClipboardReport,
} from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  lazy,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  Suspense,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { useShallow } from "zustand/react/shallow";
import { createStore, type StoreApi } from "zustand/vanilla";
import { api, notFoundBy, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { commandOf } from "../commands/catalog";
import { bindingText } from "../commands/keys";
import {
  type Handlers,
  runHandler,
  useCommands,
  usePlatform,
  useRegistry,
} from "../commands/registry";
import { boardPart, type Part } from "../document/doc";
import {
  useDocumentState,
  useDocuments,
  useProjectDocument,
} from "../document/react";
import {
  HistoryButtons,
  type HistoryOf,
  HistoryStatus,
  useHistory,
  useHistoryCommands,
  useOwnHistory,
} from "../project/History";
import { loadPart } from "../project/queries";
import { followInPage, showInPage } from "../shell/focus";
import { Button, buttonClass, LastingButton, pressedClass } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { Menu, MenuItem, MenuSeparator } from "../ui/Menu";
import { useMediaQuery } from "../ui/media";
import { Alert } from "../ui/Page";
import { besidePanel, useDocked } from "../ui/Panel";
import { type CanvasActions, type Refused, useCanvasActions } from "./actions";
import { CanvasTree, type TreeHandle } from "./CanvasTree";
import { ContextMenu, ContextMenus } from "./ContextMenu";
import { lacksAny } from "./clipboard";
import { maxEdited } from "./commands";
import {
  type BenchOptions,
  CanvasController,
  type CanvasView,
  type PortOf,
  renderedMark,
  type SavedView,
} from "./controller";
import { hasTagOf, tagKey, tagsOf } from "./edit";
import type { Engine, RendererKind } from "./engine";
import type { Handle, MenuTarget, Mode, PointerInput } from "./gestures";
import { Inspector } from "./Inspector";
import { type ClipboardAsk, canvasKey, clipboardKey } from "./keyboard";
import {
  ConnectOffer,
  endPlace,
  type FollowLink,
  LinkHandle,
  portPlace,
  resizeTakes,
  TargetLink,
} from "./Links";
import {
  type LabelContext,
  type LabelSources,
  labelContext,
  labelsOf,
  namesChanged,
  titleOf,
  typeOf,
} from "./labels";
import { portsOf } from "./layout";
import { Minimap } from "./minimap";
import { PasteDialog } from "./PasteDialog";
import { ResizeHandle } from "./ResizeHandle";
import type { Scene } from "./scene";
import { TextEditor, type TextEditorHandle } from "./TextEditor";
import { linkOf, type TargetLookup, TargetReads, targetOf } from "./targets";
import { readTheme } from "./theme";
import { centerOf, type Point } from "./viewport";

const Bench = lazy(() => import("./bench"));

/** What the board's page asks of its canvas. */
export interface CanvasOptions {
  /** The renderer to use, whatever the browser's would be, as the benchmark and the tablet's checklist compare them. */
  renderer?: RendererKind;
  bench?: BenchOptions;
}

const reducedMotionQuery = "(prefers-reduced-motion: reduce)";
const darkQuery = "(prefers-color-scheme: dark)";
const forcedColorsQuery = "(forced-colors: active)";

/** The parts of the project's document that the names that cards show are of: its speakers, and the variables that their texts show. */
const nameParts: readonly Part[] = ["components", "variables"];

/** The zoom that the toolbar shows, as the canvas tells it, in a store of its own. */
type ZoomShown = StoreApi<number>;

/** Where the device keeps whether the canvas snaps to the grid (FR-EDIT-02), which every board's canvas shares. */
const snapKey = "storycreep.canvas.snap";

/** Returns whether the device's canvas snaps, as it kept it, or does by default where it kept nothing, as in a private window. */
function snapKept(): boolean {
  try {
    return window.localStorage.getItem(snapKey) !== "off";
  } catch {
    return true;
  }
}

/** Keeps whether the device's canvas snaps, where the browser's storage lets it. */
function keepSnap(on: boolean): void {
  try {
    window.localStorage.setItem(snapKey, on ? "on" : "off");
  } catch {
    // A browser that keeps nothing, as a private window's, snaps anew.
  }
}

/** The classes of a refusal that the canvas says over it, whose box has the page's background, as the cards under it would else show through. */
const messageAlert =
  "rounded-md border border-danger bg-canvas px-4 py-3 text-danger shadow-md";

/** How long the undo of a bulk move stays offered, in milliseconds (NFR-TOUCH-05). */
const undoMoveTime = 10_000;

/**
 * Returns what the toolbar says of a refusal of of, for reason, the
 * server's: that the nodes could not be copied, or pasted, or else that the
 * change could not be saved (useCanvasActions' refused).
 */
function refusedMessage(
  t: ReturnType<typeof useTranslation>["t"],
  of: Refused | undefined,
  reason: string,
): string {
  switch (of) {
    case "copy":
      return t("canvas.copy_failed", { reason });
    case "paste":
      return t("canvas.paste_failed", { reason });
    default:
      return t("canvas.edit_failed", { reason });
  }
}

/**
 * Returns what returns the tags of the nodes of scene, each once, whatever
 * its case, the most used first (tagsOf), which the tag filter offers: the
 * same list, found from the board's nodes alone, until one of them comes,
 * goes, or changes its tags, rather than one found anew from every node of
 * the project for each change of the document, as of a move or of another
 * session's keystroke, under which the page would render anew.
 */
function tagsOfScene(scene: Scene): () => string[] {
  let version: number | undefined;
  let lists: string[][] = [];
  let tags: string[] = [];
  return () => {
    if (scene.version === version) {
      return tags;
    }
    version = scene.version;
    const now: string[][] = [];
    scene.nodes.forEach((_, item) => {
      if (item.node.tags.length > 0) {
        now.push(item.node.tags);
      }
    });
    if (now.length !== lists.length || now.some((l, i) => l !== lists[i])) {
      lists = now;
      const found = tagsOf(now.map((list) => ({ tags: list })));
      if (
        found.length !== tags.length ||
        found.some((tag, i) => tag !== tags[i])
      ) {
        tags = found;
      }
    }
    return tags;
  };
}

/**
 * The undo of the bulk move that the canvas made last, which it offers for
 * a while from when it joined the history (NFR-TOUCH-05), though the board
 * could not be changed for some of it, as while the project was archived,
 * rather than for as long anew once it can be again, and until another
 * step joins or leaves it, as once the user changed or undid anything
 * else, whose undo it would else be; and which it does not send while a
 * batch of the page's is under way, which the undo would wait for to join
 * the history, and then undo in place of the move (ProjectDocument's undo).
 */
function UndoMove({
  projectId,
  moved,
  history,
  onDone,
}: {
  projectId: string;
  moved: { key: number; count: number; version: number; until: number };
  history: HistoryOf;
  /** Stops offering the undo of the move of key, and of no move after it. */
  onDone: (key: number) => void;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const sending = useDocumentState(doc, (state) => state.sending > 0);
  // The history's version, rather than how many steps it has, which a step
  // that joins a history that holds as many as it keeps leaves as they were.
  const stale = history.version !== moved.version;
  const { key, until } = moved;
  // Before the browser paints the offer, so that one whose time is up, as
  // it comes back once the board can be changed again, is not shown.
  useLayoutEffect(() => {
    const left = until - Date.now();
    if (stale || left <= 0) {
      onDone(key);
      return;
    }
    const timer = setTimeout(() => onDone(key), left);
    return () => clearTimeout(timer);
  }, [stale, onDone, key, until]);
  if (stale) {
    return null;
  }
  return (
    <p
      role="status"
      className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-surface px-4 py-2"
    >
      {t("canvas.moved", { count: moved.count })}
      <Button
        disabled={history.stepping || sending}
        onClick={() => {
          // As the document is now, rather than as the page last rendered it.
          const now = doc.store.getState();
          if (now.sending > 0) {
            return;
          }
          if (now.historyVersion === moved.version) {
            history.step.mutate("undo");
          }
          onDone(key);
        }}
      >
        {t("canvas.undo_move")}
      </Button>
    </p>
  );
}

/**
 * The toolbar's button of the zoom, which shows it and shows 100% again: it
 * alone follows the zoom, which a pinch changes in nearly every frame, so
 * that the rest of the canvas's page is not rendered again with it.
 */
function ZoomReset({
  zoom,
  onClick,
}: {
  zoom: ZoomShown;
  onClick: () => void;
}) {
  const { t, i18n } = useTranslation();
  const value = useStore(zoom);
  const percent = formatNumber(i18n.language, value, {
    style: "percent",
    maximumFractionDigits: 0,
  });
  return (
    <Button
      aria-label={t("canvas.zoom_reset", { zoom: percent })}
      onClick={onClick}
      className="min-w-20 tabular-nums"
    >
      {percent}
    </Button>
  );
}

/**
 * Returns the size of the canvas's element, in CSS pixels, which the
 * renderer's canvas, the rubber band, and the points of the pointers are
 * laid out in: its whole box, as its edge is a ring drawn outside it rather
 * than a border within it.
 */
function sizeOf(host: HTMLElement): { width: number; height: number } {
  const r = host.getBoundingClientRect();
  return { width: r.width, height: r.height };
}

/** Returns the input of a pointer's event, at its point of the element at rect. */
function pointerInput(e: PointerEvent, rect: DOMRect): PointerInput {
  return {
    id: e.pointerId,
    x: e.clientX - rect.left,
    y: e.clientY - rect.top,
    kind:
      e.pointerType === "touch"
        ? "touch"
        : e.pointerType === "pen"
          ? "pen"
          : "mouse",
    button: e.button,
    buttons: e.buttons,
    shift: e.shiftKey,
    additive: e.ctrlKey || e.metaKey,
  };
}

/** What the canvas of a board is given. */
interface BoardCanvasProps {
  projectId: string;
  boardId: string;
  boardName: string;
  /** The ID of the heading of the board's settings, under the canvas, which the toolbar links to, whose element takes the focus. */
  settingsId?: string;
  /**
   * The read of the board's nodes and connections that the page's loader
   * started, which is the canvas's first, rather than a read of its own.
   */
  read?: Promise<void>;
  options?: CanvasOptions;
  /**
   * What the canvas showed of the board before, which it opens with in
   * place of the fit to the board's content (PRD FR-PROJ-04), as it was
   * when the canvas was mounted.
   */
  initial?: SavedView;
  /** Tells what the canvas shows, from the frame that placed it on, as it changes (CanvasEvents' view). */
  onView?: (view: CanvasView) => void;
  /** The ID of the workspace of the project, in whose projects what the page's clipboard holds of it is pasted (FR-EDIT-07). */
  workspaceId: string;
  /** Whether the user may change the board's nodes, as an editor of a project that is not archived. */
  editable?: boolean;
  /**
   * The node that the canvas opens on, selected and shown whole, in place of
   * the fit or the view that it restores, as a link to the node asks (plan
   * item 61), or, once it changes, shows at once.
   */
  focus?: string;
}

/**
 * The canvas of a board of a project. Its controller, whose scene holds the
 * nodes of one board alone, and the loader's read, are of the board that it
 * was first given: a page that gives it another board in place, without
 * the router's remount that the board's page has, shows that board's canvas
 * anew, rather than read the other board's content into a scene that keeps
 * none of it, under a toolbar that names it.
 */
export function BoardCanvas(props: BoardCanvasProps) {
  return (
    <BoardCanvasOf key={`${props.projectId}/${props.boardId}`} {...props} />
  );
}

/** The canvas of the one board that it was mounted with (BoardCanvas). */
function BoardCanvasOf({
  projectId,
  boardId,
  boardName,
  settingsId,
  read,
  options = {},
  initial,
  onView,
  workspaceId,
  editable = false,
  focus,
}: BoardCanvasProps) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  // What the page's clipboard holds, which pastes here where it was copied
  // in this workspace, by the account that the page is signed in to.
  const documents = useDocuments();
  const userId = useSuspenseQuery(sessionQuery).data?.user_id;
  const held = useStore(documents.clipboard);
  const copied =
    held !== undefined &&
    held.workspaceId === workspaceId &&
    held.userId === userId
      ? held
      : undefined;
  // The page's latest onView, which the controller's events call.
  const onViewRef = useRef(onView);
  useLayoutEffect(() => {
    onViewRef.current = onView;
  });
  const helpId = useId();
  const hostRef = useRef<HTMLDivElement>(null);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const bandRef = useRef<HTMLDivElement>(null);
  // The layer of what the canvas places over its nodes, outside its element.
  const overRef = useRef<HTMLDivElement>(null);
  const minimapRef = useRef<HTMLCanvasElement>(null);
  /** The pointer whose press went down on the minimap, which its drag follows. */
  const minimapPress = useRef<number>(undefined);
  const [mode, setMode] = useState<Mode>("pan");
  const [selection, setSelection] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const selected = selection.size;
  // The connection selected, in place of nodes, and what a connection
  // dropped on the empty board offers: to create a node at at of the board
  // and connect from to it (FR-EDIT-03 AC3), each as the canvas tells it
  // (CanvasEvents), which holds them.
  const [connection, setConnection] = useState<string>();
  // The changes of the selection, of nodes or of a connection, counted,
  // each of which the toolbar's live region says (NFR-A11Y-03).
  const [selections, setSelections] = useState(0);
  const [offer, setOffer] = useState<{ from: PortOf; at: Point }>();
  // The context menu that the canvas asked for, of what is selected, or of
  // the board at a point of it (FR-EDIT-09), each of a key of its own, and
  // whether the focus was in the canvas's tree as it was asked for, which
  // takes it back then, as the canvas does else.
  const [menu, setMenu] = useState<{
    key: number;
    target: MenuTarget;
    tree: boolean;
  }>();
  const menus = useRef(0);
  const tree = useRef<TreeHandle>(null);
  const [snap, setSnap] = useState(snapKept);
  const [tagFilter, setTagFilter] = useState("");
  const [inspecting, setInspecting] = useState(false);
  const inspectorButton = useRef<HTMLButtonElement>(null);
  // The open inspector's heading, which Enter on a branch or a jumper gives
  // the focus, as it opens the inspector.
  const inspectorHeading = useRef<HTMLHeadingElement>(null);
  const docked = useDocked();
  // The inspector, opened, or, where it is open already, as docked beside
  // the canvas, given the focus, as one that opens takes it: as Enter on a
  // branch, a jumper, or a connection asks, and the context menu.
  const inspect = useCallback(() => {
    setInspecting(true);
    inspectorHeading.current?.focus();
  }, []);
  // The element whose text the canvas's editor shows over it, if any.
  const [editing, setEditing] = useState<string>();
  // The last bulk move, once it joined the history, which the canvas offers
  // the undo of for a while (NFR-TOUCH-05).
  const [moved, setMoved] = useState<{
    key: number;
    count: number;
    version: number;
    /** When the offer ends, by Date.now(). */
    until: number;
  }>();
  // How many bulk moves the canvas offered the undo of, which keys each.
  const moves = useRef(0);
  const [zoom] = useState<ZoomShown>(() => createStore<number>(() => 1));
  const [content, setContent] = useState<"loading" | "ready" | "failed">(
    "loading",
  );
  const [attempt, setAttempt] = useState(0);
  // The loader's read, of the document that it was of, which the canvas's
  // first read of that document takes, as often as React runs its effect,
  // as twice under StrictMode, rather than once, after which the second
  // would ask the server again.
  const [loaderRead] = useState(() => ({ doc, read }));
  // The reads of the names that cards show, counted, which each read of
  // the board's content that succeeded starts, and a failure of which the
  // toolbar says, with Try again, which starts another.
  const [namesRead, setNamesRead] = useState(0);
  const [namesFailed, setNamesFailed] = useState(false);
  const [engine, setEngine] = useState<"starting" | "running" | "unsupported">(
    "starting",
  );
  // What the fetch of the renderer's chunk failed with, which the page
  // throws to its route's error (RouteError), as it does a chunk of its own.
  const [engineFailure, setEngineFailure] = useState<{ error: unknown }>();
  const [empty, setEmpty] = useState(false);
  const [showMinimap, setShowMinimap] = useState(true);
  const [height, setHeight] = useState<number>();
  // Whether the system asks for reduced motion (NFR-A11Y-07), as it changes.
  const reducedMotion = useMediaQuery(reducedMotionQuery, false);
  // Whether the system's theme is dark, which the page's colours, and so
  // the canvas's, follow, as it changes.
  const dark = useMediaQuery(darkQuery, false);
  // Whether the system forces its own colours on the page, as Windows's
  // contrast themes do, which the browser draws the page's elements in, but
  // not the canvas's pixels, which the canvas draws in them itself.
  const forcedColors = useMediaQuery(forcedColorsQuery, false);
  const [controller] = useState(() => {
    const c = new CanvasController(boardId, {
      selection: (ids) => {
        setSelection(ids);
        setSelections((n) => n + 1);
      },
      connection: (id) => {
        setConnection(id);
        setSelections((n) => n + 1);
      },
      offer: (o) => setOffer(o),
      menu: (target, held) => {
        setMenu({
          key: ++menus.current,
          target,
          tree: tree.current?.focused() === true,
        });
        if (held) {
          // A long press that opens the menu is felt, where the device can
          // (NFR-TOUCH-02).
          try {
            navigator.vibrate?.(10);
          } catch {
            // A browser that refuses to vibrate, as before a first touch.
          }
        }
      },
      zoom: (z) => zoom.setState(z, true),
      rendered: () => {
        try {
          performance.clearMarks(renderedMark);
          performance.mark(renderedMark, { detail: { boardId } });
        } catch {
          // A browser without marks' details has no harness to read them.
        }
      },
      view: (view) => onViewRef.current?.(view),
    });
    if (initial !== undefined) {
      c.restore(initial);
    }
    return c;
  });
  const actions = useCanvasActions(projectId, boardId, workspaceId, controller);
  // The targets of the board's jumpers that the document tells nothing of,
  // as nodes of boards that the page has not opened, which the canvas reads
  // by themselves as their jumpers' cards are drawn (targets.ts), and again
  // once the page is shown again; none in a run of the benchmark, which
  // measures the canvas alone. Each answer writes the cards again, and
  // shows the page anew, whose link to the target of the jumper selected
  // follows it.
  const queryClient = useQueryClient();
  const [, setTargetsRead] = useState(0);
  const [targetsChanged] = useState(() => () => {
    controller.relabel();
    setTargetsRead((n) => n + 1);
  });
  const [targetReads] = useState(
    () =>
      new TargetReads(
        (id) =>
          queryClient
            .fetchQuery({
              queryKey: ["projects", projectId, "nodes", id, "target"],
              queryFn: async ({ signal }) => {
                try {
                  return (
                    await api.call(
                      "getNode",
                      { path: { project_id: projectId, node_id: id } },
                      { signal },
                    )
                  ).body;
                } catch (error) {
                  // A node that the project does not have, or has in its
                  // trash, is what a broken jumper targets, and no failure.
                  if (notFoundBy(error)) {
                    return null;
                  }
                  throw error;
                }
              },
              staleTime: 0,
              gcTime: 0,
              retry: false,
            })
            .then((node) => node ?? undefined),
        targetsChanged,
      ),
  );
  const benching = options.bench !== undefined;
  const targets = useMemo<TargetLookup>(
    () => ({
      boardRead: (id) => doc.hasRead(boardPart(id)),
      treeRead: () => doc.hasRead("tree"),
      read: (id) => (benching ? undefined : targetReads.read(id)),
      boardWasRead: targetsChanged,
    }),
    [doc, targetReads, benching, targetsChanged],
  );
  useEffect(() => {
    targetReads.start();
    const shown = () => {
      if (document.visibilityState === "visible") {
        targetReads.readAgain();
      }
    };
    document.addEventListener("visibilitychange", shown);
    return () => {
      document.removeEventListener("visibilitychange", shown);
      targetReads.stop();
    };
  }, [targetReads]);
  const history = useHistory(projectId);
  // The undo and the redo are the toolbar's, rather than the project's
  // layout's above the page, while it shows them, and so are their
  // commands (plan note N72).
  useOwnHistory(editable);
  useHistoryCommands(editable ? history : undefined, "page");
  // The tags of the board's nodes, which the tag filter offers, as the
  // document has them, with the one that it dims by, though no node has it
  // any longer, as once another session took it away, until it is cleared
  // (tagsOfScene, which the board's content sets as it changes).
  const [tags, setTags] = useState<string[]>([]);
  const [sceneTags] = useState(() => tagsOfScene(controller.scene));
  // The tag that the filter dims by, in the spelling that the list has of
  // it, whatever its case, as once most of its nodes spell it otherwise,
  // rather than offered twice, once as the filter has it.
  const filterShown = useMemo(() => {
    if (tagFilter === "") {
      return "";
    }
    const key = tagKey(tagFilter);
    return tags.find((tag) => tagKey(tag) === key) ?? tagFilter;
  }, [tags, tagFilter]);
  const editingNode = useDocumentState(doc, (state) =>
    editing === undefined ? undefined : state.view.nodes.get(editing),
  );
  // The editor of the text that is open, which holds what the user typed.
  const editor = useRef<TextEditorHandle>(null);
  const editingNow = useRef(editing);
  useLayoutEffect(() => {
    editingNow.current = editing;
  });
  // Opens the editor of the text of the element of id, in place of the one
  // open, unless that one holds text that is not saved, which it keeps,
  // with the focus, saying to save or cancel it first, rather than lose it,
  // or text that it is saving, which gives way once its save applies
  // (TextEditorHandle's giveWay, and closeEditor). The editor of id that is
  // open already takes the focus back, as from the canvas, where the user
  // clicked, rather than leave it there.
  const openText = useCallback((id: string) => {
    const open = editingNow.current;
    if (open === id && editor.current !== null) {
      editor.current.focus();
      return;
    }
    if (
      open !== undefined &&
      open !== id &&
      editor.current?.giveWay(id) === false
    ) {
      return;
    }
    setEditing(id);
  }, []);
  // The element whose editor is open as the document had it last, which the
  // editor keeps once the document no longer has it, as once it was deleted,
  // which takes it out of the document, until the node is back, as once an
  // undo restored it, or the editor closes.
  const edited = useRef<BoardNode>(undefined);
  if (editingNode !== undefined) {
    edited.current = editingNode;
  }
  const editedNode =
    editingNode ??
    (editing !== undefined && edited.current?.id === editing
      ? edited.current
      : undefined);
  // An element whose editor is open that is no longer on the board, as once
  // it was deleted, here or by another session, closes the editor, unless
  // it holds text that is not saved, which it keeps, saying why it cannot be
  // saved (TextEditor's gone), as it does once the board can no longer be
  // changed. One that the canvas never had closes it at once, and the focus
  // that the editor had, which falls to the page's body as the editor goes,
  // goes back to the canvas, as the editor's own close gives it, rather than
  // leave the canvas's keys to the user's Tab.
  const editingGone =
    editingNode === undefined ||
    editingNode.deleted_at !== undefined ||
    editingNode.board_id !== boardId;
  const editingLost = editing !== undefined && editedNode === undefined;
  useEffect(() => {
    if (editingLost) {
      setEditing(undefined);
      if (document.activeElement === document.body) {
        hostRef.current?.focus({ preventScroll: true });
      }
    }
  }, [editingLost]);
  // The inspector of a board that can no longer be changed goes, rather than
  // come back, and take the focus, once the board can be changed again;
  // what a dropped connection offers ends with the board's edits, as the
  // canvas tells.
  useEffect(() => {
    if (!editable) {
      setInspecting(false);
    }
  }, [editable]);
  // What a dropped connection offers ends as the user chooses a type of
  // node, or cancels: the canvas draws its line no longer, and tells the
  // page that it offers nothing.
  const endOffer = useCallback(() => controller.endOffer(), [controller]);
  // The editor closes, or gives way to the editor of next, whose own text
  // takes the focus as it opens. The canvas takes the focus that the editor
  // had, or that fell to the page's body, as the Save button's while it was
  // disabled: not the focus that the user took elsewhere meanwhile, as to
  // the inspector's title while the save was under way, whose keys, as
  // Backspace, would else go to the canvas, and delete the selection.
  const closeEditor = useCallback((next?: string) => {
    setEditing(next);
    const active = document.activeElement;
    if (
      next === undefined &&
      (active === null ||
        active === document.body ||
        overRef.current?.contains(active) === true)
    ) {
      hostRef.current?.focus({ preventScroll: true });
    }
  }, []);
  // What the canvas changes of the board, as its drags and keys ask, on a
  // board that the user may change.
  const edits = useRef<{ actions: CanvasActions }>(undefined);
  useLayoutEffect(() => {
    edits.current = { actions };
  });
  useEffect(() => {
    controller.edits = editable
      ? {
          move: (places, expected) => {
            const current = edits.current;
            if (current === undefined) {
              return;
            }
            const count = places.size;
            void current.actions.move(places, expected).then(({ answer }) => {
              // The undo of the move alone, once it is the step that undo
              // would send: not of a batch that added none, as one answered
              // with already_applied, whose undo would be the step before.
              if (
                count >= 2 &&
                answer?.status === "applied" &&
                doc.undoesNext(answer.seq)
              ) {
                setMoved({
                  // Another key than any move's before, though of the same
                  // millisecond, so that its offer starts anew.
                  key: ++moves.current,
                  count,
                  version: doc.store.getState().historyVersion,
                  until: Date.now() + undoMoveTime,
                });
              }
            });
          },
          resize: (id, size, rev) =>
            void edits.current?.actions.resize(id, size, rev),
          remove: (ids) => void edits.current?.actions.remove(ids),
          open: (id) => {
            const node = doc.store.getState().view.nodes.get(id);
            if (node?.type === "element") {
              openText(id);
            } else {
              inspect();
            }
          },
          connect: (from, to) => void edits.current?.actions.connect(from, to),
          reconnect: (id, ends, rev) =>
            void edits.current?.actions.reconnect(id, ends, rev),
          removeConnection: (id) =>
            void edits.current?.actions.removeConnection(id),
          openConnection: inspect,
        }
      : undefined;
    return () => {
      controller.edits = undefined;
    };
  }, [controller, doc, editable, openText, inspect]);
  useEffect(() => {
    controller.snap = snap;
  }, [controller, snap]);
  // The tag filter dims the nodes without its tag, whatever its case, and
  // changes nothing of the board (FR-EDIT-06 AC1): its tag folded once, and
  // each node's tags once, rather than in each frame (hasTagOf).
  useEffect(() => {
    if (tagFilter === "") {
      controller.setDimmed(undefined);
      return;
    }
    const has = hasTagOf(tagFilter);
    controller.setDimmed((node) => !has(node));
  }, [controller, tagFilter]);
  // A link to a node opens the canvas on it, and, where the inspector docks
  // beside the canvas, on a board that the user may change, the inspector
  // of it too, once the canvas has found it, as the link is followed,
  // rather than as the window turns, or as the user's role or the
  // project's archive changes, long after, which would take the user's
  // selection and view away; a node that the board does not have opens
  // neither, as without the link. Whether the inspector docks, and whether
  // the board may be changed, are as they are once the canvas has found the
  // node, as once its content has been read, rather than as the link was
  // followed, before, as once the project was archived meanwhile, whose
  // inspector would else come back once the archive is lifted.
  const opensInspector = useRef(false);
  useLayoutEffect(() => {
    opensInspector.current = editable && docked;
  });
  const openNode = useCallback(
    (id: string) =>
      controller.focus(id, () => {
        if (opensInspector.current) {
          setInspecting(true);
        }
      }),
    [controller],
  );
  useEffect(() => {
    if (focus !== undefined) {
      openNode(focus);
    }
  }, [openNode, focus]);
  // A link on the page to a node of this board that the address names
  // already, as a jumper's to its target on its own board, followed a
  // second time, or on a board that a link to that target opened, changes
  // nothing of the address, by which the canvas opens a node: the canvas
  // opens the node again itself, selected and shown whole, as the click
  // asks (FR-EDIT-05). A click with a key that asks the browser to open the
  // link elsewhere is the browser's.
  const follow = useCallback<FollowLink>(
    (event, board, node) => {
      if (
        board === boardId &&
        node === focus &&
        !(event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
      ) {
        openNode(node);
      }
    },
    [boardId, focus, openNode],
  );

  // The canvas takes the window's height below where it starts, as a
  // tablet's screen is the board's, as the window's size changes, and as
  // what is above it changes its height, as the toolbar that wraps once it
  // says how many nodes are selected, or an alert at the page's top: the
  // page, taller than the window once the canvas takes the rest of it,
  // changes its height with it. The window's height is its root's, the
  // height of the window with a phone's browser's bars shown, which does not
  // change as they collapse and come back while the page scrolls, as its
  // innerHeight does, which would grow the canvas and shrink it again, and
  // move the board's settings under it, as the user scrolls to them.
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (host === null) {
      return;
    }
    const measure = () => {
      const top = host.getBoundingClientRect().top + window.scrollY;
      const viewport = document.documentElement.clientHeight;
      setHeight(Math.max(320, viewport - top - 16));
    };
    measure();
    window.addEventListener("resize", measure);
    const page =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(measure)
        : undefined;
    page?.observe(document.body);
    return () => {
      window.removeEventListener("resize", measure);
      page?.disconnect();
    };
  }, []);

  // The elements, their size, the theme, and the browser's events.
  useLayoutEffect(() => {
    const host = hostRef.current;
    const band = bandRef.current;
    if (host === null || band === null) {
      return;
    }
    controller.attach({ host, band });
    const resize = () => {
      controller.setSize(sizeOf(host), window.devicePixelRatio || 1);
    };
    resize();
    const observer =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(resize)
        : undefined;
    observer?.observe(host);
    // The screen's pixels to a CSS pixel, which change without the canvas's
    // size, as the window moves to a screen of another density: a query of
    // the density as it is tells once it is another, and is made anew for
    // the one after.
    let density: MediaQueryList | undefined;
    const densityChanged = () => {
      resize();
      watchDensity();
    };
    const watchDensity = () => {
      density?.removeEventListener("change", densityChanged);
      density =
        typeof window.matchMedia === "function"
          ? window.matchMedia(
              `(resolution: ${window.devicePixelRatio || 1}dppx)`,
            )
          : undefined;
      density?.addEventListener("change", densityChanged);
    };
    watchDensity();
    // A pointer that the canvas follows is the host's, whose capture it
    // takes as the pointer goes down, and which the controller gives back
    // as soon as the gestures no longer follow it (GestureHost's release).
    // Of a pointer that it holds, the host hears that it left only once the
    // browser has lost it, as a pen that leaves the range where the screen
    // sees it hover (Gestures' leave).
    const pointer =
      (type: "down" | "move" | "leave" | "up" | "cancel") =>
      (e: PointerEvent) => {
        if (
          type === "down" &&
          e.target !== host &&
          !surfaceRef.current?.contains(e.target as Node)
        ) {
          return;
        }
        // A pointer that the canvas does not follow, as a mouse that moves
        // over it with no button down, reads nothing of the page's layout.
        if (type !== "down" && !controller.gestures.tracks(e.pointerId)) {
          return;
        }
        const input = pointerInput(e, host.getBoundingClientRect());
        if (type !== "down") {
          controller.gestures[type](input);
          return;
        }
        if (controller.down(input)) {
          host.focus({ preventScroll: true });
          host.setPointerCapture?.(e.pointerId);
          e.preventDefault();
        }
      };
    const down = pointer("down");
    const move = pointer("move");
    const leave = pointer("leave");
    const up = pointer("up");
    const cancel = pointer("cancel");
    // The host's own capture, once it lost it, rather than that of an
    // element in it, as the minimap's, or of a finger on the renderer's
    // canvas, which the browser gives it and the host takes, whose loss
    // bubbles up to it.
    const lost = (e: PointerEvent) => {
      if (e.target === host) {
        cancel(e);
      }
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = host.getBoundingClientRect();
      controller.gestures.wheel({
        x: e.clientX - r.left,
        y: e.clientY - r.top,
        dx: e.deltaX,
        dy: e.deltaY,
        deltaMode: e.deltaMode,
        zoom: e.ctrlKey || e.metaKey,
        shift: e.shiftKey,
      });
    };
    // Safari's pinch of a trackpad, which comes as a gesture's scale.
    let scale = 1;
    const gesture = (e: Event) => {
      e.preventDefault();
      const g = e as Event & {
        scale?: number;
        clientX?: number;
        clientY?: number;
      };
      const r = host.getBoundingClientRect();
      if (e.type === "gesturestart") {
        scale = 1;
      }
      const next = g.scale ?? 1;
      controller.gestures.scale(
        {
          x: (g.clientX ?? r.left + r.width / 2) - r.left,
          y: (g.clientY ?? r.top + r.height / 2) - r.top,
        },
        scale,
        next,
      );
      scale = next;
    };
    // A touch on the canvas, or on its minimap, is the canvas's alone:
    // cancelling it keeps the browser from taking it for a scroll or a
    // fling of its own, which a tap right after, as of the toolbar's
    // button, would only stop, rather than press, and from zooming the page
    // with a double tap (NFR-TOUCH-08). A button over the canvas, as the
    // one that reads its content again, keeps its taps. The browser's menu
    // does not open over the canvas either, where a right click opens the
    // canvas's own (menuOf): a Ctrl+click on a Mac, which the canvas takes
    // as a click that adds a node to the selection, or takes it out, would
    // else open it, and the menu would take the click's release.
    const own = (e: Event) => {
      if (!(e.target instanceof Element && e.target.closest("button"))) {
        e.preventDefault();
      }
    };
    // A right click on the canvas, or on a handle over it, opens the
    // canvas's own menu, of what is under it, as a long press does
    // (FR-EDIT-09, plan note N70), in place of the browser's: a mouse's, or
    // a pen's barrel button's, by the event's button, as neither a
    // Ctrl+click on a Mac, which is a click, nor the menu key, which the
    // canvas's keys take (keyDown), nor a finger's long press, which the
    // browser may ask its menu for after the gestures have opened the
    // canvas's, has the right button; nor one while the canvas follows a
    // pointer, as in the middle of a drag. One on the minimap, or on a
    // button over the canvas, is not the board's.
    const menuOf = (e: Event) => {
      own(e);
      if (!(e instanceof MouseEvent) || e.button !== 2) {
        return;
      }
      const type = (e as PointerEvent).pointerType;
      if (type === "touch" || controller.gestures.following()) {
        return;
      }
      const onHandle =
        e.target instanceof Element &&
        e.target.closest("[data-canvas]") !== null;
      if (
        e.target !== host &&
        !surfaceRef.current?.contains(e.target as Node) &&
        !onHandle
      ) {
        return;
      }
      controller.gestures.menu(
        pointerInput(e as PointerEvent, host.getBoundingClientRect()),
      );
    };
    const blur = () => {
      controller.gestures.cancelAll();
      controller.gestures.setSpace(false);
    };
    // What the canvas places over its nodes, outside its element, that says
    // it is the canvas's own (data-canvas), as the resize handle and the
    // link handles, takes the wheel, the pinch, and the browser's menu as
    // the canvas does, rather than let them through to the page, which
    // would scroll it, zoom it, or open its menu over the canvas, and has
    // its touches cancelled as the canvas's are, so that the browser takes
    // a finger's drag of a handle for no fling of its own, which would take
    // the tap right after it, as of what a dropped connection offers, for
    // the fling's end rather than a press (NFR-TOUCH-08); the rest keeps
    // its own, as the editor of a text, whose wheel scrolls it, and whose
    // menu is its text's.
    const over = overRef.current;
    const ofCanvas = (e: Event) =>
      e.target instanceof Element && e.target.closest("[data-canvas]") !== null;
    const overWheel = (e: WheelEvent) => {
      if (ofCanvas(e)) {
        wheel(e);
      }
    };
    const overGesture = (e: Event) => {
      if (ofCanvas(e)) {
        gesture(e);
      }
    };
    const overMenu = (e: Event) => {
      if (ofCanvas(e)) {
        menuOf(e);
      }
    };
    const overTouch = (e: Event) => {
      if (ofCanvas(e)) {
        e.preventDefault();
      }
    };
    over?.addEventListener("touchstart", overTouch, { passive: false });
    over?.addEventListener("touchmove", overTouch, { passive: false });
    over?.addEventListener("wheel", overWheel, { passive: false });
    over?.addEventListener("contextmenu", overMenu);
    for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
      over?.addEventListener(type, overGesture);
    }
    host.addEventListener("touchstart", own, { passive: false });
    host.addEventListener("touchmove", own, { passive: false });
    host.addEventListener("contextmenu", menuOf);
    host.addEventListener("pointerdown", down);
    host.addEventListener("pointermove", move);
    host.addEventListener("pointerleave", leave);
    host.addEventListener("pointerup", up);
    host.addEventListener("pointercancel", cancel);
    host.addEventListener("lostpointercapture", lost);
    host.addEventListener("wheel", wheel, { passive: false });
    for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
      host.addEventListener(type, gesture);
    }
    window.addEventListener("blur", blur);
    return () => {
      controller.stop();
      controller.attach(undefined);
      observer?.disconnect();
      density?.removeEventListener("change", densityChanged);
      host.removeEventListener("touchstart", own);
      host.removeEventListener("touchmove", own);
      host.removeEventListener("contextmenu", menuOf);
      host.removeEventListener("pointerdown", down);
      host.removeEventListener("pointermove", move);
      host.removeEventListener("pointerleave", leave);
      host.removeEventListener("pointerup", up);
      host.removeEventListener("pointercancel", cancel);
      host.removeEventListener("lostpointercapture", lost);
      host.removeEventListener("wheel", wheel);
      for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
        host.removeEventListener(type, gesture);
        over?.removeEventListener(type, overGesture);
      }
      over?.removeEventListener("touchstart", overTouch);
      over?.removeEventListener("touchmove", overTouch);
      over?.removeEventListener("wheel", overWheel);
      over?.removeEventListener("contextmenu", overMenu);
      window.removeEventListener("blur", blur);
    };
  }, [controller]);

  // The theme, of the page's colours, or of the system's forced colours,
  // which the canvas reads again as the system's theme changes them. A
  // change from one contrast theme to another of the same lightness, as
  // from Windows's Aquatic to its Night sky, changes neither query, and the
  // page hears of it by no event, while the browser draws the page's own
  // elements in the new colours, so in forced colours the canvas reads them
  // again too as its window gets the focus back, as from the system's
  // settings, where the theme changes; it draws again only where they did
  // (setTheme).
  // biome-ignore lint/correctness/useExhaustiveDependencies: dark reads the page's colours again, which it changes.
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (host === null) {
      return;
    }
    const read = () => controller.setTheme(readTheme(host, forcedColors));
    read();
    if (!forcedColors) {
      return;
    }
    window.addEventListener("focus", read);
    return () => window.removeEventListener("focus", read);
  }, [controller, dark, forcedColors]);

  // The canvas's size once its height is set, at once, rather than once the
  // observer tells of it, after the next frame: that frame places the canvas
  // on the board's content where it has been read already, as of a board
  // that the page showed before, which it fits to the canvas's height, not
  // to the height of none that the canvas had before, at 10%.
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (host !== null && height !== undefined) {
      controller.setSize(sizeOf(host), window.devicePixelRatio || 1);
    }
  }, [controller, height]);

  // The renderer, on a canvas element of its own in the surface, as a
  // renderer that ends may leave its canvas's context lost. PixiJS, with
  // the renderer's module, is fetched only as the canvas starts it, in a
  // chunk of its own, so that the board's page, its toolbar, and the read
  // of its content, which the renderer draws once it has started, do not
  // wait for the browser to fetch and parse it first. A chunk that cannot
  // be fetched, as once the server has a version of the app other than the
  // page's, is the page's error, as a chunk of the page's own is, rather
  // than a browser that cannot draw. A renderer that the page asks for
  // anew, as by its address, starts in place of the one before, on the
  // canvas as it is, and the start of the one before, where it is still
  // under way, tries no renderer after the one that it is starting, which
  // ends once it has started.
  useEffect(() => {
    const surface = surfaceRef.current;
    const host = hostRef.current;
    if (surface === null || host === null) {
      return;
    }
    let ended = false;
    let started: Engine | undefined;
    // What tells the start that it is no longer wanted, so that it tries no
    // renderer, and adds no canvas, beside the one that starts in its place.
    const abort = new AbortController();
    setEngine("starting");
    const fontFamily = getComputedStyle(host).fontFamily || "sans-serif";
    import("./engine").then(
      ({ createEngine }) =>
        createEngine(surface, {
          preference: options.renderer,
          forced: options.renderer !== undefined,
          fontFamily,
          signal: abort.signal,
        }).then(
          (e) => {
            if (ended) {
              e.destroy();
              return;
            }
            // The renderer is sized in the frame that draws it, where what
            // it throws, as once its GPU's device is lost, is tried again
            // as what it throws as it draws is (CanvasController's sized),
            // rather than here, where the canvas would say that the browser
            // has no renderer, of one that started.
            controller.setEngine(e);
            started = e;
            setEngine("running");
          },
          () => {
            if (!ended) {
              setEngine("unsupported");
            }
          },
        ),
      (error: unknown) => {
        if (!ended) {
          setEngineFailure({ error });
        }
      },
    );
    return () => {
      ended = true;
      abort.abort();
      controller.setEngine(undefined);
      started?.destroy();
    };
  }, [controller, options.renderer]);

  // The minimap, while it is shown. A press that went down on the minimap
  // before, which React tells nothing more of once its canvas is gone, as
  // the minimap is hidden under it, is none of the one shown after, which
  // a drag that went down elsewhere and crosses it does not move.
  useEffect(() => {
    const canvas = minimapRef.current;
    minimapPress.current = undefined;
    controller.setMinimap(
      showMinimap && canvas !== null ? new Minimap(canvas) : undefined,
    );
  }, [controller, showMinimap]);

  // The board's content, from the project's document, as it changes.
  useEffect(() => {
    let names: LabelSources | undefined;
    const sync = () => {
      const view = doc.store.getState().view;
      // Whether a name that cards show changed (labels.ts).
      const changed = names !== undefined && namesChanged(names, view);
      names = view;
      controller.sync(view, changed);
      setEmpty(controller.scene.nodes.size === 0);
      setTags(sceneTags());
    };
    // The keys that labels names, each a literal there, are the catalog's,
    // as extraction reads them. What labels read of the document is made
    // once for each state of it that they read, rather than for each card,
    // with the targets of jumpers that lookup tells of.
    const labelsBy = (lookup: TargetLookup) => {
      let context: { view: LabelSources; c: LabelContext } | undefined;
      return (item: { node: BoardNode }) => {
        const view = doc.store.getState().view;
        if (context?.view !== view) {
          context = { view, c: labelContext(view, t, lookup) };
        }
        return labelsOf(item.node, context.c);
      };
    };
    controller.labels = labelsBy(targets);
    // A jumper that is known to be broken, by what the document tells, or
    // by a read of its target that was made, rather than by one that this
    // would ask for, of every jumper of the board at once.
    const known: TargetLookup = {
      ...targets,
      read: (id) => targetReads.peek(id),
    };
    controller.broken = (node) =>
      targetOf(node, doc.store.getState().view, known).state === "gone";
    // What the canvas's tree says of a node is what its card shows, of what
    // is known of a jumper's target alike, rather than a read of the target
    // of every jumper of the board, which a tree of all of its nodes would
    // else ask for at once.
    controller.described = labelsBy(known);
    // The messages of t, which is another as the locale changes.
    controller.relabel();
    sync();
    return doc.store.subscribe(sync);
  }, [controller, doc, t, sceneTags, targets, targetReads]);

  // The board's nodes and connections, read once, first by the read that
  // the page's loader started (read), whose failure the canvas says, rather
  // than ask the server again as it mounts, after a read that failed before
  // it, which the document takes the part out for, and then the names that
  // its cards show (namesRead).
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt reads the board's content again, as the user asks for it.
  useEffect(() => {
    let ended = false;
    setContent("loading");
    const first =
      attempt === 0 && loaderRead.doc === doc ? loaderRead.read : undefined;
    (first ?? doc.load(boardPart(boardId))).then(
      () => {
        if (ended) {
          return;
        }
        setContent("ready");
        controller.setReady();
        setNamesRead((n) => n + 1);
      },
      () => {
        if (!ended) {
          setContent("failed");
        }
      },
    );
    return () => {
      ended = true;
    };
  }, [controller, doc, boardId, attempt]);

  // The names that cards show, of the project's speakers and of the
  // variables that their texts show, which the board's first frame does not
  // wait for, and which are read anew where the document had read them
  // already, as the parts that a page shows are (loadPart). A read of them
  // that fails, which leaves the cards without them, is said in the
  // toolbar, with Try again, rather than leave the user to wonder why while
  // they stay on the board, as the document reads a part that it does not
  // hold only as it is asked for it.
  useEffect(() => {
    if (namesRead === 0) {
      return;
    }
    let ended = false;
    setNamesFailed(false);
    Promise.all(nameParts.map((part) => loadPart(doc, part))).then(
      () => {},
      () => {
        if (!ended) {
          setNamesFailed(true);
        }
      },
    );
    return () => {
      ended = true;
    };
  }, [doc, namesRead]);

  useEffect(() => {
    controller.reducedMotion = reducedMotion;
  }, [controller, reducedMotion]);

  useEffect(() => {
    controller.setMode(mode);
  }, [controller, mode]);

  // The offer of the move of key alone, rather than that of a move after
  // it, which the page may have set before the offer before it ended.
  const clearMoved = useCallback(
    (key: number) => setMoved((m) => (m?.key === key ? undefined : m)),
    [],
  );
  const hostElement = useCallback(() => hostRef.current, []);
  // A press of a node's resize handle, or of a link handle, which is
  // outside the canvas's element, is the canvas's own, at the handle's point
  // of the canvas, by the same rules as a press on it (Gestures' down), and
  // the canvas's element captures its pointer, as it does a press's on it,
  // so that its moves, its release, and its loss come to the canvas, which
  // follows them as those of every pointer. A press on a port's link handle
  // where it covers the resize handle of its node, nearer that handle's
  // middle than the port, is the resize handle's (resizeTakes).
  const pressHandle = useCallback(
    (e: ReactPointerEvent<HTMLElement>, handle: Handle) => {
      const host = hostRef.current;
      if (host === null) {
        return;
      }
      const input = pointerInput(e.nativeEvent, host.getBoundingClientRect());
      const pressed =
        typeof handle !== "string" &&
        handle.kind === "new" &&
        resizeTakes(controller, handle, input)
          ? handle.node
          : handle;
      if (controller.down(input, pressed)) {
        host.focus({ preventScroll: true });
        host.setPointerCapture?.(e.pointerId);
        e.preventDefault();
      }
    },
    [controller],
  );
  const toggleSnap = () => {
    keepSnap(!snap);
    setSnap(!snap);
  };
  // The one selected node, as the document has it, whose ports have link
  // handles, and, of a jumper, its target, which its link opens.
  const only = selected === 1 ? [...selection][0] : undefined;
  const onlyNode = useDocumentState(doc, (state) =>
    only === undefined ? undefined : state.view.nodes.get(only),
  );
  const onlyTarget = useDocumentState(
    doc,
    useShallow((state) =>
      onlyNode?.type === "jumper"
        ? targetOf(onlyNode, state.view, targets)
        : undefined,
    ),
  );
  const onlyLink = onlyTarget === undefined ? undefined : linkOf(onlyTarget);
  // The name of its type, which the toolbar says with its title as it says
  // that it is selected: none, of a type that the page does not know.
  const onlyType = onlyNode === undefined ? "" : typeOf(onlyNode, t);
  // The nodes at the ends of the connection selected, which the toolbar
  // names as it says that the connection is selected (NFR-A11Y-03).
  const chosen = useDocumentState(doc, (state) =>
    connection === undefined
      ? undefined
      : state.view.connections.get(connection),
  );
  const chosenFrom = useDocumentState(doc, (state) =>
    chosen === undefined
      ? undefined
      : state.view.nodes.get(chosen.from_node_id),
  );
  const chosenTo = useDocumentState(doc, (state) =>
    chosen === undefined ? undefined : state.view.nodes.get(chosen.to_node_id),
  );

  // The context menu is of what is selected: one of nodes goes once none
  // is selected, and one of a connection once that connection is selected
  // no longer, as once another session deleted what it was of.
  const menuStale =
    menu !== undefined &&
    ((menu.target.kind === "node" && selected === 0) ||
      (menu.target.kind === "connection" &&
        connection !== menu.target.connection));
  useEffect(() => {
    if (menuStale) {
      setMenu(undefined);
    }
  }, [menuStale]);
  const closeMenu = useCallback(() => setMenu(undefined), []);
  const navigate = useNavigate();
  // Opens the target of the jumper selected, as its link does (TargetLink).
  const goToTarget = () => {
    if (onlyLink === undefined) {
      return;
    }
    follow(
      { ctrlKey: false, metaKey: false, shiftKey: false, altKey: false },
      onlyLink.boardId,
      onlyLink.nodeId,
    );
    void navigate({
      to: "/projects/$projectId/boards/$boardId",
      params: { projectId, boardId: onlyLink.boardId },
      search: { node: onlyLink.nodeId },
    });
  };

  // A paste: the report of what of the clipboard the board's project lacks,
  // under way, while which Paste is unavailable (useCanvasActions' busy),
  // and then the dialog that lists it, of the clipboard that it was asked
  // of, and of the point of the board where it was asked to go, with what it
  // gives the focus back to as it closes (FR-EDIT-07 AC1): the toolbar's
  // Paste, of a paste that it asked for, which a tap does not give the
  // focus, and else what had the focus as the dialog opened, the canvas or
  // the item of its tree.
  const [pasting, setPasting] = useState<{
    clipboard: Clipboard;
    at: Point;
    report: ClipboardReport;
    opener: Opener;
    asked: ReadonlySet<string>;
  }>();
  const pasteButton = useRef<HTMLButtonElement>(null);
  const editableNow = useRef(editable);
  useLayoutEffect(() => {
    editableNow.current = editable;
  });
  // Paste is offered of what the page's clipboard holds of this workspace,
  // on a board that may be changed, once the canvas is placed on its nodes,
  // as a new node is (Add), and while no copy, paste, or duplicate of the
  // canvas is under way (busy).
  const clipboardReady = content === "ready" && copied !== undefined;
  const pastable = editable && clipboardReady && !actions.busy;
  // Pastes what the page's clipboard holds, its nodes' middle at at of the
  // board, or at the canvas's centre, where at is not given: at once where
  // nothing that they name is missing from the board's project, and else
  // once the dialog that lists what is has been answered, which gives the
  // focus back to opener, where it is given; and selects the pasted nodes
  // unless the user selected others meanwhile (asked), as while the report
  // was under way. While a copy, a paste, or a duplicate of the canvas is
  // under way, a paste asked before the canvas renders it unavailable
  // pastes nothing (useCanvasActions' oneAtATime).
  const pasteAt = async (at?: Point, opener?: Opener) => {
    const clipboard = copied?.clipboard;
    if (!pastable || clipboard === undefined) {
      return;
    }
    const point = at ?? centerOf(controller.viewport(), controller.size());
    const asked = controller.selection();
    const report = await actions.resolve(clipboard);
    if (report === undefined || !editableNow.current) {
      return;
    }
    if (lacksAny(report)) {
      const active = document.activeElement;
      setPasting({
        clipboard,
        at: point,
        report,
        opener: opener ?? (active instanceof HTMLElement ? active : null),
        asked,
      });
    } else {
      void actions.paste(clipboard, point, undefined, asked);
    }
  };
  // What the clipboard's keys ask, on the canvas, or in its tree, which has
  // selected what they act on first (clipboardKey): not in the middle of a
  // drag, as the menu's keys do nothing then, whose nodes they would change;
  // Cut on a board that may be changed alone; and Copy, Cut, and Duplicate
  // of at most as many nodes as a clipboard holds.
  const clipboardAsked = (asked: ClipboardAsk) => {
    if (controller.gestures.following()) {
      return;
    }
    if (asked === "paste") {
      void pasteAt();
      return;
    }
    const ids = [...controller.selection()];
    if (
      ids.length === 0 ||
      ids.length > maxEdited ||
      (asked === "cut" && !editable)
    ) {
      return;
    }
    void (asked === "cut" ? actions.cut(ids) : actions.copy(ids));
  };
  // The tree's, the same function each render, as the tree renders again
  // for what it shows alone.
  const clipboardNow = useRef(clipboardAsked);
  useLayoutEffect(() => {
    clipboardNow.current = clipboardAsked;
  });
  const treeClipboard = useCallback(
    (asked: ClipboardAsk) => clipboardNow.current(asked),
    [],
  );

  // The canvas's commands (FR-EDIT-10, plan note N72), which its keys and
  // the palette run alike, each available where the toolbar's control of
  // it is: the controller's own, as the zoom and Delete, and the toolbar's,
  // as Add, Duplicate, and the inspector; C opens the inspector at its
  // Connect to, of the one element or branch selected. None of those that
  // change the board acts in the middle of a drag, as the menu's keys do
  // not, which the canvas renders nothing anew for: each says whether it
  // can run as it is asked (a getter), of the drag under way then, rather
  // than of the one under way as the canvas last rendered.
  const registry = useRegistry();
  const [connectAsked, setConnectAsked] = useState(0);
  useEffect(() => {
    if (connectAsked > 0) {
      document.querySelector<HTMLElement>("[data-connect-to]")?.focus();
    }
  }, [connectAsked]);
  const still = () => !controller.gestures.following();
  const ofController = (id: Parameters<typeof controller.command>[0]) => ({
    run: () => controller.command(id),
  });
  const create = (kind: "element" | "branch" | "jumper") => ({
    get enabled() {
      return content === "ready" && still();
    },
    run: () => void actions.create(kind),
  });
  const several = selected > 0 && selected <= maxEdited;
  const canvasHandlers: Handlers = {
    "canvas.pan_left": ofController("canvas.pan_left"),
    "canvas.pan_right": ofController("canvas.pan_right"),
    "canvas.pan_up": ofController("canvas.pan_up"),
    "canvas.pan_down": ofController("canvas.pan_down"),
    "canvas.pan_far_left": ofController("canvas.pan_far_left"),
    "canvas.pan_far_right": ofController("canvas.pan_far_right"),
    "canvas.pan_far_up": ofController("canvas.pan_far_up"),
    "canvas.pan_far_down": ofController("canvas.pan_far_down"),
    "canvas.zoom_in": ofController("canvas.zoom_in"),
    "canvas.zoom_out": ofController("canvas.zoom_out"),
    "canvas.zoom_reset": ofController("canvas.zoom_reset"),
    "canvas.fit": ofController("canvas.fit"),
    "canvas.zoom_selection": {
      ...ofController("canvas.zoom_selection"),
      enabled: selected > 0,
    },
    // Escape ends a drag under way, cancelled, as a rubber band drawn with
    // nothing selected, or else clears the selection (the controller's).
    "canvas.deselect": {
      ...ofController("canvas.deselect"),
      get enabled() {
        return selected > 0 || connection !== undefined || !still();
      },
    },
    "canvas.minimap": { run: () => setShowMinimap((shown) => !shown) },
    "canvas.mode_pan": { run: () => setMode("pan") },
    "canvas.mode_select": { run: () => setMode("select") },
    "canvas.go_to_target": {
      enabled: onlyLink !== undefined,
      run: goToTarget,
    },
    ...(settingsId === undefined
      ? {}
      : {
          "canvas.settings": {
            run: () => showInPage(document, settingsId),
          },
        }),
    "canvas.copy": {
      get enabled() {
        return several && !actions.busy && still();
      },
      run: () => clipboardAsked("copy"),
    },
    "canvas.menu": {
      get enabled() {
        return still();
      },
      run: () => controller.menuOfSelection(),
    },
    ...(editable
      ? {
          "canvas.add_element": create("element"),
          "canvas.add_branch": create("branch"),
          "canvas.add_jumper": create("jumper"),
          // As the toolbar's Duplicate, unavailable while a copy, a paste,
          // or a duplicate is under way, which would drop it (oneAtATime).
          "canvas.duplicate": {
            get enabled() {
              return several && !actions.busy && still();
            },
            run: () => void actions.duplicate([...selection]),
          },
          "canvas.cut": {
            get enabled() {
              return several && !actions.busy && still();
            },
            run: () => clipboardAsked("cut"),
          },
          "canvas.paste": {
            get enabled() {
              return clipboardReady && !actions.busy && still();
            },
            run: () => clipboardAsked("paste"),
          },
          "canvas.connect": {
            get enabled() {
              return (
                (onlyNode?.type === "element" || onlyNode?.type === "branch") &&
                still()
              );
            },
            run: () => {
              setInspecting(true);
              setConnectAsked((n) => n + 1);
            },
          },
          "canvas.open": {
            ...ofController("canvas.open"),
            get enabled() {
              return (selected === 1 || connection !== undefined) && still();
            },
          },
          "canvas.delete": {
            ...ofController("canvas.delete"),
            get enabled() {
              return (selected > 0 || connection !== undefined) && still();
            },
          },
          "canvas.inspector": {
            run: () => setInspecting((open) => !open),
          },
          "canvas.snap": { run: toggleSnap },
        }
      : {}),
  };
  useCommands("page", canvasHandlers);
  const platform = usePlatform();
  const paletteKey = bindingText(
    t,
    commandOf("palette.open").keys[0],
    platform,
  );

  const keyDown = (e: ReactKeyboardEvent) => {
    if (e.target !== e.currentTarget) {
      return;
    }
    if (e.key === " " && !e.ctrlKey && !e.metaKey && !e.altKey) {
      controller.gestures.setSpace(true);
      e.preventDefault();
      return;
    }
    // The command of the canvas's, or of the page's, that the key is, on
    // the page's platform, as ? that shows the shortcuts. A key held down
    // runs it once, but for those that pan and zoom on (CommandSpec's
    // repeats), as E would else add an element with each repeat.
    const id = canvasKey(e, platform);
    if (id === undefined) {
      return;
    }
    if (e.repeat && commandOf(id).repeats !== true) {
      e.preventDefault();
      return;
    }
    // The clipboard's keys are the canvas's whatever they do, rather than
    // the browser's copy that would follow them (clipboardKey).
    const asked = clipboardKey(id);
    if (asked !== undefined) {
      clipboardAsked(asked);
      e.preventDefault();
      return;
    }
    // Shift+F10, and the menu key, open the context menu of what is
    // selected, or of the board, as a right click on it does (NFR-A11Y-04),
    // but, as a right click, not while the canvas follows a pointer, as in
    // the middle of a drag; and are never the browser's, whose menu would
    // open over the canvas.
    if (id === "canvas.menu") {
      if (!controller.gestures.following()) {
        controller.menuOfSelection();
      }
      e.preventDefault();
      return;
    }
    // The canvas's other keys, which the registry runs with what the canvas
    // and the page registered for them, or, outside the app's frame, which
    // has the registry, the canvas's own handler.
    if (
      registry !== undefined ? registry.run(id) : runHandler(canvasHandlers[id])
    ) {
      e.preventDefault();
    }
  };
  const keyUp = (e: ReactKeyboardEvent) => {
    if (e.key === " ") {
      controller.gestures.setSpace(false);
    }
  };
  // Space let go of once the focus has left the canvas goes up elsewhere,
  // so the canvas lets go of it as the focus leaves, rather than pan on
  // every drag of the select mode after.
  const blur = () => controller.gestures.setSpace(false);
  const minimapRelease = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (minimapPress.current === e.pointerId) {
      minimapPress.current = undefined;
    }
  };
  const minimapPointer = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    // The primary button's alone, as a finger's touch or a pen's tip, as on
    // the canvas: a right click, the middle button, or a pen's barrel
    // button or its eraser, does nothing to the board here either, nor a
    // move without the primary button down, which ends the minimap's press
    // whose release it did not hear of, nor one of a press that went down
    // elsewhere, as a mouse's on the page's text beside the canvas, that
    // crosses the minimap, which follows only its own.
    if (e.type === "pointerdown") {
      if (e.button !== 0) {
        return;
      }
      // A press outside the context menu closes it, as one on the board
      // does, whose focus the canvas takes: the minimap keeps its press to
      // itself, and takes no focus, so the menu, which hears of neither,
      // is closed here, rather than stay open over a board that the press
      // moves under it.
      closeMenu();
      minimapPress.current = e.pointerId;
      e.currentTarget.setPointerCapture?.(e.pointerId);
    } else if ((e.buttons & 1) === 0) {
      minimapRelease(e);
      return;
    } else if (minimapPress.current !== e.pointerId) {
      return;
    }
    e.stopPropagation();
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    controller.centerOnMinimap({ x: e.clientX - r.left, y: e.clientY - r.top });
  };

  if (engineFailure !== undefined) {
    throw engineFailure.error;
  }

  // What the canvas says over it: that it cannot draw, that the board's
  // content failed to load, that it loads, that the renderer, whose chunk
  // the canvas fetches after the page's, still starts once the content has
  // been read, rather than show a canvas as blank as of a board without
  // nodes, or that the board has none.
  const overlay =
    engine === "unsupported"
      ? { role: "alert" as const, text: t("canvas.unsupported") }
      : content === "failed"
        ? { role: "alert" as const, text: t("canvas.load_failed") }
        : content === "loading"
          ? { role: "status" as const, text: t("canvas.loading") }
          : engine === "starting"
            ? { role: "status" as const, text: t("canvas.starting") }
            : empty
              ? { role: "status" as const, text: t("canvas.empty") }
              : undefined;

  return (
    <div className="space-y-2">
      <fieldset className="flex min-w-0 flex-wrap items-center gap-2">
        <legend className="sr-only">{t("canvas.controls")}</legend>
        <fieldset className="flex min-w-0 gap-2">
          <legend className="sr-only">{t("canvas.mode")}</legend>
          <Button
            aria-pressed={mode === "pan"}
            onClick={() => setMode("pan")}
            className={pressedClass}
          >
            {t("canvas.mode_pan")}
          </Button>
          <Button
            aria-pressed={mode === "select"}
            onClick={() => setMode("select")}
            className={pressedClass}
          >
            {t("canvas.mode_select")}
          </Button>
        </fieldset>
        <div className="flex gap-2">
          <Button
            aria-label={t("canvas.zoom_out")}
            onClick={() => controller.zoomStep(-1)}
          >
            −
          </Button>
          <ZoomReset zoom={zoom} onClick={() => controller.zoomTo(1)} />
          <Button
            aria-label={t("canvas.zoom_in")}
            onClick={() => controller.zoomStep(1)}
          >
            +
          </Button>
        </div>
        <Button onClick={() => controller.fit()}>{t("canvas.fit")}</Button>
        <Button
          disabled={selected === 0}
          onClick={() => controller.zoomToSelection()}
        >
          {t("canvas.zoom_selection")}
        </Button>
        <Button
          aria-pressed={showMinimap}
          onClick={() => setShowMinimap((shown) => !shown)}
          className={pressedClass}
        >
          {t("canvas.minimap")}
        </Button>
        {settingsId !== undefined && (
          // The board's settings, under the canvas, which neither a swipe
          // nor the wheel scrolls the page to over the canvas, which keeps
          // the browser's gestures off over it (NFR-TOUCH-08), and whose
          // wheel pans the board (N66): the link brings their heading into
          // view and gives it the focus, so that a finger, a mouse, and the
          // keys reach them in one action.
          <a
            href={`#${settingsId}`}
            onClick={(e) => followInPage(e, settingsId)}
            className={buttonClass()}
          >
            {t("canvas.settings")}
          </a>
        )}
        {/* The page's clipboard (FR-EDIT-07, plan note N71): Copy, on any
        board, as a viewer may copy what they read, and Cut and Paste on a
        board that may be changed. While a copy, a paste, or a duplicate is
        under way, as a press of one of them starts, each of them, and
        Duplicate, says that it is unavailable, rather than be disabled,
        and keeps the keyboard's focus that it has, which the dialog of a
        paste gives back to Paste as it closes (LastingButton). */}
        <fieldset className="flex min-w-0 flex-wrap gap-2">
          <legend className="sr-only">{t("canvas.clipboard")}</legend>
          <LastingButton
            disabled={selected === 0 || selected > maxEdited}
            unavailable={actions.busy}
            onClick={() => void actions.copy([...selection])}
          >
            {t("canvas.copy")}
          </LastingButton>
          {editable && (
            <>
              <LastingButton
                disabled={selected === 0 || selected > maxEdited}
                unavailable={actions.busy}
                onClick={() => void actions.cut([...selection])}
              >
                {t("canvas.cut")}
              </LastingButton>
              <LastingButton
                ref={pasteButton}
                disabled={!clipboardReady}
                unavailable={actions.busy}
                onClick={() =>
                  void pasteAt(undefined, () => pasteButton.current)
                }
              >
                {t("canvas.paste")}
              </LastingButton>
            </>
          )}
        </fieldset>
        {editable && (
          <fieldset className="flex min-w-0 flex-wrap gap-2">
            <legend className="sr-only">{t("canvas.edit")}</legend>
            <Menu
              // A node is added at the centre of the canvas, beside the
              // board's nodes there, once the canvas is placed on them, as
              // once they have been read, rather than at the board's origin,
              // over a node not read yet.
              disabled={content !== "ready"}
              trigger={<Button aria-haspopup="menu">{t("canvas.add")}</Button>}
            >
              <MenuItem onSelect={() => void actions.create("element")}>
                {t("canvas.add_element")}
              </MenuItem>
              <MenuItem onSelect={() => void actions.create("branch")}>
                {t("canvas.add_branch")}
              </MenuItem>
              <MenuItem onSelect={() => void actions.create("jumper")}>
                {t("canvas.add_jumper")}
              </MenuItem>
            </Menu>
            <LastingButton
              disabled={selected === 0 || selected > maxEdited}
              unavailable={actions.busy}
              onClick={() => void actions.duplicate([...selection])}
            >
              {t("canvas.duplicate")}
            </LastingButton>
            <Button
              disabled={selected === 0 && connection === undefined}
              onClick={() =>
                void (connection === undefined
                  ? actions.remove([...selection])
                  : actions.removeConnection(connection))
              }
            >
              {t("canvas.delete")}
            </Button>
            <Button
              ref={inspectorButton}
              aria-pressed={inspecting}
              onClick={() => setInspecting((open) => !open)}
              className={pressedClass}
            >
              {t("canvas.inspector")}
            </Button>
            <Button
              aria-pressed={snap}
              onClick={toggleSnap}
              className={pressedClass}
            >
              {t("canvas.snap")}
            </Button>
            <HistoryButtons history={history} />
          </fieldset>
        )}
        <label className="flex items-center gap-2">
          <span>{t("canvas.tag_filter")}</span>
          <select
            value={filterShown}
            onChange={(e) => setTagFilter(e.currentTarget.value)}
            className="min-h-11 max-w-48 rounded-md border border-border bg-canvas px-3 text-fg"
          >
            <option value="">{t("canvas.tag_filter_none")}</option>
            {filterShown !== "" && !tags.includes(filterShown) && (
              <option value={filterShown}>{filterShown}</option>
            )}
            {tags.map((tag) => (
              <option key={tag} value={tag}>
                {tag}
              </option>
            ))}
          </select>
        </label>
        {/* What is selected, which the page says as it changes
        (NFR-A11Y-03), whole each time: the one node selected, and the
        nodes at the ends of the connection selected, by name, and that
        nothing is selected, in words that the page does not show, in place
        of those that it does. The one node selected that the document
        has not yet, as one that the page creates, and selects before its
        batch shows it, is said once, by its name, as the document has it,
        rather than as one node first and by name right after (aria-busy);
        and a new name of what is selected is said as it is given, as what
        is selected is called so from then on. A live region is read as
        its content changes, which the words of a selection that are those
        of the one before it, as of another node of the same name, or of as
        many other nodes, would not: what says the selection is put there
        anew for each change of it (selections), and nothing is there
        before the first, so that a board that opens says nothing of a
        selection that did not change. A node of a type that the page does
        not know, which has no name of its type to say, is said as the page
        shows it, as one node. */}
        <p
          className="text-sm text-fg-muted"
          aria-live="polite"
          aria-atomic="true"
          aria-busy={
            selected === 1 && onlyNode === undefined ? true : undefined
          }
        >
          <span key={selections}>
            {selected === 1 && onlyNode !== undefined && onlyType !== "" ? (
              <>
                <span aria-hidden="true">
                  {t("canvas.selected", { count: selected })}
                </span>
                <span className="sr-only">
                  {t("canvas.selected_node", {
                    title: titleOf(onlyNode, t),
                    type: onlyType,
                  })}
                </span>
              </>
            ) : selected > 0 ? (
              t("canvas.selected", { count: selected })
            ) : connection === undefined ? (
              selections > 0 && (
                <span className="sr-only">{t("canvas.selected_none")}</span>
              )
            ) : chosenFrom !== undefined && chosenTo !== undefined ? (
              <>
                <span aria-hidden="true">
                  {t("canvas.connection_selected")}
                </span>
                <span className="sr-only">
                  {t("canvas.selected_connection", {
                    from: titleOf(chosenFrom, t),
                    to: titleOf(chosenTo, t),
                  })}
                </span>
              </>
            ) : (
              t("canvas.connection_selected")
            )}
            {/* Why Copy, Cut, and Duplicate are disabled, of more nodes than
            a clipboard holds. */}
            {selected > maxEdited &&
              ` ${
                editable
                  ? t("canvas.too_many", { most: maxEdited })
                  : t("canvas.too_many_copy", { most: maxEdited })
              }`}
          </span>
        </p>
        {/* What the page's clipboard holds of this workspace, which Paste
        pastes, said as a copy or a cut puts it there, as it changes
        nothing that the page shows (FR-EDIT-07). */}
        <p role="status" className="text-sm text-fg-muted">
          {copied !== undefined && (
            <span key={copied.serial}>
              {t("canvas.on_clipboard", {
                count: copied.clipboard.nodes.length,
              })}
            </span>
          )}
        </p>
        {namesFailed && (
          <p role="alert" className="flex flex-wrap items-center gap-2 text-sm">
            {t("canvas.names_failed")}
            <Button onClick={() => setNamesRead((n) => n + 1)}>
              {t("canvas.retry")}
            </Button>
          </p>
        )}
      </fieldset>
      <div className={inspecting && editable ? besidePanel : undefined}>
        <div className="relative min-w-0">
          <div
            ref={hostRef}
            role="application"
            aria-roledescription={t("canvas.roledescription")}
            aria-label={t("canvas.label", { board: boardName })}
            aria-describedby={helpId}
            // biome-ignore lint/a11y/noNoninteractiveTabindex: the canvas takes the keys that pan and zoom it (NFR-A11Y-04).
            tabIndex={0}
            onKeyDown={keyDown}
            onKeyUp={keyUp}
            onBlur={blur}
            style={{ height }}
            className="relative touch-none overflow-hidden overscroll-none rounded-md bg-canvas ring-1 ring-border select-none focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-focus"
          >
            <div ref={surfaceRef} className="absolute inset-0" />
            <div
              ref={bandRef}
              hidden
              aria-hidden="true"
              className="pointer-events-none absolute top-0 left-0 border-2 border-accent bg-accent/10"
            />
            {overlay !== undefined && (
              <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
                {/* The message lets a press on it through to the canvas under
                it, so that a drag that starts on it, as on an empty board,
                pans as one beside it does, as the wheel over it does; its
                button alone, which reads the content again, takes its own. */}
                <p
                  role={overlay.role}
                  className="max-w-md rounded-md border border-border bg-surface px-4 py-3 text-center"
                >
                  {overlay.text}
                  {content === "failed" && engine !== "unsupported" && (
                    <>
                      {" "}
                      <Button
                        onClick={() => setAttempt((n) => n + 1)}
                        className="pointer-events-auto"
                      >
                        {t("canvas.retry")}
                      </Button>
                    </>
                  )}
                </p>
              </div>
            )}
            {showMinimap && (
              // The minimap is a pointer's shortcut: the keys pan and fit the
              // board as it does (NFR-A11Y-04), so it is hidden from the
              // accessibility tree, as the canvas's pixels are.
              <div
                aria-hidden="true"
                className="absolute right-3 bottom-3 h-32 w-48 overflow-hidden rounded-md border border-border bg-surface shadow-md"
              >
                <canvas
                  ref={minimapRef}
                  onPointerDown={minimapPointer}
                  onPointerMove={minimapPointer}
                  onPointerUp={minimapRelease}
                  onPointerCancel={minimapRelease}
                  onLostPointerCapture={minimapRelease}
                  className="block size-full cursor-pointer"
                />
              </div>
            )}
          </div>
          {/* The board's nodes and connections, as a tree that assistive
          technology reads, and the keyboard moves through, after the
          canvas, which draws them as pixels (NFR-A11Y-03, NFR-A11Y-04). */}
          <CanvasTree
            ref={tree}
            projectId={projectId}
            boardName={boardName}
            controller={controller}
            selection={selection}
            connection={connection}
            editable={editable}
            onClipboard={treeClipboard}
          />
          {/* What the canvas places over its nodes, outside its element, which
          takes every touch and key of its own: the resize handle of the
          node selected, the link handles of its ports, or of the ends of
          the connection selected, the link of a jumper to its target, and
          the editor of an element's text. The layer lets a pointer through
          to the canvas but on them, and the canvas takes the wheel and the
          pinch over a handle too, and a handle's press, whose pointer it
          follows as its own (pressHandle). */}
          <div
            ref={overRef}
            className="pointer-events-none absolute inset-0 overflow-hidden rounded-md"
          >
            {editable &&
              selected === 1 &&
              // The node whose text's editor is open over it has no handle,
              // which the editor would cover.
              [...selection]
                .filter((id) => id !== editing)
                .map((id) => (
                  <ResizeHandle
                    key={id}
                    id={id}
                    controller={controller}
                    onPress={pressHandle}
                  />
                ))}
            {editable &&
              onlyNode !== undefined &&
              onlyNode.id !== editing &&
              // A handle on each port that connections leave: an element's
              // one, and each of a branch's outputs.
              portsOf(onlyNode).map((port) => (
                <LinkHandle
                  key={`${onlyNode.id} ${port}`}
                  link={{ kind: "new", node: onlyNode.id, port }}
                  controller={controller}
                  place={() =>
                    portPlace(controller, { node: onlyNode.id, port })
                  }
                  onPress={pressHandle}
                />
              ))}
            {editable &&
              connection !== undefined &&
              (["from", "to"] as const).map((end) => (
                <LinkHandle
                  key={`${connection} ${end}`}
                  link={{ kind: end, connection }}
                  controller={controller}
                  place={() => endPlace(controller, connection, end)}
                  onPress={pressHandle}
                />
              ))}
            {onlyNode !== undefined &&
              onlyNode.id !== editing &&
              onlyLink !== undefined && (
                <TargetLink
                  id={onlyNode.id}
                  controller={controller}
                  projectId={projectId}
                  boardId={onlyLink.boardId}
                  nodeId={onlyLink.nodeId}
                  onFollow={follow}
                />
              )}
            {editable && offer !== undefined && (
              <ConnectOffer
                controller={controller}
                at={offer.at}
                onChoose={(type) => {
                  void actions.connectNew(offer.from, type, offer.at);
                  endOffer();
                }}
                onCancel={endOffer}
                returnFocus={hostElement}
              />
            )}
            {/* The context menu, of what is selected, or of the board at
            its point (FR-EDIT-09): what the toolbar and the inspector do
            too, and, of the board, a node created where the menu was asked
            for. A board that may not be changed has those of its items
            that change nothing. What holds it is there before one is open,
            and hears the key, or the pointer, that asks for one. */}
            <ContextMenus
              open={menu !== undefined && !menuStale}
              onClose={closeMenu}
            >
              {menu !== undefined && !menuStale && (
                <ContextMenu
                  key={menu.key}
                  controller={controller}
                  at={menu.target.at}
                  label={
                    menu.target.kind === "board"
                      ? t("canvas.menu.board")
                      : menu.target.kind === "connection"
                        ? t("canvas.menu.connection")
                        : t("canvas.menu.nodes", { count: selected })
                  }
                  returnFocus={() => {
                    if (!(menu.tree && tree.current?.focus() === true)) {
                      hostRef.current?.focus({ preventScroll: true });
                    }
                  }}
                >
                  {menu.target.kind === "board" ? (
                    <>
                      {editable && content === "ready" && (
                        <>
                          <MenuItem
                            onSelect={() =>
                              void actions.create("element", menu.target.at)
                            }
                          >
                            {t("canvas.menu.add_element")}
                          </MenuItem>
                          <MenuItem
                            onSelect={() =>
                              void actions.create("branch", menu.target.at)
                            }
                          >
                            {t("canvas.menu.add_branch")}
                          </MenuItem>
                          <MenuItem
                            onSelect={() =>
                              void actions.create("jumper", menu.target.at)
                            }
                          >
                            {t("canvas.menu.add_jumper")}
                          </MenuItem>
                          {copied !== undefined && (
                            <MenuItem
                              disabled={!pastable}
                              onSelect={() => void pasteAt(menu.target.at)}
                            >
                              {t("canvas.menu.paste_here")}
                            </MenuItem>
                          )}
                          <MenuSeparator />
                        </>
                      )}
                      <MenuItem onSelect={() => controller.fit()}>
                        {t("canvas.fit")}
                      </MenuItem>
                    </>
                  ) : menu.target.kind === "connection" ? (
                    <>
                      <MenuItem onSelect={inspect}>
                        {t("canvas.menu.inspect")}
                      </MenuItem>
                      <MenuSeparator />
                      <MenuItem
                        danger
                        onSelect={() =>
                          connection !== undefined &&
                          void actions.removeConnection(connection)
                        }
                      >
                        {t("canvas.delete")}
                      </MenuItem>
                    </>
                  ) : (
                    <>
                      {editable && onlyNode?.type === "element" && (
                        <MenuItem onSelect={() => openText(onlyNode.id)}>
                          {t("canvas.menu.edit_text")}
                        </MenuItem>
                      )}
                      {editable && (
                        <MenuItem onSelect={inspect}>
                          {t("canvas.menu.inspect")}
                        </MenuItem>
                      )}
                      {onlyLink !== undefined && (
                        <MenuItem onSelect={goToTarget}>
                          {t("canvas.go_to_target")}
                        </MenuItem>
                      )}
                      <MenuItem onSelect={() => controller.zoomToSelection()}>
                        {t("canvas.zoom_selection")}
                      </MenuItem>
                      <MenuItem
                        disabled={selected > maxEdited || actions.busy}
                        onSelect={() => void actions.copy([...selection])}
                      >
                        {t("canvas.copy")}
                      </MenuItem>
                      {editable && (
                        <>
                          <MenuItem
                            disabled={selected > maxEdited || actions.busy}
                            onSelect={() => void actions.cut([...selection])}
                          >
                            {t("canvas.cut")}
                          </MenuItem>
                          <MenuItem
                            disabled={selected > maxEdited || actions.busy}
                            onSelect={() =>
                              void actions.duplicate([...selection])
                            }
                          >
                            {t("canvas.duplicate")}
                          </MenuItem>
                          <MenuSeparator />
                          <MenuItem
                            danger
                            onSelect={() => void actions.remove([...selection])}
                          >
                            {t("canvas.delete")}
                          </MenuItem>
                        </>
                      )}
                    </>
                  )}
                </ContextMenu>
              )}
            </ContextMenus>
            {editing !== undefined && editedNode !== undefined && (
              <TextEditor
                key={editing}
                ref={editor}
                projectId={projectId}
                node={editedNode}
                controller={controller}
                host={hostElement}
                gone={editingGone}
                readOnly={!editable}
                onClose={closeEditor}
              />
            )}
          </div>
          {/* What the board's edits say, as what became of an undo, why a
          batch was refused, and the undo of a bulk move: over the canvas's
          top, rather than above it, where each as it came or went would
          move the canvas down the page and back, and the board with it,
          under a drag's pointer too (plan note N68). A press, the wheel, or
          a pinch on its boxes is the canvas's, as on the board beside them,
          rather than lost on a box that stays until the next undo, redo, or
          batch: their buttons alone take their own. A board that may not be
          changed says why a copy was refused alone. */}
          <div className="pointer-events-none absolute inset-x-2 top-2 flex max-w-xl flex-col items-start gap-2 [&_button]:pointer-events-auto">
            {editable && (
              <HistoryStatus history={history} alertClassName={messageAlert} />
            )}
            <Alert
              className={messageAlert}
              message={
                actions.error === null
                  ? undefined
                  : refusedMessage(
                      t,
                      actions.refused,
                      refusalOf(actions.error, t)?.message ?? "",
                    )
              }
            />
            {editable && moved !== undefined && (
              // A bulk move after the one before offers its own undo for as
              // long, rather than for what was left of the one before's.
              <UndoMove
                key={moved.key}
                projectId={projectId}
                moved={moved}
                history={history}
                onDone={clearMoved}
              />
            )}
          </div>
          {pasting !== undefined && (
            <PasteDialog
              report={pasting.report}
              opener={pasting.opener}
              onPaste={(create) => {
                setPasting(undefined);
                if (editableNow.current) {
                  void actions.paste(
                    pasting.clipboard,
                    pasting.at,
                    create ? pasting.report : undefined,
                    pasting.asked,
                  );
                }
              }}
              onCancel={() => setPasting(undefined)}
            />
          )}
        </div>
        {inspecting && editable && (
          <Inspector
            projectId={projectId}
            selection={selection}
            connection={connection}
            actions={actions}
            targets={targets}
            onSelectConnection={(id) => controller.selectConnection(id)}
            onSelectNode={(id) => controller.select([id], "replace")}
            onFollow={follow}
            // A sheet over the canvas closes to show the editor of the text,
            // which it would else hold inert under it.
            onEditText={(id) => {
              openText(id);
              if (!docked) {
                setInspecting(false);
              }
            }}
            onClose={() => setInspecting(false)}
            returnFocus={() => inspectorButton.current}
            headingRef={inspectorHeading}
          />
        )}
      </div>
      <div className="flex flex-wrap items-start gap-2">
        <p id={helpId} className="min-w-0 flex-1 text-sm text-fg-muted">
          {editable
            ? t("canvas.help_edit", { palette: paletteKey })
            : t("canvas.help", { palette: paletteKey })}
        </p>
        <Button
          aria-haspopup="dialog"
          onClick={() => registry?.run("shortcuts.show")}
        >
          {t("canvas.shortcuts")}
        </Button>
      </div>
      {options.bench !== undefined && (
        <Suspense>
          <Bench
            handle={controller.handle}
            options={options.bench}
            edit={editable ? actions.move : undefined}
          />
        </Suspense>
      )}
    </div>
  );
}
