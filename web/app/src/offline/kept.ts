// What a page reads from the device once no answer comes from the server,
// as offline (PRD FR-OFF-02, plan note N97): the session, the account, and
// the instance that the account last signed in had, and the projects that
// it keeps offline, each with its role and its workspace, which the device
// keeps with the projects' copies (offline/keeper.ts). The page's queries of
// them read the server first, and the device only where the request got
// no answer at all (NetworkError), so that a page that loads offline, as
// the installed app opened on a flight, starts as the account, at the
// board that it last showed, from the copy of its project; any answer of
// the server's, a refusal too, is the server's, as without a device's.

import { NetworkError, type ProjectBody } from "@storycreep/shared/api";
import type { QueryClient } from "@tanstack/react-query";
import type { Workspace } from "../workspace/queries";
import type { KeptSession } from "./db";

/** What the device keeps, as the page reads it once the server does not answer. */
export interface KeptReads {
  /** The session that the device keeps, of the account last signed in, or undefined for none. */
  session(): Promise<KeptSession | undefined>;
  /** The project of userId's copy of projectId, with the account's role on it, or undefined for none. */
  project(userId: string, projectId: string): Promise<ProjectBody | undefined>;
  /** The workspace of one of userId's copies, of workspaceId, or undefined for none. */
  workspace(
    userId: string,
    workspaceId: string,
  ): Promise<Workspace | undefined>;
}

/** What the device keeps, of each page's query client that has one (keepReads). */
const kept = new WeakMap<QueryClient, KeptReads>();

/** Has client's queries read reads once the server does not answer them, from now on. */
export function keepReads(client: QueryClient, reads: KeptReads): void {
  kept.set(client, reads);
}

/**
 * Returns what read answers, or, where it rejects with a NetworkError, as
 * once the browser has no connection, what from finds that the device keeps
 * of it, for client's page; where the device keeps none, or client's page
 * has no device's, it rejects as read did.
 */
export async function orKept<T>(
  client: QueryClient,
  read: () => Promise<T>,
  from: (reads: KeptReads) => Promise<T | undefined>,
): Promise<T> {
  try {
    return await read();
  } catch (error) {
    const reads = kept.get(client);
    if (!(error instanceof NetworkError) || reads === undefined) {
      throw error;
    }
    const found = await from(reads).catch(() => undefined);
    if (found === undefined) {
      throw error;
    }
    return found;
  }
}
