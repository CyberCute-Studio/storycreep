// Signing out (PRD FR-AUTH-02 AC2): the server deletes the session and
// clears its cookie, and the page goes to the sign-in screen and drops what
// it held of the account.

import type { SignedInBody } from "@storycreep/shared/api";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { api, signedOutBy } from "../api";
import { refusalOf } from "./refusal";
import { csrfHeaders, endSession, signedOut } from "./session";

export interface SignOutOptions {
  /**
   * Whether the page stays where it is, signed out, instead of going to the
   * sign-in screen, as the page of an invitation does, which then offers to
   * sign in with another account to accept it. The page must show nothing
   * of the account once the session is null.
   */
  stay?: boolean;
}

/**
 * Returns the mutation that signs the page out of session. What shows its
 * control shows its failure too (signOutFailure): the page is still signed
 * in then, which a user who walked away from it would not know.
 */
export function useSignOut({ stay = false }: SignOutOptions = {}) {
  const client = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: async (session: SignedInBody) => {
      try {
        await api.call("logout", { headers: csrfHeaders(session) });
      } catch (error) {
        // A session that has ended already, as elsewhere, is signed out.
        if (!signedOutBy(error)) {
          throw error;
        }
      }
    },
    onSuccess: () =>
      stay
        ? signedOut(client)
        : endSession(client, () => router.navigate({ to: "/login" })),
  });
}

/** What signOutFailure reads of the mutation of useSignOut. */
interface SignOutState {
  error: Error | null;
  /** The session that the mutation signed out of, or failed to. */
  variables: SignedInBody | undefined;
}

/**
 * Returns what a page says of signOut's failure while the page is still
 * signed in to the session that it failed to end, session, the page's, and
 * undefined otherwise: once the session has ended some other way, as in
 * another tab, or the page has signed in again since, the failure is of a
 * session that the page no longer has, which saying that it is still
 * signed in would belie.
 */
export function signOutFailure(
  signOut: SignOutState,
  session: SignedInBody | null | undefined,
  t: TFunction,
): string | undefined {
  if (
    signOut.error === null ||
    !session ||
    signOut.variables?.session.id !== session.session.id
  ) {
    return undefined;
  }
  return t("auth.error.sign_out", {
    reason: refusalOf(signOut.error, t).message ?? "",
  });
}
