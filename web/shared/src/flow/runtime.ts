// The flow of a snapshot (PRD §5.8.5, §8.4.2), as play mode runs it: the
// TypeScript twin of the Go package server/internal/flow's Runtime, which
// gives the same node sequence, text, choices, signals, and state for the
// same inputs (FR-PLAY-06), as the conformance suite checks.

import saveStateSchema from "../../../../schema/save-state/v1/save-state.schema.json" with {
  type: "json",
};
import {
  condition,
  type DeclaredType,
  type Env,
  type EvaluationError,
  equalValue,
  floatValue,
  isJsonObject,
  JsonError,
  type JsonValue,
  jsonOf,
  NONE,
  type Node,
  plain,
  readJSON,
  readValue,
  run,
  type Script,
  semantics,
  type Value,
  valueString,
} from "../expr";
import type { SaveState } from "../save-state";
import {
  attributeKey,
  type Connection,
  OUT,
  type Snapshot,
  type SnapshotNode,
} from "./load";
import { type Schema, validate } from "./schema";
import { render } from "./text";

/** The runtime errors of the runtime's own (§5.8.7), beside the evaluation's. */
export type FlowError =
  /** A choice that is not one of the current node's while the flow awaits one, or any while it does not. */
  | "invalid_choice"
  /** An advance while the flow cannot advance (canAdvance). */
  | "cannot_advance"
  /**
   * A board or a node that a start or a goto names that the snapshot does
   * not have, a node of another board than the one that it names, a board
   * without an entry node that it names without a node, or no entry of the
   * project, which it names with neither, as a reset starts at.
   */
  | "unknown_node"
  /** A jumper without a target, which only a draft has, that the flow reaches. */
  | "jumper_broken"
  /** A branch that the flow would leave once it has passed through MAX_PASSES branches in one call. */
  | "flow_loop"
  /** A choose, an advance, or a goto of a flow that strict mode halted. */
  | "flow_halted";

/** A runtime error of an evaluation or of the flow (§5.8.7). */
export type RuntimeError = EvaluationError | FlowError;

/** The reasons of an end event (§5.8.5). */
export type Reason = "ending" | "dead_end" | "no_choices";

/** The expressions of an entity, as an error event names them: the fields of the published format that hold them. */
export type ExpressionField = "onEnter" | "onExit" | "onTraverse" | "condition";

/**
 * What the flow reports as it runs (§8.4.3), each as the Godot runtime emits
 * the signal of PRD §8.4.3 that its comment names.
 */
export type Event =
  /** node_entered: the flow entered the node, after its visit count and before its on-enter. */
  | { readonly kind: "enter"; readonly node: string }
  /**
   * node_ready: the flow came to the node, after its on-enter, and awaits a
   * choice there, its choices built, or ends there, when exit and end follow.
   */
  | { readonly kind: "ready"; readonly node: string }
  /** node_exited: the flow left the node, after its on-exit. */
  | { readonly kind: "exit"; readonly node: string }
  /** flow_ended: the flow ended, for the reason, after the on-exit of the node that it ended at. */
  | { readonly kind: "end"; readonly reason: Reason }
  /** narrative_signal: a script emitted the signal, with its value, or undefined for none. */
  | {
      readonly kind: "signal";
      readonly name: string;
      readonly value: Value | undefined;
    }
  /**
   * runtime_error: the code, of the expression of the entity at, if it is
   * one's, or of the jumper or the branch at, or of none, "", for a call's
   * own, and the expression "" for none.
   */
  | {
      readonly kind: "error";
      readonly code: RuntimeError;
      readonly at: string;
      readonly expression: ExpressionField | "";
    };

/** A choice of the current node: its connection's ID, and its text, the connection's label with its values resolved. */
export interface Choice {
  readonly id: string;
  readonly text: string;
}

/** The phase of the flow where it is at rest at a node, as a save state's cursor's. */
export type Phase = "awaiting_choice" | "ended";

/**
 * The most branches that the flow passes through in one call, so that a
 * cycle of branches whose conditions always hold, which no element stops,
 * ends rather than runs without end.
 */
export const MAX_PASSES = 100;

/** The version of the save state that saveState writes and loadState reads. */
export const STATE_VERSION = 1;

/** loadState's error for a state that it does not load. */
export class StateError extends Error {
  constructor(message: string) {
    super(`flow: a state that this runtime does not load: ${message}`);
    this.name = "StateError";
  }
}

