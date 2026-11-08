// The page's undo and redo of its own batches of a project (PRD FR-EDIT-08):
// each step is the undo that the server answered a batch with (FR-API-03),
// the commands that set back what the batch changed, each with the
// revisions that it expects, which the page sends back as a batch of its
// own, and whose answer holds the undo of that batch, the step that redoes
// it. The steps are the user's, of one project, and the history keeps at
// least the last 200 of them (depth), in the tab's session storage, so that
// a reload keeps the latest of them that fit, the oldest left out first
// (save), and so at least the last 50 wherever 50 fit (AC3).
//
// A step that applies brings each entity that the undone batch changed back
// to what it was, at a new revision, so the history moves what its other
// steps expect of the entity, from the revision that it had before the
// undone batch, which the store held of it once it had every operation
// before the batch's seq (captured), to the revision that the answer gives
// (move), as the server's undo of a batch expects what the batch left.

import type { UndoCommandBody } from "@storycreep/shared/api";
import { formatUuid, newUuid } from "@storycreep/shared/ids";

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

/** What the history keeps in the tab's storage. */
interface Saved {
  v: 1;
  /** The history that wrote it, first after v, as clear finds it without reading the rest (History.writer). */
  w?: string;
  undo: Step[];
  redo: Step[];
  captured: Record<string, Captured>;
}

