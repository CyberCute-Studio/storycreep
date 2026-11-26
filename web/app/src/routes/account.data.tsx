import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { PersonalDataExport } from "../auth/PersonalDataExport";
import { Page } from "../ui/Page";

export const Route = createFileRoute("/account/data")({
  component: YourData,
});

// The account's data (PRD FR-GDPR-01): an export of what the instance holds
// of the user.
function YourData() {
  const { t } = useTranslation();
  return (
    <Page title={t("account.data.title")} width="wide">
      <PersonalDataExport />
    </Page>
  );
}
