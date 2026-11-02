import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { accountQuery, instanceQuery } from "../api";
import { requireSession } from "../auth/session";
import { VerifyNotice } from "../auth/VerifyNotice";
import { PRODUCT_NAME } from "../product";
import { TextLink } from "../ui/Page";
import { useDocumentTitle } from "../ui/title";

export const Route = createFileRoute("/")({
  beforeLoad: requireSession,
  component: Home,
});

// The start page of a signed-in user, which the workspace's projects join
// with the management screens (plan item 55).
function Home() {
  const { t } = useTranslation();
  const account = useSuspenseQuery(accountQuery).data;
  const instance = useSuspenseQuery(instanceQuery).data;
  useDocumentTitle();
  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <h1 className="text-2xl font-semibold">{PRODUCT_NAME}</h1>
      <p>{t("home.signed_in", { name: account.display_name })}</p>
      <VerifyNotice account={account} instance={instance} />
      <p>{t("home.body")}</p>
      <p>
        <TextLink to="/account">{t("home.account")}</TextLink>
      </p>
    </div>
  );
}
