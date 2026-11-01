import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { PRODUCT_NAME } from "../product";
import { useDocumentTitle } from "../ui/title";

export const Route = createFileRoute("/")({
  component: Home,
});

// The start page, which the sign-in screen replaces (plan item 54).
function Home() {
  const { t } = useTranslation();
  useDocumentTitle();
  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <h1 className="text-2xl font-semibold">{PRODUCT_NAME}</h1>
      <p>{t("home.body")}</p>
    </div>
  );
}
