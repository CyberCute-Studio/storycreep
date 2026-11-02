import { type ErrorComponentProps, useRouter } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useErrorConsole } from "../errors/ErrorConsole";
import { Button } from "../ui/Button";
import { useDocumentTitle } from "../ui/title";
import { Shell } from "./Shell";

/**
 * The page of an error that kept a page from being shown, which the error
 * console has recorded: it offers to try again, and the console.
 */
export function RouteError({ reset }: ErrorComponentProps) {
  const { t } = useTranslation();
  const router = useRouter();
  const { show } = useErrorConsole();
  useDocumentTitle(t("route_error.title"));
  return (
    <Shell>
      <div className="mx-auto max-w-2xl space-y-4">
        <h1 className="text-2xl font-semibold">{t("route_error.title")}</h1>
        <p>{t("route_error.body")}</p>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="primary"
            onClick={() => {
              reset();
              void router.invalidate();
            }}
          >
            {t("route_error.retry")}
          </Button>
          <Button onClick={(event) => show(event.currentTarget)}>
            {t("route_error.show_console")}
          </Button>
        </div>
      </div>
    </Shell>
  );
}
