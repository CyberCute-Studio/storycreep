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

  it("sends a file as it is, with its own type", async () => {
    const { fetch, calls } = fakeFetch(
      () => new Response(null, { status: 204 }),
    );
    const picture = new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], {
      type: "image/png",
    });
    const answer = await createClient<Operations>(routes, {
      baseUrl,
      fetch,
    }).call("putThingPicture", { path: { id: "t1" }, body: picture });
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(picture);
    const headers = new Headers(calls[0].init.headers);
    expect(headers.get("Content-Type")).toBe("image/png");
    expect(answer.status).toBe(204);
  });

  it("sends a file without a type as bytes", async () => {
    const { fetch, calls } = fakeFetch(
      () => new Response(null, { status: 204 }),
    );
    await createClient<Operations>(routes, { baseUrl, fetch }).call(
      "putThingPicture",
      { path: { id: "t1" }, body: new Blob(["raw"]) },
    );
    expect(new Headers(calls[0].init.headers).get("Content-Type")).toBe(
      "application/octet-stream",
    );
  });

  it("reads the answer of an operation that answers with a file as a Blob", async () => {
    const { fetch, calls } = fakeFetch(
      () =>
        new Response(new Uint8Array([1, 2, 3]), {
          status: 200,
          headers: { "Content-Type": "image/png" },
        }),
    );
    const answer = await createClient<Operations>(routes, {
      baseUrl,
      fetch,
    }).call("getThingPicture", { path: { id: "t1" } });
    expect(new Headers(calls[0].init.headers).get("Accept")).toBeNull();
    expect(answer.status).toBe(200);
    if (answer.status === 200) {
      expect(answer.body).toBeInstanceOf(Blob);
      expect(answer.body.type).toBe("image/png");
      expect(new Uint8Array(await answer.body.arrayBuffer())).toEqual(
        new Uint8Array([1, 2, 3]),
      );
    }
  });

  it("answers a file's status without a body, as a redirect, with no body", async () => {
    const { fetch } = fakeFetch(
      () =>
        new Response(null, {
          status: 302,
          headers: { Location: "https://bucket.example.com/t1.png" },
        }),
    );
    const answer = await createClient<Operations>(routes, {
      baseUrl,
      fetch,
    }).call("getThingPicture", { path: { id: "t1" } });
    expect(answer.status).toBe(302);
    if (answer.status === 302) {
      expect(answer.body).toBeUndefined();
    }
  });

  it("refuses JSON where it expects a file", async () => {
    const { fetch } = fakeFetch(() => json(200, { not: "a file" }));
    const call = createClient<Operations>(routes, { baseUrl, fetch }).call(
      "getThingPicture",
      { path: { id: "t1" } },
    );
    await expect(call).rejects.toThrow("not a file");
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
    const error = await createApiClient({ baseUrl, fetch })
      .call("getHealth")
      .catch((e: unknown) => e);
    expect(error).toMatchObject({ status: 502, error: undefined });
    expect(await (error as ApiError).response.text()).toBe(
      "<html>Bad Gateway</html>",
    );
  });

  it("leaves a JSON error body that is not the envelope for the caller to read", async () => {
    const { fetch } = fakeFetch(() => json(502, { message: "upstream down" }));
    const error = await createApiClient({ baseUrl, fetch })
      .call("getHealth")
      .catch((e: unknown) => e);
    expect(error).toMatchObject({ status: 502, error: undefined });
    expect(await (error as ApiError).response.json()).toEqual({
      message: "upstream down",
    });
  });

  it("leaves the envelope's body for the caller to read too", async () => {
    const envelope = {
      error: { code: "internal_error", message: "x", request_id: "req-2" },
    };
    const { fetch } = fakeFetch(() => json(500, envelope));
    const error = await createApiClient({ baseUrl, fetch })
      .call("getHealth")
      .catch((e: unknown) => e);
    expect(error).toMatchObject({ status: 500, error: envelope.error });
    expect(await (error as ApiError).response.json()).toEqual(envelope);
  });

  it("refuses a success answer whose body is not JSON", async () => {
    const { fetch } = fakeFetch(
      () =>
        new Response("<!doctype html><title>App</title>", {
          status: 200,
          headers: { "Content-Type": "text/html" },
        }),
    );
    await expect(
      createApiClient({ baseUrl, fetch }).call("getVersion"),
    ).rejects.toThrow(
      "The server answered with the status 200 and a body that is not JSON (Content-Type: text/html).",
    );
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

  it("refuses path parameters that would name another path", async () => {
    const { client: c, calls } = client(() => json(200, {}));
    for (const id of ["", ".", ".."]) {
      await expect(c.call("getThing", { path: { id } })).rejects.toThrow(
        `The path parameter id cannot be ${JSON.stringify(id)}.`,
      );
    }
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
