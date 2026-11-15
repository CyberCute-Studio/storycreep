// Play mode's projection of the project's document into the published game
// format (PRD §6, FR-PLAY-01, plan item 71, note N78): the draft as the page
// holds it, its own batches among it, written as the files of a snapshot of
// the draft channel, which the shared flow's load reads and its Runtime
// runs, so that play mode runs the data model that the conformance suite
// checks, with no round trip to the server. Each file is laid out as the
// server's are (§6.2), its keys sorted and indented by two spaces, so that
// item 89's cross-check against the Go serializer compares entities, and
// the conformance suite's snapshots, which Go wrote, project back to their
// own bytes (projection.test.ts).
//
// A draft may hold what no snapshot does: an expression that does not
// compile, a text's reference to what is in the trash, a speaker in the
// trash, a jumper whose target is in the trash, is a jumper, or is not on
// the board that it names, a board's or the project's entry that is gone,
// a connection that leaves a port that its node no longer has. The
// projection stands in for each, as the maintainer chose (N78), and lists
// it: a condition that does not compile never holds, a script that does
// not compile does nothing, a reference or a speaker is left out, a jumper
// has no target, which play mode's runtime reports as jumper_broken once
// the flow reaches it (§5.8.5), an entry is left out, and so is the
// connection.

import type {
  Connection as ConnectionBody,
  Expression,
  Node as NodeBody,
  RichTextBlock,
  RichTextDoc,
} from "@storycreep/shared/api";
import {
  type Context,
  compile,
  ExprError,
  type Node as ExprNode,
  encode,
  Project,
  UsageError,
  versionOf,
  walk,
} from "@storycreep/shared/expr/compile";
import {
  attributeKey,
  type ExpressionField,
  fileHash,
  OUT,
  type Pace,
} from "@storycreep/shared/flow";
import type {
  ASTExpression,
  ASTScript,
  AttributeDefinition,
  Board as BoardFile,
  BranchOutput,
  Component,
  ComponentType,
  Condition,
  Connection,
  Entry,
  Manifest,
  ManifestBoard,
  Node,
  Script,
  Segment,
  Settings,
  Variable,
} from "@storycreep/shared/published";
import {
  type Names,
  plainTextOf,
  previewOf,
  segmentsOf,
  tryParseRichTextOfAnySize,
} from "@storycreep/shared/richtext";
import { byCodePoints } from "../canvas/edit";
import { hundredths } from "../document/appliers/node";
import type { Doc } from "../document/doc";
import { compareChildren } from "../project/orderkey";

/** What the projection reads of the document. */
export type ProjectionSources = Pick<
  Doc,
  | "project"
  | "boards"
  | "nodes"
  | "connections"
  | "variables"
  | "componentTypes"
  | "components"
>;

/**
 * What an expression is of, as a stand-in and a runtime error name where
 * it is: a node, a connection that leaves an element, which is a choice,
 * one that leaves a branch, which the flow passes on by alone, or a
 * branch's output.
 */
export type ExpressionOwner = "node" | "choice" | "connection" | "output";

// Where an expression is, as its stand-in says: its owner, which kind of
// entity it is, its field, and the node that a link opens it by.
type ExpressionAt = {
  entity: string;
  owner: ExpressionOwner;
  field: ExpressionField;
  node: string;
};

// What a text's value reference or mention refers to that its text cannot
// show, and why (StandIn's reference).
type Unreadable = {
  ref: string;
  reason: "gone" | "board" | "runtime";
};

/**
 * What the projection stood in for, of what no snapshot holds (N78), each
 * with the board and the node that a link opens it by, as a validation
 * finding's (FR-DEP-03 AC3): a node's own, the node that a connection
 * leaves, an output's branch, or a board.
 */
