import { ApiError } from "@storycreep/shared/api";
import { describe, expect, it, vi } from "vitest";
import { createErrorLog, describeError } from "./log";

const at = new Date("2026-11-01T12:00:00Z");

function log(limit?: number) {
  return createErrorLog({ limit, now: () => at, page: () => "http://x/app/" });
}

describe("createErrorLog", () => {
  it("records errors in order, with where and when they happened", () => {
    const l = log();
    l.record("error", new TypeError("a"));
    l.record("rejection", "b");
    expect(l.entries()).toMatchObject([
      {
        id: 1,
        source: "error",
        name: "TypeError",
        message: "a",
        time: at,
        page: "http://x/app/",
        count: 1,
      },
      { id: 2, source: "rejection", name: "Error", message: "b", count: 1 },
    ]);
  });

  it("counts an error that happens again in a row as one entry", () => {
    const l = log();
    const error = new Error("loop");
    l.record("render", error, { componentStack: "\n    at A" });
    l.record("render", error, { componentStack: "\n    at A" });
    l.record("render", error, { componentStack: "\n    at B" });
    expect(l.entries().map((e) => [e.id, e.count])).toEqual([
      [1, 2],
      [2, 1],
    ]);
  });

  it("keeps the newest errors up to its limit", () => {
    const l = log(3);
    for (const message of ["1", "2", "3", "4"]) {
      l.record("error", new Error(message));
    }
    expect(l.entries().map((e) => e.message)).toEqual(["2", "3", "4"]);
  });

  it("tells its subscribers of each change until they leave", () => {
    const l = log();
    const listener = vi.fn();
    const stop = l.subscribe(listener);
    l.record("error", new Error("a"));
    const before = l.entries();
    l.clear();
    expect(l.entries()).toEqual([]);
    expect(before).toHaveLength(1);
    stop();
    l.record("error", new Error("b"));
    expect(listener).toHaveBeenCalledTimes(2);
  });
});

describe("describeError", () => {
  it("reads the request of an error that the server answered with", () => {
    const response = new Response(null, { status: 503 });
    Object.defineProperty(response, "url", { value: "http://x/version" });
    const error = new ApiError(response, {
      code: "not_ready",
      message: "The server is starting.",
      request_id: "req_1",
    });
    expect(describeError(error)).toMatchObject({
      name: "ApiError",
      message: "not_ready: The server is starting.",
      request: {
        url: "http://x/version",
        status: 503,
        code: "not_ready",
        requestId: "req_1",
      },
    });
  });

  it("describes a thrown value that is not an error", () => {
    expect(describeError({ a: 1 })).toEqual({
      name: "object",
      message: '{"a":1}',
    });
    expect(describeError(undefined)).toEqual({
      name: "undefined",
      message: "undefined",
    });
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    expect(describeError(cyclic)).toEqual({
      name: "object",
      message: "[object Object]",
    });
  });
});
