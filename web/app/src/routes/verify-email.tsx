import {
  type QueryClient,
  queryOptions,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { accountQuery, api, sessionQuery } from "../api";
import { useFragmentToken } from "../auth/fragment";
import { refusalOf } from "../auth/refusal";
import { Alert, Page, Status, TextLink } from "../ui/Page";

export const Route = createFileRoute("/verify-email")({
  component: VerifyEmail,
});

/**
 * The verification of the link of token, which the page asks for as it
 * opens (PRD FR-AUTH-03). It is a query of the token, rather than a
 * mutation of the page's, so that what became of the link outlives the
 * page's components and its session, which sessionless lists it for
 * (auth/session.ts): a page that shows itself anew, as once another tab has
 * signed in, or once its session has ended elsewhere, which leaves it where
 * it is, says what became of the link, rather than using it again, which
 * the server would refuse as used, and so does a component that React
 * mounts twice, as in development. Since the link works once, it is never
 * asked for again, nor tried again after a failure, which may have used it.
 */
function verifyQuery(client: QueryClient, token: string) {
  return queryOptions({
    queryKey: ["verify-email", token],
    queryFn: async () => {
      await api.call("verifyEmail", { body: { token } });
      void client.invalidateQueries({ queryKey: accountQuery.queryKey });
      return "verified" as const;
    },
    staleTime: Number.POSITIVE_INFINITY,
    gcTime: Number.POSITIVE_INFINITY,
    retry: false,
    retryOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { showsRefusals: true },
  });
}

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
  const verify = useQuery(verifyQuery(client, token));
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
