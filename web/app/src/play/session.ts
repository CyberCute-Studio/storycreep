// A project's play session (PRD FR-PLAY-01, plan item 71, note N78): the
// flow of the draft as play mode walks it, on the shared flow's Runtime, in
// strict mode, as play mode always runs (§5.8.7), against a snapshot that
// projection.ts makes of the project's document, so that no step makes a
// round trip to the server (AC1). Starting reads what the document does not
// hold yet, every board's content and every component whole, a few reads at
// a time; from then on the session keeps the snapshot that it started with,
// and says once the draft has changed since, which Restart projects anew
// (N78). A session is the page's, one for each document of a project, kept
// as long as the document is, so that the flow follows a jumper to another
// board's page and keeps its state there (AC3). It keeps each value of the
// flow's state, and which the last step changed, which its inspector edits
// by hand (FR-PLAY-02), the choices that the flow hides, with why
// (FR-PLAY-03), and the history of its steps, of which Step back restores
// the state before the last, exactly, and Reset starts anew with the
// snapshot's values (FR-PLAY-04, plan note N79).

import {
  type Node as ExprNode,
  NONE,
  sameValue,
  type Value,
} from "@storycreep/shared/expr/compile";
import type {
  Choice,
  Event,
  ExpressionField,
  Phase,
  Reason,
  Runtime,
  RuntimeError,
  Snapshot,
} from "@storycreep/shared/flow";
import type { SaveState } from "@storycreep/shared/save-state";
import { createStore, type StoreApi } from "zustand/vanilla";
import { boardPart, type Doc, expressionFields } from "../document/doc";
import { equal } from "../document/reconcile";
import { inTurns, type ProjectDocument } from "../document/store";
import type { FailedPart } from "./explain";
import type { ExpressionOwner, ProjectedValue, Projection } from "./projection";

// What play runs a draft with, the projection, the shared flow's loader,
// and its runtime, with the JSON Schemas that the loader checks a snapshot
// against, and the evaluator that explains a hidden choice, read once play
// starts, rather than with the board's page, whose first frame they would
// else make later.
const engine = () =>
  Promise.all([
    import("./projection"),
    import("@storycreep/shared/flow"),
    import("./explain"),
  ]);

// What engine reads.
type Engine = Awaited<ReturnType<typeof engine>>;

/** Where play starts: a node of a board, or the board's entry node where node is "". */
export interface PlayStart {
  readonly board: string;
  readonly node: string;
}

/** A runtime error that the flow reported (§5.8.7), with where it is, as the error panel shows it. */
export interface PlayError {
  readonly code: RuntimeError;
  /** The entity whose expression it is, or the jumper or the branch, or "" for a call's own. */
  readonly at: string;
  readonly expression: ExpressionField | "";
  /** The board and the node that a link opens it by, absent for a call's own. */
  readonly board?: string;
  readonly node?: string;
  /** What at is, as the panel says where an expression is: a node, a choice, a connection that leaves a branch, or a branch's output; absent for a call's own. */
  readonly owner?: ExpressionOwner;
  /** The expression's source, as the snapshot holds it. */
  readonly source?: string;
  /** The step that reported it, from 0, the start's. */
  readonly step: number;
  /**
   * Of a runtime error of the choices built again of a value set by hand
   * (PlaySession's edit), that value's index among its step's edits, after
   * which the history lists it; absent for the step's own.
   */
  readonly edit?: number;
}

/**
 * A choice that the flow hides, as its condition was false as the flow
 * built the current node's choices, which the author view lists, with why
 * (FR-PLAY-03).
 */
export interface HiddenChoice {
  /** Its connection's ID. */
  readonly id: string;
  /** Its text, the connection's label with its values resolved, "" for none. */
  readonly text: string;
  /** Its condition's source, as the snapshot holds it. */
  readonly source: string;
  /**
   * The parts of its condition that failed, with the values that they
   * read, or undefined where the condition does not compile, which play
   * stands in for with false (N78).
   */
  readonly failed: readonly FailedPart[] | undefined;
}

/** A value that the author set by hand in the inspector (FR-PLAY-02), as the runtime holds it. */
export interface PlayEdit {
  /** The value's key (ProjectedValue's). */
  readonly key: string;
  readonly value: Value;
}

/** A node that a step entered, and its board. */
export interface PlayPlace {
  readonly board: string;
  readonly node: string;
}

/** A step of the flow, as its history lists it (FR-PLAY-04). */
export interface PlayStep {
  /** The choice that the step took, as it was offered, absent for the start's step. */
  readonly choice?: Choice;
  /** The nodes that the flow entered in the step, in order: the branches that it passed on by, and then the node that it came to. */
  readonly entered: readonly PlayPlace[];
  /** Why the flow ended in the step, if it did. */
  readonly ended?: Reason;
  /** The values that the author set by hand after the step, before the next, in order. */
  readonly edits: readonly PlayEdit[];
}

