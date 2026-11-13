// A board's script view (PRD FR-TEXT-04, NFR-A11Y-02, plan note N75): the
// board as a linear, screenplay-like document, which the board's page shows
// in place of its canvas, of its nodes in the order of order.ts, the rule of
// which it says above them: each node's title, the line of its type, as its
// card says it, with an element's speaker, its text, its choices as
// indented lines, each with its label, its condition in plain language
// (plain.ts), and the node that it leads to, a branch's outputs with the
// connections of each, and a jumper's target. It is the canvas's accessible
// alternative: a document of headings, lists, links, and buttons, which a
// keyboard and a screen reader read and work alone (AC3). It changes the
// board through the commands that the canvas sends for the same changes
// (AC1): a node's title and text, as its inspector and the canvas's editor
// of a text do, node.update, and a choice's label, as the connection's
// inspector does, connection.update, each in a form of its own, which
// expects the revision that it shows (useEntityForm); and, in the
// constrained form that it offers them, "add node after" and "add choice"
// (batches.ts). What the rest of the canvas does to a node, the view leaves
// to the canvas, which each node's Show on canvas opens at the node. The
// document shows what any page or session changes as it commits, so the
// canvas shows the view's changes, and the view the canvas's, without a
// reload. A node's entry is shown anew only once its node, the connections
// that leave it, or what its names and conditions read of the project
// changes, so that an edit of one node's text of a board of hundreds
// renders one entry (AC4).

