// What a project's expressions can name (PRD §5.8.3), which check resolves
// names against: the TypeScript twin of the Go package
// server/internal/expr's Project.

import { isCanonicalId } from "../ids";
import { isKey } from "./ast";
import { UsageError } from "./error";

/**
 * A variable that a project's expressions can name (PRD FR-VAR-01,
 * FR-VAR-02): a global one, or one of a board.
 */
export interface Variable {
  /** The variable's ID, in its canonical spelling. */
  readonly id: string;
  readonly key: string;
  readonly type: "bool" | "int" | "float" | "string";
  /** The ID of the board whose variable it is, or absent for a global one. */
  readonly board?: string;
}

/** The types of an attribute. */
export type AttributeType =
  | "bool"
  | "int"
  | "float"
  | "string"
  | "text"
  | "enum";

/**
 * One of a component's attributes, of its type's attribute definition
 * (FR-COMP-01), which each component of the type lists alike.
 */
export interface Attribute {
  /** The attribute definition's ID, in its canonical spelling. */
  readonly id: string;
  readonly key: string;
  /**
   * An expression reads an enum as a string, and a text, which is never
   * runtime, not at all.
   */
  readonly type: AttributeType;
  readonly runtime?: boolean;
  /** An enum's options, which are all that an expression may assign it. */
  readonly options?: readonly string[];
}

/**
 * A component, whose runtime attributes a project's expressions can read and
 * write (FR-COMP-02, FR-COMP-03).
 */
export interface Component {
  /** The component's ID, in its canonical spelling. */
  readonly id: string;
  readonly key: string;
  readonly attributes: readonly Attribute[];
}

/**
 * A node of one of a project's boards, which a node reference can name
 * (§5.8.4): an element, a branch, or a jumper, which visited and visits do
 * not count (§5.8.5).
 */
export interface BoardNode {
  /** The node's ID, in its canonical spelling. */
  readonly id: string;
  readonly jumper?: boolean;
}

/** A component and its attributes by their keys, and by their definitions' IDs. */
export interface ComponentEntry {
  readonly id: string;
  readonly key: string;
  readonly attributes: readonly Attribute[];
  readonly byKey: ReadonlyMap<string, Attribute>;
  readonly byId: ReadonlyMap<string, Attribute>;
}

const variableTypes = new Set(["bool", "int", "float", "string"]);
const attributeTypes = new Set([
  "bool",
  "int",
  "float",
  "string",
  "text",
  "enum",
]);

function projectError(message: string): UsageError {
  return new UsageError("project", `not what a project has: ${message}`);
}

/**
 * What a project's expressions can name: its variables, global and of each
 * board, its components and their attributes, and its nodes, those that are
 * not in the trash, by their keys and IDs. Project.of returns one, and with
 * one of another's names and a board's, which check reads and never changes,
 * so that one Project serves every expression of the project.
 */
export class Project {
  private readonly globals: Map<string, Variable>;
  // boards are each board's variables, by its ID, and then by their keys.
  private readonly boards = new Map<string, Map<string, Variable>>();
  // boardsOf are the boards that have a variable of each key.
  private readonly boardsOf = new Map<string, string[]>();
  private readonly components: Map<string, ComponentEntry>;
  // variables and componentIds are the variables and the components by their
  // IDs, by which checkResolved reads a name.
  private readonly variables = new Map<string, Variable>();
  private readonly componentIds: Map<string, ComponentEntry>;
  // nodes say of each node, by its ID, whether it is a jumper.
  private readonly nodes = new Map<string, boolean>();
  // base is the Project that with added this one's names to, or undefined
  // for one that Project.of returns. A Project that with returns shares
  // base's globals and components, and its other maps hold what with added
  // alone, so that a read of a board's variables, of a key's boards, of a
  // variable by its ID, or of a node goes on to base's.
  private readonly base: Project | undefined;

  private constructor(base: Project | undefined) {
    this.base = base;
    this.globals = base?.globals ?? new Map();
    this.components = base?.components ?? new Map();
    this.componentIds = base?.componentIds ?? new Map();
  }

  /** A Project that names nothing. */
  static readonly EMPTY: Project = new Project(undefined);

