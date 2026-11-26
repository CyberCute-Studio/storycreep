// The draft that the client-side analysis engine reads (plan item 89, note
// N96): the project's document as the page holds it, its own batches among
// it, read as the server's validation, index, and graph read the draft of
// the database, of the entities that are not in the trash alone, each
// expression compiled from its source, as the server compiles it, against
// what the draft's expressions can name, rather than read from the AST
// that the document holds, which an expression that the page changed has
// not yet. The dependency index (uses.ts), the validation (validation.ts),
// and the graph (graph.ts) are pure functions of it, so that they run on a
// page offline as they run on the server (PRD FR-OFF-05).

import type {
  Attribute,
  Board,
  BranchOutput,
  Connection,
  Expression,
  Node,
  RichTextDoc,
  Variable,
} from "@storycreep/shared/api";
import {
  type Context,
  compile,
  ExprError,
  type Node as ExprNode,
  Project,
  UsageError,
} from "@storycreep/shared/expr/compile";
import type { ComponentEntry, Doc } from "../document/doc";
import { compareChildren } from "../project/orderkey";

/** What the analysis reads of the document: what the projection reads, and the document's seq. */
export type AnalysisSources = Pick<
  Doc,
  | "seq"
  | "project"
  | "boards"
  | "nodes"
  | "connections"
  | "variables"
  | "componentTypes"
  | "components"
>;

/** readDraft's error for a document that it cannot read as a draft. */
export class AnalysisError extends Error {
  constructor(
    /**
     * Why: the project, or a component whole, not read yet ("unread"), or
     * the document holding what no project does, as two components of one
     * key, for a moment ("inconsistent").
     */
    readonly reason: "unread" | "inconsistent",
    message: string,
  ) {
    super(`analysis: ${message}`);
    this.name = "AnalysisError";
  }
}

/** The fields of an owner of expressions that hold them, as the server names them. */
export type ExpressionField =
  | "on_enter"
  | "on_exit"
  | "condition"
  | "on_traverse";

/** The kinds of the owners of expressions: a node, a connection, or a branch's output. */
export type ExpressionOwner = "node" | "connection" | "branch_output";

/** An expression of an owner that is not in the trash, compiled from its source. */
export interface DraftExpression {
  readonly owner: ExpressionOwner;
  /** The owner's ID. */
  readonly id: string;
  readonly field: ExpressionField;
  /** The board of the owner, which the expression is compiled as of. */
  readonly board: string;
  /** The node that a client opens the expression by: a node's own, the node that a connection leaves, or an output's branch. */
  readonly node: string;
  /** Its AST as the compiler gave it, if it compiles. */
  readonly ast?: ExprNode;
  /** The code of its first error, if it does not. */
  readonly error?: string;
}

/** The kinds of the owners of texts: a node's text, a connection's label, or a component's description. */
export type TextOwner = "node" | "connection" | "component";

/** A text of an owner that is not in the trash. */
export interface DraftText {
  readonly owner: TextOwner;
  /** The owner's ID. */
  readonly id: string;
  /** The board of a node's text or a connection's label; a component's description has none. */
  readonly board?: string;
  /** The node that a client opens it by: a node's own, or the node that a connection leaves. */
  readonly node?: string;
  readonly doc: RichTextDoc;
}

/** A branch's output that is not in the trash, with its branch. */
export interface DraftOutput {
  readonly output: BranchOutput;
  readonly node: Node;
}

/** An attribute definition that is not in the trash, of a component type that is not. */
export type DraftAttribute = Attribute;

/**
 * The project's draft: its entities that are not in the trash, as the
 * server's live views hold them, structurally, whether or not the document
 * has marked what went to the trash with what it went with: a node of a
 * board in the trash is in it too, and so is a connection of a node, an
 * output, or a board that is.
 */
