// Generated from schema/published/v1/*.schema.json. Do not edit; run
// "go test ./internal/published -run TestTypeScript -update" in server/
// instead.

/** The AST of an expression: a condition's, an expression, or a script's, a script node, whose names are resolved to the IDs of what they name (PRD §5.8.8). */
export type AST = ASTExpression | ASTScript;

/** An assignment, with its operator as its source writes it. */
export interface ASTAssign {
  kind: "assign";
  op: "%=" | "*=" | "+=" | "-=" | "/=" | "=";
  target: ASTTarget;
  value: ASTExpression;
}

/** A component's runtime attribute. */
export interface ASTAttr {
  attribute: AttributeDefId;
  kind: "attr";
  /** <component_key>.<attribute_key>, for readability: the AST names the attribute by its ref, the component's ID, and its attribute, the attribute definition's. */
  name: string;
  ref: ComponentId;
}

/** A binary operator applied to its operands, left and right. */
export interface ASTBinary {
  kind: "binary";
  left: ASTExpression;
  op: "add" | "and" | "div" | "eq" | "gt" | "gte" | "lt" | "lte" | "mod" | "mul" | "ne" | "or" | "sub";
  right: ASTExpression;
}

/** A literal of the type bool. */
export interface ASTBool {
  kind: "lit";
  type: "bool";
  value: boolean;
}

/** A call of a function. */
export interface ASTCall {
  /** The arguments, in order, [] for none. */
  args: Array<ASTExpression>;
  /** The function's name. A runtime refuses at load a name that it does not know (PRD §6.4). */
  function: string;
  kind: "call";
}

/** The AST of an expression: a condition's, an operand, a call's argument, or an assignment's value. */
export type ASTExpression = ASTBool | ASTInt | ASTFloat | ASTString | ASTVar | ASTAttr | ASTNodeRef | ASTUnary | ASTBinary | ASTCall;

/** A literal of the type float. */
export interface ASTFloat {
  kind: "lit";
  type: "float";
  value: number;
}

/** A literal of the type int. */
export interface ASTInt {
  kind: "lit";
  type: "int";
  value: number;
}

/** A node reference literal, node("nd_…") (§5.8.4). */
export interface ASTNodeRef {
  kind: "node_ref";
  ref: NodeId;
}

/** The AST of a script: its statements, in order. */
export interface ASTScript {
  kind: "script";
  /** The script's statements, in order, [] for none. */
  statements: Array<ASTStatement>;
}

/** A statement of a script: an assignment or a call. */
export type ASTStatement = ASTAssign | ASTCall;

/** A literal of the type string. */
export interface ASTString {
  kind: "lit";
  type: "string";
  value: string;
}

/** What an assignment assigns to: a variable or a component's runtime attribute. */
export type ASTTarget = ASTVar | ASTAttr;

/** A unary operator, not (!) or neg (-), applied to its operand. */
export interface ASTUnary {
  kind: "unary";
  op: "neg" | "not";
  operand: ASTExpression;
}

/** A variable. */
export interface ASTVar {
  kind: "var";
  /** The variable's key, for readability: the AST names the variable by its ref. */
  name: string;
  ref: VariableId;
}

/** Any JSON value but null, at any depth: what an object may hold of a property that this version of the format does not define, which a consumer ignores (PRD §6.4). */
export type AnyValue = boolean | number | string | Array<AnyValue> | { [key: string]: AnyValue };

/** An asset's ID, in its canonical spelling (PRD §5.2). */
export type AssetId = string;

/** An attribute definition's ID, in its canonical spelling (PRD §5.2). */
export type AttributeDefId = string;

/** An attribute definition of a component type (FR-COMP-01). */
export type AttributeDefinition = {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: boolean;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  options?: never;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "bool";
} | {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: number;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  options?: never;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "int";
} | {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: number;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  options?: never;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "float";
} | {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: string;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  options?: never;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "string";
} | {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: string;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  options?: never;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "text";
} | {
  /** The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own. */
  default: string;
  id: AttributeDefId;
  /** The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's. */
  key: string;
  name: string;
  /** An enum's options, in their author's order, one of which each of its values is. */
  options: Array<string>;
  /** Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is. */
  runtime: boolean;
  /** The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9). */
  type: "enum";
};

/** Values of attributes, by their attribute definitions' IDs, each of its definition's type. */
export type AttributeValues = { [key: string]: Value };

