import type {
  Browser,
  BrowserContext,
  BrowserContextOptions,
  Page,
  Response,
  WebSocket as Socket,
} from "@playwright/test";
import { createProject, firstWorkspace, sessionOf } from "./content.ts";
import { peerPort, peerURL, port } from "./environment.ts";
import {
  type Account,
  atStart,
  en,
  expect,
  message,
  press,
  registerAccount,
  signIn,
  test,
  violations,
  watchPolicy,
} from "./fixtures.ts";

// A project's pages as they follow its document (PRD §10.6, FR-RT-02): a
// change that one session commits shows in the others, whichever process of
// the instance each is connected to, within 2 seconds (AC1, NFR-PERF-08),
// without taking away what a user is typing in another field, or the focus
// (AC3); and the undo history of the user's changes, which a reload keeps
// (FR-EDIT-08 AC3). The second process is the peer that Playwright starts
// beside the server, on its database (e2e/peer.ts).

/** Registers an account, signs the page in as it, and returns it. */
async function signedIn(page: Page, name: string): Promise<Account> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return account;
}

/** Creates a project named name in the first workspace of the account that page is signed in as, and returns its ID. */
async function projectOf(page: Page, name: string): Promise<string> {
  const session = await sessionOf(page.request);
  const workspace = await firstWorkspace(page.request);
  return createProject(page.request, session, workspace.id, name);
}

/**
 * Opens a page of a new context of browser, with options, as baseURL and
 * the test's X-Forwarded-For, whose violations of the
 * Content-Security-Policy refused records, signed in as account, and adds
 * the context to contexts, which the test closes.
 */
async function signedInPage(
  browser: Browser,
  options: BrowserContextOptions,
  account: Account,
  refused: string[],
  contexts: BrowserContext[],
): Promise<Page> {
  const context = await browser.newContext(options);
  contexts.push(context);
  const page = await context.newPage();
  await watchPolicy(page, refused);
  await signIn(page, account.email);
  await atStart(page, account.name);
  return page;
}

/** Reports whether a frame of the real-time channel is the answer that a subscription to projectId holds. */
function subscribes(payload: string | Buffer, projectId: string): boolean {
  try {
    const m = JSON.parse(String(payload)) as {
      type?: unknown;
      project_id?: unknown;
    };
    return m.type === "subscribed" && m.project_id === projectId;
  } catch {
    return false;
  }
}

/**
 * Records the URL of each socket of the real-time channel that page opens
 * from now on, once the server has answered its subscription to projectId:
 * a change made after that reaches the page as it commits, which a bound
 * of 2 seconds starts from. Call it before the page opens the project.
 */
function subscriptions(page: Page, projectId: string): string[] {
  const urls: string[] = [];
  page.on("websocket", (socket: Socket) => {
    if (!socket.url().endsWith("/api/v1/ws")) {
      return;
    }
    socket.on("framereceived", ({ payload }) => {
      if (subscribes(payload, projectId)) {
        urls.push(socket.url());
      }
    });
  });
  return urls;
}

/** Waits until urls (subscriptions) has more than count, and returns the URL of the socket after them. */
async function subscribed(urls: string[], count = 0): Promise<string> {
  await expect
    .poll(() => urls.length, {
      message: "the page's socket subscribed to the project",
      timeout: 15_000,
    })
    .toBeGreaterThan(count);
  return urls[count];
}

/** Reports whether response answers a batch of commands of projectId. */
function answersBatch(projectId: string) {
  return (response: Response) =>
    response.request().method() === "POST" &&
    new URL(response.url()).pathname ===
      `/api/v1/projects/${projectId}/commands`;
}

/** The row of the tree of the board or the folder named name. */
function row(page: Page, name: string) {
  return page
    .locator("[data-tree-id]")
    .filter({ has: page.getByText(name, { exact: true }) });
}

/** Creates a board named name in the tree that page shows, through its dialog, once the server has it, and returns its ID. */
async function createBoard(page: Page, name: string): Promise<string> {
  await press(
    page.getByRole("button", { name: en["boards.new_board"], exact: true }),
  );
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel(en["boards.board_name"], { exact: true }).fill(name);
  await press(dialog.getByRole("button", { name: en["boards.create_board"] }));
  // The dialog closes once the server has answered.
  await expect(dialog).toBeHidden();
  await expect(row(page, name)).toBeVisible();
  const id = await row(page, name).getAttribute("data-tree-id");
  expect(id).not.toBeNull();
  return id ?? "";
}

/**
 * Renames the board whose page page shows to name, through the page's form,
 * and returns the server's answer, once the form shows the board as the
 * answer left it: the form is shown anew for each revision of the board, so
 * that the next change is typed into the new one, not the one that goes.
 */
