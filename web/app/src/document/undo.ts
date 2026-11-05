// The page's undo and redo of its own batches of a project (PRD FR-EDIT-08):
// each step is the undo that the server answered a batch with (FR-API-03),
// the commands that set back what the batch changed, each with the
// revisions that it expects, which the page sends back as a batch of its
// own, and whose answer holds the undo of that batch, the step that redoes
// it. The steps are the user's, of one project, and the history keeps at
// least the last 200 of them (depth), in the tab's session storage, so that
// a reload keeps them, as many as fit, and at least the last 50 if they fit
// (AC3).
//
// A step that applies brings each entity that the undone batch changed back
// to what it was, at a new revision, so the history moves what its other
// steps expect of the entity, from the revision that it had before the
// undone batch, which the store held of it once it had every operation
// before the batch's seq (captured), to the revision that the answer gives
// (move), as the server's undo of a batch expects what the batch left.

import type { UndoCommandBody } from "@storycreep/shared/api";

/** A step of the history: the commands that undo a batch, or redo one. */
export interface Step {
  /** The commands, in the order to send them, each with what it expects. */
  commands: UndoCommandBody[];
  /**
   * The seqs of the batches that the step undoes, the first first: one, or
   * several for a step whose batch was sent in parts, as an undo too large
   * for one request.
   */
  seqs: number[];
}

/**
 * The revisions that the store held of the entities that a batch changed,
 * once it had every operation before the batch: an entity's revision, or
 * null for one that it did not have, as one that the batch created.
 */
export type Captured = Record<string, number | null>;

/** How many steps the history keeps at least (FR-EDIT-08). */
export const depth = 200;

/** How many of the latest steps a reload keeps at least, where they fit in the tab's storage (FR-EDIT-08 AC3). */
export const keptOnReload = 50;

/** What the history keeps in the tab's storage. */
interface Saved {
  v: 1;
  undo: Step[];
  redo: Step[];
  captured: Record<string, Captured>;
}

/** The key of the tab's storage that the history of a user's project is kept under. */
export function historyKey(userId: string, projectId: string): string {
  return `storycreep.undo.${userId}.${projectId}`;
}

/** The prefix of the keys of every history, which signing out clears. */
export const historyPrefix = "storycreep.undo.";

/** Takes every history out of storage, as once the page has signed out. */
export function clearHistories(storage: Storage | undefined): void {
  if (storage === undefined) {
    return;
  }
  try {
    const keys: string[] = [];
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (key?.startsWith(historyPrefix)) {
        keys.push(key);
      }
    }
    for (const key of keys) {
      storage.removeItem(key);
    }
  } catch {
    // A storage that the browser refuses has nothing to clear.
  }
}

/** Returns command with each revision that it expects of an entity of revisions moved there. */
export function withRevisions(
  command: UndoCommandBody,
  revisions: ReadonlyMap<string, number>,
): UndoCommandBody {
  let expected: Record<string, number> | undefined;
  for (const id of Object.keys(command.expected_rev)) {
    const rev = revisions.get(id);
    if (rev !== undefined && rev !== command.expected_rev[id]) {
      expected ??= { ...command.expected_rev };
      expected[id] = rev;
    }
  }
  return expected === undefined
    ? command
    : { ...command, expected_rev: expected };
}

/**
 * Returns the step that redoes a batch sent in parts, from the undos that
 * their answers held, the first part's first: the last part's undo first,
 * since it undoes what came last, each command expecting what the last of
 * the parts that changed an entity left of it, latest, as the undo of one
 * batch of them all would.
 */
export function joinedUndo(
  undos: readonly (readonly UndoCommandBody[])[],
  latest: ReadonlyMap<string, number>,
): UndoCommandBody[] {
  return undos
    .toReversed()
    .flat()
    .map((command) => withRevisions(command, latest));
}

/** The history of a user's project. */
export class History {
  private undoSteps: Step[] = [];
  private redoSteps: Step[] = [];
  private captured = new Map<number, Captured>();
  private readonly storage: Storage | undefined;
  private readonly key: string | undefined;
  /** Whether the history no longer keeps itself in the tab's storage (close). */
  private closed = false;