/** A snapshot's file of one board, boards/<slug>.json: the board, its nodes, and its connections (PRD §6.3, §6.5). */
export interface Board {
  board: BoardInfo;
  /** The board's connections, sorted by ID. */
  connections: Array<Connection>;
  /** The board's nodes, sorted by ID. */
  nodes: Array<Node>;
  /** The version of the published format, 1. */
  schemaVersion: 1;
}

/** A board's ID, in its canonical spelling (PRD §5.2). */
export type BoardId = string;

/** The board: its ID, name, slug, and entry node, as the manifest's index has them. */
export interface BoardInfo {
  /** The board's entry node. Without it, the board has none. */
  entryNodeId?: NodeId;
  id: BoardId;
  name: string;
  slug: string;
}

/** A branch, which picks the first of its outputs whose condition holds (FR-EDIT-04). */
export interface Branch {
  cover?: never;
  id: NodeId;
  isEnding?: never;
  kind?: never;
  /** The script that runs when the flow enters the branch. Without it, none. */
  onEnter?: Script;
  /** The script that runs when the flow leaves the branch. Without it, none. */
  onExit?: Script;
  /** The branch's outputs, in their author's order, which is the order in which their conditions are evaluated, [] for none. */
  outputs: Array<BranchOutput>;
  position: Position;
  speaker?: never;
  tags?: never;
  target?: never;
  text?: never;
  /** The branch's title. Without it, the branch has none. */
  title?: string;
  type: "branch";
}

/** An output of a branch: if or elseif, with a condition, or else, without one (FR-EDIT-04). The connections that leave it name it as their fromPort. */
export type BranchOutput = {
  /** The condition that picks the output. */
  condition: Condition;
  id: BranchOutputId;
  kind: "if";
  /** The output's place among its branch's, from 0. */
  order: number;
} | {
  /** The condition that picks the output. */
  condition: Condition;
  id: BranchOutputId;
  kind: "elseif";
  /** The output's place among its branch's, from 0. */
  order: number;
} | {
  condition?: never;
  id: BranchOutputId;
  kind: "else";
  /** The output's place among its branch's, from 0. */
  order: number;
};

/** A branch output's ID, in its canonical spelling (PRD §5.2). */
export type BranchOutputId = string;

/** A component, with a value of each attribute of its type, runtime or not (FR-COMP-02). */
export interface Component {
  /** A value of each of the attribute definitions of the component's type, by the definition's ID, of its type. */
  attributes: AttributeValues;
  componentTypeId: ComponentTypeId;
  id: ComponentId;
  /** The component's image. Its file is the snapshot's assets/<the ID's body>.<its extension> when the snapshot includes assets. Without it, the component has none. */
  image?: AssetId;
  /** The key by which expressions name the component, unique among the project's. */
  key: string;
  name: string;
}

/** A component's ID, in its canonical spelling (PRD §5.2). */
export type ComponentId = string;

/** A component type, with its attribute definitions (FR-COMP-01). */
export interface ComponentType {
  /** The type's attribute definitions, in their author's order, [] for none. */
  attributes: Array<AttributeDefinition>;
  id: ComponentTypeId;
  key: string;
  name: string;
}

/** A component type's ID, in its canonical spelling (PRD §5.2). */
export type ComponentTypeId = string;

/** A snapshot's project/component_types.json: the component types, with their attribute definitions (PRD §6.3). */
export interface ComponentTypes {
  /** Every component type that a component of the snapshot is of, sorted by ID. */
  componentTypes: Array<ComponentType>;
  /** The version of the published format, 1. */
  schemaVersion: 1;
}

/** A snapshot's project/components.json: the components, with their values (PRD §6.3). */
export interface Components {
  /** Every component, sorted by ID. */
  components: Array<Component>;
  /** The version of the published format, 1. */
  schemaVersion: 1;
}

/** A condition: its source, and its AST, an expression, whose names are resolved to the IDs of what they name (PRD §5.8.5, §5.8.8). */
export interface Condition {
  ast: ASTExpression;
  /** The source, as its author wrote it, for debugging: a runtime evaluates the AST, and parses no source (PRD §6.1). */
  source: string;
}

