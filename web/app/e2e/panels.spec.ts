import { randomUUID } from "node:crypto";
import type { APIRequestContext, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { createProject, firstWorkspace, sessionOf } from "./content.ts";
import {
  type ApiSession,
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

// The panels of a project's variables, components, and component types (PRD
// FR-VAR-01 to FR-VAR-04, FR-COMP-01, FR-COMP-02, FR-COMP-04), on the
// desktop, where a panel is docked beside its list, and on the tablet, where
// it is a sheet over the page (NFR-TOUCH-06): a variable's life from its
// creation to its guarded deletion, through its rename's preview and its
// usages, a component's values and key, an attribute's change of type, and
// the time that the list of a thousand variables takes to show and to
// filter (FR-VAR-01 AC3).

/** Registers an account, signs the page in as it, and returns the page's session. */
async function signedIn(page: Page, name: string): Promise<ApiSession> {
  const account = await registerAccount(page.request, name);
  await signIn(page, account.email);
  await atStart(page, name);
  return sessionOf(page.request);
}

/** A command of a batch, without its ID, which apply gives it. */
interface Command {
  type: string;
  payload: Record<string, unknown>;
}

/** Applies a batch of commands to projectId through the API, as request's account. */
async function apply(
  request: APIRequestContext,
  session: ApiSession,
  projectId: string,
  commands: Command[],
): Promise<void> {
  const res = await request.post(`/api/v1/projects/${projectId}/commands`, {
    headers: { "X-CSRF-Token": session.csrf },
    data: {
      commands: commands.map((c) => ({ command_id: randomUUID(), ...c })),
    },
  });
  expect(res.status(), await res.text()).toBe(200);
}

/** The panel of the page: a region beside the list on the desktop, a dialog over the page on the tablet. */
function panelOf(page: Page, name: string) {
  return test.info().project.use.hasTouch
    ? page.getByRole("dialog", { name, exact: true })
    : page.getByRole("region", { name, exact: true });
}

/**
 * The button of an item of a list of variables, components, or types, whose
 * name its first line starts with, after the glyph of a type's icon, which
 * is no part of its name.
 */
function item(page: Page, name: string) {
  return page.locator("[data-item-id]").filter({
    has: page.getByText(new RegExp(`^\\W*${name}$`, "u")),
  });
}

test.describe("the variables", () => {
  // Verifies: NFR-A11Y-08, NFR-TOUCH-06
  test("a variable is created, edited, renamed after a preview of what the rename rewrites, listed where it is used, and deleted only once that is forced", async ({
    page,
  }) => {
    const session = await signedIn(page, "Vera Variables");
    const workspace = await firstWorkspace(page.request);
    const projectId = await createProject(
      page.request,
      session,
      workspace.id,
      "Harbour",
    );
    const boardId = newId("board");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: boardId, name: "Docks" } },
    ]);
    await page.goto(`/app/projects/${projectId}/variables`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["variables.title"],
    );
    expect(await violations(page)).toEqual([]);

    // The variable is created, of a board, and its panel opens.
    await press(page.getByRole("button", { name: en["variables.new"] }));
    const create = page.getByRole("dialog", {
      name: en["variable.create_title"],
    });
    await create.getByLabel(en["variable.key"], { exact: true }).fill("rep");
    await create
      .getByLabel(en["variable.type"], { exact: true })
      .selectOption("int");
    await create.getByLabel(en["variable.default"], { exact: true }).fill("2");
    expect(await violations(page)).toEqual([]);
    await press(create.getByRole("button", { name: en["variable.create"] }));
    await expect(create).toBeHidden();
    const panel = panelOf(page, "rep");
    await expect(panel).toBeVisible();
    expect(await violations(page)).toEqual([]);

    // What becomes of its value is said beside its reset_on_enter.
    await press(panel.getByRole("radio", { name: en["variable.scope_board"] }));
    const flag = panel.getByRole("checkbox", {
      name: en["variable.reset_on_enter"],
    });
    await expect(flag).toHaveAccessibleDescription(
      message("variable.persistence_kept", { board: "Docks" }),
    );
    await press(flag);
    await expect(flag).toHaveAccessibleDescription(
      message("variable.persistence_reset", { board: "Docks" }),
    );
    await press(panel.getByRole("button", { name: en["variable.save"] }));
    await expect(panel.getByText(en["board.saved"])).toBeVisible();

    // An expression of the board reads it, and another writes it.
    await apply(page.request, session, projectId, [
      {
        type: "node.create",
        payload: {
          id: newId("node"),
          board_id: boardId,
          type: "element",
          title: "The gate",
          position: { x: 0, y: 0 },
          on_enter: "rep += 1",
        },
      },
    ]);

    // Its usages list the script, of its node, on its board.
    await press(panel.getByRole("button", { name: en["usage.show"] }));
    const writes = panel.getByRole("list", { name: en["variable.writes"] });
    await expect(writes).toHaveText("The on-enter script of The gate · Docks");

    // The rename shows what it rewrites before it renames.
    await press(panel.getByRole("button", { name: en["variable.rename"] }));
    const rename = page.getByRole("dialog", {
      name: message("rename.title", { key: "rep" }),
    });
    await rename.getByLabel(en["rename.new_key"]).fill("trust");
    await press(rename.getByRole("button", { name: en["rename.preview"] }));
    await expect(rename.getByText("trust += 1", { exact: true })).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(rename.getByRole("button", { name: en["rename.submit"] }));
    await expect(rename).toBeHidden();
    await expect(panelOf(page, "trust")).toBeVisible();

    // Its deletion is refused while the script uses it, and forced then.
    const renamed = panelOf(page, "trust");
    await press(renamed.getByRole("button", { name: en["variable.delete"] }));
    const deletion = page.getByRole("alertdialog");
    await press(deletion.getByRole("button", { name: en["deletion.confirm"] }));
    await expect(
      deletion.getByText(
        "It is used 2 times in the project: read once, and changed once. So it was not deleted.",
      ),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(
      deletion.getByRole("button", { name: en["deletion.confirm_anyway"] }),
    );
    await expect(deletion).toBeHidden();
    await expect(renamed).toBeHidden();
    await expect(item(page, "trust")).toHaveCount(0);
  });

  // Verifies: NFR-TOUCH-06
  test("a panel opens and closes with the list where it was, and the docked panel stays in view as the list scrolls", async ({
    page,
  }) => {
    const session = await signedIn(page, "Lena Long");
    const workspace = await firstWorkspace(page.request);
    const projectId = await createProject(
      page.request,
      session,
      workspace.id,
      "Archive",
    );
    await apply(
      page.request,
      session,
      projectId,
      Array.from({ length: 120 }, (_, i) => ({
        type: "variable.create",
        payload: {
          id: newId("variable"),
          key: `flag_${String(i).padStart(3, "0")}`,
          type: "bool",
          scope: "global",
        },
      })),
    );
    await page.goto(`/app/projects/${projectId}/variables`);
    await expect(page.locator("[data-item-id]")).toHaveCount(120);
    const far = item(page, "flag_090");
    await far.scrollIntoViewIfNeeded();
    const scrolled = await page.evaluate(() => window.scrollY);
    expect(scrolled).toBeGreaterThan(0);

    // The panel opens over the list, or beside it, as the list was.
    await press(far);
    const panel = panelOf(page, "flag_090");
    const heading = panel.getByRole("heading", { name: "flag_090" });
    await expect(heading).toBeFocused();
    await expect(heading).toBeInViewport();
    await expect(far).toBeInViewport();

    // The docked panel stays at the top of the window as the list scrolls.
    if (!test.info().project.use.hasTouch) {
      await page.mouse.wheel(0, 1500);
      await expect
        .poll(() => page.evaluate(() => window.scrollY))
        .toBeGreaterThan(scrolled + 1000);
      await expect(heading).toBeInViewport();
      await far.scrollIntoViewIfNeeded();
    }

    // Once it closes, the item that opened it has the focus, in view.
    await press(panel.getByRole("button", { name: en["panel.close"] }));
    await expect(panel).toBeHidden();
    await expect(far).toBeFocused();
    await expect(far).toBeInViewport();
  });

  // Verifies: FR-VAR-01/AC3
  test("the list of a thousand variables shows, and filters, within 300 ms", async ({
    page,
  }, testInfo) => {
    test.setTimeout(120_000);
    const session = await signedIn(page, "Tom Thousand");
    const workspace = await firstWorkspace(page.request);
    const projectId = await createProject(
      page.request,
      session,
      workspace.id,
      "Ledger",
    );
    const count = 1_000;
    const folders = Array.from({ length: 10 }, (_, i) => ({
      id: newId("folder"),
      name: `Chapter ${i + 1}`,
    }));
    await apply(
      page.request,
      session,
      projectId,
      folders.map((f) => ({
        type: "folder.create",
        payload: { id: f.id, kind: "variable", name: f.name },
      })),
    );
    await apply(
      page.request,
      session,
      projectId,
      Array.from({ length: count }, (_, i) => ({
        type: "variable.create",
        payload: {
          id: newId("variable"),
          key: `flag_${String(i).padStart(4, "0")}`,
          type: i % 2 === 0 ? "bool" : "int",
          scope: "global",
          description: `Set in scene ${i}`,
          folder_id: folders[i % folders.length].id,
        },
      })),
    );
    // The project's document reads the variables once, as the page opens
    // them; the list is timed as it shows what the document holds, as when
    // the user comes back to it.
    await page.goto(`/app/projects/${projectId}/variables`);
    await expect(page.locator("[data-item-id]")).toHaveCount(count);
    await press(page.getByRole("link", { name: en["project.nav.boards"] }));
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Ledger");
    const runs = 5;
    const shown: number[] = [];
    const filtered: number[] = [];
    for (let run = 0; run < runs; run++) {
      // From the link's activation to the frame after the list has every
      // item, as the user sees it.
      shown.push(
        await page.evaluate(
          ({ count, name }) =>
            new Promise<number>((resolve) => {
              const link = [
                ...document.querySelectorAll<HTMLAnchorElement>("nav a"),
              ].find((a) => a.textContent === name);
              const start = performance.now();
              link?.click();
              const check = () => {
                if (
                  document.querySelectorAll("[data-item-id]").length === count
                ) {
                  requestAnimationFrame(() =>
                    resolve(performance.now() - start),
                  );
                } else {
                  requestAnimationFrame(check);
                }
              };
              check();
            }),
          { count, name: en["project.nav.variables"] },
        ),
      );
      // From the filter's input to the frame after the list shows the ten
      // variables whose keys hold it.
      filtered.push(
        await page.evaluate(
          ({ label, text }) =>
            new Promise<number>((resolve) => {
              const input = [
                ...document.querySelectorAll<HTMLInputElement>("input"),
              ].find((i) => i.labels?.[0]?.textContent === label);
              if (input === undefined) {
                resolve(Number.POSITIVE_INFINITY);
                return;
              }
              const set = Object.getOwnPropertyDescriptor(
                HTMLInputElement.prototype,
                "value",
              )?.set;
              const start = performance.now();
              set?.call(input, text);
              input.dispatchEvent(new Event("input", { bubbles: true }));
              const check = () => {
                if (document.querySelectorAll("[data-item-id]").length === 10) {
                  requestAnimationFrame(() =>
                    resolve(performance.now() - start),
                  );
                } else {
                  requestAnimationFrame(check);
                }
              };
              check();
            }),
          { label: en["variables.filter"], text: "flag_012" },
        ),
      );
      await press(page.getByRole("link", { name: en["project.nav.boards"] }));
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        "Ledger",
      );
    }
    const worst = (times: number[]) => Math.round(Math.max(...times));
    const report = `shown in ${shown.map(Math.round).join(", ")} ms, filtered in ${filtered.map(Math.round).join(", ")} ms`;
    testInfo.annotations.push({
      type: "FR-VAR-01/AC3",
      description: `${count} variables: ${report}`,
    });
    console.log(`FR-VAR-01 AC3, ${count} variables: ${report}`);
    expect(worst(shown), "the slowest showing of the list").toBeLessThan(300);
    expect(worst(filtered), "the slowest filtering of the list").toBeLessThan(
      300,
    );
  });
});

