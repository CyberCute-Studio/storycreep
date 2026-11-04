import { ApiError, type ErrorBody, NetworkError } from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  accountQuery,
  api,
  instanceQuery,
  sessionQuery,
  versionQuery,
} from "../api";
import { testAccount, testInstance, testSession } from "../test/app";
import { bodyField, passwordMismatch, signInRegistered } from "./forms";
import { fragmentToken } from "./fragment";
import { refusalOf } from "./refusal";
import {
  appPath,
  endSession,
  followSession,
  loadSessionState,
  signedIn,
  signInFor,
  signInReturn,
  signInSearch,
} from "./session";

const t = createI18n("en").t;

/** Returns the ApiError of an answer of status with the error envelope of error. */
function refused(
  status: number,
  error: Partial<ErrorBody> & { code: string },
  headers: Record<string, string> = {},
): ApiError {
  const body = {
    message: `${error.code} message`,
    request_id: "req",
    ...error,
  };
  return new ApiError(
    new Response(JSON.stringify({ error: body }), {
      status,
      headers: { "Content-Type": "application/json", ...headers },
    }),
    body,
  );
}

describe("refusalOf", () => {
  it("says what the server refused, at the form's fields where it names them", () => {
    expect(refusalOf(refused(401, { code: "invalid_credentials" }), t)).toEqual(
      {
        message: "The email address or the password is wrong.",
        fields: {},
      },
    );
    expect(
      refusalOf(
        refused(
          429,
          { code: "rate_limited", details: { reason: "locked" } },
          {
            "Retry-After": "30",
          },
        ),
        t,
      ).message,
    ).toBe(
      "Signing in with this email address failed too often. Try again in 30 seconds.",
    );
    expect(
      refusalOf(
        refused(429, { code: "rate_limited", details: { reason: "rate" } }),
        t,
      ).message,
    ).toBe("Too many requests. Try again in a moment.");

    const fields = bodyField("email", "password");
    expect(
      refusalOf(
        refused(422, {
          code: "weak_password",
          details: {
            reason: "too_short",
            errors: [{ location: "body.password", message: "is too short" }],
          },
        }),
        t,
        fields,
      ),
    ).toEqual({ fields: { password: "Use at least 12 characters." } });
    expect(
      refusalOf(
        refused(422, {
          code: "validation_failed",
          details: {
            errors: [
              { location: "body.email", message: "is not an email address" },
              { location: "body.other", message: "is wrong" },
            ],
          },
        }),
        t,
        fields,
      ),
    ).toEqual({
      message: "Is wrong.",
      fields: { email: "Is not an email address." },
    });
    // A code that names no field puts its own refusal at the field that the
    // form has for it.
    expect(
      refusalOf(refused(403, { code: "invalid_setup_code" }), t, (location) =>
        location === "body.setup_code" ? "setup_code" : undefined,
      ),
    ).toEqual({
      fields: {
        setup_code:
          "This is not the setup code of this instance. Copy it from the server's log.",
      },
    });
    // A code that the app does not know is shown in the server's words.
    expect(refusalOf(refused(403, { code: "owner_required" }), t).message).toBe(
      "owner_required message",
    );
  });

  it("says when the server could not be reached or could not answer", () => {
    expect(
      refusalOf(new NetworkError(new TypeError("Failed to fetch")), t).message,
    ).toBe(
      "The server could not be reached. Check your connection, and try again.",
    );
    // A fault of the app's own is no network's.
    expect(
      refusalOf(new TypeError("The path parameter id is missing."), t).message,
    ).toBe(t("error.unexpected"));
    expect(
      refusalOf(
        new ApiError(new Response("<html>", { status: 502 }), undefined),
        t,
      ).message,
    ).toBe("The server could not answer. Try again in a moment.");
    expect(refusalOf(refused(500, { code: "internal_error" }), t).message).toBe(
      "The server could not answer. Try again in a moment.",
    );
    // A proxy's page of a refusal, as of a body too large, is no refusal
    // of the server's, which trying again would mend.
    expect(
      refusalOf(
        new ApiError(new Response("<html>", { status: 413 }), undefined),
        t,
      ).message,
    ).toBe(t("error.unexpected"));
  });

  // Verifies: FR-ENT-03/AC2
  it("names the page that offers the plans only where the server's refusal of a limit names it", () => {
    const limit = (details: Record<string, unknown>) =>
      refusalOf(
        refused(409, {
          code: "limit_exceeded",
          details: { key: "projects", usage: 3, limit: 3, ...details },
        }),
        t,
      );
    // A self-hosted instance names none, so the form offers no upgrade.
    expect(limit({})).toEqual({
      message: "The workspace has as many projects as it may have: 3 of 3.",
      fields: {},
    });
    expect(
      limit({ upgrade_url: "https://storycreep.example/pricing" }).upgrade,
    ).toBe("https://storycreep.example/pricing");
    expect(limit({ upgrade_url: "/pricing" }).upgrade).toBe(
      `${window.location.origin}/pricing`,
    );
    // Nothing but a page that the browser opens, never a script.
    expect(
      limit({ upgrade_url: "javascript:alert(1)" }).upgrade,
    ).toBeUndefined();
    expect(limit({ upgrade_url: 3 }).upgrade).toBeUndefined();
    // Only a refusal of a limit names one.
    expect(
      refusalOf(
        refused(409, {
          code: "invite_exists",
          details: { upgrade_url: "https://storycreep.example/pricing" },
        }),
        t,
      ).upgrade,
    ).toBeUndefined();
  });

  it("says a code in the form's own words where it gives them, at its field or for the whole form", () => {
    const own = { slug_taken: "Another board has this slug." };
    expect(
      refusalOf(
        refused(409, {
          code: "slug_taken",
          details: {
            errors: [
              {
                location: "body.commands[0].payload.slug",
                message: "is taken",
              },
            ],
          },
        }),
        t,
        (location) => (location.endsWith(".slug") ? "slug" : undefined),
        own,
      ),
    ).toEqual({ fields: { slug: "Another board has this slug." } });
    // A code without a location is the form's message.
    expect(
      refusalOf(refused(409, { code: "already_member" }), t, undefined, {
        already_member: "This address belongs to a member already.",
      }),
    ).toEqual({
      message: "This address belongs to a member already.",
      fields: {},
    });
  });
});