/** setVariable's and setAttribute's error for what they do not set. */
export class ValueError extends Error {
  constructor(message: string) {
    super(`flow: ${message}`);
    this.name = "ValueError";
  }
}

/**
 * Returns v as a text's value shows it (FR-TEXT-02): as to_string writes it,
 * or, when it is a number and decimals is not undefined, the decimal that
 * to_string writes rounded half away from zero to that many digits after the
 * point, as the expression corpus's interpolate cases say.
 */
export function interpolate(v: Value, decimals: number | undefined): string {
  if (decimals !== undefined && v.type === "int") {
    return semantics.formatFixed(v.int, decimals);
  }
  if (decimals !== undefined && v.type === "float") {
    return semantics.formatFixed(v.float, decimals);
  }
  return valueString(v);
}

/**
 * Runs the flow of a snapshot (§5.8.5, §8.4.2), and holds its state: the
 * values of the variables and of the components' runtime attributes, the
 * nodes' visit counts, and where the flow is. Each call that moves the flow,
 * start, goto, reset, choose, advance, and loadState, returns the events that
 * it emits, in order, once it has run to its end, as the Godot runtime emits
 * its signals one after another within the call (§8.4.3); nothing runs
 * within a call but the call itself, so that between two calls the flow is
 * always at rest: before it starts, awaiting a choice, or ended.
 *
 * In strict mode, as play mode always runs and the Godot runtime in a debug
 * build, a call that reports a runtime error halts the flow once it has run
 * as it runs in non-strict mode, so that the state is the same in both:
 * until start, reset, or loadState, every choose, advance, and goto reports
 * flow_halted and does nothing (FR-GODOT-08).
 */
export class Runtime {
  private readonly s: Snapshot;
  private readonly strict: boolean;
  // events are the events of the call that runs.
  private events: Event[] = [];
  private vars = new Map<string, Value>();
  private attrs = new Map<string, Value>();
  private visitCounts = new Map<string, number>();
  // current is the current node, or "" before the flow starts, and phase
  // its phase where the flow is at rest there, or "" between two nodes
  // within a call.
  private node = "";
  private phase: Phase | "" = "";
  private currentChoices: Connection[] = [];
  // isHalted is set once strict mode halts the flow.
  private isHalted = false;
  // errored is set once the call reports a runtime error, and passes counts
  // the branches that it has passed through.
  private errored = false;
  private passes = 0;
  // at and expression are the entity and the expression that the evaluator
  // evaluates, which its runtime errors are of.
  private at = "";
  private expression: ExpressionField | "" = "";
  // env is the evaluator's Env of the runtime's state.
  private readonly env: Env;

  /**
   * Returns a runtime of the snapshot s, its variables and its runtime
   * attributes their values in the snapshot, no node visited, and the flow
   * not started, in strict mode if strict is set.
   */
  constructor(s: Snapshot, strict: boolean) {
    this.s = s;
    this.strict = strict;
    this.env = {
      variable: (id) => this.vars.get(id) ?? NONE,
      setVariable: (id, v) => {
        this.vars.set(id, v);
      },
      attribute: (c, a) => this.attrs.get(attributeKey(c, a)) ?? NONE,
      setAttribute: (c, a, v) => {
        this.attrs.set(attributeKey(c, a), v);
      },
      visits: (n) => this.visitCounts.get(n) ?? 0,
      signal: (name, value) => {
        this.emit({ kind: "signal", name, value });
      },
      error: (code) => {
        this.errored = true;
        this.emit({
          kind: "error",
          code,
          at: this.at,
          expression: this.expression,
        });
      },
    };
    this.defaults();
  }

  // defaults sets every value to its snapshot's, and clears the visit counts
  // and where the flow is.
  private defaults(): void {
    this.vars = new Map(this.s.defaults);
    this.attrs = new Map(this.s.initial);
    this.visitCounts = new Map();
    this.node = "";
    this.phase = "";
    this.currentChoices = [];
  }

  // call runs f, a call that moves the flow, after the halt that it clears,
  // if it is start, reset, or loadState, or refused by it, with
  // flow_halted, if it is choose, advance, or goto, halts the flow in strict
  // mode once f has reported a runtime error, and returns the events that it
  // emitted, in order, [] for none.
  private call(clears: boolean, f: () => void): Event[] {
    this.events = [];
    this.errored = false;
    this.passes = 0;
    this.at = "";
    this.expression = "";
    if (clears) {
      this.isHalted = false;
    }
    if (this.isHalted) {
      this.fail("flow_halted", "");
    } else {
      f();
    }
    if (this.strict && this.errored) {
      this.isHalted = true;
    }
    const events = this.events;
    this.events = [];
    return events;
  }

