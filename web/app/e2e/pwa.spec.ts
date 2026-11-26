import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import type { BrowserContext, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { makeProject } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import {
  en,
  expect,
  press,
  test,
  violations,
  watchPolicy,
} from "./fixtures.ts";

// The installable app and its service worker (PRD FR-OFF-01, plan note
// N95), as the server serves the build: Chromium's own check that the tool
// installs, the manifest, which opens the installed app at the tool, the
// shell that the worker keeps, which answers the app's pages, each with a
// nonce of its own, offline too, while the API's answers never come from
// the worker, and a new build, whose worker waits for the user's reload
// while the page goes on with its own build. The other end-to-end tests
// block the worker (playwright.config.ts); these allow it.

test.use({ serviceWorkers: "allow" });

/** Waits until the page's worker is active, which it is once it has kept every file of the build. */
async function workerReady(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
}

/** Returns the nonce of the policy of a response's headers. */
function nonceOf(headers: Record<string, string>): string | undefined {
  return headers["content-security-policy"]?.match(/'nonce-([^']+)'/)?.[1];
}

/** Returns the URLs of every request that the page's caches hold. */
async function cachedURLs(page: Page): Promise<string[]> {
  return page.evaluate(async () => {
    const urls: string[] = [];
    for (const name of await caches.keys()) {
      const cache = await caches.open(name);
      for (const request of await cache.keys()) {
        urls.push(request.url);
      }
    }
    return urls;
  });
}

/** The directory of the build that the server serves (vite.config.ts). */
const dist = join(
  import.meta.dirname,
  "../../../server/internal/webassets/dist/app",
);

/** Returns the paths of every file of the build, from its root. */
function buildFiles(dir = dist): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    return entry.isDirectory()
      ? buildFiles(path)
      : [relative(dist, path).replaceAll("\\", "/")];
  });
}

// Verifies: FR-OFF-01/AC1, FR-OFF-01/AC3
test("Chromium finds the tool installable, and the installed app opens at the tool", async ({
  playwright,
  baseURL,
  extraHTTPHeaders,
}, info) => {
  // Chromium never installs from a context off the record, as each of
  // Playwright's new contexts is, so the check runs in a profile on disk.
  const profile = mkdtempSync(join(tmpdir(), "storycreep-e2e-pwa-"));
  const {
    channel,
    viewport,
    userAgent,
    deviceScaleFactor,
    isMobile,
    hasTouch,
  } = info.project.use;
  let context: BrowserContext | undefined;
  try {
    context = await playwright.chromium.launchPersistentContext(profile, {
      channel,
      viewport,
      userAgent,
      deviceScaleFactor,
      isMobile,
      hasTouch,
      baseURL,
      extraHTTPHeaders,
      serviceWorkers: "allow",
    });
    const page = context.pages()[0] ?? (await context.newPage());
    const refused: string[] = [];
    await watchPolicy(page, refused);
    await page.goto("/app/");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["login.title"],
    );
    await workerReady(page);
    const cdp = await context.newCDPSession(page);
    const { installabilityErrors } = await cdp.send(
      "Page.getInstallabilityErrors",
    );
    expect(installabilityErrors).toEqual([]);
    const { url, errors } = await cdp.send("Page.getAppManifest");
    expect(url).toBe(`${baseURL}/app/manifest.webmanifest`);
    expect(errors).toEqual([]);

    // The installed app opens at the tool, and is the app of the tool alone.
    const res = await page.request.get(url);
    expect(res.headers()["content-type"]).toBe("application/manifest+json");
    const manifest = (await res.json()) as {
      id: string;
      start_url: string;
      scope: string;
      display: string;
      icons: { src: string; type: string }[];
    };
    for (const field of ["id", "start_url", "scope"] as const) {
      expect(new URL(manifest[field], url).href, field).toBe(`${baseURL}/app/`);
    }
    expect(manifest.display).toBe("standalone");
    for (const icon of manifest.icons) {
      const image = await page.request.get(new URL(icon.src, url).href);
      expect(image.status(), icon.src).toBe(200);
      expect(image.headers()["content-type"]).toBe(icon.type);
    }
    expect(refused).toEqual([]);
  } finally {
    await context?.close();
    rmSync(profile, { recursive: true, force: true });
  }
});

test("the worker keeps every file of the build, and answers the app's pages from it, each with a nonce of its own, offline too", async ({
  page,
  context,
  baseURL,
}) => {
  const fromWorker: string[] = [];
  page.on("response", (response) => {
    if (
      new URL(response.url()).pathname.startsWith("/api/") &&
      response.fromServiceWorker()
    ) {
      fromWorker.push(response.url());
    }
  });
  await page.goto("/app/");
  await workerReady(page);

  // Its list is every file of the build but itself, the entry page by the
  // path that the server answers with it.
  const worker = await (await page.request.get("/app/sw.js")).text();
  const listed = [...worker.matchAll(/"url":"([^"]+)"/g)].map((m) => m[1]);
  expect(listed.sort()).toEqual(
    buildFiles()
      .filter((f) => f !== "sw.js")
      .map((f) => (f === "index.html" ? "./" : f))
      .sort(),
  );

  // The page loaded anew is the worker's, with a nonce of its own, which its
  // scripts and styles carry.
  const nonces = new Set<string>();
  for (const path of ["/app/", "/app/projects/prj_0123"]) {
    const res = await page.goto(path);
    expect(res?.fromServiceWorker(), path).toBe(true);
    const nonce = nonceOf(res?.headers() ?? {});
    expect(nonce, path).toMatch(/^[A-Z2-7]{26}$/);
    nonces.add(nonce ?? "");
    expect(
      await page.evaluate(
        () =>
          (document.querySelector('meta[property="csp-nonce"]') as HTMLElement)
            .nonce,
      ),
    ).toBe(nonce);
    await expect(page.locator("#root")).not.toBeEmpty();
  }
  expect(nonces.size).toBe(2);
  // A path that names a file is the server's.
  const missing = await page.goto("/app/missing.js");
  expect(missing?.fromServiceWorker()).toBe(false);
  expect(missing?.status()).toBe(404);

  // Offline, the shell still loads, and runs: the app says that it could
  // not reach the server, once its requests have been tried again.
  await context.setOffline(true);
  try {
    const res = await page.goto("/app/");
    expect(res?.fromServiceWorker()).toBe(true);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["route_error.title"],
      { timeout: 30_000 },
    );
  } finally {
    await context.setOffline(false);
  }

  // No answer of the API ever came from the worker, nor is any kept.
  expect(fromWorker).toEqual([]);
  const cached = await cachedURLs(page);
  expect(cached.length).toBeGreaterThan(0);
  for (const url of cached) {
    expect(url.startsWith(`${baseURL}/app/`), url).toBe(true);
  }
});

