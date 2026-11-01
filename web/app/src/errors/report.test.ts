import { createI18n } from "@storycreep/shared/i18n";
import { expect, it } from "vitest";
import type { LoggedError } from "./log";
import { errorHeading, formatEntry, formatReport } from "./report";

const { t } = createI18n("en");

const entry: LoggedError = {
  id: 3,
  source: "request",
  name: "ApiError",
  message: "internal_error: Something failed.",
  stack:
    "ApiError: internal_error: Something failed.\n    at call (client.ts:1:1)",
  time: new Date("2026-11-01T12:00:00Z"),
  page: "http://x/app/projects?a=1&b=2",
  count: 2,
  request: {
    url: "http://x/api/v1/x?a=1&b=2",
    status: 500,
    code: "internal_error",
    requestId: "req_9",
  },
};

it("writes an error's details, with its request, as text", () => {
  expect(formatEntry(entry, t)).toBe(
    [
      "Error 3, at 2026-11-01T12:00:00.000Z: Request",
      "Page: http://x/app/projects?a=1&b=2",
      "Happened 2 times in a row",
      "Request: http://x/api/v1/x?a=1&b=2, answered with 500",
      "Error code: internal_error",
      "Request ID: req_9",
      "ApiError: internal_error: Something failed.\n    at call (client.ts:1:1)",
    ].join("\n"),
  );
});

it("names an error whose stack does not, and the components that were rendering", () => {
  const text = formatEntry(
    {
      ...entry,
      source: "render",
      count: 1,
      request: undefined,
      name: "TypeError",
      message: "x is undefined",
      stack: "render@app.js:1:2",
      componentStack: "\n    at Board\n    at Shell",
    },
    t,
  );
  expect(text.split("\n").slice(2)).toEqual([
    "TypeError: x is undefined",
    "render@app.js:1:2",
    "Components:",
    "    at Board",
    "    at Shell",
  ]);
});

it("names an error without a message once, as its stack does", () => {
  const text = formatEntry(
    {
      ...entry,
      count: 1,
      request: undefined,
      name: "Error",
      message: "",
      stack: "Error\n    at f (app.js:1:2)",
    },
    t,
  );
  expect(text.split("\n").slice(2)).toEqual(["Error", "    at f (app.js:1:2)"]);
  expect(errorHeading({ name: "", message: "x" })).toBe("x");
  expect(errorHeading({ name: "TypeError", message: "x" })).toBe(
    "TypeError: x",
  );
});

it("heads a report with where the errors happened", () => {
  const context = {
    time: new Date("2026-11-01T13:00:00Z"),
    userAgent: "Agent/1",
    locale: "en",
  };
  const server = { version: "1.2.0", commit: "abc", go_version: "go1.26" };
  expect(formatReport([], { ...context, server }, t)).toBe(
    [
      "Storycreep error report",
      "Report made at: 2026-11-01T13:00:00.000Z",
      "Browser: Agent/1",
      "Language: en",
      "Server: 1.2.0",
      "Commit: abc",
    ].join("\n"),
  );
  const report = formatReport([entry, { ...entry, id: 4 }], context, t);
  expect(report).toContain("Server: unknown\n\nError 3,");
  expect(report).toContain("(client.ts:1:1)\n\nError 4,");
});
