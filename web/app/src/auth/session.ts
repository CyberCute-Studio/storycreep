// The page's session (PRD FR-AUTH-02): what signing in and out change in
// the server's state that the page holds, the CSRF token that every request
// that changes something sends, and the routes' guards, which send a
// signed-out page to the sign-in screen and back again once it has signed
// in.

import type {
  AccountBody,
  LoginBody,
  SignedInBody,
} from "@storycreep/shared/api";
import type { QueryClient } from "@tanstack/react-query";
import { type ParsedLocation, redirect } from "@tanstack/react-router";
import {
  accountQuery,
  api,
  fetchSession,
  instanceQuery,
  sessionQuery,
  signedOutBy,
} from "../api";
import type { RouterContext } from "../router";

/**
 * The queries of the page that outlive its session, whose answers are the
 * same whoever asks: the server's version, the instance, what first-run
 * setup asks for, an invitation that its link shows, which a page that
 * signs in with it keeps showing, and what became of a link that its page
 * used (linkOutcome, and the verification of an address). Every other query
 * is dropped once the page signs in to another account or out, so that one
 * that this list misses is asked for again, rather than another account's
 * answer shown.
 */
const sessionless = new Set([
  "version",
  "instance",
  "session",
  "setup",
  "invite",
  "verify-email",
  "reset-password",
]);

/**
 * The headers of a request that changes something: the session's CSRF
 * token, without which the server refuses it with csrf_failed. It throws
 * if the page is signed out, as a form still shown may be once its session
 * has ended, whose request would be refused.
 */
export function csrfHeaders(session: SignedInBody | null | undefined): {
  "X-CSRF-Token": string;
} {
  if (!session) {
    throw new Error("The page is signed out.");
  }
  return { "X-CSRF-Token": session.csrf_token };
}

/**
 * Records in client that the page signed in, or that its session has a new
 * secret, as after a change of the password, which followSession learns:
 * the session that the answer body holds, with its CSRF token. Whatever
 * the page held of another account's is dropped, and the session's account
 * is asked for first, so that the page has both at once: a component that
 * reads the account once the page has a session, as an invitation's step
 * that follows signing in where the page is, has it, rather than waiting
 * for it, which shows none of the page's content (useSuspenseQuery). It is
 * asked for again of the same account too, so that what another tab has
 * changed of it shows once the page asks for its session, as once its tab
 * is shown again: an address that another tab verified, as the emailed
 * link's does, or a language saved elsewhere. If the server cannot answer,
 * the page keeps what it had of the account, or, for another account, the
 * components ask for it themselves, and so it does if it has recorded the
 * account itself while it asked, as the answer to saving the language,
 * which is newer. A session that the page only learnt of, as followSession
 * does, is recorded only if current still says so once the account has
 * come, after which the page may have changed its session itself, as by
 * signing in; signedIn resolves to whether it recorded it.
 */
export async function signedIn(
  client: QueryClient,
  session: SignedInBody,
  current: () => boolean = () => true,
): Promise<boolean> {
  const held = client.getQueryData(accountQuery.queryKey);
  const account = await api.call("getAccount", {}).then(
    (answer) => answer.body,
    () => undefined,
  );
  if (!current()) {
    return false;
  }
  const switched =
    client.getQueryData(sessionQuery.queryKey)?.user_id !== session.user_id;
  if (switched) {
    dropAccountData(client);
  }
  const newer =
    !switched && client.getQueryData(accountQuery.queryKey) !== held;
  if (account?.id === session.user_id && !newer) {
    client.setQueryData(accountQuery.queryKey, account);
  }
  client.setQueryData(sessionQuery.queryKey, session);
  return true;
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
  await signedIn(client, session);
  return session;
}

/** What endSession shows the page again with: the app's router. */
interface Reloadable {
  invalidate: () => Promise<void>;
}

/**
 * Ends the page's signed-in state once its session has ended, as once the
 * page signed out, or once the session ended elsewhere (sessionChanges):
 * the page shows itself again, through the guards of its routes, which
 * decide whether it needs a session. A page that needs one goes to the
 * sign-in screen (requireSession), which comes back to it, with the token
 * of a link's fragment, if it has one, still in a fragment, and one that
 * needs none, as a link's page, stays where it is, signed out, as an
 * invitation's does, to sign in with another account. The session is null
 * for the components still shown, which keep what they read of the
 * account until the page has shown itself again, so that none asks for it
 * again, and what the page held of the account is dropped then, or if the
 * page cannot show itself again.
 */
