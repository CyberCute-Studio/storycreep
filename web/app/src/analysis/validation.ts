// The client-side validation (plan item 89, note N96): the TypeScript twin
// of the server's rules (package validation, PRD FR-DEP-03, note N55), of a
// draft (draft.ts) and its index (uses.ts), each finding as the server's
// report holds it, but for its message, of which it gives the arguments,
// and for a mute, which is no part of the rules. The rules are those of
// rules.go, in its order, and the report's order is the server's, so that
// the two give the same findings, in the same order, of the same draft.

import type { Finding, Target } from "@storycreep/shared/api";
import {
  decode,
  type Node as ExprNode,
  encode,
} from "@storycreep/shared/expr/compile";
import { compareIds, type Draft, portOf } from "./draft";
import { Prover } from "./overlap";
import { type Index, referencesOf } from "./uses";

/** A rule's stable code (FR-DEP-03). */
export type Code = Finding["code"];

/** How much a finding matters: an error blocks publishing, a warning needs an acknowledgement to publish, and an info neither. */
export type Severity = Finding["severity"];

/** A rule of the validation: its code and its severity. */
export interface Rule {
  readonly code: Code;
  readonly severity: Severity;
}

/** The rules, in the order of FR-DEP-03's table, as the server's are. */
export const rules: readonly Rule[] = [
  { code: "expr_invalid", severity: "error" },
  { code: "ref_dangling", severity: "error" },
  { code: "jumper_broken", severity: "error" },
  { code: "jumper_invalid", severity: "error" },
  { code: "branch_no_outputs", severity: "error" },
  { code: "port_unconnected", severity: "warning" },
  { code: "var_read_never_written", severity: "warning" },
  { code: "var_written_never_read", severity: "warning" },
  { code: "node_unreachable", severity: "warning" },
  { code: "node_empty", severity: "warning" },
  { code: "choice_duplicate_label", severity: "warning" },
  { code: "dead_end", severity: "warning" },
  { code: "speaker_missing", severity: "info" },
  { code: "board_entry_missing", severity: "warning" },
  { code: "connection_self", severity: "warning" },
  { code: "connection_parallel", severity: "warning" },
];

const severityOf = new Map(rules.map((r) => [r.code, r.severity]));

const ranks: Readonly<Record<Severity, number>> = {
  error: 0,
  warning: 1,
  info: 2,
};

// order holds the place of each code's findings in a report: its rule's
// severity's place among the severities, and then its rule's among the
// rules.
const order = new Map(
  rules.map((r, i) => [r.code, ranks[r.severity] * rules.length + i]),
);

/** The arguments of a finding's message, as the server's catalog's message of its code reads them (validation.<code>). */
export type Args = Readonly<Record<string, string | number>>;

/**
 * A finding, as the server's report holds it but for its message, of which
 * it has the arguments, and for a mute, which the rules do not read.
 */
export type AnalysisFinding = Omit<Finding, "message" | "mute"> & {
  readonly args: Args;
};

/** What an entity of a finding is, as its entity_type, or a target's type, says. */
type Kind = Finding["entity_type"];

/** An entity, by its kind and its ID. */
interface Ref {
  readonly kind: Kind;
  readonly id: string;
}

// What a rule found, before the report names its board and node.
interface Found {
  readonly code: Code;
  readonly entity: Ref;
  readonly field?: Finding["field"];
  // target is what a dangling reference refers to, with its attribute
  // definition for an attribute, a jumper's target, or the connection that
  // a choice or a connection repeats.
  readonly target?: Ref;
  readonly attribute?: string;
  readonly args?: Args;
}

/** The text of an element that has none: one empty paragraph, as the editor's empty document is. */
const emptyText = canonical({ type: "doc", content: [{ type: "paragraph" }] });

/**
 * Returns what the rules find of the draft, whose index is index, in the
 * order of the server's report: by their severities and the order of their
 * rules, their boards, their nodes, their entities, their fields, and what
 * they name, each by its UUID.
 */
