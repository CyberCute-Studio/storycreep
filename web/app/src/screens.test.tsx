import {
  type AccountBody,
  ApiError,
  type ErrorBody,
  NetworkError,
} from "@storycreep/shared/api";
import { catalogs } from "@storycreep/shared/i18n";
import { focusManager } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { accountQuery, api, sessionQuery } from "./api";
import { sessionChanges, signedIn } from "./auth/session";
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

/** What the query client does once the tab gets the focus again. */
function focusAgain() {
  onTestFinished(() => focusManager.setFocused(undefined));
  focusManager.setFocused(false);
  focusManager.setFocused(true);
}

/** Makes it a minute and a second later, as Date.now tells the time. */
function aMinuteLater() {
  const now = Date.now() + 61_000;
  vi.spyOn(Date, "now").mockReturnValue(now);
}

/** Lets the page do what it does next, as ask for what it shows. */
const settle = () => new Promise((resolve) => setTimeout(resolve));

/**
 * Queries the page's content, its main landmark, rather than the header,
 * which has an alert of its own, of signing out.
 */
const content = () => within(screen.getByRole("main"));

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

  it("send a sign-in screen that has signed in since where signing in would have, with its fragment", async () => {
    fakeApi((operation) => {
      if (operation === "lookupInvite") {
        return {
          kind: "link",
          workspace_name: "Studio",
          role: "editor",
          expires_at: "2026-12-01T10:00:00Z",
        };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/login?redirect=%2Finvite#token=secret", {
      session: testSession,
    });
    await screen.findByRole("region", { name: "Accept" });
    expect(router.state.location.pathname).toBe("/invite");
    expect(router.state.location.hash).toBe("token=secret");
  });
});

describe("signing out", () => {
  it("says so when it fails, which leaves the page signed in", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "logout") {
        throw refused(403, { code: "csrf_failed" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/", { session: testSession });
    const header = await screen.findByRole("banner");
    await user.click(within(header).getByRole("button", { name: "Sign out" }));
    expect(await within(header).findByRole("alert")).toHaveProperty(
      "textContent",
      "Signing out failed, so you are still signed in. The server refused the request, because it did not seem to come from this page. Try again.",
    );
    expect(screen.getByText("You are signed in as Jo.")).toBeDefined();
  });

  it("says no more that it failed once the session has ended otherwise", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "logout") {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, router } = renderApp("/", { session: testSession });
    const header = await screen.findByRole("banner");
    await user.click(within(header).getByRole("button", { name: "Sign out" }));
    await waitFor(() =>
      expect(within(header).getByRole("alert").textContent).not.toBe(""),
    );

    // Another tab signed out, which this one finds once it is shown again.
    sessionChanges(queryClient, router).ended();
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(within(header).getByRole("alert").textContent).toBe("");
  });

  it("goes to signing in when the session had ended already", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "logout") {
        throw refused(401, { code: "unauthenticated" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/", { session: testSession });
    const header = await screen.findByRole("banner");
    await user.click(within(header).getByRole("button", { name: "Sign out" }));
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.pathname).toBe("/login");
    expect(within(header).getByRole("alert").textContent).toBe("");
  });

  it("leaves a page that needs no session where it is, as an invitation's, whose link it keeps", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return {
            workspace_name: "Studio",
            kind: "link",
            role: "editor",
            expires_at: "2026-12-01T10:00:00Z",
          };
        case "logout":
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/invite#token=secret", {
      session: testSession,
    });
    const header = await screen.findByRole("banner");
    await screen.findByRole("region", { name: "Accept" });
    await user.click(within(header).getByRole("button", { name: "Sign out" }));
    await screen.findByRole("region", { name: "Have an account? Sign in" });
    expect(router.state.location.pathname).toBe("/invite");
    expect(router.state.location.hash).toBe("token=secret");
  });

  it("sends a page that needs a session to signing in, which comes back to it", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "logout") {
        return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/account", { session: testSession });
    const header = await screen.findByRole("banner");
    await screen.findByRole("heading", { level: 1, name: "Account" });
    await user.click(within(header).getByRole("button", { name: "Sign out" }));
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.search).toEqual({ redirect: "/account" });
  });
});

describe("another tab", () => {
  it("signing in to another account shows the page anew, not what it held of the one before", async () => {
    const sam = {
      ...testAccount,
      id: "usr_sam",
      email: "sam@example.com",
      display_name: "Sam",
    };
    fakeApi((operation) => {
      if (operation === "getAccount") {
        return sam;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, router } = renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "en" },
    });
    expect(
      await screen.findByRole("combobox", { name: "Language" }),
    ).toHaveProperty("value", "en");

    // What the page does once its tab is shown again (followSession).
    await signedIn(queryClient, { ...testSession, user_id: sam.id });
    await router.invalidate();
    await screen.findByText("sam@example.com");
    expect(screen.getByRole("combobox", { name: "Language" })).toHaveProperty(
      "value",
      "",
    );
  });
});

