import { ApiError, NetworkError } from "@storycreep/shared/api";
import { onlineManager } from "@tanstack/react-query";
import { describe, expect, it, onTestFinished, vi } from "vitest";
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
      new ApiError(new Response(null, { status }), {
        code: "refused",
        message: "refused",
        request_id: "req",
      });
    const run = (error: ApiError, meta?: { showsRefusals: boolean }) =>
      client
        .getMutationCache()
        .build(client, { mutationFn: () => Promise.reject(error), meta })
        .execute(undefined)
        .catch(() => {});
    await run(answer(422));
    await run(answer(409), { showsRefusals: false });
    await run(answer(500));
    // An answer without the error envelope, as a proxy's page, is no
    // refusal of the server's.
    await run(new ApiError(new Response(null, { status: 413 }), undefined));
    expect(log.entries().map((e) => e.request?.status)).toEqual([
      409, 500, 413,
    ]);
  });

  it("records no answer that a list is not there, which a loader shows as the page of no page, but its other refusals", async () => {
    const log = createErrorLog();
    const client = createQueryClient(log);
    const answer = (status: number, code: string) =>
      new ApiError(new Response(null, { status }), {
        code,
        message: code,
        request_id: "req",
      });
    const read = (error: ApiError) =>
      client
        .fetchQuery({
          queryKey: [error.status],
          queryFn: () => Promise.reject(error),
          meta: { showsNotFound: true },
          retry: false,
        })
        .catch(() => {});
    await read(answer(404, "not_found"));
    await read(answer(400, "invalid_id"));
    await read(answer(403, "insufficient_role"));
    expect(log.entries().map((e) => e.request?.status)).toEqual([403]);
  });

  it("sends a mutation while the browser says that it has no connection, which then fails rather than waits for it", async () => {
    const client = createQueryClient(createErrorLog());
    onlineManager.setOnline(false);
    onTestFinished(() => onlineManager.setOnline(true));
    const send = vi.fn(() =>
      Promise.reject(new NetworkError(new TypeError("Failed to fetch"))),
    );
    await expect(
      client
        .getMutationCache()
        .build(client, { mutationFn: send })
        .execute(undefined),
    ).rejects.toBeInstanceOf(NetworkError);
    expect(send).toHaveBeenCalledOnce();
  });

  it("asks for the session again once an answer tells that it may have changed elsewhere", async () => {
    const log = createErrorLog();
    const checkSession = vi.fn();
    const client = createQueryClient(log, { checkSession });
    const answer = (status: number, code: string) =>
      new ApiError(new Response(null, { status }), {
        code,
        message: code,
        request_id: "req",
      });
    const run = (error: ApiError) =>
      client
        .getMutationCache()
        .build(client, { mutationFn: () => Promise.reject(error) })
        .execute(undefined)
        .catch(() => {});
    // A session that ended, and one that another tab gave a new CSRF token
    // by changing the password.
    await run(answer(401, "unauthenticated"));
    await run(answer(403, "csrf_failed"));
    expect(checkSession).toHaveBeenCalledTimes(2);
    // A wrong password, and a refusal of the account's rights, tell
    // nothing of the session.
    await run(answer(401, "invalid_credentials"));
    await run(answer(403, "insufficient_role"));
    expect(checkSession).toHaveBeenCalledTimes(2);
    expect(log.entries()).toEqual([]);
  });
});
