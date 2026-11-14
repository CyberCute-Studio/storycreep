// The layout of a project's dependency graph (PRD FR-DEP-02, plan note N77):
// its boards in layers, from the top, each board below the boards that it
// depends on, and its edges between them. A graph may have cycles, which it
// lays out as any other graph, and which are no error (AC4): the layering
// takes the edges that close a cycle, which a depth-first search from the
// boards in their order finds, as if they went the other way, so that every
// other edge goes down a layer or more, and each that closes a cycle goes up,
// along the right of the boards that it joins. Within a layer, the boards
// are ordered by the places of the boards that they are joined to, a few
// times down and up, as a layered layout's barycentre heuristic does, which
// starts from the boards' own order, so that the same graph is always laid
// out alike. A layer wider than a row's boards wraps into rows, and the
// boards that no edge joins follow the layers, in rows of their own. It is
// a pure function of its boards, its edges, and its sizes, which the view
// lays out again as the window's width changes.

/** An edge of the layout: the board that it leaves and the one that it enters. */
export interface LayoutEdge {
  from: string;
  to: string;
}

/** What the layout is made of: a board's box, the gaps between boxes, and how many boxes a row holds at most. */
export interface LayoutSizes {
  width: number;
  height: number;
  /** The gap between two boxes of a row. */
  gap: number;
  /** The gap between two rows. */
  rowGap: number;
  /** How many boxes a row holds at most, at least 1. */
  perRow: number;
  /** The margin around the layout, which edges that go up curve into on the right. */
  margin: number;
}

/** A board's box: its top left corner, and its layer, or -1 for a board that no edge joins. */
export interface LayoutBox {
  id: string;
  x: number;
  y: number;
  layer: number;
}

/** An edge's route: an SVG path's data from the board that it leaves to the one that it enters, and whether it goes up, as one that closes a cycle. */
export interface LayoutRoute {
  from: string;
  to: string;
  path: string;
  up: boolean;
}

/** A laid out graph: its boxes, in the order of the boards, its routes, in the order of the edges, and its size. */
export interface Layout {
  boxes: LayoutBox[];
  routes: LayoutRoute[];
  width: number;
  height: number;
}

/** How many times the order of the layers is swept down and up. */
const sweeps = 4;

/**
 * Lays out the graph of boards, in their order, which the layout starts
 * from, and of edges, of which an edge from a board to itself, one of a
 * board that boards lacks, and one that another before it repeats, are
 * left out.
 */
export function layoutGraph(
  boards: readonly string[],
  edges: readonly LayoutEdge[],
  sizes: LayoutSizes,
): Layout {
  const index = new Map(boards.map((id, i) => [id, i]));
  const pairs: [number, number][] = [];
  const seen = new Set<string>();
  for (const { from, to } of edges) {
    const u = index.get(from);
    const v = index.get(to);
    const key = `${u}:${v}`;
    if (u === undefined || v === undefined || u === v || seen.has(key)) {
      continue;
    }
    seen.add(key);
    pairs.push([u, v]);
  }
  const n = boards.length;
  const out: number[][] = Array.from({ length: n }, () => []);
  for (const [u, v] of pairs) {
    out[u].push(v);
  }
  for (const targets of out) {
    targets.sort((a, b) => a - b);
  }
  const up = closingEdges(out);
  // The edges that the layering reads: each edge, but those that close a
  // cycle the other way.
  const preds: number[][] = Array.from({ length: n }, () => []);
  const succs: number[][] = Array.from({ length: n }, () => []);
  const added = new Set<string>();
  for (const [u, v] of pairs) {
    const [a, b] = up.has(`${u}:${v}`) ? [v, u] : [u, v];
    if (!added.has(`${a}:${b}`)) {
      added.add(`${a}:${b}`);
      succs[a].push(b);
      preds[b].push(a);
    }
  }
  const joined = new Array<boolean>(n).fill(false);
  for (const [u, v] of pairs) {
    joined[u] = true;
    joined[v] = true;
  }
  const layerOf = longestPath(succs, preds, joined);
  const layers: number[][] = [];
  for (let i = 0; i < n; i++) {
    if (joined[i]) {
      const layer = layers[layerOf[i]];
      if (layer === undefined) {
        layers[layerOf[i]] = [i];
      } else {
        layer.push(i);
      }
    }
  }
  order(layers, preds, succs);

  // The rows: each layer's, wrapped, and then the boards that no edge joins.
  const perRow = Math.max(1, Math.floor(sizes.perRow));
  const rows: number[][] = [];
  for (const layer of layers) {
    for (let i = 0; i < layer.length; i += perRow) {
      rows.push(layer.slice(i, i + perRow));
    }
  }
  const alone = boards.map((_, i) => i).filter((i) => !joined[i]);
  const graphRows = rows.length;
  for (let i = 0; i < alone.length; i += perRow) {
    rows.push(alone.slice(i, i + perRow));
  }
  const widest = Math.max(0, ...rows.map((r) => r.length));
  const content = widest * sizes.width + Math.max(0, widest - 1) * sizes.gap;
  const at = new Array<{ x: number; y: number }>(n);
  let y = sizes.margin;
  rows.forEach((row, r) => {
    // A row of the boards that no edge joins is set apart from the layers.
    if (r === graphRows && r > 0) {
      y += sizes.rowGap;
    }
    const rowWidth = row.length * sizes.width + (row.length - 1) * sizes.gap;
    const x0 = sizes.margin + (content - rowWidth) / 2;
    row.forEach((b, i) => {
      at[b] = { x: x0 + i * (sizes.width + sizes.gap), y };
    });
    y += sizes.height + sizes.rowGap;
  });
  const height = rows.length === 0 ? 0 : y - sizes.rowGap + sizes.margin;
  const boxes: LayoutBox[] = boards.map((id, i) => ({
    id,
    x: at[i].x,
    y: at[i].y,
    layer: joined[i] ? layerOf[i] : -1,
  }));
  const routes = route(boards, pairs, up, at, sizes);
  return {
    boxes,
    routes,
    width: n === 0 ? 0 : content + 2 * sizes.margin,
    height,
  };
}

