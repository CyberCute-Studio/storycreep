// What a route's loader reads of what its address names, as a workspace or a
// project by its ID, which shows the page of no page where the server has
// none for the user (notFoundBy), as the server does not tell a workspace
// that the user may not see from one that does not exist.

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
