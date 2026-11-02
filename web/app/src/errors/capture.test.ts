import { afterEach, describe, expect, it, vi } from "vitest";
import { captureGlobalErrors, rootErrorOptions } from "./capture";
import { createErrorLog } from "./log";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("captureGlobalErrors", () => {
  it("records the errors and rejections that nothing handled, until it stops", () => {
    const log = createErrorLog();
    // A window of its own, since Vitest fails a test on an error of the
    // page's window that nothing handled.
    const target = new EventTarget() as Window;
    const stop = captureGlobalErrors(target, log);
    target.dispatchEvent(
      new ErrorEvent("error", {
        error: new RangeError("thrown"),
        message: "Uncaught RangeError: thrown",
      }),
    );
    // A script of another origin's error has only a message.
    target.dispatchEvent(new ErrorEvent("error", { message: "Script error." }));
    target.dispatchEvent(
      new PromiseRejectionEvent("unhandledrejection", {
        promise: Promise.resolve(),
        reason: new Error("rejected"),
      }),
    );
    stop();
    target.dispatchEvent(
      new ErrorEvent("error", { error: new Error("after") }),
    );
    expect(log.entries().map((e) => [e.source, e.name, e.message])).toEqual([
      ["error", "RangeError", "thrown"],
      ["error", "Error", "Script error."],
      ["rejection", "Error", "rejected"],
    ]);
  });
});

describe("rootErrorOptions", () => {
  it("records the errors of rendering with their components, but those it ignores", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const log = createErrorLog();
    const options = rootErrorOptions(log, (e) => e === "ignored");
    options.onCaughtError?.(new Error("caught"), {
      componentStack: "\n    at Page",
      errorBoundary: undefined,
    });
    options.onUncaughtError?.(new Error("uncaught"), {
      componentStack: "\n    at Root",
    });
    options.onRecoverableError?.(new Error("recovered"), {
      componentStack: "",
    });
    // As React 19 reports an error of a view transition, against its types.
    options.onRecoverableError?.(new Error("transition"), {
      componentStack: null as unknown as string,
    });
    options.onCaughtError?.("ignored", {
      componentStack: "",
      errorBoundary: undefined,
    });
    expect(
      log.entries().map((e) => [e.source, e.message, e.componentStack]),
    ).toEqual([
      ["render", "caught", "\n    at Page"],
      ["render", "uncaught", "\n    at Root"],
      ["render", "recovered", ""],
      ["render", "transition", undefined],
    ]);
  });
});