  /**
   * Returns the history kept under key in storage, or a new one, which it
   * keeps there from then on; with no key, as before the page knows its
   * user, it keeps none.
   */
  constructor(storage: Storage | undefined, key: string | undefined) {
    this.storage = storage;
    this.key = key;
    if (storage === undefined || key === undefined) {
      return;
    }
    try {
      const text = storage.getItem(key);
      if (text === null) {
        return;
      }
      const saved = JSON.parse(text) as Saved;
      if (saved.v !== 1) {
        return;
      }
      this.undoSteps = saved.undo;
      this.redoSteps = saved.redo;
      for (const [seq, revs] of Object.entries(saved.captured)) {
        this.captured.set(Number(seq), revs);
      }
    } catch {
      // A history that cannot be read, as one that another version of the
      // app wrote, is none.
    }
  }

  /** How many steps can be undone. */
  get undoable(): number {
    return this.undoSteps.length;
  }

  /** How many steps can be redone. */
  get redoable(): number {
    return this.redoSteps.length;
  }

  /** Returns the step that undo would send, the latest. */
  nextUndo(): Step | undefined {
    return this.undoSteps.at(-1);
  }

  /** Returns the step that redo would send, the latest that was undone. */
  nextRedo(): Step | undefined {
    return this.redoSteps.at(-1);
  }

  /**
   * Records that the user's batch applied, with the step that undoes it: a
   * new change of the project, after which nothing undone can be redone. A
   * batch that cannot be undone, whose step is undefined, still ends what
   * could be redone, which may undo what it changed.
   */
  done(step: Step | undefined): void {
    this.redoSteps = [];
    if (step !== undefined) {
      this.push(this.undoSteps, step);
    }
    this.save();
  }

  /**
   * Takes the next step to undo out of the history, once it has applied, and
   * records the step that redoes it, if any; a step sent in parts of which
   * some applied is left in its place as rest, what did not.
   */
  undone(step: Step, redo: Step | undefined, rest?: Step): void {
    this.take(this.undoSteps, step, rest);
    if (redo !== undefined) {
      this.push(this.redoSteps, redo);
    }
    this.save();
  }

  /** Takes the next step to redo out of the history, once it has applied, as undone does, and records the step that undoes it again, if any. */
  redone(step: Step, undo: Step | undefined, rest?: Step): void {
    this.take(this.redoSteps, step, rest);
    if (undo !== undefined) {
      this.push(this.undoSteps, undo);
    }
    this.save();
  }

  /** Takes step out of the history, as one refused for what changed since, which it would be again. */
  drop(step: Step): void {
    this.take(this.undoSteps, step);
    this.take(this.redoSteps, step);
    this.save();
  }

  /** Records what the store held of the entities that the batch of seq changed, once it had every operation before it. */
  capture(seq: number, revs: Captured): void {
    this.captured.set(seq, revs);
    this.save();
  }

  /** Reports whether the history wants to know what the batch of seq changed (capture): whether one of its steps undoes it. */
  wants(seq: number): boolean {
    return (
      !this.captured.has(seq) &&
      [...this.undoSteps, ...this.redoSteps].some((s) => s.seqs.includes(seq))
    );
  }

  /**
   * Returns the revisions that the store held of the entities that step's
   * batches changed before them, an entity's from the first of them that
   * changed it, as far as the history captured them.
   */
  before(step: Step): Map<string, number | null> {
    const before = new Map<string, number | null>();
    for (const seq of step.seqs) {
      for (const [id, rev] of Object.entries(this.captured.get(seq) ?? {})) {
        if (!before.has(id)) {
          before.set(id, rev);
        }
      }
    }
    return before;
  }

  /**
   * Moves what every step of the history expects of each entity of moves
   * that a step brought back, by its ID, from before, the revision that it
   * had before the batch that the step undid, to after, the one that the
   * step's answer gives, all at once, so that the history is kept in the
   * tab's storage once, rather than once for each entity of a step that
   * brought back hundreds.
   */
  move(
    moves: readonly (readonly [id: string, before: number, after: number])[],
  ): void {
    if (moves.length === 0) {
      return;
    }
    const movedCommand = (command: UndoCommandBody): UndoCommandBody => {
      let expected: Record<string, number> | undefined;
      for (const [id, before, after] of moves) {
        if (command.expected_rev[id] === before) {
          expected ??= { ...command.expected_rev };
          expected[id] = after;
        }
      }
      return expected === undefined
        ? command
        : { ...command, expected_rev: expected };
    };
    const moved = (steps: Step[]) =>
      steps.map((step) => ({
        ...step,
        commands: step.commands.map(movedCommand),
      }));
    this.undoSteps = moved(this.undoSteps);
    this.redoSteps = moved(this.redoSteps);
    this.save();
  }

