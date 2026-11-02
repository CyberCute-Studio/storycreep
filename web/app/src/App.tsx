// The app: its providers of the locale, the server's state, and the error
// console around its router.

import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import type { i18n as I18n } from "i18next";
import { Component, type ReactNode } from "react";
import { I18nextProvider, useTranslation } from "react-i18next";
import { ErrorConsoleBody, ErrorConsoleProvider } from "./errors/ErrorConsole";
import type { ErrorLog } from "./errors/log";
import type { createAppRouter } from "./router";
import { Button } from "./ui/Button";
import { useDocumentTitle } from "./ui/title";

export interface AppProps {
  i18n: I18n;
  queryClient: QueryClient;
  router: ReturnType<typeof createAppRouter>;
  log: ErrorLog;
}

export function App({ i18n, queryClient, router, log }: AppProps) {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <ErrorConsoleProvider log={log}>
          <CrashBoundary>
            <RouterProvider router={router} />
          </CrashBoundary>
        </ErrorConsoleProvider>
      </QueryClientProvider>
    </I18nextProvider>
  );
}

/**
 * Shows the error console in place of the app once an error has kept the
 * router from rendering, as one that its error page could not show, since
 * that page failed too (createAppRouter turns the router's own boundary
 * off); its pages' errors show that page instead. The React root has
 * recorded it.
 */
class CrashBoundary extends Component<
  { children: ReactNode },
  { crashed: boolean }
> {
  state = { crashed: false };

  static getDerivedStateFromError() {
    return { crashed: true };
  }

  render() {
    return this.state.crashed ? <Crashed /> : this.props.children;
  }
}

function Crashed() {
  const { t } = useTranslation();
  useDocumentTitle(t("crash.title"));
  return (
    <main className="mx-auto flex min-h-dvh max-w-3xl flex-col gap-4 px-4 py-8">
      <h1 className="text-2xl font-semibold">{t("crash.title")}</h1>
      <p>{t("crash.body")}</p>
      <div>
        <Button variant="primary" onClick={() => window.location.reload()}>
          {t("crash.reload")}
        </Button>
      </div>
      <ErrorConsoleBody />
    </main>
  );
}