  /**
   * Returns the Project of the variables, the components, and the nodes, or
   * throws a UsageError of the kind project for what is not a project's: an
   * ID that is not one of its entity's, in its canonical spelling, a key that
   * is not an identifier, a type that is not a variable's or an attribute's,
   * a text attribute that is runtime, an enum's options that it does not
   * have, an entity whose ID, or whose key in its scope, is another's, or an
   * attribute definition that two components list unalike, or one component
   * twice. It copies what it reads.
   */
  static of(
    variables: readonly Variable[],
    components: readonly Component[],
    nodes: readonly BoardNode[],
  ): Project {
    const p = new Project(undefined);
    for (const v of variables) {
      p.addVariable(v);
    }
    // definitions are the attribute definitions by their IDs, which the
    // components of a type share.
    const definitions = new Map<string, Attribute>();
    for (const c of components) {
      p.id("component", c.id);
      key(c.key);
      if (p.components.has(c.key)) {
        throw projectError(`two components have the key ${c.key}`);
      }
      const attributes: Attribute[] = [];
      const byKey = new Map<string, Attribute>();
      const byId = new Map<string, Attribute>();
      for (const given of c.attributes) {
        const a: Attribute = {
          id: given.id,
          key: given.key,
          type: given.type,
          runtime: given.runtime === true,
          options: [...(given.options ?? [])],
        };
        if (!isCanonicalId("attribute_def", a.id)) {
          throw projectError(
            `${JSON.stringify(a.id)} is not the ID of an attribute_def in its canonical spelling`,
          );
        }
        if (byId.has(a.id)) {
          throw projectError(
            `${c.key} lists the attribute definition ${a.id} twice`,
          );
        }
        key(a.key);
        if (!attributeTypes.has(a.type)) {
          throw projectError(
            `${JSON.stringify(a.type)} is no type of an attribute`,
          );
        }
        if (a.type === "text" && a.runtime) {
          throw projectError(
            `the attribute ${c.key}.${a.key} is a text, which is never runtime`,
          );
        }
        if ((a.type === "enum") !== (a.options ?? []).length > 0) {
          throw projectError(
            `the attribute ${c.key}.${a.key} has options and is no enum, or is one without them`,
          );
        }
        if (byKey.has(a.key)) {
          throw projectError(
            `two attributes of ${c.key} have the key ${a.key}`,
          );
        }
        // The components of a type share its definitions, each of which is
        // one entity, of one key, type, runtime flag, and options.
        const d = definitions.get(a.id);
        if (
          d !== undefined &&
          (d.key !== a.key ||
            d.type !== a.type ||
            d.runtime !== a.runtime ||
            !sameList(d.options ?? [], a.options ?? []))
        ) {
          throw projectError(
            `two components list the attribute definition ${a.id} unalike`,
          );
        }
        definitions.set(a.id, a);
        attributes.push(a);
        byKey.set(a.key, a);
        byId.set(a.id, a);
      }
      const entry: ComponentEntry = {
        id: c.id,
        key: c.key,
        attributes,
        byKey,
        byId,
      };
      p.components.set(c.key, entry);
      p.componentIds.set(c.id, entry);
    }
    for (const n of nodes) {
      p.addNode(n);
    }
    return p;
  }

  /**
   * Returns the Project of this one's names and the variables of the board
   * whose ID is board, each of which is of that board, and the nodes, as of
   * returns of them all, or throws a UsageError of the kind project; this
   * one stays as it is. The Project holds what it adds alone, and reads the
   * rest in this one, as the expressions of a board in the trash see the
   * board's own variables and nodes.
   */
  with(
    board: string,
    variables: readonly Variable[],
    nodes: readonly BoardNode[],
  ): Project {
    if (!isCanonicalId("board", board)) {
      throw projectError(
        `${JSON.stringify(board)} is not the ID of a board in its canonical spelling`,
      );
    }
    const q = new Project(this);
    for (const v of variables) {
      if (v.board !== board) {
        throw projectError(`the variable ${v.id} is not of the board ${board}`);
      }
      q.addVariable(v);
    }
    for (const n of nodes) {
      q.addNode(n);
    }
    return q;
  }

  // id throws unless s is the ID of an entity of the kind, in its canonical
  // spelling, that is not the ID of a variable, a component, or a node.
  private id(kind: "variable" | "component" | "node", s: string): void {
    if (!isCanonicalId(kind, s)) {
      throw projectError(
        `${JSON.stringify(s)} is not the ID of a ${kind} in its canonical spelling`,
      );
    }
    if (
      this.variable(s) !== undefined ||
      this.componentIds.has(s) ||
      this.node(s) !== undefined
    ) {
      throw projectError(`${s} is the ID of two entities`);
    }
  }

