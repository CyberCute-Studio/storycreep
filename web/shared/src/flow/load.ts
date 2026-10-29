// The loader of a published snapshot (PRD §6.3, §8.6): the TypeScript twin
// of the Go package server/internal/flow's Load, which loads what it loads
// of every snapshot that the server writes, and refuses what it refuses, as
// play mode's runtime and the Godot addon's do; beyond it, it checks each
// file against its JSON Schema, which Go's Load does not, so that it refuses
// a file that breaks a pattern or a bound that the Go types do not check as
// they read a file, which no server writes, though Go's Load may load it.

import boardSchema from "../../../../schema/published/v1/board.schema.json" with {
  type: "json",
};
import componentTypesSchema from "../../../../schema/published/v1/component_types.schema.json" with {
  type: "json",
};
import componentsSchema from "../../../../schema/published/v1/components.schema.json" with {
  type: "json",
};
import settingsSchema from "../../../../schema/published/v1/settings.schema.json" with {
  type: "json",
};
import manifestSchema from "../../../../schema/published/v1/storycreep.schema.json" with {
  type: "json",
};
import variablesSchema from "../../../../schema/published/v1/variables.schema.json" with {
  type: "json",
};
import {
  AST_VERSION,
  type Context,
  checkResolved,
  DecodeError,
  decode,
  ExprError,
  isJsonObject,
  JsonError,
  type JsonValue,
  type Node,
  Project,
  type Component as ProjectComponent,
  plain,
  readJSONBytes,
  readValue,
  type Script,
  UsageError,
  type Value,
  versionOf,
} from "../expr";
import type {
  Board as BoardFile,
  Components,
  ComponentType,
  ComponentTypes,
  Entry,
  JumperTarget,
  Manifest,
  ManifestBoard,
  Segment,
  Settings,
  Variable,
  Variables,
} from "../published";
import { foldCase } from "./fold";
import { type Schema, validate } from "./schema";
import { sha256Yielding } from "./sha256";

/** The paths of a snapshot's files but its boards' and its assets'. */
export const MANIFEST_PATH = "storycreep.json";
export const SETTINGS_PATH = "project/settings.json";
export const VARIABLES_PATH = "project/variables.json";
export const COMPONENT_TYPES_PATH = "project/component_types.json";
export const COMPONENTS_PATH = "project/components.json";

/** Returns the path of the file of the board whose slug is slug. */
export function boardPath(slug: string): string {
  return `boards/${slug}.json`;
}

/** load's error for files that are not a snapshot that a runtime runs. */
export class LoadError extends Error {
  constructor(message: string) {
    super(`flow: not a snapshot that a runtime runs: ${message}`);
    this.name = "LoadError";
  }
}

/**
 * load's error, a LoadError, for a snapshot whose astVersion is newer than
 * AST_VERSION, the runtime's ast_unsupported (FR-EXPR-04 AC4, PRD §6.4),
 * which a newer runtime may run.
 */
export class ASTUnsupportedError extends LoadError {
  readonly code = "ast_unsupported";

  constructor(message: string) {
    super(`ast_unsupported: ${message}`);
    this.name = "ASTUnsupportedError";
  }
}

/** A connection of a board, its expressions decoded. */
export interface Connection {
  readonly id: string;
  readonly from: string;
  readonly fromPort: string;
  readonly to: string;
  readonly order: number;
  readonly label: readonly Segment[] | undefined;
  readonly condition: Node | undefined;
  readonly onTraverse: Script | undefined;
}

/** A branch's output, its condition decoded. */
export interface Output {
  readonly id: string;
  readonly kind: "if" | "elseif" | "else";
  readonly condition: Node | undefined;
}

/** What every node of a snapshot has: its board, and the connections that leave each of its ports, in order. */
interface NodeBase {
  readonly id: string;
  readonly board: string;
  /** The connections that leave each of its ports, out or a branch's output's ID. */
  readonly ports: ReadonlyMap<string, readonly Connection[]>;
}

/** A node of a snapshot: an element, a branch, or a jumper. */
export type SnapshotNode = NodeBase &
  (
    | {
        readonly type: "element";
        readonly isEnding: boolean;
        readonly text: readonly Segment[] | undefined;
        readonly onEnter: Script | undefined;
        readonly onExit: Script | undefined;
      }
    | {
        readonly type: "branch";
        readonly outputs: readonly Output[];
        readonly onEnter: Script | undefined;
        readonly onExit: Script | undefined;
      }
    | { readonly type: "jumper"; readonly target: JumperTarget | undefined }
  );

/** An element's port, which its connections leave by. */
export const OUT = "out";

/** Returns the key of a component's runtime attribute. */
export function attributeKey(component: string, attribute: string): string {
  return `${component}/${attribute}`;
}

/**
 * A published snapshot, loaded and checked (load), which a Runtime runs and
 * never changes, so that several runtimes may run one at once.
 */
