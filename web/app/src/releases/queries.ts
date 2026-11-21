// A project's releases, its snapshots (PRD FR-SNAP-01, FR-SNAP-02,
// FR-SNAP-04, plan note N88), as the server answers them: the list, one
// release with its stored report, the version that publishing proposes, a
// release's files, read from its zip, and the entity diff of two releases,
// or of a release and the draft. A snapshot's build runs on the server, and
// tells no one when it ends, so what shows one that is building reads it
// again until it has (buildPoll).

import {
  type Acknowledgement,
  ApiError,
  type Diff,
  type Finding,
  type Proposal,
  type PublishInputBody,
  type Report,
  type SignedInBody,
  type Snapshot,
} from "@storycreep/shared/api";
import type { Manifest } from "@storycreep/shared/published";
import { type QueryClient, queryOptions } from "@tanstack/react-query";
import { allAtOnce, api, everyPage, pagedQuery } from "../api";
import { csrfHeaders } from "../auth/session";
import { sentWait, whenSent } from "../document/sent";
import type { ProjectDocument } from "../document/store";
import { type ReleaseContent, readRelease } from "./content";
import { readZip } from "./zip";

/** How often a page reads a snapshot that is building again, in milliseconds. */
export const buildPoll = 2000;

/** How long a poll waits after a refusal of the rate limit that names no time, in milliseconds. */
const limitedPoll = 30_000;

/** How long the page keeps a release's files once nothing shows them: an export counts against the hourly limit of exports, and a release's files never change. */
const contentKept = 30 * 60 * 1000;

/**
 * The version of the published format that the release's view reads, which
 * the types of @storycreep/shared/published are of, so that a change of them
 * to a new version fails the typecheck here.
 */
const viewedSchema: Manifest["schemaVersion"] = 1;

/** The key of every read of projectId's snapshots. */
function snapshotsKey(projectId: string) {
  return ["projects", projectId, "snapshots"] as const;
}

/** The key of what a publication changes: the lists, and the proposed version. */
function listsKey(projectId: string) {
  return [...snapshotsKey(projectId), "lists"] as const;
}

/** The project's snapshots, of every status, newest first, a page at a time. */
export function releasesQuery(projectId: string) {
  return pagedQuery(
    [...listsKey(projectId), "all"] as const,
    async (query, signal) =>
      (
        await api.call(
          "listSnapshots",
          { path: { project_id: projectId }, query },
          { signal },
        )
      ).body,
    { limit: 50, meta: { showsNotFound: true } },
  );
}

/** Every release of the project that is ready, newest first, which a diff compares. */
export function readyReleasesQuery(projectId: string) {
  return queryOptions({
    queryKey: [...listsKey(projectId), "ready"] as const,
    queryFn: ({ signal }): Promise<Snapshot[]> =>
      everyPage(
        async (cursor) =>
          (
            await api.call(
              "listSnapshots",
              {
                path: { project_id: projectId },
                query: { status: "ready", limit: allAtOnce, cursor },
              },
              { signal },
            )
          ).body,
      ),
    meta: { showsNotFound: true },
  });
}

/** The project's newest release that is ready, or null for none (PRD §7.5). */
export function latestReleaseQuery(projectId: string) {
  return queryOptions({
    queryKey: [...listsKey(projectId), "latest"] as const,
    queryFn: async ({ signal }): Promise<Snapshot | null> =>
      (
        await api.call(
          "listSnapshots",
          {
            path: { project_id: projectId },
            query: { status: "ready", limit: 1 },
          },
          { signal },
        )
      ).body.items[0] ?? null,
  });
}

/** The version that publishing proposes (PRD §7.3). */
export function nextVersionQuery(projectId: string) {
  return queryOptions({
    queryKey: [...listsKey(projectId), "next"] as const,
    queryFn: async ({ signal }): Promise<Proposal> =>
      (
        await api.call(
          "getNextSnapshotVersion",
          { path: { project_id: projectId } },
          { signal },
        )
      ).body,
  });
}

/** Has the page read what a publication changed again. */
export function readReleasesAgain(client: QueryClient, projectId: string) {
  return client.invalidateQueries({ queryKey: listsKey(projectId) });
}

/**
 * Returns how long a poll of a build waits after its last read, which error
 * failed, if it did: a refusal of the rate limit is waited out, and another
 * refusal, which reading again would only repeat, as of a snapshot that is
 * gone, ends the poll. A read that no answer came to, or that the server
 * failed, is tried again at the usual pace.
 */