describe("appPath", () => {
  it("keeps a redirect within the app", () => {
    expect(appPath("/account/tokens?x=1#y")).toBe("/account/tokens?x=1#y");
    for (const path of [
      "https://example.com/",
      "//example.com/",
      "/\\example.com",
      "/\t/example.com",
      "/\n\\example.com",
      "/\r\n//example.com",
      "//[x",
      "account",
      "",
      undefined,
      42,
    ]) {
      expect(appPath(path), String(path)).toBeUndefined();
    }
    expect(signInSearch({ redirect: "//example.com" })).toEqual({});
    expect(signInSearch({ redirect: "/account" })).toEqual({
      redirect: "/account",
    });
  });
});

describe("signInFor and signInReturn", () => {
  it("keep a link's token in a fragment on the way to signing in and back", () => {
    const to = signInFor({
      href: "/invite?x=%23y#token=abc",
      hash: "token=abc",
    });
    expect(to).toEqual({
      to: "/login",
      search: { redirect: "/invite?x=%23y" },
      hash: "token=abc",
    });
    expect(signInReturn(to.search, to.hash)).toBe("/invite?x=%23y#token=abc");
    expect(signInFor({ href: "/account", hash: "" })).toEqual({
      to: "/login",
      search: { redirect: "/account" },
      hash: "",
    });
    expect(signInReturn({ redirect: "/account" }, "")).toBe("/account");
    // A fragment without a page to go back to stays on the sign-in screen.
    expect(signInReturn({}, "token=abc")).toBe("/");
  });
});

describe("passwordMismatch", () => {
  it("marks each submission whose passwords differ anew", () => {
    const data = (password: string, confirm: string) => {
      const d = new FormData();
      d.set("password", password);
      d.set("confirm", confirm);
      return d;
    };
    expect(passwordMismatch(data("a", "a"))).toBeUndefined();
    const first = passwordMismatch(data("a", "b"));
    expect(first).toEqual({ field: "confirm" });
    expect(passwordMismatch(data("a", "b"))).not.toBe(first);
  });
});

describe("signInRegistered", () => {
  it("takes signing in that fails for a registration that stands, whatever refused it", async () => {
    expect(await signInRegistered(async () => "signed-in")).toBe("signed-in");
    // An address that had an account, with another password.
    expect(
      await signInRegistered(async () => {
        throw refused(401, { code: "invalid_credentials" });
      }),
    ).toBe("registered");
    // A refusal that the sign-in screen shows, where the page leads.
    expect(
      await signInRegistered(async () => {
        throw refused(429, { code: "rate_limited" });
      }),
    ).toBe("registered");
    expect(
      await signInRegistered(async () => {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }),
    ).toBe("registered");
  });
});

