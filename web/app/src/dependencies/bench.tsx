// The dependency view's benchmark (PRD NFR-PERF-11, FR-DEP-02 AC3): the view
// opened with ?bench=load, as the canvas harness's run in CI does and as the
// reference tablet's checklist does by hand (docs/canvas-checklist.md),
// shows when its graph was first drawn, from the start of the page's load,
// which the harness reads from the element's data-bench-result.

import { formatNumber } from "@storycreep/shared/i18n";
import { useTranslation } from "react-i18next";

/** What a run of the view's benchmark measured. */
export interface DependencyBenchResult {
  /** When the frame after the one that first drew the graph began, in milliseconds from the start of the page's load. */
  rendered: number;
  boards: number;
  pairs: number;
  edges: number;
}

/** The result of the view's benchmark, once drawnAt says when the graph was first drawn. */
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
  const result: DependencyBenchResult | undefined =
    drawnAt === undefined
      ? undefined
      : { rendered: drawnAt, boards, pairs, edges };
  const n = (value: number, digits = 0) =>
    formatNumber(i18n.language, value, { maximumFractionDigits: digits });
  return (
    <section
      aria-labelledby="bench-title"
      className="space-y-2 rounded-md border border-border bg-surface p-4"
      data-bench-phase={result === undefined ? "running" : "done"}
      data-bench-result={
        result === undefined ? undefined : JSON.stringify(result)
      }
    >
      <h2 id="bench-title" className="text-lg font-semibold">
        {t("bench.title", { scenario: "dependencies" })}
      </h2>
      <p role="status">
        {result === undefined ? t("bench.recording") : t("bench.done")}
      </p>
      {result !== undefined && (
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1">
          <dt>{t("bench.graph_rendered")}</dt>
          <dd>{t("bench.ms", { value: n(result.rendered, 1) })}</dd>
          <dt>{t("bench.graph_boards")}</dt>
          <dd>{n(result.boards)}</dd>
          <dt>{t("bench.graph_edges")}</dt>
          <dd>{n(result.edges)}</dd>
        </dl>
      )}
    </section>
  );
}