/**
 * Returns the edges of out, as `u:v`, that close a cycle: those that a
 * depth-first search from each board in its order, along the edges in the
 * order of their targets, finds entering a board that it is still within,
 * without which the rest of the graph has no cycle.
 */
function closingEdges(out: readonly number[][]): Set<string> {
  const n = out.length;
  const state = new Uint8Array(n); // 0 unseen, 1 within, 2 done
  const closing = new Set<string>();
  for (let root = 0; root < n; root++) {
    if (state[root] !== 0) {
      continue;
    }
    // An explicit stack of each board and the next of its edges, so that a
    // long chain of boards does not exhaust the call stack.
    const stack: [number, number][] = [[root, 0]];
    state[root] = 1;
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      const [u, next] = top;
      if (next >= out[u].length) {
        state[u] = 2;
        stack.pop();
        continue;
      }
      top[1] = next + 1;
      const v = out[u][next];
      if (state[v] === 1) {
        closing.add(`${u}:${v}`);
      } else if (state[v] === 0) {
        state[v] = 1;
        stack.push([v, 0]);
      }
    }
  }
  return closing;
}

/**
 * Returns the layer of each board that joined holds, of the graph of succs
 * and preds, which has no cycle: 0 for a board that no edge enters, and
 * otherwise one more than the deepest board that an edge leaves for it.
 */
function longestPath(
  succs: readonly number[][],
  preds: readonly number[][],
  joined: readonly boolean[],
): number[] {
  const n = succs.length;
  const layer = new Array<number>(n).fill(0);
  const waiting = preds.map((p) => p.length);
  const queue: number[] = [];
  for (let i = 0; i < n; i++) {
    if (joined[i] && waiting[i] === 0) {
      queue.push(i);
    }
  }
  for (let head = 0; head < queue.length; head++) {
    const u = queue[head];
    for (const v of succs[u]) {
      layer[v] = Math.max(layer[v], layer[u] + 1);
      waiting[v]--;
      if (waiting[v] === 0) {
        queue.push(v);
      }
    }
  }
  return layer;
}

/**
 * Orders the boards of each layer by the mean place of the boards that they
 * are joined to in the layers above, sweeping down, and then in the layers
 * below, sweeping up, a few times; a board joined to none keeps its place,
 * and boards of the same mean keep their order.
 */
