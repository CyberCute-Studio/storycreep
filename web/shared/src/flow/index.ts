// The runtime of Storycreep's narrative (PRD §5.8.5, §8.6), as play mode runs
// it: the TypeScript twin of the Go package server/internal/flow, the
// reference runtime. load reads and checks a published snapshot, and a
// Runtime runs its flow, so that the two, and the Godot addon's runtime,
// give the same node sequence, text, choices, signals, and state for the
// same inputs (FR-PLAY-06), as schema/conformance's scenarios check. It
// evaluates the snapshot's ASTs with the expression engine, and never
// parses a source (PRD §6.1).

export { foldCase } from "./fold";
export {
  ASTUnsupportedError,
  attributeKey,
  boardPath,
  COMPONENT_TYPES_PATH,
  COMPONENTS_PATH,
  type Connection,
  LoadError,
  load,
  MANIFEST_PATH,
  OUT,
  type Output,
  SETTINGS_PATH,
  Snapshot,
  type SnapshotNode,
  VARIABLES_PATH,
} from "./load";
export {
  type Choice,
  type Event,
  type ExpressionField,
  type FlowError,
  interpolate,
  MAX_PASSES,
  type Phase,
  type Reason,
  Runtime,
  type RuntimeError,
  STATE_VERSION,
  StateError,
  ValueError,
} from "./runtime";
export { type Schema, validate } from "./schema";
export { render } from "./text";
