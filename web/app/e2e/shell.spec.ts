import type { Locator, Page } from "@playwright/test";
import { en, expect, press, test, violations } from "./fixtures.ts";

// The app's shell as the server serves it, in each project of
// playwright.config.ts: on the tablet, every control is tapped, never
// clicked, since a touch screen has no mouse. A page that is signed out, as
// these are, starts at the sign-in screen.

/** The title of the sign-in screen, where a signed-out page starts. */
const signInTitle = `${en["login.title"]} – Storycreep`;

/** The button that opens the error console, whose name says how many errors it has. */
function consoleButton(page: Page): Locator {
  return page.getByRole("button", { name: /^Errors: / });
}

/**
 * Opens the app, which shows a signed-out page the sign-in screen, once it
 * has made its first requests.
 */
async function openApp(page: Page): Promise<void> {
  await page.goto("/app/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  await page.waitForLoadState("networkidle");
}

/** Records two errors that nothing catches, as an app's bug would make. */
async function makeErrors(page: Page): Promise<void> {
  await page.evaluate(() => {
    setTimeout(() => {
      throw new Error("e2e: an uncaught error");
    });
    void Promise.reject(new Error("e2e: an unhandled rejection"));
  });
  await expect(consoleButton(page)).toHaveAccessibleName("Errors: 2");
}

/**
 * Makes the app fail as it shows a page, as an app's bug would: setting the
 * document's title to one of titles, which a page does once it is shown,
 * throws.
 */
async function failTitles(
  page: Page,
  titles: readonly string[],
): Promise<void> {
  await page.addInitScript((failing) => {
    const title = Object.getOwnPropertyDescriptor(Document.prototype, "title");
    Object.defineProperty(Document.prototype, "title", {
      configurable: true,
      get() {
        return title?.get?.call(this);
      },
      set(value: string) {
        if (failing.includes(value)) {
          throw new Error("e2e: a page that fails");
        }
        title?.set?.call(this, value);
      },
    });
  }, titles);
}

// Verifies: NFR-A11Y-08, NFR-A11Y-01
test("the page of an error that kept a page from being shown offers the error console, and has no accessibility violations", async ({
  page,
}) => {
  // The sign-in screen fails, and its error page shows.
  await failTitles(page, [signInTitle]);
  await page.goto("/app/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["route_error.title"],
  );
  await expect(page).toHaveTitle(`${en["route_error.title"]} – Storycreep`);
  expect(await violations(page)).toEqual([]);

  await press(
    page.getByRole("button", { name: en["route_error.show_console"] }),
  );
  const dialog = page.getByRole("dialog", { name: en["error_console.title"] });
  await expect(dialog).toContainText("Error: e2e: a page that fails");
});

// Verifies: NFR-A11Y-08, NFR-A11Y-01
test("an error that the error page could not show shows the error console in place of the app, with no accessibility violations", async ({
  page,
}) => {
  // The header's button fails to render once it counts one error, on the
  // start page and on the error page alike, which leaves the router nothing
  // to show; its first error is the failed request of the server's version.
  await page.addInitScript(() => {
    const select = Intl.PluralRules.prototype.select;
    Intl.PluralRules.prototype.select = function (
      this: Intl.PluralRules,
      n: number,
    ) {
      if (n === 1) {
        throw new Error("e2e: a header that fails");
      }
      return select.call(this, n);
    };
  });
  await page.route("**/version", (route) =>
    route.fulfill({
      status: 404,
      json: {
        error: {
          code: "not_found",
          message: "e2e: no version",
          request_id: "req_e2e",
        },
      },
    }),
  );
  await page.goto("/app/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["crash.title"],
  );
  await expect(page).toHaveTitle(`${en["crash.title"]} – Storycreep`);
  await expect(
    page.getByRole("list", { name: en["error_console.list"] }),
  ).toContainText("Error: e2e: a header that fails");
  expect(await violations(page)).toEqual([]);
});

// Verifies: NFR-A11Y-08, NFR-A11Y-01
test("the shell's pages and the error console have no accessibility violations", async ({
  page,
}) => {
  await openApp(page);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page).toHaveTitle(signInTitle);
  expect(await violations(page)).toEqual([]);

  await press(consoleButton(page));
  const dialog = page.getByRole("dialog", { name: en["error_console.title"] });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(en["error_console.empty"]);
  expect(await violations(page)).toEqual([]);
  await press(dialog.getByRole("button", { name: en["error_console.close"] }));
  await expect(dialog).toBeHidden();

  await makeErrors(page);
  await press(consoleButton(page));
  await press(dialog.locator("summary").first());
  await expect(
    dialog.getByRole("button", { name: en["error_console.copy_one"] }),
  ).toBeVisible();
  expect(await violations(page)).toEqual([]);

  await page.goto("/app/no-such-page");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["not_found.title"],
  );
  expect(await violations(page)).toEqual([]);
});

