// The dialog that publishes a project's draft as a release (PRD FR-SNAP-01,
// FR-DEP-04, §7.3, plan note N88): the validation report of the draft as it
// is now, whose errors block publishing and whose warnings need the box that
// acknowledges them, the proposed version, which the author may change, with
// the major version that the diff against the last release suggests where
// it removes content or retypes a variable, and the required note. The box
// acknowledges the warnings that it was ticked for: the report follows the
// draft while the dialog is open, so a warning that joins them clears it,
// rather than be published as acknowledged unread.

import {
  ApiError,
  type Finding,
  type ProjectBody,
  type PublishInputBody,
  type Snapshot,
} from "@storycreep/shared/api";
import {
  useMutation,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useProjectDocument } from "../document/react";
import { Button } from "../ui/Button";
import { closesUnlessPending, DialogFrame, FormButtons } from "../ui/Dialog";
import {
  CheckboxField,
  TextAreaField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert, RefusalAlert, Status, textLink } from "../ui/Page";
import {
  type FindingNames,
  useFindingNames,
  whereText,
} from "../validation/Findings";
import {
  findingKey,
  validationKey,
  validationQuery,
} from "../validation/queries";
import {
  acknowledgementOf,
  diffQuery,
  draft,
  latestReleaseQuery,
  majorChanges,
  majorOf,
  nextMajor,
  nextVersionQuery,
  publishRelease,
  readReleasesAgain,
  versionPattern,
} from "./queries";

/** The longest note, in characters (PRD FR-SNAP-01). */
const maxNote = 2000;

/** How many findings of a severity the dialog lists, of which the validation's page lists the rest. */
const findingsListed = 20;

/** The code of a refusal of error, if the server answered with one. */
function codeOf(error: unknown): string | undefined {
  return error instanceof ApiError ? error.error?.code : undefined;
}

/**
 * The button that opens the dialog, whose form starts anew each time it
 * opens. onPublished is told of the snapshot that it published, building,
 * as the dialog closes.
 */