test.describe("the components", () => {
  // Verifies: NFR-A11Y-08
  test("an attribute is added to a type, a component's value of it is set, its key changed after a preview, and the attribute's type changed after the list of what would not convert", async ({
    page,
  }) => {
    const session = await signedIn(page, "Cora Components");
    const workspace = await firstWorkspace(page.request);
    const projectId = await createProject(
      page.request,
      session,
      workspace.id,
      "Harbour",
    );

    // The project's default type of characters gains an attribute.
    await page.goto(`/app/projects/${projectId}/component-types`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["component_types.title"],
    );
    await press(item(page, "Character"));
    const typePanel = panelOf(page, "Character");
    await expect(typePanel).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(
      typePanel.getByRole("button", {
        name: en["component_type.add_attribute"],
      }),
    );
    const add = page.getByRole("dialog", {
      name: message("attribute.create_title", { type: "Character" }),
    });
    await add.getByLabel(en["attribute.name"], { exact: true }).fill("Trust");
    await add.getByLabel(en["attribute.key_optional"]).fill("trust");
    await add.getByLabel(en["attribute.default"], { exact: true }).fill("1");
    expect(await violations(page)).toEqual([]);
    await press(add.getByRole("button", { name: en["attribute.create"] }));
    await expect(add).toBeHidden();
    await expect(typePanel.getByText("Trust", { exact: true })).toBeVisible();

    // A component of the type is created, and its value set. The panel
    // closes first, as a sheet over the page holds the focus until it does.
    await press(typePanel.getByRole("button", { name: en["panel.close"] }));
    await expect(typePanel).toBeHidden();
    await press(
      page.getByRole("link", {
        name: en["project.nav.components"],
        exact: true,
      }),
    );
    await press(page.getByRole("button", { name: en["components.new"] }));
    const create = page.getByRole("dialog", {
      name: en["component.create_title"],
    });
    await create
      .getByLabel(en["component.name"], { exact: true })
      .fill("Elara");
    await create.getByLabel(en["component.type"]).selectOption({
      label: "Character",
    });
    await press(create.getByRole("button", { name: en["component.create"] }));
    await expect(create).toBeHidden();
    const panel = panelOf(page, "Elara");
    const trust = panel.getByLabel("Trust", { exact: true });
    await expect(trust).toHaveValue("1");
    expect(await violations(page)).toEqual([]);
    await trust.fill("x");
    await press(panel.getByRole("button", { name: en["component.save"] }));
    await expect(
      panel.getByText("Enter a whole number, such as 3 or -12."),
    ).toBeVisible();
    await trust.fill("4");
    await press(panel.getByRole("button", { name: en["component.save"] }));
    await expect(panel.getByText(en["board.saved"])).toBeVisible();

    // Its key changes after the preview, which nothing names yet.
    await press(
      panel.getByRole("button", { name: en["component.rename_key"] }),
    );
    const rename = page.getByRole("dialog", {
      name: message("rename.title", { key: "elara" }),
    });
    await rename.getByLabel(en["rename.new_key"]).fill("lady_elara");
    await press(rename.getByRole("button", { name: en["rename.preview"] }));
    await expect(rename.getByText(en["rename.renamed_empty"])).toBeVisible();
    await press(rename.getByRole("button", { name: en["rename.submit"] }));
    await expect(rename).toBeHidden();
    await expect(
      panel.getByText("lady_elara", { exact: true }).first(),
    ).toBeVisible();

    // The attribute becomes a bool, whose conversion of 4 fails.
    await press(panel.getByRole("button", { name: en["panel.close"] }));
    await expect(panel).toBeHidden();
    await press(
      page.getByRole("link", {
        name: en["project.nav.component_types"],
        exact: true,
      }),
    );
    await press(item(page, "Character"));
    await press(
      panelOf(page, "Character").getByRole("button", {
        name: message("boards.actions", { name: "Trust" }),
      }),
    );
    await press(
      page.getByRole("menuitem", {
        name: en["component_type.retype_attribute"],
      }),
    );
    const retype = page.getByRole("dialog", {
      name: message("attribute.retype_title", { name: "Trust" }),
    });
    await retype.getByLabel(en["attribute.new_type"]).selectOption("bool");
    await expect(
      retype.getByText(
        message("attribute.unconvertible", {
          name: "Elara",
          key: "lady_elara",
        }),
      ),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await press(retype.getByRole("button", { name: en["attribute.retype"] }));
    await expect(retype).toBeHidden();
  });
});
