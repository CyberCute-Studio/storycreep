// What the canvas's tests build boards of: nodes and connections as the API
// answers them, and a document that holds them.

import type { Connection, Node } from "@storycreep/shared/api";
import { type Doc, emptyDoc } from "../document/doc";

const at = "2026-01-01T00:00:00Z";

/** Returns an element of board at x, y, with what extra gives it. */
export function testNode(
  id: string,
  x: number,
  y: number,
  extra: Partial<Node> = {},
): Node {
  return {
    id,
    board_id: "bd_a",
    project_id: "prj_test",
    type: "element",
    kind: "narration",
    title: id,
    color: "none",
    tags: [],
    is_ending: false,
    position: { x, y },
    text: { type: "doc", content: [{ type: "paragraph" }] },
    rev: 1,
    created_at: at,
    updated_at: at,
    ...extra,
  };
}

/** Returns a connection of board from the out port of from to to. */
export function testConnection(
  id: string,
  from: string,
  to: string,
  extra: Partial<Connection> = {},
): Connection {
  return {
    id,
    board_id: "bd_a",
    project_id: "prj_test",
    from_node_id: from,
    from_port: "out",
    to_node_id: to,
    order: "V",
    label: { type: "doc", content: [{ type: "paragraph" }] },
    rev: 1,
    created_at: at,
    updated_at: at,
    ...extra,
  };
}

/** Returns a document that holds nodes and connections. */
export function docOf(nodes: Node[], connections: Connection[] = []): Doc {
  return {
    ...emptyDoc(),
    nodes: new Map(nodes.map((n) => [n.id, n])),
    connections: new Map(connections.map((c) => [c.id, c])),
  };
}

/** Returns nodes in a grid of cols columns, 320 by 200 units apart, as the fixtures lay them out. */
export function gridNodes(count: number, cols: number, board = "bd_a"): Node[] {
  return Array.from({ length: count }, (_, i) =>
    testNode(`nd_${board}_${i}`, (i % cols) * 320, Math.floor(i / cols) * 200, {
      board_id: board,
    }),
  );
}