async function renameOnPage(
  page: Page,
  projectId: string,
  name: string,
): Promise<Response> {
  const field = page.getByLabel(en["board.name"], { exact: true });
  const before = await field.elementHandle();
  await field.fill(name);
  const answered = page.waitForResponse(answersBatch(projectId));
  await press(page.getByRole("button", { name: en["board.save"] }));
  const response = await answered;
  expect(response.ok(), await response.text()).toBe(true);
  await expect
    .poll(() => before?.evaluate((e) => e.isConnected), {
      message: "the board's form is shown anew",
    })
    .toBe(false);
  await before?.dispose();
  return response;
}

/** The boards of a project, as the API lists them. */
async function boardsOf(page: Page, projectId: string) {
  const res = await page.request.get(
    `/api/v1/projects/${projectId}/boards?limit=200`,
  );
  expect(res.status(), await res.text()).toBe(200);
  return (
    (await res.json()) as {
      items: { id: string; name: string; description: string }[];
    }
  ).items;
}

test.describe("two processes of the instance", () => {
  // The processes are the instance's, not the device's.
  test.skip(
    ({ hasTouch }) => hasTouch,
    "the desktop project covers the processes of the instance",
  );

  // Verifies: FR-RT-02/AC1
  test("pages connected to two server processes both show a rename made in a third within 2 s", async ({
    page,
    browser,
    baseURL,
    extraHTTPHeaders,
    refused,
  }) => {
    const account = await signedIn(page, "Rhea Realtime");
    const projectId = await projectOf(page, "Estuary");
    const contexts: BrowserContext[] = [];
    try {
      // A, on the server, shows the tree.
      const a = page;
      const aSockets = subscriptions(a, projectId);
      await a.goto(`/app/projects/${projectId}`);
      const boardId = await createBoard(a, "Mudflat");
      expect(new URL(await subscribed(aSockets)).port).toBe(String(port));

      // B, on the peer, the other process, shows the board's page.
      const b = await signedInPage(
        browser,
        { baseURL: peerURL, extraHTTPHeaders },
        account,
        refused,
        contexts,
      );
      const bSockets = subscriptions(b, projectId);
      await b.goto(`/app/projects/${projectId}/boards/${boardId}`);
      await expect(b.getByRole("heading", { level: 1 })).toHaveText("Mudflat");
      expect(new URL(await subscribed(bSockets)).port).toBe(String(peerPort));

      // C, a third session, on the server, renames the board on its page.
      const c = await signedInPage(
        browser,
        { baseURL, extraHTTPHeaders },
        account,
        refused,
        contexts,
      );
      await c.goto(`/app/projects/${projectId}/boards/${boardId}`);
      await expect(c.getByRole("heading", { level: 1 })).toHaveText("Mudflat");
      await c
        .getByLabel(en["board.name"], { exact: true })
        .fill("Mudflat at low tide");
      const answered = c.waitForResponse(answersBatch(projectId));
      await press(c.getByRole("button", { name: en["board.save"] }));
      const response = await answered;
      expect(response.ok(), await response.text()).toBe(true);
      // Both show it within 2 seconds of the answer, which comes once the
      // batch has committed.
      await Promise.all([
        expect(
          a.getByRole("link", { name: "Mudflat at low tide", exact: true }),
        ).toBeVisible({ timeout: 2_000 }),
        expect(b.getByRole("heading", { level: 1 })).toHaveText(
          "Mudflat at low tide",
          { timeout: 2_000 },
        ),
      ]);
    } finally {
      for (const context of contexts) {
        await context.close();
      }
    }
  });

  // Verifies: NFR-PERF-08
  test("a change committed in one session shows in another, of the same process and of the other, within 2 s at the 95th percentile", async ({
    page,
    browser,
    baseURL,
    extraHTTPHeaders,
    refused,
  }, testInfo) => {
    test.setTimeout(120_000);
    const edits = 20;
    const account = await signedIn(page, "Lars Latency");
    const projectId = await projectOf(page, "Tideline");
    const contexts: BrowserContext[] = [];
    try {
      // The page that commits shows the board's page, on the server.
      await page.goto(`/app/projects/${projectId}`);
      const boardId = await createBoard(page, "Edit 0");
      await page.goto(`/app/projects/${projectId}/boards/${boardId}`);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        "Edit 0",
      );

      // The pages that watch show the tree: one of the server, one of the
      // peer, whose operations come through PostgreSQL's NOTIFY.
      const watchers: { name: string; page: Page }[] = [];
      for (const [name, url] of [
        ["same process", baseURL],
        ["other process", peerURL],
      ] as const) {
        const watcher = await signedInPage(
          browser,
          { baseURL: url, extraHTTPHeaders },
          account,
          refused,
          contexts,
        );
        const sockets = subscriptions(watcher, projectId);
        await watcher.goto(`/app/projects/${projectId}`);
        await expect(row(watcher, "Edit 0")).toBeVisible();
        await subscribed(sockets);
        watchers.push({ name, page: watcher });
      }

      interface Sample {
        edit: number;
        watcher: string;
        /** From the request's start, before the batch committed: an upper bound. */
        fromRequest: number;
        /** From the start of the answer, after the batch committed: a lower bound. */
        fromAnswer: number;
      }
      const samples: Sample[] = [];
      for (let edit = 1; edit <= edits; edit++) {
        const name = `Edit ${edit}`;
        // Each watcher records when its tree first shows the new name,
        // with the machine's clock, which the request's timing reads too.
        await Promise.all(
          watchers.map((w) =>
            w.page.evaluate((name) => {
              const shown = () =>
                [...document.querySelectorAll("[data-tree-id] a")].some(
                  (e) => e.textContent === name,
                );
              const at = window as unknown as { e2eShownAt?: number };
              at.e2eShownAt = undefined;
              const observer = new MutationObserver(() => {
                if (shown()) {
                  at.e2eShownAt = Date.now();
                  observer.disconnect();
                }
              });
              observer.observe(document.body, {
                childList: true,
                subtree: true,
                characterData: true,
              });
            }, name),
          ),
        );
        const response = await renameOnPage(page, projectId, name);
        const timing = response.request().timing();
        for (const w of watchers) {
          const shownAt = (await (
            await w.page.waitForFunction(
              () => (window as unknown as { e2eShownAt?: number }).e2eShownAt,
              undefined,
              { timeout: 10_000 },
            )
          ).jsonValue()) as number;
          samples.push({
            edit,
            watcher: w.name,
            fromRequest: shownAt - timing.startTime,
            fromAnswer: shownAt - (timing.startTime + timing.responseStart),
          });
        }
      }

      const percentile = (sorted: number[], p: number) =>
        sorted[Math.max(0, Math.ceil((p / 100) * sorted.length) - 1)];
      const summary = Object.fromEntries(
        watchers.map((w) => {
          const of = (key: "fromRequest" | "fromAnswer") => {
            const sorted = samples
              .filter((s) => s.watcher === w.name)
              .map((s) => s[key])
              .sort((x, y) => x - y);
            return {
              p50: Math.round(percentile(sorted, 50)),
              p95: Math.round(percentile(sorted, 95)),
              max: Math.round(sorted[sorted.length - 1]),
            };
          };
          return [
            w.name,
            { fromRequest: of("fromRequest"), fromAnswer: of("fromAnswer") },
          ];
        }),
      );
      const report = Object.entries(summary)
        .map(
          ([name, { fromRequest: r, fromAnswer: a }]) =>
            `${name}: p50 ${r.p50} ms, p95 ${r.p95} ms, max ${r.max} ms from the request (from the answer: p50 ${a.p50} ms, p95 ${a.p95} ms, max ${a.max} ms)`,
        )
        .join("; ");
      testInfo.annotations.push({
        type: "NFR-PERF-08",
        description: `${edits} edits: ${report}`,
      });
      console.log(`NFR-PERF-08, ${edits} edits: ${report}`);
      await testInfo.attach("nfr-perf-08.json", {
        body: JSON.stringify({ edits, summary, samples }, null, 2),
        contentType: "application/json",
      });
      // Measured from the request, which includes the commit itself, the
      // bound holds whatever part of the time the commit took.
      for (const [name, { fromRequest }] of Object.entries(summary)) {
        expect(fromRequest.p95, `the p95 of ${name}`).toBeLessThan(2_000);
      }
    } finally {
      for (const context of contexts) {
        await context.close();
      }
    }
  });
});