export type StandIn = {
  /** The board and the node that a link opens it by, the node absent for a board's or the project's. */
  readonly board?: string;
  readonly node?: string;
} & (
  | {
      /**
       * An expression that does not compile, or a branch's conditional
       * output that has no condition: a condition never holds, a script
       * does nothing.
       */
      readonly kind: "expression";
      /** The node, the connection, or the branch output whose it is. */
      readonly entity: string;
      /** What entity is (ExpressionOwner). */
      readonly owner: ExpressionOwner;
      readonly field: ExpressionField;
      readonly source: string;
      /** The compiler's error, or "" for an output without a condition. */
      readonly code: string;
    }
  | {
      /** A value reference or a mention of what the snapshot does not hold, or that the text's board does not read, left out of its text. */
      readonly kind: "reference";
      /** The node or the connection whose text or label holds it. */
      readonly entity: string;
      readonly field: "text" | "label";
      /** The variable or the component referred to. */
      readonly ref: string;
      /**
       * Why it is left out: what it refers to is not in the snapshot, as in
       * the trash ("gone"), is another board's variable ("board"), or is an
       * attribute that is not runtime ("runtime").
       */
      readonly reason: "gone" | "board" | "runtime";
    }
  | {
      /** A text or a label that is no rich text that the server takes, left out. */
      readonly kind: "text";
      readonly entity: string;
      readonly field: "text" | "label";
    }
  | {
      /** An element's speaker that the snapshot does not hold, left out. */
      readonly kind: "speaker";
      readonly entity: string;
      readonly ref: string;
    }
  | {
      /** A jumper's target that the snapshot does not hold, that is a jumper, or that is not on the board that the target names: the jumper has none. */
      readonly kind: "target";
      readonly entity: string;
      readonly ref: string;
      /** Why: the node is not in the snapshot, as in the trash ("gone"), is a jumper ("jumper"), or is on another board than the target names ("board"). */
      readonly reason: "gone" | "jumper" | "board";
    }
  | {
      /** A board's entry node that the snapshot does not hold, left out. */
      readonly kind: "board_entry";
      readonly entity: string;
      readonly ref: string;
    }
  | {
      /** The project's entry, of a board or a node that the snapshot does not hold, or of a node that is not on the board that it names, left out. */
      readonly kind: "entry";
      readonly entity: string;
      readonly ref: string;
      /** Why: what it names is not in the snapshot, as in the trash ("gone"), or its node is on another board than it names ("board"). */
      readonly reason: "gone" | "board";
    }
  | {
      /** A connection that leaves a port that its node does not have, or a jumper, or enters a node that its board does not hold, left out. */
      readonly kind: "connection";
      readonly entity: string;
      /** Why: it leaves by a port that its node does not have, a jumper's among them ("port"), or it enters a node that its board does not hold, in the trash or on another board ("target"). */
      readonly reason: "port" | "target";
    }
);

/**
 * A variable, or a component's runtime attribute, of the snapshot, which
 * play mode's inspector lists with its value (FR-PLAY-02, plan note N79).
 */
export type ProjectedValue = {
  /** Its key of the runtime's state: the variable's ID, or attributeKey of the component and the attribute definition. */
  readonly key: string;
  /** Its name as an expression of its board names it: the variable's key, or the component's and the attribute's keys, as elara.trust. */
  readonly name: string;
  /** Its type, of which an enum's value is one of its options. */
  readonly type: "bool" | "int" | "float" | "string" | "enum";
  /** An enum's options, in their author's order, [] of another type. */
  readonly options: readonly string[];
} & (
  | {
      readonly kind: "variable";
      readonly variable: string;
      /** The board whose variable it is, absent for a global one. */
      readonly board?: string;
    }
  | {
      readonly kind: "attribute";
      readonly component: string;
      readonly attribute: string;
    }
);

/** What the projection wrote: the snapshot's files, the boards' files as objects, and what it stood in for. */
export interface Projection {
  /** The snapshot's files, by their paths, as load reads them. */
  readonly files: ReadonlyMap<string, Uint8Array>;
  /** The board files, by their boards' IDs, which play mode shows a node's speaker and an expression's source from. */
  readonly boards: ReadonlyMap<string, BoardFile>;
  /** The components' names, by their IDs, as the snapshot holds them. */
  readonly componentNames: ReadonlyMap<string, string>;
  /** What the projection stood in for, in the order of the boards and of their nodes and connections. */
  readonly standIns: readonly StandIn[];
  /**
   * Each condition that compiled, of a connection or a branch's output, by
   * the entity's ID: its AST as the compiler gave it, which, unlike the one
   * that a snapshot's file holds, has where each of its parts is in its
   * source, which play mode's author view shows the part of that failed
   * (FR-PLAY-03).
   */
  readonly conditions: ReadonlyMap<string, ExprNode>;
  /** The snapshot's variables, in the order of their IDs, and then each component's runtime attributes, in the order of the components' IDs and of their type's definitions, as the runtime holds them. */
  readonly values: readonly ProjectedValue[];
}

