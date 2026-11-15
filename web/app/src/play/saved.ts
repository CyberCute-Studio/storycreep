// Play mode's saved play states (PRD FR-PLAY-05, plan note N80): what a
// state of the flow saved on the server loads as on the snapshot that play
// runs now, of the draft as it is, though the draft may have changed since
// the state was saved, and the report of what loading it could not keep as
// it was, by the rules of §8.5, as a runtime loads a state saved against
// another snapshot: a value of a variable or a runtime attribute that no
// longer is dropped, one that the state lacks, as of a variable added since,
// defaulted, one that its declaration no longer reads, as of a variable whose
// type changed, or of an enum whose options did, retyped, and so defaulted,
// the visit counts of the nodes that no longer are, or that are jumpers,
// dropped, and a cursor at a node where the flow can no longer rest replaced
// by the entry of its board, or of the project, where the flow then starts as
// any start there does. The shared runtime's loadState takes a state of its own
// snapshot alone, so play loads the state that this makes of the saved one
// (plan item 105 brings the rules into the runtimes, Go's and Godot's). Play
// imports it once play starts, as it imports the runtime (session.ts).

import { jsonOf, readValue, type Value } from "@storycreep/shared/expr/compile";
import { attributeKey, OUT, type Snapshot } from "@storycreep/shared/flow";
import type { SaveState } from "@storycreep/shared/save-state";

/** The names of a saved state's values, as play named them as it saved the state (PlayStateNames of the API). */
export interface SavedNames {
  /** Each variable's name, by its ID. */
  readonly variables?: { readonly [id: string]: string };
  /** Each runtime attribute's name, by its component's ID and its attribute definition's. */
  readonly attributes?: {
    readonly [component: string]: { readonly [attribute: string]: string };
  };
}

/** A value of a saved state that loading dropped, as its entity no longer is one of the snapshot's. */
export interface DroppedValue {
  /** Its key of the runtime's state: the variable's ID, or attributeKey of the component and the attribute definition. */
  readonly key: string;
  /** Its name as play named it as it saved the state, or undefined for none. */
  readonly name: string | undefined;
}

/** Where loading started play instead of at a saved state's cursor, at a node where the flow can no longer rest. */
export type CursorFallback =
  /** At the entry node of the cursor's board. */
  | { readonly at: "board"; readonly board: string }
  /** At the project's entry. */
  | { readonly at: "project" }
  /** Nowhere: the flow has no node, as the project has no entry. */
  | { readonly at: "none" };

/**
 * What loading a saved state could not keep as it was saved (PRD §8.5,
 * FR-PLAY-05 AC2), each list in the order of the keys of what it lists.
 */
export interface LoadReport {
  /** The values of the variables and the runtime attributes that the snapshot no longer has, which loading dropped. */
  readonly dropped: readonly DroppedValue[];
  /** How many visit counts of the nodes that the snapshot no longer has, or that are jumpers, which the flow counts no visit of, loading dropped. */
  readonly droppedVisits: number;
  /** The keys of the snapshot's values that the state holds none of, as those added since it was saved, which loading gave their values in the snapshot. */
  readonly defaulted: readonly string[];
  /** The keys of the snapshot's values whose saved value their declarations no longer read, as of a type or the options that changed, which loading gave their values in the snapshot. */
  readonly retyped: readonly string[];
  /** Where play started instead, of a cursor at a node that the snapshot no longer has, or where the flow can no longer rest; absent where the state's cursor loaded, or it has none. */
  readonly cursor?: CursorFallback;
}

/** Reports whether loading kept everything of the state as it was saved. */
export function keptWhole(r: LoadReport): boolean {
  return (
    r.dropped.length === 0 &&
    r.droppedVisits === 0 &&
    r.defaulted.length === 0 &&
    r.retyped.length === 0 &&
    r.cursor === undefined
  );
}

/** What a saved state loads as on a snapshot (adapt). */
export interface Adapted {
  /** A state of the snapshot, which its runtime's loadState takes. */
  readonly state: SaveState;
  /** Where the flow starts once the state has loaded, for a cursor that did not load; absent where none does. */
  readonly start?: { readonly board: string; readonly node: string };
  readonly report: LoadReport;
}

/**
 * Returns what saved, a state saved against the project's draft as it was,
 * or a release of it, whose values names names, loads as on the snapshot s,
 * a projection of the project's draft as it is, by §8.5's rules: each value
 * of a variable and of a runtime attribute of s that saved holds of the type
 * that its declaration reads, as an int read for a float, as its saved
 * value, an enum's among its options; each other of s as its value in s,
 * reported as defaulted where saved holds none of it, and as retyped where
 * its declaration does not read what saved holds; each value of saved that
 * s has no variable or runtime attribute of reported as dropped, as each
 * visit count of a node that s does not have, or that is a jumper; and the
 * cursor, where it is at a node of its board that is no jumper, and, where
 * it awaits a choice, at an element that is no ending and that a connection
 * leaves, as one at rest there is; or else none, and the flow started, once
 * the state has loaded, at the entry node of the cursor's board, if s has
 * the board, and the board an entry node, or else at the project's entry,
 * if it has one, as reported: a start, as a board entry, which resets the
 * board's resetOnEnter variables and runs the entry's on-enter, as any start
 * there does (§5.8.5), rather than put the flow at a node that it did not
 * enter. A state saved before the flow started has no cursor, and loads with
 * none. Its snapshot is s's, as loadState takes it.
 */