function order(
  layers: number[][],
  preds: readonly number[][],
  succs: readonly number[][],
): void {
  const place = new Map<number, number>();
  const mark = () => {
    for (const layer of layers) {
      layer.forEach((b, i) => {
        place.set(b, layer.length === 1 ? 0.5 : i / (layer.length - 1));
      });
    }
  };
  const sortBy = (layer: number[], near: readonly number[][]) => {
    const mean = new Map<number, number>();
    layer.forEach((b, i) => {
      const of = near[b];
      mean.set(
        b,
        of.length === 0
          ? (place.get(b) ?? i)
          : of.reduce((sum, o) => sum + (place.get(o) ?? 0), 0) / of.length,
      );
    });
    const before = new Map(layer.map((b, i) => [b, i]));
    layer.sort(
      (a, b) =>
        (mean.get(a) ?? 0) - (mean.get(b) ?? 0) ||
        (before.get(a) ?? 0) - (before.get(b) ?? 0),
    );
  };
  mark();
  for (let s = 0; s < sweeps; s++) {
    for (let l = 1; l < layers.length; l++) {
      sortBy(layers[l], preds);
      mark();
    }
    for (let l = layers.length - 2; l >= 0; l--) {
      sortBy(layers[l], succs);
      mark();
    }
  }
}

/**
 * Returns the route of each edge of pairs: one that goes down leaves the
 * bottom of its board's box and enters the top of the other's, and one that
 * goes up (up) leaves the right of its box and enters the right of the
 * other's, curving out to the right, so that it is not drawn over an edge
 * that goes down between the same boards. The edges that leave or enter a
 * side of a box do so at points spread along it, in the order of the boxes
 * at their other ends, so that they are told apart.
 */
function route(
  boards: readonly string[],
  pairs: readonly [number, number][],
  up: ReadonlySet<string>,
  at: readonly { x: number; y: number }[],
  sizes: LayoutSizes,
): LayoutRoute[] {
  const { width: w, height: h } = sizes;
  const goesUp = (u: number, v: number) => up.has(`${u}:${v}`);
  /** The edges at each side of each box, by `${box}:${side}`, as indexes of pairs. */
  const sides = new Map<string, number[]>();
  const sideOf = (box: number, side: string, edge: number) => {
    const key = `${box}:${side}`;
    let list = sides.get(key);
    if (list === undefined) {
      list = [];
      sides.set(key, list);
    }
    list.push(edge);
  };
  pairs.forEach(([u, v], e) => {
    if (goesUp(u, v)) {
      sideOf(u, "right", e);
      sideOf(v, "right", e);
    } else {
      sideOf(u, "bottom", e);
      sideOf(v, "top", e);
    }
  });
  /** The place of edge e among those of its side of box, as a share of the side, from 0 to 1. */
  const share = new Map<string, number>();
  for (const [key, list] of sides) {
    const [boxText, side] = key.split(":");
    const box = Number(boxText);
    const other = (e: number) =>
      pairs[e][0] === box ? pairs[e][1] : pairs[e][0];
    list.sort((a, b) => {
      const pa = at[other(a)];
      const pb = at[other(b)];
      return side === "right"
        ? pa.y - pb.y || pa.x - pb.x || a - b
        : pa.x - pb.x || pa.y - pb.y || a - b;
    });
    list.forEach((e, i) => {
      share.set(`${e}:${box}`, (i + 1) / (list.length + 1));
    });
  }
  const round = (v: number) => Math.round(v * 10) / 10;
  return pairs.map(([u, v], e) => {
    const s = at[u];
    const t = at[v];
    const of = (box: number) => share.get(`${e}:${box}`) ?? 0.5;
    if (goesUp(u, v)) {
      const sx = s.x + w;
      const sy = s.y + h * of(u);
      const tx = t.x + w;
      const ty = t.y + h * of(v);
      const bulge = Math.min(
        sizes.margin * 2,
        Math.max(sizes.gap, Math.abs(sy - ty) / 4),
      );
      return {
        from: boards[u],
        to: boards[v],
        up: true,
        path: `M ${round(sx)} ${round(sy)} C ${round(sx + bulge)} ${round(sy)}, ${round(tx + bulge)} ${round(ty)}, ${round(tx)} ${round(ty)}`,
      };
    }
    const sx = s.x + w * of(u);
    const sy = s.y + h;
    const tx = t.x + w * of(v);
    const ty = t.y;
    const mid = (ty - sy) / 2;
    return {
      from: boards[u],
      to: boards[v],
      up: false,
      path: `M ${round(sx)} ${round(sy)} C ${round(sx)} ${round(sy + mid)}, ${round(tx)} ${round(ty - mid)}, ${round(tx)} ${round(ty)}`,
    };
  });
}
