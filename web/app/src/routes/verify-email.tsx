import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, api, sessionQuery } from "../api";
import { useFragmentToken } from "../auth/fragment";
import { refusalOf } from "../auth/refusal";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/verify-email")({
  component: VerifyEmail,
});

// The page of a link that verifies an email address (PRD FR-AUTH-03),
// whose token is in the URL's fragment. Opening the link is the user's ask,
// so the page verifies the address as it opens, once: a mail client that
// fetches the link to preview it runs no script, and sends no fragment.
function VerifyEmail() {
  const { t } = useTranslation();
  const token = useFragmentToken();
  if (token === undefined) {
    return (
      <Page title={t("verify.title")}>
        <p>{t("verify.no_token")}</p>
        <p>
          <Next />
        </p>
      </Page>
    );
  }
  // Another link opened in the same page, whose fragment alone changes, is
  // verified in its turn, and shows its own outcome.
  return <VerifyToken key={token} token={token} />;
}

/** Where the page leads once it has said what became of the link. */
function Next() {
  const { t } = useTranslation();
  return useQuery(sessionQuery).data ? (
    <TextLink to="/">{t("verify.home")}</TextLink>
  ) : (
    <TextLink to="/login">{t("verify.sign_in")}</TextLink>
  );
}

function VerifyToken({ token }: { token: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const verify = useMutation({
    mutationFn: async (value: string) => {
      await api.call("verifyEmail", { body: { token: value } });
    },
    onSuccess: () =>
      client.invalidateQueries({ queryKey: accountQuery.queryKey }),
    meta: { showsRefusals: true },
  });
  // A component that React mounts twice, as in development, verifies once:
  // the link works once.
  const sent = useRef(false);
  const { mutate } = verify;
  useEffect(() => {
    if (!sent.current) {
      sent.current = true;
      mutate(token);
    }
  }, [token, mutate]);

  return (
    <Page title={t("verify.title")}>
      <Alert
        message={
          verify.error === null ? undefined : refusalOf(verify.error, t).message
        }
      />
      <Status
        message={
          verify.isSuccess
            ? t("verify.done")
            : verify.isPending
              ? t("verify.pending")
              : undefined
        }
      />
      {(verify.isSuccess || verify.isError) && (
        <p>
          <Next />
        </p>
      )}
    </Page>
  );
}