/** Returns the history that wrote text, a value of the tab's storage, or null for none, as of text that is none, or that no history wrote (Saved.w). */
function writerOf(text: string | null): string | null {
  return text === null
    ? null
    : (/^\{"v":1,"w":"([0-9a-f-]+)"/.exec(text)?.[1] ?? null);
}

/** Reports whether value is an object, as JSON reads one, rather than null or a list. */
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Reports whether value is a revision, or a seq, as the server gives them. */
function isRevision(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

/**
 * Reports whether value is a list of steps, each with its commands, of a
 * type and with the revisions that each expects, and the seqs of one batch
 * at least, as the history wrote them: one that another version of the app
 * wrote otherwise, as with a seq that is no number, would wait for an
 * operation that never comes, and move what nothing expects (move).
 */
function isSteps(value: unknown): value is Step[] {
  return (
    Array.isArray(value) &&
    value.every(
      (step) =>
        isObject(step) &&
        Array.isArray(step.commands) &&
        step.commands.every(
          (command: unknown) =>
            isObject(command) &&
            typeof command.type === "string" &&
            isObject(command.expected_rev) &&
            Object.values(command.expected_rev).every(isRevision),
        ) &&
        Array.isArray(step.seqs) &&
        step.seqs.length > 0 &&
        step.seqs.every(isRevision),
    )
  );
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
  /** How often the steps changed (version). */
  private changes = 0;
  private captured = new Map<number, Captured>();
  /**
   * The seqs of the batches that the steps undo or redo, which the history
   * finds anew once its steps change (forget), rather than look for in every
   * step for each operation that the store applies (wants).
   */
  private seqs = new Set<number>();
  private readonly storage: Storage | undefined;
  private readonly key: string | undefined;
  /** Whether the history no longer keeps itself in the tab's storage (close). */
  private closed = false;
  /** Whether the history is to be written to the tab's storage, once what changes it at once has (save). */
  private unsaved = false;
  /**
   * Each step, and each batch's capture, as JSON, which the history writes
   * to the tab's storage once for each change, a few times where it does
   * not fit, but serialises once: a step that moves is another object, and
   * one that does not the same (move), as a capture that changes is another
   * (advance).
   */
  private readonly json = new WeakMap<object, string>();
  /**
   * How many of the oldest steps the last write that fit left out (write),
   * from which the next looks for how many to leave out, as a history that
   * fills the storage leaves out about as many with each change, rather
   * than halve its way there anew, writing its whole text each time.
   */
  private left = 0;
  /**
   * What tells the history's writes to the tab's storage apart from those
   * of another history of the same key, as the project's next document's,
   * which it writes with them (Saved.w), rather than keep their text, which
   * may be megabytes, to compare with what the storage holds (clear).
   */
  private readonly writer = formatUuid(newUuid());
  /**
   * The history that wrote what the tab's storage held under key as this
   * one last read it or wrote it there, or null for none, as of nothing
   * (writerOf), which a history that no longer keeps itself there takes
   * out only while the storage holds what that one wrote still (clear),
   * rather than what the project's next document, which reads the same
   * key, has written there since.
   */
  private stored: string | null | undefined;

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
      this.stored = writerOf(text);
      if (text === null) {
        return;
      }
      // A history that cannot be read, as one that another version of the
      // app wrote, is none, rather than a part of one.
      const saved = JSON.parse(text) as Partial<Saved> | null;
      if (
        saved?.v !== 1 ||
        !isSteps(saved.undo) ||
        !isSteps(saved.redo) ||
        !isObject(saved.captured)
      ) {
        return;
      }
      const captured = new Map<number, Captured>();
      for (const [seq, revs] of Object.entries(saved.captured)) {
        if (
          !isRevision(Number(seq)) ||
          !isObject(revs) ||
          !Object.values(revs).every((rev) => rev === null || isRevision(rev))
        ) {
          return;
        }
        captured.set(Number(seq), revs as Captured);
      }
      this.undoSteps = saved.undo;
      this.redoSteps = saved.redo;
      this.captured = captured;
      this.forget();
    } catch {
      // A history that is no JSON is none.
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

  /**
   * Counts the changes of the history's steps, as one joins, leaves, or is
   * dropped, which how many there are does not tell once the history holds
   * as many as it keeps (depth), where one that joins takes the oldest out.
   */
  get version(): number {
    return this.changes;
  }

  /**
   * The latest seq of the batches that the steps undo or redo, or 0 for
   * none: one that the server had, as it answered the batch, which it has
   * still, but for a database restored from an older backup.
   */
  get latest(): number {
    let latest = 0;
    for (const seq of this.seqs) {
      latest = Math.max(latest, seq);
    }
    return latest;
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
    if (this.redoSteps.length > 0) {
      this.redoSteps = [];
      this.changes++;
    }
    if (step !== undefined) {
      this.push(this.undoSteps, step);
    } else {
      this.forget();
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

  /**
   * Takes every step out of the history, as once the project's seqs and
   * revisions that they wait for and expect are of changes that the server
   * no longer has, as of a database restored from an older backup, and out
   * of the tab's storage, even once the history no longer keeps itself
   * there (close), as of a document dropped while the answer or the read
   * that tells of the restore was under way, whose steps kept there the
   * project's next document would read: while the storage holds what the
   * history last read or wrote there still (stored), and not once it holds
   * another history, which the project's next document, which reads and
   * writes the same key, wrote since, as with steps of the user's changes
   * since it found the restore itself, which a reload would otherwise lose
   * (FR-EDIT-08 AC3), however the document was dropped, and whatever was
   * under way. The steps that the project's next document has read from
   * there are its own, which this does not reach, so that a document is not
   * dropped while a read of its that may tell of a restore is under way
   * (ProjectDocument.busy), unless at once, as once it found its project
   * gone, or the session is another account's.
   */
  clear(): void {
    if (this.undoSteps.length > 0 || this.redoSteps.length > 0) {
      this.changes++;
    }
    this.undoSteps = [];
    this.redoSteps = [];
    this.captured.clear();
    this.seqs.clear();
    if (!this.closed) {
      this.save();
      return;
    }
    if (this.storage === undefined || this.key === undefined) {
      return;
    }
    try {
      if (writerOf(this.storage.getItem(this.key)) === this.stored) {
        this.storage.removeItem(this.key);
        this.stored = null;
      }
    } catch {
      // A storage that the browser refuses keeps nothing to take out.
    }
  }

  /** Records what the store held of the entities that the batch of seq changed, once it had every operation before it. */
  capture(seq: number, revs: Captured): void {
    this.captured.set(seq, revs);
    this.save();
  }

  /** Reports whether the history wants to know what the batch of seq changed (capture): whether one of its steps undoes it. */
  wants(seq: number): boolean {
    return !this.captured.has(seq) && this.seqs.has(seq);
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
    // A step none of whose commands moved stays the same object, whose JSON
    // the history has serialised already (json).
    const moved = (steps: Step[]) =>
      steps.map((step) => {
        const commands = step.commands.map(movedCommand);
        return commands.every((c, i) => c === step.commands[i])
          ? step
          : { ...step, commands };
      });
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
   * dropped, once it has written what it recorded before: what it records
   * after, as the answer to a batch that comes once the page has signed
   * out, is not written there.
   */
  close(): void {
    this.write();
    this.closed = true;
  }

  private push(steps: Step[], step: Step): void {
    steps.push(step);
    this.changes++;
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
      this.changes++;
    }
    this.forget();
  }

  /** Finds the seqs of the batches that the steps undo anew (seqs), and forgets what it captured of one that no step undoes any longer. */
  private forget(): void {
    this.seqs = new Set(
      [...this.undoSteps, ...this.redoSteps].flatMap((s) => s.seqs),
    );
    for (const seq of this.captured.keys()) {
      if (!this.seqs.has(seq)) {
        this.captured.delete(seq);
      }
    }
  }

  /**
   * Keeps the history in the tab's storage once what changes it at once has,
   * as a batch's answer that a step joins and whose capture follows, or a
   * step that moves the others, takes its place, and advances what it
   * captured, so that the history is written once for them all, rather than
   * once for each (write).
   */
  private save(): void {
    if (this.unsaved) {
      return;
    }
    this.unsaved = true;
    queueMicrotask(() => this.write());
  }

  /**
   * Writes the history to the tab's storage, if it is to be (save): the
   * latest steps that fit, the oldest steps to undo left out first, and then
   * the oldest to redo, which come after the latest change. Where the storage
   * is full, how many are left out is looked for from as many as the last
   * write left out (left), one fewer, as once a step has been taken away,
   * or one more, as once one has been added, so that a history that stays
   * full is written twice for each change, and otherwise found by halving,
   * so that a long history is written a few times, rather than once for
   * each step that is left out.
   */
  private write(): void {
    if (!this.unsaved) {
      return;
    }
    this.unsaved = false;
    const storage = this.storage;
    const key = this.key;
    if (this.closed || storage === undefined || key === undefined) {
      return;
    }
    const steps = this.undoSteps.length + this.redoSteps.length;
    const wrote = (left: number) => this.writeLeaving(storage, key, left);
    // The fewest left out that fit are more than fails, which does not, and
    // fits at most, which does, and which the storage holds, as a write that
    // fails leaves what the storage had.
    let fails = -1;
    let fits = Math.min(this.left, steps);
    if (wrote(fits)) {
      if (fits > 0) {
        if (wrote(fits - 1)) {
          fits--;
        } else {
          fails = fits - 1;
        }
      }
    } else {
      fails = fits;
      if (fails < steps && wrote(fails + 1)) {
        fits = fails + 1;
      } else if (fails + 1 < steps && wrote(steps)) {
        fails++;
        fits = steps;
      } else {
        // Not even the history without its steps fits.
        this.left = 0;
        try {
          storage.removeItem(key);
          this.stored = null;
        } catch {
          // Nothing to keep, and nothing to take out.
        }
        return;
      }
    }
    while (fits - fails > 1) {
      const half = Math.floor((fits + fails) / 2);
      if (wrote(half)) {
        fits = half;
      } else {
        fails = half;
      }
    }
    this.left = fits;
  }

  /** Writes the history under key in storage, without its oldest steps, as many as left, and reports whether it fit. */
  private writeLeaving(storage: Storage, key: string, left: number): boolean {
    const undo = this.undoSteps.slice(Math.min(left, this.undoSteps.length));
    const redo = this.redoSteps.slice(
      Math.max(0, left - this.undoSteps.length),
    );
    const seqs = new Set([...undo, ...redo].flatMap((s) => s.seqs));
    const captured: string[] = [];
    for (const [seq, revs] of this.captured) {
      if (seqs.has(seq)) {
        captured.push(`${JSON.stringify(String(seq))}:${this.jsonOf(revs)}`);
      }
    }
    // What it saves (Saved), as JSON.stringify would write it, of the JSON
    // of each of its steps and captures.
    const steps = (list: Step[]) => list.map((s) => this.jsonOf(s)).join(",");
    const text = `{"v":1,"w":"${this.writer}","undo":[${steps(undo)}],"redo":[${steps(redo)}],"captured":{${captured.join(",")}}}`;
    try {
      storage.setItem(key, text);
      this.stored = this.writer;
      return true;
    } catch {
      // The storage is full.
      return false;
    }
  }

  /** Returns value, a step or a capture, as JSON, serialised once (json). */
  private jsonOf(value: Step | Captured): string {
    let text = this.json.get(value);
    if (text === undefined) {
      text = JSON.stringify(value);
      this.json.set(value, text);
    }
    return text;
  }
}