  // emit records the event e of the call that runs.
  private emit(e: Event): void {
    this.events.push(e);
  }

  // fail reports the runtime error code of the entity at, the call's own.
  private fail(code: RuntimeError, at: string): void {
    this.errored = true;
    this.emit({ kind: "error", code, at, expression: "" });
  }

  /**
   * Starts the flow at the node of the board, as start() does (§5.8.5): at
   * the board's entry node when node is "", and at the project's entry when
   * both are "", wherever the flow was, without the on-exit of the node that
   * was current, as a board entry, so that the board's resetOnEnter
   * variables are reset, and at a jumper's target when the node is a jumper.
   * A board or a node that the snapshot does not have reports unknown_node,
   * and a jumper without a target jumper_broken, and neither moves the flow.
   * It clears a halt.
   */
  start(board: string, node: string): Event[] {
    return this.call(true, () => {
      const id = this.resolve(board, node);
      if (id !== undefined) {
        this.settle(id, true);
      }
    });
  }

  /**
   * Moves the flow to the node of the board, as goto() does, as start does,
   * but that it leaves the current node first, running its on-exit, but for
   * one that the flow ended at, which it left already, and runs no
   * connection's on-traverse. A node that start would not move to moves the
   * flow nowhere, without the on-exit.
   */
  goto(board: string, node: string): Event[] {
    return this.call(false, () => {
      const id = this.resolve(board, node);
      if (id === undefined) {
        return;
      }
      if (this.node !== "" && this.phase === "awaiting_choice") {
        this.exit();
      }
      this.settle(id, true);
    });
  }

  /**
   * Sets every variable and runtime attribute to its value in the snapshot,
   * clears every visit count, and starts the flow at the project's entry, as
   * start("", "") does, as reset() does, once it has resolved the entry,
   * before anything else (§5.8.5): of a snapshot without an entry, it
   * reports unknown_node, and of one whose entry is a jumper without a target
   * jumper_broken, and changes nothing. It clears a halt.
   */
  reset(): Event[] {
    return this.call(true, () => {
      const id = this.resolve("", "");
      if (id !== undefined) {
        this.defaults();
        this.settle(id, true);
      }
    });
  }

  /**
   * Takes the choice of the current node whose connection's ID is id, as
   * choose() does: the node's on-exit, then the connection's on-traverse,
   * then the flow arrives at its node (§5.8.5). An id that is not one of the
   * current choices, or any while the flow does not await a choice, reports
   * invalid_choice and moves nothing.
   */
  choose(id: string): Event[] {
    return this.call(false, () => {
      const c = this.currentChoices.find((x) => x.id === id);
      if (this.phase !== "awaiting_choice" || c === undefined) {
        this.fail("invalid_choice", "");
        return;
      }
      this.leave(c);
    });
  }

  /**
   * Takes the one connection that leaves the current node, which has no
   * condition, as advance() does, as choose does, or reports cannot_advance
   * and moves nothing when the flow cannot advance (canAdvance).
   */
  advance(): Event[] {
    return this.call(false, () => {
      if (!this.canAdvance()) {
        this.fail("cannot_advance", "");
        return;
      }
      this.leave(this.nodeOf(this.node).ports.get(OUT)?.[0] as Connection);
    });
  }

  /**
   * Reports whether the flow awaits a choice at a node that one connection
   * leaves, without a condition, which is then its one choice, as
   * can_advance() does.
   */
  canAdvance(): boolean {
    if (this.phase !== "awaiting_choice") {
      return false;
    }
    const cs = this.nodeOf(this.node).ports.get(OUT) ?? [];
    return cs.length === 1 && cs[0].condition === undefined;
  }

  // nodeOf returns the node of the snapshot whose ID is id, which it has.
  private nodeOf(id: string): SnapshotNode {
    return this.s.nodes.get(id) as SnapshotNode;
  }

  // resolve returns the node that a start or a goto of the board and the
  // node moves the flow to, which is no jumper, or reports why it moves it
  // to none and returns undefined.
  private resolve(board: string, node: string): string | undefined {
    let id = node;
    if (node !== "") {
      const n = this.s.nodes.get(node);
      if (n === undefined || (board !== "" && n.board !== board)) {
        this.fail("unknown_node", "");
        return undefined;
      }
    } else if (board !== "") {
      const entry = this.s.boards.get(board)?.info.entryNodeId;
      if (entry === undefined) {
        this.fail("unknown_node", "");
        return undefined;
      }
      id = entry;
    } else {
      if (this.s.entry === undefined) {
        this.fail("unknown_node", "");
        return undefined;
      }
      id = this.s.entry.nodeId;
    }
    return this.destination(id);
  }