export function validate(draft: Draft, index: Index): AnalysisFinding[] {
  const c = new Checker(draft, index);
  c.findExpressions();
  c.findReferences();
  c.findJumpers();
  c.findBranches();
  c.findVariables();
  c.findReachability();
  c.findElements();
  c.findConnections();
  c.findBoards();
  return c.out.sort(compareFindings);
}

/** Orders findings as the server's report lists them. */
export function compareFindings(
  a: AnalysisFinding,
  b: AnalysisFinding,
): number {
  return (
    (order.get(a.code) ?? 0) - (order.get(b.code) ?? 0) ||
    compareIds(a.board_id, b.board_id) ||
    compareIds(a.node_id, b.node_id) ||
    compareIds(a.entity_id, b.entity_id) ||
    compareStrings(a.field ?? "", b.field ?? "") ||
    compareIds(a.target?.id, b.target?.id) ||
    compareIds(a.target?.attribute_id, b.target?.attribute_id)
  );
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

// The reasons that a reference dangles, as the message of ref_dangling says
// them: what it refers to is not a part of the draft, or is an attribute
// that its component does not have (deleted); or a node's text or a
// connection's label shows what an expression of its board does not read
// (FR-TEXT-02, §5.8.3), a variable of another board (scope), or an
// attribute that is not runtime (runtime).
type Dangling = "deleted" | "scope" | "runtime";

// The checker holds what the rules look up of a draft, once.
class Checker {
  readonly out: AnalysisFinding[] = [];
  // from are the connections that leave each node, by any port; choices
  // those that leave each element by its out, in their order; fromOutput
  // how many leave each output; into how many enter each node.
  private readonly from = new Map<string, Draft["connections"][number][]>();
  private readonly choices = new Map<string, Draft["connections"][number][]>();
  private readonly fromOutput = new Map<string, number>();
  private readonly into = new Map<string, number>();
  // outputsOf is the number of each branch's outputs.
  private readonly outputsOf = new Map<string, number>();
  // roots are the nodes of each board that no connection enters.
  private readonly roots = new Map<string, string[]>();
  // conditions are the connections' conditions, by their IDs: the AST of
  // one that compiles, as the server stores it, or undefined, with valid
  // false, for one that does not. A connection without a condition has
  // none here.
  private readonly conditions = new Map<
    string,
    { ast?: ExprNode; valid: boolean }
  >();
  // canonicals are the conditions' ASTs as the server stores them, each
  // made once it is first compared.
  private readonly canonicals = new Map<string, ExprNode | undefined>();
  // entries are the entry nodes of the boards whose entry node is a node of
  // the draft, by the boards' IDs.
  private readonly entries = new Map<string, string>();
  // connections are the draft's connections, by their IDs.
  private readonly connections = new Map<
    string,
    Draft["connections"][number]
  >();

  constructor(
    private readonly d: Draft,
    private readonly index: Index,
  ) {
    for (const o of d.outputs.values()) {
      this.outputsOf.set(o.node.id, (this.outputsOf.get(o.node.id) ?? 0) + 1);
    }
    for (const cn of d.connections) {
      this.connections.set(cn.id, cn);
      push(this.from, cn.from_node_id, cn);
      const port = portOf(cn);
      if (port === undefined) {
        push(this.choices, cn.from_node_id, cn);
      } else {
        this.fromOutput.set(port, (this.fromOutput.get(port) ?? 0) + 1);
      }
      this.into.set(cn.to_node_id, (this.into.get(cn.to_node_id) ?? 0) + 1);
    }
    for (const n of d.nodes.values()) {
      if (!this.into.has(n.id)) {
        push(this.roots, n.board_id, n.id);
      }
    }
    for (const e of d.expressions) {
      if (e.owner === "connection" && e.field === "condition") {
        this.conditions.set(e.id, {
          ...(e.ast === undefined ? {} : { ast: e.ast }),
          valid: e.ast !== undefined,
        });
      }
    }
    for (const b of d.boards.values()) {
      if (b.entry_node_id !== undefined && d.nodes.has(b.entry_node_id)) {
        this.entries.set(b.id, b.entry_node_id);
      }
    }
  }

  // add adds a finding of what a rule found, with the board and the node
  // by which a client opens its entity (context), and the board's name and
  // the node's title, which a client names it by.
  private add(f: Found): void {
    const [board, node] = this.context(f.entity);
    let target: Target | undefined;
    if (f.target !== undefined) {
      target =
        f.attribute === undefined
          ? { type: f.target.kind as Target["type"], id: f.target.id }
          : { type: "attribute", id: f.target.id, attribute_id: f.attribute };
    }
    this.out.push({
      code: f.code,
      severity: severityOf.get(f.code) ?? "error",
      entity_type: f.entity.kind,
      entity_id: f.entity.id,
      ...(f.field === undefined ? {} : { field: f.field }),
      ...(board === undefined
        ? {}
        : {
            board_id: board,
            board_name: this.d.boards.get(board)?.name ?? "",
          }),
      ...(node === undefined
        ? {}
        : { node_id: node, node_title: this.d.nodes.get(node)?.title ?? "" }),
      ...(target === undefined ? {} : { target }),
      args: f.args ?? {},
    });
  }

  // context returns the board and the node by which a client opens the
  // entity e: a node's own and its board, the node that a connection
  // leaves, an output's branch, a board's own, a board's variable's board,
  // and none for a global variable, a component, or the project.
  private context(e: Ref): [string | undefined, string | undefined] {
    switch (e.kind) {
      case "node": {
        const n = this.d.nodes.get(e.id);
        return n === undefined ? [undefined, undefined] : [n.board_id, n.id];
      }
      case "connection": {
        const cn = this.connections.get(e.id);
        return cn === undefined
          ? [undefined, undefined]
          : [cn.board_id, cn.from_node_id];
      }
      case "branch_output": {
        const o = this.d.outputs.get(e.id);
        return o === undefined
          ? [undefined, undefined]
          : [o.node.board_id, o.node.id];
      }
      case "board":
        return [e.id, undefined];
      case "variable": {
        const v = this.d.variables.get(e.id);
        return [v?.scope === "board" ? v.board_id : undefined, undefined];
      }
    }
    return [undefined, undefined];
  }

  // findExpressions finds the expressions that do not compile
  // (expr_invalid), one finding of each, at its owner's field.
  findExpressions(): void {
    for (const e of this.d.expressions) {
      if (e.ast === undefined) {
        this.add({
          code: "expr_invalid",
          entity: { kind: e.owner, id: e.id },
          field: e.field,
          args: { error: e.error ?? "" },
        });
      }
    }
  }

  // findReferences finds what refers to what is not a part of the draft,
  // or to an attribute of a component that the component's type does not
  // define, and the value references of a node's text or a connection's
  // label that read what an expression of its board does not
  // (ref_dangling), once for each thing that each field refers to: a
  // text's value references and mentions, an element's speaker, a board's
  // entry node, and the project's entry, its board and its node. A jumper's
  // target is jumper_broken's.
  findReferences(): void {
    const seen = new Set<string>();
    const d = this.d;
    for (const t of d.texts) {
      const entity: Ref = { kind: t.owner, id: t.id };
      const field =
        t.owner === "node"
          ? "text"
          : t.owner === "connection"
            ? "label"
            : "description";
      // A node's text and a connection's label are shown by a runtime, as
      // an expression of their board reads what they show; a component's
      // description is not, and is of no board.
      const shown = t.board !== undefined;
      for (const r of referencesOf(t.doc)) {
        let reason: Dangling | undefined;
        if (r.type === "mention") {
          if (!d.components.has(r.id)) {
            reason = "deleted";
          }
          if (reason !== undefined) {
            this.dangling(
              {
                code: "ref_dangling",
                entity,
                field,
                target: { kind: "component", id: r.id },
              },
              reason,
              seen,
            );
          }
          continue;
        }
        if (r.attribute === undefined) {
          const v = d.variables.get(r.ref);
          if (v === undefined) {
            reason = "deleted";
          } else if (
            shown &&
            v.scope === "board" &&
            v.board_id !== undefined &&
            v.board_id !== t.board
          ) {
            reason = "scope";
          }
          if (reason !== undefined) {
            this.dangling(
              {
                code: "ref_dangling",
                entity,
                field,
                target: { kind: "variable", id: r.ref },
              },
              reason,
              seen,
            );
          }
          continue;
        }
        const component = d.components.get(r.ref);
        const a = d.attributes.get(r.attribute);
        if (
          component === undefined ||
          a === undefined ||
          a.component_type_id !== component.component_type_id
        ) {
          reason = "deleted";
        } else if (shown && !a.runtime) {
          reason = "runtime";
        }
        if (reason !== undefined) {
          this.dangling(
            {
              code: "ref_dangling",
              entity,
              field,
              target: { kind: "component", id: r.ref },
              attribute: r.attribute,
            },
            reason,
            seen,
          );
        }
      }
    }
    for (const n of d.nodes.values()) {
      const speaker = n.speaker_component_id;
      if (
        n.type === "element" &&
        speaker !== undefined &&
        !d.components.has(speaker)
      ) {
        this.dangling(
          {
            code: "ref_dangling",
            entity: { kind: "node", id: n.id },
            field: "speaker_component_id",
            target: { kind: "component", id: speaker },
          },
          "deleted",
          seen,
        );
      }
    }
    for (const b of d.boards.values()) {
      if (b.entry_node_id !== undefined && !d.nodes.has(b.entry_node_id)) {
        this.dangling(
          {
            code: "ref_dangling",
            entity: { kind: "board", id: b.id },
            field: "entry_node_id",
            target: { kind: "node", id: b.entry_node_id },
          },
          "deleted",
          seen,
        );
      }
    }
    const project: Ref = { kind: "project", id: d.project };
    if (d.entry.board !== undefined && !d.boards.has(d.entry.board)) {
      this.dangling(
        {
          code: "ref_dangling",
          entity: project,
          field: "entry",
          target: { kind: "board", id: d.entry.board },
        },
        "deleted",
        seen,
      );
    }
    if (d.entry.node !== undefined && !d.nodes.has(d.entry.node)) {
      this.dangling(
        {
          code: "ref_dangling",
          entity: project,
          field: "entry",
          target: { kind: "node", id: d.entry.node },
        },
        "deleted",
        seen,
      );
    }
  }

  // dangling adds the finding f of a dangling reference, which dangles for
  // the reason, unless seen holds it already, with the reason and the kind
  // of what it refers to as the message's arguments: a field refers to a
  // thing once however often it names it.
  private dangling(f: Found, reason: Dangling, seen: Set<string>): void {
    const key = [f.entity.id, f.target?.id, f.field, f.attribute].join(" ");
    if (seen.has(key)) {
      return;
    }
    seen.add(key);
    this.add({
      ...f,
      args: {
        target:
          f.attribute === undefined ? (f.target?.kind ?? "") : "attribute",
        reason,
      },
    });
  }

  // findJumpers finds the jumpers without a target that is a node of the
  // draft (jumper_broken), and those whose target is a jumper, or that
  // connections leave (jumper_invalid, §5.8.5).
  findJumpers(): void {
    for (const n of this.d.nodes.values()) {
      if (n.type !== "jumper") {
        continue;
      }
      const self: Ref = { kind: "node", id: n.id };
      const id = n.target?.node_id;
      const target = id === undefined ? undefined : this.d.nodes.get(id);
      if (id === undefined) {
        this.add({
          code: "jumper_broken",
          entity: self,
          field: "target",
          args: { reason: "none" },
        });
      } else if (target === undefined) {
        this.add({
          code: "jumper_broken",
          entity: self,
          field: "target",
          target: { kind: "node", id },
          args: { reason: "deleted" },
        });
      } else if (target.type === "jumper") {
        this.add({
          code: "jumper_invalid",
          entity: self,
          field: "target",
          target: { kind: "node", id: target.id },
          args: { reason: "target" },
        });
      }
      if ((this.from.get(n.id)?.length ?? 0) > 0) {
        this.add({
          code: "jumper_invalid",
          entity: self,
          args: { reason: "connections" },
        });
      }
    }
  }

  // findBranches finds the branches without outputs (branch_no_outputs),
  // and the outputs that no connection leaves (port_unconnected).
  findBranches(): void {
    for (const n of this.d.nodes.values()) {
      if (n.type === "branch" && !this.outputsOf.has(n.id)) {
        this.add({
          code: "branch_no_outputs",
          entity: { kind: "node", id: n.id },
        });
      }
    }
    for (const o of this.d.outputs.keys()) {
      if (!this.fromOutput.has(o)) {
        this.add({
          code: "port_unconnected",
          entity: { kind: "branch_output", id: o },
        });
      }
    }
  }

  // findVariables finds the variables that a site reads but none writes
  // (var_read_never_written), and those that a site writes but none reads
  // (var_written_never_read), as their usages hold them (FR-DEP-01): a
  // script that updates a variable, as with +=, reads it too, and a text
  // that shows one reads it.
  findVariables(): void {
    const read = new Set<string>();
    const written = new Set<string>();
    for (const s of this.index.sites) {
      for (const u of s.uses) {
        if (u.kind === "variable") {
          (u.access === "write" ? written : read).add(u.target);
        }
      }
    }
    for (const v of this.d.variables.values()) {
      const self: Ref = { kind: "variable", id: v.id };
      if (read.has(v.id) && !written.has(v.id)) {
        this.add({
          code: "var_read_never_written",
          entity: self,
          args: { key: v.key },
        });
      } else if (written.has(v.id) && !read.has(v.id)) {
        this.add({
          code: "var_written_never_read",
          entity: self,
          args: { key: v.key },
        });
      }
    }
  }

  // findReachability finds the nodes that the flow cannot reach from any
  // board's entry, whatever the conditions (node_unreachable). The flow
  // starts at a board's entry node, or, on a board without one in the
  // draft, at any of the board's roots, and at the project's entry node; it
  // goes on from a jumper to its target, and from any other node but an
  // ending, where it ends, by every connection that leaves it (§5.8.5).
  findReachability(): void {
    const d = this.d;
    const reached = new Set<string>();
    const queue: string[] = [];
    const reach = (id: string | undefined) => {
      if (id !== undefined && d.nodes.has(id) && !reached.has(id)) {
        reached.add(id);
        queue.push(id);
      }
    };
    for (const b of d.boards.values()) {
      if (!this.entries.has(b.id)) {
        for (const root of this.roots.get(b.id) ?? []) {
          reach(root);
        }
      }
    }
    for (const e of this.entries.values()) {
      reach(e);
    }
    reach(d.entry.node);
    for (let id = queue.pop(); id !== undefined; id = queue.pop()) {
      const n = d.nodes.get(id);
      if (n?.type === "jumper") {
        reach(n.target?.node_id);
      } else if (n !== undefined && !n.is_ending) {
        for (const cn of this.from.get(id) ?? []) {
          reach(cn.to_node_id);
        }
      }
    }
    for (const n of d.nodes.values()) {
      if (!reached.has(n.id)) {
        this.add({
          code: "node_unreachable",
          entity: { kind: "node", id: n.id },
        });
      }
    }
  }

  // findElements finds the elements without text that no connection
  // leaves (node_empty), those that no connection leaves that are not
  // endings (dead_end), the lines of dialogue without a speaker
  // (speaker_missing), and the choices of an element that repeat an
  // earlier choice of it: the same label, and conditions that may both
  // hold (choice_duplicate_label). An ending's connections are no choices
  // (§5.8.5).
  findElements(): void {
    for (const n of this.d.nodes.values()) {
      if (n.type !== "element") {
        continue;
      }
      const self: Ref = { kind: "node", id: n.id };
      const out = this.choices.get(n.id) ?? [];
      if (
        out.length === 0 &&
        n.text !== undefined &&
        canonical(n.text) === emptyText
      ) {
        this.add({ code: "node_empty", entity: self });
      }
      if (out.length === 0 && !n.is_ending) {
        this.add({ code: "dead_end", entity: self });
      }
      if (
        (n.kind ?? "dialogue") === "dialogue" &&
        n.speaker_component_id === undefined
      ) {
        this.add({ code: "speaker_missing", entity: self });
      }
      if (n.is_ending) {
        continue;
      }
      // One prover for the element's choices, so that however many share
      // a label, the element costs a bounded time.
      const p = new Prover();
      for (const same of sameLabel(out)) {
        for (let j = 1; j < same.length; j++) {
          for (let i = 0; i < j; i++) {
            if (this.overlap(p, same[i].id, same[j].id)) {
              this.add({
                code: "choice_duplicate_label",
                entity: { kind: "connection", id: same[j].id },
                target: { kind: "connection", id: same[i].id },
              });
              break;
            }
          }
        }
      }
    }
  }

  // overlap reports whether the conditions of the connections a and b may
  // both hold: unless one of them never holds, or p proves that they never
  // hold together. A connection without a condition always may, and one
  // whose condition does not compile, as far as the validation knows, may
  // too. The conditions are read as the server stores them, its canonical
  // AST, as its validation reads them.
  private overlap(p: Prover, a: string, b: string): boolean {
    const ca = this.conditions.get(a);
    const cb = this.conditions.get(b);
    if ((ca !== undefined && !ca.valid) || (cb !== undefined && !cb.valid)) {
      return true;
    }
    return !p.exclusive(this.canonical(a), this.canonical(b));
  }

  // canonical returns the condition of the connection id as the server
  // stores it, its canonical AST, which the server's validation decodes, or
  // undefined for none.
  private canonical(id: string): ExprNode | undefined {
    if (!this.canonicals.has(id)) {
      const ast = this.conditions.get(id)?.ast;
      this.canonicals.set(
        id,
        ast === undefined ? undefined : decode(encode(ast), "condition"),
      );
    }
    return this.canonicals.get(id);
  }

  // findConnections finds the connections that enter the node that they
  // leave (connection_self), and those that leave a port for a node that
  // an earlier connection of the port enters (connection_parallel), each
  // found once, naming the first earlier one.
  findConnections(): void {
    const first = new Map<string, string>();
    for (const cn of this.d.connections) {
      const self: Ref = { kind: "connection", id: cn.id };
      if (cn.from_node_id === cn.to_node_id) {
        this.add({ code: "connection_self", entity: self });
      }
      const key = `${cn.from_node_id} ${cn.from_port} ${cn.to_node_id}`;
      const earlier = first.get(key);
      if (earlier === undefined) {
        first.set(key, cn.id);
      } else {
        this.add({
          code: "connection_parallel",
          entity: self,
          target: { kind: "connection", id: earlier },
        });
      }
    }
  }

  // findBoards finds the boards without an entry node in the draft that
  // have more than one root (board_entry_missing).
  findBoards(): void {
    for (const b of this.d.boards.values()) {
      const roots = this.roots.get(b.id)?.length ?? 0;
      if (!this.entries.has(b.id) && roots > 1) {
        this.add({
          code: "board_entry_missing",
          entity: { kind: "board", id: b.id },
          args: { roots },
        });
      }
    }
  }
}

// sameLabel returns the choices of one node that share their label with
// another of them, the same document, grouped by their labels in the order
// of their first choices, each group in the choices' order.
function sameLabel<T extends { label: unknown }>(choices: readonly T[]): T[][] {
  const groups = new Map<string, T[]>();
  for (const c of choices) {
    const key = canonical(c.label);
    const g = groups.get(key);
    if (g === undefined) {
      groups.set(key, [c]);
    } else {
      g.push(c);
    }
  }
  return [...groups.values()].filter((g) => g.length > 1);
}

/**
 * Returns the JSON of v with each object's keys sorted, so that two
 * documents that are the same, as PostgreSQL's jsonb compares them, are
 * the same string.
 */
export function canonical(v: unknown): string {
  return JSON.stringify(sortKeys(v));
}

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) {
    return v.map(sortKeys);
  }
  if (typeof v === "object" && v !== null) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v).sort()) {
      const x = (v as Record<string, unknown>)[k];
      if (x !== undefined) {
        out[k] = sortKeys(x);
      }
    }
    return out;
  }
  return v;
}

// push appends v to the list of key in m.
function push<V>(m: Map<string, V[]>, key: string, v: V): void {
  const list = m.get(key);
  if (list === undefined) {
    m.set(key, [v]);
  } else {
    list.push(v);
  }
}
