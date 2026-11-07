import type { APIRequestContext, Page } from "@playwright/test";
import {
  acceptInvite,
  createProject,
  drag,
  firstWorkspace,
  inviteByLink,
  sessionOf,
  signedIn,
} from "./content.ts";
import { port } from "./environment.ts";
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
  watchPolicy,
} from "./fixtures.ts";
import { startInstance } from "./instance.ts";

// The pages of a project (PRD FR-PROJ-01, FR-PROJ-02, FR-PROJ-06,
// FR-TEN-04): its life from creation to the trash and back, the tree of its
// boards, which a drag or a dialog changes, a board's page and its trash,
// and the members' grants on it; and the limit of a workspace's projects on
// a self-hosted instance that caps it, which offers no plan (FR-ENT-03).

/** The boards and the folders of a project, as the API lists them. */
async function treeOf(request: APIRequestContext, projectId: string) {
  const boards = await request.get(
    `/api/v1/projects/${projectId}/boards?limit=200`,
  );
  const folders = await request.get(
    `/api/v1/projects/${projectId}/folders?kind=board&limit=200`,
  );
  return {
    boards: (
      (await boards.json()) as {
        items: {
          id: string;
          name: string;
          folder_id?: string;
          order: string;
        }[];
      }
    ).items,
    folders: (
      (await folders.json()) as { items: { id: string; name: string }[] }
    ).items,
  };
}

/** The row of the tree of the board or the folder named name. */
function row(page: Page, name: string) {
  return page
    .locator("[data-tree-id]")
    .filter({ has: page.getByText(name, { exact: true }) });
}

