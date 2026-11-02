// The page's session (PRD FR-AUTH-02): what signing in and out change in
// the server's state that the page holds, the CSRF token that every request
// that changes something sends, and the routes' guards, which send a
// signed-out page to the sign-in screen and back again once it has signed
// in.

import type { LoginBody, SignedInBody } from "@storycreep/shared/api";
import type { QueryClient } from "@tanstack/react-query";
import { type ParsedLocation, redirect } from "@tanstack/react-router";
import { accountQuery, api, instanceQuery, sessionQuery } from "../api";
import type { RouterContext } from "../router";

/** The queries of the page that outlive its session, as the server's version. */
const sessionless = new Set(["version", "instance", "session"]);

/**
 * The headers of a request that changes something: the session's CSRF
 * token, without which the server refuses it with csrf_failed.
 */
export function csrfHeaders(session: SignedInBody): {
  "X-CSRF-Token": string;
} {
  return { "X-CSRF-Token": session.csrf_token };
}

/**
 * Records in client that the page signed in, or that its session has a new
 * secret, as after a change of the password: the session that the answer
 * body holds, with its CSRF token. Whatever the page held of another
 * account's is dropped.
 */
export function signedIn(client: QueryClient, session: SignedInBody): void {
  const previous = client.getQueryData(sessionQuery.queryKey);
  if (previous?.user_id !== session.user_id) {
    dropAccountData(client);
  }
  client.setQueryData(sessionQuery.queryKey, session);
}

/**
 * Signs the page in with credentials (PRD FR-AUTH-02), and records the
 * session in client, as every screen that ends signed in does: signing in,
 * first-run setup, registering, and accepting an invitation. It rejects
 * with the server's refusal, as invalid_credentials.
 */
export async function signIn(
  client: QueryClient,
  credentials: LoginBody,
): Promise<SignedInBody> {
  const session = (await api.call("login", { body: credentials })).body;
  signedIn(client, session);
  return session;
}

/**
 * Records in client that the page is signed out, and drops what it held of
 * the account. A component still shown reads the session as null until the
 * page that it is on goes, as the router's guards send it to the sign-in
 * screen.
 */
export function signedOut(client: QueryClient): void {
  client.setQueryData(sessionQuery.queryKey, null);
  dropAccountData(client);
}

/**
 * Ends the page's signed-in state once its session has ended, by signing
 * out or elsewhere: the session is null for the components still shown,
 * which keep what they read of the account until the page goes to the
 * sign-in screen (toSignIn), so that none asks for it again, and what the
 * page held of the account is dropped once it is there.
 */
export async function endSession(
  client: QueryClient,
  toSignIn: () => Promise<void>,
): Promise<void> {
  client.setQueryData(sessionQuery.queryKey, null);
  await toSignIn();
  signedOut(client);
}

function dropAccountData(client: QueryClient): void {
  client.removeQueries({
    predicate: (query) => !sessionless.has(String(query.queryKey[0])),
  });
}

/**
 * Returns path if it is a path of the app, as the sign-in screen's redirect
 * holds, and undefined otherwise: a URL of another origin, or a path that a
 * browser reads as one, as //example.com or /\example.com, whose redirect
 * would take the user off the instance once they had signed in.
 */
export function appPath(path: unknown): string | undefined {
  if (typeof path !== "string" || !path.startsWith("/")) {
    return undefined;
  }
  // A browser reads a backslash as a slash in the path of an http URL.
  const second = path.charAt(1);
  return second === "/" || second === "\\" ? undefined : path;
}

/** The search of the sign-in screen: the page to go back to. */
export interface SignInSearch {
  redirect?: string;
}

/** Reads the sign-in screen's search, keeping only a redirect within the app. */
export function signInSearch(search: Record<string, unknown>): SignInSearch {
  const path = appPath(search.redirect);
  return path === undefined ? {} : { redirect: path };
}

/** What a route's beforeLoad is given that the guards read. */
interface GuardArgs {
  context: RouterContext;
  location: ParsedLocation;
}

/**
 * The guard of a route that needs a session: a signed-out page goes to the
 * sign-in screen, which comes back to the page once it has signed in. It
 * makes sure that the account is in the cache, which the route's components
 * read.
 */
export async function requireSession({ context, location }: GuardArgs) {
  const session = await context.queryClient.ensureQueryData(sessionQuery);
  if (session === null) {
    throw redirect({ to: "/login", search: { redirect: location.href } });
  }
  await context.queryClient.ensureQueryData(accountQuery);
  return { session };
}

/**
 * The guard of a screen for a signed-out page, as signing in or
 * registering: a page that has a session goes to the start page instead.
 */
export async function requireSignedOut({ context }: GuardArgs) {
  const session = await context.queryClient.ensureQueryData(sessionQuery);
  if (session !== null) {
    throw redirect({ to: "/" });
  }
}

/**
 * Makes sure that the instance, the session, and, with a session, the
 * account are in the cache, which every page reads, and returns them.
 */
export async function loadSessionState(client: QueryClient) {
  const [instance, session] = await Promise.all([
    client.ensureQueryData(instanceQuery),
    client.ensureQueryData(sessionQuery),
  ]);
  const account =
    session === null ? null : await client.ensureQueryData(accountQuery);
  return { instance, session, account };
}