/** A connection of a board, from a node's port to a node of the board (FR-EDIT-03). */
export interface Connection {
  /** The condition that makes the connection a choice. Without it, the connection always is. */
  condition?: Condition;
  /** The node that the connection leaves: an element, or a branch by one of its outputs. */
  from: NodeId;
  /** The port that the connection leaves its node by: an element's out, or the ID of a branch's output. */
  fromPort: string;
  id: ConnectionId;
  /** The connection's label, the text of its choice. Without it, the connection has none. */
  label?: Label;
  /** The script that runs when the flow traverses the connection. Without it, none. */
  onTraverse?: Script;
  /** The connection's place among those that leave its port, from 0, the order of its choice. */
  order: number;
  /** The node of the board that the connection leads to. */
  to: NodeId;
}

/** A connection's ID, in its canonical spelling (PRD §5.2). */
export type ConnectionId = string;

/** How many of each entity the snapshot holds. */
export interface Counts {
  boards: number;
  components: number;
  connections: number;
  /** The nodes of every type: elements, branches, and jumpers. */
  nodes: number;
  /** The variables, global and of each board. */
  variables: number;
}

/** The working draft, as it was when it was pulled, which has neither an ID nor a version (PRD §7.8). */
export interface DraftSnapshot {
  channel: "draft";
  /** The hash of the draft's content when it was pulled. */
  contentHash: Hash;
  id?: never;
  note?: never;
  publishedAt?: never;
  pulledAt: Timestamp;
  version?: never;
}

/** An element: a line of dialogue or of narration (FR-EDIT-02). */
export interface Element {
  /** The element's cover image. Its file is the snapshot's assets/<the ID's body>.<its extension> when the snapshot includes assets. Without it, the element has none. */
  cover?: AssetId;
  id: NodeId;
  /** Whether the flow ends at the element (PRD §5.8.5). */
  isEnding: boolean;
  /** Dialogue, which its speaker says, or narration. */
  kind: "dialogue" | "narration";
  /** The script that runs when the flow enters the element. Without it, none. */
  onEnter?: Script;
  /** The script that runs when the flow leaves the element, by a connection or by ending there. Without it, none. */
  onExit?: Script;
  outputs?: never;
  position: Position;
  /** The component that says a line of dialogue. Without it, the element has none. */
  speaker?: ComponentId;
  /** The element's tags, as their author wrote them, in their order, no two the same but for their case. Without them, none. */
  tags?: Array<string>;
  target?: never;
  /** The element's text. Without it, the element's text is empty. */
  text?: Text;
  /** The element's title. Without it, the element has none. */
  title?: string;
  type: "element";
}

/** Where the flow starts: a node, and its board. */
export interface Entry {
  boardId: BoardId;
  /** A node of the board. */
  nodeId: NodeId;
}

/** What wrote the snapshot. */
export interface GeneratorInfo {
  /** What wrote the snapshot, storycreep for the server. */
  name: string;
  /** Its version. */
  version: string;
}

/** sha256: and the lowercase hexadecimal SHA-256 of the bytes. */
export type Hash = string;

/** A jumper, which continues the flow at its target, as if the flow had reached the target itself (PRD §5.8.5). */
export interface Jumper {
  cover?: never;
  id: NodeId;
  isEnding?: never;
  kind?: never;
  onEnter?: never;
  onExit?: never;
  outputs?: never;
  position: Position;
  speaker?: never;
  tags?: never;
  /** The node that the jumper continues the flow at. Without it, the jumper has none, which only a draft may lack. */
  target?: JumperTarget;
  text?: never;
  /** The jumper's title. Without it, the jumper has none. */
  title?: string;
  type: "jumper";
}

/** The node that a jumper continues the flow at, and its board. */
export interface JumperTarget {
  /** The node's board, which may be the jumper's own. */
  boardId: BoardId;
  /** A node of the board, which is no jumper. */
  nodeId: NodeId;
}

/** A connection's label, the text of its choice: its segments, at least one. */
export interface Label {
  /** The rich text's segments, in order, at least one: an empty rich text is left out. */
  segments: Array<Segment>;
}

/** A snapshot's manifest, storycreep.json: what the snapshot is, its boards, and the hash of each of its files (PRD §6.3, §6.5). */
export interface Manifest {
  /** The highest ast_version among the constructs that the snapshot's expressions use, 1 for none (FR-EXPR-04). A runtime that supports a lower one refuses the snapshot (PRD §6.4). */
  astVersion: number;
  /** Every board, sorted by ID, by which a runtime finds a board's file without reading any other board's (PRD §6.5). */
  boards: Array<ManifestBoard>;
  counts: Counts;
  /** Where the flow starts: the project's entry node, of its entry board. Without it, the project has none. */
  entry?: Entry;
  /** The hash of each of the snapshot's files but the manifest, by its path, which uses forward slashes. */
  files: { [key: string]: Hash };
  generator: GeneratorInfo;
  project: ProjectInfo;
  /** The version of the published format, 1. */
  schemaVersion: 1;
  snapshot: Snapshot;
}

