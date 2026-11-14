// What a route's loader reads of what its address names, as a workspace or a
// project by its ID, which shows the page of no page where the server has
// none for the user (notFoundBy), as the server does not tell a workspace
// that the user may not see from one that does not exist.

import { decodeId, encodeId, type IdKind } from "@storycreep/shared/ids";
import { notFound } from "@tanstack/react-router";
import { notFoundBy } from "../api";

/** Resolves to what read resolves to, or throws the router's notFound where the server has none. */
export async function found<T>(read: Promise<T>): Promise<T> {
  try {
    return await read;
  } catch (error) {
    if (notFoundBy(error)) {
      throw notFound();
    }
    throw error;
  }
}

/**
 * Returns the ID of kind that a page's address holds as the server writes
 * it (encodeId), which the page's queries are keyed by, as are those that a
 * change reads again, and which the account's memberships hold: the server
 * takes an ID in either case, as one typed by hand in lowercase, which would
 * otherwise key queries of its own, which no change reads again, and match
 * none of the memberships. One that is no ID of kind is kept as it is, which
 * the server refuses as none (notFoundBy), which shows the page of no page.
 */
export function canonicalId(kind: IdKind, id: string): string {
  return searchId(kind, id) ?? id;
}

/**
 * Returns the ID of kind that a page's search holds as a filter of what the
 * page asks the server for, as the server writes it (encodeId), or
 * undefined for none, or for one that is no ID of kind, as a link cut short
 * holds: the page then shows what it shows without that filter, as the board
 * page opens the canvas as without a node that the board does not have,
 * rather than ask the server with it, which refuses it as none
 * (notFoundBy), and show the page of no page for a page that is there.
 */
export function searchId(kind: IdKind, id: unknown): string | undefined {
  if (typeof id !== "string" || id === "") {
    return undefined;
  }
  try {
    return encodeId(kind, decodeId(kind, id));
  } catch {
    return undefined;
  }
}