import type {
  Board,
  BranchOutput,
  Connection,
  Expression,
  Node,
} from "@storycreep/shared/api";
import { type Node as Ast, decode } from "@storycreep/shared/expr/compile";
import { newId } from "@storycreep/shared/ids";
import { createLink, Link as RouterLink } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import {
  type ComponentPropsWithRef,
  type KeyboardEvent,
  memo,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import {
  kindOf,
  nameParts,
  namesDiffer,
  oneLine,
  plainText,
  referenceNames,
  titleOf,
} from "../canvas/labels";
import {
  type JumperTarget,
  type TargetLookup,
  targetOf,
} from "../canvas/targets";
import { ConflictNotice, useEntityForm } from "../content/form";
import { useTextChoices, useTextNames } from "../content/useTextNames";
import { boardPart } from "../document/doc";
import { useDocumentState, useProjectDocument } from "../document/react";
import { equal } from "../document/reconcile";
import type { Overlap } from "../document/store";
import type { Words } from "../expr/messages";
import { Parts } from "../expr/Names";
import { type Part, plainOf } from "../expr/plain";
import type { ScopeSources } from "../expr/scope";
import { payloadField } from "../project/dialogs";
import { loadPart, useCommands } from "../project/queries";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { suggestionTook } from "../richtext/suggest";
import { focusIsNowhere, followInPage, showInPage } from "../shell/focus";
import { Button, buttonClass } from "../ui/Button";
import { TextField, useFocusRefusal } from "../ui/Field";
import { type Held, useOwnKeys } from "../ui/held";
import { Alert, textLink } from "../ui/Page";
import {
  addAfterBatch,
  addChoiceBatch,
  type NewIds,
  placeAfter,
} from "./batches";
import { leavingByNode, scriptOrder } from "./order";
import { isEmptyText, RichText, type TextNames } from "./RichText";
import { nothingShown, placeShown, type Shown } from "./shown";

/** Returns the ID of the entry of the node of nodeId, which a link to it gives the focus. */
export function entryId(nodeId: string): string {
  return `script-${nodeId}`;
}

/** Returns the ID of the button of what, of the node or the connection of id, which the focus goes back to once what it opened closes. */
function buttonId(
  id: string,
  what: "edit" | "after" | "choice" | "label",
): string {
  return `script-${id}-${what}`;
}

/** What an editor that is open edits: a node's title and text, its field first as focusOn says, or a choice's label. */
type Editing =
  | { kind: "node"; id: string; focusOn: "title" | "text" }
  | { kind: "choice"; id: string };

/** What the view gives each entry, which changes only once what its names and conditions read of the project changes: its variables, components, component types, or boards, or what went from it, each map of which changes with any of its entities, or a node's title. */
interface Shared {
  t: TFunction;
  words: Words;
  projectId: string;
  boardId: string;
  editable: boolean;
  /** The names that texts show of what they refer to. */
  names: TextNames;
  /** What conditions name, and jumpers target, with the board's nodes' titles. */
  scope: ScopeSources;
  /**
   * What tells of a jumper's target that the document does not hold: that
   * it was deleted, where the document has read the target's board, or
   * holds no such board, as the canvas's card tells it, but without the
   * reads of targets by themselves that the canvas makes.
   */
  targets: TargetLookup;
  /** Opens the editor of what editing names, beside those open already. */
  onEdit: (editing: Editing) => void;
  /**
   * Closes the editor of the node or the connection of id, and, where
   * moveFocus says to, gives the element of focus the focus, or else the
   * entry of the node of entry.
   */
  onClose: (
    id: string,
    focus: string,
    entry: string,
    moveFocus: boolean,
  ) => void;
  onAddAfter: (node: Node) => void;
  onAddChoice: (node: Node) => void;
}

/** What the view is given. */
export interface ScriptViewProps {
  projectId: string;
  board: Board;
  /** The read of the board's nodes and connections that the page's loader started. */
  read: Promise<void> | undefined;
  /** Whether the user may change the board. */
  editable: boolean;
  /** The node whose entry the view shows with the focus once it has read the board, as a link names it. */
  focus?: string;
}

/**
 * Returns map, of the nodes of the project, as the same object as the one
 * before for as long as no node's title, board, or trash changed, which is
 * what the view shows of a node beyond its own entry, as a choice's target
 * and a condition's node, so that an edit of a node's text shows one entry
 * anew rather than all. The map kept is the one of the view as it last
 * committed, rather than of a render that React may yet discard.
 */
function useTitled(map: ReadonlyMap<string, Node>): ReadonlyMap<string, Node> {
  const kept = useRef(map);
  const titled =
    kept.current !== map &&
    namesDiffer(
      kept.current,
      map,
      (n) => `${n.title}\n${n.board_id}\n${n.deleted_at ?? ""}`,
    )
      ? map
      : kept.current;
  useLayoutEffect(() => {
    kept.current = titled;
  }, [titled]);
  return titled;
}

/** The view of the board of the project of projectId. */
export function ScriptView({
  projectId,
  board,
  read,
  editable,
  focus,
}: ScriptViewProps) {
  const { t, i18n } = useTranslation();
  const doc = useProjectDocument(projectId);
  const boardId = board.id;
  const words = useMemo<Words>(
    () => ({ t, locale: i18n.language }),
    [t, i18n.language],
  );

  // The board's nodes and connections, read once, first by the read that
  // the page's loader started, as the canvas reads them, whose failure the
  // view says, with Try again.
  const [content, setContent] = useState<"loading" | "ready" | "failed">(
    "loading",
  );
  const [attempt, setAttempt] = useState(0);
  const [loaderRead] = useState(() => ({ doc, read }));
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt reads the board's content again, as the user asks for it.
  useEffect(() => {
    let ended = false;
    setContent("loading");
    const first =
      attempt === 0 && loaderRead.doc === doc ? loaderRead.read : undefined;
    (first ?? doc.load(boardPart(boardId))).then(
      () => {
        if (!ended) {
          setContent("ready");
        }
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
  }, [doc, boardId, attempt]);

  // The names that the entries show, of the speakers and of what texts and
  // conditions refer to, read anew as the view opens, as the canvas reads
  // them, whose failure the view says, with Try again.
  const [namesRead, setNamesRead] = useState(0);
  const [namesFailed, setNamesFailed] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: namesRead reads the names again, as the user asks for it.
  useEffect(() => {
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

  const nodes = useDocumentState(doc, (s) => s.view.nodes);
  const connections = useDocumentState(doc, (s) => s.view.connections);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  const boards = useDocumentState(doc, (s) => s.view.boards);
  const gone = useDocumentState(doc, (s) => s.view.gone);
  const titled = useTitled(nodes);

  // The board's nodes and connections, each the same array as the view
  // committed last for as long as it holds the same objects, so that a
  // change elsewhere in the project, as of another board's nodes, orders
  // nothing anew.
  const nodesNow = useMemo(
    () =>
      [...nodes.values()].filter(
        (n) => n.board_id === boardId && n.deleted_at === undefined,
      ),
    [nodes, boardId],
  );
  const boardNodes = useSame(nodesNow);
  const connectionsNow = useMemo(
    () =>
      [...connections.values()].filter(
        (c) => c.board_id === boardId && c.deleted_at === undefined,
      ),
    [connections, boardId],
  );
  const boardConnections = useSame(connectionsNow);
  const leavingOf = useLeaving(boardConnections, boardNodes);
  const order = useMemo(
    () => scriptOrder(boardNodes, leavingOf, board.entry_node_id),
    [boardNodes, leavingOf, board.entry_node_id],
  );

  // The editors that are open, by the ID of what each edits, of which the
  // user may open several, as of a node and of another's choice, so that
  // one opened, or a node added, never takes away what another holds; and
  // the element that the focus goes to once the view has rendered, as the
  // button that opened an editor that closed.
  const [open, setOpen] = useState<ReadonlyMap<string, Editing>>(
    () => new Map(),
  );
  const opened = useCallback(
    (change: (open: Map<string, Editing>) => void) =>
      setOpen((was) => {
        const next = new Map(was);
        change(next);
        return next;
      }),
    [],
  );
  // What the view showed of its open forms, and which of them were ghosts,
  // as it committed last (placeShown).
  const shownLast = useRef<Shown>(nothingShown);
  const ghostsLast = useRef<ReadonlySet<string>>(new Set());
  const [focusTo, setFocusTo] = useState<{
    id: string;
    entry?: string;
    /** Whether the focus goes there only where nothing has it (focusIsNowhere), as once the form that had it closed, rather than taken from where the user went meanwhile. */
    unlessFocused?: boolean;
  }>();
  useLayoutEffect(() => {
    if (focusTo === undefined) {
      return;
    }
    // The element named, or, where the view does not show it, as the
    // button of an entry that went, or that a viewer is not offered, the
    // entry, or else the view's heading, rather than nowhere.
    if (!focusTo.unlessFocused || focusIsNowhere(document)) {
      (
        document.getElementById(focusTo.id) ??
        (focusTo.entry === undefined
          ? null
          : document.getElementById(entryId(focusTo.entry))) ??
        document.getElementById("script-heading")
      )?.focus();
    }
    setFocusTo(undefined);
  }, [focusTo]);

  // The entry that the address names, given the focus once the board has
  // been read, once for each node that it names.
  const shownFocus = useRef<string>(undefined);
  useEffect(() => {
    if (
      content !== "ready" ||
      focus === undefined ||
      shownFocus.current === focus
    ) {
      return;
    }
    shownFocus.current = focus;
    showInPage(document, entryId(focus));
  }, [content, focus]);

  // The structural edits, each a batch, of which an element has one under
  // way at a time, whose buttons wait for it, so that a double click adds
  // one node; each batch's refusal, whatever others are under way, the
  // view says, in the entry of the node that it was of, until that node's
  // next addition, and reads out, each in a line of its own, so that two
  // of the same words are both heard. The editor of what a refused batch
  // created is then a ghost (placeShown), which keeps what the user typed
  // in it meanwhile, and closes of nothing typed, as a ghost does; the
  // focus goes back to the button that asked for it, where nothing has the
  // focus, or as that editor closes where it had it, rather than be taken
  // from another editor that the user went to meanwhile. Each is a
  // mutation of the page's batches (useCommands), which the query client's
  // cache hears of, as of a session that ended, whose promise settles of
  // its own batch, as mutate's callbacks would of the latest alone.
  const structural = useCommands(projectId);
  const [adding, setAdding] = useState<ReadonlyMap<string, NewIds>>(
    () => new Map(),
  );
  // What each node's last addition was refused with, by the node's ID, and
  // the refusals that the view read out since the last addition began, by
  // the ID of what each created.
  const [refused, setRefused] = useState<ReadonlyMap<string, unknown>>(
    () => new Map(),
  );
  const [readOut, setReadOut] = useState<
    readonly { key: string; error: unknown }[]
  >([]);
  // The element and the button of the addition that created what each
  // open ghost of a refused addition edits, by its ID, which the focus
  // goes back to as its editor closes.
  const notAdded = useRef(
    new Map<string, { node: string; what: "after" | "choice" }>(),
  );
  // What the view committed last, which its callbacks read.
  const latest = useRef({ boardNodes, leavingOf, adding, structural, open });
  useLayoutEffect(() => {
    latest.current = { boardNodes, leavingOf, adding, structural, open };
  });
  const add = useCallback(
    (node: Node, what: "after" | "choice") => {
      const { boardNodes, leavingOf, adding, structural } = latest.current;
      if (adding.has(node.id)) {
        return;
      }
      const leaving = leavingOf.get(node.id) ?? [];
      const ids = { node: newId("node"), connection: newId("connection") };
      const batch =
        what === "after"
          ? addAfterBatch(node, leaving, placeAfter(node, boardNodes), ids)
          : addChoiceBatch(
              node,
              placeAfter(
                node,
                boardNodes,
                leaving.filter((c) => c.from_port === "out").length,
              ),
              ids,
            );
      const settled = () =>
        setAdding((was) => {
          const next = new Map(was);
          next.delete(node.id);
          return next;
        });
      setRefused((was) => {
        if (!was.has(node.id)) {
          return was;
        }
        const next = new Map(was);
        next.delete(node.id);
        return next;
      });
      setReadOut((was) => (was.length === 0 ? was : []));
      setAdding((was) => new Map(was).set(node.id, ids));
      // The editor of what the batch creates: the new node's text, or the
      // new choice's label.
      const made = what === "after" ? ids.node : ids.connection;
      opened((o) => {
        o.set(
          made,
          what === "after"
            ? { kind: "node", id: made, focusOn: "text" }
            : { kind: "choice", id: made },
        );
      });
      structural.mutateAsync(batch).then(settled, (error: unknown) => {
        settled();
        // The editor that the view showed stays, a ghost, and one that it
        // never showed goes.
        const shown = shownLast.current;
        if (latest.current.open.has(made)) {
          if (shown.nodes.has(made) || shown.choices.has(made)) {
            notAdded.current.set(made, { node: node.id, what });
          } else {
            opened((o) => {
              o.delete(made);
            });
          }
        }
        setRefused((was) => new Map(was).set(node.id, error));
        setReadOut((was) => [...was, { key: made, error }]);
        setFocusTo({ id: buttonId(node.id, what), unlessFocused: true });
      });
    },
    [opened],
  );
  const onAddAfter = useCallback((node: Node) => add(node, "after"), [add]);
  const onAddChoice = useCallback((node: Node) => add(node, "choice"), [add]);
  const onEdit = useCallback(
    (e: Editing) =>
      opened((o) => {
        o.set(e.id, e);
      }),
    [opened],
  );
  const onClose = useCallback(
    (id: string, focus: string, entry: string, moveFocus: boolean) => {
      opened((o) => {
        o.delete(id);
      });
      // The ghost of what a refused addition created gives the focus back
      // to the button that asked for the addition, as its refusal does.
      const by = ghostsLast.current.has(id)
        ? notAdded.current.get(id)
        : undefined;
      notAdded.current.delete(id);
      if (moveFocus) {
        setFocusTo(
          by === undefined
            ? { id: focus, entry }
            : { id: buttonId(by.node, by.what), entry: by.node },
        );
      }
    },
    [opened],
  );

  const targets = useMemo<TargetLookup>(
    () => ({
      boardRead: (id) => doc.hasRead(boardPart(id)),
      treeRead: () => doc.hasRead("tree"),
      read: () => undefined,
    }),
    [doc],
  );
  const shared = useMemo<Shared>(
    () => ({
      t,
      words,
      projectId,
      boardId,
      editable,
      names: referenceNames({ variables, components, componentTypes }, boardId),
      scope: {
        variables,
        components,
        componentTypes,
        boards,
        nodes: titled,
        gone,
      },
      targets,
      onEdit,
      onClose,
      onAddAfter,
      onAddChoice,
    }),
    [
      t,
      words,
      projectId,
      boardId,
      editable,
      variables,
      components,
      componentTypes,
      boards,
      titled,
      gone,
      targets,
      onEdit,
      onClose,
      onAddAfter,
      onAddChoice,
    ],
  );

  // The entries as the view shows them (placeShown): each node's at its
  // place in the order; but the entry of a node whose editor is open, or
  // whose choice's label's is, in the part where the view showed it last,
  // and that choice in the entry where it showed it, whatever moved them,
  // so that the editor, the same as before, keeps what the user typed; and
  // one that the board no longer has, as once another session deleted it,
  // as a ghost where the view showed it last, saying that it can no longer
  // be saved, until the user cancels it, or the node is back, as once its
  // deletion is undone, or once a batch refused for want of an answer
  // applied all the same. An editor of what the view has not shown yet, as
  // of a node that a batch under way adds, shows nothing until its entry
  // does; and what a batch under way created, which the page takes from the
  // board as the batch is refused, before the view hears of the refusal,
  // stays where the view showed it, with no ghost until then. What the view
  // showed is what it committed, rather than what a render that React may
  // yet discard placed.
  const creating = useMemo(
    () =>
      new Set(
        [...adding.values()].flatMap((ids) => [ids.node, ids.connection]),
      ),
    [adding],
  );
  const placed = placeShown(
    order,
    leavingOf,
    connections,
    open,
    shownLast.current,
    creating,
  );
  useLayoutEffect(() => {
    shownLast.current = placed.shown;
    ghostsLast.current = placed.ghosts;
  });
  const { sections, ghosts } = placed;

  const entries = (list: readonly Node[]) =>
    list.map((node) => {
      const leaving = placed.leaving.get(node.id) ?? none;
      return (
        <Entry
          key={node.id}
          node={node}
          leaving={leaving}
          editing={editingIn(open, node.id, leaving)}
          gone={goneIn(ghosts, node.id, leaving)}
          adding={adding.has(node.id)}
          refused={refusalOf(refused.get(node.id), t)?.message}
          shared={shared}
        />
      );
    });

  // The view's rule depends on its entry node being on the board, which one
  // in the trash is not, or on the view showing the part of what that node
  // reaches, as of an open form's entry kept there, or the ghost of the
  // entry node; the other part is named for what the entry node does not
  // reach where the view shows that part, and for all the nodes where it
  // does not; and each part counts the entries that it lists.
  const reaching = sections.reached.length > 0;
  const entered = order.reached.length > 0 || reaching;
  const shownCount = sections.reached.length + sections.unreached.length;
  return (
    <section aria-labelledby="script-heading" className="space-y-4">
      <h2 id="script-heading" tabIndex={-1} className="text-lg font-semibold">
        {t("script.heading")}
      </h2>
      <p className="max-w-prose text-fg-muted">
        {entered || content !== "ready"
          ? t("script.rule")
          : t("script.rule_no_entry")}
      </p>
      {namesFailed && (
        <p role="alert" className="flex flex-wrap items-center gap-2">
          {t("script.names_failed")}
          <Button onClick={() => setNamesRead((n) => n + 1)}>
            {t("script.retry")}
          </Button>
        </p>
      )}
      {/* Each refused addition is read out here, in a line of its own,
      and shown in its node's entry, where the focus goes back to. */}
      <div role="alert">
        {readOut.map((r) => (
          <p key={r.key} className="sr-only">
            {refusalOf(r.error, t)?.message}
          </p>
        ))}
      </div>
      {content === "failed" ? (
        <p role="alert" className="flex flex-wrap items-center gap-2">
          {t("script.load_failed")}
          <Button onClick={() => setAttempt((n) => n + 1)}>
            {t("script.retry")}
          </Button>
        </p>
      ) : content === "loading" ? (
        <p role="status">{t("script.loading")}</p>
      ) : shownCount === 0 ? (
        <p role="status">{t("script.empty")}</p>
      ) : (
        <>
          {reaching && (
            <section aria-labelledby="script-reached" className="space-y-3">
              <h3 id="script-reached" className="font-semibold">
                {t("script.reached", { count: sections.reached.length })}
              </h3>
              <ol className="max-w-3xl space-y-3">
                {entries(sections.reached)}
              </ol>
            </section>
          )}
          {sections.unreached.length > 0 && (
            <section aria-labelledby="script-unreached" className="space-y-3">
              <h3 id="script-unreached" className="font-semibold">
                {reaching
                  ? t("script.unreached", { count: sections.unreached.length })
                  : t("script.all", { count: sections.unreached.length })}
              </h3>
              <ol className="max-w-3xl space-y-3">
                {entries(sections.unreached)}
              </ol>
            </section>
          )}
        </>
      )}
    </section>
  );
}

/** No connections, as one array for every node that none leaves. */
const none: readonly Connection[] = [];

/**
 * Returns list, or the list that the view committed last, where that holds
 * the same objects in the same order, so that what is computed of it is
 * not computed anew.
 */
function useSame<T>(list: readonly T[]): readonly T[] {
  const kept = useRef(list);
  const was = kept.current;
  const same =
    was !== list &&
    was.length === list.length &&
    was.every((item, i) => item === list[i])
      ? was
      : list;
  useLayoutEffect(() => {
    kept.current = same;
  }, [same]);
  return same;
}

/**
 * Returns the connections of the board, connections, that leave each of
 * its nodes, boardNodes, by the node's ID, in the order that the flow
 * offers them (leavingByNode), each list the same array as the view
 * committed last for as long as its connections are the same objects, so
 * that the entry of a node whose connections did not change is not shown
 * anew.
 */
function useLeaving(
  connections: readonly Connection[],
  boardNodes: readonly Node[],
): ReadonlyMap<string, readonly Connection[]> {
  const kept = useRef<ReadonlyMap<string, readonly Connection[]>>(new Map());
  const leaving = useMemo(() => {
    const ordered = leavingByNode(boardNodes, connections);
    const out = new Map<string, readonly Connection[]>();
    for (const [id, list] of ordered) {
      const was = kept.current.get(id);
      out.set(
        id,
        was !== undefined &&
          was.length === list.length &&
          was.every((c, i) => c === list[i])
          ? was
          : list,
      );
    }
    return out;
  }, [connections, boardNodes]);
  useLayoutEffect(() => {
    kept.current = leaving;
  }, [leaving]);
  return leaving;
}

/**
 * Returns what of the entry of the node of nodeId, of the connections
 * leaving, the editors of open edit, as one string, which the entry renders
 * anew for only as it changes: node:title or node:text, of its node's
 * title and text, of which field first, and the IDs of its choices whose
 * labels are open, apart by spaces; or the empty string of none.
 */
function editingIn(
  open: ReadonlyMap<string, Editing>,
  nodeId: string,
  leaving: readonly Connection[],
): string {
  if (open.size === 0) {
    return "";
  }
  const parts: string[] = [];
  const node = open.get(nodeId);
  if (node?.kind === "node") {
    parts.push(`node:${node.focusOn}`);
  }
  for (const c of leaving) {
    if (open.get(c.id)?.kind === "choice") {
      parts.push(c.id);
    }
  }
  return parts.join(" ");
}

/**
 * Returns which of the entry of the node of nodeId, of the connections
 * leaving, is a ghost of ghosts, which the board no longer has, as one
 * string, which the entry renders anew for only as it changes: node, of
 * the node itself, and the IDs of its choices, apart by spaces; or the empty
 * string of none.
 */
function goneIn(
  ghosts: ReadonlySet<string>,
  nodeId: string,
  leaving: readonly Connection[],
): string {
  if (ghosts.size === 0) {
    return "";
  }
  const parts = ghosts.has(nodeId) ? ["node"] : [];
  for (const c of leaving) {
    if (ghosts.has(c.id)) {
      parts.push(c.id);
    }
  }
  return parts.join(" ");
}

/** The ASTs of the conditions that the view has decoded, by their expression, which stays the same object until it changes. */
const decoded = new WeakMap<Expression, Ast | null>();

/**
 * Returns what a condition says, as the view shows it: in plain language,
 * from its AST, of a condition that compiles, as the expression editor says
 * it (plain.ts); or else its source, on one line, as code.
 */
function conditionParts(
  condition: Expression,
  shared: Shared,
): { parts: Part[]; valid: boolean } {
  let ast = decoded.get(condition);
  if (ast === undefined) {
    ast = null;
    if (condition.is_valid && condition.ast !== undefined) {
      try {
        ast = decode(condition.ast, "condition");
      } catch {
        ast = null;
      }
    }
    decoded.set(condition, ast);
  }
  if (ast !== null) {
    return {
      parts: plainOf(ast, shared.words, shared.scope, shared.boardId)[0] ?? [],
      valid: true,
    };
  }
  return {
    parts: [{ kind: "code", text: oneLine(condition.source) }],
    valid: condition.is_valid,
  };
}

/** A condition, as an annotation of what it is the condition of. */
function Condition({
  condition,
  shared,
  lead,
}: {
  condition: Expression;
  shared: Shared;
  /** What the annotation starts with, as Only if. */
  lead: string;
}) {
  const { parts, valid } = conditionParts(condition, shared);
  return (
    <>
      {lead} <Parts parts={parts} />
      {!valid && <> {shared.t("script.condition_invalid")}</>}
    </>
  );
}

/**
 * A link of the router's that it never marks current (aria-current), as a
 * link to an entry is no page of its own, though the address names the
 * entry's node, as a link that opened the view at it did.
 */
const EntryLink = createLink(function EntryLink({
  "aria-current": _current,
  "data-status": _status,
  ...props
}: ComponentPropsWithRef<"a"> & { "data-status"?: string }) {
  return <a {...props} />;
});

/** A link to the entry of the node of nodeId, of this board, which gives it the focus, or to the script view of its own board. */
function NodeLink({
  nodeId,
  boardId,
  label,
  shared,
}: {
  nodeId: string;
  boardId: string;
  label: string;
  shared: Shared;
}) {
  return (
    <EntryLink
      to="/projects/$projectId/boards/$boardId"
      params={{ projectId: shared.projectId, boardId }}
      search={{ view: "script", node: nodeId }}
      className={textLink}
      onClick={(e) => {
        if (boardId === shared.boardId) {
          followInPage(e, entryId(nodeId));
        }
      }}
    >
      {label}
    </EntryLink>
  );
}

/** Where a choice leads: the node that it enters, by its title. */
function Target({ nodeId, shared }: { nodeId: string; shared: Shared }) {
  const target = shared.scope.nodes.get(nodeId);
  return (
    <NodeLink
      nodeId={nodeId}
      boardId={shared.boardId}
      label={target === undefined ? nodeId : titleOf(target, shared.t)}
      shared={shared}
    />
  );
}

/** What a jumper's entry says of its target, with a link to it where the page knows where it is. */
function JumpTarget({
  target,
  shared,
}: {
  target: JumperTarget;
  shared: Shared;
}) {
  const { t } = shared;
  switch (target.state) {
    case "none":
      return <>{t("canvas.node.jumper_none")}</>;
    case "gone":
      return <>{t("canvas.node.jumper_broken")}</>;
    case "found": {
      const board = target.elsewhere
        ? shared.scope.boards.get(target.boardId)?.name
        : undefined;
      const title = titleOf(target, t);
      return (
        <>
          {t("script.continues_at")}{" "}
          <NodeLink
            nodeId={target.nodeId}
            boardId={target.boardId}
            label={
              board === undefined
                ? title
                : t("script.node_on_board", { node: title, board })
            }
            shared={shared}
          />
        </>
      );
    }
    case "unknown": {
      const board =
        target.boardId === undefined
          ? undefined
          : shared.scope.boards.get(target.boardId)?.name;
      return target.boardId === undefined || board === undefined ? (
        <>{t("canvas.node.jumper_to", { target: target.nodeId })}</>
      ) : (
        <>
          {t("script.continues_at")}{" "}
          <NodeLink
            nodeId={target.nodeId}
            boardId={target.boardId}
            label={t("script.node_of_board", { board })}
            shared={shared}
          />
        </>
      );
    }
  }
}

/** Returns what names a choice: its label, as the plain words that a card writes of it, on one line, or the node that it leads to, of one without. */
function choiceName(c: Connection, shared: Shared): string {
  const label = oneLine(plainText(c.label, shared.names));
  if (label !== "") {
    return label;
  }
  const to = shared.scope.nodes.get(c.to_node_id);
  return shared.t("script.choice_to", {
    node: to === undefined ? c.to_node_id : titleOf(to, shared.t),
  });
}

/** A choice of an element, or a connection of a branch's output, as an indented line of its node's entry. */
function Choice({
  connection,
  editing,
  gone,
  shared,
}: {
  connection: Connection;
  editing: boolean;
  /** Whether the board no longer has the connection, of which the view shows a ghost while its label is open. */
  gone: boolean;
  shared: Shared;
}) {
  const { t } = shared;
  if (editing) {
    return (
      <li>
        <ChoiceEditor
          projectId={shared.projectId}
          connection={connection}
          unsavable={
            gone
              ? t("script.gone")
              : shared.editable
                ? undefined
                : t("script.read_only")
          }
          onClose={(moveFocus) =>
            shared.onClose(
              connection.id,
              buttonId(connection.id, "label"),
              connection.from_node_id,
              moveFocus,
            )
          }
        />
      </li>
    );
  }
  const labelled = !isEmptyText(connection.label);
  return (
    <li className="space-y-1 border-s-2 border-border ps-3">
      {labelled && <RichText doc={connection.label} names={shared.names} />}
      {connection.condition !== undefined && (
        <p className="text-sm text-fg-muted">
          <Condition
            condition={connection.condition}
            shared={shared}
            lead={t("script.only_if")}
          />
        </p>
      )}
      <p className="text-sm">
        {labelled ? t("script.leads_to") : t("script.continues_to")}{" "}
        <Target nodeId={connection.to_node_id} shared={shared} />
      </p>
      {shared.editable && (
        <Button
          id={buttonId(connection.id, "label")}
          onKeyDown={onceHeld}
          aria-label={t("script.edit_label_named", {
            choice: choiceName(connection, shared),
          })}
          onClick={() => shared.onEdit({ kind: "choice", id: connection.id })}
        >
          {t("script.edit_label")}
        </Button>
      )}
    </li>
  );
}

/** A branch's output, with the connections that leave it. */
function Output({
  output,
  leaving,
  editing,
  gone,
  shared,
}: {
  output: BranchOutput;
  leaving: readonly Connection[];
  /** The IDs of the choices whose labels are open. */
  editing: readonly string[];
  /** The IDs of the choices that the board no longer has. */
  gone: readonly string[];
  shared: Shared;
}) {
  const { t } = shared;
  const of = leaving.filter((c) => c.from_port === output.id);
  const lead =
    output.kind === "if"
      ? t("script.output_if")
      : output.kind === "elseif"
        ? t("script.output_elseif")
        : t("script.output_else");
  return (
    <li className="space-y-2">
      <p>
        {output.kind === "else" || output.condition === undefined ? (
          lead
        ) : (
          <Condition condition={output.condition} shared={shared} lead={lead} />
        )}
      </p>
      {of.length === 0 ? (
        <p className="text-sm text-fg-muted">{t("script.not_connected")}</p>
      ) : (
        <ul className="ms-4 space-y-2">
          {of.map((c) => (
            <Choice
              key={c.id}
              connection={c}
              editing={editing.includes(c.id)}
              gone={gone.includes(c.id)}
              shared={shared}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

/** What an entry is given. */
interface EntryProps {
  node: Node;
  /** The connections that leave the node, in the order that the flow offers them. */
  leaving: readonly Connection[];
  /** What of the entry its editors edit (editingIn). */
  editing: string;
  /** What of the entry the board no longer has (goneIn). */
  gone: string;
  /** Whether an addition after the node, or to it, is under way, which its buttons of additions wait for. */
  adding: boolean;
  /** Why the view's last addition after the node, or to it, was refused, if it was. */
  refused: string | undefined;
  shared: Shared;
}

/** The entry of a node: its title, its type, its text, and what follows it. */
const Entry = memo(function Entry({
  node,
  leaving,
  editing,
  gone,
  adding,
  refused,
  shared,
}: EntryProps) {
  const { t } = shared;
  const opened = editing === "" ? [] : editing.split(" ");
  const nodeOpen = opened.find((o) => o.startsWith("node:"));
  const ghosts = gone === "" ? [] : gone.split(" ");
  const nodeGone = ghosts.includes("node");
  const title = titleOf(node, t);
  const headingId = `${entryId(node.id)}-title`;
  const element = node.type === "element";
  const choices = element ? leaving : [];
  // A connection that leaves a branch by a port that it does not have, as
  // an output in the trash, is listed after its outputs, as the canvas's
  // tree lists it.
  const outputIds = new Set((node.outputs ?? []).map((o) => o.id));
  const astray =
    node.type === "branch"
      ? leaving.filter((c) => !outputIds.has(c.from_port))
      : [];
  return (
    <li>
      <article
        id={entryId(node.id)}
        tabIndex={-1}
        aria-labelledby={headingId}
        className="space-y-2 rounded-md border border-border p-4 [contain-intrinsic-size:auto_12rem] [content-visibility:auto] focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-focus"
      >
        {/* The heading names the entry whether its editor is open or not. */}
        <h4 id={headingId} className="font-semibold break-words">
          {title}
        </h4>
        {nodeOpen !== undefined ? (
          <NodeEditor
            projectId={shared.projectId}
            node={node}
            focusOn={nodeOpen === "node:text" ? "text" : "title"}
            unsavable={
              nodeGone
                ? t("script.gone")
                : shared.editable
                  ? undefined
                  : t("script.read_only")
            }
            onClose={(moveFocus) =>
              shared.onClose(
                node.id,
                buttonId(node.id, "edit"),
                node.id,
                moveFocus,
              )
            }
          />
        ) : (
          <>
            <p className="text-sm text-fg-muted">
              {kindOf(node, {
                t,
                componentName: shared.names.componentName,
              })}
            </p>
            {element &&
              (node.text === undefined || isEmptyText(node.text) ? (
                <p className="text-fg-muted">{t("script.no_text")}</p>
              ) : (
                <div className={node.kind === "narration" ? "italic" : ""}>
                  <RichText doc={node.text} names={shared.names} />
                </div>
              ))}
            {node.type === "jumper" && (
              <p>
                <JumpTarget
                  target={targetOf(node, shared.scope, shared.targets)}
                  shared={shared}
                />
              </p>
            )}
          </>
        )}
        {choices.length > 0 && (
          <ul
            aria-label={t("script.choices_of", { node: title })}
            className="ms-4 space-y-2"
          >
            {choices.map((c) => (
              <Choice
                key={c.id}
                connection={c}
                editing={opened.includes(c.id)}
                gone={ghosts.includes(c.id)}
                shared={shared}
              />
            ))}
          </ul>
        )}
        {node.type === "branch" && (
          <ul
            aria-label={t("script.outputs_of", { node: title })}
            className="ms-4 space-y-3"
          >
            {(node.outputs ?? []).map((o) => (
              <Output
                key={o.id}
                output={o}
                leaving={leaving}
                editing={opened}
                gone={ghosts}
                shared={shared}
              />
            ))}
            {astray.map((c) => (
              <Choice
                key={c.id}
                connection={c}
                editing={opened.includes(c.id)}
                gone={ghosts.includes(c.id)}
                shared={shared}
              />
            ))}
          </ul>
        )}
        {refused !== undefined && (
          <p className="rounded-md border border-danger px-4 py-3 text-danger">
            {refused}
          </p>
        )}
        {/* The ghost of a node that the board no longer has offers
        nothing to do with it. */}
        {!nodeGone && (
          <div className="flex flex-wrap gap-2">
            {shared.editable && nodeOpen === undefined && (
              <Button
                id={buttonId(node.id, "edit")}
                onKeyDown={onceHeld}
                aria-label={t("script.edit_named", { node: title })}
                onClick={() =>
                  shared.onEdit({ kind: "node", id: node.id, focusOn: "title" })
                }
              >
                {t("script.edit")}
              </Button>
            )}
            {shared.editable && element && (
              <>
                <Button
                  id={buttonId(node.id, "after")}
                  onKeyDown={onceHeld}
                  aria-label={t("script.add_after_named", { node: title })}
                  disabled={adding}
                  onClick={() => shared.onAddAfter(node)}
                >
                  {t("script.add_after")}
                </Button>
                <Button
                  id={buttonId(node.id, "choice")}
                  onKeyDown={onceHeld}
                  aria-label={t("script.add_choice_named", { node: title })}
                  disabled={adding}
                  onClick={() => shared.onAddChoice(node)}
                >
                  {t("script.add_choice")}
                </Button>
              </>
            )}
            {/* The canvas names no view, which the link says it does not
            (explicitUndefined), so that the router does not count it a
            part of this page, of view=script, and mark it current. */}
            <RouterLink
              to="/projects/$projectId/boards/$boardId"
              params={{ projectId: shared.projectId, boardId: shared.boardId }}
              search={{ view: undefined, node: node.id }}
              activeOptions={{ explicitUndefined: true }}
              aria-label={t("script.show_on_canvas_named", { node: title })}
              className={buttonClass()}
            >
              {t("script.show_on_canvas")}
            </RouterLink>
          </div>
        )}
      </article>
    </li>
  );
}, sameEntry);

/** Reports whether an entry of a and one of b show the same: the same node, connections, editing, ghosts, additions, refusal, and names. */
function sameEntry(a: EntryProps, b: EntryProps): boolean {
  return (
    a.node === b.node &&
    a.leaving === b.leaving &&
    a.editing === b.editing &&
    a.gone === b.gone &&
    a.adding === b.adding &&
    a.refused === b.refused &&
    a.shared === b.shared
  );
}

/**
 * Reports whether e, a key that went down in an editor's form, is the
 * Escape that closes the editor, where it holds no change: one of the
 * form's own fields, rather than of a dialog of its rich text, as the
 * link's, which is in a portal, whose keys React brings here too; not one
 * that composes; and not one that the list of a { or an @ of its rich text
 * took, which closes the list alone (suggestionTook), as the list that the
 * caret opens, of no change, once it is moved after a { or an @ that the
 * text holds. It does not read whether something else took the key:
 * ProseMirror takes every Escape in its text (captureKeyDown).
 */
function leavesBy(e: KeyboardEvent<HTMLFormElement>): boolean {
  return (
    e.key === "Escape" &&
    !e.nativeEvent.isComposing &&
    !suggestionTook(e.nativeEvent) &&
    // The DOM's Node, rather than the API's of the same name, a type alone.
    e.target instanceof globalThis.Node &&
    e.currentTarget.contains(e.target)
  );
}

/**
 * Takes the repeats of a key held on a button of an entry that opens a form,
 * or adds, for nothing, as the canvas takes the repeats of a key whose
 * command runs once: the focus comes back to such a button as its form
 * closes, as of an Enter held in its title, or on its Save or its Cancel,
 * whose repeats would else open the form again, or add again.
 */
function onceHeld(e: KeyboardEvent<HTMLButtonElement>): void {
  if (e.repeat) {
    e.preventDefault();
  }
}

/** What an editor's form is of its entity's form (useEntityForm), which its life and its frame read. */
interface EditorForm {
  held: Pick<Held<unknown>, "key" | "props" | "form">;
  commands: { isPending: boolean; isSuccess: boolean };
  overlaps: Overlap["fields"];
  useTheirs: () => void;
}

/** What each editor of the view is given. */
interface EditorProps {
  projectId: string;
  /** Why what the editor edits can no longer be saved, if it cannot. */
  unsavable?: string;
  /** Closes the editor, with the focus back on its button where moveFocus says to. */
  onClose: (moveFocus: boolean) => void;
}

/**
 * Reports whether form, an editor's, has the focus, or nothing has
 * (focusIsNowhere), as once the Save that had it waits, disabled, for the
 * save under way, so that the editor that closes gives it to its button,
 * and the editor whose save is refused to its field (useFocusRefusal),
 * rather than take it from where the user went meanwhile, as another
 * editor.
 */
function focusHere(form: HTMLElement | null): boolean {
  return (
    form !== null &&
    (form.contains(form.ownerDocument.activeElement) ||
      focusIsNowhere(form.ownerDocument))
  );
}

/**
 * The selector of the box of a rich text of an editor's form: of one that
 * may be changed, and of one that the editor cannot keep as it is, as of a
 * newer version's (contenteditable="false"), which takes the focus too, to
 * be read, as a read-only field does, rather than leave the focus on the
 * page as the button that opened the form goes.
 */
const textBox = "[contenteditable]";

/**
 * The life of an editor of the view, a node's or a choice's, of form, the
 * focus in the field of focusOn, a selector, as it opens; closing once its
 * save applies; closing, where it can no longer be saved, as unsavable
 * says, of nothing that the user changed, as changed reads it, as the
 * canvas's editor of a text closes; either with the focus back on its
 * button where the form had it, or nothing had (focusHere), rather than
 * taken from where the user is; and it returns what closes it on Escape,
 * of one that holds no change (leavesBy).
 */
function useEditorLife(
  form: EditorForm,
  {
    focusOn,
    unsavable,
    changed,
    onClose,
  }: Pick<EditorProps, "unsavable" | "onClose"> & {
    focusOn: string;
    changed: () => boolean;
  },
): (e: KeyboardEvent<HTMLFormElement>) => void {
  const within = form.held.form;
  const pending = form.commands.isPending;
  // biome-ignore lint/correctness/useExhaustiveDependencies: the focus is given as the editor opens alone.
  useEffect(() => {
    within.current?.querySelector<HTMLElement>(focusOn)?.focus();
  }, []);
  const saved = form.commands.isSuccess;
  // biome-ignore lint/correctness/useExhaustiveDependencies: within is the form's ref, which is read where the focus is as the save applies.
  useEffect(() => {
    if (saved) {
      onClose(focusHere(within.current));
    }
  }, [saved, onClose]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: changed reads what the editor holds as it can no longer be saved, or the save that was under way settles.
  useEffect(() => {
    if (unsavable !== undefined && !pending && !changed()) {
      onClose(focusHere(within.current));
    }
  }, [unsavable, pending, onClose]);
  return (e) => {
    if (leavesBy(e) && !pending && !changed()) {
      e.preventDefault();
      onClose(true);
    }
  };
}

/**
 * The form of an editor of the view, named label, of form: why it can no
 * longer be saved, as unsavable says, or why its save was refused, refusal,
 * and the fields of a refusal for a change made elsewhere (ConflictNotice,
 * of fieldName and shownValue); its fields, children; and its Save, which
 * submit saves by, and Cancel, which wait while a save is under way, and
 * Save while it can no longer be saved.
 */
function EditorFrame({
  form,
  label,
  unsavable,
  refusal,
  fieldName,
  shownValue,
  submit,
  onKeyDown,
  onClose,
  children,
}: Pick<EditorProps, "unsavable" | "onClose"> & {
  form: EditorForm;
  label: string;
  refusal: string | undefined;
  fieldName: (field: string) => string;
  shownValue: (field: string, value: unknown) => string | undefined;
  submit: () => void;
  onKeyDown: (e: KeyboardEvent<HTMLFormElement>) => void;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const { held } = form;
  const pending = form.commands.isPending;
  // A key held as the form opened, as the Enter on the button that opened
  // it, repeats into nothing until it goes up, as in the canvas's editor of
  // a text, rather than submit the form, or add a paragraph for each, which
  // the user never typed (useOwnKeys).
  const ownKeys = useOwnKeys();
  return (
    <form
      key={held.key}
      {...held.props}
      onSubmit={(event) => {
        event.preventDefault();
        if (!pending && unsavable === undefined) {
          submit();
        }
      }}
      {...ownKeys}
      onKeyDown={onKeyDown}
      noValidate
      aria-label={label}
      className="space-y-3"
    >
      <Alert message={unsavable} />
      <Alert message={form.overlaps.length > 0 ? undefined : refusal} />
      <ConflictNotice
        form={form}
        fieldName={fieldName}
        shownValue={shownValue}
      />
      {children}
      <div className="flex flex-wrap gap-2">
        <Button
          type="submit"
          variant="primary"
          disabled={pending || unsavable !== undefined}
        >
          {t("script.save")}
        </Button>
        <Button disabled={pending} onClick={() => onClose(true)}>
          {t("script.cancel")}
        </Button>
      </div>
    </form>
  );
}

const nodeField = payloadField("title", "text");

/**
 * The form of a node's title and an element's text, in its entry, which
 * Save sends as one node.update of what changed, as the canvas's inspector
 * and its editor of a text send them, expecting the revision that it shows;
 * it closes once the save applies, or nothing changed. Escape closes one
 * that holds nothing that the user changed. Where the node can no longer be
 * saved, as unsavable says, it keeps what the user typed, saying why, until
 * the user cancels it, and closes of nothing typed (useEditorLife).
 */
function NodeEditor({
  projectId,
  node,
  focusOn,
  unsavable,
  onClose,
}: EditorProps & { node: Node; focusOn: "title" | "text" }) {
  const { t } = useTranslation();
  const form = useEntityForm(projectId, node, (view, id) => view.nodes.get(id));
  const { commands, held, shown, against } = form;
  const text = useRef<{ key: string; value: RichTextValue }>(undefined);
  const [invalid, setInvalid] = useState<{ text?: string }>();
  const refusal = refusalOf(commands.error, t, nodeField);
  useFocusRefusal(held.form, invalid ?? commands.error, focusHere);
  const names = useTextNames(projectId, node.board_id);
  const choices = useTextChoices(projectId, node.board_id);
  const element = shown.type === "element";
  const pending = commands.isPending;
  const title = titleOf(shown, t);
  const typed = () =>
    text.current?.key === held.key ? text.current.value : undefined;
  const currentText = () => typed()?.doc ?? shown.text;
  const typedTitle = () =>
    String(
      held.form.current?.querySelector<HTMLInputElement>('input[name="title"]')
        ?.value ?? shown.title,
    ).trim();
  const changed = () =>
    typedTitle() !== against.title ||
    (element &&
      (typed()?.error !== undefined || !equal(currentText(), against.text)));
  // The field that the editor opens with the focus in: the title, or, of a
  // node that the view has just added, its text.
  const keyDown = useEditorLife(form, {
    focusOn: focusOn === "text" && element ? textBox : 'input[name="title"]',
    unsavable,
    changed,
    onClose,
  });

  const submit = () => {
    if (element && typed()?.error !== undefined) {
      form.refused();
      setInvalid({ text: t("inspector.error.text") });
      return;
    }
    setInvalid(undefined);
    const update: { title?: string; text?: Node["text"] } = {};
    const newTitle = typedTitle();
    if (newTitle !== against.title) {
      update.title = newTitle;
    }
    const newText = currentText();
    if (element && newText !== undefined && !equal(newText, against.text)) {
      update.text = newText;
    }
    if (Object.keys(update).length === 0) {
      onClose(true);
      return;
    }
    form.save(
      [
        {
          type: "node.update",
          payload: { id: shown.id, ...update },
          expected_rev: { [against.id]: against.rev },
        },
      ],
      update,
    );
  };
  return (
    <EditorFrame
      form={form}
      label={t("script.node_editor", { node: title })}
      unsavable={unsavable}
      refusal={refusal?.message}
      fieldName={(field) =>
        field === "title" ? t("inspector.node_title") : t("inspector.text")
      }
      shownValue={(field, value) =>
        field === "title" ? String(value ?? "") : undefined
      }
      submit={submit}
      onKeyDown={keyDown}
      onClose={onClose}
    >
      <TextField
        label={t("inspector.node_title")}
        name="title"
        autoComplete="off"
        maxLength={200}
        defaultValue={shown.title}
        // What the user types while the save is under way would be lost as
        // the editor closes once it applies; read-only keeps the focus,
        // which a disabled field would drop.
        readOnly={pending}
        error={refusal?.fields.title}
      />
      {element && (
        <RichTextEditor
          label={t("canvas.text_of", { node: title })}
          defaultValue={shown.text}
          error={invalid?.text ?? refusal?.fields.text}
          names={names}
          choices={choices}
          editable={!pending}
          onChange={(value) => {
            text.current = { key: held.key, value };
            held.props.onChange();
          }}
        />
      )}
    </EditorFrame>
  );
}

const labelField = payloadField("label");

/**
 * The form of a choice's label, in its node's entry, which Save sends as
 * one connection.update, as the connection's inspector sends it, expecting
 * the revision that it shows; it closes once the save applies, or nothing
 * changed, and Escape closes one whose label is as it was. Where the
 * connection can no longer be saved, it keeps what the user typed, as the
 * node's editor does.
 */
function ChoiceEditor({
  projectId,
  connection,
  unsavable,
  onClose,
}: EditorProps & { connection: Connection }) {
  const { t } = useTranslation();
  const form = useEntityForm(projectId, connection, (view, id) =>
    view.connections.get(id),
  );
  const { commands, held, shown, against } = form;
  const label = useRef<{ key: string; value: RichTextValue }>(undefined);
  const [invalid, setInvalid] = useState<{ label: string }>();
  const refusal = refusalOf(commands.error, t, labelField);
  useFocusRefusal(held.form, invalid ?? commands.error, focusHere);
  const names = useTextNames(projectId, connection.board_id);
  const choices = useTextChoices(projectId, connection.board_id);
  const pending = commands.isPending;
  const typed = () =>
    label.current?.key === held.key ? label.current.value : undefined;
  const current = () => typed()?.doc ?? shown.label;
  const changed = () =>
    typed()?.error !== undefined || !equal(current(), against.label);
  const keyDown = useEditorLife(form, {
    focusOn: textBox,
    unsavable,
    changed,
    onClose,
  });

  const submit = () => {
    if (typed()?.error !== undefined) {
      form.refused();
      setInvalid({ label: t("inspector.error.text") });
      return;
    }
    setInvalid(undefined);
    const value = current();
    if (equal(value, against.label)) {
      onClose(true);
      return;
    }
    form.save(
      [
        {
          type: "connection.update",
          payload: { id: shown.id, label: value },
          expected_rev: { [against.id]: against.rev },
        },
      ],
      { label: value },
    );
  };
  return (
    <EditorFrame
      form={form}
      label={t("script.choice_editor")}
      unsavable={unsavable}
      refusal={refusal?.message}
      fieldName={() => t("inspector.label")}
      shownValue={() => undefined}
      submit={submit}
      onKeyDown={keyDown}
      onClose={onClose}
    >
      <RichTextEditor
        label={t("inspector.label")}
        defaultValue={shown.label}
        error={invalid?.label ?? refusal?.fields.label}
        names={names}
        choices={choices}
        editable={!pending}
        onChange={(value) => {
          label.current = { key: held.key, value };
          held.props.onChange();
        }}
      />
    </EditorFrame>
  );
}