/** What the flow shows where it is at rest. */
export interface PlayFlow {
  /** The current node and its board, or "" before the flow has a node, as after a start that did not move it. */
  readonly board: string;
  readonly node: string;
  readonly phase: Phase | "";
  /** The current node's text, its values resolved, as a game shows it. */
  readonly text: string;
  /** The current element's speaker's name, or "" for none. */
  readonly speaker: string;
  /** The current node's title, or "" for none. */
  readonly title: string;
  /** Its choices, in their author's order, whose conditions held (FR-EDIT-03 AC1). */
  readonly choices: readonly Choice[];
  /** Why the flow ended, once it has (§5.8.5). */
  readonly ended: Reason | undefined;
  /** Whether strict mode halted the flow at a runtime error. */
  readonly halted: boolean;
  /** The nodes that the flow entered, in the order that it first entered them, and the connections that it took, which the canvas highlights. */
  readonly visited: ReadonlySet<string>;
  readonly traversed: ReadonlySet<string>;
  /** The runtime errors of every step, in order. */
  readonly errors: readonly PlayError[];
  /** How many steps the flow has taken: the start's, and one for each choice. */
  readonly steps: number;
  /** The value of each variable and runtime attribute, by its key (ProjectedValue's). */
  readonly values: ReadonlyMap<string, Value>;
  /**
   * The keys of the values that the last step changed (FR-PLAY-02 AC2), of
   * the start's, those that differ from the snapshot's; an edit by hand
   * changes none of them.
   */
  readonly changes: ReadonlySet<string>;
  /** The current node's choices that the flow hides, in their author's order, as it built its choices. */
  readonly hidden: readonly HiddenChoice[];
  /** The steps that the flow has taken, in order, the start's first (FR-PLAY-04). */
  readonly history: readonly PlayStep[];
}

/**
 * What a session keeps of the projection that it runs (playedOf): what it
 * stood in for, and, by ID, what play shows that the snapshot, which the
 * loader read the files into once, does not hold, rather than the boards'
 * files whole: each node's title and its speaker's name, each board's name,
 * where each connection and each branch output is, and each expression's
 * source.
 */
export interface PlayedProjection {
  readonly standIns: Projection["standIns"];
  /** Each node's title and its speaker's name, "" for none, by the node's ID. */
  readonly nodes: ReadonlyMap<
    string,
    { readonly title: string; readonly speaker: string }
  >;
  /** Each board's name, as the snapshot holds it, by the board's ID. */
  readonly boards: ReadonlyMap<string, string>;
  /** The board of each connection and each branch output, the node that a link opens it by, the node that it leaves, or its branch, and what it is. */
  readonly places: ReadonlyMap<
    string,
    {
      readonly board: string;
      readonly node: string;
      readonly owner: Exclude<ExpressionOwner, "node">;
    }
  >;
  /** Each expression's source, by its entity's ID and its field, as `${id} ${field}`. */
  readonly sources: ReadonlyMap<string, string>;
  /** Each condition's AST, as the compiler gave it, with where its parts are, by its entity's ID (Projection's). */
  readonly conditions: ReadonlyMap<string, ExprNode>;
  /** The snapshot's variables and runtime attributes, which the inspector lists (Projection's), and each by its key. */
  readonly values: readonly ProjectedValue[];
  readonly valueByKey: ReadonlyMap<string, ProjectedValue>;
  /** The components' names, by their IDs, as the snapshot holds them. */
  readonly componentNames: ReadonlyMap<string, string>;
}

/** What of a turn a board's page shows once (PlaySession's firstShown). */
export type TurnShown = "canvas" | "panel";

/** A project's play session, as its store holds it. */
export type PlayState =
  | { readonly status: "idle" }
  | {
      readonly status: "loading";
      readonly start: PlayStart;
      /** How many of the reads that play waits for have been answered, of how many. */
      readonly done: number;
      readonly total: number;
    }
  | {
      readonly status: "failed";
      readonly start: PlayStart;
      readonly error: unknown;
      /**
       * Whether play failed to read the project, as a request that the
       * connection lost, which Try again reads anew, rather than to run the
       * draft that it read, as one that the loader refuses in a way that the
       * projection stands in for none of, which the panel says, with why.
       */
      readonly reading: boolean;
    }
  | {
      readonly status: "playing";
      readonly start: PlayStart;
      readonly projection: PlayedProjection;
      readonly flow: PlayFlow;
      /** Whether the draft has changed since play projected it (N78). */
      readonly changed: boolean;
      /**
       * Counts the session's calls of the flow, its starts and its choices,
       * across its restarts, after each of which the board's page shows the
       * current node's board, as once the flow followed a jumper (AC3).
       */
      readonly turn: number;
    };

