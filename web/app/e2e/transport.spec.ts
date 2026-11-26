import { networkInterfaces } from "node:os";
import type { Browser, BrowserContext, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { apply, createProject, firstWorkspace, sessionOf } from "./content.ts";
import { port } from "./environment.ts";
import {
  atStart,
  en,
  expect,
  press,
  signIn,
  test,
  violations,
  watchPolicy,
} from "./fixtures.ts";
import { type Instance, startInstance } from "./instance.ts";

// The session over the transports that a self-hosted instance is reached
// over (PRD FR-AUTH-02 AC6, NFR-SEC-15): plain HTTP at an address of the
// machine on its network, as http://192.168.1.20:8080, without a
// certificate, whose session cookie is sc_session, without Secure, and whose
// pages warn that they travel unencrypted; and HTTPS through a proxy that
// terminates TLS and passes plain HTTP on to the instance, whose public URL
// is https, and whose cookie is __Host-sc_session, Secure. Over plain HTTP,
// the tool runs online alone (FR-OFF-01): it edits and syncs, registers no
// service worker, which browsers give such a page none of, and says why.
// Each test starts
// an instance of its own, on a new database, whose first-run setup it
// completes through the setup screen.

/** Returns an IPv4 address of this machine on a network, if it has one. */
function lanAddress(): string | undefined {
  for (const addresses of Object.values(networkInterfaces())) {
    for (const a of addresses ?? []) {
      if (a.family === "IPv4" && !a.internal) {
        return a.address;
      }
    }
  }
  return undefined;
}

const setupOwner = {
  name: "Ada Admin",
  email: "ada@example.com",
  password: "a passphrase of the first owner",
};

/**
 * Completes the instance's first-run setup through the setup screen, which
 * signs in as the owner, and signs out and in again, as a user who comes
 * back does.
 */
async function setUpAndSignIn(page: Page, instance: Instance): Promise<void> {
  await page.goto("/app/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["setup.title"],
  );
  expect(await violations(page)).toEqual([]);
  const field = (label: string) => page.getByLabel(label, { exact: true });
  await field(en["setup.code.label"]).fill(await instance.setupCode);
  await field(en["auth.display_name"]).fill(setupOwner.name);
  await field(en["auth.email"]).fill(setupOwner.email);
  await field(en["auth.new_password"]).fill(setupOwner.password);
  await field(en["auth.confirm_password"]).fill(setupOwner.password);
  await field(en["setup.workspace.label"]).fill("Ada's studio");
  await press(page.getByRole("button", { name: en["setup.submit"] }));
  await atStart(page, setupOwner.name);

  await press(page.getByRole("button", { name: en["shell.sign_out"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["login.title"],
  );
  await signIn(page, setupOwner.email, setupOwner.password);
  await atStart(page, setupOwner.name);
}

/**
 * Checks that the session lasts across requests: a page loaded anew is
 * still signed in, and so is the next page, whose requests read the
 * account's sessions.
 */
async function keepsSession(page: Page): Promise<void> {
  await page.reload();
  await atStart(page, setupOwner.name);
  await page.goto("/app/account/sessions");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["sessions.title"],
  );
  await expect(page.getByText(en["sessions.current"])).toBeVisible();
}

/** Returns the session cookie of context, which must have exactly one. */
async function sessionCookie(context: BrowserContext) {
  const cookies = (await context.cookies()).filter((c) =>
    ["sc_session", "__Host-sc_session"].includes(c.name),
  );
  expect(cookies.map((c) => c.name)).toHaveLength(1);
  return cookies[0];
}

/**
 * Opens a page of a new context of browser at the instance's URL, whose
 * violations of the Content-Security-Policy refused records, with the
 * page's service worker allowed, rather than blocked as the other tests
 * block it, if serviceWorkers says so.
 */
async function open(
  browser: Browser,
  instance: Instance,
  refused: string[],
  serviceWorkers: "allow" | "block" = "block",
) {
  const context = await browser.newContext({
    baseURL: instance.url,
    // The proxy's certificate is its own, which no browser trusts.
    ignoreHTTPSErrors: true,
    serviceWorkers,
  });
  const page = await context.newPage();
  await watchPolicy(page, refused);
  return { context, page };
}

/**
 * The name of the database of a test's instance: one for each kind of
 * instance, project, and port of the run, as server.ts names its own, so
 * that runs on other ports keep apart, and a run that was stopped before it
 * could drop it, as Playwright's on Windows, leaves it for the next run on
 * the port to replace, rather than one more each run.
 */
function databaseName(kind: string): string {
  const project = test.info().project.name.toLowerCase().replace(/\W+/g, "_");
  return `storycreep_e2e_${port}_${project}_${kind}`;
}

test.describe("the session over each transport", () => {
  // The transport is the instance's, not the device's, and each test starts
  // an instance and migrates a database of its own.
  test.skip(
    ({ hasTouch }) => hasTouch,
    "the desktop project covers the transports",
  );
  // Starting the instance, which migrates its new database, counts against
  // the test's time, and takes longer on a busy machine, as a CI runner,
  // than the 30 seconds that a test has by default.
  test.beforeEach(() => {
    test.setTimeout(180_000);
  });

  // Verifies: FR-AUTH-02/AC6, NFR-SEC-15, FR-OFF-01/AC4
  test("first-run setup and signing in over plain HTTP at a LAN address keep the session, every page warns, and the tool edits and syncs online alone", async ({
    browser,
    refused,
  }) => {
    const lan = lanAddress();
    test.skip(lan === undefined, "this machine has no address on a network");
    const instance = await startInstance({
      database: databaseName("lan"),
      host: lan as string,
    });
    try {
      expect(instance.url).toMatch(/^http:\/\/\d+\.\d+\.\d+\.\d+:\d+$/);
      // The worker is allowed, so that nothing but the browser keeps the
      // page from registering it.
      const { context, page } = await open(browser, instance, refused, "allow");
      const thrown: string[] = [];
      page.on("pageerror", (error) => {
        thrown.push(error.stack ?? String(error));
      });
      try {
        await setUpAndSignIn(page, instance);
        await keepsSession(page);
        const cookie = await sessionCookie(context);
        expect(cookie).toMatchObject({
          name: "sc_session",
          secure: false,
          httpOnly: true,
          sameSite: "Lax",
          path: "/",
        });
        // Every page says that it travels unencrypted, signed in or not.
        await expect(
          page.getByText(en["shell.unencrypted.title"]),
        ).toBeVisible();
        expect(await violations(page)).toEqual([]);
        await page.goto("/app/login");
        await expect(
          page.getByText(en["shell.unencrypted.title"]),
        ).toBeVisible();

        // The tool runs online alone: the page is no secure context, which
        // browsers give no service worker, and registers none, and the
        // warning says that installing the app and its offline features
        // need HTTPS, with a link to how to serve the instance over it.
        await atStart(page, setupOwner.name);
        expect(
          await page.evaluate(() => [
            window.isSecureContext,
            "serviceWorker" in navigator,
          ]),
        ).toEqual([false, false]);
        await expect(
          page.getByText(en["shell.unencrypted.body"]),
        ).toBeVisible();
        const tls = page.getByRole("link", {
          name: en["shell.unencrypted.tls"],
        });
        await expect(tls).toHaveAttribute(
          "href",
          "https://github.com/CyberCute-Studio/storycreep/blob/main/deploy/README.md#behind-a-reverse-proxy",
        );
        await expect(tls).toHaveAttribute("target", "_blank");
        await expect(tls).toHaveAttribute("rel", "noopener noreferrer");
        await expect(
          page.getByRole("button", { name: en["shell.install"] }),
        ).toHaveCount(0);

        // It syncs: a board that another session makes shows in the page's
        // tree, over the real-time channel; and it edits: the description
        // that the page saves reaches the server.
        const session = await sessionOf(page.request);
        const workspace = await firstWorkspace(page.request);
        const projectId = await createProject(
          page.request,
          session,
          workspace.id,
          "Harbour",
        );
        await page.goto(`/app/projects/${projectId}`);
        await expect(
          page.getByRole("button", {
            name: en["boards.new_board"],
            exact: true,
          }),
        ).toBeVisible();
        const boardId = newId("board");
        await apply(page.request, session, projectId, [
          { type: "board.create", payload: { id: boardId, name: "Quay" } },
        ]);
        await press(page.getByRole("link", { name: "Quay", exact: true }));
        await expect(page.getByRole("heading", { level: 1 })).toHaveText(
          "Quay",
        );
        await press(page.getByLabel(en["board.description"], { exact: true }));
        await page.keyboard.type("Where the boats come in");
        await press(page.getByRole("button", { name: en["board.save"] }));
        await expect(page.getByText(en["board.saved"])).toBeVisible();
        const res = await page.request.get(
          `/api/v1/projects/${projectId}/boards/${boardId}`,
        );
        expect(res.status(), await res.text()).toBe(200);
        expect(
          ((await res.json()) as { description: string }).description,
        ).toBe("Where the boats come in");

        // Nothing was thrown, of registering a worker or of anything else,
        // and the error console has nothing.
        expect(thrown).toEqual([]);
        await expect(
          page.getByRole("button", { name: /^Errors: / }),
        ).toHaveAccessibleName("Errors: none");
      } finally {
        await context.close();
      }
    } finally {
      await instance.stop();
    }
  });

  // Verifies: FR-AUTH-02/AC6
  test("first-run setup and signing in behind a proxy that terminates TLS get the __Host- cookie, Secure", async ({
    browser,
    refused,
  }) => {
    const instance = await startInstance({
      database: databaseName("tls"),
      host: "127.0.0.1",
      tls: true,
      // The proxy is on this machine, and forwards each client's address.
      env: { STORYCREEP_TRUSTED_PROXIES: "127.0.0.1" },
    });
    try {
      expect(instance.url).toMatch(/^https:\/\/localhost:\d+$/);
      const { context, page } = await open(browser, instance, refused);
      try {
        await setUpAndSignIn(page, instance);
        await keepsSession(page);
        const cookie = await sessionCookie(context);
        expect(cookie).toMatchObject({
          name: "__Host-sc_session",
          secure: true,
          httpOnly: true,
          sameSite: "Lax",
          path: "/",
        });
        expect(cookie.domain).toBe("localhost");
        await expect(page.getByText(en["shell.unencrypted.title"])).toHaveCount(
          0,
        );
      } finally {
        await context.close();
      }
    } finally {
      await instance.stop();
    }
  });
});
