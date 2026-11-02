import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useDocumentTitle } from "../ui/title";
import { InShell } from "./Shell";

/**
 * The page of a URL of the app that no route matches, or that a route says
 * it has no page of, with notFound(). The router shows it in the root
 * route's Outlet, within the Shell, but for a notFound() that a component
 * throws as it renders, which the root route's boundary catches in place of
 * the Shell, so it frames itself with the Shell when nothing else does.
 */
export function NotFound() {
  const { t } = useTranslation();
  useDocumentTitle(t("not_found.title"));
  return (
    <InShell>
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
    </InShell>
  );
}