/**
 * What the author chose of play's panel, which play's starts and the board's
 * pages that the flow is followed to keep: whether it shows the choices that
 * the flow hides (FR-PLAY-03), and what the inspector's filter holds
 * (FR-PLAY-02).
 */
export interface PlayView {
  readonly showHidden: boolean;
  readonly filter: string;
}

/** How many reads play makes at once as it starts, of boards and components, within the account's limit of reads. */
export const parallelReads = 4;

/** A project's play session, and what moves it. */
export class PlaySession {
  readonly store: StoreApi<PlayState> = createStore<PlayState>(() => ({
    status: "idle",
  }));
  readonly view: StoreApi<PlayView> = createStore<PlayView>(() => ({
    showHidden: false,
    filter: "",
  }));
  private readonly doc: ProjectDocument;
  // generation counts the starts and stops, so that a start that one after
  // it replaced does nothing more once its reads are answered; aborting
  // stops the hashing and the loading of the start that it was of, which a
  // start after it, or a stop, replaced, rather than let it go on to its end.
  private generation = 0;
  private aborting = new AbortController();
  private runtime: Runtime | undefined;
  private snapshot: Snapshot | undefined;
  // What runs the snapshot, once play has read it, which Reset makes a new
  // runtime with.
  private modules: Engine | undefined;
  // The state and the flow before each step but the start's, in order,
  // which Step back restores the last of (FR-PLAY-04).
  private back: { readonly state: SaveState; readonly flow: PlayFlow }[] = [];
  // The choices that the runtime hid as it built the current node's in the
  // call that runs, which its observer tells, or undefined where it built
  // none, which observed alone reads.
  private hidden: HiddenChoice[] | undefined;
  private unwatch: (() => void) | undefined;
  private turns = 0;
  // The last turn that a board's page showed, by what of it (firstShown).
  private readonly shown = new Map<TurnShown, number>();

  constructor(doc: ProjectDocument) {
    this.doc = doc;
  }

  /** The session's state. */
  get state(): PlayState {
    return this.store.getState();
  }

  /**
   * Reports whether no board's page has shown turn as what says yet, as
   * the canvas that reveals the current node, or the panel that gives it
   * the focus, and records that the caller shows it: the page of the board
   * that the turn leaves the flow on shows it once, the page that the board's
   * page follows the flow to among them, and a page that the user opens
   * later, as by going back to that board, does not show it anew.
   */
  firstShown(what: TurnShown, turn: number): boolean {
    if (turn <= (this.shown.get(what) ?? 0)) {
      return false;
    }
    this.shown.set(what, turn);
    return true;
  }

  /**
   * Starts play at start, reading first what the document does not hold of
   * the project, and replaces any session under way. It resolves once the
   * flow has started, or play failed to.
   */
  async start(start: PlayStart): Promise<void> {
    const generation = this.begin();
    const { signal } = this.aborting;
    this.store.setState({ status: "loading", start, done: 0, total: 0 }, true);
    const current = () => generation === this.generation;
    let reading = true;
    try {
      const [modules] = await Promise.all([engine(), this.readAll(current)]);
      const [{ projectDraft, ProjectionError }, { load }] = modules;
      if (!current()) {
        return;
      }
      const view = this.doc.store.getState().view;
      let projection: Projection;
      try {
        // A board whose content the document has not read, as one that came
        // back from the trash once play had read the boards, would be
        // projected without its nodes.
        const missing = [...view.boards.values()].find((b) =>
          unreadBoard(this.doc, b),
        );
        if (missing !== undefined) {
          throw new ProjectionError(
            "unread",
            `the board ${missing.id} has not been read`,
          );
        }
        projection = await projectDraft(view, { signal });
      } catch (error) {
        // A board, or a component that the document does not hold whole, as
        // one whose values a read of the components' list took again after
        // play read it, is a read that failed, which Try again makes anew,
        // rather than a draft that play cannot run.
        reading = error instanceof ProjectionError && error.reason === "unread";
        throw error;
      }
      reading = false;
      if (!current()) {
        return;
      }
      const played = playedOf(projection);
      const snapshot = await load(projection.files, { signal });
      if (!current()) {
        return;
      }
      this.snapshot = snapshot;
      this.modules = modules;
      const flow = this.startFlow(played, start);
      this.store.setState(
        {
          status: "playing",
          start,
          projection: played,
          flow,
          changed: false,
          turn: ++this.turns,
        },
        true,
      );
      this.watch(view);
    } catch (error) {
      if (current()) {
        // The start is over, so that the reads that it still has to make,
        // as once importing what plays the draft failed while they went on,
        // are not made.
        this.begin();
        this.store.setState({ status: "failed", start, error, reading }, true);
      }
    }
  }

