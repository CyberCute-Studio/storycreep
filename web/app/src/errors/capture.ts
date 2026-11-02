// What the error console records (PRD NFR-OBS-06): an error that nothing
// caught, a promise rejected without a handler, and an error of rendering,
// whether an error boundary caught it or not. Each is recorded in the log
// and nowhere else. The errors of the API's requests are recorded by the
// query client (createQueryClient, in api.ts).

import type { RootOptions } from "react-dom/client";
import type { ErrorLog } from "./log";

/**
 * Records in log the errors that reach target, the page's window, without a
 * handler: those thrown and not caught, as in an event handler, and the
 * promises rejected without a handler. It returns the function that stops.
 */
export function captureGlobalErrors(
  target: Pick<Window, "addEventListener" | "removeEventListener">,
  log: ErrorLog,
): () => void {
  const onError = (event: ErrorEvent) => {
    // A script of another origin's error has no error, only a message.
    log.record("error", event.error ?? event.message);
  };
  const onRejection = (event: PromiseRejectionEvent) => {
    log.record("rejection", event.reason);
  };
  target.addEventListener("error", onError);
  target.addEventListener("unhandledrejection", onRejection);
  return () => {
    target.removeEventListener("error", onError);
    target.removeEventListener("unhandledrejection", onRejection);
  };
}

/**
 * Returns the options of the app's React root that record its errors of
 * rendering in log, with the components that were rendering, but those that
 * ignore says are not errors, as the router's signals that a page was not
 * found, which it throws to its boundaries. Each still goes to the console of
 * the browser's developer tools, as React's own would.
 */
export function rootErrorOptions(
  log: ErrorLog,
  ignore: (error: unknown) => boolean = () => false,
): Pick<
  RootOptions,
  "onUncaughtError" | "onCaughtError" | "onRecoverableError"
> {
  const record = (error: unknown, info: { componentStack?: string }) => {
    // Asking of a thrown value may throw, as a proxy's trap may, which would
    // lose the error: it is recorded then.
    let ignored = false;
    try {
      ignored = ignore(error);
    } catch {}
    if (ignored) {
      return;
    }
    // React gives an error of a view transition that it started null as its
    // components, though its types say a string, which is all that the
    // console can show: the log has none then.
    const components: unknown = info.componentStack;
    log.record("render", error, {
      componentStack: typeof components === "string" ? components : undefined,
    });
    // The developer tools' console is the browser's, on this machine.
    console.error(error);
  };
  return {
    onUncaughtError: record,
    onCaughtError: record,
    onRecoverableError: record,
  };
}
