// The upload of an archive to a project import (PRD FR-DATA-01 AC4): the file
// is sliced into the import's chunks, which go one at a time, so that no more
// than one chunk of it is held at once, however large the archive. A request
// that fails for a while is tried again with backoff, and an upload that is
// interrupted goes on from what the server says it has.

import {
  ApiError,
  NetworkError,
  type ProjectImportBody,
} from "@storycreep/shared/api";
import { api } from "../api";
import { backoff } from "../document/realtime";

/** How many times a request is tried again before the upload stops. */
export const retries = 5;

/**
 * The longest wait for a request to be tried again, in milliseconds: a
 * Retry-After beyond it stops the upload, which the user resumes, rather
 * than holding the dialog for longer without a word.
 */
export const longestWait = 120_000;

/** How many times completing an upload may find that it lacks chunks. */
const completeRounds = 3;

/** What one run of an upload works with and reports to. */
export interface UploadRun {
  workspaceId: string;
  headers: { "X-CSRF-Token": string };
  /** Stops the run, whose request under way is cut off. */
  signal: AbortSignal;
  /** Told how many bytes of the archive the server holds, as that grows. */
  onSent: (bytes: number) => void;
  /** Told whether the run is waiting to try a request again. */
  onWaiting: (waiting: boolean) => void;
}

/** Returns the code of the server's refusal that error is, if it is one. */
export function refusalCode(error: unknown): string | undefined {
  return error instanceof ApiError ? error.error?.code : undefined;
}

/**
 * Reports whether error may pass by itself: no answer came, the server
 * failed, or its rate limit is waiting. Any other refusal would be repeated.
 */
export function isTransient(error: unknown): boolean {
  return (
    error instanceof NetworkError ||
    (error instanceof ApiError && (error.status >= 500 || error.status === 429))
  );
}

/**
 * Returns the wait that a Retry-After header asks for, in milliseconds, as
 * seconds or as a date, or undefined where it has neither.
 */
export function retryAfter(
  header: string | null,
  now = Date.now(),
): number | undefined {
  const value = header?.trim();
  if (value === undefined || value === "") {
    return undefined;
  }
  if (/^\d+$/.test(value)) {
    return Number(value) * 1000;
  }
  const date = Date.parse(value);
  return Number.isNaN(date) ? undefined : Math.max(0, date - now);
}

/**
 * Returns how long to wait before trying a request again after failures
 * failures in a row, of which error is the last: what the server asks for,
 * or otherwise the backoff of the app's real-time channel.
 */
export function retryWait(error: unknown, failures: number): number {
  const asked =
    error instanceof ApiError
      ? retryAfter(error.response.headers.get("Retry-After"))
      : undefined;
  return asked ?? backoff(failures, Math.random);
}

/** Resolves after ms milliseconds, or rejects as signal aborts. */
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason);
      return;
    }
    const stop = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", stop);
      resolve();
    }, ms);
    signal.addEventListener("abort", stop, { once: true });
  });
}

/**
 * Returns what request answers, trying it again while it fails for a while
 * (isTransient), up to retries times, and rethrows the failure that is left.
 */
async function withRetries<T>(
  request: () => Promise<T>,
  run: UploadRun,
): Promise<T> {
  for (let failures = 0; ; failures++) {
    try {
      const answer = await request();
      run.onWaiting(false);
      return answer;
    } catch (error) {
      if (run.signal.aborted) {
        throw error;
      }
      const wait =
        isTransient(error) && failures < retries
          ? retryWait(error, failures)
          : undefined;
      if (wait === undefined || wait > longestWait) {
        run.onWaiting(false);
        throw error;
      }
      run.onWaiting(true);
      await sleep(wait, run.signal);
    }
  }
}

/** Returns the size of the chunk index of item. */
function chunkSize(item: ProjectImportBody, index: number): number {
  return index === item.chunk_count - 1
    ? item.size_bytes - index * item.chunk_bytes
    : item.chunk_bytes;
}

/** Returns the indexes of the chunks that the server lacks, in order. */
export function missingChunks(item: ProjectImportBody): number[] {
  const received = new Set(item.received_chunks);
  const missing: number[] = [];
  for (let index = 0; index < item.chunk_count; index++) {
    if (!received.has(index)) {
      missing.push(index);
    }
  }
  return missing;
}