  // destination returns the node that the flow arrives at when it reaches
  // the node id: id, or a jumper's target (§5.8.5), or reports
  // jumper_broken of a jumper without one and returns undefined.
  private destination(id: string): string | undefined {
    const n = this.nodeOf(id);
    if (n.type !== "jumper") {
      return id;
    }
    if (n.target === undefined) {
      this.fail("jumper_broken", id);
      return undefined;
    }
    return n.target.nodeId;
  }

  // leave takes the connection c from the current node, which awaits a
  // choice: when c leads to a jumper without a target, the flow ends at the
  // node, as at a dead end, without its ready, which it reported already;
  // otherwise the node's on-exit, then c's on-traverse, then the flow
  // arrives at c's node.
  private leave(c: Connection): void {
    const dest = this.destination(c.to);
    if (dest === undefined) {
      this.finish("dead_end");
      return;
    }
    this.exit();
    this.traverse(c);
    this.settle(dest, false);
  }

  // exit leaves the current node, at which the flow then awaits no choice,
  // and runs its on-exit and reports its exit.
  private exit(): void {
    this.phase = "";
    this.currentChoices = [];
    const id = this.node;
    const n = this.nodeOf(id);
    this.runScript(id, "onExit", n.type === "jumper" ? undefined : n.onExit);
    this.emit({ kind: "exit", node: id });
  }

  // traverse runs the on-traverse of c, the connection that the flow takes.
  private traverse(c: Connection): void {
    this.runScript(c.id, "onTraverse", c.onTraverse);
  }

  // settle has the flow arrive at the node id, which is no jumper, as a
  // board entry when entry is set or the node's board is not the current
  // node's, and go on through each branch that it passes until it is at
  // rest: awaiting a choice, or ended. A branch's connection to a jumper
  // without a target ends the flow at the branch, a dead end, and a branch
  // that the flow would leave once it has left MAX_PASSES branches in the
  // call ends it there with flow_loop, once its connection's destination is
  // resolved (§5.8.5).
  private settle(first: string, firstEntry: boolean): void {
    let id = first;
    let entry = firstEntry;
    for (;;) {
      const next = this.arrive(id, entry);
      if (next === undefined) {
        return;
      }
      const dest = this.destination(next.to);
      if (dest === undefined) {
        this.end("dead_end");
        return;
      }
      if (this.passes >= MAX_PASSES) {
        this.fail("flow_loop", this.node);
        this.end("dead_end");
        return;
      }
      this.passes++;
      this.exit();
      this.traverse(next);
      id = dest;
      entry = false;
    }
  }

  // arrive has the flow enter the node id (§5.8.5): the board's entry, when
  // it is one, which resets its resetOnEnter variables, then its visit
  // count, its enter, and its on-enter; then an element's choices, at which
  // it awaits a choice, or ends; or a branch's output, of which it returns
  // the connection that the flow passes on by, or undefined when it ends
  // there.
  private arrive(id: string, entry: boolean): Connection | undefined {
    const n = this.nodeOf(id);
    if (entry || this.node === "" || this.nodeOf(this.node).board !== n.board) {
      for (const v of this.s.boards.get(n.board)?.reset ?? []) {
        this.vars.set(v, this.s.defaults.get(v) as Value);
      }
    }
    this.node = id;
    this.phase = "";
    this.currentChoices = [];
    const count = this.visitCounts.get(id) ?? 0;
    if (count < semantics.MAX_INT) {
      this.visitCounts.set(id, count + 1);
    }
    this.emit({ kind: "enter", node: id });
    if (n.type === "element") {
      this.runScript(id, "onEnter", n.onEnter);
      this.element(n);
    } else if (n.type === "branch") {
      this.runScript(id, "onEnter", n.onEnter);
      return this.branch(n);
    }
    return undefined;
  }