  /** Starts play again where it last started, projecting the draft anew. */
  restart(): Promise<void> {
    const s = this.state;
    return s.status === "idle" ? Promise.resolve() : this.start(s.start);
  }

  /**
   * Takes the current node's choice of the connection id (§5.8.5), a step,
   * once it has kept the state and the flow before it, which Step back
   * restores. A choice that the flow does not offer, as once strict mode
   * halted it, which the panel offers none of, does nothing.
   */
  choose(id: string): void {
    const s = this.state;
    const r = this.runtime;
    if (s.status !== "playing" || r === undefined) {
      return;
    }
    const choice = s.flow.choices.find((c) => c.id === id);
    if (
      choice === undefined ||
      s.flow.halted ||
      s.flow.phase !== "awaiting_choice"
    ) {
      return;
    }
    // The state before the step is kept once the step has run, so that a
    // call that threw keeps none that no step of the history follows.
    const state = r.saveState();
    const { result: events, hidden } = this.observed(() => r.choose(id));
    this.back.push({ state, flow: s.flow });
    this.store.setState({
      ...s,
      flow: this.flowAfter(s.flow, s.projection, events, hidden, choice),
      turn: ++this.turns,
    });
  }

  /**
   * Returns the flow to where it was before its last step, with the state
   * that it had then, exactly: the values, those that the author set by
   * hand before the step among them, the visit counts, the choices, and
   * the history and the runtime errors as they were (FR-PLAY-04 AC1). The
   * start's step has none before it. It is a turn, so that the board's page
   * shows the board that the flow is on again.
   */
  stepBack(): void {
    const s = this.state;
    const r = this.runtime;
    const last = this.back.at(-1);
    if (s.status !== "playing" || r === undefined || last === undefined) {
      return;
    }
    this.back.pop();
    // The state is the flow's at rest before a step: at a choice that it
    // offered, not halted, whose choices loading builds again as they were,
    // of the same values, and so are the choices hidden, which the flow
    // kept holds.
    this.observed(() => r.loadState(last.state));
    this.store.setState({ ...s, flow: last.flow, turn: ++this.turns });
  }

  /**
   * Starts the flow anew where play started, with the snapshot's values and
   * no node visited, on the snapshot that play runs, rather than on the
   * draft projected anew, as Restart does, and clears the history
   * (FR-PLAY-04). It is a turn, as a start is.
   */
  reset(): void {
    const s = this.state;
    if (s.status !== "playing" || this.runtime === undefined) {
      return;
    }
    this.store.setState({
      ...s,
      flow: this.startFlow(s.projection, s.start),
      turn: ++this.turns,
    });
  }

  /**
   * Sets the variable or the runtime attribute of key to v by hand
   * (FR-PLAY-02), as the runtime's setVariable or setAttribute take it, so
   * that the next evaluation reads it, without a restart (AC1): the current
   * node's text shows it, and, while the flow awaits a choice and strict
   * mode has not halted it, its choices are built again of it, as loading a
   * state builds them, so that a choice whose condition the value makes
   * true is offered, and one that it makes false is hidden; where it hides
   * them all, the flow awaits a choice of none, as loading a state leaves
   * it, rather than ends, which the panel says, until a value set offers
   * one again, or Step back, Reset, or Restart. It changes no value's mark
   * of the last step, and the history lists it after the step, with the
   * runtime errors of the choices built again of it after it. Halted, the
   * flow stays halted, until Step back, Reset, or Restart, as strict mode
   * halts it until a start or a load (§5.8.7). It returns false, and
   * changes nothing, of a value that the runtime does not take of the type
   * (AC3), as a string of more than 4,096 characters.
   */
  edit(key: string, v: Value): boolean {
    const s = this.state;
    const r = this.runtime;
    const snapshot = this.snapshot;
    const m = this.modules;
    if (
      s.status !== "playing" ||
      r === undefined ||
      snapshot === undefined ||
      m === undefined
    ) {
      return false;
    }
    const target = s.projection.valueByKey.get(key);
    if (target === undefined) {
      return false;
    }
    const before = valueIn(r, target);
    try {
      if (target.kind === "variable") {
        r.setVariable(target.variable, v);
      } else {
        r.setAttribute(target.component, target.attribute, v);
      }
    } catch (error) {
      if (error instanceof m[1].ValueError) {
        return false;
      }
      throw error;
    }
    const value = valueIn(r, target);
    if (sameValue(before, value)) {
      return true;
    }
    const flow = s.flow;
    const { result: events, hidden } =
      r.current().phase === "awaiting_choice" && !r.halted()
        ? this.observed(() => r.loadState(r.saveState()))
        : { result: [], hidden: undefined };
    const last = flow.history.at(-1);
    // The value's index among the step's edits, after which the history
    // lists the runtime errors of the choices built again of it.
    const edit = last?.edits.length ?? 0;
    const errors = [...flow.errors];
    for (const e of events) {
      if (e.kind === "error") {
        errors.push({
          ...errorOf(s.projection, snapshot, e, flow.steps - 1),
          edit,
        });
      }
    }
    const history =
      last === undefined
        ? flow.history
        : [
            ...flow.history.slice(0, -1),
            { ...last, edits: [...last.edits, { key, value }] },
          ];
    const { node } = r.current();
    this.store.setState({
      ...s,
      flow: {
        ...flow,
        text: node === "" ? "" : r.text(),
        choices: r.choices(),
        hidden: hidden ?? flow.hidden,
        halted: r.halted(),
        errors,
        values: valuesOf(r, s.projection, flow.values),
        history,
      },
    });
    return true;
  }

