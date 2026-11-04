import type { APIRequestContext, Page } from "@playwright/test";
import {
  adminLink,
  apiSignIn,
  atStart,
  en,
  expect,
  message,
  password,
  press,
  registerAccount,
  signIn,
  test,
  uniqueEmail,
  violations,
} from "./fixtures.ts";

// Signing up, in, and out, and the pages of the links that the instance
// gives out (PRD FR-AUTH-01, FR-AUTH-02, FR-AUTH-03, FR-TEN-02), on the
// instance that Playwright starts, which anyone may register on, and which
// sends no email, so that an instance admin, its owner, gives out the links
// that reset a password and verify an address, and an invitation's link
// goes to the admin who makes it.

/** The field of a form whose label is label, exactly. */
function field(page: Page, label: string) {
  return page.getByLabel(label, { exact: true });
}

// Verifies: NFR-A11Y-08
test("registering, signing out, and signing in again, with a wrong password first", async ({
  page,
}) => {
  await page.goto("/app/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  expect(await violations(page)).toEqual([]);
  await press(page.getByRole("link", { name: en["login.sign_up"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["signup.title"],
  );
  expect(await violations(page)).toEqual([]);

  const email = uniqueEmail();
  await field(page, en["auth.display_name"]).fill("Sam Signup");
  await field(page, en["auth.email"]).fill(email);
  await field(page, en["auth.new_password"]).fill(password);
  await field(page, en["auth.confirm_password"]).fill("another passphrase");
  await press(page.getByRole("button", { name: en["signup.submit"] }));
  await expect(page.getByText(en["auth.error.passwords_differ"])).toBeVisible();
  await expect(field(page, en["auth.confirm_password"])).toBeFocused();
  await field(page, en["auth.confirm_password"]).fill(password);
  await press(page.getByRole("button", { name: en["signup.submit"] }));
  await atStart(page, "Sam Signup");
  expect(await violations(page)).toEqual([]);

  await press(page.getByRole("button", { name: en["shell.sign_out"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  await signIn(page, email, "not the passphrase");
  await expect(page.getByRole("main").getByRole("alert")).toHaveText(
    en["auth.error.invalid_credentials"],
  );
  expect(await violations(page)).toEqual([]);
  await signIn(page, email);
  await atStart(page, "Sam Signup");
});

// Verifies: NFR-A11Y-08
test("a link that resets a password, which an instance admin gives out where the instance sends no email", async ({
  page,
  request,
}) => {
  await page.goto("/app/forgot-password");
  await expect(page.getByText(en["forgot.no_email"])).toBeVisible();
  expect(await violations(page)).toEqual([]);

  const account = await registerAccount(request, "Riley Reset");
  const link = await adminLink(request, "password-reset", account.email);
  expect(new URL(link).hash).toMatch(/^#token=/);
  await page.goto(link);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["reset.title"],
  );
  expect(await violations(page)).toEqual([]);
  const replacement = "a new passphrase of the e2e tests";
  await field(page, en["auth.new_password"]).fill(replacement);
  await field(page, en["auth.confirm_password"]).fill(replacement);
  await press(page.getByRole("button", { name: en["reset.submit"] }));
  await expect(page.getByText(en["reset.done"])).toBeVisible();

  // The link works once.
  await page.reload();
  await field(page, en["auth.new_password"]).fill(replacement);
  await field(page, en["auth.confirm_password"]).fill(replacement);
  await press(page.getByRole("button", { name: en["reset.submit"] }));
  await expect(page.getByRole("main").getByRole("alert")).toHaveText(
    en["auth.error.token_used"],
  );

  await signIn(page, account.email, replacement);
  await atStart(page, "Riley Reset");
});

// Verifies: NFR-A11Y-08
test("a link that verifies an email address", async ({ page, request }) => {
  const account = await registerAccount(request, "Vic Verify");
  const link = await adminLink(request, "verification", account.email);
  await page.goto(link);
  await expect(page.getByText(en["verify.done"])).toBeVisible();
  expect(await violations(page)).toEqual([]);

  await signIn(page, account.email);
  await atStart(page, "Vic Verify");
  await page.goto("/app/account");
  await expect(
    page.getByText(en["account.profile.verified"], { exact: true }),
  ).toBeVisible();
});

/**
 * Registers an account that signs in, which gives it its personal workspace,
 * which it owns, and makes a link invitation to it with role, whose link it
 * returns, with the workspace's name.
 */
async function invitation(
  request: APIRequestContext,
  role: string,
): Promise<{ link: string; workspace: string }> {
  const inviter = await registerAccount(request, "Ivy Inviter");
  const session = await apiSignIn(request, inviter.email);
  const workspaces = await request.get("/api/v1/workspaces");
  expect(workspaces.status()).toBe(200);
  const [workspace] = (
    (await workspaces.json()) as {
      items: { id: string; name: string }[];
    }
  ).items;
  const res = await request.post(`/api/v1/workspaces/${workspace.id}/invites`, {
    headers: { "X-CSRF-Token": session.csrf },
    data: { role },
  });
  expect(res.status(), await res.text()).toBe(201);
  const { url } = (await res.json()) as { url: string };
  return { link: url, workspace: workspace.name };
}

// Verifies: NFR-A11Y-08
test("an invitation's link registers an account and accepts the invitation", async ({
  page,
  request,
}) => {
  const { link, workspace } = await invitation(request, "editor");
  await page.goto(link);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    message("invite.title_of", { workspace }),
  );
  await expect(
    page.getByText(
      message("invite.summary", { workspace, role: en["role.editor"] }),
    ),
  ).toBeVisible();
  expect(await violations(page)).toEqual([]);

  const register = page.getByRole("region", {
    name: en["invite.register_title"],
  });
  await register
    .getByLabel(en["auth.display_name"], { exact: true })
    .fill("Nia Newcomer");
  await register
    .getByLabel(en["auth.email"], { exact: true })
    .fill(uniqueEmail());
  await register
    .getByLabel(en["auth.new_password"], { exact: true })
    .fill(password);
  await register
    .getByLabel(en["auth.confirm_password"], { exact: true })
    .fill(password);
  await press(
    register.getByRole("button", { name: en["invite.register_submit"] }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    message("invite.joined_title", { workspace }),
  );
  await expect(
    page.getByText(
      message("invite.joined", { workspace, role: en["role.editor"] }),
    ),
  ).toBeVisible();
  expect(await violations(page)).toEqual([]);

  // The link is used.
  await page.reload();
  await expect(page.getByRole("main").getByRole("alert")).toHaveText(
    en["invite.error.accepted"],
  );
});

test("an invitation's link accepted by a user who is signed in", async ({
  page,
  request,
}) => {
  const { link, workspace } = await invitation(request, "viewer");
  const invitee = await registerAccount(request, "Ola Other");
  await signIn(page, invitee.email);
  await atStart(page, "Ola Other");
  await page.goto(link);
  await expect(
    page.getByText(message("invite.signed_in_as", { email: invitee.email })),
  ).toBeVisible();
  await press(page.getByRole("button", { name: en["invite.accept"] }));
  await expect(
    page.getByText(
      message("invite.joined", { workspace, role: en["role.viewer"] }),
    ),
  ).toBeVisible();
});
