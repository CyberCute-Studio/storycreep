// What a jumper's target is to the canvas (PRD FR-EDIT-05, plan note N69):
// the node that it goes to, with its title and its board, which the
// jumper's card shows and its link opens; none; or gone, as a node that
// was deleted, or purged, with or without its board, whose jumper the
// canvas draws as broken (AC1). The project's document tells of a target
// that it holds, and of one that it would hold if the project had it: a
// node of a board whose content it has read, or of a board that the tree of
// boards, which it has read, lacks, as one in the trash. Of any other, as a
// node of a board that no page has opened, it tells nothing, and the canvas
// reads the node by itself (TargetReads), once, as its jumper's card is
// first drawn, and again once the page is shown again, as what another
// session did to it meanwhile comes by no operation that the document
// applies to a board that it does not hold.

import type { Node } from "@storycreep/shared/api";
import type { Doc } from "../document/doc";

/** What a jumper's target is, as far as the page can tell. */
export type JumperTarget =
  /** The jumper has none. */
  | { state: "none" }
  /** The node of nodeId, of the board of boardId, which is another board than the jumper's where elsewhere says so, titled title. */
  | {
      state: "found";
      nodeId: string;
      boardId: string;
      title: string;
      elsewhere: boolean;
    }
  /** A node that the page has not read yet, or could not, of the board of boardId. */
  | { state: "unknown"; nodeId: string; boardId: string | undefined }
  /** A node that the project no longer has but in its trash, if there. */
  | { state: "gone"; nodeId: string };

/** What a read of a target by itself found: the node's title and its board, or that the project does not have it. */
export type TargetRead = { title: string; boardId: string } | "gone";

/** What tells of a target that the document's entities do not: what the document has read, and what the canvas read of the target itself. */
export interface TargetLookup {
  /** Reports whether the document has read the content of the board of boardId, and holds it, so that a node of it that it lacks is gone. */
  boardRead(boardId: string): boolean;
  /** Reports whether the document has read the tree of the project's boards, and holds it, so that a board that it lacks is gone. */
  treeRead(): boolean;
  /** Returns what a read of the node of nodeId by itself found, or undefined while none has, which asks for one. */
  read(nodeId: string): TargetRead | undefined;
}

/**
 * Returns what the target of node, a jumper, is, as view, the project's
 * document, and lookup tell; without lookup, a target that the document
 * does not hold is unknown.
 */
export function targetOf(
  node: Pick<Node, "board_id" | "target">,
  view: Pick<Doc, "nodes" | "boards">,
  lookup?: TargetLookup,
): JumperTarget {
  const nodeId = node.target?.node_id;
  if (nodeId === undefined || nodeId === "") {
    return { state: "none" };
  }
  const boardId = node.target?.board_id;
  const held = view.nodes.get(nodeId);
  if (held !== undefined) {
    return held.deleted_at === undefined
      ? {
          state: "found",
          nodeId,
          boardId: held.board_id,
          title: held.title,
          elsewhere: held.board_id !== node.board_id,
        }
      : { state: "gone", nodeId };
  }
  if (lookup === undefined) {
    return { state: "unknown", nodeId, boardId };
  }
  if (
    boardId !== undefined &&
    (lookup.boardRead(boardId) ||
      (lookup.treeRead() && !view.boards.has(boardId)))
  ) {
    return { state: "gone", nodeId };
  }
  const read = lookup.read(nodeId);
  if (read === undefined) {
    return { state: "unknown", nodeId, boardId };
  }
  return read === "gone"
    ? { state: "gone", nodeId }
    : {
        state: "found",
        nodeId,
        boardId: read.boardId,
        title: read.title,
        elsewhere: read.boardId !== node.board_id,
      };
}

/** How many targets are read at once. */
const readsAtOnce = 4;

/** How long the reads' answers are gathered before the canvas is told of them, in milliseconds, so that those of a view's jumpers write its cards again once, rather than each in turn. */
const tellDelay = 50;

/**
 * The reads of the targets that the document does not tell of, each by its
 * node's ID, a few at a time: read returns what a read found, and asks for
 * one where none has been made, and changed is called, a moment after, once
 * answers came. A read that failed, as of a server that could not be
 * reached, is not made again until the reads are forgotten (forget), as
 * once the page is shown again.
 */
export class TargetReads {
  private readonly results = new Map<string, TargetRead | "failed">();
  /** The nodes whose read was asked for, under way or answered. */
  private readonly asked = new Set<string>();
  private queue: string[] = [];
  private active = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  /** Whether the reads are stopped, as they are until the page starts them (start), and once it has gone (stop). */
  private stopped = true;

  constructor(
    /** Reads the node of id, and resolves with it, or with undefined for one that the project does not have, or has in its trash. */
    private readonly readNode: (id: string) => Promise<Node | undefined>,
    /** Tells that what read returns changed. */
    private readonly changed: () => void,
  ) {}

  /** Returns what the read of the node of id found, or undefined while none has, which asks for one. */
  read(id: string): TargetRead | undefined {
    const result = this.results.get(id);
    if (result === undefined) {
      this.ask(id);
      return undefined;
    }
    return result === "failed" ? undefined : result;
  }

  /** Returns what a read of the node of id found, as read does, without asking for one. */
  peek(id: string): TargetRead | undefined {
    const result = this.results.get(id);
    return result === "failed" ? undefined : result;
  }

  private ask(id: string): void {
    if (this.stopped || this.asked.has(id)) {
      return;
    }
    this.asked.add(id);
    this.queue.push(id);
    this.pump();
  }

  private pump(): void {
    while (this.active < readsAtOnce && this.queue.length > 0) {
      const id = this.queue.shift() as string;
      this.active++;
      this.readNode(id)
        .then(
          (node): TargetRead | "failed" =>
            node === undefined
              ? "gone"
              : { title: node.title, boardId: node.board_id },
          (): "failed" => "failed",
        )
        .then((result) => {
          this.active--;
          if (!this.stopped) {
            this.results.set(id, result);
            if (result !== "failed") {
              this.tell();
            }
          }
          this.pump();
        });
    }
  }

  private tell(): void {
    if (this.timer !== undefined) {
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = undefined;
      if (!this.stopped) {
        this.changed();
      }
    }, tellDelay);
  }

  /**
   * Forgets what the reads found, so that each target is read again as it
   * is next asked for, and tells that it changed, if they found anything; a
   * read under way is as good as one made anew, and stays.
   */
  forget(): void {
    if (this.results.size === 0) {
      return;
    }
    for (const id of this.results.keys()) {
      this.asked.delete(id);
    }
    this.results.clear();
    if (!this.stopped) {
      this.changed();
    }
  }

  /** Starts the reads, as the page that shows what they find is shown. */
  start(): void {
    this.stopped = false;
  }

  /**
   * Ends the reads, until they are started again: none is made after, and
   * nothing is told of. Those asked for and not made yet may be asked for
   * again.
   */
  stop(): void {
    this.stopped = true;
    for (const id of this.queue) {
      this.asked.delete(id);
    }
    this.queue = [];
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }
}
