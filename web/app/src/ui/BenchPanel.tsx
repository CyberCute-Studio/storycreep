// The panel of a scripted run of a benchmark (PRD §9.1), as the canvas's
// (canvas/bench.tsx) and the dependency view's (dependencies/bench.tsx) show
// it: what the harness reads, the run's phase, in the element's
// data-bench-phase, which it waits for to be done, and what the run
// measured, in its data-bench-result, under the heading that names the
// run, with what the run measured as a person reads it.

import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Where a run is: waiting for what it measures, warming up, recording, or done. */
export type BenchPhase = "waiting" | "warm" | "running" | "done";

/** The panel of a run of scenario, in phase, with what it measured, result, once it has. */
export function BenchPanel({
  scenario,
  phase,
  result,
  children,
}: {
  scenario: string;
  phase: BenchPhase;
  /** What the run measured, which the harness reads as JSON, once it has. */
  result: object | undefined;
  /** What the run measured, as a person reads it. */
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-labelledby="bench-title"
      className="space-y-2 rounded-md border border-border bg-surface p-4"
      data-bench-phase={phase}
      data-bench-result={
        result === undefined ? undefined : JSON.stringify(result)
      }
    >
      <h2 id="bench-title" className="text-lg font-semibold">
        {t("bench.title", { scenario })}
      </h2>
      <p role="status">
        {phase === "done"
          ? t("bench.done")
          : phase === "running"
            ? t("bench.recording")
            : t("bench.warming")}
      </p>
      {children}
    </section>
  );
}
