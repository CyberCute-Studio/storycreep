// The writing statistics of a node, in its inspector (PRD FR-TEXT-06, plan
// note N76): the words and the characters of its text and of the labels of
// the connections that leave it, and how many of those are choices and how
// many conditions it has, read as the inspector shows the node, and again as
// each operation of the project applies, so that they follow an edit of it
// within a second (AC1).

import type { Node } from "@storycreep/shared/api";
import { useQuery } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useProjectDocument } from "../document/react";
import { Alert } from "../ui/Page";
import { CountList } from "./Counts";
import { nodeStatisticsQuery } from "./queries";

/** The statistics of node, a section of its inspector. */
export function NodeStatistics({
  projectId,
  node,
}: {
  projectId: string;
  node: Node;
}) {
  const { t } = useTranslation();
  const headingId = useId();
  const doc = useProjectDocument(projectId);
  const stats = useQuery(nodeStatisticsQuery(doc, node.id));
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h3 id={headingId} className="text-lg font-semibold">
        {t("inspector.statistics")}
      </h3>
      <p className="text-sm text-fg-muted">{t("inspector.statistics_hint")}</p>
      {stats.isPending ? (
        <p className="text-fg-muted">{t("usage.loading")}</p>
      ) : stats.isError ? (
        <Alert message={refusalOf(stats.error, t)?.message} />
      ) : (
        <>
          <CountList counts={stats.data} />
          {!stats.data.complete && (
            <p className="text-fg-muted">{t("statistics.incomplete")}</p>
          )}
        </>
      )}
    </section>
  );
}
