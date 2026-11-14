// The drawing of a project's dependency graph (PRD FR-DEP-02, plan note
// N77): each board a button, with its name and its badge's counts, at the
// place that the layout gives it (layout.ts), in the order of the rows, and
// along each, which the keyboard's focus follows, and each pair of boards
// that edges join a line from the board that the other depends on, with an
// arrow at the board that depends on it, of every kind of edge between
// them. A press of a board or of a line selects it, which the view inspects
// beside it. The lines are drawn for the eye: a keyboard, and a screen
// reader, select a pair from the view's list of them, which holds the same,
// as a board's button selects the board. A graph with cycles is drawn as
// any other, its lines that close one curving up along the right (AC4).

import type { GraphBoard } from "@storycreep/shared/api";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { type Layout, type LayoutSizes, layoutGraph } from "./layout";

/** A pair of boards that edges join, from the board that the other depends on: its key, as from:to. */
export function pairKey(from: string, to: string): string {
  return `${from}:${to}`;
}

/** The sizes of the drawing, in CSS pixels: a box of a finger's height and more (NFR-TOUCH-01). */
const sizes: Omit<LayoutSizes, "perRow"> = {
  width: 184,
  height: 64,
  gap: 32,
  rowGap: 64,
  margin: 32,
};

/** Returns how many boxes a row of a drawing width wide holds. */
function perRowOf(width: number): number {
  return Math.max(
    1,
    Math.floor(
      (width - 2 * sizes.margin + sizes.gap) / (sizes.width + sizes.gap),
    ),
  );
}

/** What the drawing shows of the selection: the pair of boards selected, or the board, whose lines it marks. */
export interface GraphSelection {
  pair?: string;
  board?: string;
}

/**
 * The drawing of the graph of boards, in their order, and of the pairs of
 * boards that its edges join, which lays out as wide as its element is,
 * and again as that changes, calling onDrawn once after the first frame
 * that shows it, as the view's benchmark reads (NFR-PERF-11).
 */
export function DependencyGraph({
  boards,
  pairs,
  selection,
  onSelectPair,
  onSelectBoard,
  onDrawn,
}: {
  boards: readonly GraphBoard[];
  pairs: readonly { from: string; to: string }[];
  selection: GraphSelection;
  onSelectPair: (pair: string) => void;
  onSelectBoard: (board: string) => void;
  onDrawn?: () => void;
}) {
  const { t } = useTranslation();
  const host = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState<number>();
  useLayoutEffect(() => {
    const element = host.current;
    if (element === null) {
      return;
    }
    setWidth(element.clientWidth);
    if (typeof ResizeObserver !== "function") {
      return;
    }
    const observer = new ResizeObserver(() => setWidth(element.clientWidth));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const perRow = width === undefined ? undefined : perRowOf(width);
  const layout = useMemo<Layout | undefined>(
    () =>
      perRow === undefined
        ? undefined
        : layoutGraph(
            boards.map((b) => b.id),
            pairs,
            { ...sizes, perRow },
          ),
    [boards, pairs, perRow],
  );
  // The boards in the order that the drawing shows them, row by row, and
  // along each row, which the keyboard's focus follows, rather than that of
  // the tree, which the layout places anywhere (WCAG 2.4.3).
  const shown = useMemo(
    () =>
      layout === undefined
        ? []
        : layout.boxes
            .map((box, i) => ({ box, board: boards[i] }))
            .sort((a, b) => a.box.y - b.box.y || a.box.x - b.box.x),
    [layout, boards],
  );
  const drawn = useRef(false);
  useLayoutEffect(() => {
    if (layout === undefined || drawn.current || onDrawn === undefined) {
      return;
    }
    drawn.current = true;
    // The frame after the one that this commit paints in.
    requestAnimationFrame(() => requestAnimationFrame(onDrawn));
  }, [layout, onDrawn]);
  const marked = (from: string, to: string) =>
    selection.pair === pairKey(from, to) ||
    selection.board === from ||
    selection.board === to;
  return (
    <div ref={host} className="w-full overflow-x-auto">
      {layout !== undefined && (
        <div
          className="relative"
          style={{ width: layout.width, height: layout.height }}
          data-graph-boards={layout.boxes.length}
          data-graph-pairs={layout.routes.length}
        >
          <svg
            aria-hidden="true"
            className="absolute inset-0"
            width={layout.width}
            height={layout.height}
          >
            <defs>
              <marker
                id="dependency-arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="8"
                markerHeight="8"
                orient="auto-start-reverse"
              >
                <path d="M 0 0 L 10 5 L 0 10 z" className="fill-fg-muted" />
              </marker>
              <marker
                id="dependency-arrow-marked"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="8"
                markerHeight="8"
                orient="auto-start-reverse"
              >
                <path d="M 0 0 L 10 5 L 0 10 z" className="fill-accent" />
              </marker>
            </defs>
            {layout.routes.map((r) => {
              const on = marked(r.from, r.to);
              return (
                <path
                  key={pairKey(r.from, r.to)}
                  d={r.path}
                  fill="none"
                  className={on ? "stroke-accent" : "stroke-fg-muted"}
                  strokeWidth={on ? 3 : 1.5}
                  strokeDasharray={r.up ? "6 4" : undefined}
                  markerEnd={`url(#${on ? "dependency-arrow-marked" : "dependency-arrow"})`}
                />
              );
            })}
            {/* What a press of a line hits: a band along it, 12 px to each
            side, as a touch's tap on a connection's line of the canvas
            takes (tapSlop, canvas/gestures.ts), which takes the press for
            its pair. It is no
            wider, as the bands of the lines that meet at a box's side would
            cover one another, and the list of every pair is what selects a
            pair whatever the pointer. */}
            {layout.routes.map((r) => (
              // biome-ignore lint/a11y/noStaticElementInteractions: the lines are drawn for the eye, and a press of one is a pointer's shortcut to the button of its pair in the view's list, which a keyboard and a screen reader use.
              <path
                key={pairKey(r.from, r.to)}
                d={r.path}
                fill="none"
                stroke="transparent"
                strokeWidth={24}
                pointerEvents="stroke"
                className="cursor-pointer"
                data-pair={pairKey(r.from, r.to)}
                onClick={() => onSelectPair(pairKey(r.from, r.to))}
              />
            ))}
          </svg>
          {shown.map(({ box, board }) => {
            const pressed = selection.board === board.id;
            return (
              <button
                key={board.id}
                type="button"
                aria-pressed={pressed}
                className={`absolute flex cursor-pointer flex-col items-start justify-center overflow-hidden rounded-md border px-3 text-start ${pressed ? "border-accent bg-accent text-accent-fg" : "border-border bg-surface text-fg hover:bg-surface-hover"}`}
                style={{
                  left: box.x,
                  top: box.y,
                  width: sizes.width,
                  height: sizes.height,
                }}
                onClick={() => onSelectBoard(board.id)}
              >
                <span className="w-full truncate font-medium">
                  {board.name}
                </span>
                {/* Its name, as read, is its two lines, the space apart. */}{" "}
                <span className={`text-sm ${pressed ? "" : "text-fg-muted"}`}>
                  {t("dependencies.board_counts", {
                    inbound: board.inbound,
                    outbound: board.outbound,
                  })}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
