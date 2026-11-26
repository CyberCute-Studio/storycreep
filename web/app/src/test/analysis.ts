// What the client-side analysis engine's tests build drafts of (plan item
// 89): a world that starts as a clean project, the board main, whose entry
// node start, an element of narration with text, leads to end, an ending
// with text, of which the rules find nothing, as the server's rules' tests
// start from (package validation, rules_test.go), changed by each test, and
// read as the engine reads the page's document.

import type {
  Attribute,
  Board,
  BranchOutput,
  ComponentType,
  Connection,
  Node,
  RichTextDoc,
  RichTextInline,
  Variable,
} from "@storycreep/shared/api";
import { type Draft, readDraft } from "../analysis/draft";
import { type Index, indexOf } from "../analysis/uses";
import { boardPart, type ComponentEntry } from "../document/doc";
import { testConnection, testNode } from "./canvas";
import {
  expression,
  orderKey,
  richText,
  testBoard,
  testDraft,
  testVariable,
  text,
} from "./play";

const at = "2026-11-01T10:00:00Z";

/** The ID prefixes of what a world holds. */
type Prefix = "bd" | "nd" | "cn" | "bo" | "var" | "ct" | "atr" | "cmp";

/** A world of the engine's tests, whose entities a test adds and changes before it reads them (analyse). */
export class World {
  boards: Board[] = [];
  nodes: Node[] = [];
  connections: Connection[] = [];
  variables: Variable[] = [];
  componentTypes: ComponentType[] = [];
  components: ComponentEntry[] = [];
  entry: { board_id?: string; node_id?: string } = {};
  /** The boards whose content the document has not read, of which it holds no part; it holds every other board's. */
  readonly unread = new Set<string>();
  readonly main: string;
  readonly start: string;
  readonly end: string;
  private next = 1;

  constructor() {
    this.main = this.board("Main");
    this.start = this.element(this.main, { title: "Start" });
    this.end = this.element(this.main, { is_ending: true });
    this.setEntry(this.main, this.start);
    this.connect(this.start, this.end);
  }

  /** Returns a new ID of the prefix, each later than the one before. */
  id(prefix: Prefix): string {
    return `${prefix}_${String(this.next++).padStart(26, "0")}`;
  }

  /** Adds a board, and returns its ID. */
  board(name = "Board", extra: Partial<Board> = {}): string {
    const id = this.id("bd");
    this.boards.push(testBoard(id, `${name} ${this.next}`, extra));
    return id;
  }

  /** Makes the node the entry node of the board. */
  setEntry(board: string, node: string | undefined): void {
    const b = this.boardOf(board);
    if (node === undefined) {
      delete b.entry_node_id;
    } else {
      b.entry_node_id = node;
    }
  }

  /** Returns the board of the ID. */
  boardOf(id: string): Board {
    const b = this.boards.find((x) => x.id === id);
    if (b === undefined) {
      throw new Error(`no board ${id}`);
    }
    return b;
  }

  /** Returns the node of the ID. */
  nodeOf(id: string): Node {
    const n = this.nodes.find((x) => x.id === id);
    if (n === undefined) {
      throw new Error(`no node ${id}`);
    }
    return n;
  }

  /** Returns the connection of the ID. */
  connectionOf(id: string): Connection {
    const c = this.connections.find((x) => x.id === id);
    if (c === undefined) {
      throw new Error(`no connection ${id}`);
    }
    return c;
  }

  /** Adds an element of narration with text to the board, as extra changes it, and returns its ID. */
  element(board: string, extra: Partial<Node> = {}): string {
    const id = this.id("nd");
    this.nodes.push(
      testNode(id, 0, 0, {
        board_id: board,
        project_id: "prj_0000000000000000000000TEST",
        title: "",
        text: richText(text("Text")),
        ...extra,
      }),
    );
    return id;
  }

  /** Adds a branch to the board, and returns its ID. */
  branch(board: string, extra: Partial<Node> = {}): string {
    return this.element(board, {
      type: "branch",
      kind: undefined,
      text: undefined,
      outputs: [],
      ...extra,
    });
  }

  /** Adds a jumper to the board, whose target is the node target, or none, and returns its ID. */
  jumper(board: string, target: string | undefined): string {
    const to =
      target === undefined
        ? undefined
        : this.nodes.find((n) => n.id === target);
    return this.element(board, {
      type: "jumper",
      kind: undefined,
      text: undefined,
      ...(target === undefined
        ? {}
        : { target: { board_id: to?.board_id ?? board, node_id: target } }),
    });
  }

