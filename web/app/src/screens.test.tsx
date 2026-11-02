import {
  type AccountBody,
  ApiError,
  type ErrorBody,
} from "@storycreep/shared/api";
import { catalogs } from "@storycreep/shared/i18n";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { accountQuery, api, sessionQuery } from "./api";
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

describe("a refused form", () => {
  it("moves the focus to the field that the server refused once, not again whenever it shows anew", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      if (operation === "changePassword") {
        throw refused(403, {
          code: "wrong_password",
          details: {
            errors: [{ location: "body.current_password", message: "is not" }],
          },
        });
      }
      if (operation === "updateAccount") {
        const { body } = input as { body: { locale: string } };
        return { ...testAccount, locale: body.locale } satisfies AccountBody;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account", { session: testSession });
    const current = await screen.findByLabelText("Current password");
    await user.type(current, "not it");
    await user.type(screen.getByLabelText("New password"), "a new passphrase");
    await user.type(
      screen.getByLabelText("Repeat the new password"),
      "a new passphrase",
    );
    await user.click(
      screen.getByRole("button", { name: "Change the password" }),
    );
    await screen.findByText("This is not your current password.");
    expect(document.activeElement).toBe(current);

    // Saving the language shows the account's page anew, the refused form
    // too, which leaves the focus on what the user pressed.
    const save = screen.getByRole("button", { name: "Save the language" });
    await user.click(save);
    await screen.findByText("Saved.");
    expect(document.activeElement).toBe(save);
  });
});

describe("first-run setup", () => {
  /** What setup asks for, of a configuration that sets SMTP and storage. */
  const pendingSetup = {
    signup_policy: "closed",
    signup_policy_fixed: false,
    smtp_fixed: true,
    storage_fixed: true,
  };

  it("says that setup completed, and leads to signing in, when signing in after it fails", async () => {
    const user = userEvent.setup();
    let instance = { ...testInstance, setup_pending: true };
    fakeApi((operation) => {
      switch (operation) {
        case "getSetup":
          return pendingSetup;
        case "completeSetup":
          instance = { ...instance, setup_pending: false };
          return undefined;
        case "getInstance":
          return instance;
        case "login":
          throw new TypeError("Failed to fetch");
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/setup", { instance });
    await user.type(await screen.findByLabelText("Setup code"), "7K2M");
    await user.type(screen.getByLabelText("Name"), "Ada");
    await user.type(screen.getByLabelText("Email address"), "ada@example.com");
    await user.type(screen.getByLabelText("New password"), "a passphrase!");
    await user.type(
      screen.getByLabelText("Repeat the new password"),
      "a passphrase!",
    );
    await user.type(screen.getByLabelText("Workspace name"), "Studio");
    await user.click(screen.getByRole("button", { name: "Complete setup" }));
    await screen.findByText(
      "This instance is set up. Sign in with the email address and the password of the account that you created.",
    );
    await user.click(screen.getByRole("link", { name: "Sign in" }));
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
  });

  it("goes to signing in once setup has completed elsewhere", async () => {
    fakeApi((operation) => {
      if (operation === "getSetup") {
        throw refused(404, { code: "not_found" });
      }
      if (operation === "getInstance") {
        return testInstance;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/setup", {
      instance: { ...testInstance, setup_pending: true },
    });
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.pathname).toBe("/login");
  });
});

describe("an invitation", () => {
  const invite = {
    kind: "email",
    email: "jo@example.com",
    workspace_name: "Studio",
    role: "editor",
    expires_at: "2026-12-01T10:00:00Z",
  } as const;
  const other = { ...testAccount, email: "sam@example.com" };

  it("says why accepting it refused the account that signed in with it, and signing out stays on it", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return invite;
        case "login":
          return testSession;
        case "acceptInvite":
          throw refused(403, { code: "invite_email_mismatch" });
        case "getAccount":
          return other;
        case "logout":
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/invite#token=secret");
    const signIn = await screen.findByRole("region", {
      name: "Have an account? Sign in",
    });
    await user.type(
      within(signIn).getByLabelText("Password"),
      "a passphrase of sam",
    );
    await user.click(
      within(signIn).getByRole("button", { name: "Sign in and accept" }),
    );
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "This invitation is for jo@example.com, but you are signed in as sam@example.com. Sign out, and sign in with jo@example.com.",
    );

    const accept = screen.getByRole("region", { name: "Accept" });
    await user.click(within(accept).getByRole("button", { name: "Sign out" }));
    await screen.findByRole("region", { name: "Have an account? Sign in" });
    expect(router.state.location.pathname).toBe("/invite");
    expect(router.state.location.hash).toBe("token=secret");
  });
});

describe("resetting a password", () => {
  it("leaves the page signed in to another account than the one that it reset", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "resetPassword":
          return undefined;
        case "getSession":
          return testSession;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/reset-password#token=secret", {
      session: testSession,
    });
    await user.type(
      await screen.findByLabelText("New password"),
      "a new passphrase",
    );
    await user.type(
      screen.getByLabelText("Repeat the new password"),
      "a new passphrase",
    );
    await user.click(
      screen.getByRole("button", { name: "Set the new password" }),
    );
    await screen.findByText(
      "Your password is changed, and every session of your account is signed out. Sign in with the new password.",
    );
    expect(queryClient.getQueryData(sessionQuery.queryKey)).toEqual(
      testSession,
    );
    expect(queryClient.getQueryData(accountQuery.queryKey)).toEqual(
      testAccount,
    );
  });
});