  // element builds the choices of the element n, the current node, at which
  // the flow awaits a choice, or ends: at an ending, whose connections it
  // does not read, at a dead end, which no connection leaves, or at a node
  // whose connections' conditions are all false.
  private element(n: SnapshotNode & { type: "element" }): void {
    const cs = n.ports.get(OUT) ?? [];
    if (n.isEnding) {
      this.end("ending");
      return;
    }
    if (cs.length === 0) {
      this.end("dead_end");
      return;
    }
    this.currentChoices = this.hold(cs);
    if (this.currentChoices.length === 0) {
      this.end("no_choices");
      return;
    }
    this.phase = "awaiting_choice";
    this.emit({ kind: "ready", node: n.id });
  }

  // hold returns the connections of cs whose conditions hold, in order, a
  // connection without one among them.
  private hold(cs: readonly Connection[]): Connection[] {
    return cs.filter((c) => this.holds(c.id, c.condition));
  }

  // branch picks the output of the branch n, the current node: its first if
  // or elseif whose condition holds, or else its else, and returns the first
  // connection of the output whose condition holds, which the flow passes on
  // by, unless settle ends it there, evaluating the outputs' conditions, and
  // then the connections', in order until one holds, and none after it, or
  // ends the flow at the branch, at a dead end when it picks no output or
  // none leaves the output, or at no choices when no connection's condition
  // holds, and returns undefined.
  private branch(n: SnapshotNode & { type: "branch" }): Connection | undefined {
    const picked = n.outputs.find(
      (o) => o.kind === "else" || this.holds(o.id, o.condition),
    );
    const cs = picked === undefined ? [] : (n.ports.get(picked.id) ?? []);
    if (cs.length === 0) {
      this.end("dead_end");
      return undefined;
    }
    const c = cs.find((x) => this.holds(x.id, x.condition));
    if (c === undefined) {
      this.end("no_choices");
    }
    return c;
  }

  // end ends the flow at the current node, for the reason (§5.8.5): its
  // ready, its choices none, then its on-exit, its exit, and the end.
  private end(reason: Reason): void {
    this.emit({ kind: "ready", node: this.node });
    this.finish(reason);
  }

  // finish ends the flow at the current node, for the reason, as end does,
  // but for its ready: its phase is ended once its on-exit has run and it
  // has reported its exit, as a cursor's is (§8.5).
  private finish(reason: Reason): void {
    this.exit();
    this.phase = "ended";
    this.emit({ kind: "end", reason });
  }

  // runScript runs the script s, if it is not undefined, of the entity at's
  // expression.
  private runScript(
    at: string,
    expression: ExpressionField,
    s: Script | undefined,
  ): void {
    if (s === undefined) {
      return;
    }
    this.at = at;
    this.expression = expression;
    run(s, this.env);
    this.at = "";
    this.expression = "";
  }

  // holds reports whether the condition c, of the entity at, holds, as one
  // that is undefined does.
  private holds(at: string, c: Node | undefined): boolean {
    if (c === undefined) {
      return true;
    }
    this.at = at;
    this.expression = "condition";
    try {
      return condition(c, this.env);
    } finally {
      this.at = "";
      this.expression = "";
    }
  }

  /**
   * Returns the current node and its phase, or "" and "" before the flow
   * starts, as current_node() does: the node that the flow ended at stays
   * current (§5.8.5).
   */
  current(): { node: string; phase: Phase | "" } {
    return { node: this.node, phase: this.phase };
  }

  /** Reports whether strict mode halted the flow. */
  halted(): boolean {
    return this.isHalted;
  }

  /** Reports whether the flow ended, as is_finished() does. */
  isFinished(): boolean {
    return this.phase === "ended";
  }

  /**
   * Returns the current choices, in their order, none while the flow does
   * not await a choice: the connections that leave the current node whose
   * conditions held when the flow arrived at it, or when loadState loaded the
   * state, as get_choices() does. A change of a value after either does not
   * change them; the next node's choices read it.
   */
  choices(): Choice[] {
    return this.currentChoices.map((c) => ({
      id: c.id,
      text: c.label === undefined ? "" : this.render(c.label),
    }));
  }

  /**
   * Returns the current node's text, its values resolved as they are now
   * (FR-TEXT-02), as a StorycreepNode's text() does: its plain text, each
   * value as to_string writes it, or a number with its decimals rounded half
   * away from zero, and "" of a branch, of an element without text, and
   * before the flow starts.
   */
  text(): string {
    if (this.node === "") {
      return "";
    }
    const n = this.nodeOf(this.node);
    return n.type === "element" && n.text !== undefined
      ? this.render(n.text)
      : "";
  }