/** projectDraft's error for a document that it cannot project at all. */
export class ProjectionError extends Error {
  constructor(
    /**
     * Why: the project, a component of the snapshot whole, or, as the play
     * session refuses it, a board's content, not read yet ("unread"), or
     * the document holding what no project has, as two components of one
     * key, or two boards of one slug, for a moment ("inconsistent").
     */
    readonly reason: "unread" | "inconsistent",
    message: string,
  ) {
    super(`play: ${message}`);
    this.name = "ProjectionError";
  }
}

/** What wrote a snapshot that the projection writes, as the manifest's generator says. */
export const GENERATOR = { name: "storycreep-web", version: "play" } as const;

/** The options of projectDraft. */
export interface ProjectOptions {
  /** When the draft is projected, as its manifest's pulledAt says, to the second: now, unless given. */
  readonly now?: Date;
  /** The signal that stops the hashing of the files once it is aborted, as of a start that a stop replaced, as load's does. */
  readonly signal?: AbortSignal;
}

const literalFalse: ASTExpression = { kind: "lit", type: "bool", value: false };

/**
 * Returns the projection of view, the project's document as the page shows
 * it, into the files of a snapshot of the draft channel (§6.3, §7.8): every
 * board, node, connection, variable, component type, and component that is
 * not in the trash, each expression compiled from its source, as the server
 * compiles it, against what the snapshot holds, and each text's segments
 * derived, with what it stands in for listed (StandIn). It reads the
 * entities that view holds: a board whose content the page has not read
 * has no nodes, which play mode reads first (session.ts), as it reads each
 * component whole. It reads view at once, and resolves once it has hashed
 * the files that it wrote, as load does, by the platform's SHA-256, or by
 * its own, which lets the page run, where it has none, and stops once the
 * options' signal is aborted, with its reason, as load does. It rejects with a
 * ProjectionError where view has not read the project, or a component of
 * the snapshot whole, whose values its type's defaults would else stand in
 * for unsaid, or holds what no project has.
 */