  // addVariable adds the variable v to this Project's own maps.
  private addVariable(given: Variable): void {
    const v: Variable = { ...given };
    this.id("variable", v.id);
    key(v.key);
    if (!variableTypes.has(v.type)) {
      throw projectError(`${JSON.stringify(v.type)} is no type of a variable`);
    }
    const board = v.board ?? "";
    if (board !== "" && !isCanonicalId("board", board)) {
      throw projectError(
        `${JSON.stringify(board)} is not the ID of a board in its canonical spelling`,
      );
    }
    if (
      (board === "" && this.global(v.key) !== undefined) ||
      (board !== "" && this.ofBoard(board, v.key) !== undefined)
    ) {
      throw projectError(`two variables of one scope have the key ${v.key}`);
    }
    if (board === "") {
      this.globals.set(v.key, v);
    } else {
      let vars = this.boards.get(board);
      if (vars === undefined) {
        vars = new Map();
        this.boards.set(board, vars);
      }
      vars.set(v.key, v);
      let boards = this.boardsOf.get(v.key);
      if (boards === undefined) {
        boards = [];
        this.boardsOf.set(v.key, boards);
      }
      boards.push(board);
    }
    this.variables.set(v.id, v);
  }

  // addNode adds the node n.
  private addNode(n: BoardNode): void {
    this.id("node", n.id);
    this.nodes.set(n.id, n.jumper === true);
  }

  /** Returns the global variable of the key, or undefined. */
  global(key: string): Variable | undefined {
    return this.globals.get(key);
  }

  /**
   * Returns the variable of the key of the board whose ID is board, or
   * undefined, as for the board "", which has none.
   */
  ofBoard(board: string, key: string): Variable | undefined {
    for (let p: Project | undefined = this; p !== undefined; p = p.base) {
      const v = p.boards.get(board)?.get(key);
      if (v !== undefined) {
        return v;
      }
    }
    return undefined;
  }

  /** Returns the keys of the variables of the board whose ID is board, in no order. */
  boardKeys(board: string): string[] {
    const keys: string[] = [];
    for (let p: Project | undefined = this; p !== undefined; p = p.base) {
      for (const k of p.boards.get(board)?.keys() ?? []) {
        keys.push(k);
      }
    }
    return keys;
  }

  /** Returns the keys of the global variables, in no order. */
  globalKeys(): string[] {
    return [...this.globals.keys()];
  }

  /** Reports whether a board other than the one whose ID is board has a variable of the key. */
  elsewhere(board: string, key: string): boolean {
    for (let p: Project | undefined = this; p !== undefined; p = p.base) {
      if ((p.boardsOf.get(key) ?? []).some((b) => b !== board)) {
        return true;
      }
    }
    return false;
  }

  /** Returns the variable whose ID is id, or undefined. */
  variable(id: string): Variable | undefined {
    for (let p: Project | undefined = this; p !== undefined; p = p.base) {
      const v = p.variables.get(id);
      if (v !== undefined) {
        return v;
      }
    }
    return undefined;
  }

  /** Returns the component of the key, or undefined. */
  component(key: string): ComponentEntry | undefined {
    return this.components.get(key);
  }

  /** Returns the component whose ID is id, or undefined. */
  componentById(id: string): ComponentEntry | undefined {
    return this.componentIds.get(id);
  }

  /** Returns the keys of the components, in no order. */
  componentKeys(): string[] {
    return [...this.components.keys()];
  }

  /**
   * Returns whether the node whose ID is id is a jumper, or undefined when
   * the project does not have it.
   */
  node(id: string): boolean | undefined {
    for (let p: Project | undefined = this; p !== undefined; p = p.base) {
      const jumper = p.nodes.get(id);
      if (jumper !== undefined) {
        return jumper;
      }
    }
    return undefined;
  }
}

// key throws unless s is an identifier that is not a reserved word, as a key
// is (§5.8.1).
function key(s: string): void {
  if (typeof s !== "string" || !isKey(s)) {
    throw projectError(`${JSON.stringify(s)} is no key`);
  }
}

function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}
