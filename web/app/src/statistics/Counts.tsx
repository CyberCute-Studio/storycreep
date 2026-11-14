// What the writing statistics show of a part of a project (PRD FR-TEXT-06):
// its words, characters, choices, and conditions, and its nodes, each with
// its name, as a list of terms, and the note that a part whose texts the
// server has yet to count, as content that predates the statistics, or of
// an older version's counts, may have other words and characters than it
// says.

import type { NodeStatistics } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import { useTranslation } from "react-i18next";

/** What the statistics of a node, a board, and the project have alike. */
export type Counts = Omit<NodeStatistics, "id" | "seq">;

/** The counts of a part of a project, as a list of terms, with its nodes' where it has them. */
export function CountList({
  counts,
  nodes,
}: {
  counts: Counts;
  nodes?: number;
}) {
  const { t, i18n } = useTranslation();
  const number = (n: number) => formatNumber(i18n.language, n);
  const rows: [string, number][] = [
    ...(nodes === undefined
      ? []
      : ([[t("statistics.nodes"), nodes]] as [string, number][])),
    [t("statistics.words"), counts.words],
    [t("statistics.characters"), counts.characters],
    [t("statistics.choices"), counts.choices],
    [t("statistics.conditions"), counts.conditions],
  ];
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1">
      {rows.map(([term, n]) => (
        <div key={term} className="contents">
          <dt className="text-fg-muted">{term}</dt>
          <dd className="tabular-nums">{number(n)}</dd>
        </div>
      ))}
    </dl>
  );
}