/** A board of the manifest's index: its ID, slug, name, entry node, and file. */
export interface ManifestBoard {
  /** The board's entry node, where the flow enters the board when it starts there. Without it, the board has none. */
  entryNodeId?: NodeId;
  /** The path of the board's file, boards/ and its slug and .json, which the manifest's files list too. */
  file: string;
  id: BoardId;
  name: string;
  slug: string;
}

/** A node of a board: an element, a branch, or a jumper, which its type tells (FR-EDIT-02). */
export type Node = Element | Branch | Jumper;

/** A node's ID, in its canonical spelling (PRD §5.2). */
export type NodeId = string;

/** Where the node is on its board's canvas, to 2 decimal places (PRD §6.2). */
export interface Position {
  /** Rightward, in units of the canvas. */
  x: number;
  /** Downward, in units of the canvas. */
  y: number;
}

/** A project's ID, in its canonical spelling (PRD §5.2). */
export type ProjectId = string;

/** The snapshot's project. */
export interface ProjectInfo {
  id: ProjectId;
  name: string;
  slug: string;
}

/** A published release. */
export interface ReleaseSnapshot {
  channel: "release";
  /** The hash of the snapshot's content. */
  contentHash: Hash;
  id: SnapshotId;
  /** The note that the release was published with. */
  note: string;
  publishedAt: Timestamp;
  pulledAt?: never;
  /** The release's version, which its author chose. */
  version: string;
}

/** A script: its source, and its AST, a script node, whose names are resolved to the IDs of what they name (PRD §5.8.5, §5.8.8). */
export interface Script {
  ast: ASTScript;
  /** The source, as its author wrote it, for debugging: a runtime evaluates the AST, and parses no source (PRD §6.1). */
  source: string;
}

/** A segment of a rich text. */
export type Segment = SegmentText | SegmentMention | SegmentVariable | SegmentAttribute | SegmentBreak | SegmentParagraph | SegmentBulletItem | SegmentOrderedItem;

/** A component's runtime attribute's value, which the text shows as the runtime renders it (FR-TEXT-02). */
export interface SegmentAttribute {
  attribute: AttributeDefId;
  /** The number of digits after the point that a number is shown with, rounded half away from zero. A value that is not a number is shown as it is. */
  decimals?: number;
  /** The URL of the link that the segment is part of. */
  href?: string;
  kind: "value";
  marks: SegmentMarks;
  /** The value's name as an expression names it, as met_elara or elara.trust, for debugging: the segment reads the value by its ref. */
  name: string;
  ref: ComponentId;
}

/** A line break. */
export interface SegmentBreak {
  kind: "break";
}

/** An item of a bulleted list begins. */
export interface SegmentBulletItem {
  /** The item's depth, 1 for an item of a list that no list holds. */
  depth: number;
  kind: "item";
  list: "bullet";
  number?: never;
}

/** An inline segment's marks, each at most once, in alphabetical order, [] for none. link is there exactly when href is. */
export type SegmentMarks = Array<"bold" | "code" | "italic" | "link" | "strike">;

/** A mention of a component. */
export interface SegmentMention {
  /** The URL of the link that the segment is part of. */
  href?: string;
  kind: "mention";
  marks: SegmentMarks;
  ref: ComponentId;
  /** The component's name when the snapshot was published. */
  value: string;
}

/** An item of a numbered list begins. */
export interface SegmentOrderedItem {
  /** The item's depth, 1 for an item of a list that no list holds. */
  depth: number;
  kind: "item";
  list: "ordered";
  /** The item's number. */
  number: number;
}

/** A paragraph begins. */
export interface SegmentParagraph {
  /** The depth of the list item that the paragraph belongs to, or none for a paragraph of no item. */
  depth?: number;
  kind: "paragraph";
}

/** Text. */
export interface SegmentText {
  /** The URL of the link that the segment is part of. */
  href?: string;
  kind: "text";
  marks: SegmentMarks;
  /** The text. */
  value: string;
}

