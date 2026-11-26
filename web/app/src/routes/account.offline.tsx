import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { StoragePanel } from "../offline/StoragePanel";
import { Page } from "../ui/Page";

export const Route = createFileRoute("/account/offline")({
  component: OfflineStorage,
});

// The storage panel of the projects that the device keeps offline for the
// account (PRD FR-OFF-02).
function OfflineStorage() {
  const { t } = useTranslation();
  return (
    <Page title={t("account.offline.title")} width="wide">
      <StoragePanel />
    </Page>
  );
}