  /**
   * Records that the parts of a step sent in parts that applied left the
   * entities of revisions at those revisions, so that rest, the step kept in
   * its place, moves what the other steps expect of each from there once it
   * applies, as those parts moved them there (move), rather than from what
   * the store held of it before the batch that the step undoes.
   */
  advance(rest: Step, revisions: ReadonlyMap<string, number>): void {
    for (const seq of rest.seqs) {
      const captured = this.captured.get(seq);
      if (captured === undefined) {
        continue;
      }
      const next = { ...captured };
      for (const [id, rev] of revisions) {
        if (typeof next[id] === "number") {
          next[id] = rev;
        }
      }
      this.captured.set(seq, next);
    }
    this.save();
  }

  /**
   * Stops keeping the history in the tab's storage, as once its document is
   * dropped: what it records after, as the answer to a batch that comes once
   * the page has signed out, is not written there.
   */
  close(): void {
    this.closed = true;
  }

  private push(steps: Step[], step: Step): void {
    steps.push(step);
    if (steps.length > depth) {
      steps.splice(0, steps.length - depth);
    }
    this.forget();
  }

  /**
   * Takes step out of steps, found by its batches' seqs, which no other step
   * has, since a step that moved (move) is another object, or puts rest in
   * its place.
   */
  private take(steps: Step[], step: Step, rest?: Step): void {
    const i = steps.findLastIndex((s) => s.seqs[0] === step.seqs[0]);
    if (i >= 0) {
      steps.splice(i, 1, ...(rest === undefined ? [] : [rest]));
    }
    this.forget();
  }

  /** Forgets what it captured of a batch that no step undoes any longer. */
  private forget(): void {
    const seqs = new Set(
      [...this.undoSteps, ...this.redoSteps].flatMap((s) => s.seqs),
    );
    for (const seq of this.captured.keys()) {
      if (!seqs.has(seq)) {
        this.captured.delete(seq);
      }
    }
  }

  /**
   * Keeps the history in the tab's storage: the latest steps that fit, the
   * oldest steps to undo left out first, and then the oldest to redo, which
   * come after the latest change. Where the storage is full, how many are
   * left out is found by halving, so that a long history is written a few
   * times, rather than once for each step that is left out.
   */
  private save(): void {
    if (this.closed || this.storage === undefined || this.key === undefined) {
      return;
    }
    if (this.write(this.storage, this.key, 0)) {
      return;
    }
    let fits = this.undoSteps.length + this.redoSteps.length;
    if (!this.write(this.storage, this.key, fits)) {
      try {
        this.storage.removeItem(this.key);
      } catch {
        // Nothing to keep, and nothing to take out.
      }
      return;
    }
    // A write that fails leaves what the storage had, so it holds the last
    // that fit, of the fewest steps left out.
    let fails = 0;
    while (fits - fails > 1) {
      const half = Math.floor((fits + fails) / 2);
      if (this.write(this.storage, this.key, half)) {
        fits = half;
      } else {
        fails = half;
      }
    }
  }

  /** Writes the history under key in storage, without its oldest steps, as many as left, and reports whether it fit. */
  private write(storage: Storage, key: string, left: number): boolean {
    const undo = this.undoSteps.slice(Math.min(left, this.undoSteps.length));
    const redo = this.redoSteps.slice(
      Math.max(0, left - this.undoSteps.length),
    );
    const seqs = new Set([...undo, ...redo].flatMap((s) => s.seqs));
    const captured: Record<string, Captured> = {};
    for (const [seq, revs] of this.captured) {
      if (seqs.has(seq)) {
        captured[seq] = revs;
      }
    }
    const saved: Saved = { v: 1, undo, redo, captured };
    try {
      storage.setItem(key, JSON.stringify(saved));
      return true;
    } catch {
      // The storage is full.
      return false;
    }
  }
}