  // render returns the text of the segments, their values resolved.
  private render(segments: Parameters<typeof render>[0]): string {
    return render(segments, (s) => {
      const v =
        s.attribute === undefined
          ? this.vars.get(s.ref)
          : this.attrs.get(attributeKey(s.ref, s.attribute));
      return interpolate(v ?? NONE, s.decimals);
    });
  }

  /**
   * Returns the value of the variable whose ID is id, as get_variable() does
   * by an ID, or none, of no type, of an id that is no variable's.
   */
  variable(id: string): Value {
    return this.vars.get(id) ?? NONE;
  }

  /**
   * Sets the variable whose ID is id to v, an int widened for a float, a -0
   * as 0, as set_variable() does, or throws a ValueError and sets nothing of
   * an id that is no variable's, a v of another type, or one that is no
   * value of the language, which a save state would not hold as it is. The
   * choices of the current node stay as they are (choices).
   */
  setVariable(id: string, v: Value): void {
    const d = this.s.declared.get(id);
    if (d === undefined) {
      throw new ValueError(`${id} is no variable of the snapshot`);
    }
    this.vars.set(id, asSaved(typed(v, d.type), d.type, []));
  }

  /**
   * Returns the value of the runtime attribute whose definition's ID is
   * attribute of the component whose ID is component.
   */
  attribute(component: string, attribute: string): Value {
    return this.attrs.get(attributeKey(component, attribute)) ?? NONE;
  }

  /**
   * Sets the runtime attribute to v, as setVariable sets a variable, an
   * enum's to one of its options, or throws a ValueError.
   */
  setAttribute(component: string, attribute: string, v: Value): void {
    const k = attributeKey(component, attribute);
    const cur = this.attrs.get(k);
    if (cur === undefined) {
      throw new ValueError(
        `${attribute} of ${component} is no runtime attribute of the snapshot`,
      );
    }
    // As loadState reads what saveState writes: an enum's by its
    // declaration, as one of its options.
    this.attrs.set(
      k,
      asSaved(
        typed(v, cur.type as DeclaredType),
        this.s.declaredTypes.get(k) as DeclaredType,
        this.s.options.get(k) ?? [],
      ),
    );
  }

  /** Returns how many times the flow has entered the node, as visits() does. */
  visits(node: string): number {
    return this.visitCounts.get(node) ?? 0;
  }

  /**
   * Returns the runtime's state as a save state (PRD §8.5), as save_state()
   * does: the snapshot's project, its release's ID and version, or neither
   * of a draft, its schemaVersion, the cursor, unless the flow has not
   * started, the value of every variable and of every component's runtime
   * attribute, and the visit count of every node that the flow has entered.
   * Neither a halt nor the current choices are part of it: loadState builds
   * the choices again.
   */
  saveState(): SaveState {
    const m = this.s.manifest;
    const st: SaveState = {
      attributes: {},
      projectId: m.project.id,
      schemaVersion: m.schemaVersion,
      stateVersion: STATE_VERSION,
      variables: {},
      visits: {},
    };
    if (m.snapshot.channel === "release") {
      st.snapshotId = m.snapshot.id;
      st.snapshotVersion = m.snapshot.version;
    }
    if (this.node !== "") {
      st.cursor = {
        boardId: this.nodeOf(this.node).board,
        nodeId: this.node,
        phase: this.phase as Phase,
      };
    }
    for (const id of this.s.variables) {
      const d = this.s.declared.get(id);
      const json = jsonOf(this.vars.get(id) ?? NONE, d?.type ?? "string");
      if (json !== undefined) {
        st.variables[id] = json;
      }
    }
    for (const [c, a] of this.s.attributes) {
      const v = this.attrs.get(attributeKey(c, a)) ?? NONE;
      const json = jsonOf(v, v.type === "" ? "string" : v.type);
      st.attributes[c] ??= {};
      if (json !== undefined) {
        st.attributes[c][a] = json;
      }
    }
    for (const [id, n] of this.visitCounts) {
      st.visits[id] = n;
    }
    return st;
  }

