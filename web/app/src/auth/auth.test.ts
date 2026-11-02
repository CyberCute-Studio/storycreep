import { ApiError, type ErrorBody } from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import {
  accountQuery,
  instanceQuery,
  sessionQuery,
  versionQuery,
} from "../api";
import { testAccount, testInstance, testSession } from "../test/app";
import { bodyField } from "./forms";
import { fragmentToken } from "./fragment";
import { refusalOf } from "./refusal";
import { appPath, signedIn, signedOut, signInSearch } from "./session";

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
    expect(refusalOf(new TypeError("Failed to fetch"), t).message).toBe(
      "The server could not be reached. Check your connection, and try again.",
    );
    expect(
      refusalOf(
        new ApiError(new Response("<html>", { status: 502 }), undefined),
        t,
      ).message,
    ).toBe("The server could not answer. Try again in a moment.");
    expect(refusalOf(refused(500, { code: "internal_error" }), t).message).toBe(
      "The server could not answer. Try again in a moment.",
    );
  });
});

describe("appPath", () => {
  it("keeps a redirect within the app", () => {
    expect(appPath("/account/tokens?x=1#y")).toBe("/account/tokens?x=1#y");
    for (const path of [
      "https://example.com/",
      "//example.com/",
      "/\\example.com",
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

describe("fragmentToken", () => {
  it("reads the token of a link's fragment", () => {
    expect(fragmentToken("token=abc")).toBe("abc");
    expect(fragmentToken("#token=abc&x=1")).toBe("abc");
    expect(fragmentToken("")).toBeUndefined();
    expect(fragmentToken("token=")).toBeUndefined();
  });
});

describe("signedIn and signedOut", () => {
  it("keep what outlives a session, and drop the account's data", () => {
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
    // password, keeps the account's data.
    signedIn(client, { ...testSession, csrf_token: "renewed" });
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(testAccount);
    signedIn(client, { ...testSession, csrf_token: "again" });
    expect(client.getQueryData(accountQuery.queryKey)).toEqual(testAccount);
    expect(client.getQueryData(sessionQuery.queryKey)?.csrf_token).toBe(
      "again",
    );

    // Another account's does not.
    signedIn(client, { ...testSession, user_id: "usr_other" });
    expect(client.getQueryData(accountQuery.queryKey)).toBeUndefined();

    client.setQueryData(accountQuery.queryKey, testAccount);
    signedOut(client);
    expect(client.getQueryData(sessionQuery.queryKey)).toBeNull();
    expect(client.getQueryData(accountQuery.queryKey)).toBeUndefined();
    expect(client.getQueryData(["tokens"])).toBeUndefined();
    expect(client.getQueryData(instanceQuery.queryKey)).toEqual(testInstance);
    expect(client.getQueryData(versionQuery.queryKey)).toBeDefined();
  });
});
