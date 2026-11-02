// Signing out (PRD FR-AUTH-02 AC2): the server deletes the session and
// clears its cookie, and the page goes to the sign-in screen and drops what
// it held of the account.

import type { SignedInBody } from "@storycreep/shared/api";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { api } from "../api";
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

/** Returns the mutation that signs the page out of session. */
export function useSignOut({ stay = false }: SignOutOptions = {}) {
  const client = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: async (session: SignedInBody) => {
      await api.call("logout", { headers: csrfHeaders(session) });
    },
    onSuccess: () =>
      stay
        ? signedOut(client)
        : endSession(client, () => router.navigate({ to: "/login" })),
  });
}