export function PublishButton({
  project,
  onPublished,
}: {
  project: ProjectBody;
  onPublished: (snapshot: Snapshot) => void;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const publish = useMutation({
    mutationFn: (body: PublishInputBody) =>
      publishRelease(project.id, body, session),
    onSuccess: (snapshot) => {
      setOpen(false);
      onPublished(snapshot);
      return readReleasesAgain(client, project.id);
    },
    onError: (error) => {
      // What the dialog showed was out of date: it shows what the server
      // found instead, and the box is clear again if the warnings changed.
      const code = codeOf(error);
      if (code === "publish_blocked" || code === "warnings_unacknowledged") {
        void client.invalidateQueries({
          queryKey: validationKey(project.id),
        });
      } else if (code === "version_not_newer") {
        void readReleasesAgain(client, project.id);
      }
    },
  });
  return (
    <Dialog.Root
      open={open}
      onOpenChange={closesUnlessPending(setOpen, publish)}
    >
      <Dialog.Trigger asChild>
        <Button variant="primary">{t("releases.publish.open")}</Button>
      </Dialog.Trigger>
      <DialogFrame title={t("releases.publish.title")}>
        <PublishForm projectId={project.id} publish={publish} />
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The state of the dialog's request, which outlives its form. */
interface PublishRequest {
  isPending: boolean;
  error: Error | null;
  mutate: (body: PublishInputBody) => void;
  reset: () => void;
}

/** What the form itself refuses of what was typed, by field. */
interface Problems {
  version?: string;
  note?: string;
}

function PublishForm({
  projectId,
  publish,
}: {
  projectId: string;
  publish: PublishRequest;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const report = useQuery({
    ...validationQuery(doc),
    meta: { showsNotFound: true, showsRefusals: true },
  });
  const proposal = useQuery(nextVersionQuery(projectId));
  const [typedVersion, setTypedVersion] = useState<string>();
  const [note, setNote] = useState("");
  const [ackedKey, setAckedKey] = useState<string>();
  const [problems, setProblems] = useState<Problems>();
  const form = useRef<HTMLFormElement>(null);
  useFocusRefusal(form, publish.error);
  useFocusRefusal(form, problems);

  const version = typedVersion ?? proposal.data?.version ?? "";
  const findings = report.data?.findings ?? [];
  const errors = findings.filter((f) => f.severity === "error");
  const warnings = findings.filter((f) => f.severity === "warning");
  const names = useFindingNames(projectId, findings);
  // The warnings that the box was ticked for, which it stands for only
  // while they are the warnings that the report has.
  const warningsKey = warnings.map(findingKey).join("\n");
  const acknowledged = warnings.length === 0 || ackedKey === warningsKey;
  const ackOutdated =
    warnings.length > 0 && ackedKey !== undefined && !acknowledged;

  const refusal = refusalOf(publish.error, t, (location) =>
    location === "body.version"
      ? "version"
      : location === "body.note"
        ? "note"
        : undefined,
  );
  // That the version is not newer is about the version's field.
  const notNewer = codeOf(publish.error) === "version_not_newer";
  const versionError =
    problems?.version ??
    refusal?.fields.version ??
    (notNewer ? refusal?.message : undefined);
  const noteError = problems?.note ?? refusal?.fields.note;

  const ready = report.data !== undefined && errors.length === 0;
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (publish.isPending || report.data === undefined || !ready) {
      return;
    }
    if (!acknowledged) {
      return;
    }
    const text = note.trim();
    const found: Problems = {};
    if (!versionPattern.test(version)) {
      found.version = t("releases.publish.version_invalid");
    }
    if (text === "") {
      found.note = t("releases.publish.note_required");
    } else if ([...text].length > maxNote) {
      found.note = t("releases.publish.note_too_long", { max: maxNote });
    }
    if (found.version !== undefined || found.note !== undefined) {
      publish.reset();
      setProblems(found);
      return;
    }
    setProblems(undefined);
    publish.mutate({
      version,
      note: text,
      acknowledged_seq: report.data.seq,
      acknowledged_warnings: warnings.map(acknowledgementOf),
    });
  };
  const edited = () => {
    setProblems(undefined);
    if (!publish.isPending) {
      publish.reset();
    }
  };
  const latest = proposal.data?.latest_version;
  return (
    <form ref={form} noValidate onSubmit={onSubmit} className="space-y-4">
      <section aria-label={t("releases.publish.report")} className="space-y-2">
        {report.isPending && <p>{t("releases.publish.checking")}</p>}
        <Alert
          message={
            report.data === undefined
              ? refusalOf(report.error, t)?.message
              : undefined
          }
        />
        {report.data !== undefined && (
          <>
            <p className="font-medium">
              {t("releases.publish.counts", {
                errors: errors.length,
                warnings: warnings.length,
              })}
            </p>
            {errors.length > 0 && (
              <>
                <p className="text-danger">{t("releases.publish.blocked")}</p>
                <FindingLines findings={errors} names={names} />
                <p>
                  <Link
                    to="/projects/$projectId/validation"
                    params={{ projectId }}
                    className={textLink}
                  >
                    {t("releases.publish.open_validation")}
                  </Link>
                </p>
              </>
            )}
            {warnings.length > 0 && (
              <>
                <FindingLines findings={warnings} names={names} />
                {errors.length === 0 && (
                  <CheckboxField
                    label={t("releases.publish.acknowledge", {
                      count: warnings.length,
                    })}
                    checked={acknowledged}
                    onChange={(event) =>
                      setAckedKey(
                        event.target.checked ? warningsKey : undefined,
                      )
                    }
                    hint={
                      ackOutdated
                        ? t("releases.publish.acknowledge_outdated")
                        : undefined
                    }
                  />
                )}
              </>
            )}
          </>
        )}
      </section>
      <TextField
        label={t("releases.publish.version")}
        hint={
          latest === undefined
            ? t("releases.publish.version_first")
            : t("releases.publish.version_hint", { latest })
        }
        error={versionError}
        value={version}
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => {
          setTypedVersion(event.target.value);
          edited();
        }}
      />
      {latest !== undefined && report.data !== undefined && (
        <MajorSuggestion
          projectId={projectId}
          seq={report.data.seq}
          latest={latest}
          version={version}
          onUse={(major) => {
            setTypedVersion(major);
            edited();
          }}
        />
      )}
      <TextAreaField
        label={t("releases.publish.note")}
        hint={t("releases.publish.note_hint", { max: maxNote })}
        error={noteError}
        value={note}
        required
        onChange={(event) => {
          setNote(event.target.value);
          edited();
        }}
      />
      <RefusalAlert
        refusal={
          refusal === undefined || notNewer
            ? undefined
            : { ...refusal, fields: {} }
        }
      />
      <FormButtons
        cancel={t("releases.publish.cancel")}
        submit={t("releases.publish.submit")}
        pending={publish.isPending}
        disabled={!ready || !acknowledged}
      />
    </form>
  );
}

/** The findings of a severity, each with where it is, of which it lists findingsListed. */
function FindingLines({
  findings,
  names,
}: {
  findings: readonly Finding[];
  names: FindingNames;
}) {
  const { t } = useTranslation();
  const rest = findings.length - findingsListed;
  return (
    <>
      <ul>
        {findings.slice(0, findingsListed).map((finding) => (
          <li
            key={findingKey(finding)}
            className="space-y-1 border-t border-border py-2"
          >
            <p className="break-words">{finding.message}</p>
            <p className="break-words text-fg-muted">
              {whereText(t, finding, names)}
            </p>
          </li>
        ))}
      </ul>
      {rest > 0 && <p>{t("releases.publish.more", { count: rest })}</p>}
    </>
  );
}

/**
 * The major version that the diff of the last ready release into the draft
 * suggests, where it removes content or gives a variable another type (PRD
 * §7.3): a suggestion alone, since a version has no automatic meaning, which
 * goes once the version typed is of a greater major than latest's, and
 * which says so where it could not read the diff, rather than say nothing
 * of a draft that may well need it.
 */
function MajorSuggestion({
  projectId,
  seq,
  latest,
  version,
  onUse,
}: {
  projectId: string;
  /** The seq of the report, as of which the diff is read. */
  seq: number;
  /** The newest version of the project's releases, building or ready. */
  latest: string;
  version: string;
  onUse: (major: string) => void;
}) {
  const { t } = useTranslation();
  const session = useSuspenseQuery(sessionQuery).data;
  const release = useQuery(latestReleaseQuery(projectId));
  const from = release.data?.id;
  const diff = useQuery({
    ...diffQuery(projectId, from ?? "", draft, session, seq),
    enabled: from !== undefined,
  });
  if (release.isError || diff.isError) {
    return <p className="text-fg-muted">{t("releases.publish.no_diff")}</p>;
  }
  if (diff.data === undefined) {
    return null;
  }
  const major = nextMajor(latest);
  const typedMajor = majorOf(version);
  const latestMajor = majorOf(latest);
  if (
    major === undefined ||
    (typedMajor !== undefined &&
      latestMajor !== undefined &&
      typedMajor > latestMajor)
  ) {
    return null;
  }
  const { removed, retyped, unsure } = majorChanges(diff.data);
  if (removed === 0 && retyped === 0) {
    return unsure ? (
      <p className="text-fg-muted">{t("releases.publish.major_unsure")}</p>
    ) : null;
  }
  return (
    <div className="space-y-2">
      <Status
        message={t("releases.publish.major_suggested", {
          removed,
          retyped,
          major,
        })}
      />
      <Button onClick={() => onUse(major)}>
        {t("releases.publish.use_version", { version: major })}
      </Button>
    </div>
  );
}