export interface Draft {
  /** The seq of the project's latest operation that the document is as of. */
  readonly seq: number;
  /** The project's ID. */
  readonly project: string;
  /** The project's entry, its board and its node, each absent where it has none. */
  readonly entry: { readonly board?: string; readonly node?: string };
  /** The boards, in the order of their IDs. */
  readonly boards: ReadonlyMap<string, Board>;
  /** The nodes, in the order of their IDs. */
  readonly nodes: ReadonlyMap<string, Node>;
  /** The branches' outputs, by their IDs. */
  readonly outputs: ReadonlyMap<string, DraftOutput>;
  /**
   * The connections, in the order of their ports, by the nodes that they
   * leave and then the outputs, an element's out first, and each port's in
   * its order, as the server's validation reads them.
   */
  readonly connections: readonly Connection[];
  /** The expressions of the nodes, the outputs, and the connections. */
  readonly expressions: readonly DraftExpression[];
  /** The texts of the nodes, the connections, and the components. */
  readonly texts: readonly DraftText[];
  /** The variables, in the order of their IDs. */
  readonly variables: ReadonlyMap<string, Variable>;
  /** The components, each read whole. */
  readonly components: ReadonlyMap<string, ComponentEntry>;
  /** The attribute definitions, by their IDs. */
  readonly attributes: ReadonlyMap<string, DraftAttribute>;
}

/** Returns the UUID of a public ID as a string that sorts as its bytes do: its body, without its prefix. */
export function uuidOrder(id: string | undefined): string {
  return id === undefined ? "" : id.slice(id.indexOf("_") + 1);
}

/** Compares two public IDs, or none, which comes first, as the server compares their UUIDs. */
export function compareIds(
  a: string | undefined,
  b: string | undefined,
): number {
  const x = uuidOrder(a);
  const y = uuidOrder(b);
  return x < y ? -1 : x > y ? 1 : 0;
}

function byId(a: { id: string }, b: { id: string }): number {
  return compareIds(a.id, b.id);
}

/**
 * Returns the draft of view, the project's document as the page shows it,
 * whose every board's content and every component whole the caller has
 * read, as play mode reads them (play/session.ts). It throws an
 * AnalysisError where view has not read the project, or a component
 * whole, whose description is one of the draft's texts, or holds what no
 * project does.
 */
