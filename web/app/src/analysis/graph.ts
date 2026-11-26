// The client-side dependency graph (plan item 89, note N96): the
// TypeScript twin of the server's graph of the dependencies between a
// project's boards (package dependency's Graph and Contributions, PRD
// FR-DEP-02, note N56), of a draft (draft.ts) and its index (uses.ts), as
// the API answers GET …/dependencies and the list of an edge's
// contributions.

import type { Contribution, Edge, End, Graph } from "@storycreep/shared/api";
import { compareIds, type Draft } from "./draft";
import type { Index, Site, Use } from "./uses";

/** The kind of an edge, which says what its contributions are. */
export type EdgeKind = Edge["kind"];

/** The kinds of edge, in the order of a graph's edges between the same two boards. */
export const edgeKinds: readonly EdgeKind[] = [
  "variable",
  "attribute",
  "jumper",
  "visit",
];

/**
 * What keeps some of a graph's contributions, and so of its edges, as
 * FR-DEP-02's filters do: each field that is set keeps the contributions
 * that it names, and a contribution is kept only if every one of them
 * keeps it, so that two fields that name nothing in common keep nothing.
 */
export interface GraphFilter {
  /** Keeps the edges of the kind. */
  readonly kind?: EdgeKind;
  /** Keeps the edges that leave the board or enter it. */
  readonly board?: string;
  /** Keeps the contributions of the variable, which only edges of the kind variable have. */
  readonly variable?: string;
  /** Keeps the contributions of the component's attributes, which only edges of the kind attribute have. */
  readonly component?: string;
  /** Keeps the contributions of the attributes of the definition, of any component, which only edges of the kind attribute have. */
  readonly attribute?: string;
}

// keeps returns the kinds of edge that f keeps.
function keeps(f: GraphFilter): Set<EdgeKind> {
  return new Set(
    edgeKinds.filter(
      (k) =>
        (f.kind === undefined || f.kind === k) &&
        (f.variable === undefined || k === "variable") &&
        ((f.component === undefined && f.attribute === undefined) ||
          k === "attribute"),
    ),
  );
}

// keepsUse reports whether f keeps the contributions of the use u, a
// variable's or an attribute's, of an edge of a kind that it keeps.
function keepsUse(f: GraphFilter, u: Use): boolean {
  return u.kind === "variable"
    ? f.variable === undefined || f.variable === u.target
    : (f.component === undefined || f.component === u.target) &&
        (f.attribute === undefined || f.attribute === u.attribute);
}

// valueKind returns the kind of edge of a use of a variable or an
// attribute, or undefined for another.
function valueKind(u: Use): "variable" | "attribute" | undefined {
  return u.kind === "variable" || u.kind === "attribute" ? u.kind : undefined;
}

// targetOf returns what tells the target of a use of a variable or an
// attribute apart from the others: its kind, its target, and its
// attribute.
function targetOf(u: Use): string {
  return `${u.kind} ${u.target} ${u.attribute ?? ""}`;
}

/**
 * Returns the graph of the dependencies between the draft's boards that
 * the filter keeps (FR-DEP-02), as the server's GET …/dependencies answers
 * it: every board, in the order of their IDs, with the counts of its badge,
 * and the edges, in the order of the boards that they leave and enter, and
 * of their kinds, each with how many contributions it has. No edge joins a
 * board to itself.
 */