// Verifies: NFR-A11Y-08
test("a project is created, archived, unarchived, deleted to the trash, and restored from it", async ({
  page,
}) => {
  await signedIn(page, "Pia Projects");
  await expect(page.getByText(en["projects.empty"])).toBeVisible();
  await press(page.getByRole("button", { name: en["projects.create"] }));
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await dialog.getByLabel(en["projects.name"], { exact: true }).fill("Harbour");
  await dialog
    .getByLabel(en["projects.description"], { exact: true })
    .fill("A story by the sea.");
  await press(
    dialog.getByRole("button", { name: en["projects.create_submit"] }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Harbour");
  await expect(page.getByText(en["boards.empty"])).toBeVisible();
  expect(await violations(page)).toEqual([]);

  await press(
    page.getByRole("link", { name: en["project.nav.settings"], exact: true }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["project_settings.title"],
  );
  expect(await violations(page)).toEqual([]);
  await press(
    page.getByRole("button", { name: en["project_settings.archive"] }),
  );
  await expect(page.getByText(en["project.archived_manager"])).toBeVisible();
  // An archived project is read-only (PRD FR-PROJ-01 AC2).
  await press(
    page.getByRole("link", { name: en["project.nav.boards"], exact: true }),
  );
  await expect(
    page.getByRole("button", { name: en["boards.new_board"] }),
  ).toHaveCount(0);
  await press(
    page.getByRole("link", { name: en["project.nav.settings"], exact: true }),
  );
  await press(
    page.getByRole("button", { name: en["project_settings.unarchive"] }),
  );
  await expect(page.getByText(en["project.archived_manager"])).toHaveCount(0);

  await press(
    page.getByRole("button", { name: en["project_settings.delete"] }),
  );
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(
    confirm.getByRole("button", {
      name: en["project_settings.delete_confirm"],
    }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["projects.title"],
  );
  await expect(page.getByText(en["projects.empty"])).toBeVisible();

  await press(
    page.getByRole("link", { name: en["workspace.nav.trash"], exact: true }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    en["trash.title"],
  );
  const trashed = page
    .getByRole("list", { name: en["trash.projects_list"] })
    .getByRole("listitem");
  await expect(trashed).toHaveCount(1);
  await expect(trashed).toContainText("Harbour");
  expect(await violations(page)).toEqual([]);
  await press(trashed.getByRole("button", { name: en["trash.restore"] }));
  await expect(
    page.getByText(message("trash.restored", { name: "Harbour" })),
  ).toBeVisible();
  await expect(page.getByText(en["trash.projects_empty"])).toBeVisible();
  await press(
    page.getByRole("link", { name: en["workspace.nav.projects"], exact: true }),
  );
  await expect(page.getByRole("link", { name: "Harbour" })).toBeVisible();
});

// Verifies: NFR-A11Y-08
test("the tree of boards makes folders and boards, moves a board by dragging it and with its Move, and renames it", async ({
  page,
}) => {
  const session = await signedIn(page, "Tom Tree");
  const workspace = await firstWorkspace(page.request);
  const projectId = await createProject(
    page.request,
    session,
    workspace.id,
    "Lighthouse",
  );
  await page.goto(`/app/projects/${projectId}`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    "Lighthouse",
  );

  const create = async (
    button: string,
    label: string,
    name: string,
    submit: string,
  ) => {
    await press(page.getByRole("button", { name: button, exact: true }));
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByLabel(label, { exact: true }).fill(name);
    await press(dialog.getByRole("button", { name: submit }));
    await expect(dialog).toBeHidden();
    await expect(row(page, name)).toBeVisible();
  };
  await create(
    en["boards.new_folder"],
    en["boards.folder_name"],
    "Act one",
    en["boards.create_folder"],
  );
  await create(
    en["boards.new_board"],
    en["boards.board_name"],
    "Opening",
    en["boards.create_board"],
  );
  await create(
    en["boards.new_board"],
    en["boards.board_name"],
    "Finale",
    en["boards.create_board"],
  );
  expect(await violations(page)).toEqual([]);

  // Dragged by its handle onto the folder, the board goes into it.
  await drag(
    page,
    row(page, "Opening").locator("[data-drag-handle]"),
    row(page, "Act one"),
  );
  await expect
    .poll(async () => {
      const { boards, folders } = await treeOf(page.request, projectId);
      return (
        boards.find((b) => b.name === "Opening")?.folder_id === folders[0]?.id
      );
    })
    .toBe(true);

  // The menu's Move does the same without dragging: into the folder, first.
  await press(
    page.getByRole("button", {
      name: message("boards.actions", { name: "Finale" }),
    }),
  );
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(menu.getByRole("menuitem", { name: en["boards.move"] }));
  const move = page.getByRole("dialog");
  await expect(move).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await move
    .getByLabel(en["boards.move_to"], { exact: true })
    .selectOption({ label: "Act one" });
  await move
    .getByLabel(en["boards.place"], { exact: true })
    .selectOption({ label: en["boards.place_start"] });
  await press(move.getByRole("button", { name: en["boards.move_submit"] }));
  await expect(move).toBeHidden();
  await expect
    .poll(async () => {
      const { boards, folders } = await treeOf(page.request, projectId);
      const inside = boards
        .filter((b) => b.folder_id === folders[0]?.id)
        .sort((a, b) => (a.order < b.order ? -1 : 1))
        .map((b) => b.name);
      return inside;
    })
    .toEqual(["Finale", "Opening"]);
  // The menu's button has the focus back.
  await expect(
    page.getByRole("button", {
      name: message("boards.actions", { name: "Finale" }),
    }),
  ).toBeFocused();

  await press(
    page.getByRole("button", {
      name: message("boards.actions", { name: "Opening" }),
    }),
  );
  await press(
    page.getByRole("menu").getByRole("menuitem", { name: en["boards.rename"] }),
  );
  const rename = page.getByRole("dialog");
  await rename
    .getByLabel(en["boards.new_name"], { exact: true })
    .fill("Opening night");
  await press(rename.getByRole("button", { name: en["boards.rename_submit"] }));
  await expect(rename).toBeHidden();
  await expect(page.getByRole("link", { name: "Opening night" })).toBeVisible();

  // Dragged onto the lower edge of the open folder, where the line that
  // marks the drop shows above its first child, a board goes there, first
  // in the folder, rather than after the folder, below its children.
  await create(
    en["boards.new_board"],
    en["boards.board_name"],
    "Prologue",
    en["boards.create_board"],
  );
  await drag(
    page,
    row(page, "Prologue").locator("[data-drag-handle]"),
    row(page, "Act one"),
    0.9,
  );
  await expect
    .poll(async () => {
      const { boards, folders } = await treeOf(page.request, projectId);
      return boards
        .filter((b) => b.folder_id === folders[0]?.id)
        .sort((a, b) => (a.order < b.order ? -1 : 1))
        .map((b) => b.name);
    })
    .toEqual(["Prologue", "Finale", "Opening night"]);
});

// Verifies: NFR-A11Y-08
test("a board's page changes its settings, and deletes it to the trash, from which it is restored", async ({
  page,
}) => {
  const session = await signedIn(page, "Bea Board");
  const workspace = await firstWorkspace(page.request);
  const projectId = await createProject(
    page.request,
    session,
    workspace.id,
    "Cove",
  );
  await page.goto(`/app/projects/${projectId}`);
  await press(
    page.getByRole("button", { name: en["boards.new_board"], exact: true }),
  );
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel(en["boards.board_name"], { exact: true })
    .fill("Beach");
  await press(dialog.getByRole("button", { name: en["boards.create_board"] }));
  await press(page.getByRole("link", { name: "Beach" }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Beach");
  expect(await violations(page)).toEqual([]);

  await page.getByLabel(en["board.slug"], { exact: true }).fill("the-beach");
  await page
    .getByLabel(en["board.description"], { exact: true })
    .fill("Where it starts.");
  await page
    .getByLabel(en["board.color"], { exact: true })
    .selectOption("teal");
  await press(page.getByRole("button", { name: en["board.save"] }));
  await expect(page.getByText(en["board.saved"])).toBeVisible();
  await expect(page.getByLabel(en["board.slug"], { exact: true })).toHaveValue(
    "the-beach",
  );
  await expect(page.getByLabel(en["board.color"], { exact: true })).toHaveValue(
    "teal",
  );

  await press(page.getByRole("button", { name: en["board.delete"] }));
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toBeVisible();
  // No jumper leads to it, so the deletion needs no more than this.
  await expect(
    confirm.getByRole("button", { name: en["boards.delete_board"] }),
  ).toBeEnabled();
  expect(await violations(page)).toEqual([]);
  await press(confirm.getByRole("button", { name: en["boards.delete_board"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Cove");
  await expect(page.getByText(en["boards.empty"])).toBeVisible();

  await press(
    page.getByRole("link", { name: en["project.nav.trash"], exact: true }),
  );
  const item = page
    .getByRole("list", { name: message("trash.list", { project: "Cove" }) })
    .getByRole("listitem");
  await expect(item).toContainText("Beach");
  await expect(item).toContainText(en["trash.kind_board"]);
  expect(await violations(page)).toEqual([]);
  await press(item.getByRole("button", { name: en["trash.restore"] }));
  await expect(
    page.getByText(message("trash.restored", { name: "Beach" })),
  ).toBeVisible();
  await expect(page.getByText(en["trash.empty"])).toBeVisible();
  await press(
    page.getByRole("link", { name: en["project.nav.boards"], exact: true }),
  );
  await expect(page.getByRole("link", { name: "Beach" })).toBeVisible();
});

// Verifies: NFR-A11Y-08
test("a member's grant on a project replaces their role in the workspace there, until it is taken away", async ({
  page,
  playwright,
  baseURL,
  extraHTTPHeaders,
}) => {
  const session = await signedIn(page, "Ann Access");
  const workspace = await firstWorkspace(page.request);
  const projectId = await createProject(
    page.request,
    session,
    workspace.id,
    "Reef",
  );
  const link = await inviteByLink(
    page.request,
    session,
    workspace.id,
    "viewer",
  );
  const other = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders,
  });
  try {
    const viewer = await registerAccount(other, "Val Viewer");
    const viewerSession = await apiSignIn(other, viewer.email);
    await acceptInvite(other, viewerSession, link);
    const roleOnProject = async () =>
      (
        (await (await other.get(`/api/v1/projects/${projectId}`)).json()) as {
          role: string;
        }
      ).role;
    expect(await roleOnProject()).toBe("viewer");

    await page.goto(`/app/projects/${projectId}`);
    await press(
      page.getByRole("link", { name: en["project.nav.access"], exact: true }),
    );
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      en["access.title"],
    );
    expect(await violations(page)).toEqual([]);
    const val = page
      .getByRole("list", { name: message("access.list", { project: "Reef" }) })
      .getByRole("listitem")
      .filter({ hasText: "Val Viewer" });
    await expect(val).toContainText(
      message("access.workspace_role", { role: en["role.viewer"] }),
    );

    await press(val.getByRole("button", { name: en["access.change"] }));
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    expect(await violations(page)).toEqual([]);
    await dialog
      .getByLabel(en["access.role"], { exact: true })
      .selectOption("editor");
    await press(dialog.getByRole("button", { name: en["access.save"] }));
    await expect(dialog).toBeHidden();
    await expect(val).toContainText(
      message("access.granted", { role: en["role.editor"] }),
    );
    expect(await roleOnProject()).toBe("editor");

    await press(val.getByRole("button", { name: en["access.change"] }));
    await page
      .getByRole("dialog")
      .getByLabel(en["access.role"], { exact: true })
      .selectOption("");
    await press(
      page.getByRole("dialog").getByRole("button", { name: en["access.save"] }),
    );
    await expect(val).toContainText(
      message("access.workspace_role", { role: en["role.viewer"] }),
    );
    expect(await roleOnProject()).toBe("viewer");
  } finally {
    await other.dispose();
  }
});

test.describe("a self-hosted instance that caps the projects of a workspace", () => {
  // The cap is the instance's, not the device's, and the test starts an
  // instance and migrates a database of its own, which takes longer than
  // the 30 seconds that a test has by default on a busy machine.
  test.skip(
    ({ hasTouch }) => hasTouch,
    "the desktop project covers the instance's cap",
  );
  test.beforeEach(() => {
    test.setTimeout(180_000);
  });

  // Verifies: FR-ENT-03/AC2, FR-ENT-03/AC1, NFR-A11Y-08
  test("says which limit a new project would pass, and how far, and offers no upgrade", async ({
    browser,
  }) => {
    const owner = {
      name: "Cal Capped",
      email: "cal@example.com",
      password: "a passphrase of the capped owner",
    };
    const instance = await startInstance({
      database: `storycreep_e2e_${port}_desktop_capped`,
      host: "127.0.0.1",
      env: {
        STORYCREEP_SETUP_OWNER_EMAIL: owner.email,
        STORYCREEP_SETUP_OWNER_NAME: owner.name,
        STORYCREEP_SETUP_OWNER_PASSWORD: owner.password,
        STORYCREEP_SETUP_WORKSPACE_NAME: "Capped studio",
        STORYCREEP_WORKSPACE_LIMITS: "projects=1",
      },
    });
    const context = await browser.newContext({ baseURL: instance.url });
    const refused: string[] = [];
    try {
      const page = await context.newPage();
      await watchPolicy(page, refused);
      await signIn(page, owner.email, owner.password);
      await atStart(page, owner.name);
      const session = await sessionOf(page.request);
      const workspace = await firstWorkspace(page.request);
      await createProject(page.request, session, workspace.id, "The one");
      // The server's refusal names the limit and the numbers, and no plan.
      const second = await page.request.post(
        `/api/v1/workspaces/${workspace.id}/projects`,
        {
          headers: { "X-CSRF-Token": session.csrf },
          data: { name: "Another" },
        },
      );
      expect(second.status()).toBe(409);
      const { error } = (await second.json()) as {
        error: { code: string; details: Record<string, unknown> };
      };
      expect(error.code).toBe("limit_exceeded");
      expect(error.details).toMatchObject({
        key: "projects",
        usage: 1,
        limit: 1,
      });
      expect(error.details.upgrade_url).toBeUndefined();

      await page.reload();
      await press(page.getByRole("button", { name: en["projects.create"] }));
      const dialog = page.getByRole("dialog");
      await expect(
        dialog.getByText(
          "The workspace has 1 of the 1 projects that it may have, not counting archived ones. Close to the limit.",
        ),
      ).toBeVisible();
      await dialog
        .getByLabel(en["projects.name"], { exact: true })
        .fill("Another");
      await press(
        dialog.getByRole("button", { name: en["projects.create_submit"] }),
      );
      await expect(dialog.getByRole("alert")).toHaveText(
        "The workspace has as many projects as it may have: 1 of 1.",
      );
      await expect(dialog.getByRole("link")).toHaveCount(0);
      expect(await violations(page)).toEqual([]);
      await press(dialog.getByRole("button", { name: en["projects.cancel"] }));

      await page.goto(`/app/workspaces/${workspace.id}/settings`);
      const projects = page
        .getByRole("table", { name: en["usage.title"] })
        .getByRole("row", { name: new RegExp(`^${en["usage.projects"]}`) });
      await expect(projects).toContainText(en["usage.near_limit"]);
      await expect(
        page.getByRole("link", { name: en["limit.upgrade"] }),
      ).toHaveCount(0);
      expect(refused, "violations of the Content-Security-Policy").toEqual([]);
    } finally {
      await context.close();
      await instance.stop();
    }
  });
});
