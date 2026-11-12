// What the end-to-end tests share: Playwright's test, which fails on a
// violation of the Content-Security-Policy and makes each test a client of
// an IP address of its own, how a control is activated on each project's
// device, axe's check of a page, the app's messages, and the accounts and
// links that the tests start from.

import { createHash, randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import AxeBuilder from "@axe-core/playwright";
import {
  type APIRequestContext,
  test as base,
  expect,
  type Locator,
  type Page,
  type TestInfo,
} from "@playwright/test";
import { owner } from "./environment.ts";

export { expect };

/** The app's messages in English, by key. */
export const en = JSON.parse(
  readFileSync(new URL("../../shared/i18n/en.json", import.meta.url), "utf8"),
) as Record<string, string>;

/**
 * Returns the IP address that the requests of a test come from, as the
 * X-Forwarded-For that the server believes of 127.0.0.1 names
 * (STORYCREEP_TRUSTED_PROXIES): one of 10.0.0.0/8 of its own, for each
 * test, project, and attempt, so that the tests do not share the rate limits
 * of one address.
 */
function clientAddress(info: TestInfo): string {
  const hash = createHash("sha256")
    .update(
      `${info.testId} ${info.project.name} ${info.retry} ${info.repeatEachIndex}`,
    )
    .digest();
  return `10.${hash[0]}.${hash[1]}.${hash[2]}`;
}

/**
 * Records in refused each violation of the Content-Security-Policy on the
 * pages that page opens, as the test fixture does of its own page, for a
 * page of another context that a test opens.
 */
export async function watchPolicy(
  page: Page,
  refused: string[],
): Promise<void> {
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`);
    });
  });
  page.on("console", (message) => {
    if (/Content Security Policy|CSP violation/i.test(message.text())) {
      refused.push(message.text());
    }
  });
}

/**
 * Playwright's test, which fails on a violation of the Content-Security-Policy
 * on any page that it opens (PRD NFR-SEC-09), as a style that a library adds
 * without the page's nonce: refused holds those so far.
 */
export const test = base.extend<{ refused: string[] }>({
  extraHTTPHeaders: async ({ extraHTTPHeaders }, use, info) => {
    await use({ ...extraHTTPHeaders, "X-Forwarded-For": clientAddress(info) });
  },
  refused: [
    async ({ page }, use) => {
      const refused: string[] = [];
      await watchPolicy(page, refused);
      await use(refused);
      expect(refused, "violations of the Content-Security-Policy").toEqual([]);
    },
    { auto: true },
  ],
});

/** Activates a control as the project's device does: a tap or a click. */
export async function press(locator: Locator): Promise<void> {
  if (test.info().project.use.hasTouch) {
    await locator.tap();
  } else {
    await locator.click();
  }
}

/** The violations that axe finds of WCAG 2.2 AA (PRD NFR-A11Y-01). */
export async function violations(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  return results.violations.map(
    (v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`,
  );
}

/** The password of the accounts that the tests register. */
export const password = "a passphrase of the e2e tests";

/** An account that a test registered. */
export interface Account {
  email: string;
  name: string;
}

/** Returns an email address that no other test, or run, uses. */
export function uniqueEmail(): string {
  return `e2e-${randomUUID()}@example.com`;
}

/** Registers a new account, with password, through the API. */
export async function registerAccount(
  request: APIRequestContext,
  name = "Jo Tester",
): Promise<Account> {
  const email = uniqueEmail();
  const res = await request.post("/api/v1/auth/register", {
    data: { email, display_name: name, password },
  });
  expect(res.status(), await res.text()).toBe(202);
  return { email, name };
}

/** A session that a request context signed in to, with its CSRF token. */
export interface ApiSession {
  csrf: string;
  userID: string;
}

/**
 * Signs the request context in, through the API, which keeps the session's
 * cookie, and returns the session's CSRF token.
 */
export async function apiSignIn(
  request: APIRequestContext,
  email: string,
  secret = password,
): Promise<ApiSession> {
  const res = await request.post("/api/v1/auth/login", {
    data: { email, password: secret },
  });
  expect(res.status(), await res.text()).toBe(200);
  const body = (await res.json()) as { csrf_token: string; user_id: string };
  return { csrf: body.csrf_token, userID: body.user_id };
}

/**
 * Takes, as the instance's owner, an instance admin, a link that resets the
 * password of the account of email, or verifies its address, which an
 * instance that sends no email gives out (PRD FR-AUTH-03), through request,
 * which is signed in as the owner from then on: a test that goes on to make
 * requests as another account signs request in as that account first.
 */
export async function adminLink(
  request: APIRequestContext,
  kind: "password-reset" | "verification",
  email: string,
): Promise<string> {
  const session = await apiSignIn(request, owner.email, owner.password);
  const res = await request.post(`/api/v1/admin/${kind}-links`, {
    headers: { "X-CSRF-Token": session.csrf },
    data: { email },
  });
  expect(res.status(), await res.text()).toBe(201);
  return ((await res.json()) as { url: string }).url;
}

