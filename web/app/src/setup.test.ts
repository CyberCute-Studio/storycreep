import { ApiError } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { createQueryClient } from "./api";
import { createErrorLog } from "./errors/log";
import { createAppI18n } from "./i18n";
import { cspNonce } from "./nonce";

describe("cspNonce", () => {
  it("reads the nonce of the page's meta element", () => {
    const doc = document.implementation.createHTMLDocument();
    expect(cspNonce(doc)).toBeUndefined();
    const meta = doc.createElement("meta");
    meta.setAttribute("property", "csp-nonce");
    meta.setAttribute("nonce", "N0NCE");
    doc.head.append(meta);
    expect(cspNonce(doc)).toBe("N0NCE");
  });
});

describe("createAppI18n", () => {
  it("names the locale of the browser's languages on the page, as it changes", () => {
    const root = document.createElement("html");
    // The catalogs have English alone, which an unknown locale falls back to.
    const i18n = createAppI18n(["xx-YY", "en-GB"], root);
    expect(i18n.language).toBe("en");
    expect(root.lang).toBe("en");
    expect(root.dir).toBe("ltr");
    // As changeLanguage tells, once a catalog of the locale exists.
    i18n.emit("languageChanged", "ar");
    expect(root.lang).toBe("ar");
    expect(root.dir).toBe("rtl");
  });
});

describe("createQueryClient", () => {
  it("records a failed request, and tries again only what the server failed to answer", async () => {
    const log = createErrorLog();
    const client = createQueryClient(log);
    const retry = client.getDefaultOptions().queries?.retry;
    if (typeof retry !== "function") {
      throw new Error("the query client has no retry function");
    }
    const refused = new ApiError(
      new Response(null, { status: 404 }),
      undefined,
    );
    const failed = new ApiError(new Response(null, { status: 502 }), undefined);
    expect(retry(0, refused)).toBe(false);
    expect(retry(0, failed)).toBe(true);
    expect(retry(3, failed)).toBe(false);
    expect(retry(0, new TypeError("network"))).toBe(true);

    await client
      .fetchQuery({
        queryKey: ["x"],
        queryFn: () => Promise.reject(refused),
        retry: false,
      })
      .catch(() => {});
    expect(log.entries().map((e) => [e.source, e.request?.status])).toEqual([
      ["request", 404],
    ]);
  });

  it("records no refusal of a mutation, which the form that made it shows, unless it says it shows none", async () => {
    const log = createErrorLog();
    const client = createQueryClient(log);
    const answer = (status: number) =>
      new ApiError(new Response(null, { status }), undefined);
    const run = (error: ApiError, meta?: { showsRefusals: boolean }) =>
      client
        .getMutationCache()
        .build(client, { mutationFn: () => Promise.reject(error), meta })
        .execute(undefined)
        .catch(() => {});
    await run(answer(422));
    await run(answer(409), { showsRefusals: false });
    await run(answer(500));
    expect(log.entries().map((e) => e.request?.status)).toEqual([409, 500]);
  });
});