  /** Ends play. */
  stop(): void {
    this.begin();
    this.store.setState({ status: "idle" }, true);
  }

  // begin ends what the session ran, the hashing and the loading of a start
  // under way among it, and returns the next generation.
  private begin(): number {
    this.unwatch?.();
    this.unwatch = undefined;
    this.runtime = undefined;
    this.snapshot = undefined;
    this.modules = undefined;
    this.back = [];
    this.aborting.abort();
    this.aborting = new AbortController();
    return ++this.generation;
  }

  // readAll reads what the document does not hold of the project yet: the
  // tree, the variables, and the components, then every board's content
  // and every component whole, of a type, neither in the trash, that the
  // projection reads, parallelReads at a time, telling the store how many
  // it has read, while current reports that the start is still the
  // session's, and no more once a read has failed.
  private async readAll(current: () => boolean): Promise<void> {
    const doc = this.doc;
    await Promise.all([
      doc.load("tree"),
      doc.load("variables"),
      doc.load("components"),
    ]);
    if (!current()) {
      return;
    }
    const reads: (() => Promise<void>)[] = [];
    // addUnread adds the reads of what the document holds now and has not
    // read: each board's content, and each component whole.
    const addUnread = () => {
      const view = doc.store.getState().view;
      for (const b of view.boards.values()) {
        if (unreadBoard(doc, b)) {
          reads.push(() => doc.load(boardPart(b.id)));
        }
      }
      for (const c of view.components.values()) {
        const type = view.componentTypes.get(c.component_type_id);
        if (
          c.deleted_at === undefined &&
          c.attributes === undefined &&
          type !== undefined &&
          type.deleted_at === undefined
        ) {
          reads.push(() => doc.readComponent(c.id));
        }
      }
    };
    addUnread();
    let done = 0;
    const progress = () => {
      const s = this.state;
      if (current() && s.status === "loading") {
        this.store.setState({ ...s, done, total: reads.length });
      }
    };
    // inTurn makes the reads from the first of from on, parallelReads at a
    // time, as the document makes its own (inTurns), and none once the
    // start is no longer the session's.
    const inTurn = (from: number) => {
      progress();
      return inTurns(
        reads.slice(from).map((read) => async () => {
          if (!current()) {
            return;
          }
          await read();
          done++;
          progress();
        }),
        parallelReads,
      );
    };
    await inTurn(0);
    // A component that a read of the components' list took the values of
    // since, as it answers them without them, is read whole again, as the
    // others are, and a board that came back from the trash since, whose
    // content the document took out with it, is read too.
    if (current()) {
      const before = reads.length;
      addUnread();
      if (reads.length > before) {
        await inTurn(before);
      }
    }
  }

  // watch marks the session changed once the draft that it projected, of
  // view, changes, as the page's own batch or another session's operation
  // changes what the projection reads, since play projected it: while play
  // loaded the projection too, before the watch began.
  private watch(projected: Doc): void {
    // The view that the watch compared last, which the document's state
    // keeps as it changes something else, as whether it is connected, and
    // which the watch does not compare again.
    let compared: Doc | undefined;
    // done marks the session changed where view is not the draft that it
    // projected, and reports whether the watch is over.
    const done = (view: Doc): boolean => {
      const s = this.state;
      if (s.status !== "playing" || s.changed) {
        return true;
      }
      if (view === compared) {
        return false;
      }
      compared = view;
      if (draftChanged(projected, view)) {
        this.store.setState({ ...s, changed: true });
        return true;
      }
      return false;
    };
    if (done(this.doc.store.getState().view)) {
      return;
    }
    this.unwatch = this.doc.store.subscribe((state) => {
      if (done(state.view)) {
        this.unwatch?.();
        this.unwatch = undefined;
      }
    });
  }