  /**
   * Sets the runtime's state to st, a state saved against the runtime's
   * snapshot, as load_state() does (§5.8.5): the values, the visit counts,
   * and the cursor, without the on-exit of the node that was current, as no
   * board entry, and without the visit count, the enter, or the on-enter of
   * the cursor's node, whose choices it builds again when the cursor awaits
   * one, from the conditions as they hold of st, and which it does not end
   * when none holds. It clears a halt, and returns the events that it emits,
   * as a call that moves the flow does: the runtime errors of the
   * conditions, [] for none.
   *
   * It loads a state of the snapshot alone, as save-state/v1's schema takes
   * one, and throws a StateError, and changes nothing, of any other: of
   * another project or snapshot, of another stateVersion, that lacks a
   * variable or a runtime attribute of the snapshot or holds one that the
   * snapshot does not have, or attributes of a component without any, or a
   * value that its declaration's type does not read (§5.8.9), a visit count
   * of a node that the snapshot does not have, or of a jumper, or below 1,
   * or beyond 2^53 − 1, or whose cursor is not of a node of its board, which
   * is no jumper, in a phase that is neither awaiting_choice nor ended, and
   * awaits a choice only at an element that is no ending and that a
   * connection leaves, as no flow awaits one elsewhere.
   *
   * st is a state as JSON.parse reads one, which it reads as JSON.stringify
   * writes it, or its JSON itself, a string, which it reads as readJSON
   * does, each number as it is written, as the Go runtime reads a state's
   * file (published.Decode), so that a number that JSON.parse would round,
   * as an int's 2.0000000000000001, which is no integer, is refused rather
   * than read as 2. Either way it refuses a null anywhere, which no state
   * holds, as the Go runtime does.
   */
  loadState(st: SaveState | string): Event[] {
    const { state, vars, attrs, visits } = stateOf(this.s, st);
    return this.call(true, () => {
      this.vars = vars;
      this.attrs = attrs;
      this.visitCounts = visits;
      this.node = "";
      this.phase = "";
      this.currentChoices = [];
      const c = state.cursor;
      if (c !== undefined) {
        // The flow comes to rest, of the cursor's phase, once its choices
        // are built.
        this.node = c.nodeId;
        if (c.phase === "awaiting_choice") {
          this.currentChoices = this.hold(
            this.nodeOf(c.nodeId).ports.get(OUT) ?? [],
          );
        }
        this.phase = c.phase;
      }
    });
  }
}

// typed returns v as a value of the type typ, an int widened for a float, or
// throws when it is of another type, or an int beyond ±(2^53 − 1) for a
// float, which is no value of the language, and which the float would hold
// rounded, as another value (asSaved).
function typed(v: Value, typ: DeclaredType): Value {
  if (v.type === typ) {
    return v;
  }
  if (typ === "float" && v.type === "int") {
    asSaved(v, "int", []);
    return floatValue(v.int);
  }
  throw new ValueError(
    `a ${v.type || "value of no type"} is no value of the type ${typ}`,
  );
}

// asSaved returns v, a value of a variable or a runtime attribute that its
// declaration types typ, an enum's one of options, as saveState writes it
// (jsonOf) and loadState reads it back (readValue), a -0 as 0, or throws a
// ValueError when it reads back as another value, or as none: when v is no
// value of the language, an int beyond ±(2^53 − 1), a float that is not
// finite, or a string of more than 4,096 characters, or with a NUL or a lone
// surrogate, or, of an enum, a string that is none of its options. It is
// that round trip, so that what it takes is what a save and a load keep.
function asSaved(
  v: Value,
  typ: DeclaredType,
  options: readonly string[],
): Value {
  const [read, problem] = readValue(
    jsonOf(v, v.type === "" ? "string" : v.type),
    typ,
    options,
  );
  if (problem !== undefined || !equalValue(read, v)) {
    throw new ValueError(
      `the ${v.type || "value"} is no value of the language, as an int beyond ±(2^53 − 1), a float that is not finite, or a string of more than 4096 characters, or with a NUL, is not, or, of an enum, none of its options`,
    );
  }
  return read;
}