describe("fragmentToken", () => {
  it("reads the token of a link's fragment", () => {
    expect(fragmentToken("token=abc")).toBe("abc");
    expect(fragmentToken("#token=abc&x=1")).toBe("abc");
    expect(fragmentToken("")).toBeUndefined();
    expect(fragmentToken("token=")).toBeUndefined();
  });
});

describe("signedIn and endSession", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("keep what outlives a session, and drop the account's data", async () => {
    const verified = { ...testAccount, email_verified: true };
    const other = { ...testAccount, id: "usr_other", display_name: "Sam" };
    let account: typeof testAccount = verified;
    const call = vi.spyOn(api, "call").mockImplementation((async (
      operation: string,
    ) => {
      if (operation !== "getAccount") {
        throw new Error(`unexpected ${operation}`);
      }
      return { status: 200, body: account, response: new Response() };
    }) as typeof api.call);
    const client = new QueryClient();
    client.setQueryData(versionQuery.queryKey, {
      version: "(devel)",
      go_version: "go",
    });
    client.setQueryData(instanceQuery.queryKey, testInstance);
    client.setQueryData(sessionQuery.queryKey, testSession);
    client.setQueryData(accountQuery.queryKey, testAccount);
    client.setQueryData(["tokens"], []);

    // Another session of the same account, as after a change of the
    // password, keeps the account's data, with the account as the server
    // has it now, as once another tab verified its address.
    await signedIn(client, { ...testSession, csrf_token: "renewed" });
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(verified);
    expect(client.getQueryData(["tokens"])).toEqual([]);
    // One whose account the server cannot say keeps what it had of it.
    call.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await signedIn(client, { ...testSession, csrf_token: "again" });
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(verified);
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "again",
    );
    expect(call).toHaveBeenCalledTimes(2);

    // Another account's does not, and the page has the other account with
    // its session.
    account = other;
    await signedIn(client, { ...testSession, user_id: "usr_other" });
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(other);
    expect(client.getQueryData(["tokens"])).toBeUndefined();

    // A server that cannot say which account it is leaves the components
    // to ask for it.
    call.mockRejectedValue(new TypeError("Failed to fetch"));
    await signedIn(client, testSession);
    expect(client.getQueryData(sessionQuery.queryKey)).toEqual(testSession);
    expect(client.getQueryData(accountQuery.queryKey)).toBeUndefined();

    client.setQueryData(["tokens"], []);
    client.setQueryData(accountQuery.queryKey, testAccount);
    await endSession(client, { invalidate: async () => {} });
    expect(client.getQueryData(sessionQuery.queryKey)).toBeNull();
    expect(client.getQueryData(accountQuery.queryKey)).toBeUndefined();
    expect(client.getQueryData(["tokens"])).toBeUndefined();
    expect(client.getQueryData(instanceQuery.queryKey)).toEqual(testInstance);
    expect(client.getQueryData(versionQuery.queryKey)).toBeDefined();
  });
});