  // startFlow makes a new runtime of the snapshot that play runs, which
  // tells the session of the choices that it hides, and returns the flow of
  // its start at start, with no step before it.
  private startFlow(played: PlayedProjection, start: PlayStart): PlayFlow {
    const m = this.modules;
    const snapshot = this.snapshot;
    if (m === undefined || snapshot === undefined) {
      return emptyFlow(new Map());
    }
    const [, { Runtime }, { failedParts }] = m;
    // hide explains each choice that the runtime r hid, as it builds them,
    // while what r holds is what their conditions read, with its text as
    // the runtime renders a choice's.
    const hide = (r: Runtime, c: Choice): HiddenChoice => {
      const tree = played.conditions.get(c.id);
      const source = played.sources.get(`${c.id} condition`) ?? "";
      return {
        id: c.id,
        text: c.text,
        source,
        failed: tree === undefined ? undefined : failedParts(tree, source, r),
      };
    };
    const r: Runtime = new Runtime(snapshot, true, {
      choices: (_, hidden) => {
        this.hidden = hidden.map((c) => hide(r, c));
      },
    });
    this.runtime = r;
    this.back = [];
    const before = valuesOf(r, played);
    const { result: events, hidden } = this.observed(() =>
      r.start(start.board, start.node),
    );
    return this.flowAfter(emptyFlow(before), played, events, hidden, undefined);
  }

  // observed runs call, a call of the runtime that may build the current
  // node's choices, and returns what it returns, with the choices that the
  // runtime hid as it built them, which its observer told meanwhile, or
  // undefined where it built none: the one place that reads what the
  // observer tells, so that no call reads what another's told.
  private observed<T>(call: () => T): {
    readonly result: T;
    readonly hidden: readonly HiddenChoice[] | undefined;
  } {
    this.hidden = undefined;
    try {
      const result = call();
      return { result, hidden: this.hidden };
    } finally {
      this.hidden = undefined;
    }
  }

  // flowAfter returns flow after the events of a call of the runtime that
  // is a step, a start's, or a choice's of the choice chosen, of which the
  // runtime hid the choices hidden (observed).
  private flowAfter(
    flow: PlayFlow,
    projection: PlayedProjection,
    events: readonly Event[],
    hidden: readonly HiddenChoice[] | undefined,
    chosen: Choice | undefined,
  ): PlayFlow {
    const r = this.runtime;
    const s = this.snapshot;
    if (r === undefined || s === undefined) {
      return flow;
    }
    const step = chosen === undefined ? 0 : flow.steps;
    const visited = new Set(flow.visited);
    const traversed = new Set(flow.traversed);
    const errors = [...flow.errors];
    const entered: PlayPlace[] = [];
    let ended: Reason | undefined;
    // The connections that the call took: the one chosen, and the one of
    // each branch that the flow passed on by.
    for (const c of r.taken()) {
      traversed.add(c);
    }
    for (const e of events) {
      switch (e.kind) {
        case "enter":
          visited.add(e.node);
          entered.push({
            board: s.nodes.get(e.node)?.board ?? "",
            node: e.node,
          });
          break;
        case "end":
          ended = e.reason;
          break;
        case "error":
          errors.push(errorOf(projection, s, e, step));
          break;
        case "exit":
        case "ready":
        case "signal":
          break;
      }
    }
    const { node, phase } = r.current();
    const board = node === "" ? "" : (s.nodes.get(node)?.board ?? "");
    const shown = projection.nodes.get(node);
    const values = valuesOf(r, projection, flow.values);
    const changes = new Set<string>();
    for (const [key, v] of values) {
      if (!sameValue(flow.values.get(key) ?? NONE, v)) {
        changes.add(key);
      }
    }
    const taken: PlayStep = {
      ...(chosen === undefined ? {} : { choice: chosen }),
      entered,
      ...(ended === undefined ? {} : { ended }),
      edits: [],
    };
    return {
      board,
      node,
      phase,
      text: node === "" ? "" : r.text(),
      speaker: shown?.speaker ?? "",
      title: shown?.title ?? "",
      choices: r.choices(),
      ended: phase === "ended" ? (ended ?? flow.ended) : undefined,
      halted: r.halted(),
      visited,
      traversed,
      errors,
      steps: flow.steps + 1,
      values,
      changes,
      hidden: hidden ?? [],
      history: [...flow.history, taken],
    };
  }
}

// emptyFlow returns the flow before play starts, of the values that the
// snapshot holds.
function emptyFlow(values: ReadonlyMap<string, Value>): PlayFlow {
  return {
    board: "",
    node: "",
    phase: "",
    text: "",
    speaker: "",
    title: "",
    choices: [],
    ended: undefined,
    halted: false,
    visited: new Set(),
    traversed: new Set(),
    errors: [],
    steps: 0,
    values,
    changes: new Set(),
    hidden: [],
    history: [],
  };
}

