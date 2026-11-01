import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useDocumentTitle } from "../ui/title";

/** The page of a URL of the app that no route matches. */
export function NotFound() {
  const { t } = useTranslation();
  useDocumentTitle(t("not_found.title"));
  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <h1 className="text-2xl font-semibold">{t("not_found.title")}</h1>
      <p>{t("not_found.body")}</p>
      <p>
        <Link
          to="/"
          className="text-accent underline underline-offset-2 hover:text-accent-hover"
        >
          {t("not_found.home")}
        </Link>
      </p>
    </div>
  );
}
