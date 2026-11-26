import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import {
  api,
  createQueryClient,
  fetchSession,
  requestsIn,
  whenSaved,
} from "./api";
import { createErrorLog } from "./errors/log";
import { createAppI18n } from "./i18n";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
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
    const stop = requestsIn(i18n);
    onTestFinished(stop);
    await api.call("getVersion");
    await i18n.changeLanguage("cs");
    await api.call("getVersion");
    // Once stopped, as after this test, it asks for no locale.
    stop();
    await i18n.changeLanguage("en");
    await api.call("getVersion");
    expect(
      fetch.mock.calls.map(([, init]) =>
        new Headers(init?.headers).get("Accept-Language"),
      ),
    ).toEqual(["en", "cs", null]);
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

/** Returns whether the promise has resolved, once the timers due have run. */
async function settled(promise: Promise<void>): Promise<boolean> {
  let done = false;
  void promise.then(() => {
    done = true;
  });
  await vi.advanceTimersByTimeAsync(0);
  return done;
}

describe("whenSaved", () => {
  it("waits for the mutations under way, for at most the wait, as a reload for a new version does", async () => {
    vi.useFakeTimers();
    const client = createQueryClient(createErrorLog());
    expect(await settled(whenSaved(client, 10_000))).toBe(true);

    // A form's save, which the server has not answered yet.
    let answer = () => {};
    const save = client.getMutationCache().build(client, {
      mutationFn: () => new Promise<void>((resolve) => (answer = resolve)),
    });
    const saving = save.execute(undefined);
    const saved = whenSaved(client, 10_000);
    expect(await settled(saved)).toBe(false);
    answer();
    await saving;
    expect(await settled(saved)).toBe(true);

    const stuck = client.getMutationCache().build(client, {
      mutationFn: () => new Promise<void>(() => {}),
    });
    void stuck.execute(undefined);
    const waited = whenSaved(client, 10_000);
    await vi.advanceTimersByTimeAsync(9_999);
    expect(await settled(waited)).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(await settled(waited)).toBe(true);
  });
});
