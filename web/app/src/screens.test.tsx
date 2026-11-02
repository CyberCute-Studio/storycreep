import {
  type AccountBody,
  ApiError,
  type ErrorBody,
  NetworkError,
} from "@storycreep/shared/api";
import { catalogs } from "@storycreep/shared/i18n";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
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
      "Signing out failed, so you are still signed in. The server refused the request, because it did not seem to come from this page. Reload the page, and try again.",
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