export function readDraft(view: AnalysisSources): Draft {
  const project = view.project;
  if (project === undefined) {
    throw new AnalysisError("unread", "the project has not been read");
  }
  const live = <T extends { id: string; deleted_at?: string }>(
    m: ReadonlyMap<string, T>,
  ) => [...m.values()].filter((e) => e.deleted_at === undefined).sort(byId);

  const boards = new Map(live(view.boards).map((b) => [b.id, b]));
  const nodes = new Map(
    live(view.nodes)
      .filter((n) => boards.has(n.board_id))
      .map((n) => [n.id, n]),
  );
  const outputs = new Map<string, DraftOutput>();
  for (const n of nodes.values()) {
    for (const o of n.outputs ?? []) {
      if (o.deleted_at === undefined) {
        outputs.set(o.id, { output: o, node: n });
      }
    }
  }
  const connections = live(view.connections)
    .filter(
      (c) =>
        boards.has(c.board_id) &&
        nodes.has(c.from_node_id) &&
        nodes.has(c.to_node_id) &&
        (c.from_port === "out" || outputs.has(c.from_port)),
    )
    .sort(
      (a, b) =>
        compareIds(a.from_node_id, b.from_node_id) ||
        compareIds(portOf(a), portOf(b)) ||
        compareChildren(a, b),
    );
  const variables = new Map(
    live(view.variables)
      .filter(
        (v) =>
          v.scope === "global" ||
          (v.board_id !== undefined && boards.has(v.board_id)),
      )
      .map((v) => [v.id, v]),
  );
  const components = new Map(live(view.components).map((c) => [c.id, c]));
  const partial = [...components.values()].find(
    (c) => c.attributes === undefined,
  );
  if (partial !== undefined) {
    throw new AnalysisError(
      "unread",
      `the component ${partial.id} has not been read whole`,
    );
  }
  // Each type's attribute definitions, in their author's order, of the
  // types that are not in the trash.
  const definitionsOf = new Map<string, Attribute[]>();
  const attributes = new Map<string, Attribute>();
  for (const t of live(view.componentTypes)) {
    const defs = t.attributes
      .filter((a) => a.deleted_at === undefined)
      .sort(compareChildren);
    definitionsOf.set(t.id, defs);
    for (const a of defs) {
      attributes.set(a.id, a);
    }
  }

  // What the draft's expressions can name, as the server compiles them.
  let names: Project;
  try {
    names = Project.of(
      [...variables.values()].map((v) => ({
        id: v.id,
        key: v.key,
        type: v.type,
        ...(v.scope === "board" && v.board_id !== undefined
          ? { board: v.board_id }
          : {}),
      })),
      [...components.values()].map((c) => ({
        id: c.id,
        key: c.key,
        attributes: (definitionsOf.get(c.component_type_id) ?? []).map((d) => ({
          id: d.id,
          key: d.key,
          type: d.type,
          runtime: d.runtime,
          ...(d.type === "enum" ? { options: d.options ?? [] } : {}),
        })),
      })),
      [...nodes.values()].map((n) => ({
        id: n.id,
        jumper: n.type === "jumper",
      })),
    );
  } catch (e) {
    if (e instanceof UsageError) {
      throw new AnalysisError("inconsistent", e.message);
    }
    throw e;
  }

  const expressions: DraftExpression[] = [];
  // add adds the expression of the owner's field, compiled from its source
  // in ctx as an expression of the board: a script or a connection's
  // condition only where it has a source, as the server keeps none of the
  // empty source, and a conditional output's condition whatever its
  // source, as the empty one does not compile.
  const add = (
    expression: Expression | undefined,
    ctx: Context,
    at: Omit<DraftExpression, "ast" | "error">,
    always = false,
  ) => {
    const source = expression?.source ?? "";
    if (source === "" && !always) {
      return;
    }
    try {
      expressions.push({ ...at, ast: compile(source, ctx, names, at.board) });
    } catch (e) {
      if (!(e instanceof ExprError)) {
        throw e;
      }
      expressions.push({ ...at, error: e.code });
    }
  };
  for (const n of nodes.values()) {
    if (n.type === "jumper") {
      continue;
    }
    const at = {
      owner: "node",
      id: n.id,
      board: n.board_id,
      node: n.id,
    } as const;
    add(n.on_enter, "script", { ...at, field: "on_enter" });
    add(n.on_exit, "script", { ...at, field: "on_exit" });
  }
  for (const { output, node } of outputs.values()) {
    if (output.kind !== "else") {
      add(
        output.condition,
        "condition",
        {
          owner: "branch_output",
          id: output.id,
          field: "condition",
          board: node.board_id,
          node: node.id,
        },
        true,
      );
    }
  }
  for (const c of connections) {
    const at = {
      owner: "connection",
      id: c.id,
      board: c.board_id,
      node: c.from_node_id,
    } as const;
    add(c.condition, "condition", { ...at, field: "condition" });
    add(c.on_traverse, "script", { ...at, field: "on_traverse" });
  }

  const texts: DraftText[] = [];
  for (const n of nodes.values()) {
    if (n.text !== undefined) {
      texts.push({
        owner: "node",
        id: n.id,
        board: n.board_id,
        node: n.id,
        doc: n.text,
      });
    }
  }
  for (const c of connections) {
    texts.push({
      owner: "connection",
      id: c.id,
      board: c.board_id,
      node: c.from_node_id,
      doc: c.label,
    });
  }
  for (const c of components.values()) {
    if (c.description !== undefined) {
      texts.push({ owner: "component", id: c.id, doc: c.description });
    }
  }

  return {
    seq: view.seq,
    project: project.id,
    entry: {
      ...(project.entry.board_id === undefined
        ? {}
        : { board: project.entry.board_id }),
      ...(project.entry.node_id === undefined
        ? {}
        : { node: project.entry.node_id }),
    },
    boards,
    nodes,
    outputs,
    connections,
    expressions,
    texts,
    variables,
    components,
    attributes,
  };
}

/** Returns the output that a connection leaves, or undefined for an element's out. */
export function portOf(c: Connection): string | undefined {
  return c.from_port === "out" ? undefined : c.from_port;
}