// Verifies: FR-RT-02/AC3
test("a rename made elsewhere shows on a board's page without taking away what is being typed in its description, or the focus", async ({
  page,
  browser,
  baseURL,
  extraHTTPHeaders,
  refused,
  hasTouch,
  isMobile,
  viewport,
  userAgent,
  deviceScaleFactor,
}) => {
  const account = await signedIn(page, "Fay Focus");
  const projectId = await projectOf(page, "Harbour");
  const contexts: BrowserContext[] = [];
  try {
    // A shows the board's page, from the tree.
    const sockets = subscriptions(page, projectId);
    await page.goto(`/app/projects/${projectId}`);
    await createBoard(page, "Quay");
    await press(page.getByRole("link", { name: "Quay", exact: true }));
    const heading = page.getByRole("heading", { level: 1 });
    await expect(heading).toHaveText("Quay");
    await subscribed(sockets);

    // B, another session of the same device, shows the tree.
    const b = await signedInPage(
      browser,
      {
        baseURL,
        extraHTTPHeaders,
        hasTouch,
        isMobile,
        viewport,
        userAgent,
        deviceScaleFactor,
      },
      account,
      refused,
      contexts,
    );
    await b.goto(`/app/projects/${projectId}`);
    await expect(row(b, "Quay")).toBeVisible();

    // A types in the description, and has not saved it.
    const description = page.getByLabel(en["board.description"], {
      exact: true,
    });
    await press(description);
    const typed = "Where the boats come in";
    await page.keyboard.type(typed);
    await expect(description).toHaveValue(typed);
    await expect(description).toBeFocused();

    // B renames the board: its name, which A is not editing.
    await press(
      b.getByRole("button", {
        name: message("boards.actions", { name: "Quay" }),
      }),
    );
    await press(
      b.getByRole("menu").getByRole("menuitem", { name: en["boards.rename"] }),
    );
    const rename = b.getByRole("dialog");
    await rename
      .getByLabel(en["boards.new_name"], { exact: true })
      .fill("Night quay");
    await press(
      rename.getByRole("button", { name: en["boards.rename_submit"] }),
    );
    await expect(rename).toBeHidden();

    // A's page shows the new name, and the description still holds what A
    // typed, with the focus and the caret where they were.
    await expect(heading).toHaveText("Night quay", { timeout: 2_000 });
    await expect(description).toHaveValue(typed);
    await expect(description).toBeFocused();
    expect(
      await description.evaluate((e) => [
        (e as HTMLTextAreaElement).selectionStart,
        (e as HTMLTextAreaElement).selectionEnd,
      ]),
    ).toEqual([typed.length, typed.length]);
    // A goes on typing where it left off, and saves: the board keeps B's
    // name, and gets A's description.
    await page.keyboard.type(", at dusk.");
    const whole = `${typed}, at dusk.`;
    await expect(description).toHaveValue(whole);
    await press(page.getByRole("button", { name: en["board.save"] }));
    await expect(page.getByText(en["board.saved"])).toBeVisible();
    await expect(
      page.getByLabel(en["board.name"], { exact: true }),
    ).toHaveValue("Night quay");
    await expect
      .poll(async () =>
        (await boardsOf(page, projectId)).map((board) => ({
          name: board.name,
          description: board.description,
        })),
      )
      .toEqual([{ name: "Night quay", description: whole }]);
  } finally {
    for (const context of contexts) {
      await context.close();
    }
  }
});

