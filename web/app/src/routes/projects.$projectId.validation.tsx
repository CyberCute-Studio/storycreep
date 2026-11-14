import type { Finding } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useProjectDocument } from "../document/react";
import { editsProject, projectQuery, useCommands } from "../project/queries";
import { found } from "../shell/found";
import { Button } from "../ui/Button";
import { Alert, Page, Status } from "../ui/Page";
import {
  FindingItem,
  type FindingNames,
  MuteDialog,
  type Muting,
  useFindingNames,
} from "../validation/Findings";
import {
  findingKey,
  type MutedCode,
  validationQuery,
} from "../validation/queries";

export const Route = createFileRoute("/projects/$projectId/validation")({
  // The project first, so that one that the account may not see shows the
  // page of no page, and then its report.
  loader: async ({ context, params }) => {
    await found(
      context.queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    await found(
      context.queryClient.ensureQueryData(
        validationQuery(context.documents.get(params.projectId)),
      ),
    );
  },
  gcTime: 0,
  component: Validation,
});

/** How many findings a list shows at first, and how many more each Show more shows. */
const shownAtOnce = 100;

// A project's validation (PRD FR-DEP-03, plan note N77): what the server
// finds of its draft, its errors, which block publishing, its warnings, and
// its infos, each with what is wrong, as the server says it, and where, by
// a link that opens the editor of what it is about (AC3), and, apart, the
// warnings and infos that are muted, each with its reason (AC4), which an
// editor mutes, changes the reason of, and unmutes here. The server
// validates the draft anew for each read, and the page reads the report
// again as each operation of the project applies, so that it follows an
// edit, here or in another session, as a fix, or a mute.
function Validation() {
  const { t, i18n } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const doc = useProjectDocument(projectId);
  const report = useSuspenseQuery(validationQuery(doc)).data;
  const userId = useSuspenseQuery(sessionQuery).data?.user_id;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  const names = useFindingNames(projectId, [
    ...report.findings,
    ...report.muted,
  ]);
  const [muting, setMuting] = useState<Muting>();
  const [said, setSaid] = useState<string>();
  const unmute = useCommands(projectId);
  const unmuteRefusal = refusalOf(unmute.error, t);
  const mutedHeading = useRef<HTMLHeadingElement>(null);
  // A mute's dialog opens without what the page said of the last mute or
  // unmute, nor the last unmute's refusal, which its own success would
  // otherwise be shown beside.
  const onMute = (m: Muting) => {
    setSaid(undefined);
    unmute.reset();
    setMuting(m);
  };
  const onUnmute = (finding: Finding, code: MutedCode) => {
    setSaid(undefined);
    // The finding goes back to its severity's list, with the button, and
    // the focus to the heading of the muted ones, once each unmute applies,
    // as one pressed before the last was answered does too, whose callbacks
    // of mutate the mutation would drop for the last's.
    void unmute
      .mutateAsync([
        {
          type: "validation.unmute",
          payload: { entity_id: finding.entity_id, code },
        },
      ])
      .then(
        () => {
          setSaid(t("validation.unmuted"));
          mutedHeading.current?.focus();
        },
        // A refusal is shown by the page's alert, of the mutation's error.
        () => undefined,
      );
  };
  const number = (n: number) => formatNumber(i18n.language, n);
  const { counts } = report;
  const severities = [
    ["error", t("validation.errors", { count: counts.errors })],
    ["warning", t("validation.warnings", { count: counts.warnings })],
    ["info", t("validation.infos", { count: counts.infos })],
  ] as const;
  return (
    <Page
      title={t("validation.title")}
      documentTitle={t("validation.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("validation.intro")}</p>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1">
        <dt className="text-fg-muted">{t("validation.count_errors")}</dt>
        <dd className="tabular-nums">{number(counts.errors)}</dd>
        <dt className="text-fg-muted">{t("validation.count_warnings")}</dt>
        <dd className="tabular-nums">{number(counts.warnings)}</dd>
        <dt className="text-fg-muted">{t("validation.count_infos")}</dt>
        <dd className="tabular-nums">{number(counts.infos)}</dd>
        <dt className="text-fg-muted">{t("validation.count_muted")}</dt>
        <dd className="tabular-nums">{number(counts.muted)}</dd>
      </dl>
      <Status message={said} />
      <Alert message={unmuteRefusal?.message} />
      {report.findings.length === 0 && <p>{t("validation.none")}</p>}
      {severities.map(([severity, heading]) => (
        <FindingSection
          key={severity}
          id={`validation-${severity}`}
          heading={heading}
          findings={report.findings.filter((f) => f.severity === severity)}
          projectId={projectId}
          names={names}
          editable={editable}
          userId={userId}
          onMute={onMute}
          onUnmute={onUnmute}
        />
      ))}
      <section aria-labelledby="validation-muted" className="space-y-2">
        <h2
          id="validation-muted"
          ref={mutedHeading}
          tabIndex={-1}
          className="text-xl font-semibold"
        >
          {t("validation.muted", { count: counts.muted })}
        </h2>
        <p className="text-fg-muted">{t("validation.muted_intro")}</p>
        {report.muted.length === 0 ? (
          <p>{t("validation.none_muted")}</p>
        ) : (
          <FindingList
            findings={report.muted}
            projectId={projectId}
            names={names}
            editable={editable}
            userId={userId}
            onMute={onMute}
            onUnmute={onUnmute}
          />
        )}
      </section>
      {muting !== undefined && (
        <MuteDialog
          projectId={projectId}
          muting={muting}
          names={names}
          onClose={() => setMuting(undefined)}
          onMuted={() => {
            // One that the report no longer has, as one that another
            // session fixed while the dialog was open, is muted all the
            // same, as a mute is part of what it is about, but is listed
            // under Muted only once it is found again.
            const key = findingKey(muting.finding);
            const listed = [...report.findings, ...report.muted].some(
              (f) => findingKey(f) === key,
            );
            setSaid(
              muting.finding.mute !== undefined
                ? t("validation.reason_changed")
                : listed
                  ? t("validation.muted_now")
                  : t("validation.muted_gone"),
            );
            // A finding muted now goes to the muted ones, with the button
            // that opened the dialog; one whose reason changed stays.
            return muting.finding.mute === undefined
              ? mutedHeading.current
              : muting.opener;
          }}
        />
      )}
    </Page>
  );
}

interface ListProps {
  findings: readonly Finding[];
  projectId: string;
  names: FindingNames;
  editable: boolean;
  userId: string | undefined;
  onMute: (muting: Muting) => void;
  onUnmute: (finding: Finding, code: MutedCode) => void;
}

/** The findings of a severity, under their heading, if there are any. */
function FindingSection({
  id,
  heading,
  ...list
}: ListProps & { id: string; heading: string }) {
  if (list.findings.length === 0) {
    return null;
  }
  return (
    <section aria-labelledby={id} className="space-y-2">
      <h2 id={id} className="text-xl font-semibold">
        {heading}
      </h2>
      <FindingList {...list} />
    </section>
  );
}

/** A list of findings, of shownAtOnce at first, and as many more at each press of Show more. */
function FindingList({ findings, ...item }: ListProps) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(shownAtOnce);
  return (
    <>
      <ul>
        {findings.slice(0, shown).map((finding) => (
          <FindingItem key={findingKey(finding)} finding={finding} {...item} />
        ))}
      </ul>
      {findings.length > shown && (
        <Button onClick={() => setShown((n) => n + shownAtOnce)}>
          {t("validation.more", { count: findings.length - shown })}
        </Button>
      )}
    </>
  );
}