function nextPoll(error: unknown): number | false {
  if (!(error instanceof ApiError) || error.status >= 500) {
    return buildPoll;
  }
  if (error.status !== 429) {
    return false;
  }
  const seconds = Number.parseInt(
    error.response.headers.get("Retry-After") ?? "",
    10,
  );
  return Number.isFinite(seconds)
    ? Math.max(buildPoll, seconds * 1000)
    : limitedPoll;
}

/** A release's metadata, which the page reads again while it is building. */
export function releaseQuery(projectId: string, snapshotId: string) {
  return queryOptions({
    queryKey: [...snapshotsKey(projectId), "one", snapshotId] as const,
    queryFn: async ({ signal }): Promise<Snapshot> =>
      (
        await api.call(
          "getSnapshot",
          { path: { project_id: projectId, snapshot_id: snapshotId } },
          { signal },
        )
      ).body,
    meta: { showsNotFound: true },
    refetchInterval: (query) =>
      query.state.data?.status === "building"
        ? nextPoll(query.state.error)
        : false,
  });
}

/**
 * The report stored with a release (PRD FR-DEP-04 AC2), or null while it has
 * none, as one that is building, or whose build failed before it validated.
 */
export function releaseReportQuery(projectId: string, snapshotId: string) {
  return queryOptions({
    queryKey: [...snapshotsKey(projectId), "report", snapshotId] as const,
    queryFn: async ({ signal }): Promise<Report | null> => {
      try {
        return (
          await api.call(
            "getSnapshotValidation",
            { path: { project_id: projectId, snapshot_id: snapshotId } },
            { signal },
          )
        ).body;
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) {
          return null;
        }
        throw error;
      }
    },
    meta: { showsRefusals: true },
  });
}

/** What a ready release holds, read from its zip, once, since it never changes. */
export function releaseContentQuery(projectId: string, snapshotId: string) {
  return queryOptions({
    queryKey: [...snapshotsKey(projectId), "content", snapshotId] as const,
    queryFn: async ({ signal }): Promise<ReleaseContent> => {
      const answer = await api.call(
        "exportSnapshot",
        {
          path: { project_id: projectId, snapshot_id: snapshotId },
          query: { schema: viewedSchema },
        },
        { signal },
      );
      const bytes = new Uint8Array(await answer.body.arrayBuffer());
      return readRelease(await readZip(bytes));
    },
    // Static, rather than an infinite staleTime, so that neither a focus of
    // the window after a failed read, nor a change of the project's settings
    // that reads the project again, spends another export on it.
    staleTime: "static",
    gcTime: contentKept,
    // Each attempt that reaches the server counts as an export, as one whose
    // answer was cut off midway has, which the client cannot tell from one
    // that never arrived, so no attempt is made again of the page's accord.
    retry: false,
    meta: { showsRefusals: true },
  });
}

/** What a diff compares into: a release's ID, or the draft. */
export const draft = "draft";

/**
 * The entity diff of the release from into to, a release's ID or draft
 * (PRD FR-SNAP-04). A diff of two releases never changes. One into the draft
 * is read once as what shows it opens, after the page's batches, or after
 * sentWait, so that it holds what the page shows, and again only when asked
 * (refetch), and is dropped once nothing shows it, so that the next to show
 * it reads the draft as it is then. The route counts in the rate limit of
 * writes that the page's edits need (FR-API-04), so no read of either is
 * tried again of the page's accord.
 */
export function diffQuery(
  doc: ProjectDocument,
  from: string,
  to: string,
  session: SignedInBody | null,
) {
  const projectId = doc.projectId;
  return queryOptions({
    queryKey: [...snapshotsKey(projectId), "diff", from, to] as const,
    queryFn: async ({ signal }): Promise<Diff> => {
      if (to === draft) {
        await whenSent(doc, signal, sentWait);
      }
      return (
        await api.call(
          "diffSnapshots",
          {
            path: { project_id: projectId },
            headers: csrfHeaders(session),
            body: { from, to },
          },
          { signal },
        )
      ).body;
    },
    staleTime: "static",
    retry: false,
    ...(to === draft ? { gcTime: 0 } : {}),
    meta: { showsRefusals: true },
  });
}

