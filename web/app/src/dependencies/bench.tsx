// The dependency view's benchmark (PRD NFR-PERF-11, FR-DEP-02 AC3): the view
// opened with ?bench=load, as the canvas harness's run in CI does and as the
// reference tablet's checklist does by hand (docs/canvas-checklist.md),
// shows when its graph was first drawn, from the start of the view's load,
// which its route marks (openedMark) as it begins to read the project, its
// tree, and its graph, so that the render that NFR-PERF-11 times is the
// view's own, as an opening of it from the project's other pages is, rather
// than the app's start, which every page shares; and from the start of the
// page's load too, which the harness reports beside it. The harness reads
// both from the element's data-bench-result.

import { formatNumber } from "@storycreep/shared/i18n";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { BenchPanel } from "../ui/BenchPanel";

/** The performance mark of the start of the view's load, which its route makes before it reads anything (NFR-PERF-11). */
export const openedMark = "dependencies-opened";

/** What a run of the view's benchmark measured. */
export interface DependencyBenchResult {
  /** When the frame after the one that first drew the graph began, in milliseconds from the start of the view's load (openedMark). */
  rendered: number;
  /** The same, in milliseconds from the start of the page's load. */
  loaded: number;
  boards: number;
  pairs: number;
  edges: number;
}

/**
 * The result of the view's benchmark, once drawnAt says when the graph was
 * first drawn, as of then: a later navigation of the view, as a filter's,
 * which has its route mark the start of its load anew, and draws another
 * graph, leaves it as it is.
 */
export function DependencyBench({
  drawnAt,
  boards,
  pairs,
  edges,
}: {
  drawnAt: number | undefined;
  boards: number;
  pairs: number;
  edges: number;
}) {
  const { t, i18n } = useTranslation();
  const [result, setResult] = useState<DependencyBenchResult>();
  useEffect(() => {
    if (drawnAt === undefined || result !== undefined) {
      return;
    }
    const opened = performance.getEntriesByName(openedMark)[0]?.startTime ?? 0;
    setResult({
      rendered: drawnAt - opened,
      loaded: drawnAt,
      boards,
      pairs,
      edges,
    });
  }, [drawnAt, result, boards, pairs, edges]);
  const n = (value: number, digits = 0) =>
    formatNumber(i18n.language, value, { maximumFractionDigits: digits });
  return (
    <BenchPanel
      scenario="dependencies"
      phase={result === undefined ? "running" : "done"}
      result={result}
    >
      {result !== undefined && (
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1">
          <dt>{t("bench.graph_rendered")}</dt>
          <dd>{t("bench.ms", { value: n(result.rendered, 1) })}</dd>
          <dt>{t("bench.graph_loaded")}</dt>
          <dd>{t("bench.ms", { value: n(result.loaded, 1) })}</dd>
          <dt>{t("bench.graph_boards")}</dt>
          <dd>{n(result.boards)}</dd>
          <dt>{t("bench.graph_edges")}</dt>
          <dd>{n(result.edges)}</dd>
        </dl>
      )}
    </BenchPanel>
  );
}
