// Generated from schema/save-state/v1/*.schema.json. Do not edit; run
// "go test ./internal/published -run TestTypeScript -update" in server/
// instead.

/** Any JSON value but null, at any depth: what an object may hold of a property that this version of the format does not define, which a consumer ignores (PRD §6.4). */
export type AnyValue = boolean | number | string | Array<AnyValue> | { [key: string]: AnyValue };

/** An attribute definition's ID, in its canonical spelling (PRD §5.2). */
export type AttributeDefId = string;

/** Values of attributes, by their attribute definitions' IDs, each of its definition's type. */
export type AttributeValues = { [key: string]: Value };

/** A board's ID, in its canonical spelling (PRD §5.2). */
export type BoardId = string;

/** A component's ID, in its canonical spelling (PRD §5.2). */
export type ComponentId = string;

/** Where the flow is: its current node, and its phase. */
export interface Cursor {
  boardId: BoardId;
  /** The current node, a node of the board that is no jumper. */
  nodeId: NodeId;
  /** awaiting_choice when the node's choices are built and the game chooses one, or advances; ended when the flow ended at the node, after its on-exit ran (PRD §5.8.5). */
  phase: "awaiting_choice" | "ended";
}

/** A node's ID, in its canonical spelling (PRD §5.2). */
export type NodeId = string;

/** A project's ID, in its canonical spelling (PRD §5.2). */
export type ProjectId = string;

/** The narrative state that a runtime saves and loads (PRD §8.5, FR-GODOT-07): where the flow is, and the values of the variables and the runtime attributes, and the nodes' visit counts, by ID, never by key. */
export interface SaveState {
  /** The values of the components' runtime attributes, by the component's ID and the attribute definition's. */
  attributes: { [key: string]: AttributeValues };
  /** Where the flow is. Without it, the flow has not started. */
  cursor?: Cursor;
  projectId: ProjectId;
  /** The version of the published format of the snapshot that the state was saved against. */
  schemaVersion: number;
  /** The snapshot that the state was saved against. Without it, the state was saved against a draft (PRD §7.8). */
  snapshotId?: SnapshotId;
  /** The snapshot's version. Without it, the state was saved against a draft. */
  snapshotVersion?: string;
  /** The version of the save-state format, 1, which a runtime of an older one refuses (PRD §8.5). */
  stateVersion: 1;
  /** The values of the variables, global and of each board, by their IDs, each of its variable's type. */
  variables: { [key: string]: Value };
  /** How many times the flow has entered each node that it has entered, by the node's ID; a node that it has not entered has none. */
  visits: Visits;
}

/** A snapshot's ID, in its canonical spelling (PRD §5.2). */
export type SnapshotId = string;

/** A value, which its declaration's type says how to read, never its JSON form (PRD §5.8.9): a boolean, a number, or a string. */
export type Value = boolean | number | string;

/** A variable's ID, in its canonical spelling (PRD §5.2). */
export type VariableId = string;

/** How many times the flow has entered each node that it has entered, by the node's ID. */
export type Visits = { [key: string]: number };