export async function endSession(
  client: QueryClient,
  router: Reloadable,
): Promise<void> {
  client.setQueryData(sessionQuery.queryKey, null);
  try {
    await router.invalidate();
  } finally {
    dropAccountData(client);
  }
}

/** What followSession does once the session has changed elsewhere. */
export interface SessionChanges {
  /**
   * The page's session has ended, as when another tab signed out, or it
   * was revoked.
   */
  ended: () => void;
  /**
   * The page is signed in to another account than it was, or to one where
   * it was signed out, as when another tab signed in: the session is
   * recorded in the client already, and what the page held of the account
   * before is dropped, so the page shows itself anew.
   */
  replaced: () => void;
}

/** The page's following of its session (followSession). */
export interface SessionFollower {
  /**
   * Asks the server for the session now: once it answered a request with
   * 401 unauthenticated, which tells that the session may have ended, or
   * that the request went with one that the page has replaced since, as by
   * changing the password, whose session is still the page's; once it
   * answered one with 403 csrf_failed, as after another tab changed the
   * password, which gave the session a new CSRF token; and once the page
   * has done what changes the session in the server alone
   * (RouterContext.checkSession), as changing the password, whose new
   * secret it then records, or resetting one, which ends every session of
   * its account. It resolves once the answer is recorded, or once a later
   * question has made it one that no longer counts.
   */
  check: () => Promise<void>;
  /** Stops following the session. */
  stop: () => void;
}

/**
 * Keeps the page's session the one of the browser's cookie, which every tab
 * of the browser shares, and which another tab can change: by signing out,
 * by signing in to another account, or by changing the password, which
 * gives the session a new CSRF token (PRD FR-AUTH-02). Whenever doc is
 * shown again, as when the user comes back to its tab, and whenever the
 * follower's check is called, the page asks the server for the session,
 * records it in client, with its account as the server has it now
 * (signedIn), and tells changes if the session is no longer the account's
 * that it was. Only the answer to the latest question counts,
 * since one asked before it may be of a session that has changed since,
 * as one that ended while the question was under way, and so only if the
 * page has not changed its session itself since it asked, as by signing
 * in, which makes the answer one of the session before.
 */
export function followSession(
  client: QueryClient,
  doc: Pick<
    Document,
    "visibilityState" | "addEventListener" | "removeEventListener"
  >,
  changes: SessionChanges,
): SessionFollower {
  let asked = 0;
  const ask = async () => {
    const known = client.getQueryData(sessionQuery.queryKey);
    if (known === undefined) {
      return;
    }
    const question = ++asked;
    const current = () =>
      question === asked &&
      client.getQueryData(sessionQuery.queryKey) === known;
    try {
      const session = await fetchSession();
      if (session === null) {
        if (known !== null && current()) {
          changes.ended();
        }
        return;
      }
      if (
        (await signedIn(client, session, current)) &&
        known?.user_id !== session.user_id
      ) {
        changes.replaced();
      }
    } catch {
      // The server could not say: the next request that needs the session
      // finds out, as any does.
    }
  };
  const shown = () => {
    if (doc.visibilityState === "visible") {
      void ask();
    }
  };
  doc.addEventListener("visibilitychange", shown);
  return {
    check: ask,
    stop: () => doc.removeEventListener("visibilitychange", shown),
  };
}

/**
 * What the page does once its session has changed elsewhere (followSession):
 * it shows itself again, through the guards of its routes, which decide
 * whether it needs a session. A page whose session has ended is signed out
 * (endSession), and a link's page, which stays where it is, shows what
 * became of the link (linkOutcome). A page signed in to another account
 * shows itself anew, as the root route keys it on the account that it
 * loaded for.
 */
export function sessionChanges(
  client: QueryClient,
  router: Reloadable,
): SessionChanges {
  return {
    ended: () => void endSession(client, router),
    replaced: () => void router.invalidate(),
  };
}