describe("a session that ends elsewhere", () => {
  it("sends a page that needs one to signing in, which comes back to it", async () => {
    const { queryClient, router } = renderApp("/account", {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Account" });
    sessionChanges(queryClient, router).ended();
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.search).toEqual({ redirect: "/account" });
    expect(queryClient.getQueryData(accountQuery.queryKey)).toBeUndefined();
  });

  it("sends a page to signing in once a request of it is answered as signed out, as once the session was revoked elsewhere", async () => {
    const call = fakeApi((operation) => {
      if (operation === "listSessions" || operation === "getSession") {
        throw refused(401, { code: "unauthenticated" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router, log } = renderApp("/account/sessions", {
      session: testSession,
    });
    // The list's 401 has the page ask the server for its session.
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
    expect(router.state.location.search).toEqual({
      redirect: "/account/sessions",
    });
    expect(
      call.mock.calls.filter(([operation]) => operation === "getSession"),
    ).toHaveLength(1);
    // Neither 401 is an error of the app's, nor was tried again.
    expect(log.entries()).toEqual([]);
    expect(
      call.mock.calls.filter(([operation]) => operation === "listSessions"),
    ).toHaveLength(1);
  });

  it("leaves a link's page where it is, which says what became of the link, rather than using it again, as it shows itself anew", async () => {
    const call = fakeApi((operation) => {
      switch (operation) {
        case "verifyEmail":
          return undefined;
        case "getAccount":
          return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, router } = renderApp("/verify-email#token=secret", {
      session: testSession,
    });
    await screen.findByText("Your email address is verified.");

    sessionChanges(queryClient, router).ended();
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Sign out" })).toBeNull(),
    );
    expect(router.state.location.pathname).toBe("/verify-email");
    expect(router.state.location.hash).toBe("token=secret");
    await screen.findByText("Your email address is verified.");

    // Another tab signed in, and the page shows itself anew again.
    await signedIn(queryClient, testSession);
    await router.invalidate();
    await screen.findByRole("button", { name: "Sign out" });
    expect(screen.getByText("Your email address is verified.")).toBeDefined();
    expect(
      call.mock.calls.filter(([operation]) => operation === "verifyEmail"),
    ).toHaveLength(1);
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
    expect(await content().findByRole("alert")).toHaveProperty(
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

  it("keeps the account that another tab has signed in to since the language was saved, rather than the one before", async () => {
    const user = userEvent.setup();
    const other = { ...testAccount, id: "usr_other", email: "sam@example.com" };
    let answer: (account: AccountBody) => void = () => {};
    fakeApi((operation) => {
      if (operation === "updateAccount") {
        return new Promise<AccountBody>((resolve) => {
          answer = resolve;
        });
      }
      if (operation === "getAccount") {
        return other;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account", {
      session: testSession,
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Language" }),
      "cs",
    );
    await user.click(screen.getByRole("button", { name: "Save the language" }));
    await signedIn(queryClient, { ...testSession, user_id: other.id });
    answer({ ...testAccount, locale: "cs" });
    await new Promise((resolve) => setTimeout(resolve));
    expect(queryClient.getQueryData(accountQuery.queryKey)).toEqual(other);
    expect(document.documentElement.lang).toBe("en");
  });

  it("shows the language that the account keeps once another device has saved another", async () => {
    fakeApi((operation) => {
      if (operation === "getAccount") {
        return { ...testAccount, locale: "cs" } satisfies AccountBody;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "en" },
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    expect(
      await screen.findByRole("combobox", { name: "Language" }),
    ).toHaveProperty("value", "en");

    // What the page does once its tab is shown again (followSession).
    await signedIn(queryClient, testSession);
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Jazyk" })).toHaveProperty(
        "value",
        "cs",
      ),
    );
  });

  it("keeps the language that the user saved while the page read the account again", async () => {
    const user = userEvent.setup();
    let read: (account: AccountBody) => void = () => {};
    fakeApi((operation, input) => {
      if (operation === "getAccount") {
        return new Promise<AccountBody>((resolve) => {
          read = resolve;
        });
      }
      if (operation === "updateAccount") {
        const { body } = input as { body: { locale: string } };
        return { ...testAccount, locale: body.locale } satisfies AccountBody;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account", {
      session: testSession,
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    const select = await screen.findByRole("combobox", { name: "Language" });
    // The page asks for its account again, as once its tab is shown again
    // (followSession), and the answer comes once the language is saved.
    const asked = signedIn(queryClient, testSession);
    await user.selectOptions(select, "cs");
    await user.click(screen.getByRole("button", { name: "Save the language" }));
    await screen.findByText("Uloženo.");
    read(testAccount);
    expect(await asked).toBe(true);
    expect(queryClient.getQueryData(accountQuery.queryKey)?.locale).toBe("cs");
    expect(document.documentElement.lang).toBe("cs");
  });

  it("keeps the language that the user saved while the page read the account again, though the page had it already", async () => {
    const user = userEvent.setup();
    let read: (account: AccountBody) => void = () => {};
    fakeApi((operation, input) => {
      if (operation === "getAccount") {
        return new Promise<AccountBody>((resolve) => {
          read = resolve;
        });
      }
      if (operation === "updateAccount") {
        const { body } = input as { body: { locale: string } };
        return { ...testAccount, locale: body.locale } satisfies AccountBody;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "cs" },
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    await screen.findByRole("heading", { level: 1, name: "Účet" });
    // The page asks for its account again, whose answer, read before the
    // language is saved, has none, as another device left it; the answer
    // to saving it is what the page held.
    const asked = signedIn(queryClient, testSession);
    await user.click(screen.getByRole("button", { name: "Uložit jazyk" }));
    await screen.findByText("Uloženo.");
    read(testAccount);
    expect(await asked).toBe(true);
    expect(queryClient.getQueryData(accountQuery.queryKey)?.locale).toBe("cs");
    expect(document.documentElement.lang).toBe("cs");
  });

  it("keeps the language that it shows while the account of a session that another tab signed in to is read", async () => {
    let read: (account: AccountBody) => void = () => {};
    let reads = 0;
    fakeApi((operation) => {
      if (operation === "getAccount") {
        reads++;
        // The page could not read it with the session (signedIn), and its
        // components ask for it themselves.
        if (reads === 1) {
          throw refused(503, { code: "unavailable" });
        }
        return new Promise<AccountBody>((resolve) => {
          read = resolve;
        });
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, i18n } = renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "cs" },
      catalogs: testCatalogs,
      languages: ["en-US"],
    });
    await screen.findByRole("heading", { level: 1, name: "Účet" });
    const shown: string[] = [];
    i18n.on("languageChanged", (locale: string) => shown.push(locale));

    // What the page does once its tab is shown again (followSession).
    await signedIn(queryClient, { ...testSession, user_id: "usr_other" });
    await waitFor(() => expect(reads).toBe(2));
    await settle();
    expect(document.documentElement.lang).toBe("cs");
    read({ ...testAccount, id: "usr_other", locale: "cs" });
    await screen.findByRole("heading", { level: 1, name: "Účet" });
    await settle();
    // Not the browser's language in between.
    expect(shown).toEqual([]);
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

  it("keeps a language that the account prefers, which the app has no catalog of, as the choice", async () => {
    renderApp("/account", {
      session: testSession,
      account: { ...testAccount, locale: "cs" },
    });
    expect(
      await screen.findByRole("combobox", { name: "Language" }),
    ).toHaveProperty("value", "cs");
  });

  it("shows the page in the browser's language where the account prefers none", async () => {
    renderApp("/account", {
      session: testSession,
      catalogs: testCatalogs,
      languages: ["cs-CZ", "en"],
    });
    await screen.findByRole("heading", { level: 1, name: "Účet" });
    expect(document.documentElement.lang).toBe("cs");
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

  it("drops the server's refusal of the attempt before once it refuses passwords that differ itself", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      if (operation === "changePassword") {
        throw refused(403, {
          code: "wrong_password",
          details: {
            errors: [{ location: "body.current_password", message: "is not" }],
          },
        });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account", { session: testSession });
    const current = await screen.findByLabelText("Current password");
    const repeat = screen.getByLabelText("Repeat the new password");
    await user.type(current, "not it");
    await user.type(screen.getByLabelText("New password"), "a new passphrase");
    await user.type(repeat, "a new passphrase");
    const submit = screen.getByRole("button", { name: "Change the password" });
    await user.click(submit);
    await screen.findByText("This is not your current password.");

    await user.clear(repeat);
    await user.type(repeat, "another passphrase");
    await user.click(submit);
    await screen.findByText("The two passwords are not the same.");
    expect(screen.queryByText("This is not your current password.")).toBeNull();
    expect(document.activeElement).toBe(repeat);
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
          throw new NetworkError(new TypeError("Failed to fetch"));
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

  it("goes to the start page once it has signed in, though reading the instance again failed", async () => {
    const user = userEvent.setup();
    let instance = { ...testInstance, setup_pending: true };
    let reads = 0;
    fakeApi((operation) => {
      switch (operation) {
        case "getSetup":
          return pendingSetup;
        case "completeSetup":
          instance = { ...instance, setup_pending: false };
          return undefined;
        case "getInstance":
          reads++;
          if (reads === 1) {
            throw refused(503, { code: "unavailable" });
          }
          return instance;
        case "login":
          return testSession;
        case "getAccount":
          return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/setup", { instance });
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
    await screen.findByText("You are signed in as Jo.");
    expect(router.state.location.pathname).toBe("/");
    expect(reads).toBe(2);
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

  it("says that someone else completed it first, though reading the instance again then fails", async () => {
    const user = userEvent.setup();
    let reads = 0;
    fakeApi((operation) => {
      switch (operation) {
        case "getSetup":
          return pendingSetup;
        case "completeSetup":
          throw refused(404, { code: "not_found" });
        case "getInstance":
          reads++;
          if (reads === 1) {
            throw new NetworkError(new TypeError("Failed to fetch"));
          }
          return testInstance;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/setup", { instance: { ...testInstance, setup_pending: true } });
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
      "Someone completed the setup of this instance first. Sign in with the account that it created.",
    );
    await user.click(screen.getByRole("link", { name: "Sign in" }));
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
  });
});

describe("registering", () => {
  it("leads to signing in once signing in with the new account fails, as when the server limits it, rather than asking to register again", async () => {
    const user = userEvent.setup();
    const call = fakeApi((operation) => {
      switch (operation) {
        case "registerAccount":
          return undefined;
        case "login":
          throw refused(429, { code: "rate_limited" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/signup");
    await user.type(await screen.findByLabelText("Name"), "Ada");
    await user.type(screen.getByLabelText("Email address"), "ada@example.com");
    await user.type(screen.getByLabelText("New password"), "a passphrase!");
    await user.type(
      screen.getByLabelText("Repeat the new password"),
      "a passphrase!",
    );
    await user.click(
      screen.getByRole("button", { name: "Create the account" }),
    );
    await screen.findByText(
      "If this email address had no account, it has one now. Sign in to continue.",
    );
    expect(screen.queryByRole("button", { name: "Create the account" })).toBe(
      null,
    );
    expect(
      call.mock.calls.filter(([operation]) => operation === "registerAccount"),
    ).toHaveLength(1);
    await user.click(screen.getByRole("link", { name: "Sign in" }));
    await screen.findByRole("heading", { level: 1, name: "Sign in" });
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
    const call = fakeApi((operation) => {
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
    expect(await content().findByRole("alert")).toHaveProperty(
      "textContent",
      "This invitation is for jo@example.com, but you are signed in as sam@example.com. Sign out, and sign in with jo@example.com.",
    );
    // The control that signed in went with its step: the focus is on the
    // first of the step that shows the refusal.
    const accept = screen.getByRole("region", { name: "Accept" });
    expect(document.activeElement).toBe(
      within(accept).getByRole("button", { name: "Accept the invitation" }),
    );

    await user.click(within(accept).getByRole("button", { name: "Sign out" }));
    await screen.findByRole("region", { name: "Have an account? Sign in" });
    expect(router.state.location.pathname).toBe("/invite");
    expect(router.state.location.hash).toBe("token=secret");
    // What the invitation's link shows is the same for every account, so
    // signing in and out did not ask for it again.
    expect(
      call.mock.calls.filter(([operation]) => operation === "lookupInvite"),
    ).toHaveLength(1);
  });

  it("says why signing out of an account that it is not for failed", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return invite;
        case "acceptInvite":
          throw refused(403, { code: "invite_email_mismatch" });
        case "logout":
          throw new NetworkError(new TypeError("Failed to fetch"));
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/invite#token=secret", { session: testSession, account: other });
    const accept = await screen.findByRole("region", { name: "Accept" });
    await user.click(
      within(accept).getByRole("button", { name: "Accept the invitation" }),
    );
    await user.click(
      await within(accept).findByRole("button", { name: "Sign out" }),
    );
    expect(await within(accept).findByRole("alert")).toHaveProperty(
      "textContent",
      "Signing out failed, so you are still signed in. The server could not be reached. Check your connection, and try again.",
    );
  });

  it("shows what the account that signs in on it joined, without the signed-in step in between", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return invite;
        case "login":
          return testSession;
        case "acceptInvite":
          return { id: "wsp_studio", name: "Studio", role: "editor" };
        case "getAccount":
          return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/invite#token=secret");
    const signIn = await screen.findByRole("region", {
      name: "Have an account? Sign in",
    });
    // Whether the page shows, at any time, the step of accepting it as the
    // signed-in user.
    let shown = false;
    const observer = new MutationObserver(() => {
      shown ||= document.getElementById("invite-accept") !== null;
    });
    observer.observe(document.body, { childList: true, subtree: true });
    onTestFinished(() => observer.disconnect());
    await user.type(
      within(signIn).getByLabelText("Password"),
      "a passphrase of jo",
    );
    await user.click(
      within(signIn).getByRole("button", { name: "Sign in and accept" }),
    );
    await screen.findByText("You have joined Studio as editor.");
    expect(shown).toBe(false);
  });

  it("shows itself in the language of the account that signs in on it", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return invite;
        case "login":
          return testSession;
        case "acceptInvite":
          throw refused(409, { code: "already_member" });
        case "getAccount":
          return { ...testAccount, locale: "cs" };
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/invite#token=secret", {
      catalogs: { en: catalogs.en, cs: { "invite.accept_title": "Přijetí" } },
      languages: ["en-US"],
    });
    const signIn = await screen.findByRole("region", {
      name: "Have an account? Sign in",
    });
    expect(document.documentElement.lang).toBe("en");
    await user.type(
      within(signIn).getByLabelText("Password"),
      "a passphrase of jo",
    );
    await user.click(
      within(signIn).getByRole("button", { name: "Sign in and accept" }),
    );
    await screen.findByRole("region", { name: "Přijetí" });
    expect(document.documentElement.lang).toBe("cs");
  });

  it("shows another invitation opened in the same page, not what it had of the one before", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      switch (operation) {
        case "lookupInvite": {
          const { body } = input as { body: { token: string } };
          return body.token === "first"
            ? invite
            : { ...invite, workspace_name: "Another studio" };
        }
        case "acceptInvite":
          return { id: "wsp_studio", name: "Studio", role: "editor" };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/invite#token=first", {
      session: testSession,
    });
    const accept = await screen.findByRole("region", { name: "Accept" });
    await user.click(
      within(accept).getByRole("button", { name: "Accept the invitation" }),
    );
    await screen.findByText("You have joined Studio as editor.");

    await router.navigate({ to: "/invite", hash: "token=second" });
    await screen.findByRole("heading", {
      level: 1,
      name: "Join Another studio",
    });
    expect(
      screen.getByRole("button", { name: "Accept the invitation" }),
    ).toBeDefined();
  });

  it("says only that it was accepted to another account than the one that joined", async () => {
    const user = userEvent.setup();
    const sam = { ...testAccount, id: "usr_sam", email: "sam@example.com" };
    const call = fakeApi((operation) => {
      switch (operation) {
        case "lookupInvite":
          return invite;
        case "acceptInvite":
          return { id: "wsp_studio", name: "Studio", role: "editor" };
        case "getAccount":
          return sam;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, router } = renderApp("/invite#token=secret", {
      session: testSession,
    });
    const accept = await screen.findByRole("region", { name: "Accept" });
    await user.click(
      within(accept).getByRole("button", { name: "Accept the invitation" }),
    );
    await screen.findByText("You have joined Studio as editor.");

    // Another account signed in to in this tab, as after signing out.
    await signedIn(queryClient, { ...testSession, user_id: sam.id });
    await router.invalidate();
    expect(await content().findByRole("alert")).toHaveProperty(
      "textContent",
      "This invitation has been accepted already.",
    );
    expect(screen.queryByText("You have joined Studio as editor.")).toBeNull();
    expect(
      call.mock.calls.filter(([operation]) => operation === "acceptInvite"),
    ).toHaveLength(1);
  });

  it("asks no more for an invitation that the server refused, as the tab is shown again, for a minute", async () => {
    const call = fakeApi((operation) => {
      if (operation === "lookupInvite") {
        throw refused(410, { code: "invite_revoked" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    // The server counts these among the requests about credentials.
    const lookups = () =>
      call.mock.calls.filter(([operation]) => operation === "lookupInvite")
        .length;
    renderApp("/invite#token=secret");
    await screen.findByText("This invitation has been revoked.");
    expect(lookups()).toBe(1);
    focusAgain();
    await settle();
    expect(lookups()).toBe(1);

    aMinuteLater();
    focusAgain();
    await waitFor(() => expect(lookups()).toBe(2));
  });

  it("asks again for an invitation whose request did not reach the server, as the tab is shown again, but tries no more one that the server failed to answer", async () => {
    let reached = false;
    const call = fakeApi((operation) => {
      if (operation === "lookupInvite") {
        if (!reached) {
          throw new NetworkError(new TypeError("Failed to fetch"));
        }
        throw refused(500, { code: "internal_error" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    const lookups = () =>
      call.mock.calls.filter(([operation]) => operation === "lookupInvite")
        .length;
    renderApp("/invite#token=secret");
    await screen.findByText(
      "The server could not be reached. Check your connection, and try again.",
    );
    // Tried three times more, as any request that did not reach the
    // server, which counted none of them.
    expect(lookups()).toBe(4);

    reached = true;
    focusAgain();
    await screen.findByText(
      "The server could not answer. Try again in a moment.",
    );
    // The server counted its failure among the requests about credentials.
    expect(lookups()).toBe(5);
    focusAgain();
    await settle();
    expect(lookups()).toBe(5);
  });
});

describe("verifying an email address", () => {
  it("verifies another link opened in the same page, and says what became of it", async () => {
    const call = fakeApi((operation, input) => {
      if (operation === "verifyEmail") {
        const { body } = input as { body: { token: string } };
        if (body.token === "first") {
          throw refused(410, { code: "token_expired" });
        }
        return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = renderApp("/verify-email#token=first");
    await waitFor(() =>
      expect(content().getByRole("alert").textContent).not.toBe(""),
    );

    await router.navigate({ to: "/verify-email", hash: "token=second" });
    await screen.findByText("Your email address is verified.");
    expect(content().getByRole("alert").textContent).toBe("");
    expect(call).toHaveBeenLastCalledWith("verifyEmail", {
      body: { token: "second" },
    });
  });
});

describe("the skip link", () => {
  it("moves the focus to the page's content, and leaves the URL and its token as they are", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/reset-password#token=secret");
    const skip = await screen.findByRole("link", { name: "Skip to content" });
    const before = window.location.href;
    await user.click(skip);
    expect(document.activeElement).toBe(screen.getByRole("main"));
    expect(window.location.href).toBe(before);
    expect(router.state.location.hash).toBe("token=secret");
    expect(screen.getByLabelText("New password")).toBeDefined();
  });
});

describe("resetting a password", () => {
  it("says that the password is reset once the page shows itself anew, as when another tab signs in", async () => {
    const user = userEvent.setup();
    const call = fakeApi((operation) => {
      switch (operation) {
        case "resetPassword":
          return undefined;
        case "getSession":
          throw refused(401, { code: "unauthenticated" });
        case "getAccount":
          return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient, router } = renderApp("/reset-password#token=secret");
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
    const done =
      "Your password is changed, and every session of your account is signed out. Sign in with the new password.";
    await screen.findByText(done);

    await signedIn(queryClient, testSession);
    await router.invalidate();
    await screen.findByRole("button", { name: "Sign out" });
    expect(screen.getByText(done)).toBeDefined();
    expect(screen.queryByLabelText("New password")).toBeNull();
    expect(
      call.mock.calls.filter(([operation]) => operation === "resetPassword"),
    ).toHaveLength(1);
  });

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

  it("shows the page anew for the account that another tab has signed in to since", async () => {
    const user = userEvent.setup();
    const sam = { ...testAccount, id: "usr_sam", display_name: "Sam" };
    const samSession = { ...testSession, user_id: sam.id, csrf_token: "sam" };
    fakeApi((operation) => {
      switch (operation) {
        case "resetPassword":
          return undefined;
        case "getSession":
          return samSession;
        case "getAccount":
          return sam;
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
    await screen.findByRole("link", { name: "Sam" });
    expect(
      screen.getByText(
        "Your password is changed, and every session of your account is signed out. Sign in with the new password.",
      ),
    ).toBeDefined();
    expect(queryClient.getQueryData(sessionQuery.queryKey)).toEqual(samSession);
    expect(queryClient.getQueryData(accountQuery.queryKey)).toEqual(sam);
  });

  it("stays signed in when the server cannot say whether the page's session still is", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "resetPassword":
          return undefined;
        case "getSession":
          throw refused(503, { code: "unavailable" });
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
    expect(screen.getByRole("button", { name: "Sign out" })).toBeDefined();
  });
});

describe("changing the password", () => {
  it("records the session's new CSRF token, though a question of the session that went before the change's answer came took it for ended", async () => {
    const user = userEvent.setup();
    const renewed = { ...testSession, csrf_token: "renewed" };
    let questions = 0;
    let checkSession = async () => {};
    fakeApi(async (operation) => {
      switch (operation) {
        case "changePassword":
          // A question that went with the cookie before the change, as
          // once the tab was shown again, is answered first.
          await checkSession();
          return undefined;
        case "getSession":
          questions++;
          if (questions === 1) {
            throw refused(401, { code: "unauthenticated" });
          }
          return renewed;
        case "getAccount":
          return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const app = renderApp("/account", { session: testSession });
    checkSession = app.checkSession;
    await user.type(
      await screen.findByLabelText("Current password"),
      "the old passphrase",
    );
    await user.type(screen.getByLabelText("New password"), "a new passphrase");
    await user.type(
      screen.getByLabelText("Repeat the new password"),
      "a new passphrase",
    );
    await user.click(
      screen.getByRole("button", { name: "Change the password" }),
    );
    await waitFor(() =>
      expect(app.queryClient.getQueryData(sessionQuery.queryKey)).toEqual(
        renewed,
      ),
    );
    await screen.findByRole("heading", { level: 1, name: "Account" });
    expect(app.router.state.location.pathname).toBe("/account");
    expect(questions).toBe(2);
  });
});

describe("the sessions", () => {
  it("gives the page's content the focus once the item of a session that it revoked goes", async () => {
    const user = userEvent.setup();
    const other = {
      ...testSession.session,
      id: "ses_other",
      current: false,
      user_agent: "Another browser",
    };
    let revoked = false;
    fakeApi((operation) => {
      switch (operation) {
        case "listSessions":
          return {
            items: revoked
              ? [testSession.session]
              : [testSession.session, other],
            next_cursor: null,
          };
        case "revokeSession":
          revoked = true;
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/sessions", { session: testSession });
    await screen.findByText("Another browser");
    await user.click(content().getByRole("button", { name: "Sign out" }));
    await waitFor(() =>
      expect(screen.queryByText("Another browser")).toBeNull(),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("main")),
    );
  });

  it("takes a session that has ended elsewhere since it was listed for revoked, and reads the list again without it", async () => {
    const user = userEvent.setup();
    const other = {
      ...testSession.session,
      id: "ses_other",
      current: false,
      user_agent: "Another browser",
    };
    // Signed out on its device, after the page listed it.
    let ended = false;
    fakeApi((operation) => {
      switch (operation) {
        case "listSessions":
          return {
            items: ended ? [testSession.session] : [testSession.session, other],
            next_cursor: null,
          };
        case "revokeSession":
          ended = true;
          throw refused(404, { code: "not_found", message: "Not found." });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/sessions", { session: testSession });
    await screen.findByText("Another browser");
    await user.click(content().getByRole("button", { name: "Sign out" }));
    await waitFor(() =>
      expect(screen.queryByText("Another browser")).toBeNull(),
    );
    expect(screen.queryByText("Not found.")).toBeNull();
  });

  it("signs out of every other session though the list of them could not be read", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "listSessions":
          throw refused(503, { code: "unavailable" });
        case "revokeOtherSessions":
          return { revoked: 1 };
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/sessions", { session: testSession });
    await screen.findByText(
      "The server could not answer. Try again in a moment.",
    );
    await user.click(
      screen.getByRole("button", { name: "Sign out of every other session" }),
    );
    await screen.findByText("Signed out of 1 other session.");
  });
});

describe("the error page", () => {
  it("asks no more for an account that the page could not read, until the user tries again", async () => {
    const user = userEvent.setup();
    let fails = true;
    const call = fakeApi((operation) => {
      if (operation === "getAccount") {
        if (fails) {
          throw refused(500, { code: "internal_error" });
        }
        return testAccount;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account", { session: testSession, account: null });
    await screen.findByRole("heading", {
      level: 1,
      name: "Something went wrong",
    });
    const reads = () =>
      call.mock.calls.filter(([operation]) => operation === "getAccount");
    // The page tried it three times more, as it tries any request that the
    // server failed to answer, and the error page asks no more.
    expect(reads()).toHaveLength(4);
    // Nor as the tab gets the focus again.
    focusAgain();
    await settle();
    expect(reads()).toHaveLength(4);

    fails = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("heading", { level: 1, name: "Account" });
    expect(reads()).toHaveLength(5);
  });
});

describe("the API tokens", () => {
  /** The names of the options of select. */
  const options = (select: HTMLElement) =>
    within(select)
      .getAllByRole("option")
      .map((option) => option.textContent);

  /** A membership of the workspace id, as its owner. */
  const workspace = (id: string, name: string) => ({
    id,
    name,
    role: "owner",
    membership_id: `mem_${id}`,
    default_locale: "en",
    created_at: "2026-11-01T10:00:00Z",
  });

  /** A token of workspace wsp_a, as the server answers its making. */
  const made = {
    id: "tok_new",
    name: "Release pipeline",
    scopes: ["snapshot:read"],
    user_id: testSession.user_id,
    workspace_id: "wsp_a",
    created_at: "2026-11-02T10:00:00Z",
    secret: "sct_shown_once",
  };

  /** Makes a token named name in the dialog, with the scopes as it ticks them. */
  async function makeToken(
    user: ReturnType<typeof userEvent.setup>,
    untick: string[] = [],
  ) {
    await user.click(
      await screen.findByRole("button", { name: "Make a token" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), made.name);
    for (const scope of untick) {
      await user.click(within(dialog).getByRole("checkbox", { name: scope }));
    }
    await user.click(
      within(dialog).getByRole("button", { name: "Make the token" }),
    );
    return dialog;
  }

  it("list more workspaces and projects to choose from as the user asks for them", async () => {
    const user = userEvent.setup();
    fakeApi((operation, input) => {
      const first =
        (input as { query?: { cursor?: string } }).query?.cursor === undefined;
      switch (operation) {
        case "listWorkspaces":
          return first
            ? { items: [workspace("wsp_a", "Studio A")], next_cursor: "a" }
            : { items: [workspace("wsp_b", "Studio B")], next_cursor: null };
        case "listProjects":
          return first
            ? { items: [{ id: "prj_a", name: "Game A" }], next_cursor: "a" }
            : { items: [{ id: "prj_b", name: "Game B" }], next_cursor: null };
        case "listWorkspaceTokens":
          return { items: [], next_cursor: null };
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    const workspaces = await screen.findByRole("combobox", {
      name: "Workspace",
    });
    expect(options(workspaces)).toEqual(["Studio A"]);
    await user.click(
      screen.getByRole("button", { name: "Show more workspaces" }),
    );
    await waitFor(() =>
      expect(options(workspaces)).toEqual(["Studio A", "Studio B"]),
    );
    expect(screen.queryByRole("button", { name: "Show more workspaces" })).toBe(
      null,
    );

    const targets = screen.getByRole("combobox", { name: "Tokens of" });
    await user.click(
      await screen.findByRole("button", { name: "Show more projects" }),
    );
    await waitFor(() =>
      expect(options(targets)).toEqual([
        "The whole workspace",
        "Game A",
        "Game B",
      ]),
    );
    expect(screen.queryByRole("button", { name: "Show more projects" })).toBe(
      null,
    );
  });

  it("shows the first workspace once the one chosen is no longer listed", async () => {
    const user = userEvent.setup();
    let listed = [
      workspace("wsp_a", "Studio A"),
      workspace("wsp_b", "Studio B"),
    ];
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: listed, next_cursor: null };
        case "listProjects":
        case "listWorkspaceTokens":
          return { items: [], next_cursor: null };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account/tokens", {
      session: testSession,
    });
    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Workspace" }),
      "wsp_b",
    );
    await screen.findByRole("heading", { name: "Tokens of Studio B" });

    // The user has left it, as the list says once it is read again.
    listed = [workspace("wsp_a", "Studio A")];
    await queryClient.invalidateQueries({ queryKey: ["workspaces"] });
    await screen.findByRole("heading", { name: "Tokens of Studio A" });
  });

  it("shows the whole workspace's tokens once the project chosen is no longer listed", async () => {
    const user = userEvent.setup();
    let projects = [{ id: "prj_a", name: "Game A" }];
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: projects, next_cursor: null };
        case "listWorkspaceTokens":
        case "listProjectTokens":
          return { items: [], next_cursor: null };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account/tokens", {
      session: testSession,
    });
    const targets = await screen.findByRole("combobox", { name: "Tokens of" });
    await waitFor(() =>
      expect(options(targets)).toEqual(["The whole workspace", "Game A"]),
    );
    await user.selectOptions(targets, "prj_a");
    await screen.findByRole("heading", { name: "Tokens of Game A" });

    // It went to the trash, as the list says once it is read again.
    projects = [];
    await queryClient.invalidateQueries({
      queryKey: ["workspaces", "wsp_a", "projects"],
    });
    await screen.findByRole("heading", { name: "Tokens of Studio A" });
    expect(targets).toHaveProperty("value", "");
  });

  it("reads a target's tokens again once it has made one, not each time the target is chosen again", async () => {
    const user = userEvent.setup();
    const call = fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return {
            items: [{ id: "prj_a", name: "Game A" }],
            next_cursor: null,
          };
        case "listWorkspaceTokens":
        case "listProjectTokens":
          return { items: [], next_cursor: null };
        case "createWorkspaceToken":
          return made;
      }
      throw new Error(`unexpected ${operation}`);
    });
    // The server counts these among the requests about credentials.
    const lists = () =>
      call.mock.calls.filter(
        ([operation]) => operation === "listWorkspaceTokens",
      ).length;
    renderApp("/account/tokens", { session: testSession });
    const targets = await screen.findByRole("combobox", { name: "Tokens of" });
    await screen.findByText("No tokens yet.");
    await waitFor(() =>
      expect(options(targets)).toEqual(["The whole workspace", "Game A"]),
    );
    await user.selectOptions(targets, "prj_a");
    await screen.findByRole("heading", { name: "Tokens of Game A" });
    await user.selectOptions(targets, "");
    await screen.findByRole("heading", { name: "Tokens of Studio A" });
    expect(lists()).toBe(1);

    const dialog = await makeToken(user);
    await within(dialog).findByLabelText("Token");
    await waitFor(() => expect(lists()).toBe(2));
  });

  it("asks no more for tokens that the server refused, as the target is chosen again or the tab is shown again, for a minute", async () => {
    const user = userEvent.setup();
    const call = fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return {
            items: [{ ...workspace("wsp_a", "Studio A"), role: "viewer" }],
            next_cursor: null,
          };
        case "listProjects":
          return {
            items: [{ id: "prj_a", name: "Game A" }],
            next_cursor: null,
          };
        case "listWorkspaceTokens":
        case "listProjectTokens":
          throw refused(403, {
            code: "insufficient_role",
            message: "Your role cannot see the tokens.",
          });
      }
      throw new Error(`unexpected ${operation}`);
    });
    // The server counts these among the requests about credentials.
    const lists = (of: string) =>
      call.mock.calls.filter(([operation]) => operation === of).length;
    renderApp("/account/tokens", { session: testSession });
    const targets = await screen.findByRole("combobox", { name: "Tokens of" });
    await content().findByText("Your role cannot see the tokens.");
    await waitFor(() =>
      expect(options(targets)).toEqual(["The whole workspace", "Game A"]),
    );
    await user.selectOptions(targets, "prj_a");
    await waitFor(() => expect(lists("listProjectTokens")).toBe(1));
    await user.selectOptions(targets, "");
    focusAgain();
    await settle();
    expect(
      content().getByText("Your role cannot see the tokens."),
    ).toBeDefined();
    expect(lists("listWorkspaceTokens")).toBe(1);

    aMinuteLater();
    focusAgain();
    await waitFor(() => expect(lists("listWorkspaceTokens")).toBe(2));
  });

  it("says that a guest's projects could not be read, and not that they have none", async () => {
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return {
            items: [{ ...workspace("wsp_a", "Studio A"), role: "guest" }],
            next_cursor: null,
          };
        case "listProjects":
          throw refused(503, { code: "unavailable" });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText(
      "The server could not answer. Try again in a moment.",
    );
    expect(
      screen.queryByText("No project of this workspace is shared with you."),
    ).toBeNull();
  });

  it("reads the first page of a target's tokens alone again once it has revoked one", async () => {
    const user = userEvent.setup();
    const token = (id: string, name: string) => ({
      id,
      name,
      scopes: ["snapshot:read"],
      user_id: testSession.user_id,
      workspace_id: "wsp_a",
      created_at: "2026-11-01T10:00:00Z",
    });
    let revoked = false;
    const call = fakeApi((operation, input) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          return (input as { query?: { cursor?: string } }).query?.cursor ===
            undefined
            ? {
                items: revoked ? [] : [token("tok_a", "Release pipeline")],
                next_cursor: "a",
              }
            : { items: [token("tok_b", "Nightly build")], next_cursor: null };
        case "revokeWorkspaceToken":
          revoked = true;
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    // The server counts each page among the requests about credentials.
    const lists = () =>
      call.mock.calls.filter(
        ([operation]) => operation === "listWorkspaceTokens",
      );
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText("Release pipeline");
    await user.click(screen.getByRole("button", { name: "Show more" }));
    await screen.findByText("Nightly build");
    expect(lists()).toHaveLength(2);

    await user.click(screen.getAllByRole("button", { name: "Revoke" })[0]);
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Revoke the token" }),
    );
    await waitFor(() =>
      expect(screen.queryByText("Release pipeline")).toBeNull(),
    );
    expect(lists()).toHaveLength(3);
    expect(lists()[2][1]).toMatchObject({ query: { cursor: undefined } });
    // The pages after the first come again as the user asks for them.
    expect(screen.queryByText("Nightly build")).toBeNull();
    expect(screen.getByRole("button", { name: "Show more" })).toBeDefined();
  });

  it("asks no more of its own accord for a target's tokens of which it shows more than the first page, each a request about credentials", async () => {
    const user = userEvent.setup();
    const token = (id: string, name: string) => ({
      id,
      name,
      scopes: ["snapshot:read"],
      user_id: testSession.user_id,
      workspace_id: "wsp_a",
      created_at: "2026-11-01T10:00:00Z",
    });
    const call = fakeApi((operation, input) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          return (input as { query?: { cursor?: string } }).query?.cursor ===
            undefined
            ? { items: [token("tok_a", "Release pipeline")], next_cursor: "a" }
            : { items: [token("tok_b", "Nightly build")], next_cursor: null };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const lists = () =>
      call.mock.calls.filter(
        ([operation]) => operation === "listWorkspaceTokens",
      ).length;
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText("Release pipeline");
    await user.click(screen.getByRole("button", { name: "Show more" }));
    await screen.findByText("Nightly build");
    expect(lists()).toBe(2);

    aMinuteLater();
    focusAgain();
    await settle();
    expect(lists()).toBe(2);
    expect(screen.getByText("Nightly build")).toBeDefined();
  });

  it("takes a token that has been revoked elsewhere since it was listed for revoked, and reads the list again without it", async () => {
    const user = userEvent.setup();
    // Revoked by another admin, after the page listed it.
    let gone = false;
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          return {
            items: gone
              ? []
              : [
                  {
                    id: "tok_a",
                    name: "Release pipeline",
                    scopes: ["snapshot:read"],
                    user_id: testSession.user_id,
                    workspace_id: "wsp_a",
                    created_at: "2026-11-01T10:00:00Z",
                  },
                ],
            next_cursor: null,
          };
        case "revokeWorkspaceToken":
          gone = true;
          throw refused(404, { code: "not_found", message: "Not found." });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText("Release pipeline");
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Revoke the token" }),
    );
    await waitFor(() =>
      expect(screen.queryByText("Release pipeline")).toBeNull(),
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(screen.queryByText("Not found.")).toBeNull();
  });

  it("gives the page's content the focus once the item of a token of a later page that it revoked goes", async () => {
    const user = userEvent.setup();
    const token = (id: string, name: string) => ({
      id,
      name,
      scopes: ["snapshot:read"],
      user_id: testSession.user_id,
      workspace_id: "wsp_a",
      created_at: "2026-11-01T10:00:00Z",
    });
    let revoked = false;
    fakeApi((operation, input) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          return (input as { query?: { cursor?: string } }).query?.cursor ===
            undefined
            ? { items: [token("tok_a", "Release pipeline")], next_cursor: "a" }
            : {
                items: revoked ? [] : [token("tok_b", "Nightly build")],
                next_cursor: null,
              };
        case "revokeWorkspaceToken":
          revoked = true;
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText("Release pipeline");
    await user.click(screen.getByRole("button", { name: "Show more" }));
    await screen.findByText("Nightly build");
    await user.click(screen.getAllByRole("button", { name: "Revoke" })[1]);
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Revoke the token" }),
    );
    // The list is read again from its first page, so the item goes as the
    // confirmation closes, before it could give its Revoke the focus back.
    await waitFor(() => expect(screen.queryByText("Nightly build")).toBeNull());
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("main")),
    );
  });

  it("gives the page's content the focus once the item of a token that it revoked goes", async () => {
    const user = userEvent.setup();
    let revoked = false;
    let reread: () => void = () => {};
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          if (!revoked) {
            return {
              items: [
                {
                  id: "tok_a",
                  name: "Release pipeline",
                  scopes: ["snapshot:read"],
                  user_id: testSession.user_id,
                  workspace_id: "wsp_a",
                  created_at: "2026-11-01T10:00:00Z",
                },
              ],
              next_cursor: null,
            };
          }
          return new Promise((resolve) => {
            reread = () => resolve({ items: [], next_cursor: null });
          });
        case "revokeWorkspaceToken":
          revoked = true;
          return undefined;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    await screen.findByText("Release pipeline");
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Revoke the token" }),
    );
    // The confirmation gives the focus back to the token's Revoke, which
    // goes with the token's item once the list has been read again.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Revoke" }),
      ),
    );
    reread();
    await waitFor(() =>
      expect(screen.queryByText("Release pipeline")).toBeNull(),
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("main")),
    );
  });

  it("keeps the dialog of a new token's secret open when the page outside it is clicked", async () => {
    // The dialog takes the pointer events of the page outside it, which a
    // click there goes to all the same.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
        case "listWorkspaceTokens":
          return { items: [], next_cursor: null };
        case "createWorkspaceToken":
          return made;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    const dialog = await makeToken(user);
    await within(dialog).findByLabelText("Token");
    await user.click(document.body);
    expect(dialog.isConnected).toBe(true);
    expect(within(dialog).getByLabelText("Token")).toHaveProperty(
      "value",
      made.secret,
    );
    // Escape, which a user presses to close it, does, and the control that
    // opened it gets the focus back.
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Make a token" }),
      ),
    );
  });

  it("shows the secret of a token that it made while the list was read, though the list then fails to be read", async () => {
    const user = userEvent.setup();
    let fail: (error: unknown) => void = () => {};
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          return new Promise((_, reject) => {
            fail = reject;
          });
        case "createWorkspaceToken":
          return made;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    const dialog = await makeToken(user);
    await within(dialog).findByLabelText("Token");
    fail(refused(503, { code: "unavailable" }));
    // The list says why alone, without the control that opened the dialog,
    // which the page keeps.
    await screen.findByText(
      "The server could not answer. Try again in a moment.",
    );
    expect(screen.queryByRole("button", { name: "Make a token" })).toBeNull();
    expect(dialog.isConnected).toBe(true);
    expect(within(dialog).getByLabelText("Token")).toHaveProperty(
      "value",
      made.secret,
    );
    // The page's content gets the focus once it closes.
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("main")),
    );
  });

  it("shows the secret of a project's token that it made, though the project is then no longer listed", async () => {
    const user = userEvent.setup();
    let projects = [{ id: "prj_a", name: "Game A" }];
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: projects, next_cursor: null };
        case "listWorkspaceTokens":
        case "listProjectTokens":
          return { items: [], next_cursor: null };
        case "createProjectToken":
          return made;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { queryClient } = renderApp("/account/tokens", {
      session: testSession,
    });
    const targets = await screen.findByRole("combobox", { name: "Tokens of" });
    await waitFor(() =>
      expect(options(targets)).toEqual(["The whole workspace", "Game A"]),
    );
    await user.selectOptions(targets, "prj_a");
    await screen.findByRole("heading", { name: "Tokens of Game A" });
    const dialog = await makeToken(user);
    await within(dialog).findByLabelText("Token");

    // Another tab put it in the trash, as the list says once it is read
    // again, as when this tab is shown again.
    projects = [];
    await queryClient.invalidateQueries({
      queryKey: ["workspaces", "wsp_a", "projects"],
    });
    // Behind the dialog, which hides the page from a screen reader.
    await screen.findByRole("heading", {
      name: "Tokens of Studio A",
      hidden: true,
    });
    expect(dialog.isConnected).toBe(true);
    expect(
      within(dialog).getByRole("heading", { name: "New token of Game A" }),
    ).toBeDefined();
    expect(within(dialog).getByLabelText("Token")).toHaveProperty(
      "value",
      made.secret,
    );
  });

  it("shows the secret of a token that it made, though the list then fails to be read again", async () => {
    const user = userEvent.setup();
    let lists = 0;
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
          return { items: [], next_cursor: null };
        case "listWorkspaceTokens":
          lists++;
          if (lists > 1) {
            throw refused(503, { code: "unavailable" });
          }
          return { items: [], next_cursor: null };
        case "createWorkspaceToken":
          return made;
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    const dialog = await makeToken(user);
    expect(await within(dialog).findByLabelText("Token")).toHaveProperty(
      "value",
      made.secret,
    );
    await waitFor(() => expect(lists).toBe(2));
    // The list says why, behind the dialog, which the page keeps.
    await screen.findByText(
      "The server could not answer. Try again in a moment.",
    );
    expect(dialog.isConnected).toBe(true);
    expect(within(dialog).getByLabelText("Token")).toHaveProperty(
      "value",
      made.secret,
    );
  });

  it("moves the focus to the scopes when the server refuses them", async () => {
    const user = userEvent.setup();
    fakeApi((operation) => {
      switch (operation) {
        case "listWorkspaces":
          return { items: [workspace("wsp_a", "Studio A")], next_cursor: null };
        case "listProjects":
        case "listWorkspaceTokens":
          return { items: [], next_cursor: null };
        case "createWorkspaceToken":
          throw refused(422, {
            code: "validation_failed",
            details: {
              errors: [
                {
                  location: "body.scopes",
                  message: "expected array length >= 1",
                },
              ],
            },
          });
      }
      throw new Error(`unexpected ${operation}`);
    });
    renderApp("/account/tokens", { session: testSession });
    const dialog = await makeToken(user, ["snapshot:read"]);
    await within(dialog).findByText("Expected array length >= 1.");
    expect(document.activeElement).toBe(
      within(dialog).getByRole("checkbox", { name: "snapshot:read" }),
    );
  });
});
