// Signing out (PRD FR-AUTH-02 AC2): the server deletes the session and
// clears its cookie, and the page goes to the sign-in screen and drops what
// it held of the account.

import type { SignedInBody } from "@storycreep/shared/api";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { api } from "../api";
import { csrfHeaders, endSession } from "./session";

/** Returns the mutation that signs the page out of session. */
export function useSignOut() {
  const client = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: async (session: SignedInBody) => {
      await api.call("logout", { headers: csrfHeaders(session) });
    },
    onSuccess: () =>
      endSession(client, () => router.navigate({ to: "/login" })),
  });
}
