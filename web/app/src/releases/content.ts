// What a release holds, as its read-only view shows it (PRD FR-SNAP-02 AC2,
// plan note N88), read from the files of the published format that the
// release's zip holds: its boards, each in the order of the script view
// (FR-TEXT-04 AC2), breadth-first from the entry node along the connections
// in the order of each node's ports and choices, and then the nodes that it
// does not reach, its variables, and its components. It is of the files
// alone, and of nothing of the project's document, so that what it shows is
// the release as it was published, whatever the draft has become.

import type {
  Board,
  Component,
  Components,
  ComponentType,
  ComponentTypes,
  Connection,
  Manifest,
  Node,
  Variable,
  Variables,
} from "@storycreep/shared/published";

/** A board of a release, with its nodes in the order that its view lists them. */
export interface ReleaseBoard {
  id: string;
  name: string;
  entryNodeId?: string;
  /** The nodes that the entry node reaches, itself first, breadth-first. */
  reached: Node[];
  /** The others, by ID, since a release's files hold no time of creation: an ID begins with the time on the clock of whoever made it, so this can differ from the script view's order where clocks differ. */
  unreached: Node[];
  /** The connections that leave each node, by its ID, in the order that the flow offers them. */
  leaving: ReadonlyMap<string, readonly Connection[]>;
}

/** What a release holds. */
export interface ReleaseContent {
  boards: ReleaseBoard[];
  variables: Variable[];
  componentTypes: ComponentType[];
  components: Component[];
  /** Each node of the release, with its board, by its ID, as a jumper's target names one. */
  nodes: ReadonlyMap<string, { node: Node; board: ReleaseBoard }>;
}

/** Returns the ports of node, in their order: an element's one, or a branch's outputs. */
function portsOf(node: Node): string[] {
  switch (node.type) {
    case "element":
      return ["out"];
    case "branch":
      return [...node.outputs]
        .sort((a, b) => a.order - b.order)
        .map((o) => o.id);
    default:
      return [];
  }
}

/** Returns connections, which leave node, in the order of its ports, and of each port's choices. */
function inFlowOrder(node: Node, connections: Connection[]): Connection[] {
  return portsOf(node).flatMap((port) =>
    connections
      .filter((c) => c.fromPort === port)
      .sort((a, b) => a.order - b.order),
  );
}

/** Returns the board of file, its nodes ordered from its entry node. */
function boardOf(file: Board): ReleaseBoard {
  const byId = new Map(file.nodes.map((n) => [n.id, n]));
  const from = new Map<string, Connection[]>();
  for (const c of file.connections) {
    const list = from.get(c.from);
    if (list === undefined) {
      from.set(c.from, [c]);
    } else {
      list.push(c);
    }
  }
  const leaving = new Map<string, Connection[]>();
  for (const [id, list] of from) {
    const node = byId.get(id);
    if (node !== undefined) {
      leaving.set(id, inFlowOrder(node, list));
    }
  }
  const { entryNodeId } = file.board;
  const entry = entryNodeId === undefined ? undefined : byId.get(entryNodeId);
  const reached: Node[] = entry === undefined ? [] : [entry];
  const seen = new Set(reached.map((n) => n.id));
  // The list is the queue: each node is read once, in the order reached.
  for (let i = 0; i < reached.length; i++) {
    for (const c of leaving.get(reached[i].id) ?? []) {
      const next = byId.get(c.to);
      if (next !== undefined && !seen.has(next.id)) {
        seen.add(next.id);
        reached.push(next);
      }
    }
  }
  return {
    id: file.board.id,
    name: file.board.name,
    ...(entryNodeId === undefined ? {} : { entryNodeId }),
    reached,
    unreached: file.nodes.filter((n) => !seen.has(n.id)),
    leaving,
  };
}

/** Returns what the release of files holds, which are those of a release's zip, by their paths. */
export function readRelease(
  files: ReadonlyMap<string, Uint8Array>,
): ReleaseContent {
  const decoder = new TextDecoder();
  const read = <T>(path: string): T => {
    const bytes = files.get(path);
    if (bytes === undefined) {
      throw new Error(`release: there is no ${path}`);
    }
    return JSON.parse(decoder.decode(bytes)) as T;
  };
  const manifest = read<Manifest>("storycreep.json");
  const boards = manifest.boards.map((b) => boardOf(read<Board>(b.file)));
  const nodes = new Map<string, { node: Node; board: ReleaseBoard }>();
  for (const board of boards) {
    for (const node of [...board.reached, ...board.unreached]) {
      nodes.set(node.id, { node, board });
    }
  }
  return {
    boards,
    variables: read<Variables>("project/variables.json").variables,
    componentTypes: read<ComponentTypes>("project/component_types.json")
      .componentTypes,
    components: read<Components>("project/components.json").components,
    nodes,
  };
}