export class Snapshot {
  /** The manifest. */
  readonly manifest: Manifest;
  /** The project's entry, or undefined. */
  readonly entry: Entry | undefined;
  /** The variables' IDs, in the order of their file. */
  readonly variables: readonly string[];
  /** The variables' declarations, by ID. */
  readonly declared: ReadonlyMap<string, Variable>;
  /** The variables' values in the snapshot, by ID. */
  readonly defaults: ReadonlyMap<string, Value>;
  /**
   * Each component's runtime attributes, in the order of the components'
   * file and of their type's definitions, as [component, attribute].
   */
  readonly attributes: readonly (readonly [string, string])[];
  /** The runtime attributes' values in the snapshot, by attributeKey. */
  readonly initial: ReadonlyMap<string, Value>;
  /** The runtime attributes' types, as their definitions declare them, by attributeKey. */
  readonly declaredTypes: ReadonlyMap<
    string,
    ComponentType["attributes"][number]["type"]
  >;
  /** The options of the runtime attributes that are enums, by attributeKey. */
  readonly options: ReadonlyMap<string, readonly string[]>;
  /** The boards, by ID: the index's entry, and the variables reset on entering it. */
  readonly boards: ReadonlyMap<
    string,
    { readonly info: ManifestBoard; readonly reset: readonly string[] }
  >;
  /** The nodes, by ID. */
  readonly nodes: ReadonlyMap<string, SnapshotNode>;

  /** @internal Use load. */
  constructor(s: {
    manifest: Manifest;
    entry: Entry | undefined;
    variables: string[];
    declared: Map<string, Variable>;
    defaults: Map<string, Value>;
    attributes: [string, string][];
    initial: Map<string, Value>;
    declaredTypes: Map<string, ComponentType["attributes"][number]["type"]>;
    options: Map<string, string[]>;
    boards: Map<string, { info: ManifestBoard; reset: string[] }>;
    nodes: Map<string, SnapshotNode>;
  }) {
    this.manifest = s.manifest;
    this.entry = s.entry;
    this.variables = s.variables;
    this.declared = s.declared;
    this.defaults = s.defaults;
    this.attributes = s.attributes;
    this.initial = s.initial;
    this.declaredTypes = s.declaredTypes;
    this.options = s.options;
    this.boards = s.boards;
    this.nodes = s.nodes;
  }
}

/**
 * Reads the snapshot that files holds, by their paths, as a game's
 * res://storycreep/ holds one (PRD §6.3), from its manifest,
 * storycreep.json, and checks it as the Go runtime's Load does: every item
 * of what schema/published/README.md says a consumer checks beyond the
 * schemas, each file's hash among them, and a node's tags compared as their
 * case folds; and beyond that list, what the runtime relies on: that the
 * snapshot's astVersion is at most AST_VERSION, which a newer one is refused
 * for as the runtime's ast_unsupported, with an ASTUnsupportedError, and at
 * least the highest version of the constructs that its ASTs use (versionOf);
 * that a text's or a label's value is of a variable that an expression of
 * its board reads (FR-TEXT-02, §5.8.3), or of a runtime attribute; and that
 * every value is of its declaration's type, a string, an enum's among them,
 * of at most 4,096 characters without a NUL, as the language's are. Beyond
 * what Go's Load checks, it checks each file that it reads against its JSON
 * Schema, of schema/published/v1, before it reads anything of it, so that
 * it refuses a file that breaks a pattern or a bound of its schema, which
 * the server never writes, though Go's Load, which is no validator of the
 * schemas, may load it. It rejects with a LoadError, and of a snapshot that
 * is wrong in several ways always the same one, as it reads the files and
 * checks them in the order of Go's Load, each file hashed as it reads it,
 * the files that it does not read, as the assets, last, and each board's
 * ASTs decoded as it reads the board's file. It is asynchronous since it
 * hashes the files with the platform's SHA-256, one after another, so that
 * it holds copies of one file at most, or, in a page that is no secure
 * context, which has none, with its own, which lets the page run between
 * two parts of a large file (sha256Yielding); and since it takes each file
 * from a function, if it is given one, only once it needs it
 * (SnapshotFiles). Once the signal of its options is aborted, it rejects
 * with its reason, as fetch does, before it takes or hashes any more of the
 * files.
 */
export async function load(
  files: SnapshotFiles,
  options: LoadOptions = {},
): Promise<Snapshot> {
  return new Loader(files, options.signal).load();
}

/**
 * The files of a snapshot, by their paths, as load takes them: a map of
 * their bytes, or a function that returns the bytes of the file of a path,
 * or undefined when there is none, which load calls, with the signal of its
 * options, once for each file that it reads or hashes, as it comes to it,
 * in the order of the Go runtime's Load, so that a page may fetch each file
 * only once load asks for it, and let go of an asset once load has hashed
 * it, as Go's Load streams each file of its fs.FS, rather than hold every
 * file of a snapshot at once.
 */
export type SnapshotFiles =
  | ReadonlyMap<string, Uint8Array>
  | ((
      path: string,
      signal: AbortSignal | undefined,
    ) => Promise<Uint8Array | undefined>);

/** load's options. */
export interface LoadOptions {
  /** The signal that stops load once it is aborted. */
  readonly signal?: AbortSignal;
}

