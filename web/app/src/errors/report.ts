// The details of the error console's errors as text, which the user copies
// into a report of a problem (PRD NFR-OBS-06): what happened, where, and in
// which browser and server. Times are written in UTC, as ISO 8601, so that a
// report reads the same wherever it is read.

import type { VersionBody } from "@storycreep/shared/api";
import type { TFunction } from "i18next";
import type { ErrorSource, LoggedError } from "./log";

/** What a report says of where the errors happened. */
export interface ReportContext {
  /** When the report was made. */
  time: Date;
  /** The browser, as navigator.userAgent. */
  userAgent: string;
  /** The locale that the UI is shown in. */
  locale: string;
  /** The server's version, if it is known. */
  server?: VersionBody;
}

/** Returns the label of where an error was caught. */
export function sourceLabel(source: ErrorSource, t: TFunction): string {
  switch (source) {
    case "error":
      return t("error_console.source.error");
    case "rejection":
      return t("error_console.source.rejection");
    case "render":
      return t("error_console.source.render");
    case "request":
      return t("error_console.source.request");
  }
}

/**
 * Returns an error's name and message as one line, as Error's toString writes
 * them, and V8 at the start of a stack: without the colon when either is
 * empty, as for new Error().
 */
export function errorHeading(
  error: Pick<LoggedError, "name" | "message">,
): string {
  if (error.message === "") {
    return error.name;
  }
  if (error.name === "") {
    return error.message;
  }
  return `${error.name}: ${error.message}`;
}

/** Returns the details of one error. */
export function formatEntry(entry: LoggedError, t: TFunction): string {
  const lines = [
    t("error_console.report.entry", {
      id: entry.id,
      time: entry.time.toISOString(),
      source: sourceLabel(entry.source, t),
    }),
    t("error_console.report.page", { page: entry.page }),
  ];
  if (entry.count > 1) {
    lines.push(t("error_console.report.repeated", { count: entry.count }));
  }
  if (entry.request !== undefined) {
    lines.push(
      t("error_console.report.request", {
        status: entry.request.status,
        url: entry.request.url,
      }),
    );
    if (entry.request.code !== undefined) {
      lines.push(t("error_console.report.code", { code: entry.request.code }));
    }
    if (entry.request.requestId !== undefined) {
      lines.push(
        t("error_console.report.request_id", { id: entry.request.requestId }),
      );
    }
  }
  const heading = errorHeading(entry);
  // A V8 stack starts with the error's name and message, and another
  // engine's has only the frames.
  if (entry.stack === undefined) {
    lines.push(heading);
  } else if (entry.stack.startsWith(heading)) {
    lines.push(entry.stack);
  } else {
    lines.push(heading, entry.stack);
  }
  if (entry.componentStack !== undefined) {
    lines.push(
      t("error_console.report.components"),
      entry.componentStack.replace(/^\n+/, ""),
    );
  }
  return lines.join("\n");
}

/** Returns a report of entries, with where they happened. */
export function formatReport(
  entries: readonly LoggedError[],
  context: ReportContext,
  t: TFunction,
): string {
  const server = context.server;
  const lines = [
    t("error_console.report.heading"),
    t("error_console.report.time", { time: context.time.toISOString() }),
    t("error_console.report.browser", { agent: context.userAgent }),
    t("error_console.report.locale", { locale: context.locale }),
    server === undefined
      ? t("error_console.report.server_unknown")
      : t("error_console.report.server", { version: server.version }),
  ];
  if (server?.commit !== undefined) {
    lines.push(t("error_console.report.commit", { commit: server.commit }));
  }
  return [lines.join("\n"), ...entries.map((e) => formatEntry(e, t))].join(
    "\n\n",
  );
}