/** A variable's value, which the text shows as the runtime renders it (FR-TEXT-02). */
export interface SegmentVariable {
  attribute?: never;
  /** The number of digits after the point that a number is shown with, rounded half away from zero. A value that is not a number is shown as it is. */
  decimals?: number;
  /** The URL of the link that the segment is part of. */
  href?: string;
  kind: "value";
  marks: SegmentMarks;
  /** The value's name as an expression names it, as met_elara or elara.trust, for debugging: the segment reads the value by its ref. */
  name: string;
  ref: VariableId;
}

/** A snapshot's project/settings.json: the project's entry and the signals that its scripts emit (PRD §6.3). */
export interface Settings {
  /** Where the flow starts, as the manifest's entry. Without it, the project has none. */
  entry?: Entry;
  /** The version of the published format, 1. */
  schemaVersion: 1;
  /** The names of the signals that the project's scripts emit with signal(), each once, sorted by their code points, [] for none (FR-EXPR-04). */
  signals: Array<string>;
}

/** What the snapshot is: a published release, or the working draft as it was when it was pulled, which its channel tells (PRD §7.8). */
export type Snapshot = ReleaseSnapshot | DraftSnapshot;

/** A snapshot's ID, in its canonical spelling (PRD §5.2). */
export type SnapshotId = string;

/** An element's text: its segments, at least one, and its preview (PRD §6.5). */
export interface Text {
  /** The text's plain text as one line, cut after 100 characters with an ellipsis, as schema/rich-text/README.md says. */
  preview: string;
  /** The rich text's segments, in order, at least one: an empty rich text is left out. */
  segments: Array<Segment>;
}

/** A moment, in UTC, to the second (RFC 3339), as 2026-10-12T18:04:31Z. */
export type Timestamp = string;

/** A value, which its declaration's type says how to read, never its JSON form (PRD §5.8.9): a boolean, a number, or a string. */
export type Value = boolean | number | string;

/** A variable's declaration (FR-VAR-01, FR-VAR-02). */
export type Variable = {
  boardId?: never;
  /** The variable's default, of its type. */
  default: boolean;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  resetOnEnter?: never;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "global";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "bool";
} | {
  boardId?: never;
  /** The variable's default, of its type. */
  default: number;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  resetOnEnter?: never;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "global";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "int";
} | {
  boardId?: never;
  /** The variable's default, of its type. */
  default: number;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  resetOnEnter?: never;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "global";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "float";
} | {
  boardId?: never;
  /** The variable's default, of its type. */
  default: string;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  resetOnEnter?: never;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "global";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "string";
} | {
  /** The board that the variable is of. */
  boardId: BoardId;
  /** The variable's default, of its type. */
  default: boolean;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  /** Whether the variable is reset to its default whenever the flow enters its board (PRD §5.8.5). Without it, false. */
  resetOnEnter?: boolean;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "board";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "bool";
} | {
  /** The board that the variable is of. */
  boardId: BoardId;
  /** The variable's default, of its type. */
  default: number;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  /** Whether the variable is reset to its default whenever the flow enters its board (PRD §5.8.5). Without it, false. */
  resetOnEnter?: boolean;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "board";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "int";
} | {
  /** The board that the variable is of. */
  boardId: BoardId;
  /** The variable's default, of its type. */
  default: number;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  /** Whether the variable is reset to its default whenever the flow enters its board (PRD §5.8.5). Without it, false. */
  resetOnEnter?: boolean;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "board";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "float";
} | {
  /** The board that the variable is of. */
  boardId: BoardId;
  /** The variable's default, of its type. */
  default: string;
  /** What the variable is for. Without it, the variable has no description. */
  description?: string;
  id: VariableId;
  /** The key by which expressions and game code name the variable, unique among its scope's. */
  key: string;
  /** Whether the variable is reset to its default whenever the flow enters its board (PRD §5.8.5). Without it, false. */
  resetOnEnter?: boolean;
  /** Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02). */
  scope: "board";
  /** The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9). */
  type: "string";
};

/** A variable's ID, in its canonical spelling (PRD §5.2). */
export type VariableId = string;

/** A snapshot's project/variables.json: every variable, global and of each board (PRD §6.3, §6.5). */
export interface Variables {
  /** The version of the published format, 1. */
  schemaVersion: 1;
  /** Every variable, sorted by ID. */
  variables: Array<Variable>;
}