// Verifies: FR-EDIT-08/AC3, NFR-A11Y-08
test("undo after a reload takes back at least the last 50 changes", async ({
  page,
  hasTouch,
}) => {
  // The history is the page's, which the tab's storage keeps, not the
  // device's; and 50 changes, and 50 undos, take a while.
  test.skip(hasTouch, "the desktop project covers the history");
  test.setTimeout(180_000);
  await signedIn(page, "Uma Undo");
  const projectId = await projectOf(page, "Shoals");
  await page.goto(`/app/projects/${projectId}`);
  await createBoard(page, "Step 0");
  await press(page.getByRole("link", { name: "Step 0", exact: true }));
  const heading = page.getByRole("heading", { level: 1 });
  await expect(heading).toHaveText("Step 0");

  const changes = 52;
  for (let i = 1; i <= changes; i++) {
    await renameOnPage(page, projectId, `Step ${i}`);
  }
  await expect(heading).toHaveText(`Step ${changes}`);

  await page.reload();
  await expect(heading).toHaveText(`Step ${changes}`);
  const undo = page.getByRole("button", {
    name: en["history.undo"],
    exact: true,
  });
  for (let i = 1; i <= 50; i++) {
    // The button is disabled while a step is sent.
    await expect(undo).toBeEnabled();
    const answered = page.waitForResponse(answersBatch(projectId));
    await press(undo);
    const response = await answered;
    expect(response.ok(), await response.text()).toBe(true);
    await expect(heading).toHaveText(`Step ${changes - i}`);
  }
  await expect(undo).toBeEnabled();
  await expect(page.getByText(en["history.undone"])).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await expect
    .poll(async () => (await boardsOf(page, projectId)).map((b) => b.name))
    .toEqual(["Step 2"]);
});
