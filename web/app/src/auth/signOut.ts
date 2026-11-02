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
 * control shows its error too (signOutFailure): the page is still signed
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
    meta: { showsRefusals: true },
  });
}

/** Returns what a page says of error, which signing out failed with. */
export function signOutFailure(error: unknown, t: TFunction): string {
  return t("auth.error.sign_out", {
    reason: refusalOf(error, t).message ?? "",
  });
}
