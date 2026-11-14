// The validation of a project (PRD FR-DEP-03, plan notes N55 and N77), as
// the server reports it: every finding of its draft as of one moment, those
// that are not muted apart from those that are. The server validates the
// draft anew for each read, so a read gives what the project holds now, and
// the project's document has its reads read again as its operations apply
// (Follower), so that what the page shows follows an edit, the page's own,
// as a mute, or another session's.

import type {
  Finding,
  Report,
  ValidationMutePayload,
} from "@storycreep/shared/api";
import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { sentWait, whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";

/** The key of every read of the validation of projectId, which each operation of it has read again. */
export function validationKey(projectId: string) {
  return ["projects", projectId, "validation"] as const;
}

/**
 * The validation report of the project of doc, read once the page's
 * batches have been answered, or after sentWait, so that it is of what the
 * page shows, as a mute that the page has just sent.
 */
export function validationQuery(doc: ProjectDocument) {
  return queryOptions({
    queryKey: [...validationKey(doc.projectId), "report"] as const,
    queryFn: async ({ signal }): Promise<Report> => {
      await whenSent(doc, signal, sentWait);
      return (
        await api.call(
          "validateProject",
          { path: { project_id: doc.projectId } },
          { signal },
        )
      ).body;
    },
    meta: { showsNotFound: true },
  });
}

/** A code that a mute silences, a warning's or an info's. */
export type MutedCode = ValidationMutePayload["code"];

/**
 * Returns the code of finding, if a mute may silence it: a warning's or an
 * info's, as every rule's of those severities is about an entity that a
 * mute may be of (FR-DEP-03), or undefined for an error's, which no mute
 * silences.
 */
export function mutedCode(finding: Finding): MutedCode | undefined {
  return finding.severity === "error" ? undefined : (finding.code as MutedCode);
}

/** Returns the key of finding among a report's: its code, its entity, its field, and what it names, which no two findings share. */
export function findingKey(finding: Finding): string {
  return [
    finding.code,
    finding.entity_id,
    finding.field ?? "",
    finding.target?.id ?? "",
    finding.target?.attribute_id ?? "",
  ].join(":");
}