// digest returns the hash of bytes as the manifest lists a file's: sha256:
// and the lowercase hexadecimal SHA-256 of the bytes, by WebCrypto's, which
// copies them, or, where the page is no secure context and has none
// (crypto.subtle), by sha256Yielding's, which copies those of a large file
// too, and stops once signal is aborted.
async function digest(
  bytes: Uint8Array<ArrayBuffer>,
  signal: AbortSignal | undefined,
): Promise<string> {
  const subtle: SubtleCrypto | undefined = globalThis.crypto?.subtle;
  const sum =
    subtle === undefined
      ? await sha256Yielding(bytes, signal)
      : new Uint8Array(await subtle.digest("SHA-256", bytes));
  return `sha256:${Array.from(sum, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

// The names that Windows reserves for devices, which no slug may be.
const devices = new Set([
  "aux",
  "con",
  "nul",
  "prn",
  ...Array.from({ length: 10 }, (_, i) => `com${i}`),
  ...Array.from({ length: 10 }, (_, i) => `lpt${i}`),
]);

// isFilePath reports whether path, which the manifest's schema holds to the
// forms of the pull's path rules (PRD §7.5), is a board's of a slug of at
// most 64 characters that is no name that Windows reserves for a device, or
// another file's.
function isFilePath(path: string): boolean {
  if (!path.startsWith("boards/")) {
    return true;
  }
  const slug = path.slice("boards/".length, -".json".length);
  return slug.length <= 64 && !devices.has(slug);
}

// The schemas of the files.
const schemas = {
  manifest: manifestSchema as Schema,
  settings: settingsSchema as Schema,
  variables: variablesSchema as Schema,
  componentTypes: componentTypesSchema as Schema,
  components: componentsSchema as Schema,
  board: boardSchema as Schema,
};

// fail returns the LoadError of the problem.
function fail(problem: string): LoadError {
  return new LoadError(problem);
}

// json returns the JSON of a file's bytes (readJSONBytes), or the JsonError
// of bytes that are not one JSON value.
function json(bytes: Uint8Array): JsonValue | JsonError {
  try {
    return readJSONBytes(bytes);
  } catch (e) {
    if (e instanceof JsonError) {
      return e;
    }
    throw e;
  }
}

// Loader is what load keeps while it reads a snapshot's files: the IDs of the
// entities that it has read, of which no two are one, the components' IDs,
// the paths of the files that it has read, the raw JSON of what it has read
// of them, the ASTs of the boards' expressions, and the ASTs that it has
// checked, whose constructs' versions the manifest's astVersion covers.
class Loader {
  private readonly entities = new Set<string>();
  private readonly components = new Set<string>();
  private readonly read = new Set<string>();
  // origins are the raw JSON of each object and array of the files that it
  // has read but the boards', whose numbers are as they are written, by the
  // copy that JSON.parse would read, typed by its schema (plain), which the
  // loader reads the rest of. Those of a board's file are readBoard's, which
  // it lets go of once it has decoded the board's expressions.
  private readonly origins = new Map<object, JsonValue>();
  // decoded are the ASTs of the boards' expressions, by the typed copy of
  // each expression's object.
  private readonly decoded = new Map<object, Node>();
  private readonly asts: Node[] = [];
  private manifestFiles: Record<string, string> = {};
  // release is set for a release's snapshot, every jumper of which has a
  // target.
  private release = false;
  // What the snapshot holds, as load builds it.
  private readonly declared = new Map<string, Variable>();
  private readonly defaults = new Map<string, Value>();
  private readonly variableIds: string[] = [];
  private readonly attributes: [string, string][] = [];
  private readonly initial = new Map<string, Value>();
  private readonly declaredTypes = new Map<
    string,
    ComponentType["attributes"][number]["type"]
  >();
  private readonly options = new Map<string, string[]>();
  // projectComponents are the components, each with its type's attribute
  // definitions, as what the snapshot's expressions can name holds them
  // (project), which declare adds in the order of their file.
  private readonly projectComponents: ProjectComponent[] = [];
  private readonly boards = new Map<
    string,
    { info: ManifestBoard; reset: string[] }
  >();
  private readonly nodes = new Map<string, SnapshotNode>();
  // ports are the ports of each node, by its ID, which its node holds too,
  // to which addConnections adds the connections that leave them.
  private readonly ports = new Map<string, Map<string, Connection[]>>();

  // open returns the bytes of the file of a path, or undefined.
  private readonly open: (
    path: string,
    signal: AbortSignal | undefined,
  ) => Promise<Uint8Array | undefined>;

  constructor(
    files: SnapshotFiles,
    private readonly signal: AbortSignal | undefined,
  ) {
    this.open =
      typeof files === "function" ? files : async (path) => files.get(path);
  }

  // bytes returns the bytes of the file of the path, or undefined when there
  // is none, once it has checked, before it opens the file and once it has,
  // that the signal is not aborted. It returns a view of a fixed-length
  // ArrayBuffer, the one buffer that WebCrypto and a browser's TextDecoder
  // both read a view of: of a view over a resizable ArrayBuffer or a
  // SharedArrayBuffer, a copy, made at once, so that it is of the bytes as
  // they are when the file is opened.
  private async bytes(
    path: string,
  ): Promise<Uint8Array<ArrayBuffer> | undefined> {
    this.signal?.throwIfAborted();
    const bytes = await this.open(path, this.signal);
    this.signal?.throwIfAborted();
    if (bytes === undefined) {
      return undefined;
    }
    const buffer = bytes.buffer;
    return buffer instanceof ArrayBuffer && !buffer.resizable
      ? (bytes as Uint8Array<ArrayBuffer>)
      : bytes.slice();
  }

  // parse checks raw, the JSON of the file of the path, or the problem of
  // its bytes, against the schema, and returns it as JSON.parse would have
  // read it, typed by the schema, whose objects and arrays are recorded with
  // their raw JSON in origins.
  private parse<T>(
    path: string,
    raw: JsonValue | JsonError,
    schema: Schema,
    origins: Map<object, JsonValue>,
  ): T {
    if (raw instanceof JsonError) {
      throw fail(`${path}: it ${raw.message}`);
    }
    const problem = validate(schema, raw);
    if (problem !== undefined || !isJsonObject(raw)) {
      throw fail(
        `${path}: ${problem ?? "is not an object"}, which its schema does not take`,
      );
    }
    return plain(raw, origins) as T;
  }

  // raw returns the raw JSON, whose numbers are as they are written, of x,
  // an object of a file that the loader has read, as parse returned it.
  private raw(x: object): { [key: string]: JsonValue } {
    return this.origins.get(x) as { [key: string]: JsonValue };
  }

  // manifest reads the manifest, and checks its astVersion and its files'
  // paths.
  private async manifest(): Promise<Manifest> {
    const bytes = await this.bytes(MANIFEST_PATH);
    if (bytes === undefined) {
      throw fail(`there is no ${MANIFEST_PATH}`);
    }
    const m = this.parse<Manifest>(
      MANIFEST_PATH,
      json(bytes),
      schemas.manifest,
      this.origins,
    );
    if (m.astVersion > AST_VERSION) {
      throw new ASTUnsupportedError(
        `the snapshot's astVersion is ${m.astVersion}, and this runtime supports ${AST_VERSION}`,
      );
    }
    for (const path of Object.keys(m.files).sort()) {
      if (!isFilePath(path)) {
        throw fail(
          `the manifest lists ${JSON.stringify(path)}, which is none of the paths of a snapshot's files (PRD §7.5)`,
        );
      }
    }
    return m;
  }

  // contents checks that the manifest's files list the file of the path
  // with the hash of its bytes, and returns what read returns of them. It
  // hashes the bytes, and has read read them, as they are when the file is
  // opened, before it awaits the hash, so that what it returns is of the
  // bytes that it hashed, whatever the files or the bytes become meanwhile.
  // It awaits the hash whatever read does, and refuses a file of another
  // hash before it throws what read threw, as Go's Load checks a file's hash
  // before it reads it.
  private async contents<R>(
    path: string,
    read: (bytes: Uint8Array) => R,
  ): Promise<R> {
    if (!Object.hasOwn(this.manifestFiles, path)) {
      throw fail(`${path} is not among the manifest's files`);
    }
    const want = this.manifestFiles[path];
    const bytes = await this.bytes(path);
    if (bytes === undefined) {
      throw fail(`there is no ${path}`);
    }
    const sum = digest(bytes, this.signal);
    let r: { value: R } | { thrown: unknown };
    try {
      r = { value: read(bytes) };
    } catch (e) {
      r = { thrown: e };
    }
    const got = await sum;
    this.signal?.throwIfAborted();
    if (got !== want) {
      throw fail(
        `${path}: its hash is ${got}, not the manifest's ${JSON.stringify(want)}`,
      );
    }
    if ("thrown" in r) {
      throw r.thrown;
    }
    return r.value;
  }

  // readFile reads the file of the path, once it has checked its hash, and
  // records the raw JSON of its objects and arrays in origins.
  private async readFile<T>(
    path: string,
    schema: Schema,
    origins = this.origins,
  ): Promise<T> {
    const raw = await this.contents(path, json);
    this.read.add(path);
    return this.parse<T>(path, raw, schema, origins);
  }

  // readBoard reads the board file of the path, and decodes its
  // expressions' ASTs as the Go runtime's reader of a file,
  // published.Decode, decodes them as it reads it. It decodes those of the
  // keys that each object's type defines, and ignores the same keys of a
  // type that does not define them, as an element's condition, as it
  // ignores any key that it does not know (§6.4). It decodes them in the
  // order of their keys sorted, in which Go reads them whatever order the
  // file writes them in: the connections', each's condition, then its
  // onTraverse, then the nodes', each's onEnter, then its onExit, then a
  // branch's outputs' conditions, so that it refuses the first that it
  // cannot decode.
  private async readBoard(path: string): Promise<BoardFile> {
    // The raw JSON of the file, of which it reads the expressions' alone,
    // which it lets go of once it has decoded them, so that it holds that of
    // one board's file at a time.
    const origins = new Map<object, JsonValue>();
    const f = await this.readFile<BoardFile>(path, schemas.board, origins);
    // expression decodes the AST of e, the expression of the key of the
    // object whose ID is id, if it has one, in the context ctx.
    const expression = (
      id: string,
      key: string,
      e: object | undefined,
      ctx: Context,
    ) => {
      if (e !== undefined) {
        this.decoded.set(
          e,
          this.ast(`${path}: the ${key} of ${id}`, origins.get(e), ctx),
        );
      }
    };
    for (const c of f.connections) {
      expression(c.id, "condition", c.condition, "condition");
      expression(c.id, "onTraverse", c.onTraverse, "script");
    }
    for (const n of f.nodes) {
      if (n.type === "jumper") {
        continue;
      }
      expression(n.id, "onEnter", n.onEnter, "script");
      expression(n.id, "onExit", n.onExit, "script");
      if (n.type === "branch") {
        for (const o of n.outputs) {
          expression(o.id, "condition", o.condition, "condition");
        }
      }
    }
    return f;
  }

  // entity throws when id is the ID of an entity that the loader has read
  // already, and otherwise records it.
  private entity(id: string, where: string): void {
    if (this.entities.has(id)) {
      throw fail(`${where}: ${id} is the ID of two of its entities`);
    }
    this.entities.add(id);
  }

  async load(): Promise<Snapshot> {
    const m = await this.manifest();
    this.manifestFiles = m.files;
    this.release = m.snapshot.channel === "release";
    const settings = await this.readFile<Settings>(
      SETTINGS_PATH,
      schemas.settings,
    );
    const variables = await this.readFile<Variables>(
      VARIABLES_PATH,
      schemas.variables,
    );
    const types = await this.readFile<ComponentTypes>(
      COMPONENT_TYPES_PATH,
      schemas.componentTypes,
    );
    const components = await this.readFile<Components>(
      COMPONENTS_PATH,
      schemas.components,
    );
    const boardFiles = new Map<string, BoardFile>();
    for (const b of m.boards) {
      this.entity(b.id, "the index");
      this.boards.set(b.id, { info: b, reset: [] });
      if (b.file !== boardPath(b.slug)) {
        throw fail(
          `the index's board ${b.id}'s file is ${b.file}, not boards/ and its slug and .json`,
        );
      }
      const f = await this.readBoard(b.file);
      const info = f.board;
      if (
        info.id !== b.id ||
        info.name !== b.name ||
        info.slug !== b.slug ||
        info.entryNodeId !== b.entryNodeId
      ) {
        throw fail(
          `${b.file}: its board is not the index's ${b.id}, of its ID, name, slug, or entry node`,
        );
      }
      boardFiles.set(b.id, f);
    }
    // The files that it does not read, as the assets, are hashed too, last,
    // so that every file that the manifest lists is its bytes'.
    for (const path of Object.keys(m.files).sort()) {
      if (!this.read.has(path)) {
        await this.contents(path, () => undefined);
      }
    }
    const signals = settings.signals;
    if (new Set(signals).size !== signals.length) {
      throw fail(`${SETTINGS_PATH}: a signal is there twice`);
    }
    this.declare(variables, types, components);
    for (const b of m.boards) {
      const f = boardFiles.get(b.id) as BoardFile;
      this.addNodes(b.id, b.file, f);
    }
    // What the snapshot's expressions can name, against which its ASTs are
    // checked, of the nodes in the order of the index and of their files.
    const p = this.project(variables, boardFiles, m.boards);
    for (const b of m.boards) {
      const f = boardFiles.get(b.id) as BoardFile;
      this.addConnections(b.id, b.file, f, p);
    }
    const entry = m.entry;
    const settingsEntry = settings.entry;
    if (
      (entry === undefined) !== (settingsEntry === undefined) ||
      (entry !== undefined &&
        settingsEntry !== undefined &&
        (entry.boardId !== settingsEntry.boardId ||
          entry.nodeId !== settingsEntry.nodeId))
    ) {
      throw fail("the manifest's entry is not the settings'");
    }
    if (
      entry !== undefined &&
      this.nodes.get(entry.nodeId)?.board !== entry.boardId
    ) {
      throw fail(
        `the entry ${entry.nodeId} is not a node of its board ${entry.boardId}`,
      );
    }
    for (const b of m.boards) {
      const e = b.entryNodeId;
      if (e !== undefined && this.nodes.get(e)?.board !== b.id) {
        throw fail(
          `the board ${b.id}'s entry node ${e} is not one of its nodes`,
        );
      }
    }
    const counts = {
      boards: m.boards.length,
      components: components.components.length,
      connections: 0,
      nodes: 0,
      variables: variables.variables.length,
    };
    for (const f of boardFiles.values()) {
      counts.nodes += f.nodes.length;
      counts.connections += f.connections.length;
    }
    if (
      m.counts.boards !== counts.boards ||
      m.counts.components !== counts.components ||
      m.counts.connections !== counts.connections ||
      m.counts.nodes !== counts.nodes ||
      m.counts.variables !== counts.variables
    ) {
      throw fail(
        `the manifest's counts are ${JSON.stringify(m.counts)}, but the files hold ${JSON.stringify(counts)}`,
      );
    }
    const v = versionOf(this.asts);
    if (v > m.astVersion) {
      throw fail(
        `the snapshot's astVersion is ${m.astVersion}, though its expressions use constructs of version ${v} (FR-EXPR-04)`,
      );
    }
    return new Snapshot({
      manifest: m,
      entry,
      variables: this.variableIds,
      declared: this.declared,
      defaults: this.defaults,
      attributes: this.attributes,
      initial: this.initial,
      declaredTypes: this.declaredTypes,
      options: this.options,
      boards: this.boards,
      nodes: this.nodes,
    });
  }

  // declare reads the variables, the component types' definitions, and the
  // components' runtime attributes, with their values in the snapshot.
  private declare(
    variables: Variables,
    types: ComponentTypes,
    components: Components,
  ): void {
    for (const v of variables.variables) {
      this.entity(v.id, VARIABLES_PATH);
      if (v.scope === "board") {
        const b = this.boards.get(v.boardId);
        if (b === undefined) {
          throw fail(
            `the variable ${v.id} is of ${v.boardId}, which is no board of the index`,
          );
        }
        if (v.resetOnEnter === true) {
          b.reset.push(v.id);
        }
      }
      const [d, problem] = readValue(this.raw(v).default, v.type);
      if (problem !== undefined) {
        throw fail(`the default of the variable ${v.id} ${problem}`);
      }
      this.variableIds.push(v.id);
      this.declared.set(v.id, v);
      this.defaults.set(v.id, d);
    }
    const definitions = new Map<string, ComponentType["attributes"]>();
    const typeKeys = new Set<string>();
    for (const t of types.componentTypes) {
      this.entity(t.id, COMPONENT_TYPES_PATH);
      if (typeKeys.has(t.key)) {
        throw fail(`two component types have the key ${t.key}`);
      }
      typeKeys.add(t.key);
      this.define(t);
      definitions.set(t.id, t.attributes);
    }
    for (const c of components.components) {
      this.entity(c.id, COMPONENTS_PATH);
      this.components.add(c.id);
      const defs = definitions.get(c.componentTypeId);
      if (defs === undefined) {
        throw fail(
          `the component ${c.id} is of ${c.componentTypeId}, which is no component type of the snapshot`,
        );
      }
      const values = this.raw(c.attributes);
      if (Object.keys(values).length !== defs.length) {
        throw fail(
          `the component ${c.id} has ${Object.keys(values).length} values, not one of each of its type's ${defs.length} definitions`,
        );
      }
      for (const d of defs) {
        const options = d.type === "enum" ? d.options : [];
        const [v, problem] = readValue(
          Object.hasOwn(values, d.id) ? values[d.id] : undefined,
          d.type,
          options,
        );
        if (problem !== undefined) {
          throw fail(
            `the value of ${d.id} of the component ${c.id} ${problem}`,
          );
        }
        if (d.runtime) {
          const k = attributeKey(c.id, d.id);
          this.attributes.push([c.id, d.id]);
          this.initial.set(k, v);
          this.declaredTypes.set(k, d.type);
          if (d.type === "enum") {
            this.options.set(k, d.options);
          }
        }
      }
      this.projectComponents.push({
        id: c.id,
        key: c.key,
        attributes: defs.map((d) => ({
          id: d.id,
          key: d.key,
          type: d.type,
          runtime: d.runtime,
          options: d.type === "enum" ? d.options : [],
        })),
      });
    }
  }

  // define checks the attribute definitions of the component type t: each
  // its own entity, of a key of its own among the type's, a text never
  // runtime, an enum's options each there once, and its default of its type,
  // an enum's one of its options.
  private define(t: ComponentType): void {
    const keys = new Set<string>();
    for (const d of t.attributes) {
      this.entity(d.id, COMPONENT_TYPES_PATH);
      if (keys.has(d.key)) {
        throw fail(
          `two definitions of the component type ${t.id} have the key ${d.key}`,
        );
      }
      if (d.type === "text" && d.runtime) {
        throw fail(`the definition ${d.id} is a text, which is never runtime`);
      }
      const options = d.type === "enum" ? d.options : [];
      if (new Set(options).size !== options.length) {
        throw fail(`the definition ${d.id} has an option twice`);
      }
      keys.add(d.key);
      const [, problem] = readValue(this.raw(d).default, d.type, options);
      if (problem !== undefined) {
        throw fail(`the default of the definition ${d.id} ${problem}`);
      }
    }
  }

  // addNodes adds the nodes of the board b's file f, and their ports, with
  // their scripts' ASTs, as readBoard decoded them.
  private addNodes(b: string, path: string, f: BoardFile): void {
    for (const n of f.nodes) {
      this.entity(n.id, path);
      const ports = new Map<string, Connection[]>();
      this.ports.set(n.id, ports);
      if (n.type === "element") {
        ports.set(OUT, []);
        this.nodes.set(n.id, {
          id: n.id,
          board: b,
          ports,
          type: "element",
          isEnding: n.isEnding,
          text: n.text?.segments,
          onEnter: this.decodedOf(n.onEnter) as Script | undefined,
          onExit: this.decodedOf(n.onExit) as Script | undefined,
        });
      } else if (n.type === "branch") {
        const last = n.outputs.length - 1;
        const outputs: Output[] = n.outputs.map((o, k) => {
          this.entity(o.id, path);
          if (o.order !== k) {
            throw fail(
              `${path}: the branch ${n.id}'s outputs are not ordered 0, 1, 2, and so on`,
            );
          }
          // The first output is the if, and the last may be an else, which
          // the runtime picks once no output before it holds.
          if (
            (o.kind === "if") !== (k === 0) ||
            (o.kind === "else" && k !== last)
          ) {
            throw fail(
              `${path}: the branch ${n.id}'s outputs are not one if, then any elseifs, then an else, if it has one`,
            );
          }
          ports.set(o.id, []);
          return {
            id: o.id,
            kind: o.kind,
            condition: this.decodedOf(o.condition),
          };
        });
        this.nodes.set(n.id, {
          id: n.id,
          board: b,
          ports,
          type: "branch",
          outputs,
          onEnter: this.decodedOf(n.onEnter) as Script | undefined,
          onExit: this.decodedOf(n.onExit) as Script | undefined,
        });
      } else {
        this.nodes.set(n.id, {
          id: n.id,
          board: b,
          ports,
          type: "jumper",
          target: n.target,
        });
      }
    }
  }

  // ast decodes the AST of an expression, of its field's object raw, in the
  // context ctx.
  private ast(where: string, raw: JsonValue | undefined, ctx: Context): Node {
    if (!isJsonObject(raw)) {
      throw fail(`${where}: an expression is not an object`);
    }
    try {
      return decode(raw.ast, ctx);
    } catch (e) {
      if (e instanceof DecodeError) {
        throw fail(`${where}: ${e.message}`);
      }
      throw e;
    }
  }

  // decodedOf returns the AST of the expression e, an object of a board's
  // file, as readBoard decoded it, or undefined of no expression.
  private decodedOf(e: object | undefined): Node | undefined {
    return e === undefined ? undefined : this.decoded.get(e);
  }

  // project returns what the snapshot's expressions can name, of the
  // components as declare has read them, or throws.
  private project(
    variables: Variables,
    boardFiles: Map<string, BoardFile>,
    boards: readonly ManifestBoard[],
  ): Project {
    try {
      return Project.of(
        variables.variables.map((v) => ({
          id: v.id,
          key: v.key,
          type: v.type,
          ...(v.scope === "board" ? { board: v.boardId } : {}),
        })),
        this.projectComponents,
        boards.flatMap((b) =>
          (boardFiles.get(b.id) as BoardFile).nodes.map((n) => ({
            id: n.id,
            jumper: n.type === "jumper",
          })),
        ),
      );
    } catch (e) {
      if (e instanceof UsageError) {
        throw fail(e.message);
      }
      throw e;
    }
  }

  // check checks the AST n of the board b in the context ctx against the
  // snapshot's declarations (checkResolved), and keeps it, so that the
  // manifest's astVersion is checked against its constructs.
  private check(
    where: string,
    n: Node | undefined,
    ctx: Context,
    p: Project,
    b: string,
  ): void {
    if (n === undefined) {
      return;
    }
    try {
      checkResolved(n, ctx, p, b);
    } catch (e) {
      if (e instanceof ExprError || e instanceof UsageError) {
        throw fail(`${where}: ${e.message}`);
      }
      throw e;
    }
    this.asts.push(n);
  }

  // addConnections adds the connections of the board b's file f to their
  // nodes' ports, and checks the board's expressions, texts, and jumpers.
  private addConnections(
    b: string,
    path: string,
    f: BoardFile,
    p: Project,
  ): void {
    for (const c of f.connections) {
      const from = this.nodes.get(c.from);
      const to = this.nodes.get(c.to);
      this.entity(c.id, path);
      if (
        from === undefined ||
        from.board !== b ||
        to === undefined ||
        to.board !== b
      ) {
        throw fail(
          `${path}: the connection ${c.id} does not join two nodes of the board`,
        );
      }
      const port = this.ports.get(c.from)?.get(c.fromPort);
      if (port === undefined) {
        throw fail(
          `${path}: the connection ${c.id} leaves ${c.from} by ${c.fromPort}, which is no port of it`,
        );
      }
      const connection: Connection = {
        id: c.id,
        from: c.from,
        fromPort: c.fromPort,
        to: c.to,
        order: c.order,
        label: c.label?.segments,
        condition: this.decodedOf(c.condition),
        onTraverse: this.decodedOf(c.onTraverse) as Script | undefined,
      };
      port.push(connection);
      this.check(
        `${path}: the condition of ${c.id}`,
        connection.condition,
        "condition",
        p,
        b,
      );
      this.check(
        `${path}: the script of ${c.id}`,
        connection.onTraverse,
        "script",
        p,
        b,
      );
      if (c.label !== undefined) {
        this.checkSegments(
          `${path}: the label of ${c.id}`,
          b,
          c.label.segments,
        );
      }
    }
    for (const n of f.nodes) {
      this.checkNode(path, b, n, p);
    }
    for (const n of f.nodes) {
      const ports = this.ports.get(n.id) as Map<string, Connection[]>;
      for (const port of [...ports.keys()].sort()) {
        const cs = ports.get(port) as Connection[];
        cs.sort((x, y) => x.order - y.order);
        cs.forEach((c, k) => {
          if (c.order !== k) {
            throw fail(
              `${path}: the connections of ${n.id}'s port ${port} are not ordered 0, 1, 2, and so on`,
            );
          }
        });
      }
    }
  }

  // checkNode checks the scripts, the speaker, the tags, the text, the
  // outputs' conditions, and the target of the node n of the board b.
  private checkNode(
    path: string,
    b: string,
    n: BoardFile["nodes"][number],
    p: Project,
  ): void {
    // scripts checks the node's scripts, as readBoard decoded them.
    const scripts = (onEnter?: object, onExit?: object) => {
      for (const e of [onEnter, onExit]) {
        this.check(
          `${path}: a script of ${n.id}`,
          this.decodedOf(e),
          "script",
          p,
          b,
        );
      }
    };
    if (n.type === "element") {
      if (n.speaker !== undefined && !this.components.has(n.speaker)) {
        throw fail(
          `${path}: the speaker of ${n.id} is ${n.speaker}, which is no component of the snapshot`,
        );
      }
      const tags = new Set<string>();
      for (const tag of n.tags ?? []) {
        const key = foldCase(tag);
        if (tags.has(key)) {
          throw fail(
            `${path}: two tags of ${n.id} are the same but for their case`,
          );
        }
        tags.add(key);
      }
      if (n.text !== undefined) {
        this.checkSegments(`${path}: the text of ${n.id}`, b, n.text.segments);
      }
      scripts(n.onEnter, n.onExit);
      return;
    }
    if (n.type === "branch") {
      for (const o of n.outputs) {
        this.check(
          `${path}: the condition of ${o.id}`,
          this.decodedOf(o.condition),
          "condition",
          p,
          b,
        );
      }
      scripts(n.onEnter, n.onExit);
      return;
    }
    if (n.type === "jumper") {
      if (this.release && n.target === undefined) {
        throw fail(
          `${path}: the jumper ${n.id} has no target, which only a draft's may lack`,
        );
      }
      const t = n.target;
      if (t !== undefined) {
        const target = this.nodes.get(t.nodeId);
        if (target === undefined || target.board !== t.boardId) {
          throw fail(
            `${path}: the jumper ${n.id}'s target ${t.nodeId} is not a node of its board ${t.boardId}`,
          );
        }
        if (target.type === "jumper") {
          throw fail(
            `${path}: the jumper ${n.id} targets the jumper ${t.nodeId}`,
          );
        }
      }
    }
  }

  // checkSegments checks the segments of a text or a label of the board b:
  // that each mention is of a component of the snapshot, and each value of a
  // variable that an expression of the board reads, which a board-scoped
  // variable of another board is not (§5.8.3), or of a runtime attribute of
  // a component, as an expression's var or attr leaf is (FR-TEXT-02); and
  // that a segment's marks are each there once, in alphabetical order, the
  // link among them exactly when it has an href.
  private checkSegments(
    where: string,
    b: string,
    segs: readonly Segment[],
  ): void {
    for (const s of segs) {
      if (s.kind === "text" || s.kind === "mention" || s.kind === "value") {
        for (let i = 1; i < s.marks.length; i++) {
          if (s.marks[i - 1] >= s.marks[i]) {
            throw fail(
              `${where}: a segment's marks are not each there once, in alphabetical order`,
            );
          }
        }
        if (s.marks.includes("link") !== (s.href !== undefined)) {
          throw fail(
            `${where}: a segment has the link mark without an href, or an href without the link mark`,
          );
        }
      }
      if (s.kind === "mention") {
        if (!this.components.has(s.ref)) {
          throw fail(
            `${where}: a mention is of ${s.ref}, which is no component of the snapshot`,
          );
        }
      } else if (s.kind === "value") {
        if (s.attribute !== undefined) {
          if (!this.initial.has(attributeKey(s.ref, s.attribute))) {
            throw fail(
              `${where}: a value is of ${s.ref}'s ${s.attribute}, which is no runtime attribute of the snapshot`,
            );
          }
        } else {
          const v = this.declared.get(s.ref);
          if (v === undefined) {
            throw fail(
              `${where}: a value is of ${s.ref}, which is no variable of the snapshot`,
            );
          }
          if (v.scope === "board" && v.boardId !== b) {
            throw fail(
              `${where}: a value is of ${s.ref}, a variable of another board, which an expression of the board does not read (board_variable_out_of_scope)`,
            );
          }
        }
      }
    }
  }
}
