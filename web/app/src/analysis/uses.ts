// The client-side dependency index (plan item 89, note N96): the TypeScript
// twin of the server's index of what a project's content reads and writes
// (package dependency, PRD FR-DEP-01, §5.3 expression_ref, note N53), of a
// draft (draft.ts), and the usage queries of a variable and of a component
// that the server answers from it (FR-DEP-01 AC2, FR-COMP-04).
//
// A site is an expression of the draft, a node's on-enter or on-exit
// script, a connection's condition or on-traverse script, or a branch
// output's condition; or a text, a node's, a connection's label, or a
// component's description. Each names what its uses say (Use): an
// expression what its AST names (usesOf), one that does not compile
// nothing, and a text what its value references and mentions name
// (textUsesOf). The server's index holds the sites in the trash too, which
// every query passes over; the draft holds none.

import type { Usage } from "@storycreep/shared/api";
import type { Node as ExprNode } from "@storycreep/shared/expr/compile";
import { walk } from "@storycreep/shared/expr/compile";
import { compareIds, type Draft } from "./draft";

/**
 * The kind of what a site names: a variable; an attribute of a component,
 * by the component and its definition; a node, which an expression names
 * in a node reference, as visited() reads; and a component that a text
 * mentions.
 */
export type UseKind = "variable" | "attribute" | "node" | "component";

/** How a site uses what it names: a read, or a write, of a variable or an attribute that a script assigns. */
export type Access = "read" | "write";

/** Something that a site names, and how it uses it. */
export interface Use {
  readonly kind: UseKind;
  /** The ID of the variable, the node, or the component, an attribute's among them. */
  readonly target: string;
  /** The ID of an attribute's definition, for an attribute alone. */
  readonly attribute?: string;
  readonly access: Access;
}

