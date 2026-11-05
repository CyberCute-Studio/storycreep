import type { Page } from "@playwright/test";
import {
  apiSignIn,
  atStart,
  en,
  expect,
  password,
  press,
  registerAccount,
  signIn,
  test,
  violations,
} from "./fixtures.ts";

// The account's own screens (PRD FR-AUTH-02, FR-AUTH-04, FR-I18N-02): its
// language, its sessions, its API tokens, and its password. Each test
// registers an account of its own and signs in as it.

/** Registers an account, signs the page in as it, and returns its address. */
async function signedIn(page: Page, name: string): Promise<string> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return account.email;
}

// Verifies: FR-I18N-02/AC1, NFR-A11Y-08
test("the language that the user saves stays the account's, after a reload too", async ({
  page,
}) => {
  await signedIn(page, "Lou Language");
  await page.goto("/app/account");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["account.title"],
  );
  expect(await violations(page)).toEqual([]);
  const language = page.getByRole("combobox", {
    name: en["account.language.label"],
  });
  await expect(language).toHaveValue("");
  await language.selectOption("en");
  const loads: string[] = [];
  page.on("load", () => loads.push(page.url()));
  await press(page.getByRole("button", { name: en["account.language.save"] }));
  await expect(page.getByText(en["account.language.saved"])).toBeVisible();
  // It applied without loading the page again.
  expect(loads).toEqual([]);

  const account = await page.request.get("/api/v1/auth/account");
  expect(((await account.json()) as { locale?: string }).locale).toBe("en");
  await page.reload();
  await expect(
    page.getByRole("combobox", { name: en["account.language.label"] }),
  ).toHaveValue("en");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
});

// Verifies: NFR-A11Y-08
test("the account's sessions, each of which, or all but this one, it signs out", async ({
  page,
  playwright,
  baseURL,
  extraHTTPHeaders,
}) => {
  const email = await signedIn(page, "Sid Sessions");
  // A second session, as of another device, from the test's own address.
  const other = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders,
    userAgent: "e2e another device",
  });
  try {
    await apiSignIn(other, email);

    await page.goto("/app/account/sessions");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["sessions.title"],
    );
    const items = page
      .getByRole("list", { name: en["sessions.title"] })
      .getByRole("listitem");
    await expect(items).toHaveCount(2);
    await expect(page.getByText("e2e another device")).toBeVisible();
    await expect(page.getByText(en["sessions.current"])).toBeVisible();
    expect(await violations(page)).toEqual([]);

    await press(
      page.getByRole("button", { name: en["sessions.revoke_others"] }),
    );
    await expect(
      page.getByText("Signed out of 1 other session."),
    ).toBeVisible();
    await expect(items).toHaveCount(1);
    // The button is disabled now, which Chromium takes the focus from: the
    // page's content has it, rather than the document's body.
    await expect(page.getByRole("main")).toBeFocused();
    expect((await other.get("/api/v1/auth/session")).status()).toBe(401);
  } finally {
    await other.dispose();
  }

  // Signing out this one goes to signing in.
  await press(page.getByRole("button", { name: en["sessions.sign_out_here"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
});

// Verifies: NFR-A11Y-08
test("an API token, whose secret the page shows once, which works until it is revoked", async ({
  page,
  playwright,
  baseURL,
  extraHTTPHeaders,
}) => {
  await signedIn(page, "Tia Tokens");
  await page.goto("/app/account/tokens");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["tokens.title"],
  );
  await expect(page.getByText(en["tokens.empty"])).toBeVisible();
  expect(await violations(page)).toEqual([]);

  await press(page.getByRole("button", { name: en["tokens.create"] }));
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await dialog
    .getByLabel(en["tokens.name"], { exact: true })
    .fill("Release pipeline");
  await dialog.getByLabel("project:read", { exact: true }).check();
  await press(dialog.getByRole("button", { name: en["tokens.create_submit"] }));
  const secret = dialog.getByLabel(en["tokens.secret"], { exact: true });
  await expect(secret).toHaveValue(/^scp_/);
  const value = await secret.inputValue();
  expect(await violations(page)).toEqual([]);
  await press(dialog.getByRole("button", { name: en["tokens.copy"] }));
  await expect(dialog.getByText(en["tokens.copied"])).toBeVisible();
  await press(dialog.getByRole("button", { name: en["tokens.done"] }));
  await expect(dialog).toBeHidden();
  await expect(
    page.getByRole("button", { name: en["tokens.create"] }),
  ).toBeFocused();

  const item = page
    .getByRole("list", { name: /^Tokens of / })
    .getByRole("listitem")
    .filter({ hasText: "Release pipeline" });
  await expect(item).toContainText("project:read");
  await expect(item).toContainText("snapshot:read");
  // The secret is shown nowhere else.
  await expect(page.getByText(value)).toHaveCount(0);

  // A program that holds the token, from the test's own address.
  const program = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: {
      ...extraHTTPHeaders,
      Authorization: `Bearer ${value}`,
    },
  });
  try {
    expect((await program.get("/api/v1/workspaces")).status()).toBe(200);

    await press(item.getByRole("button", { name: en["tokens.revoke"] }));
    const confirm = page.getByRole("alertdialog");
    await expect(confirm).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(
      confirm.getByRole("button", { name: en["tokens.revoke_confirm"] }),
    );
    await expect(confirm).toBeHidden();
    await expect(page.getByText(en["tokens.empty"])).toBeVisible();
    // The token's item went with its Revoke, which had the focus.
    await expect(page.getByRole("main")).toBeFocused();
    expect((await program.get("/api/v1/workspaces")).status()).toBe(401);
  } finally {
    await program.dispose();
  }
});

test("changing the password, after which the new one signs in", async ({
  page,
}) => {
  const email = await signedIn(page, "Pat Password");
  await page.goto("/app/account");
  const replacement = "a changed passphrase of the e2e tests";
  const form = page.getByRole("region", { name: en["account.password.title"] });
  await form
    .getByLabel(en["account.password.current"], { exact: true })
    .fill("not it at all");
  await form
    .getByLabel(en["auth.new_password"], { exact: true })
    .fill(replacement);
  await form
    .getByLabel(en["auth.confirm_password"], { exact: true })
    .fill(replacement);
  await press(
    form.getByRole("button", { name: en["account.password.submit"] }),
  );
  await expect(form.getByText(en["auth.error.wrong_password"])).toBeVisible();
  await expect(
    form.getByLabel(en["account.password.current"], { exact: true }),
  ).toBeFocused();

  await form
    .getByLabel(en["account.password.current"], { exact: true })
    .fill(password);
  await form
    .getByLabel(en["auth.new_password"], { exact: true })
    .fill(replacement);
  await form
    .getByLabel(en["auth.confirm_password"], { exact: true })
    .fill(replacement);
  await press(
    form.getByRole("button", { name: en["account.password.submit"] }),
  );
  await expect(form.getByText(en["account.password.changed"])).toBeVisible();

  // The session has a new CSRF token, which signing out sends.
  await press(page.getByRole("button", { name: en["shell.sign_out"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  await signIn(page, email, replacement);
  await atStart(page, "Pat Password");
});