export async function projectDraft(
  view: ProjectionSources,
  options: ProjectOptions = {},
): Promise<Projection> {
  const project = view.project;
  if (project === undefined) {
    throw new ProjectionError("unread", "the project has not been read");
  }
  const standIns: StandIn[] = [];
  const live = <T extends { deleted_at?: string }>(m: ReadonlyMap<string, T>) =>
    [...m.values()].filter((e) => e.deleted_at === undefined);

  // The boards, and the nodes of each, sorted by ID.
  const boards = live(view.boards).sort(byId);
  const boardIds = new Set(boards.map((b) => b.id));
  const nodesOf = new Map<string, NodeBody[]>(boards.map((b) => [b.id, []]));
  for (const n of live(view.nodes)) {
    nodesOf.get(n.board_id)?.push(n);
  }
  const nodes = new Map<string, NodeBody>();
  for (const list of nodesOf.values()) {
    list.sort(byId);
    for (const n of list) {
      nodes.set(n.id, n);
    }
  }
  // The connections of each board, once.
  const connectionsOf = new Map<string, ConnectionBody[]>();
  for (const c of live(view.connections)) {
    const list = connectionsOf.get(c.board_id);
    if (list === undefined) {
      connectionsOf.set(c.board_id, [c]);
    } else {
      list.push(c);
    }
  }

  // The variables, of the project or of a board of the snapshot.
  const variables = live(view.variables)
    .filter(
      (v) =>
        v.scope === "global" ||
        (v.board_id !== undefined && boardIds.has(v.board_id)),
    )
    .sort(byId);
  const variableById = new Map(variables.map((v) => [v.id, v]));

  // The components, of a type that is not in the trash, and each type's
  // definitions, in their author's order.
  const definitionsOf = new Map<string, ComponentType["attributes"]>();
  const typeById = new Map<string, ComponentType>();
  for (const t of live(view.componentTypes)) {
    const defs = t.attributes
      .filter((a) => a.deleted_at === undefined)
      .sort(compareChildren)
      .map(
        (a): AttributeDefinition =>
          ({
            default: a.default,
            id: a.id,
            key: a.key,
            name: a.name,
            ...(a.type === "enum" ? { options: [...(a.options ?? [])] } : {}),
            runtime: a.runtime,
            type: a.type,
          }) as AttributeDefinition,
      );
    definitionsOf.set(t.id, defs);
    typeById.set(t.id, {
      attributes: defs,
      id: t.id,
      key: t.key,
      name: t.name,
    });
  }
  const components = live(view.components)
    .filter((c) => typeById.has(c.component_type_id))
    .sort(byId);
  const partial = components.find((c) => c.attributes === undefined);
  if (partial !== undefined) {
    throw new ProjectionError(
      "unread",
      `the component ${partial.id} has not been read whole`,
    );
  }
  const componentById = new Map(components.map((c) => [c.id, c]));

  // What the snapshot's expressions can name, as the server compiles them.
  let names: Project;
  try {
    names = Project.of(
      variables.map((v) => ({
        id: v.id,
        key: v.key,
        type: v.type,
        ...(v.scope === "board" && v.board_id !== undefined
          ? { board: v.board_id }
          : {}),
      })),
      components.map((c) => ({
        id: c.id,
        key: c.key,
        attributes: (definitionsOf.get(c.component_type_id) ?? []).map((d) => ({
          id: d.id,
          key: d.key,
          type: d.type,
          runtime: d.runtime,
          ...(d.type === "enum" ? { options: d.options } : {}),
        })),
      })),
      [...nodes.values()].map((n) => ({
        id: n.id,
        jumper: n.type === "jumper",
      })),
    );
  } catch (e) {
    if (e instanceof UsageError) {
      throw new ProjectionError("inconsistent", e.message);
    }
    throw e;
  }

  const asts: ExprNode[] = [];
  const conditions = new Map<string, ExprNode>();
  const signals = new Set<string>();
  // compiled returns the AST of an expression's source, compiled in ctx as
  // an expression of the board, as the published format holds it, or
  // undefined, with its stand-in, for one that does not compile.
  const compiled = (
    expression: Expression,
    ctx: Context,
    board: string,
    at: ExpressionAt,
  ): unknown => {
    let ast: ExprNode;
    try {
      ast = compile(expression.source, ctx, names, board);
    } catch (e) {
      if (!(e instanceof ExprError)) {
        throw e;
      }
      standIns.push({
        kind: "expression",
        board,
        ...at,
        source: expression.source,
        code: e.code,
      });
      return undefined;
    }
    asts.push(ast);
    if (ctx === "condition") {
      conditions.set(at.entity, ast);
    }
    walk(ast, (n) => {
      if (
        n.kind === "call" &&
        n.function === "signal" &&
        n.args[0]?.kind === "lit" &&
        n.args[0].type === "string"
      ) {
        signals.add(n.args[0].value);
      }
    });
    return JSON.parse(encode(ast));
  };
  const script = (
    expression: Expression | undefined,
    board: string,
    at: ExpressionAt,
  ): Script | undefined => {
    if (expression === undefined || expression.source === "") {
      return undefined;
    }
    const ast = compiled(expression, "script", board, at);
    return ast === undefined
      ? undefined
      : { ast: ast as ASTScript, source: expression.source };
  };
  const condition = (
    expression: Expression | undefined,
    board: string,
    at: ExpressionAt,
  ): Condition | undefined => {
    if (expression === undefined || expression.source === "") {
      return undefined;
    }
    const ast = compiled(expression, "condition", board, at);
    return {
      ast: (ast as ASTExpression | undefined) ?? literalFalse,
      source: expression.source,
    };
  };

  // The rich texts, their references to what the snapshot does not hold,
  // or that their board does not read, left out.
  const segmentNames: Names = {
    value: (ref, attribute) => {
      if (attribute === undefined) {
        return variableById.get(ref)?.key ?? "";
      }
      const c = componentById.get(ref);
      const d = definitionsOf
        .get(c?.component_type_id ?? "")
        ?.find((a) => a.id === attribute);
      return c === undefined || d === undefined ? "" : `${c.key}.${d.key}`;
    },
    component: (id) => componentById.get(id)?.name ?? "",
  };
  // unreadable returns what an inline node of a text of the board refers to
  // that the snapshot does not hold, or that the board does not read, and
  // why, or undefined for none.
  const unreadable = (
    inline: { type: string; attrs?: unknown },
    board: string,
  ): Unreadable | undefined => {
    if (inline.type === "mention") {
      const id = (inline.attrs as { id: string }).id;
      return componentById.has(id) ? undefined : { ref: id, reason: "gone" };
    }
    if (inline.type !== "valueRef") {
      return undefined;
    }
    const a = inline.attrs as { ref: string; attribute?: string };
    if (a.attribute === undefined) {
      const v = variableById.get(a.ref);
      return v === undefined
        ? { ref: a.ref, reason: "gone" }
        : v.scope === "global" || v.board_id === board
          ? undefined
          : { ref: a.ref, reason: "board" };
    }
    const c = componentById.get(a.ref);
    const d = definitionsOf
      .get(c?.component_type_id ?? "")
      ?.find((x) => x.id === a.attribute);
    return d === undefined
      ? { ref: a.ref, reason: "gone" }
      : d.runtime
        ? undefined
        : { ref: a.ref, reason: "runtime" };
  };
  const segments = (
    text: RichTextDoc | undefined,
    board: string,
    at: { entity: string; field: "text" | "label"; node: string },
  ): Segment[] | undefined => {
    if (text === undefined) {
      return undefined;
    }
    const parsed = tryParseRichTextOfAnySize(text);
    if (parsed.error !== undefined) {
      standIns.push({ kind: "text", board, ...at });
      return undefined;
    }
    let doc = parsed.doc;
    const dropped: Unreadable[] = [];
    const kept = keepInlines(doc.content, (inline) => {
      const ref = unreadable(inline, board);
      if (ref !== undefined) {
        dropped.push(ref);
        return false;
      }
      return true;
    });
    if (dropped.length > 0) {
      for (const { ref, reason } of dropped) {
        standIns.push({ kind: "reference", board, ...at, ref, reason });
      }
      // The texts on either side of what went join, as the server would
      // store the document without it.
      const again = tryParseRichTextOfAnySize({ type: "doc", content: kept });
      doc =
        again.error === undefined
          ? again.doc
          : { type: "doc", content: [{ type: "paragraph" }] };
    }
    const segs = segmentsOf(doc, segmentNames);
    return segs.length === 0 ? undefined : segs;
  };

  // The boards' files.
  const boardFiles = new Map<string, BoardFile>();
  const index: ManifestBoard[] = [];
  let connectionCount = 0;
  // The boards' slugs, which name their files.
  const slugs = new Set<string>();
  for (const b of boards) {
    if (slugs.has(b.slug)) {
      throw new ProjectionError(
        "inconsistent",
        `two boards have the slug ${b.slug}`,
      );
    }
    slugs.add(b.slug);
    const own = nodesOf.get(b.id) ?? [];
    const ports = new Map<string, Set<string>>();
    const fileNodes: Node[] = [];
    for (const n of own) {
      const at = (field: ExpressionField): ExpressionAt => ({
        entity: n.id,
        owner: "node",
        field,
        node: n.id,
      });
      const title = n.title === "" ? {} : { title: n.title };
      const position = {
        x: hundredths(n.position.x),
        y: hundredths(n.position.y),
      };
      if (n.type === "element") {
        ports.set(n.id, new Set([OUT]));
        let speaker: string | undefined = n.speaker_component_id;
        if (speaker !== undefined && !componentById.has(speaker)) {
          standIns.push({
            kind: "speaker",
            board: b.id,
            node: n.id,
            entity: n.id,
            ref: speaker,
          });
          speaker = undefined;
        }
        const segs = segments(n.text, b.id, {
          entity: n.id,
          field: "text",
          node: n.id,
        });
        const onEnter = script(n.on_enter, b.id, at("onEnter"));
        const onExit = script(n.on_exit, b.id, at("onExit"));
        fileNodes.push({
          ...(n.cover_asset_id === undefined
            ? {}
            : { cover: n.cover_asset_id }),
          id: n.id,
          isEnding: n.is_ending,
          kind: n.kind ?? "dialogue",
          ...(onEnter === undefined ? {} : { onEnter }),
          ...(onExit === undefined ? {} : { onExit }),
          position,
          ...(speaker === undefined ? {} : { speaker }),
          ...(n.tags.length === 0 ? {} : { tags: [...n.tags] }),
          ...(segs === undefined
            ? {}
            : {
                text: { preview: previewOf(plainTextOf(segs)), segments: segs },
              }),
          ...title,
          type: "element",
        });
      } else if (n.type === "branch") {
        const outputs = branchOutputs(n);
        ports.set(n.id, new Set(outputs.map((o) => o.id)));
        const onEnter = script(n.on_enter, b.id, at("onEnter"));
        const onExit = script(n.on_exit, b.id, at("onExit"));
        fileNodes.push({
          id: n.id,
          ...(onEnter === undefined ? {} : { onEnter }),
          ...(onExit === undefined ? {} : { onExit }),
          outputs: outputs.map((o, order): BranchOutput => {
            if (o.kind === "else") {
              return { id: o.id, kind: "else", order };
            }
            const where: ExpressionAt = {
              entity: o.id,
              owner: "output",
              field: "condition",
              node: n.id,
            };
            let c = condition(o.condition, b.id, where);
            if (c === undefined) {
              standIns.push({
                kind: "expression",
                board: b.id,
                ...where,
                source: "",
                code: "",
              });
              c = { ast: literalFalse, source: "" };
            }
            return { condition: c, id: o.id, kind: o.kind, order };
          }),
          position,
          ...title,
          type: "branch",
        });
      } else {
        ports.set(n.id, new Set());
        let target: Entry | undefined;
        const t = n.target;
        if (t?.node_id !== undefined) {
          const to = nodes.get(t.node_id);
          if (
            to === undefined ||
            to.type === "jumper" ||
            (t.board_id !== undefined && t.board_id !== to.board_id)
          ) {
            standIns.push({
              kind: "target",
              board: b.id,
              node: n.id,
              entity: n.id,
              ref: t.node_id,
              reason:
                to === undefined
                  ? "gone"
                  : to.type === "jumper"
                    ? "jumper"
                    : "board",
            });
          } else {
            target = { boardId: to.board_id, nodeId: to.id };
          }
        }
        fileNodes.push({
          id: n.id,
          position,
          ...(target === undefined ? {} : { target }),
          ...title,
          type: "jumper",
        });
      }
    }

    // The board's connections, each port's in their author's order.
    const byPort = new Map<string, ConnectionBody[]>();
    for (const c of connectionsOf.get(b.id) ?? []) {
      const to = nodes.get(c.to_node_id);
      const port = ports.get(c.from_node_id)?.has(c.from_port) === true;
      if (!port || to === undefined || to.board_id !== b.id) {
        if (nodes.has(c.from_node_id)) {
          standIns.push({
            kind: "connection",
            board: b.id,
            node: c.from_node_id,
            entity: c.id,
            reason: port ? "target" : "port",
          });
        }
        continue;
      }
      const key = `${c.from_node_id} ${c.from_port}`;
      const list = byPort.get(key) ?? [];
      list.push(c);
      byPort.set(key, list);
    }
    const fileConnections: Connection[] = [];
    for (const list of byPort.values()) {
      list.sort(compareChildren);
      list.forEach((c, order) => {
        // A connection that leaves an element is a choice; one that leaves a
        // branch the flow passes on by alone.
        const owner =
          nodes.get(c.from_node_id)?.type === "branch"
            ? "connection"
            : "choice";
        const at = (field: ExpressionField): ExpressionAt => ({
          entity: c.id,
          owner,
          field,
          node: c.from_node_id,
        });
        const label = segments(c.label, b.id, {
          entity: c.id,
          field: "label",
          node: c.from_node_id,
        });
        const cond = condition(c.condition, b.id, at("condition"));
        const onTraverse = script(c.on_traverse, b.id, at("onTraverse"));
        fileConnections.push({
          ...(cond === undefined ? {} : { condition: cond }),
          from: c.from_node_id,
          fromPort: c.from_port,
          id: c.id,
          ...(label === undefined ? {} : { label: { segments: label } }),
          ...(onTraverse === undefined ? {} : { onTraverse }),
          order,
          to: c.to_node_id,
        });
      });
    }
    fileConnections.sort(byId);
    connectionCount += fileConnections.length;

    let entryNodeId = b.entry_node_id;
    if (
      entryNodeId !== undefined &&
      nodes.get(entryNodeId)?.board_id !== b.id
    ) {
      standIns.push({
        kind: "board_entry",
        board: b.id,
        entity: b.id,
        ref: entryNodeId,
      });
      entryNodeId = undefined;
    }
    const info = {
      ...(entryNodeId === undefined ? {} : { entryNodeId }),
      id: b.id,
      name: b.name,
      slug: b.slug,
    };
    boardFiles.set(b.id, {
      board: info,
      connections: fileConnections,
      nodes: fileNodes,
      schemaVersion: 1,
    });
    index.push({ ...info, file: boardPath(b.slug) });
  }

  // The project's entry: its node, of its board, or its board's entry node.
  let entry: Entry | undefined;
  const e = project.entry;
  if (e.node_id !== undefined) {
    const n = nodes.get(e.node_id);
    if (
      n === undefined ||
      (e.board_id !== undefined && e.board_id !== n.board_id)
    ) {
      standIns.push({
        kind: "entry",
        entity: project.id,
        ref: e.node_id,
        reason: n === undefined ? "gone" : "board",
        ...(e.board_id === undefined ? {} : { board: e.board_id }),
      });
    } else {
      entry = { boardId: n.board_id, nodeId: n.id };
    }
  } else if (e.board_id !== undefined) {
    const at = boardFiles.get(e.board_id)?.board.entryNodeId;
    if (!boardIds.has(e.board_id)) {
      standIns.push({
        kind: "entry",
        entity: project.id,
        ref: e.board_id,
        reason: "gone",
      });
    } else if (at !== undefined) {
      entry = { boardId: e.board_id, nodeId: at };
    }
  }

  const settings: Settings = {
    ...(entry === undefined ? {} : { entry }),
    schemaVersion: 1,
    signals: [...signals].sort(byCodePoints),
  };
  const variablesFile = {
    schemaVersion: 1,
    variables: variables.map(
      (v) =>
        ({
          ...(v.scope === "board" ? { boardId: v.board_id } : {}),
          default: v.default,
          ...(v.description === "" ? {} : { description: v.description }),
          id: v.id,
          key: v.key,
          ...(v.scope === "board" && v.reset_on_enter
            ? { resetOnEnter: true }
            : {}),
          scope: v.scope,
          type: v.type,
        }) as Variable,
    ),
  };
  const usedTypes = new Set(components.map((c) => c.component_type_id));
  const componentTypesFile = {
    componentTypes: [...typeById.values()]
      .filter((t) => usedTypes.has(t.id))
      .sort(byId),
    schemaVersion: 1,
  };
  const componentsFile = {
    components: components.map((c): Component => {
      const values: Component["attributes"] = {};
      for (const d of definitionsOf.get(c.component_type_id) ?? []) {
        const v = c.attributes?.[d.id];
        values[d.id] = v === undefined ? d.default : v;
      }
      return {
        attributes: values,
        componentTypeId: c.component_type_id,
        id: c.id,
        ...(c.image_asset_id === undefined ? {} : { image: c.image_asset_id }),
        key: c.key,
        name: c.name,
      };
    }),
    schemaVersion: 1,
  };

  const files = new Map<string, Uint8Array<ArrayBuffer>>();
  files.set("project/settings.json", encoded(settings));
  files.set("project/variables.json", encoded(variablesFile));
  files.set("project/component_types.json", encoded(componentTypesFile));
  files.set("project/components.json", encoded(componentsFile));
  for (const f of boardFiles.values()) {
    files.set(boardPath(f.board.slug), encoded(f));
  }
  const pulledAt = timestampOf(options.now ?? new Date());
  // The files' hashes, as load hashes each, at one pace, of bytes that are
  // the projection's own, asked for all at once, rather than each once the
  // one before is done, which the pace keeps to YIELD_EVERY bytes a task
  // all the same, where the page has no WebCrypto.
  const pace: Pace = { hashed: 0 };
  const paths = [...files.keys()].sort(byCodePoints);
  const { signal } = options;
  const sums = await Promise.all(
    paths.map((path) =>
      fileHash(files.get(path) as Uint8Array<ArrayBuffer>, pace, true, signal),
    ),
  );
  signal?.throwIfAborted();
  const hashes: Record<string, string> = {};
  let all = "";
  paths.forEach((path, i) => {
    hashes[path] = sums[i];
    all += `${path} ${sums[i]}\n`;
  });
  const manifest: Manifest = {
    astVersion: versionOf(asts),
    // In the order of the boards' IDs, as the boards are.
    boards: index,
    counts: {
      boards: index.length,
      components: components.length,
      connections: connectionCount,
      nodes: nodes.size,
      variables: variables.length,
    },
    ...(entry === undefined ? {} : { entry }),
    files: hashes,
    generator: { ...GENERATOR },
    project: { id: project.id, name: project.name, slug: project.slug },
    schemaVersion: 1,
    snapshot: {
      channel: "draft",
      // As the conformance suite's fixtures have it: the hash of the files'
      // paths and hashes, which no runtime checks (plan item 78).
      contentHash: await fileHash(
        new TextEncoder().encode(all),
        pace,
        true,
        signal,
      ),
      pulledAt,
    },
  };
  files.set("storycreep.json", encoded(manifest));
  const values: ProjectedValue[] = variables.map((v) => ({
    kind: "variable",
    key: v.id,
    variable: v.id,
    name: v.key,
    type: v.type,
    options: [],
    ...(v.scope === "board" && v.board_id !== undefined
      ? { board: v.board_id }
      : {}),
  }));
  for (const c of components) {
    for (const d of definitionsOf.get(c.component_type_id) ?? []) {
      if (d.runtime && d.type !== "text") {
        values.push({
          kind: "attribute",
          key: attributeKey(c.id, d.id),
          component: c.id,
          attribute: d.id,
          name: `${c.key}.${d.key}`,
          type: d.type,
          options: d.type === "enum" ? [...(d.options ?? [])] : [],
        });
      }
    }
  }
  return {
    files,
    boards: boardFiles,
    componentNames: new Map(components.map((c) => [c.id, c.name])),
    standIns,
    conditions,
    values,
  };
}

