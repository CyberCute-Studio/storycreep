import type { Page } from "@playwright/test";
import { acceptInvite, firstWorkspace } from "./content.ts";
import {
  apiSignIn,
  atStart,
  en,
  expect,
  message,
  press,
  registerAccount,
  signIn,
  test,
  violations,
} from "./fixtures.ts";

// The pages of a workspace (PRD FR-TEN-01–03): the switcher, which shows
// once the account has a second workspace, the settings, with the usage,
// and the members and invitations. Each test registers accounts of its own.

/** Registers an account, signs the page in as it, and returns its address. */
async function signedIn(page: Page, name: string): Promise<string> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return account.email;
}

/** The control that switches to another workspace, whose name is the current one's. */
function switcher(page: Page) {
  return page.getByRole("button", { name: /, switch to another workspace$/ });
}

// Verifies: NFR-A11Y-08
test("the switcher shows once the account has a second workspace, and switches between them", async ({
  page,
}) => {
  await signedIn(page, "Wes Workspaces");
  const personal = await firstWorkspace(page.request);
  // One workspace: no switcher, its name alone.
  await expect(
    page.getByRole("main").getByText(personal.name, { exact: true }),
  ).toBeVisible();
  await expect(switcher(page)).toHaveCount(0);
  expect(await violations(page)).toEqual([]);

  await press(page.getByRole("button", { name: en["workspace.create"] }));
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await dialog
    .getByLabel(en["workspace.name"], { exact: true })
    .fill("Second studio");
  await press(
    dialog.getByRole("button", { name: en["workspace.create_submit"] }),
  );
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["projects.title"],
  );
  await expect(switcher(page)).toHaveAccessibleName(
    message("workspace.switcher", { name: "Second studio" }),
  );

  await press(switcher(page));
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(menu.getByRole("menuitem", { name: personal.name }));
  await expect(switcher(page)).toHaveAccessibleName(
    message("workspace.switcher", { name: personal.name }),
  );
  expect(new URL(page.url()).pathname).toBe(`/app/workspaces/${personal.id}`);

  // The project list opens the workspace that the device last showed.
  await page.goto("/app/projects");
  await expect(switcher(page)).toHaveAccessibleName(
    message("workspace.switcher", { name: personal.name }),
  );
});

// Verifies: NFR-A11Y-08
test("the settings rename the workspace, and show what it uses, without a limit on a self-hosted instance", async ({
  page,
}) => {
  await signedIn(page, "Sol Settings");
  await press(
    page.getByRole("link", { name: en["workspace.nav.settings"], exact: true }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["workspace_settings.title"],
  );
  expect(await violations(page)).toEqual([]);
  const name = page.getByLabel(en["workspace_settings.name"], { exact: true });
  await name.fill("Sol's renamed studio");
  await press(
    page.getByRole("button", { name: en["workspace_settings.save"] }),
  );
  await expect(page.getByText(en["workspace_settings.saved"])).toBeVisible();
  await expect(
    page.getByText("Sol's renamed studio", { exact: true }),
  ).toBeVisible();
  expect((await firstWorkspace(page.request)).name).toBe(
    "Sol's renamed studio",
  );

  const usage = page.getByRole("table", { name: en["usage.title"] });
  await expect(
    usage.getByRole("row", { name: new RegExp(`^${en["usage.projects"]}`) }),
  ).toContainText(en["usage.no_limit"]);
  // Nothing offers a plan on a self-hosted instance (PRD FR-ENT-03 AC2).
  await expect(
    page.getByRole("link", { name: en["limit.upgrade"] }),
  ).toHaveCount(0);
});

// Verifies: NFR-A11Y-08
test("an invitation's link, which the page shows once, joins another account, whose role an admin changes before removing them", async ({
  page,
  playwright,
  baseURL,
  extraHTTPHeaders,
}) => {
  await signedIn(page, "Ines Inviter");
  await press(
    page.getByRole("link", { name: en["workspace.nav.members"], exact: true }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["members.title"],
  );
  await expect(page.getByText(en["invites.empty"])).toBeVisible();
  expect(await violations(page)).toEqual([]);

  // The instance sends no email, so the dialog shows the link.
  await press(
    page.getByRole("button", { name: en["invites.create"], exact: true }),
  );
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(dialog.getByLabel(en["invites.kind_link"], { exact: true }));
  await dialog
    .getByLabel(en["invites.role"], { exact: true })
    .selectOption("editor");
  await press(
    dialog.getByRole("button", { name: en["invites.create_submit"] }),
  );
  const link = dialog.getByLabel(en["invites.link"], { exact: true });
  await expect(link).toHaveValue(/\/app\/invite#token=/);
  const url = await link.inputValue();
  expect(await violations(page)).toEqual([]);
  await press(dialog.getByRole("button", { name: en["invites.copy"] }));
  await expect(dialog.getByText(en["invites.copied"])).toBeVisible();
  await press(dialog.getByRole("button", { name: en["invites.done"] }));
  await expect(dialog).toBeHidden();
  const invites = page.getByRole("list", { name: en["invites.title"] });
  await expect(invites.getByRole("listitem")).toHaveCount(1);
  await expect(invites).toContainText(en["invites.anyone"]);
  // The link is shown nowhere else.
  await expect(page.getByText(url)).toHaveCount(0);

  // Another account accepts it, from the test's own address.
  const other = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders,
  });
  try {
    const guest = await registerAccount(other, "Mo Member");
    await acceptInvite(other, await apiSignIn(other, guest.email), url);
  } finally {
    await other.dispose();
  }
  await page.reload();
  const members = page.getByRole("list", { name: en["members.list_title"] });
  const mo = members.getByRole("listitem").filter({ hasText: "Mo Member" });
  await expect(mo).toContainText(en["role_label.editor"]);
  await expect(page.getByText(en["invites.empty"])).toBeVisible();

  await press(mo.getByRole("button", { name: en["members.change_role"] }));
  const role = page.getByRole("dialog");
  await expect(role).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await role
    .getByLabel(en["members.role"], { exact: true })
    .selectOption("viewer");
  await press(
    role.getByRole("button", { name: en["members.change_role_submit"] }),
  );
  await expect(role).toBeHidden();
  await expect(mo).toContainText(en["role_label.viewer"]);

  await press(mo.getByRole("button", { name: en["members.remove"] }));
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(
    confirm.getByRole("button", { name: en["members.remove_confirm"] }),
  );
  await expect(confirm).toBeHidden();
  await expect(mo).toHaveCount(0);
  await expect(members.getByRole("listitem")).toHaveCount(1);
});
