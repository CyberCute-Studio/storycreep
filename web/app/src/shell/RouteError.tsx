import { NetworkError } from "@storycreep/shared/api";
import { type ErrorComponentProps, useRouter } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useErrorConsole } from "../errors/ErrorConsole";
import { NeedsConnection } from "../offline/OfflinePages";
import { useOnline } from "../offline/online";
import { Button } from "../ui/Button";
import { useDocumentTitle } from "../ui/title";
import { Shell } from "./Shell";

/**
 * The page of an error that kept a page from being shown, which the error
 * console has recorded: it offers to try again, and the console. A request
 * that got no answer while the browser has no connection, as of a page that
 * reads the server alone (offlineRoutes), or of a project that the device
 * does not keep, is no error of the app's: the page says that it needs a
 * connection, and is tried again once the browser is online again (PRD
 * FR-OFF-02).
 */
export function RouteError({ error, reset }: ErrorComponentProps) {
  const { t } = useTranslation();
  const router = useRouter();
  const { show } = useErrorConsole();
  const connected = useOnline();
  const unanswered = error instanceof NetworkError;
  const [wentOffline, setWentOffline] = useState(!connected);
  if (!connected && !wentOffline) {
    setWentOffline(true);
  }
  useEffect(() => {
    if (connected && unanswered && wentOffline) {
      setWentOffline(false);
      reset();
      void router.invalidate();
    }
  }, [connected, unanswered, wentOffline, reset, router]);
  const needsConnection = unanswered && !connected;
  useDocumentTitle(
    needsConnection
      ? t("offline.needs_connection.title")
      : t("route_error.title"),
  );
  if (needsConnection) {
    return (
      <Shell>
        <div className="mx-auto max-w-2xl">
          <NeedsConnection />
        </div>
      </Shell>
    );
  }
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