// Verifies: FR-OFF-01/AC2
test("a new build waits for the user's reload, while the page goes on with its own build and keeps what is being typed", async ({
  page,
  context,
}) => {
  // The page's build is an older one, as the worker that the page first
  // registers says: the server's worker, whose list names another revision
  // of the entry page. Playwright can change the script that a page
  // registers, but not the one that the browser asks for itself as it
  // checks for a new version, which is the server's own: the new build.
  const shellEntry = /"revision":"([0-9a-f]+)","url":"\.\/"/;
  const older = "0".repeat(32);
  await context.route("**/app/sw.js", async (route) => {
    const response = await route.fetch();
    const body = (await response.text()).replace(
      shellEntry,
      `"revision":"${older}","url":"./"`,
    );
    expect(body).toContain(older);
    await route.fulfill({ response, body });
  });
  const newer = (await (await page.request.get("/app/sw.js")).text()).match(
    shellEntry,
  )?.[1];
  expect(newer).toMatch(/^[0-9a-f]{32}$/);
  /** Returns the revisions of the entry page that the cache holds. */
  const shellRevisions = async () =>
    (await cachedURLs(page))
      .filter((u) => u.includes("/app/?__WB_REVISION__="))
      .map((u) => new URL(u).searchParams.get("__WB_REVISION__"));

  const session = await signedIn(page, "Una Update");
  const projectId = await makeProject(page.request, session, "Harbour");
  const boardId = newId("board");
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: boardId, name: "Quay" } },
  ]);
  // The worker installs, and answers the board's page once it is loaded
  // anew.
  await page.goto(`/app/projects/${projectId}/boards/${boardId}`);
  await workerReady(page);
  expect((await page.reload())?.fromServiceWorker()).toBe(true);
  const heading = page.getByRole("heading", { level: 1 });
  await expect(heading).toHaveText("Quay");
  expect(await shellRevisions()).toEqual([older]);
  await context.unroute("**/app/sw.js");

  // The user types a description, which they have not saved, as the page
  // finds the new build.
  const description = page.getByLabel(en["board.description"], {
    exact: true,
  });
  await press(description);
  const typed = "Where the boats come in";
  await page.keyboard.type(typed);
  await page.evaluate(() => {
    (window as { sameDocument?: boolean }).sameDocument = true;
  });
  // The browser asks for the worker, as it does once an hour for the page,
  // and finds the server's, of the new build.
  await page.evaluate(async () => {
    await (await navigator.serviceWorker.getRegistration())?.update();
  });
  const notice = page
    .getByRole("status")
    .filter({ hasText: en["shell.update.body"] });
  await expect(notice).toBeVisible();
  expect(await violations(page)).toEqual([]);
  // What the user typed, and the focus, stay, and they go on and save.
  await expect(description).toBeFocused();
  await expect(description).toHaveValue(typed);
  await page.keyboard.type(", at dusk.");
  const whole = `${typed}, at dusk.`;
  await press(page.getByRole("button", { name: en["board.save"] }));
  await expect(page.getByText(en["board.saved"])).toBeVisible();
  await expect
    .poll(async () => {
      const res = await page.request.get(
        `/api/v1/projects/${projectId}/boards/${boardId}`,
      );
      return ((await res.json()) as { description: string }).description;
    })
    .toBe(whole);

  // The server no longer has the page's build: a route that the page has
  // not opened yet loads its files from the worker, as the page goes on.
  await context.route("**/app/assets/**", (route) =>
    route.fulfill({ status: 404 }),
  );
  await press(page.getByRole("link", { name: en["project.nav.variables"] }));
  await expect(heading).toHaveText(en["variables.title"]);
  expect(
    await page.evaluate(
      () => (window as { sameDocument?: boolean }).sameDocument,
    ),
  ).toBe(true);
  await context.unroute("**/app/assets/**");

  // The user reloads: the new build's worker takes over, and the page loads
  // anew, from its shell, whose old revision is gone from the cache.
  await press(notice.getByRole("button", { name: en["shell.update.reload"] }));
  await page.waitForFunction(
    () => (window as { sameDocument?: boolean }).sameDocument === undefined,
  );
  await expect(heading).toHaveText(en["variables.title"]);
  await expect(notice).toHaveCount(0);
  expect(await shellRevisions()).toEqual([newer]);
});