export function graphOf(
  draft: Draft,
  index: Index,
  filter: GraphFilter = {},
): Graph {
  const kinds = keeps(filter);
  const counts = new Map<string, Edge>();
  const add = (from: string, to: string, kind: EdgeKind, n: number) => {
    if (
      from === to ||
      (filter.board !== undefined &&
        filter.board !== from &&
        filter.board !== to)
    ) {
      return;
    }
    const key = `${from} ${to} ${kind}`;
    const e = counts.get(key);
    if (e === undefined) {
      counts.set(key, {
        from_board_id: from,
        to_board_id: to,
        kind,
        contributions: n,
      });
    } else {
      e.contributions += n;
    }
  };
  // The sites of each board that write, and that read, each variable and
  // each component's attribute that the filter keeps, counted by board.
  const writes = new Map<string, Map<string, number>>();
  const reads = new Map<string, Map<string, number>>();
  const kindOf = new Map<string, "variable" | "attribute">();
  for (const s of index.sites) {
    if (s.board === undefined) {
      continue;
    }
    for (const u of s.uses) {
      if (u.kind === "node") {
        const visited = draft.nodes.get(u.target);
        if (kinds.has("visit") && visited !== undefined) {
          add(visited.board_id, s.board, "visit", 1);
        }
        continue;
      }
      const kind = valueKind(u);
      if (kind === undefined || !kinds.has(kind) || !keepsUse(filter, u)) {
        continue;
      }
      const t = targetOf(u);
      kindOf.set(t, kind);
      const of = u.access === "write" ? writes : reads;
      const boards = of.get(t) ?? new Map<string, number>();
      boards.set(s.board, (boards.get(s.board) ?? 0) + 1);
      of.set(t, boards);
    }
  }
  for (const [t, ws] of writes) {
    const kind = kindOf.get(t) ?? "variable";
    for (const [w, wn] of ws) {
      for (const [r, rn] of reads.get(t) ?? []) {
        add(w, r, kind, wn * rn);
      }
    }
  }
  if (kinds.has("jumper")) {
    for (const j of jumpersOf(draft)) {
      add(j.jumper.board_id, j.target.board_id, "jumper", 1);
    }
  }

  const edges = [...counts.values()].sort(
    (a, b) =>
      compareIds(a.from_board_id, b.from_board_id) ||
      compareIds(a.to_board_id, b.to_board_id) ||
      edgeKinds.indexOf(a.kind) - edgeKinds.indexOf(b.kind),
  );
  const linked = new Set<string>();
  const inbound = new Map<string, number>();
  const outbound = new Map<string, number>();
  for (const e of edges) {
    const pair = `${e.from_board_id} ${e.to_board_id}`;
    if (!linked.has(pair)) {
      linked.add(pair);
      outbound.set(e.from_board_id, (outbound.get(e.from_board_id) ?? 0) + 1);
      inbound.set(e.to_board_id, (inbound.get(e.to_board_id) ?? 0) + 1);
    }
  }
  return {
    seq: draft.seq,
    boards: [...draft.boards.values()].map((b) => ({
      id: b.id,
      name: b.name,
      inbound: inbound.get(b.id) ?? 0,
      outbound: outbound.get(b.id) ?? 0,
    })),
    edges,
  };
}

// jumpersOf returns the draft's jumpers whose targets are nodes of the
// draft, each with its target.
function jumpersOf(draft: Draft) {
  const out = [];
  for (const jumper of draft.nodes.values()) {
    const id = jumper.target?.node_id;
    const target = id === undefined ? undefined : draft.nodes.get(id);
    if (jumper.type === "jumper" && target !== undefined) {
      out.push({ jumper, target });
    }
  }
  return out;
}

/**
 * Returns the contributions to the edge of the kind from the board from to
 * the board to that the filter keeps (FR-DEP-02 AC2), as the server lists
 * them, in the order of contributionOrder: of a variable or an attribute,
 * each triple of it, a site of from that writes it, and a site of to that
 * reads it; of a jumper, each jumper of from whose target is a node of to;
 * and of a visit, each pair of a node of from and an expression of to that
 * reads whether it was visited. A list of a board to itself is empty, and
 * undefined is a board that the draft does not hold. The filter's board
 * plays no part in it.
 */
