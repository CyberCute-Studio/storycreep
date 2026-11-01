// The error console (PRD NFR-OBS-06): a dialog, which the header's button
// opens, that lists the errors of the log with their details, which the
// user copies, one or all, into a report of a problem. Nothing of it is ever
// sent anywhere: it reads the log, and the server's version, which the app
// asked for when it started, from the query client's cache.

import { formatDateTime } from "@storycreep/shared/i18n";
import { useQuery } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import {
  createContext,
  type ReactNode,
  useContext,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { versionQuery } from "../api";
import { Button } from "../ui/Button";
import { copyText } from "./copy";
import { type ErrorLog, errorLog, type LoggedError, useErrorLog } from "./log";
import { formatEntry, formatReport, sourceLabel } from "./report";

interface ErrorConsoleContext {
  log: ErrorLog;
  /** Opens the console. */
  show(): void;
}

const Context = createContext<ErrorConsoleContext>({
  log: errorLog,
  show: () => {},
});

/** Returns the error console of the page. */
export function useErrorConsole(): ErrorConsoleContext {
  return useContext(Context);
}

/**
 * Provides the error console of log to children, which the console's button
 * or useErrorConsole opens.
 */
export function ErrorConsoleProvider({
  log = errorLog,
  children,
}: {
  log?: ErrorLog;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const context = useMemo(() => ({ log, show: () => setOpen(true) }), [log]);
  return (
    <Context.Provider value={context}>
      <Dialog.Root open={open} onOpenChange={setOpen}>
        {children}
        <ErrorConsoleDialog />
      </Dialog.Root>
    </Context.Provider>
  );
}

/** The button that opens the console, with the number of its errors. */
export function ErrorConsoleButton() {
  const { t } = useTranslation();
  const { log } = useErrorConsole();
  const count = useErrorLog(log).length;
  // Asked for once, when the app starts, so that a report names the server
  // without a request of its own.
  useQuery(versionQuery);
  return (
    <Dialog.Trigger asChild>
      <Button aria-label={t("error_console.open", { count })}>
        <svg
          aria-hidden="true"
          viewBox="0 0 24 24"
          className="size-5"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M12 3 2 21h20L12 3z" />
          <path d="M12 10v5M12 18h.01" />
        </svg>
        <span className="hidden sm:inline">{t("error_console.button")}</span>
        {count > 0 && (
          <span className="min-w-6 rounded-full bg-danger px-1.5 text-center text-sm text-danger-fg">
            {count}
          </span>
        )}
      </Button>
    </Dialog.Trigger>
  );
}

function ErrorConsoleDialog() {
  const { t } = useTranslation();
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 bg-backdrop/50" />
      <Dialog.Content className="fixed inset-x-2 top-4 bottom-4 mx-auto flex max-w-3xl flex-col gap-4 overflow-hidden rounded-lg border border-border bg-canvas p-4 text-fg shadow-xl sm:inset-x-8 sm:top-12 sm:bottom-12">
        <div className="flex items-start gap-4">
          <div className="flex-1">
            <Dialog.Title className="text-xl font-semibold">
              {t("error_console.title")}
            </Dialog.Title>
            <Dialog.Description className="text-fg-muted">
              {t("error_console.description")}
            </Dialog.Description>
          </div>
          <Dialog.Close asChild>
            <Button>{t("error_console.close")}</Button>
          </Dialog.Close>
        </div>
        <ErrorConsoleBody />
      </Dialog.Content>
    </Dialog.Portal>
  );
}

/**
 * The console's errors, with the buttons that copy their details and clear
 * them: the dialog's content, and what the page shows instead of the app
 * when the app could not be shown at all.
 */
export function ErrorConsoleBody() {
  const { t, i18n } = useTranslation();
  const { log } = useErrorConsole();
  const logged = useErrorLog(log);
  // Newest first, as the list shows them and a report of all of them holds
  // them.
  const entries = useMemo(() => [...logged].reverse(), [logged]);
  const empty = entries.length === 0;
  const { data: server } = useQuery({ ...versionQuery, enabled: false });
  const [status, setStatus] = useState("");
  const body = useRef<HTMLDivElement>(null);

  const copy = async (selected: readonly LoggedError[]) => {
    const report = formatReport(
      selected,
      {
        time: new Date(),
        userAgent: navigator.userAgent,
        locale: i18n.language,
        server,
      },
      t,
    );
    const container = body.current ?? document.body;
    setStatus(
      (await copyText(report, container))
        ? t("error_console.copied")
        : t("error_console.copy_failed"),
    );
  };

  return (
    <div ref={body} className="flex min-h-0 flex-1 flex-col gap-4">
      {empty ? (
        <p className="text-fg-muted">{t("error_console.empty")}</p>
      ) : (
        <ol
          aria-label={t("error_console.list")}
          className="min-h-0 flex-1 space-y-2 overflow-y-auto"
        >
          {entries.map((entry) => (
            <li
              key={entry.id}
              className="rounded-md border border-border bg-surface"
            >
              <details>
                <summary className="flex min-h-11 cursor-pointer flex-wrap items-center gap-x-3 px-3 py-2">
                  <span className="text-fg-muted">
                    {formatDateTime(
                      i18n.language,
                      entry.time,
                      "short",
                      "medium",
                    )}
                  </span>
                  <span className="text-fg-muted">
                    {sourceLabel(entry.source, t)}
                  </span>
                  <span className="min-w-0 break-words font-medium text-danger">
                    {entry.name}: {entry.message}
                  </span>
                  {entry.count > 1 && (
                    <span className="text-fg-muted">
                      {t("error_console.repeated", { count: entry.count })}
                    </span>
                  )}
                </summary>
                <div className="flex flex-col items-start gap-2 border-t border-border p-3">
                  {/* The error's own details, which a copy of them gives
                      with the report's heading, made as it is copied. */}
                  <pre className="max-w-full overflow-x-auto font-mono text-sm whitespace-pre-wrap break-words">
                    {formatEntry(entry, t)}
                  </pre>
                  <Button onClick={() => copy([entry])}>
                    {t("error_console.copy_one")}
                  </Button>
                </div>
              </details>
            </li>
          ))}
        </ol>
      )}
      {/* Without errors, the buttons say that they are unavailable but keep
          the keyboard's focus, which a disabled button would drop, as Clear
          would once it has cleared the errors. */}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          aria-disabled={empty}
          onClick={() => {
            if (!empty) {
              void copy(entries);
            }
          }}
        >
          {t("error_console.copy_all")}
        </Button>
        <Button
          aria-disabled={empty}
          onClick={() => {
            if (!empty) {
              log.clear();
              setStatus(t("error_console.cleared"));
            }
          }}
        >
          {t("error_console.clear")}
        </Button>
        <p role="status" className="text-fg-muted">
          {status}
        </p>
      </div>
    </div>
  );
}