// valueIn returns the value of target that r holds.
function valueIn(r: Runtime, target: ProjectedValue): Value {
  return target.kind === "variable"
    ? r.variable(target.variable)
    : r.attribute(target.component, target.attribute);
}

// valuesOf returns the value of each variable and runtime attribute of
// projection that r holds, by its key, as the very object that previous
// holds of it where that is the same value, so that the inspector's row of
// a value that did not change draws nothing again (ValueRow), though
// loading a state, as Step back and a value set by hand do, makes each of
// the runtime's values anew.
function valuesOf(
  r: Runtime,
  projection: PlayedProjection,
  previous?: ReadonlyMap<string, Value>,
): ReadonlyMap<string, Value> {
  return new Map(
    projection.values.map((v): [string, Value] => {
      const now = valueIn(r, v);
      const was = previous?.get(v.key);
      return [v.key, was !== undefined && sameValue(was, now) ? was : now];
    }),
  );
}

// errorOf returns the runtime error that the event e reports, of the step,
// with where it is.
function errorOf(
  projection: PlayedProjection,
  s: Snapshot,
  e: Extract<Event, { kind: "error" }>,
  step: number,
): PlayError {
  return {
    ...placeOf(projection, s, e.at, e.expression),
    code: e.code,
    at: e.at,
    expression: e.expression,
    step,
  };
}

// playedOf returns what a session keeps of projection (PlayedProjection).
function playedOf(projection: Projection): PlayedProjection {
  const nodes = new Map<string, { title: string; speaker: string }>();
  const boards = new Map<string, string>();
  const places = new Map<
    string,
    { board: string; node: string; owner: Exclude<ExpressionOwner, "node"> }
  >();
  const sources = new Map<string, string>();
  const source = (
    id: string,
    field: ExpressionField,
    expression: { readonly source: string } | undefined,
  ) => {
    if (expression !== undefined) {
      sources.set(`${id} ${field}`, expression.source);
    }
  };
  for (const [board, f] of projection.boards) {
    boards.set(board, f.board.name);
    const branches = new Set<string>();
    for (const n of f.nodes) {
      nodes.set(n.id, {
        title: n.title ?? "",
        speaker:
          n.type === "element" && n.speaker !== undefined
            ? (projection.componentNames.get(n.speaker) ?? "")
            : "",
      });
      if (n.type === "jumper") {
        continue;
      }
      source(n.id, "onEnter", n.onEnter);
      source(n.id, "onExit", n.onExit);
      if (n.type === "branch") {
        branches.add(n.id);
        for (const o of n.outputs) {
          places.set(o.id, { board, node: n.id, owner: "output" });
          if (o.kind !== "else") {
            source(o.id, "condition", o.condition);
          }
        }
      }
    }
    for (const c of f.connections) {
      places.set(c.id, {
        board,
        node: c.from,
        owner: branches.has(c.from) ? "connection" : "choice",
      });
      source(c.id, "condition", c.condition);
      source(c.id, "onTraverse", c.onTraverse);
    }
  }
  return {
    standIns: projection.standIns,
    nodes,
    boards,
    places,
    sources,
    conditions: projection.conditions,
    values: projection.values,
    valueByKey: new Map(projection.values.map((v) => [v.key, v])),
    componentNames: projection.componentNames,
  };
}

// placeOf returns where the entity at of a runtime error is, as a link opens
// it, what it is, and the source of its expression of field, as the
// snapshot holds them: a node's, a connection's by the node that it leaves,
// and a branch output's by its branch.
function placeOf(
  projection: PlayedProjection,
  s: Snapshot,
  at: string,
  field: ExpressionField | "",
): { board?: string; node?: string; owner?: ExpressionOwner; source?: string } {
  if (at === "") {
    return {};
  }
  const node = s.nodes.get(at);
  const place =
    node === undefined
      ? projection.places.get(at)
      : { board: node.board, node: at, owner: "node" as const };
  if (place === undefined) {
    return {};
  }
  const source =
    field === "" ? undefined : projection.sources.get(`${at} ${field}`);
  return { ...place, ...(source === undefined ? {} : { source }) };
}

// unreadBoard reports whether b is a board out of the trash whose content
// doc has not read, which play reads before it projects the draft, and
// which, unread even then, as one that came back from the trash since, it
// does not project.
function unreadBoard(
  doc: ProjectDocument,
  b: { readonly id: string; readonly deleted_at?: string },
): boolean {
  return b.deleted_at === undefined && !doc.hasRead(boardPart(b.id));
}