// stateOf reads given, a state of the snapshot s or its JSON, as loadState
// reads it, and returns it with its values and its visit counts, or throws
// why it is none.
function stateOf(
  s: Snapshot,
  given: SaveState | string,
): {
  state: SaveState;
  vars: Map<string, Value>;
  attrs: Map<string, Value>;
  visits: Map<string, number>;
} {
  const fail = (problem: string) => new StateError(problem);
  let text: string | undefined;
  try {
    text = typeof given === "string" ? given : JSON.stringify(given);
  } catch {
    // A BigInt, or an object that holds itself.
  }
  if (text === undefined) {
    throw fail("it is not JSON");
  }
  let raw: JsonValue;
  try {
    raw = readJSON(text);
  } catch (e) {
    if (e instanceof JsonError) {
      throw fail(`it ${e.message}`);
    }
    throw e;
  }
  const problem = validate(saveStateSchema as Schema, raw);
  if (problem !== undefined || !isJsonObject(raw)) {
    throw fail(
      `${problem ?? "/ is not an object"}, which its schema does not take`,
    );
  }
  // Each number of st but the values, a version or a visit count, is one
  // that its schema holds to an integer within ±(2^53 − 1), which plain
  // reads exactly; the values are read from raw, as they are written.
  const st = plain(raw) as SaveState;
  const rawVariables = raw.variables as { [key: string]: JsonValue };
  const rawAttributes = raw.attributes as {
    [key: string]: { [key: string]: JsonValue };
  };
  const m = s.manifest;
  const id = m.snapshot.channel === "release" ? m.snapshot.id : undefined;
  const version =
    m.snapshot.channel === "release" ? m.snapshot.version : undefined;
  if (st.stateVersion !== STATE_VERSION) {
    throw fail(`its stateVersion is ${st.stateVersion}`);
  }
  if (
    st.projectId !== m.project.id ||
    st.snapshotId !== id ||
    st.snapshotVersion !== version ||
    st.schemaVersion !== m.schemaVersion
  ) {
    throw fail("it was saved against another snapshot");
  }
  const vars = new Map<string, Value>();
  const keys = Object.keys(st.variables).sort();
  const want = [...s.variables].sort();
  if (keys.length !== want.length || keys.some((k, i) => k !== want[i])) {
    throw fail("its variables are not the snapshot's");
  }
  for (const vid of keys) {
    const d = s.declared.get(vid);
    const [read, problem] = readValue(rawVariables[vid], d?.type ?? "string");
    if (problem !== undefined) {
      throw fail(`the value of the variable ${vid} ${problem}`);
    }
    vars.set(vid, read);
  }
  const attrs = new Map<string, Value>();
  let n = 0;
  for (const c of Object.keys(st.attributes).sort()) {
    const values = rawAttributes[c];
    const names = Object.keys(values).sort();
    if (names.length === 0) {
      throw fail(`it holds attributes of ${c}, but none of its values`);
    }
    for (const a of names) {
      const k = attributeKey(c, a);
      const typ = s.declaredTypes.get(k);
      if (typ === undefined) {
        throw fail(`${a} of ${c} is no runtime attribute of the snapshot`);
      }
      const [read, problem] = readValue(values[a], typ, s.options.get(k) ?? []);
      if (problem !== undefined) {
        throw fail(`the value of ${a} of ${c} ${problem}`);
      }
      attrs.set(k, read);
      n++;
    }
  }
  if (n !== s.attributes.length) {
    throw fail("it lacks a runtime attribute of the snapshot");
  }
  const visits = new Map<string, number>();
  for (const nid of Object.keys(st.visits).sort()) {
    const count = st.visits[nid];
    const node = s.nodes.get(nid);
    if (
      node === undefined ||
      !Number.isInteger(count) ||
      count < 1 ||
      count > semantics.MAX_INT
    ) {
      throw fail(
        `its visit count of ${nid} is not of a node of the snapshot, or below 1, or beyond ${semantics.MAX_INT}`,
      );
    }
    if (node.type === "jumper") {
      throw fail(
        `it has a visit count of the jumper ${nid}, which the flow never enters`,
      );
    }
    visits.set(nid, count);
  }
  const c = st.cursor;
  if (c !== undefined) {
    const node = s.nodes.get(c.nodeId);
    if (node === undefined || node.board !== c.boardId) {
      throw fail(
        `its cursor's node ${c.nodeId} is not a node of its board ${c.boardId}`,
      );
    }
    if (c.phase !== "awaiting_choice" && c.phase !== "ended") {
      throw fail(
        `its cursor's phase is ${JSON.stringify(c.phase)}, neither awaiting_choice nor ended`,
      );
    }
    const awaits = c.phase === "awaiting_choice";
    if (node.type === "jumper") {
      throw fail(`its cursor is at the jumper ${c.nodeId}`);
    }
    if (node.type === "branch" && awaits) {
      throw fail(`its cursor awaits a choice at the branch ${c.nodeId}`);
    }
    if (
      node.type === "element" &&
      awaits &&
      (node.isEnding || (node.ports.get(OUT) ?? []).length === 0)
    ) {
      throw fail(
        `its cursor awaits a choice at ${c.nodeId}, an ending or a dead end`,
      );
    }
  }
  return { state: st, vars, attrs, visits };
}
