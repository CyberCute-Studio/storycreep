// A spatial index of a board's content (PRD FR-EDIT-11): a grid of square
// cells, each with the entries whose rectangles overlap it, so that finding
// what a rectangle of the board shows takes the time of the cells that it
// covers and of what they hold, and not of the board's whole content.

import { intersects, type Rect } from "./viewport";

/** The side of a cell, in units of the board: a few nodes across. */
const cellSize = 1024;

/**
 * The most cells that an entry is listed in: one larger, as a connection
 * across a whole board, is kept apart, in a list that every query reads, so
 * that its cells do not cost more than the board's other entries.
 */
const maxCells = 256;

/** Returns the key of the cell at column cx and row cy. */
function cellKey(cx: number, cy: number): number {
  // Cantor's pairing of the column and the row, each made positive, is
  // unique for the ±1,000,000,000 of a board's extent in cells of 1024,
  // well within a double's integers.
  const a = cx >= 0 ? 2 * cx : -2 * cx - 1;
  const b = cy >= 0 ? 2 * cy : -2 * cy - 1;
  return ((a + b) * (a + b + 1)) / 2 + b;
}

/** The cells that a rectangle covers, from its first column and row to its last. */
interface Span {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

function spanOf(r: Rect): Span {
  return {
    x0: Math.floor(r.x / cellSize),
    y0: Math.floor(r.y / cellSize),
    x1: Math.floor((r.x + r.width) / cellSize),
    y1: Math.floor((r.y + r.height) / cellSize),
  };
}

function cellsOf(s: Span): number {
  return (s.x1 - s.x0 + 1) * (s.y1 - s.y0 + 1);
}

/** A spatial index of entries of type T, each by its ID. */
export class GridIndex<T> {
  private readonly cells = new Map<number, Set<string>>();
  private readonly entries = new Map<
    string,
    { rect: Rect; value: T; span: Span | undefined }
  >();
  /** The entries too large for the grid, which every query reads. */
  private readonly large = new Set<string>();

  /** How many entries the index holds. */
  get size(): number {
    return this.entries.size;
  }

  /** Puts value, of id, at rect, in place of what id had. */
  set(id: string, rect: Rect, value: T): void {
    const had = this.entries.get(id);
    const span = spanOf(rect);
    const inGrid = cellsOf(span) <= maxCells ? span : undefined;
    if (had !== undefined) {
      if (
        had.span !== undefined &&
        inGrid !== undefined &&
        had.span.x0 === inGrid.x0 &&
        had.span.y0 === inGrid.y0 &&
        had.span.x1 === inGrid.x1 &&
        had.span.y1 === inGrid.y1
      ) {
        had.rect = rect;
        had.value = value;
        return;
      }
      this.unlist(id, had.span);
    }
    this.entries.set(id, { rect, value, span: inGrid });
    if (inGrid === undefined) {
      this.large.add(id);
      return;
    }
    for (let cx = inGrid.x0; cx <= inGrid.x1; cx++) {
      for (let cy = inGrid.y0; cy <= inGrid.y1; cy++) {
        const key = cellKey(cx, cy);
        let cell = this.cells.get(key);
        if (cell === undefined) {
          cell = new Set();
          this.cells.set(key, cell);
        }
        cell.add(id);
      }
    }
  }

  /** Takes id out of the index. */
  delete(id: string): void {
    const had = this.entries.get(id);
    if (had === undefined) {
      return;
    }
    this.unlist(id, had.span);
    this.entries.delete(id);
  }

  private unlist(id: string, span: Span | undefined): void {
    if (span === undefined) {
      this.large.delete(id);
      return;
    }
    for (let cx = span.x0; cx <= span.x1; cx++) {
      for (let cy = span.y0; cy <= span.y1; cy++) {
        const key = cellKey(cx, cy);
        const cell = this.cells.get(key);
        cell?.delete(id);
        if (cell?.size === 0) {
          this.cells.delete(key);
        }
      }
    }
  }

  /** Returns the value of id, if the index has it. */
  get(id: string): T | undefined {
    return this.entries.get(id)?.value;
  }

  /** Returns the rectangle of id, if the index has it. */
  rect(id: string): Rect | undefined {
    return this.entries.get(id)?.rect;
  }

  /**
   * Calls found with each entry whose rectangle overlaps r, once each. A
   * query of a rectangle larger than the grid's cells are worth reads the
   * entries instead.
   */
  query(r: Rect, found: (id: string, value: T, rect: Rect) => void): void {
    const span = spanOf(r);
    const seen = new Set<string>();
    const visit = (id: string) => {
      if (seen.has(id)) {
        return;
      }
      seen.add(id);
      const e = this.entries.get(id);
      if (e !== undefined && intersects(e.rect, r)) {
        found(id, e.value, e.rect);
      }
    };
    if (cellsOf(span) > this.cells.size) {
      for (const id of this.entries.keys()) {
        visit(id);
      }
      return;
    }
    for (let cx = span.x0; cx <= span.x1; cx++) {
      for (let cy = span.y0; cy <= span.y1; cy++) {
        const cell = this.cells.get(cellKey(cx, cy));
        if (cell !== undefined) {
          for (const id of cell) {
            visit(id);
          }
        }
      }
    }
    for (const id of this.large) {
      visit(id);
    }
  }

  /** Calls each with every entry. */
  forEach(each: (id: string, value: T, rect: Rect) => void): void {
    for (const [id, e] of this.entries) {
      each(id, e.value, e.rect);
    }
  }
}