/** Signs the page in through the sign-in screen, from wherever it is. */
export async function signIn(
  page: Page,
  email: string,
  secret = password,
): Promise<void> {
  await page.goto("/app/login");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  await page.getByLabel(en["auth.email"]).fill(email);
  await page.getByLabel(en["auth.password"], { exact: true }).fill(secret);
  await press(page.getByRole("button", { name: en["login.submit"] }));
}

/** Returns the message of key, with its {name} arguments filled in. */
export function message(
  key: string,
  args: Record<string, string> = {},
): string {
  return en[key].replace(/\{(\w+)\}/g, (_, name: string) => args[name] ?? "");
}

/**
 * Waits until the page shows the start page of the account named name: the
 * project list of its workspace, under the header that names the account.
 */
export async function atStart(page: Page, name: string): Promise<void> {
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["projects.title"],
  );
  await expect(
    page.getByRole("banner").getByRole("link", { name, exact: true }),
  ).toBeVisible();
}

/**
 * Returns what of page a finger would miss (PRD NFR-TOUCH-01): each control
 * that the page shows smaller than 44 by 44 CSS px, and each two controls
 * closer to each other than 8 px, by what names them, so that a test
 * expects none. A control is what a tap acts on: a link, a button, a
 * field, an item of a menu, what takes the keyboard's focus, and a handle
 * that the canvas places over itself. Not one is what the page does not
 * show, what it shows to assistive technology alone, as the link that
 * skips to the content, what is inert under a sheet or a dialog, a
 * link within a line of text, whose line is its height, and a checkbox or a
 * radio button but with its label, which a tap on presses it. Two controls
 * of which one holds the other, or that the page shows over each other, as
 * a handle over the canvas, are not beside each other, and nothing is
 * beside the canvas itself, the surface that a menu opens over; nor are two handles
 * that the canvas places over one node, which are as far apart as the
 * canvas draws their places, and of which a press is of the one that it is
 * on, or, where they cover each other, of the nearer (plan notes N69 and
 * N70).
 */
export async function smallTargets(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const controls =
      'a[href], button, input:not([type="hidden"]), select, textarea, summary, [role="button"], [role="link"], [role="menuitem"], [role="tab"], [role="option"], [role="checkbox"], [role="radio"], [role="switch"], [tabindex="0"], [data-canvas]';
    // The canvas, which is the page's surface under what is over it.
    const surface = '[role="application"]';
    const found: { el: Element; box: DOMRect; name: string }[] = [];
    for (const el of document.querySelectorAll(controls)) {
      if (
        !(el instanceof HTMLElement) ||
        !el.checkVisibility({ visibilityProperty: true }) ||
        el.closest("[inert], .sr-only") !== null
      ) {
        continue;
      }
      const style = getComputedStyle(el);
      if (el.tagName === "A" && style.display === "inline") {
        continue;
      }
      // A checkbox, or a radio button, is pressed by its label too.
      const pressed =
        el instanceof HTMLInputElement &&
        (el.type === "checkbox" || el.type === "radio")
          ? (el.closest("label") ?? el)
          : el;
      const box = pressed.getBoundingClientRect();
      if (box.width === 0 || box.height === 0) {
        continue;
      }
      const name =
        el.getAttribute("aria-label") ||
        (el.textContent ?? "").trim().slice(0, 40) ||
        (el instanceof HTMLInputElement
          ? (el.labels?.[0]?.textContent ?? "").trim().slice(0, 40)
          : "") ||
        el.tagName.toLowerCase();
      found.push({
        el: pressed,
        box,
        name: `${el.tagName.toLowerCase()} "${name}"`,
      });
    }
    const missed: string[] = [];
    for (const [i, a] of found.entries()) {
      if (Math.round(a.box.width) < 44 || Math.round(a.box.height) < 44) {
        missed.push(
          `${a.name} is ${Math.round(a.box.width)} by ${Math.round(a.box.height)}`,
        );
      }
      for (const b of found.slice(i + 1)) {
        if (
          a.el.contains(b.el) ||
          b.el.contains(a.el) ||
          (a.el.matches("[data-canvas]") && b.el.matches("[data-canvas]")) ||
          a.el.matches(surface) ||
          b.el.matches(surface)
        ) {
          continue;
        }
        // How far apart the two are, across or down: less than nothing
        // where the page shows one over the other.
        const gap = Math.round(
          Math.max(
            b.box.left - a.box.right,
            a.box.left - b.box.right,
            b.box.top - a.box.bottom,
            a.box.top - b.box.bottom,
          ),
        );
        if (gap >= 0 && gap < 8) {
          missed.push(`${a.name} and ${b.name} are ${gap} apart`);
        }
      }
    }
    return missed;
  });
}