// compareUses orders uses by their kinds, their targets, their attributes,
// and their accesses, as the server lists a site's.
function compareUses(a: Use, b: Use): number {
  return (
    compareStrings(a.kind, b.kind) ||
    compareIds(a.target, b.target) ||
    compareIds(a.attribute, b.attribute) ||
    compareStrings(a.access, b.access)
  );
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

// sorted returns uses in order (compareUses), each once.
function sorted(uses: Use[]): Use[] {
  uses.sort(compareUses);
  return uses.filter((u, i) => i === 0 || compareUses(uses[i - 1], u) !== 0);
}

/**
 * Returns what the AST n names, each once, in order, as the server's
 * dependency.Uses does: an AST that compiled, whose names hold their IDs. A
 * condition reads every name that it holds. A script writes the target of
 * each assignment, and reads it too when the assignment is one of +=, -=,
 * *=, /=, and %=, which reads the value that it updates, and reads every
 * name in an assignment's value and in a call's arguments, as
 * visited(node("nd_…")) reads the node.
 */
export function usesOf(n: ExprNode): Use[] {
  // The target of each assignment, and whether the assignment reads it.
  const targets = new Map<ExprNode, boolean>();
  walk(n, (x) => {
    if (x.kind === "assign") {
      targets.set(x.target, x.op !== "=");
    }
  });
  const uses: Use[] = [];
  const add = (
    use: Omit<Use, "access">,
    written: boolean,
    read: boolean,
  ): void => {
    if (written) {
      uses.push({ ...use, access: "write" });
    }
    if (read) {
      uses.push({ ...use, access: "read" });
    }
  };
  walk(n, (x) => {
    // A name that is no assignment's target is read; a target is written,
    // and read too by an update.
    const updates = targets.get(x);
    const target = updates !== undefined;
    const read = updates ?? true;
    switch (x.kind) {
      case "var":
        add({ kind: "variable", target: x.ref }, target, read);
        break;
      case "attr":
        add(
          { kind: "attribute", target: x.ref, attribute: x.attribute },
          target,
          read,
        );
        break;
      case "node_ref":
        add({ kind: "node", target: x.ref }, false, true);
        break;
    }
  });
  return sorted(uses);
}

/**
 * Returns what the rich-text document doc names, each once, in order, as
 * the server's dependency.TextUses does: the variable, or the component's
 * attribute, of each value reference, and the component of each mention,
 * each read. A mention of a member names a user, nothing of the story's.
 */
export function textUsesOf(doc: unknown): Use[] {
  const uses: Use[] = [];
  for (const inline of referencesOf(doc)) {
    if (inline.type === "mention") {
      uses.push({ kind: "component", target: inline.id, access: "read" });
    } else if (inline.attribute === undefined) {
      uses.push({ kind: "variable", target: inline.ref, access: "read" });
    } else {
      uses.push({
        kind: "attribute",
        target: inline.ref,
        attribute: inline.attribute,
        access: "read",
      });
    }
  }
  return sorted(uses);
}

/** A value reference or a mention of a text, as its document holds it. */
export type TextReference =
  | { readonly type: "mention"; readonly id: string }
  | {
      readonly type: "valueRef";
      readonly ref: string;
      readonly attribute?: string;
    };

/**
 * Returns the value references and the mentions of the rich-text document
 * doc, each as often as it holds it, in the order of the document: every
 * object of it, at any depth, of the type valueRef or mention, as the
 * server's validation reads a stored text's with jsonpath.
 */
export function referencesOf(doc: unknown): TextReference[] {
  const out: TextReference[] = [];
  const visit = (v: unknown): void => {
    if (Array.isArray(v)) {
      for (const x of v) {
        visit(x);
      }
      return;
    }
    if (typeof v !== "object" || v === null) {
      return;
    }
    const o = v as { type?: unknown; attrs?: Record<string, unknown> };
    if (o.type === "mention") {
      out.push({ type: "mention", id: String(o.attrs?.id ?? "") });
    } else if (o.type === "valueRef") {
      const attribute = o.attrs?.attribute;
      out.push({
        type: "valueRef",
        ref: String(o.attrs?.ref ?? ""),
        ...(typeof attribute === "string" && attribute !== ""
          ? { attribute }
          : {}),
      });
    }
    for (const x of Object.values(o)) {
      visit(x);
    }
  };
  visit(doc);
  return out;
}

/**
 * What a site is, as a usage names it: a node's on-enter or on-exit
 * script, or its text; a connection's condition, its on-traverse script, or
 * its label; a branch output's condition; or a component's description.
 */
export type SiteType = Exclude<Usage["site"], "node_speaker">;

/** A site of the index, with what it names. */
export interface Site {
  readonly type: SiteType;
  /** The ID of the site's owner: the node, the connection, the output, or the component. */
  readonly owner: string;
  /** The site's board; a component's description has none. */
  readonly board?: string;
  /** The site's node: a node's own, the node that a connection leaves, or an output's branch; a component's description has none. */
  readonly node?: string;
  readonly uses: readonly Use[];
}

/** The draft's index: each site that names something, with what it names. */
export interface Index {
  readonly sites: readonly Site[];
}

/** Returns the index of the draft: its expressions that compile, and its texts, each with what it names. */
export function indexOf(draft: Draft): Index {
  const sites: Site[] = [];
  for (const e of draft.expressions) {
    if (e.ast === undefined) {
      continue;
    }
    const uses = usesOf(e.ast);
    if (uses.length > 0) {
      sites.push({
        type:
          e.owner === "branch_output"
            ? "branch_output_condition"
            : `${e.owner}_${e.field}`,
        owner: e.id,
        board: e.board,
        node: e.node,
        uses,
      } as Site);
    }
  }
  for (const t of draft.texts) {
    const uses = textUsesOf(t.doc);
    if (uses.length > 0) {
      sites.push({
        type:
          t.owner === "node"
            ? "node_text"
            : t.owner === "connection"
              ? "connection_label"
              : "component_description",
        owner: t.id,
        ...(t.board === undefined ? {} : { board: t.board }),
        ...(t.node === undefined ? {} : { node: t.node }),
        uses,
      });
    }
  }
  return { sites };
}

/** The sites, in the order of their enum, by which the client orders two sites of one node. */
export const siteTypes: readonly Usage["site"][] = [
  "node_on_enter",
  "node_on_exit",
  "node_text",
  "node_speaker",
  "connection_condition",
  "connection_on_traverse",
  "connection_label",
  "branch_output_condition",
  "component_description",
];

/** A use of a component that its usages list (FR-COMP-04). */
export type ComponentUse = "speaker" | "mention" | "read" | "write";

/**
 * Returns the sites of the draft that read the variable, or that write it,
 * as access says (FR-DEP-01 AC2), each as the server's usages list it, in
 * the order of usageOrder; or undefined for a variable that the draft does
 * not hold, whose usages the server answers with 404, though a text of the
 * draft may show it still, as one does a variable that a forced deletion
 * took.
 */
export function variableUsages(
  draft: Draft,
  index: Index,
  variable: string,
  access: Access,
): Usage[] | undefined {
  if (!draft.variables.has(variable)) {
    return undefined;
  }
  return usagesOf(
    draft,
    index,
    (u) =>
      u.kind === "variable" && u.target === variable && u.access === access,
  );
}

/**
 * Returns the sites of the draft that use the component as use says
 * (FR-COMP-04): the nodes whose speaker it is, the texts that mention it,
 * or the sites that read its attributes, or write them, each of them once
 * for each attribute that it reads or writes, as the server's usages list
 * them, in the order of usageOrder; or undefined for a component that the
 * draft does not hold, whose usages the server answers with 404, though a
 * node of the draft may have it as its speaker still, and a text mention
 * it, as they do a component that a forced deletion took.
 */
export function componentUsages(
  draft: Draft,
  index: Index,
  component: string,
  use: ComponentUse,
): Usage[] | undefined {
  if (!draft.components.has(component)) {
    return undefined;
  }
  if (use === "speaker") {
    const out: Usage[] = [];
    for (const n of draft.nodes.values()) {
      if (n.speaker_component_id === component) {
        out.push({
          site: "node_speaker",
          board_id: n.board_id,
          board_name: draft.boards.get(n.board_id)?.name ?? "",
          node_id: n.id,
          node_title: n.title,
        });
      }
    }
    return out.sort(usageOrder);
  }
  return usagesOf(draft, index, (u) =>
    use === "mention"
      ? u.kind === "component" && u.target === component
      : u.kind === "attribute" && u.target === component && u.access === use,
  );
}

// usagesOf returns a usage of each site of index for each of its uses that
// keep keeps, with its board and its node, in the order of usageOrder.
function usagesOf(
  draft: Draft,
  index: Index,
  keep: (u: Use) => boolean,
): Usage[] {
  const out: Usage[] = [];
  for (const s of index.sites) {
    for (const u of s.uses) {
      if (!keep(u)) {
        continue;
      }
      const usage: Usage = { site: s.type };
      if (s.board !== undefined) {
        usage.board_id = s.board;
        usage.board_name = draft.boards.get(s.board)?.name ?? "";
      }
      if (s.node !== undefined) {
        usage.node_id = s.node;
        usage.node_title = draft.nodes.get(s.node)?.title ?? "";
      }
      switch (s.type) {
        case "connection_condition":
        case "connection_on_traverse":
        case "connection_label":
          usage.connection_id = s.owner;
          break;
        case "branch_output_condition":
          usage.branch_output_id = s.owner;
          break;
        case "component_description":
          usage.component_id = s.owner;
          break;
      }
      if (u.attribute !== undefined) {
        usage.attribute_id = u.attribute;
      }
      out.push(usage);
    }
  }
  return out.sort(usageOrder);
}

/**
 * Orders usages by their boards, their nodes, their sites' types, the
 * entities of their sites, and their attributes. The server orders a node's
 * expressions by their own IDs, which no client holds, so its order of two
 * sites of one node may differ; the sites are the same.
 */
export function usageOrder(a: Usage, b: Usage): number {
  return (
    compareIds(a.board_id, b.board_id) ||
    compareIds(a.node_id, b.node_id) ||
    siteTypes.indexOf(a.site) - siteTypes.indexOf(b.site) ||
    compareIds(
      a.connection_id ?? a.branch_output_id ?? a.component_id,
      b.connection_id ?? b.branch_output_id ?? b.component_id,
    ) ||
    compareIds(a.attribute_id, b.attribute_id)
  );
}