// Verifies: NFR-SEC-09
test("the shell runs under its Content-Security-Policy, whose nonce its own styles carry", async ({
  page,
  refused,
}) => {
  const response = await page.goto("/app/");
  const policy = response?.headers()["content-security-policy"] ?? "";
  const nonce = /'nonce-([^']+)'/.exec(policy)?.[1];
  expect(nonce).toBeTruthy();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  expect(
    await page.evaluate(
      () =>
        document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')
          ?.nonce,
    ),
  ).toBe(nonce);

  // The dialog's scroll lock adds a style of its own to the page.
  await press(consoleButton(page));
  await expect(page.getByRole("dialog")).toBeVisible();
  const styles = await page.evaluate(() =>
    [...document.querySelectorAll("style")].map((s) => s.nonce),
  );
  expect(styles.length).toBeGreaterThan(0);
  expect(styles).not.toContain("");
  expect(refused).toEqual([]);

  const again = await page.reload();
  expect(again?.headers()["content-security-policy"]).not.toContain(nonce);
});

// Verifies: NFR-OBS-06
test("the error console shows the errors with details to copy, and sends nothing", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openApp(page);
  const requests: string[] = [];
  page.on("request", (request) => {
    requests.push(`${request.method()} ${request.url()}`);
  });

  await makeErrors(page);
  await press(consoleButton(page));
  const dialog = page.getByRole("dialog", { name: en["error_console.title"] });
  const list = dialog.getByRole("list", { name: en["error_console.list"] });
  await expect(list.getByRole("listitem")).toHaveCount(2);
  await expect(list).toContainText("Error: e2e: an uncaught error");
  await expect(list).toContainText("Error: e2e: an unhandled rejection");

  await press(list.locator("summary").first());
  await expect(list.locator("pre").first()).toContainText(
    `Page: ${new URL("/app/", page.url()).href}`,
  );

  await press(
    dialog.getByRole("button", { name: en["error_console.copy_all"] }),
  );
  await expect(dialog.getByRole("status")).toHaveText(
    en["error_console.copied"],
  );
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  expect(copied).toContain(en["error_console.report.heading"]);
  expect(copied).toContain("e2e: an uncaught error");
  expect(copied).toContain("e2e: an unhandled rejection");
  expect(copied).toMatch(/^Server: \S+/m);

  await press(dialog.getByRole("button", { name: en["error_console.clear"] }));
  await expect(dialog).toContainText(en["error_console.empty"]);
  // The dialog hides the rest of the page from assistive technology while
  // it is open.
  await press(dialog.getByRole("button", { name: en["error_console.close"] }));
  await expect(consoleButton(page)).toHaveAccessibleName("Errors: none");
  expect(requests).toEqual([]);
});

test("a link into the app opens its page, and one that it does not have says so", async ({
  page,
}) => {
  await page.goto("/app/projects/prj_0123/no-such-page?x=1");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["not_found.title"],
  );
  await expect(page).toHaveTitle(`${en["not_found.title"]} – Storycreep`);
  await press(page.getByRole("link", { name: en["not_found.home"] }));
  // The start page needs a session, so it leads to signing in, which comes
  // back to it.
  await expect(page).toHaveURL(/\/app\/login\?redirect=%2F$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  // The link went with its page, and the new page's content has the focus
  // that it had.
  await expect(page.getByRole("main")).toBeFocused();

  // With the public site off, as it is by default, / is the app's.
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
});

test("the skip link moves the focus to the page's content", async ({
  page,
}) => {
  test.skip(
    test.info().project.use.hasTouch === true,
    "a touch screen has no Tab key",
  );
  await openApp(page);
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: en["shell.skip_to_content"] });
  await expect(skip).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("main")).toBeFocused();
});