export function contributionsOf(
  draft: Draft,
  index: Index,
  from: string,
  to: string,
  kind: EdgeKind,
  filter: Omit<GraphFilter, "kind" | "board"> = {},
): Contribution[] | undefined {
  if (!draft.boards.has(from) || !draft.boards.has(to)) {
    return undefined;
  }
  if (from === to || !keeps({ ...filter, kind }).has(kind)) {
    return [];
  }
  const out: Contribution[] = [];
  const node = (id: string): End => ({
    site: "node",
    node_id: id,
    node_title: draft.nodes.get(id)?.title ?? "",
  });
  if (kind === "jumper") {
    for (const j of jumpersOf(draft)) {
      if (j.jumper.board_id === from && j.target.board_id === to) {
        out.push({ from: node(j.jumper.id), to: node(j.target.id) });
      }
    }
  } else if (kind === "visit") {
    for (const s of index.sites) {
      if (s.board !== to || isText(s)) {
        continue;
      }
      for (const u of s.uses) {
        if (u.kind === "node" && draft.nodes.get(u.target)?.board_id === from) {
          out.push({ from: node(u.target), to: endOf(draft, s) });
        }
      }
    }
  } else {
    // The sites of from that write each target, and of to that read it.
    const writers = new Map<string, { site: Site; use: Use }[]>();
    for (const s of index.sites) {
      if (s.board !== from) {
        continue;
      }
      for (const u of s.uses) {
        if (u.kind === kind && u.access === "write" && keepsUse(filter, u)) {
          const list = writers.get(targetOf(u)) ?? [];
          list.push({ site: s, use: u });
          writers.set(targetOf(u), list);
        }
      }
    }
    for (const s of index.sites) {
      if (s.board !== to) {
        continue;
      }
      for (const u of s.uses) {
        if (u.kind !== kind || u.access !== "read") {
          continue;
        }
        for (const w of writers.get(targetOf(u)) ?? []) {
          out.push({
            ...(kind === "variable"
              ? { variable_id: u.target }
              : { component_id: u.target, attribute_id: u.attribute }),
            from: endOf(draft, w.site),
            to: endOf(draft, s),
          });
        }
      }
    }
  }
  return out.sort(contributionOrder);
}

// isText reports whether the site s is a text, which names no node.
function isText(s: Site): boolean {
  return (
    s.type === "node_text" ||
    s.type === "connection_label" ||
    s.type === "component_description"
  );
}

// endOf returns the end of a contribution that is the site s, of a board,
// with its node, as a usage's site is.
function endOf(draft: Draft, s: Site): End {
  const node = s.node ?? "";
  const end: End = {
    site: s.type as End["site"],
    node_id: node,
    node_title: draft.nodes.get(node)?.title ?? "",
  };
  switch (s.type) {
    case "connection_condition":
    case "connection_on_traverse":
    case "connection_label":
      end.connection_id = s.owner;
      break;
    case "branch_output_condition":
      end.branch_output_id = s.owner;
      break;
  }
  return end;
}

/** The sites of a contribution's end, in the order of their enum. */
const endSites: readonly End["site"][] = [
  "node",
  "node_on_enter",
  "node_on_exit",
  "node_text",
  "connection_condition",
  "connection_on_traverse",
  "connection_label",
  "branch_output_condition",
];

// compareEnds orders the ends of contributions by their nodes, their
// sites' types, and the connections or the outputs of their sites.
function compareEnds(a: End, b: End): number {
  return (
    compareIds(a.node_id, b.node_id) ||
    endSites.indexOf(a.site) - endSites.indexOf(b.site) ||
    compareIds(
      a.connection_id ?? a.branch_output_id,
      b.connection_id ?? b.branch_output_id,
    )
  );
}

/**
 * Orders contributions by their targets, the variables, the components, or
 * the nodes visited, their attributes' definitions, and their ends, each by
 * its node, its site's type, and its site's connection or output. The
 * server orders the sites of one node by their UUIDs, an expression's its
 * own, which no client holds, and a text's its owner's, so its order of two
 * sites of one node may differ; the contributions are the same.
 */
export function contributionOrder(a: Contribution, b: Contribution): number {
  const target = (c: Contribution) =>
    c.variable_id ??
    c.component_id ??
    (c.from.site === "node" ? c.from.node_id : undefined);
  return (
    compareIds(target(a), target(b)) ||
    compareIds(a.attribute_id, b.attribute_id) ||
    compareEnds(a.from, b.from) ||
    compareEnds(a.to, b.to)
  );
}