export function adapt(
  saved: SaveState,
  names: SavedNames | undefined,
  s: Snapshot,
): Adapted {
  const m = s.manifest;
  const dropped: DroppedValue[] = [];
  const defaulted: string[] = [];
  const retyped: string[] = [];
  const variables: SaveState["variables"] = {};
  // read returns the value of the key: what saved holds of it, raw, where
  // its declaration, of the type typ and the options, reads it, or else v,
  // its value in s, which it reports as defaulted or retyped.
  const read = (
    key: string,
    raw: SaveState["variables"][string] | undefined,
    typ: Parameters<typeof readValue>[1],
    options: readonly string[],
    v: Value,
  ): boolean | number | string => {
    if (raw !== undefined) {
      const [, problem] = readValue(raw, typ, options);
      if (problem === undefined) {
        return raw;
      }
      retyped.push(key);
    } else {
      defaulted.push(key);
    }
    return jsonOf(v, v.type === "" ? "string" : v.type) ?? "";
  };
  for (const id of s.variables) {
    const d = s.declared.get(id);
    variables[id] = read(
      id,
      saved.variables[id],
      d?.type ?? "string",
      [],
      s.defaults.get(id) as Value,
    );
  }
  const attributes: SaveState["attributes"] = {};
  for (const [c, a] of s.attributes) {
    const k = attributeKey(c, a);
    attributes[c] ??= {};
    attributes[c][a] = read(
      k,
      saved.attributes[c]?.[a],
      s.declaredTypes.get(k) ?? "string",
      s.options.get(k) ?? [],
      s.initial.get(k) as Value,
    );
  }
  for (const id of Object.keys(saved.variables).sort()) {
    if (!s.declared.has(id)) {
      dropped.push({ key: id, name: names?.variables?.[id] });
    }
  }
  for (const c of Object.keys(saved.attributes).sort()) {
    for (const a of Object.keys(saved.attributes[c]).sort()) {
      const k = attributeKey(c, a);
      if (!s.declaredTypes.has(k)) {
        dropped.push({ key: k, name: names?.attributes?.[c]?.[a] });
      }
    }
  }
  const visits: SaveState["visits"] = {};
  let droppedVisits = 0;
  for (const id of Object.keys(saved.visits).sort()) {
    const node = s.nodes.get(id);
    if (node === undefined || node.type === "jumper") {
      droppedVisits++;
    } else {
      visits[id] = saved.visits[id];
    }
  }
  const state: SaveState = {
    attributes,
    projectId: m.project.id,
    schemaVersion: m.schemaVersion,
    stateVersion: saved.stateVersion,
    variables,
    visits,
  };
  if (m.snapshot.channel === "release") {
    state.snapshotId = m.snapshot.id;
    state.snapshotVersion = m.snapshot.version;
  }
  let start: Adapted["start"];
  let cursor: CursorFallback | undefined;
  const c = saved.cursor;
  if (c !== undefined) {
    if (rests(s, c)) {
      state.cursor = { boardId: c.boardId, nodeId: c.nodeId, phase: c.phase };
    } else if (s.boards.get(c.boardId)?.info.entryNodeId !== undefined) {
      start = { board: c.boardId, node: "" };
      cursor = { at: "board", board: c.boardId };
    } else if (s.entry !== undefined) {
      start = { board: "", node: "" };
      cursor = { at: "project" };
    } else {
      cursor = { at: "none" };
    }
  }
  return {
    state,
    ...(start === undefined ? {} : { start }),
    report: {
      dropped,
      droppedVisits,
      defaulted,
      retyped,
      ...(cursor === undefined ? {} : { cursor }),
    },
  };
}

// rests reports whether the flow can rest at the cursor c in the snapshot s,
// as loadState takes a cursor: at a node of its board that is no jumper,
// and, awaiting a choice, at an element that is no ending and that a
// connection leaves.
function rests(s: Snapshot, c: NonNullable<SaveState["cursor"]>): boolean {
  const node = s.nodes.get(c.nodeId);
  if (
    node === undefined ||
    node.board !== c.boardId ||
    node.type === "jumper"
  ) {
    return false;
  }
  if (c.phase === "ended") {
    return true;
  }
  return (
    c.phase === "awaiting_choice" &&
    node.type === "element" &&
    !node.isEnding &&
    (node.ports.get(OUT) ?? []).length > 0
  );
}
