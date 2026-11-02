import { afterEach, describe, expect, it, vi } from "vitest";
import { api, requestsIn } from "./api";
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
