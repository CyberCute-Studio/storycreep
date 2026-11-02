// What a page says of an email address that is not verified, on an
// instance that sends email (PRD FR-AUTH-03): that a link was emailed, which
// the user can ask for again. It says nothing on an instance that sends no
// email, whose addresses an instance admin's links verify, and which needs
// none verified (FR-AUTH-03): the account screen says that.

import type { AccountBody, InstanceBody } from "@storycreep/shared/api";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { Button } from "../ui/Button";
import { Alert, Status } from "../ui/Page";
import { refusalOf } from "./refusal";
import { csrfHeaders } from "./session";

export function VerifyNotice({
  account,
  instance,
}: {
  account: AccountBody;
  instance: InstanceBody;
}) {
  const { t } = useTranslation();
  const session = useQuery(sessionQuery).data;
  const resend = useMutation({
    mutationFn: async () => {
      await api.call("requestEmailVerification", {
        headers: csrfHeaders(session),
      });
    },
  });
  if (account.email_verified || !instance.email_configured) {
    return null;
  }
  return (
    <div className="space-y-2">
      <p>{t("verify_notice.body", { email: account.email })}</p>
      <Alert message={refusalOf(resend.error, t)?.message} />
      <Status
        message={resend.isSuccess ? t("verify_notice.sent") : undefined}
      />
      <Button disabled={resend.isPending} onClick={() => resend.mutate()}>
        {t("verify_notice.resend")}
      </Button>
    </div>
  );
}
