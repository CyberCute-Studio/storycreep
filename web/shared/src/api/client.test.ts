import { describe, expect, it } from "vitest";
import { ApiError, createApiClient, createClient } from "./index";
import { type Operations, routes } from "./testdata/fixture.gen";

/** A fetch that records its calls, and answers each with respond. */
function fakeFetch(respond: (url: string, init: RequestInit) => Response) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    return respond(String(input), init ?? {});
  }) as typeof globalThis.fetch;
  return { fetch, calls };
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const baseUrl = "https://storycreep.example.com/";

describe("the API client", () => {
  it("calls an operation without input, and types its answer", async () => {
    const { fetch, calls } = fakeFetch(() =>
      json(200, { version: "(devel)", go_version: "go1.26" }),
    );
    const answer = await createApiClient({ baseUrl, fetch }).call("getVersion");
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("https://storycreep.example.com/version");
    expect(calls[0].init.method).toBe("GET");
    expect(new Headers(calls[0].init.headers).get("Accept")).toBe(
      "application/json",
    );
    expect(answer.status).toBe(200);
    expect(answer.body.go_version).toBe("go1.26");
  });

  it("sends header parameters, and answers 304 without a body", async () => {
    const { fetch, calls } = fakeFetch(
      () => new Response(null, { status: 304, headers: { ETag: '"a"' } }),
    );
    const answer = await createApiClient({ baseUrl, fetch }).call(
      "getOpenAPIDocument",
      { headers: { "If-None-Match": '"a"' } },
    );
    expect(new Headers(calls[0].init.headers).get("If-None-Match")).toBe('"a"');
    expect(answer.status).toBe(304);
    if (answer.status === 304) {
      expect(answer.body).toBeUndefined();
    }
  });

  it("throws the error envelope as an ApiError", async () => {
    const envelope = {
      error: {
        code: "not_ready",
        message: "The server is starting.",
        details: { reason: "starting" },
        request_id: "req-1",
      },
    };
    const { fetch } = fakeFetch(() => json(503, envelope));
    const call = createApiClient({ baseUrl, fetch }).call("getReadiness");
    await expect(call).rejects.toBeInstanceOf(ApiError);
    await expect(call).rejects.toMatchObject({
      status: 503,
      error: envelope.error,
      message: "not_ready: The server is starting.",
    });
  });

  it("throws an answer that is not the envelope as an ApiError without one", async () => {
    const { fetch } = fakeFetch(
      () =>
        new Response("<html>Bad Gateway</html>", {
          status: 502,
          headers: { "Content-Type": "text/html" },
        }),
    );
    await expect(
      createApiClient({ baseUrl, fetch }).call("getHealth"),
    ).rejects.toMatchObject({ status: 502, error: undefined });
  });

  it("calls the page's own origin without a base URL", async () => {
    const { fetch, calls } = fakeFetch(() => json(200, { status: "ok" }));
    await createApiClient({ baseUrl: "", fetch }).call("getHealth");
    expect(calls[0].url).toBe("/healthz");
  });
});

describe("a client of generated operations", () => {
  const client = (respond: (url: string, init: RequestInit) => Response) => {
    const fake = fakeFetch(respond);
    return {
      client: createClient<Operations>(routes, { baseUrl, fetch: fake.fetch }),
      calls: fake.calls,
    };
  };

  it("fills in path parameters, encoded", async () => {
    const { client: c, calls } = client(() =>
      json(200, { id: "a/b", name: "A" }),
    );
    const answer = await c.call("getThing", { path: { id: "a/b ?" } });
    expect(calls[0].url).toBe(
      "https://storycreep.example.com/things/a%2Fb%20%3F",
    );
    expect(answer.status).toBe(200);
    if (answer.status === 200) {
      expect(answer.body.name).toBe("A");
    }
  });

  it("sends the query parameters that have values", async () => {
    const { client: c, calls } = client(() =>
      json(200, { items: [], next_cursor: null }),
    );
    await c.call("listThings", {
      query: { limit: 20, archived: false, cursor: undefined, kind: "note" },
    });
    expect(calls[0].url).toBe(
      "https://storycreep.example.com/things?limit=20&archived=false&kind=note",
    );
    await c.call("listThings");
    expect(calls[1].url).toBe("https://storycreep.example.com/things");
  });

  it("sends the body as JSON", async () => {
    const { client: c, calls } = client(() =>
      json(201, { id: "1", name: "A" }),
    );
    const answer = await c.call("createThing", {
      body: { name: "A", note: null },
    });
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe('{"name":"A","note":null}');
    expect(new Headers(calls[0].init.headers).get("Content-Type")).toBe(
      "application/json",
    );
    expect(answer.status).toBe(201);
  });

  it("answers 204 without a body", async () => {
    const { client: c } = client(() => new Response(null, { status: 204 }));
    const answer = await c.call("deleteThing", { path: { id: "1" } });
    expect(answer).toMatchObject({ status: 204, body: undefined });
  });

  it("refuses a call without its path parameters", async () => {
    const { client: c, calls } = client(() => json(200, {}));
    // @ts-expect-error The input, with the path, is required.
    await expect(c.call("getThing")).rejects.toThrow(TypeError);
    // @ts-expect-error The path needs the id.
    await expect(c.call("getThing", { path: {} })).rejects.toThrow(
      "The path parameter id is missing.",
    );
    expect(calls).toHaveLength(0);
  });

  it("passes the call's options to fetch", async () => {
    const { client: c, calls } = client(() => json(200, {}));
    const abort = new AbortController();
    await c.call(
      "getThing",
      { path: { id: "1" } },
      { signal: abort.signal, headers: { "X-Test": "1" } },
    );
    expect(calls[0].init.signal).toBe(abort.signal);
    expect(new Headers(calls[0].init.headers).get("X-Test")).toBe("1");
  });
});