// The fields of an entity, and of what it holds, as a branch's outputs and
// a type's attribute definitions, that every change of it changes, as the
// server answers the page's own, rather than what it is.
const roundTrip = new Set(["rev", "updated_at", "created_at"]);

// The fields that hold an expression, of which the projection reads the
// source alone, which it compiles anew, so that the server's answer to the
// page's own change of one, compiled, is no change of it.
const expressions = new Set<string>(expressionFields);

// The kinds of the project's content that the projection reads.
const projected = [
  "boards",
  "nodes",
  "connections",
  "variables",
  "componentTypes",
  "components",
] as const;

// draftChanged reports whether after, the document as the page shows it
// now, holds otherwise than before an entity of the kinds that the
// projection reads: one of the project's content, in the trash or not,
// added, gone, or changed in any field but its revision and its times, and
// those of what it holds, an expression's but its source, a board's or a
// component's description, or a component's values where a read of the
// components' list left them out. Those are what the server's answers and
// the reads of lists rewrite of a draft that did not change, which would
// else say that it did; a change of a field that the projection does not
// write, as a node's colour, says that it did, which Restart takes as no
// change, rather than a list of such fields to keep as the projection's
// own, which a field that the projection came to write would be missed by.
export function draftChanged(before: Doc, after: Doc): boolean {
  if (before === after) {
    return false;
  }
  if (!sameEntity(before.project, after.project, "project")) {
    return true;
  }
  for (const kind of projected) {
    const a = before[kind] as ReadonlyMap<string, unknown>;
    const b = after[kind] as ReadonlyMap<string, unknown>;
    if (a === b) {
      continue;
    }
    for (const [id, x] of a) {
      if (!sameEntity(x, b.get(id), kind)) {
        return true;
      }
    }
    for (const id of b.keys()) {
      if (!a.has(id)) {
        return true;
      }
    }
  }
  return false;
}

// sameEntity reports whether a and b, entities of kind, are the same as
// draftChanged compares them.
function sameEntity(
  a: unknown,
  b: unknown,
  kind: (typeof projected)[number] | "project",
): boolean {
  if (a === b) {
    return true;
  }
  if (a === undefined || b === undefined) {
    return false;
  }
  const x = a as Record<string, unknown>;
  const y = b as Record<string, unknown>;
  const ignored = (k: string) =>
    roundTrip.has(k) ||
    (k === "description" && kind !== "variables") ||
    (kind === "components" &&
      k === "attributes" &&
      (x.attributes === undefined || y.attributes === undefined));
  return equal(compared(x, ignored), compared(y, ignored));
}

// compared returns v as draftChanged compares it, which equal compares
// whatever the order of its fields: without the top-level fields that
// ignored tells, nor, at any depth, the round trip's, or those of no
// value, which its JSON would leave out.
function compared(
  v: Record<string, unknown>,
  ignored: (k: string) => boolean,
): Record<string, unknown> {
  // fields returns the fields of o but those that skip tells.
  const fields = (o: Record<string, unknown>, skip: (k: string) => boolean) => {
    const out: Record<string, unknown> = {};
    for (const [k, u] of Object.entries(o)) {
      if (u !== undefined && !skip(k)) {
        out[k] = field(k, u);
      }
    }
    return out;
  };
  // field returns the value u of the key k as draftChanged compares it: an
  // expression's source alone, which is what the projection reads of it.
  const field = (k: string, u: unknown): unknown => {
    if (
      expressions.has(k) &&
      typeof u === "object" &&
      u !== null &&
      typeof (u as { source?: unknown }).source === "string"
    ) {
      return (u as { source: string }).source;
    }
    if (Array.isArray(u)) {
      return u.map((w) => field("", w));
    }
    return typeof u === "object" && u !== null
      ? fields(u as Record<string, unknown>, (f) => roundTrip.has(f))
      : u;
  };
  return fields(v, ignored);
}

/**
 * Reports whether the page of the board shown, of doc's project, leaves a
 * turn that left the flow on board to that board's page, which it opens:
 * for another board that the document holds, rather than one that it no
 * longer does, as one that a collaborator deleted while playing, which the
 * snapshot still holds, and whose page would be none, so that the page
 * shown stays, and takes the turn itself.
 */
export function followsFlow(
  doc: ProjectDocument,
  board: string,
  shown: string,
): boolean {
  return (
    board !== "" &&
    board !== shown &&
    doc.store.getState().view.boards.has(board)
  );
}

const sessions = new WeakMap<ProjectDocument, PlaySession>();

/** Returns the play session of doc's project, made once for the document. */
export function playSessionOf(doc: ProjectDocument): PlaySession {
  let s = sessions.get(doc);
  if (s === undefined) {
    s = new PlaySession(doc);
    sessions.set(doc, s);
  }
  return s;
}
