// The log of the error console (PRD NFR-OBS-06): the errors that happened in
// this tab, which the console lists with details that the user can copy into
// a report. It keeps them in memory alone, and nothing reads it to send them
// anywhere: errors are never transmitted.

import { ApiError } from "@storycreep/shared/api";
import { useSyncExternalStore } from "react";

/** Where an error was caught. */
export type ErrorSource = "error" | "rejection" | "render" | "request";

/** The request of an error that the server answered with. */
export interface RequestDetails {
  /** The URL that the request was made to. */
  url: string;
  status: number;
  /** The error code of the error envelope, if the answer has one. */
  code?: string;
  /** The ID that the server logged the request under. */
  requestId?: string;
}

/** What is known of an error. */
export interface ErrorDetails {
  name: string;
  message: string;
  stack?: string;
  /** The components that were rendering, for an error of rendering. */
  componentStack?: string;
  request?: RequestDetails;
}

/** An error of the log. */
export interface LoggedError extends ErrorDetails {
  /** The entry's number, from 1, in the order recorded. */
  id: number;
  source: ErrorSource;
  /** When it last happened. */
  time: Date;
  /** The page that was open, as its URL. */
  page: string;
  /** How many times in a row it happened. */
  count: number;
}

export interface ErrorLog {
  /** Records an error, which may be anything that was thrown. */
  record(
    source: ErrorSource,
    error: unknown,
    extra?: Partial<ErrorDetails>,
  ): void;
  /** The errors, oldest first. */
  entries(): readonly LoggedError[];
  clear(): void;
  /** Calls listener after each change, until the returned function is called. */
  subscribe(listener: () => void): () => void;
}

export interface ErrorLogOptions {
  /** How many errors the log keeps; the oldest goes first. */
  limit?: number;
  now?: () => Date;
  /** The URL of the page that is open. */
  page?: () => string;
}

/** The number of errors that the log keeps by default. */
export const DEFAULT_LIMIT = 100;

/** Returns an empty log. */
export function createErrorLog(options: ErrorLogOptions = {}): ErrorLog {
  const limit = options.limit ?? DEFAULT_LIMIT;
  const now = options.now ?? (() => new Date());
  const page = options.page ?? (() => globalThis.location?.href ?? "");
  let entries: readonly LoggedError[] = [];
  let next = 1;
  const listeners = new Set<() => void>();
  const changed = () => {
    for (const listener of listeners) {
      listener();
    }
  };
  return {
    record(source, error, extra) {
      const details = { ...describeError(error), ...extra };
      const entry: LoggedError = {
        ...details,
        id: next,
        source,
        time: now(),
        page: page(),
        count: 1,
      };
      const last = entries.at(-1);
      if (last !== undefined && same(last, entry)) {
        // An error that happens again and again, as in a loop, is one entry
        // rather than a flood that pushes the others out.
        entries = [
          ...entries.slice(0, -1),
          { ...last, time: entry.time, count: last.count + 1 },
        ];
      } else {
        next++;
        entries = [...entries, entry].slice(-limit);
      }
      changed();
    },
    entries: () => entries,
    clear() {
      entries = [];
      changed();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

/** Reports whether two entries are the same error on the same page. */
function same(a: LoggedError, b: LoggedError): boolean {
  return (
    a.source === b.source &&
    a.name === b.name &&
    a.message === b.message &&
    a.stack === b.stack &&
    a.componentStack === b.componentStack &&
    a.page === b.page &&
    a.request?.url === b.request?.url &&
    a.request?.status === b.request?.status &&
    a.request?.code === b.request?.code &&
    a.request?.requestId === b.request?.requestId
  );
}

/**
 * Returns what is known of a thrown value, which may be anything, as text: an
 * error's name, message, or stack may be any value too, as a library's
 * subclass may set it, which the console could not show.
 */
export function describeError(error: unknown): ErrorDetails {
  if (error instanceof Error) {
    const details: ErrorDetails = {
      name: text(error.name),
      message: text(error.message),
    };
    if (typeof error.stack === "string") {
      details.stack = error.stack;
    }
    if (error instanceof ApiError) {
      details.request = {
        url: error.response.url,
        status: error.status,
        code: error.error?.code,
        requestId: error.error?.request_id,
      };
    }
    return details;
  }
  if (typeof error === "string") {
    return { name: "Error", message: error };
  }
  let message: string;
  try {
    message = JSON.stringify(error) ?? text(error);
  } catch {
    message = text(error);
  }
  return { name: typeof error, message };
}

/**
 * Returns a value as text, as String does, or "" for one that String cannot
 * write, as an object without a prototype, or whose toString throws.
 */
function text(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  try {
    return String(value);
  } catch {
    return "";
  }
}

/** The log of this tab. */
export const errorLog: ErrorLog = createErrorLog();

/** Returns the errors of a log, and renders again when it changes. */
export function useErrorLog(log: ErrorLog = errorLog): readonly LoggedError[] {
  return useSyncExternalStore(log.subscribe, log.entries, log.entries);
}