/** Returns how many bytes of the archive the server holds. */
export function receivedBytes(item: ProjectImportBody): number {
  let bytes = 0;
  for (const index of new Set(item.received_chunks)) {
    bytes += chunkSize(item, index);
  }
  return bytes;
}

/** Reads the import id again, trying again as any request of the run does. */
export async function readImport(
  id: string,
  run: UploadRun,
): Promise<ProjectImportBody> {
  return (
    await withRetries(
      () =>
        api.call(
          "getProjectImport",
          { path: { workspace_id: run.workspaceId, import_id: id } },
          { signal: run.signal },
        ),
      run,
    )
  ).body;
}

/** Sends the chunks of file that item lacks, one at a time, in order. */
async function sendChunks(
  file: Blob,
  item: ProjectImportBody,
  run: UploadRun,
): Promise<void> {
  let sent = receivedBytes(item);
  run.onSent(sent);
  for (const index of missingChunks(item)) {
    const start = index * item.chunk_bytes;
    // A slice is a reference to that part of the file, which the browser
    // reads only as it sends it.
    const body = file.slice(
      start,
      start + chunkSize(item, index),
      "application/octet-stream",
    );
    await withRetries(
      () =>
        api.call(
          "putProjectImportChunk",
          {
            path: {
              workspace_id: run.workspaceId,
              import_id: item.id,
              index,
            },
            headers: run.headers,
            body,
          },
          { signal: run.signal },
        ),
      run,
    );
    sent += body.size;
    run.onSent(sent);
  }
}

/**
 * Sends what item lacks of file, and hands the whole upload to the import's
 * job, and returns the import as it then is. A complete whose answer was
 * lost is answered import_not_uploading when it is tried again, as is an
 * upload that another tab finished or cancelled, so either reads the import
 * instead of failing.
 */
export async function finishUpload(
  file: Blob,
  first: ProjectImportBody,
  run: UploadRun,
): Promise<ProjectImportBody> {
  let item = first;
  for (let round = 1; ; round++) {
    try {
      await sendChunks(file, item, run);
      return (
        await withRetries(
          () =>
            api.call(
              "completeProjectImport",
              {
                path: { workspace_id: run.workspaceId, import_id: item.id },
                headers: run.headers,
              },
              { signal: run.signal },
            ),
          run,
        )
      ).body;
    } catch (error) {
      const code = refusalCode(error);
      if (code === "import_not_uploading") {
        return readImport(item.id, run);
      }
      if (code !== "import_incomplete" || round === completeRounds) {
        throw error;
      }
      item = await readImport(item.id, run);
      if (item.status !== "uploading") {
        return item;
      }
    }
  }
}

/** Reports whether item is an upload still taking chunks, of the same name and size as file. */
export function isUploadOf(item: ProjectImportBody, file: File): boolean {
  return (
    item.status === "uploading" &&
    item.file_name === file.name.trim() &&
    item.size_bytes === file.size
  );
}

/** What beginning an import found: the import, and whether to send it file. */
export interface Begun {
  item: ProjectImportBody;
  /**
   * Whether the run goes on to upload: false for an import on its way that
   * is not this file, or that was asked for with another ID policy, which
   * the dialog shows rather than continue with what the user did not ask for.
   */
  upload: boolean;
}

/**
 * Asks for an import of file. A user has one import of a workspace on its
 * way at a time, so where the server names one that is of this same file
 * with this policy the run resumes it, as also where a create whose answer
 * was lost is tried again.
 */
export async function beginImport(
  file: File,
  policy: "auto" | "remap",
  run: UploadRun,
): Promise<Begun> {
  try {
    const { body } = await withRetries(
      () =>
        api.call(
          "createProjectImport",
          {
            path: { workspace_id: run.workspaceId },
            headers: run.headers,
            body: {
              file_name: file.name,
              size_bytes: file.size,
              id_policy: policy,
            },
          },
          { signal: run.signal },
        ),
      run,
    );
    return { item: body, upload: true };
  } catch (error) {
    if (
      !(error instanceof ApiError) ||
      refusalCode(error) !== "import_in_progress"
    ) {
      throw error;
    }
    const id = error.error?.details?.import_id;
    if (typeof id !== "string") {
      throw error;
    }
    const item = await readImport(id, run);
    return {
      item,
      upload: isUploadOf(item, file) && item.id_policy === policy,
    };
  }
}
