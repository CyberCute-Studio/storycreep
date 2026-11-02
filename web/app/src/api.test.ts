import { afterEach, describe, expect, it, vi } from "vitest";
import { api, fetchSession, requestsIn } from "./api";
import { createAppI18n } from "./i18n";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("requestsIn", () => {
  it("asks the server for its messages in the page's locale, as it changes", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(
        async () =>
          new Response(
            JSON.stringify({ version: "(devel)", go_version: "go" }),
            { headers: { "Content-Type": "application/json" } },
          ),
      );
    const i18n = createAppI18n(["en-US"], document.documentElement, {
      en: { "x.y": "x" },
      cs: { "x.y": "x" },
    });
    requestsIn(i18n);
    await api.call("getVersion");
    await i18n.changeLanguage("cs");
    await api.call("getVersion");
    expect(
      fetch.mock.calls.map(([, init]) =>
        new Headers(init?.headers).get("Accept-Language"),
      ),
    ).toEqual(["en", "cs"]);
  });
});

describe("fetchSession", () => {
  it("takes the server's 401 for a signed-out page, and no other", async () => {
    const answer = (body: string, type: string) =>
      new Response(body, { status: 401, headers: { "Content-Type": type } });
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        answer(
          JSON.stringify({
            error: {
              code: "unauthenticated",
              message: "Sign in.",
              request_id: "req",
            },
          }),
          "application/json",
        ),
      )
      // A proxy's page, which asks for its own credentials.
      .mockResolvedValueOnce(answer("<h1>Sign in</h1>", "text/html"));
    expect(await fetchSession()).toBeNull();
    await expect(fetchSession()).rejects.toMatchObject({
      name: "ApiError",
      status: 401,
      error: undefined,
    });
  });
});