/**
 * Returns the two sides that the compare page shows, of those that its
 * address names and the project's ready releases, newest first: a side that
 * it leaves out is the release next to the other, so that a release alone
 * compares with the one before it, or into the draft if it is the newest,
 * and the draft from the newest. With neither, the newest two, or the only
 * release and the draft.
 */
export function comparedPair(
  ready: readonly Pick<Snapshot, "id">[],
  from: string | undefined,
  to: string | undefined,
): { from: string | undefined; to: string } {
  if (from !== undefined) {
    if (to !== undefined) {
      return { from, to };
    }
    const at = ready.findIndex((s) => s.id === from);
    return { from, to: at > 0 ? ready[at - 1].id : draft };
  }
  if (to === undefined) {
    return {
      from: ready[1]?.id ?? ready[0]?.id,
      to: ready.length > 1 ? ready[0].id : draft,
    };
  }
  if (to === draft) {
    return { from: ready[0]?.id, to };
  }
  const at = ready.findIndex((s) => s.id === to);
  if (at < 0) {
    return { from: ready[0]?.id, to };
  }
  // The oldest has none before it, and compares into the draft.
  const before = ready[at + 1];
  return before === undefined
    ? { from: to, to: draft }
    : { from: before.id, to };
}

/** Publishes the draft as a release, answered with its snapshot, building. */
export async function publishRelease(
  projectId: string,
  body: PublishInputBody,
  session: SignedInBody | null,
): Promise<Snapshot> {
  return (
    await api.call("publishSnapshot", {
      path: { project_id: projectId },
      headers: csrfHeaders(session),
      body,
    })
  ).body;
}

/**
 * Saves the zip of a ready release as the browser's download, under the
 * name that the server gives it (PRD FR-SNAP-02): its files as they were
 * built, at the release's own version of the format.
 */
export async function downloadRelease(
  projectId: string,
  release: Pick<Snapshot, "id" | "version">,
): Promise<void> {
  const { body, response } = await api.call("exportSnapshot", {
    path: { project_id: projectId, snapshot_id: release.id },
  });
  const named = /filename="?([^";]+)"?/i.exec(
    response.headers.get("Content-Disposition") ?? "",
  )?.[1];
  const url = URL.createObjectURL(body);
  const link = document.createElement("a");
  link.href = url;
  link.download = named ?? `release-${release.version}.zip`;
  document.body.append(link);
  link.click();
  link.remove();
  // A browser reads the object's URL as the download starts, which is after
  // the click returns.
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/** A version, MAJOR.MINOR.PATCH, each a number from 0 to 999,999,999 without a leading zero (PRD FR-SNAP-01). */
export const versionPattern =
  /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;

/** Returns the major number of version, or undefined for what is no version. */
export function majorOf(version: string): number | undefined {
  const match = versionPattern.exec(version);
  return match === null ? undefined : Number(match[1]);
}

/** Returns the next major version after version, or undefined past the greatest. */
export function nextMajor(version: string): string | undefined {
  const major = majorOf(version);
  return major === undefined || major >= 999_999_999
    ? undefined
    : `${major + 1}.0.0`;
}

/** What a diff against the last release holds that suggests a major version (PRD §7.3). */
export interface MajorChanges {
  /** How many entities it removes. */
  removed: number;
  /** How many variables it gives another type. */
  retyped: number;
  /** Whether the diff left out changes of variables, so that more may be retyped. */
  unsure: boolean;
}

/** Returns what of diff suggests a major version: content removed, or variables retyped. */
export function majorChanges(diff: Diff): MajorChanges {
  const { variables } = diff.project;
  const counts = diff.summary.kinds.variable;
  return {
    removed: diff.summary.removed,
    retyped: variables.filter((c) =>
      c.fields?.some((field) => field.field === "type"),
    ).length,
    unsure:
      variables.length <
      counts.added +
        counts.removed +
        counts.modified +
        counts.moved +
        counts.cosmetic,
  };
}

/** Returns the acknowledgement of a warning, which names it as the report does. */
export function acknowledgementOf(finding: Finding): Acknowledgement {
  return {
    code: finding.code as Acknowledgement["code"],
    entity_id: finding.entity_id,
    ...(finding.field === undefined ? {} : { field: finding.field }),
    ...(finding.target === undefined ? {} : { target: finding.target }),
  };
}