/** Returns the path of the file of the board whose slug is slug. */
function boardPath(slug: string): string {
  return `boards/${slug}.json`;
}

// branchOutputs returns a branch's outputs that are not in the trash, each
// of the kind of its place (FR-EDIT-04): its conditional ones in their
// author's order, the first of them its if and the others its elseifs, and
// then its else, which it has only while it has a conditional output.
function branchOutputs(
  n: NodeBody,
): { id: string; kind: "if" | "elseif" | "else"; condition?: Expression }[] {
  const outputs = (n.outputs ?? []).filter((o) => o.deleted_at === undefined);
  const conditional = outputs
    .filter((o) => o.kind !== "else")
    .sort((a, b) =>
      compareChildren(
        { id: a.id, order: a.order ?? "" },
        { id: b.id, order: b.order ?? "" },
      ),
    );
  const out: {
    id: string;
    kind: "if" | "elseif" | "else";
    condition?: Expression;
  }[] = conditional.map((o, i) => ({
    id: o.id,
    kind: i === 0 ? "if" : "elseif",
    ...(o.condition === undefined ? {} : { condition: o.condition }),
  }));
  const otherwise = outputs.find((o) => o.kind === "else");
  if (otherwise !== undefined && out.length > 0) {
    out.push({ id: otherwise.id, kind: "else" });
  }
  return out;
}

