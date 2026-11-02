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

  it("records the page without its query or fragment, which may hold a link's secret", () => {
    const l = createErrorLog({ now: () => at });
    const before = location.href;
    history.pushState(null, "", "/app/reset-password?next=1#token=secret");
    try {
      l.record("error", new Error("a"));
    } finally {
      history.pushState(null, "", before);
    }
    expect(l.entries()[0]?.page).toBe(`${location.origin}/app/reset-password`);
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

  it("counts a request that fails again in a row as one entry, with the last request's ID", () => {
    const l = log();
    const response = new Response(null, { status: 503 });
    Object.defineProperty(response, "url", { value: "http://x/version" });
    // The same request, which fails the same way each time, as a query
    // asked for again does, but which the server logs under another ID.
    for (const id of ["req_1", "req_2", "req_3"]) {
      l.record(
        "request",
        new ApiError(response, {
          code: "not_ready",
          message: "The server is starting.",
          request_id: id,
        }),
      );
    }
    expect(
      l.entries().map((e) => [e.id, e.count, e.request?.requestId]),
    ).toEqual([[1, 3, "req_3"]]);
  });

  it("keeps the newest errors up to its limit", () => {
    const l = log(3);
    for (const message of ["1", "2", "3", "4"]) {
      l.record("error", new Error(message));
    }
    expect(l.entries().map((e) => e.message)).toEqual(["2", "3", "4"]);
    const none = log(0);
    none.record("error", new Error("1"));
    expect(none.entries()).toEqual([]);
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

  it("reads a request's URL without its query or fragment, which may hold a secret", () => {
    // A file's read that an S3 bucket refused, after the server's redirect
    // to a URL of the bucket that it signed.
    const response = new Response(null, { status: 403 });
    Object.defineProperty(response, "url", {
      value: "https://bucket.example/w/a.png?X-Amz-Signature=secret#top",
    });
    expect(describeError(new ApiError(response, undefined)).request?.url).toBe(
      "https://bucket.example/w/a.png",
    );
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
    // Neither JSON.stringify nor String can write it.
    const bare = Object.create(null) as Record<string, unknown>;
    bare.self = bare;
    expect(describeError(bare)).toEqual({ name: "object", message: "" });
  });

  it("describes an error whose name, message, or stack is not text as text", () => {
    const error = new Error("x");
    Object.assign(error, { name: 42, message: { a: 1 }, stack: ["at a"] });
    expect(describeError(error)).toEqual({
      name: "42",
      message: "[object Object]",
    });
  });

  it("describes an error whose name, message, or stack throws as it is read, or that is a proxy that throws", () => {
    const fail = () => {
      throw new Error("getter");
    };
    const error = new TypeError("x");
    Object.defineProperties(error, {
      message: { get: fail },
      stack: { get: fail },
    });
    expect(describeError(error)).toEqual({ name: "TypeError", message: "" });
    const proxy = new Proxy(new Error("y"), { get: fail });
    expect(describeError(proxy)).toEqual({ name: "", message: "" });
    // Asking what it is an instance of throws too.
    const opaque = new Proxy({}, { getPrototypeOf: fail, get: fail });
    expect(describeError(opaque)).toEqual({ name: "object", message: "" });
    const log = createErrorLog();
    log.record("error", opaque);
    expect(log.entries()).toHaveLength(1);
  });
});