describe("followSession", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  /** A document whose tab the test hides and shows. */
  function tab() {
    const doc = Object.assign(new EventTarget(), {
      visibilityState: "visible" as DocumentVisibilityState,
    });
    const show = () => {
      doc.visibilityState = "visible";
      doc.dispatchEvent(new Event("visibilitychange"));
    };
    return { doc, show };
  }

  /**
   * Answers getSession with each of answers in turn, as the server would,
   * and getAccount with the account of the last session that it answered,
   * whose address is verified, as another tab may have verified it.
   */
  function sessions(...answers: (typeof testSession | null | Error)[]) {
    let last: typeof testSession | undefined;
    return vi.spyOn(api, "call").mockImplementation((async (
      operation: string,
    ) => {
      if (operation === "getAccount" && last !== undefined) {
        return {
          status: 200,
          body: { ...testAccount, id: last.user_id, email_verified: true },
          response: new Response(),
        };
      }
      if (operation !== "getSession") {
        throw new Error(`unexpected ${operation}`);
      }
      const answer = answers.shift();
      if (answer === null) {
        throw refused(401, { code: "unauthenticated" });
      }
      if (answer instanceof Error || answer === undefined) {
        throw answer ?? new Error("no answer left");
      }
      last = answer;
      return { status: 200, body: answer, response: new Response() };
    }) as typeof api.call);
  }

  /** The number of times that call asked for the session. */
  const asked = (call: ReturnType<typeof sessions>) =>
    call.mock.calls.filter(([operation]) => operation === "getSession").length;

  it("asks for the session whenever the tab is shown again, and tells what another tab changed of it", async () => {
    const client = new QueryClient();
    client.setQueryData(sessionQuery.queryKey, testSession);
    client.setQueryData(accountQuery.queryKey, testAccount);
    const ended = vi.fn();
    const replaced = vi.fn();
    const { doc, show } = tab();
    const other = { ...testSession, user_id: "usr_other" };
    const call = sessions(
      { ...testSession, csrf_token: "renewed" },
      new Error("Failed to fetch"),
      other,
      null,
    );
    const { stop } = followSession(client, doc, { ended, replaced });

    // A hidden tab asks nothing.
    doc.visibilityState = "hidden";
    doc.dispatchEvent(new Event("visibilitychange"));
    expect(call).not.toHaveBeenCalled();

    // Another tab changed the password: the session's new CSRF token, with
    // the account as the server has it now, whose address another tab
    // verified.
    show();
    await vi.waitFor(() =>
      expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
        "renewed",
      ),
    );
    expect(client.getQueryData(accountQuery.queryKey)).toEqual({
      ...testAccount,
      email_verified: true,
    });

    // A server that cannot say changes nothing.
    show();
    await vi.waitFor(() => expect(asked(call)).toBe(2));
    await new Promise((resolve) => setTimeout(resolve));
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "renewed",
    );

    // Another tab signed in to another account, whose account the page
    // has with its session.
    show();
    await vi.waitFor(() => expect(replaced).toHaveBeenCalledOnce());
    expect(client.getQueryData(sessionQuery.queryKey)).toEqual(other);
    expect(client.getQueryData(accountQuery.queryKey)?.id).toBe("usr_other");

    // Another tab signed out.
    show();
    await vi.waitFor(() => expect(ended).toHaveBeenCalledOnce());
    expect(replaced).toHaveBeenCalledOnce();

    stop();
    show();
    expect(asked(call)).toBe(4);
  });

  it("asks for the session when told to, as once a request was answered with 401, and keeps one that the server still has", async () => {
    const client = new QueryClient();
    client.setQueryData(sessionQuery.queryKey, testSession);
    client.setQueryData(accountQuery.queryKey, testAccount);
    const ended = vi.fn();
    const replaced = vi.fn();
    const { doc } = tab();
    const call = sessions(
      { ...testSession, csrf_token: "renewed" },
      { ...testSession, csrf_token: "stale" },
      null,
    );
    const { check } = followSession(client, doc, { ended, replaced });

    // A request that went with the session before the password changed:
    // the session is still the page's, with its new CSRF token.
    await check();
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "renewed",
    );
    expect(ended).not.toHaveBeenCalled();

    // A request was answered with 401 while the page was asking already,
    // whose answer the server gave before the session ended: the latest
    // answer counts, and the one before records nothing.
    await Promise.all([check(), check()]);
    expect(ended).toHaveBeenCalledOnce();
    expect(replaced).not.toHaveBeenCalled();
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "renewed",
    );
    expect(asked(call)).toBe(3);
  });

  it("resolves a check once the answer to the latest question is recorded, as of one asked while it was under way", async () => {
    const client = new QueryClient();
    client.setQueryData(sessionQuery.queryKey, testSession);
    client.setQueryData(accountQuery.queryKey, testAccount);
    const { doc, show } = tab();
    const answers: ((value: unknown) => void)[] = [];
    vi.spyOn(api, "call").mockImplementation(((operation: string) =>
      operation === "getAccount"
        ? Promise.resolve({
            status: 200,
            body: testAccount,
            response: new Response(),
          })
        : new Promise<unknown>((resolve) => {
            answers.push(resolve);
          })) as typeof api.call);
    const { check } = followSession(client, doc, {
      ended: vi.fn(),
      replaced: vi.fn(),
    });

    // A change of the password asks for the session, and the tab is shown
    // again while it does, whose question is the latest.
    let checked = false;
    const done = check().then(() => {
      checked = true;
    });
    show();
    expect(answers).toHaveLength(2);
    answers[0]?.({
      status: 200,
      body: { ...testSession, csrf_token: "before" },
      response: new Response(),
    });
    await new Promise((resolve) => setTimeout(resolve));
    expect(checked).toBe(false);
    expect(client.getQueryData(sessionQuery.queryKey)).toBe(testSession);

    answers[1]?.({
      status: 200,
      body: { ...testSession, csrf_token: "renewed" },
      response: new Response(),
    });
    await done;
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "renewed",
    );
  });

  it("takes no answer for the page's own session that it asked for before the page changed it", async () => {
    const client = new QueryClient();
    client.setQueryData(sessionQuery.queryKey, null);
    const ended = vi.fn();
    const replaced = vi.fn();
    const { doc, show } = tab();
    let answer: (value: unknown) => void = () => {};
    const call = vi.spyOn(api, "call").mockImplementation(((
      operation: string,
    ) =>
      operation === "getAccount"
        ? Promise.resolve({
            status: 200,
            body: testAccount,
            response: new Response(),
          })
        : new Promise<unknown>((resolve) => {
            answer = resolve;
          })) as typeof api.call);
    followSession(client, doc, { ended, replaced });

    show();
    expect(call).toHaveBeenCalledOnce();
    // The page signs in while the question is under way, whose answer is
    // of the session before.
    await signedIn(client, testSession);
    answer({
      status: 200,
      body: { ...testSession, user_id: "usr_other" },
      response: new Response(),
    });
    await new Promise((resolve) => setTimeout(resolve));
    expect(client.getQueryData(sessionQuery.queryKey)).toEqual(testSession);
    expect(replaced).not.toHaveBeenCalled();
    expect(ended).not.toHaveBeenCalled();
  });

  it("takes no answer whose account came once the page had changed its session itself", async () => {
    const client = new QueryClient();
    client.setQueryData(sessionQuery.queryKey, null);
    const ended = vi.fn();
    const replaced = vi.fn();
    const { doc, show } = tab();
    const other = { ...testSession, user_id: "usr_other" };
    const accounts: ((value: unknown) => void)[] = [];
    const call = vi.spyOn(api, "call").mockImplementation(((
      operation: string,
    ) =>
      operation === "getSession"
        ? Promise.resolve({
            status: 200,
            body: other,
            response: new Response(),
          })
        : new Promise<unknown>((resolve) => {
            accounts.push(resolve);
          })) as typeof api.call);
    followSession(client, doc, { ended, replaced });

    // Another tab signed in, whose account the page asks for.
    show();
    await vi.waitFor(() => expect(call).toHaveBeenCalledTimes(2));
    // The page signs in itself meanwhile, and has its account first.
    const own = signedIn(client, testSession);
    accounts[1]?.({ status: 200, body: testAccount, response: new Response() });
    await own;
    accounts[0]?.({
      status: 200,
      body: { ...testAccount, id: "usr_other" },
      response: new Response(),
    });
    await new Promise((resolve) => setTimeout(resolve));
    expect(client.getQueryData(sessionQuery.queryKey)).toEqual(testSession);
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(testAccount);
    expect(replaced).not.toHaveBeenCalled();
    expect(ended).not.toHaveBeenCalled();
  });
});

describe("loadSessionState", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("signs the page out when its account is answered as signed out, as once the session was revoked since", async () => {
    vi.spyOn(api, "call").mockImplementation((async (operation: string) => {
      if (operation === "getAccount") {
        throw refused(401, { code: "unauthenticated" });
      }
      throw new Error(`unexpected ${operation}`);
    }) as typeof api.call);
    const client = new QueryClient();
    client.setQueryData(instanceQuery.queryKey, testInstance);
    client.setQueryData(sessionQuery.queryKey, testSession);
    expect(await loadSessionState(client)).toEqual({
      instance: testInstance,
      session: null,
      account: null,
    });
    expect(client.getQueryData(sessionQuery.queryKey)).toBeNull();

    // Any other failure is the error page's.
    const failed = refused(500, { code: "internal_error" });
    vi.spyOn(api, "call").mockRejectedValue(failed);
    client.setQueryData(sessionQuery.queryKey, testSession);
    await expect(loadSessionState(client)).rejects.toBe(failed);
    expect(client.getQueryData(sessionQuery.queryKey)).toEqual(testSession);
  });
});
