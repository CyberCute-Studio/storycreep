import {
  type AccountBody,
  ApiError,
  type ErrorBody,
} from "@storycreep/shared/api";
import { catalogs } from "@storycreep/shared/i18n";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { renderApp, testAccount, testInstance, testSession } from "./test/app";

// The account screens as the router shows them, over a fake of the API's
// client: what each guard leads to, what signing in shows of a refusal, and
// the language that the user prefers.

afterEach(() => {
  vi.restoreAllMocks();
});

type Call = Parameters<typeof api.call>;

/** Answers the client's calls with answer, which returns an operation's body. */
function fakeApi(answer: (operation: Call[0], input: unknown) => unknown) {
  return vi.spyOn(api, "call").mockImplementation((async (
    operation: Call[0],
    input: unknown,
  ) => ({
    status: 200,
    body: await answer(operation, input),
    response: new Response(),
  })) as typeof api.call);
}

/** Returns the ApiError of an answer of status with the error envelope of error. */
function refused(
  status: number,
  error: Partial<ErrorBody> & { code: string },
  headers: Record<string, string> = {},
): ApiError {
  const body = { message: error.code, request_id: "req", ...error };
  return new ApiError(
    new Response(JSON.stringify({ error: body }), {
      status,
      headers: { "Content-Type": "application/json", ...headers },
    }),
    body,
  );
}

describe("the guards", () => {
  it("send a signed-out page to signing in, which comes back to it", async () => {
    const { router } = renderApp("/account/sessions");
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toEqual({
      redirect: "/account/sessions",
    });
  });

  it("send every page to first-run setup while it is pending", async () => {
    fakeApi((operation) => {
      if (operation === "getSetup") {
        return {
          signup_policy: "closed",
          signup_policy_fixed: false,
          smtp_fixed: true,
          storage_fixed: false,
        };
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/login", {
      instance: { ...testInstance, setup_pending: true },
    });
    await screen.findByRole("heading", {
      level: 1,
      name: "Set up Storycreep",
    });
    // The configuration sets the SMTP server, so setup does not ask for one.
    expect(screen.queryByRole("heading", { name: "Email" })).toBeNull();
    expect(screen.getByRole("heading", { name: "File storage" })).toBeDefined();
  });

  it("send a signed-in page away from signing in", async () => {
    renderApp("/login", { session: testSession });
    await screen.findByText("You are signed in as Jo.");
  });
});

describe("signing in", () => {
  it("says why the server refused, and goes back once it signs in", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    fakeApi((operation) => {
      if (operation === "login") {
        attempts++;
        if (attempts === 1) {
          throw refused(401, { code: "invalid_credentials" });
        }
        if (attempts === 2) {
          throw refused(
            429,
            { code: "rate_limited", details: { reason: "locked" } },
            { "Retry-After": "60" },
          );
        }
        return testSession;
      }
      if (operation === "getAccount") {
        return testAccount;
      }
      if (operation === "listSessions") {
        return { items: [testSession.session], next_cursor: null };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/login?redirect=%2Faccount%2Fsessions");
    await user.type(
      await screen.findByLabelText("Email address"),
      "jo@example.com",
    );
    await user.type(screen.getByLabelText("Password"), "a wrong password");
    const submit = screen.getByRole("button", { name: "Sign in" });

    await user.click(submit);
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "The email address or the password is wrong.",
    );
    await user.click(submit);
    await screen.findByText(
      "Signing in with this email address failed too often. Try again in 60 seconds.",
    );
    await user.click(submit);
    await screen.findByRole("heading", { level: 1, name: "Sessions" });
    expect(router.state.location.pathname).toBe("/account/sessions");
  });
});

describe("the language", () => {
  // A catalog of a second language, which the shared catalogs do not have
  // yet, of the messages of the account's page.
  const testCatalogs = {
    en: catalogs.en,
    cs: {
      "account.title": "Účet",
      "account.language.label": "Jazyk",
      "account.language.save": "Uložit jazyk",
      "account.language.saved": "Uloženo.",
    },
  };

  // Verifies: FR-I18N-02/AC1
  it("shows the page in the language that the user saves, at once, without a reload", async () => {
    const user = userEvent.setup();
    const call = fakeApi((operation, input) => {
      if (operation === "updateAccount") {
        const { body } = input as { body: { locale: string } };
        return { ...testAccount, locale: body.locale } satisfies AccountBody;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account", {
      session: testSession,
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    const heading = await screen.findByRole("heading", {
      level: 1,
      name: "Account",
    });
    expect(document.documentElement.lang).toBe("en");

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Language" }),
      "cs",
    );
    await user.click(screen.getByRole("button", { name: "Save the language" }));
    await waitFor(() => expect(heading.textContent).toBe("Účet"));
    // The same page, in the same document: nothing loaded again.
    expect(heading.isConnected).toBe(true);
    expect(document.documentElement.lang).toBe("cs");
    expect(screen.getByText("Uloženo.")).toBeDefined();
    expect(call).toHaveBeenCalledWith("updateAccount", {
      headers: { "X-CSRF-Token": testSession.csrf_token },
      body: { locale: "cs" },
    });

    // The browser's language again.
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Jazyk" }),
      "",
    );
    await user.click(screen.getByRole("button", { name: "Uložit jazyk" }));
    await waitFor(() => expect(heading.textContent).toBe("Account"));
    expect(call).toHaveBeenLastCalledWith("updateAccount", {
      headers: { "X-CSRF-Token": testSession.csrf_token },
      body: { locale: "" },
    });
  });

  // Verifies: FR-I18N-02/AC1
  it("shows every page in the language that the account keeps, before the browser's", async () => {
    renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "cs" },
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    await screen.findByRole("heading", { level: 1, name: "Účet" });
    expect(document.documentElement.lang).toBe("cs");
    // A language whose catalog lacks a message shows it in English.
    expect(screen.getByRole("heading", { name: "Password" })).toBeDefined();
  });
});
