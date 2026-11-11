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
// applies to a board that it does not hold; what it read before is what the
// target is until the read again answers.

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
  /**
   * Tells that the document has read the content of a board since, as the
   * page asked it to, which changes nothing of the document where the board
   * lacks a target, of which boardRead then tells otherwise: the canvas
   * writes its jumpers' cards again.
   */
  boardWasRead?(): void;
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

/** Reports whether a and b, what two reads of a target found, are the same. */
function sameRead(
  a: TargetRead | "failed" | undefined,
  b: TargetRead | "failed",
): boolean {
  return typeof a === "object" && typeof b === "object"
    ? a.title === b.title && a.boardId === b.boardId
    : a === b;
}

/**
 * The reads of the targets that the document does not tell of, each by its
 * node's ID, a few at a time: read returns what a read found, and asks for
 * one where none has been made, and changed is called, a moment after, once
 * answers came that found another thing than the reads before them. A read
 * that failed, as of a server that could not be reached, is not made again
 * until the reads are asked for again (readAgain), as once the page is
 * shown again; what a read found before stays what the target is until its
 * next read answers, and where that one fails.
 */
export class TargetReads {
  private readonly results = new Map<string, TargetRead | "failed">();
  /** The nodes whose read was asked for, under way or answered, since the reads were last asked for again (readAgain). */
  private readonly asked = new Set<string>();
  private queue: string[] = [];
  private active = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  /** Whether the reads are stopped, as they are until the page starts them (start), and once it has gone (stop). */
  private stopped = true;

  constructor(
    /** Reads the node of id, and resolves with it, or with undefined for one that the project does not have, or has in its trash. */
    private readonly readNode: (id: string) => Promise<Node | undefined>,
    /** Tells that what read returns changed, or that it is to be asked for again (readAgain). */
    private readonly changed: () => void,
  ) {}

  /** Returns what the last read of the node of id that answered found, or undefined while none has, and asks for a read where none was asked for since the reads were last asked for again. */
  read(id: string): TargetRead | undefined {
    this.ask(id);
    return this.peek(id);
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
            const before = this.results.get(id);
            if (result !== "failed") {
              this.results.set(id, result);
              if (!sameRead(before, result)) {
                this.tell();
              }
            } else if (before === undefined) {
              // A read again that failed leaves what the read before it
              // found, rather than take a card that says so back to one
              // that knows nothing of its target.
              this.results.set(id, result);
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
   * Has each target that was read, or failed to be, read again as it is
   * next asked for, and tells of it, so that the canvas, which asks as it
   * writes its cards, asks; what the reads found stays what read returns
   * until the new answers come, so that a jumper drawn as broken is not
   * drawn as whole meanwhile, nor one named by its target's title by its
   * board alone. A read under way is as good as one made anew, and stays.
   */
  readAgain(): void {
    if (this.results.size === 0) {
      return;
    }
    for (const id of this.results.keys()) {
      this.asked.delete(id);
    }
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