function dropAccountData(client: QueryClient): void {
  client.removeQueries({
    predicate: (query) => !sessionless.has(String(query.queryKey[0])),
  });
}

/** An origin that path resolves against, for appPath. */
const pageOrigin = "http://app.invalid";

/**
 * Returns path if it is a path of the app, as the sign-in screen's redirect
 * holds, and undefined otherwise: a URL of another origin, or a path that a
 * browser reads as one, as //example.com or /\example.com, with or without
 * a tab or a newline between the two, which a browser drops, whose
 * redirect would take the user off the instance once they had signed in,
 * and which the router refuses to go to. A path is the app's if a browser
 * resolves it to the page's own origin.
 */
export function appPath(path: unknown): string | undefined {
  if (typeof path !== "string" || !path.startsWith("/")) {
    return undefined;
  }
  try {
    return new URL(path, pageOrigin).origin === pageOrigin ? path : undefined;
  } catch {
    // A path that a browser reads as a URL of another host that it cannot
    // parse, as //[x.
    return undefined;
  }
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

/**
 * Returns where the sign-in screen is that comes back to location once the
 * page has signed in: the screen's redirect holds the location's path and
 * query, and the screen's own fragment holds the location's, so that the
 * token of a link, as an invitation's, stays in a fragment, which a browser
 * sends to no server, nor writes in a Referer or a query (plan note N26),
 * and comes back with it (signInReturn).
 */
export function signInFor(location: Pick<ParsedLocation, "href" | "hash">) {
  return {
    to: "/login",
    // The path and the query of href, whose fragment is the only part of it
    // with a #: the query that the router writes encodes one.
    search: { redirect: location.href.split("#", 1)[0] },
    hash: location.hash,
  } as const;
}

/**
 * Returns the page that the sign-in screen goes to once it has signed in:
 * its redirect, with the screen's fragment (signInFor), or else the start
 * page.
 */
export function signInReturn(search: SignInSearch, hash: string): string {
  if (search.redirect === undefined) {
    return "/";
  }
  return hash === "" ? search.redirect : `${search.redirect}#${hash}`;
}

/** What a route's beforeLoad is given that the guards read. */
interface GuardArgs {
  context: RouterContext;
  location: ParsedLocation;
}

/**
 * The guard of a route that needs a session: a signed-out page goes to the
 * sign-in screen, which comes back to the page once it has signed in. The
 * root route has read the account, which the route's components read,
 * before (loadSessionState).
 */
export async function requireSession({ context, location }: GuardArgs) {
  const session = await context.queryClient.ensureQueryData(sessionQuery);
  if (session === null) {
    throw redirect(signInFor(location));
  }
  return { session };
}

/**
 * The guard of a screen for a signed-out page, as signing in or
 * registering: a page that has a session goes to href instead, the start
 * page unless the screen names another, as the sign-in screen names the
 * page to go back to (signInReturn), as when another tab has signed in
 * since it was shown (followSession).
 */
export async function requireSignedOut(
  { context }: GuardArgs,
  href = "/",
): Promise<void> {
  const session = await context.queryClient.ensureQueryData(sessionQuery);
  if (session !== null) {
    throw redirect({ href });
  }
}

/**
 * Makes sure that the instance, the session, and, with a session, the
 * account are in the cache, which every page reads, and returns them. A
 * session whose account is answered with 401 unauthenticated has ended
 * since the server answered for it, as one revoked meanwhile, or of an
 * account suspended meanwhile: the page is signed out then, as the
 * question of the session that the answer asks for would find
 * (sessionDoubtedBy), rather than showing the error page until it has.
 */
export async function loadSessionState(client: QueryClient) {
  const [instance, known] = await Promise.all([
    client.ensureQueryData(instanceQuery),
    client.ensureQueryData(sessionQuery),
  ]);
  let session = known;
  let account: AccountBody | null = null;
  if (session !== null) {
    try {
      account = await client.ensureQueryData(accountQuery);
    } catch (error) {
      if (!signedOutBy(error)) {
        throw error;
      }
      session = null;
      client.setQueryData(sessionQuery.queryKey, null);
    }
  }
  return { instance, session, account };
}