// keepInlines returns blocks with only the inline nodes that keep tells.
function keepInlines(
  blocks: readonly RichTextBlock[],
  keep: (inline: { type: string; attrs?: unknown }) => boolean,
): RichTextBlock[] {
  return blocks.map((b): RichTextBlock => {
    if (b.type === "paragraph") {
      return b.content === undefined
        ? b
        : { ...b, content: b.content.filter(keep) };
    }
    return {
      ...b,
      content: b.content.map((item) => ({
        ...item,
        content: keepInlines(item.content, keep),
      })),
    };
  });
}

function byId(a: { id: string }, b: { id: string }): number {
  return byCodePoints(a.id, b.id);
}

// timestampOf returns the moment t as a published Timestamp, in UTC, to the
// second.
function timestampOf(t: Date): string {
  return `${t.toISOString().slice(0, 19)}Z`;
}

/**
 * Returns the bytes of v as a file of the published format is laid out
 * (§6.2), as the server writes it: UTF-8, each object's keys sorted by
 * their code points, indented by two spaces, with ", \, the control
 * characters, and U+2028 and U+2029 escaped, as Go's encoding/json escapes
 * them, HTML not, and a line feed at its end.
 */
export function encoded(v: unknown): Uint8Array<ArrayBuffer> {
  const text = JSON.stringify(sortedKeys(v), null, 2).replace(
    /[\u2028\u2029]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16)}`,
  );
  return new TextEncoder().encode(`${text}\n`);
}

// sortedKeys returns v with each object's keys in the order of their code
// points.
function sortedKeys(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(sortedKeys);
  }
  if (typeof v === "object" && v !== null) {
    const o = v as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(o).sort(byCodePoints)) {
      if (o[k] !== undefined) {
        out[k] = sortedKeys(o[k]);
      }
    }
    return out;
  }
  return v;
}
