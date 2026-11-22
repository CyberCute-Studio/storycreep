// The text export (PRD FR-TEXT-05, plan note N89): a project's narrative
// text, or one board's, of the draft or of a release, as a CSV or a JSON file
// for review and voice-over. The statistics' page offers the draft's, whose
// word counts add up to what it shows once it has counted every text, and a
// release's page that release's.

import type { Snapshot } from "@storycreep/shared/api";
import { useMutation } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../api";
import { refusalOf } from "../auth/refusal";
import { sentWait, whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";
import { saveDownload } from "../releases/queries";
import { Button } from "../ui/Button";
import { SelectField } from "../ui/Field";
import { RefusalAlert } from "../ui/Page";

/** The formats of the export's file. */
export type TextExportFormat = "csv" | "json";

/** What to export: of the draft, or of release, of the whole project, or of board. */
export interface TextExportRequest {
  projectId: string;
  release?: Pick<Snapshot, "id" | "version">;
  board?: string;
  format: TextExportFormat;
}

/** Saves the text export of request as the browser's download, under the server's name. */
export async function downloadText({
  projectId,
  release,
  board,
  format,
}: TextExportRequest): Promise<void> {
  const query = { format, ...(board === undefined ? {} : { board }) };
  const { body, response } =
    release === undefined
      ? await api.call("exportDraftText", {
          path: { project_id: projectId },
          query,
        })
      : await api.call("exportSnapshotText", {
          path: { project_id: projectId, snapshot_id: release.id },
          query,
        });
  saveDownload(body, response, `${release?.version ?? "draft"}-text.${format}`);
}

/**
 * The form that downloads the text export of the project projectId: of
 * release, or else of the draft, and of the whole project or one of boards.
 * A draft's is read once doc's batches have been answered, or after sentWait,
 * as the statistics beside it are, so that it holds the edits that the page
 * shows. A board that the list no longer holds, as one that another session
 * deleted while the form was open, is no longer chosen, even if it comes
 * back, so that the form never asks for what it does not show. Its choices
 * wait while a download is on its way. A refusal stays until the next
 * download or a change of a choice, even if the board that it was of has left
 * the list, as it says why no file came. Its page keys it by what it exports,
 * so that a refusal is never shown of another release or project.
 */
export function TextExport({
  projectId,
  doc,
  release,
  boards,
}: {
  projectId: string;
  doc?: ProjectDocument;
  release?: Pick<Snapshot, "id" | "version">;
  boards: readonly { id: string; name: string }[];
}) {
  const { t } = useTranslation();
  const heading = useId();
  const [chosen, setChosen] = useState("");
  const [format, setFormat] = useState<TextExportFormat>("csv");
  const board = boards.some((b) => b.id === chosen) ? chosen : "";
  if (board !== chosen) {
    setChosen("");
  }
  const download = useMutation({
    mutationFn: async () => {
      if (doc !== undefined) {
        await whenSent(doc, new AbortController().signal, sentWait);
      }
      await downloadText({
        projectId,
        release,
        format,
        ...(board === "" ? {} : { board }),
      });
    },
  });
  // A refusal is of what was asked for, so it goes once the form asks for
  // something else, which it cannot while a download is on its way.
  const clearRefusal = () => download.reset();
  return (
    <section aria-labelledby={heading} className="space-y-2">
      <h2 id={heading} className="text-xl font-semibold">
        {t("text_export.title")}
      </h2>
      <p>
        {release === undefined
          ? t("text_export.intro_draft")
          : t("text_export.intro_release", { version: release.version })}
      </p>
      <div className="flex flex-wrap items-end gap-4">
        <SelectField
          label={t("text_export.scope")}
          value={board}
          disabled={download.isPending}
          onChange={(e) => {
            setChosen(e.target.value);
            clearRefusal();
          }}
        >
          <option value="">{t("text_export.whole_project")}</option>
          {boards.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </SelectField>
        <SelectField
          label={t("text_export.format")}
          value={format}
          disabled={download.isPending}
          onChange={(e) => {
            setFormat(e.target.value as TextExportFormat);
            clearRefusal();
          }}
        >
          <option value="csv">{t("text_export.csv")}</option>
          <option value="json">{t("text_export.json")}</option>
        </SelectField>
        <Button disabled={download.isPending} onClick={() => download.mutate()}>
          {t("text_export.download")}
        </Button>
      </div>
      <RefusalAlert refusal={refusalOf(download.error, t)} />
    </section>
  );
}