  /** Adds an output to the branch, of the condition's source, or an else, and returns its ID. */
  output(branch: string, condition: string | "else" = "true"): string {
    const id = this.id("bo");
    const n = this.nodeOf(branch);
    const output: BranchOutput = {
      id,
      node_id: branch,
      kind: condition === "else" ? "else" : "if",
      ...(condition === "else"
        ? {}
        : { condition: expression(condition), order: orderKey(this.next) }),
      rev: 1,
      created_at: at,
      updated_at: at,
    };
    n.outputs = [...(n.outputs ?? []), output];
    return id;
  }

  /** Adds a connection from the node from, by its out, to the node to, as extra changes it, and returns its ID. */
  connect(from: string, to: string, extra: Partial<Connection> = {}): string {
    const id = this.id("cn");
    this.connections.push(
      testConnection(id, from, to, {
        board_id: this.nodeOf(from).board_id,
        project_id: "prj_0000000000000000000000TEST",
        order: orderKey(this.next),
        ...extra,
      }),
    );
    return id;
  }

  /** Adds a connection from the output of a branch to the node to, and returns its ID. */
  connectOutput(branch: string, output: string, to: string): string {
    return this.connect(branch, to, { from_port: output });
  }

  /**
   * Adds a global variable of the key, which a site reads, or writes, as
   * read and written say: end's text shows it, and end's on-enter script
   * assigns it.
   */
  variable(
    read: boolean,
    written: boolean,
    extra: Partial<Variable> = {},
  ): string {
    const id = this.id("var");
    const key = `v${this.next}`;
    this.variables.push(testVariable(id, key, "int", 0, extra));
    const end = this.nodeOf(this.end);
    if (read) {
      end.text = withInline(end.text, { type: "valueRef", attrs: { ref: id } });
    }
    if (written) {
      const source = end.on_enter?.source ?? "";
      end.on_enter = expression(
        `${source}${source === "" ? "" : "\n"}${key} = 1`,
      );
    }
    return id;
  }

  /** Adds a component type of attribute definitions, each of a key, a type, and whether it is runtime, and returns the IDs of the type and of its definitions. */
  componentType(
    key: string,
    attributes: { key: string; type: Attribute["type"]; runtime: boolean }[],
  ): { id: string; attributes: string[] } {
    const id = this.id("ct");
    const defs: Attribute[] = attributes.map((a, i) => ({
      id: this.id("atr"),
      component_type_id: id,
      key: a.key,
      name: a.key,
      type: a.type,
      runtime: a.runtime,
      default:
        a.type === "string" || a.type === "text"
          ? ""
          : a.type === "bool"
            ? false
            : 0,
      order: orderKey(i),
      rev: 1,
      created_at: at,
      updated_at: at,
    }));
    this.componentTypes.push({
      id,
      project_id: "prj_0000000000000000000000TEST",
      key,
      name: key,
      icon: "none",
      attributes: defs,
      rev: 1,
      created_at: at,
      updated_at: at,
    });
    return { id, attributes: defs.map((d) => d.id) };
  }

  /** Adds a component of the type, read whole, of the key and the description, and returns its ID. */
  component(
    type: string,
    key: string,
    description: RichTextDoc = richText(),
    extra: Partial<ComponentEntry> = {},
  ): string {
    const id = this.id("cmp");
    this.components.push({
      id,
      project_id: "prj_0000000000000000000000TEST",
      component_type_id: type,
      key,
      name: key,
      order: id,
      rev: 1,
      created_at: at,
      updated_at: at,
      attributes: {},
      description,
      ...extra,
    });
    return id;
  }

  /** Returns the draft of the world, as the engine reads the page's document, and its index. */
  analyse(): { draft: Draft; index: Index } {
    const draft = readDraft({
      ...testDraft({
        boards: this.boards,
        nodes: this.nodes,
        connections: this.connections,
        variables: this.variables,
        componentTypes: this.componentTypes,
        components: this.components,
        entry: this.entry,
      }),
      parts: new Set(
        this.boards
          .filter((b) => !this.unread.has(b.id))
          .map((b) => boardPart(b.id)),
      ),
    });
    return { draft, index: indexOf(draft) };
  }
}

/** Returns the rich text doc with the inline node added to its last paragraph, or one of it alone for none. */
export function withInline(
  doc: RichTextDoc | undefined,
  inline: RichTextInline,
): RichTextDoc {
  const content = [...(doc?.content ?? [])];
  const last = content.at(-1);
  if (last?.type === "paragraph") {
    content[content.length - 1] = {
      type: "paragraph",
      content: [...(last.content ?? []), inline],
    };
  } else {
    content.push({ type: "paragraph", content: [inline] });
  }
  return { type: "doc", content };
}
