import { readFile } from "node:fs/promises";
import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { makeProject, test } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import {
  type ApiSession,
  en,
  expect,
  message,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// A project's releases (PRD J4, FR-SNAP-01, FR-SNAP-02, FR-SNAP-04,
// FR-DEP-04, plan note N88), on the desktop and the tablet: publishing from
// the dialog, with its report, the acknowledgement of its warnings, a
// changed version, and a note; the list as the build ends; the download;
// the web diff; and the read-only view, which sends no command.

/** A rich text of one paragraph of text. */
function paragraph(text: string) {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }],
  };
}

interface Story {
  session: ApiSession;
  projectId: string;
  boardId: string;
  reply: string;
}

/**
 * Makes a project of the board Harbour, whose entry Greet leads to Reply, an
 * ending, by the choice Ask; Greet's on-exit script assigns gold, which
 * nothing reads, a warning (var_written_never_read), and condition, if
 * given, is the choice's.
 */
async function makeStory(
  page: Page,
  name: string,
  condition?: string,
): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const boardId = newId("board");
  const greet = newId("node");
  const reply = newId("node");
  const element = (
    id: string,
    title: string,
    x: number,
    more: Record<string, unknown> = {},
  ) => ({
    type: "node.create",
    payload: {
      id,
      board_id: boardId,
      type: "element",
      kind: "narration",
      title,
      position: { x, y: 0 },
      text: paragraph(`${title} says hello.`),
      ...more,
    },
  });
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: boardId, name: "Harbour" } },
    {
      type: "variable.create",
      payload: {
        id: newId("variable"),
        key: "gold",
        type: "int",
        scope: "global",
      },
    },
    element(greet, "Greet", 0, { on_exit: "gold = 1" }),
    element(reply, "Reply", 400, { is_ending: true }),
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: greet,
        to_node_id: reply,
        label: paragraph("Ask"),
        ...(condition === undefined ? {} : { condition }),
      },
    },
    { type: "board.set_entry_node", payload: { id: boardId, node_id: greet } },
  ]);
  return { session, projectId, boardId, reply };
}

/** Publishes the draft of story through the API, acknowledging its warnings, and waits for its build. */
async function publishByApi(page: Page, story: Story, version: string) {
  const base = `/api/v1/projects/${story.projectId}`;
  const report = (await (
    await page.request.get(`${base}/validation`)
  ).json()) as {
    seq: number;
    findings: {
      severity: string;
      code: string;
      entity_id: string;
      field?: string;
      target?: unknown;
    }[];
  };
  const res = await page.request.post(`${base}/snapshots`, {
    headers: { "X-CSRF-Token": story.session.csrf },
    data: {
      version,
      note: `The release ${version}`,
      acknowledged_seq: report.seq,
      acknowledged_warnings: report.findings
        .filter((f) => f.severity === "warning")
        .map(({ code, entity_id, field, target }) => ({
          code,
          entity_id,
          ...(field === undefined ? {} : { field }),
          ...(target === undefined ? {} : { target }),
        })),
    },
  });
  expect(res.status(), await res.text()).toBe(202);
  const { id } = (await res.json()) as { id: string };
  await expect
    .poll(
      async () =>
        (
          (await (
            await page.request.get(`${base}/snapshots/${id}`)
          ).json()) as {
            status: string;
          }
        ).status,
      { timeout: 30_000 },
    )
    .toBe("ready");
  return id;
}

/** Opens the releases of story, and waits for its heading. */
async function openReleases(page: Page, story: Story) {
  await page.goto(`/app/projects/${story.projectId}/releases`);
  await expect(
    page.getByRole("heading", { level: 1, name: en["releases.title"] }),
  ).toBeVisible();
}

/** Returns the release of the list whose heading starts with version. */
function rowOf(page: Page, version: string): Locator {
  return page.getByRole("listitem").filter({
    has: page.getByRole("heading", {
      level: 2,
      name: new RegExp(`^${version.replaceAll(".", "\\.")}`),
    }),
  });
}

test.describe("a project's releases", () => {
  // J4, from the Releases page to the diff.
  // Verifies: FR-SNAP-02/AC2, FR-DEP-04/AC2
  test("publishes a release, lists it, downloads it, compares it, and views it read-only", async ({
    page,
  }) => {
    const story = await makeStory(page, "Publisher");
    const first = await publishByApi(page, story, "1.3.0");
    // What the next release adds: a node and a variable.
    await apply(page.request, story.session, story.projectId, [
      {
        type: "node.create",
        payload: {
          id: newId("node"),
          board_id: story.boardId,
          type: "element",
          kind: "narration",
          title: "Keeper",
          position: { x: 800, y: 0 },
          text: paragraph("The keeper waits."),
          is_ending: true,
        },
      },
      {
        type: "variable.create",
        payload: {
          id: newId("variable"),
          key: "keeper_met",
          type: "bool",
          scope: "global",
        },
      },
    ]);

    await openReleases(page, story);
    await expect(rowOf(page, "1.3.0")).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    // The dialog shows the report, and proposes a patch bump.
    await press(
      page.getByRole("button", { name: en["releases.publish.open"] }),
    );
    const dialog = page.getByRole("dialog", {
      name: en["releases.publish.title"],
    });
    await expect(dialog.getByText(/^0 errors, \d+ warnings?$/)).toBeVisible();
    const version = dialog.getByRole("textbox", {
      name: en["releases.publish.version"],
    });
    await expect(version).toHaveValue("1.3.1");
    const submit = dialog.getByRole("button", {
      name: en["releases.publish.submit"],
    });
    await expect(submit).toBeDisabled();
    expect(await violations(page)).toEqual([]);

    // The author acknowledges the warnings, chooses a minor version, and
    // writes the note.
    await press(dialog.getByRole("checkbox"));
    await version.fill("1.4.0");
    await dialog
      .getByRole("textbox", { name: en["releases.publish.note"] })
      .fill("Harbour intro + keeper gate");
    await press(submit);
    await expect(dialog).toBeHidden();

    // It builds, and shows who published it, the note, and its counts.
    await expect(
      page.getByText(message("releases.said.building", { version: "1.4.0" })),
    ).toBeVisible();
    const row = rowOf(page, "1.4.0");
    await expect(
      row.getByText(en["releases.status.ready"], { exact: true }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(row.getByText("Harbour intro + keeper gate")).toBeVisible();
    await expect(row.getByText(/^Published by Publisher on /)).toBeVisible();
    await expect(row.getByText(/^1 board · 3 nodes · /)).toBeVisible();

    // The zip downloads under the server's name.
    const download = page.waitForEvent("download");
    await press(
      page.getByRole("button", {
        name: message("releases.download_of", { version: "1.4.0" }),
      }),
    );
    expect((await download).suggestedFilename()).toBe("lighthouse-1.4.0.zip");

    // The web diff of 1.4.0 from 1.3.0.
    await press(
      page.getByRole("link", {
        name: message("releases.compare_of", {
          version: "1.4.0",
          previous: "1.3.0",
        }),
      }),
    );
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: en["releases.compare.title"],
      }),
    ).toBeVisible();
    const nodes = page
      .getByRole("row")
      .filter({ has: page.getByRole("rowheader", { name: "Nodes" }) });
    await expect(nodes.getByRole("cell").first()).toHaveText("1");
    const variables = page
      .getByRole("row")
      .filter({ has: page.getByRole("rowheader", { name: "Variables" }) });
    await expect(variables.getByRole("cell").first()).toHaveText("1");
    await expect(page.getByText(/^Added: Keeper/)).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    // The view of the earlier release shows it as it was, and sends no
    // command, whatever is pressed.
    const commands: string[] = [];
    page.on("request", (request) => {
      if (
        request.method() === "POST" &&
        request.url().endsWith(`/projects/${story.projectId}/commands`)
      ) {
        commands.push(request.url());
      }
    });
    await page.goto(`/app/projects/${story.projectId}/releases/${first}`);
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: message("releases.view.title", { version: "1.3.0" }),
      }),
    ).toBeVisible();
    await expect(page.getByText("Greet says hello.")).toBeVisible();
    await expect(page.getByText("The keeper waits.")).toBeHidden();
    await expect(
      page.getByRole("button", { name: en["history.undo"] }),
    ).toHaveCount(0);
    await expect(page.getByRole("textbox")).toHaveCount(0);
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);
    await page.keyboard.press("ControlOrMeta+z");
    await page.getByRole("link", { name: "Harbour" }).first().focus();
    await page.keyboard.press("Delete");
    await expect(page.getByText("Greet says hello.")).toBeVisible();
    expect(commands).toEqual([]);
  });

  // A line break ends a condition's comment, so a condition shown on one
  // line would read what follows its comment as part of the comment.
  // Verifies: FR-SNAP-02/AC2
  test("keeps a branch condition's lines, and long names within a narrow page, in the view", async ({
    page,
  }) => {
    const story = await makeStory(page, "Reader");
    const fork = newId("node");
    const output = newId("branch_output");
    const longName = "Lighthouse".repeat(10);
    await apply(page.request, story.session, story.projectId, [
      { type: "board.create", payload: { id: newId("board"), name: longName } },
      {
        type: "node.create",
        payload: {
          id: fork,
          board_id: story.boardId,
          type: "branch",
          title: "Fork",
          position: { x: 0, y: 320 },
        },
      },
      { type: "branch_output.create", payload: { id: output, node_id: fork } },
      {
        type: "branch_output.update",
        payload: {
          id: output,
          condition: `gold > 0 // ${"because".repeat(30)}\n  && gold < 9`,
        },
      },
    ]);
    const release = await publishByApi(page, story, "1.0.0");
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto(`/app/projects/${story.projectId}/releases/${release}`);
    const condition = page.locator("code", { hasText: "&& gold < 9" });
    await expect(condition).toBeVisible();
    expect(
      await condition.evaluate((code) => getComputedStyle(code).whiteSpace),
    ).toBe("pre-wrap");
    for (const shown of [
      condition,
      page.getByRole("link", { name: longName }),
    ]) {
      const box = await shown.boundingBox();
      expect(box).not.toBeNull();
      expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(375);
    }
  });

  // Verifies: FR-DEP-04/AC1
  // Verifies: FR-DATA-01/AC5, FR-DATA-01/AC6
  test("exports the project's authoring data from its dialog", async ({
    page,
  }) => {
    const story = await makeStory(page, "Exporter");
    await publishByApi(page, story, "1.3.0");
    await openReleases(page, story);
    await press(
      page.getByRole("button", { name: en["authoring_export.open"] }),
    );
    const dialog = page.getByRole("dialog", {
      name: en["authoring_export.title"],
    });
    const notice = dialog.getByText(en["authoring_export.personal_data"]);
    await expect(notice).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    await press(
      dialog.getByRole("radio", {
        name: en["authoring_export.profile_content"],
      }),
    );
    await expect(notice).toBeHidden();
    await press(
      dialog.getByRole("button", { name: en["authoring_export.submit"] }),
    );
    const link = dialog.getByRole("link", {
      name: en["authoring_export.download"],
    });
    await expect(link).toBeVisible({ timeout: 30_000 });
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);
    const downloading = page.waitForEvent("download");
    await press(link);
    expect((await downloading).suggestedFilename()).toMatch(
      /-draft-authoring\.zip$/,
    );
  });

  test("imports an exported archive from the workspace's dialog, into a new project", async ({
    page,
  }) => {
    const story = await makeStory(page, "Importer");
    await publishByApi(page, story, "1.3.0");
    await openReleases(page, story);
    await press(
      page.getByRole("button", { name: en["authoring_export.open"] }),
    );
    const exporting = page.getByRole("dialog", {
      name: en["authoring_export.title"],
    });
    await press(
      exporting.getByRole("button", { name: en["authoring_export.submit"] }),
    );
    const link = exporting.getByRole("link", {
      name: en["authoring_export.download"],
    });
    await expect(link).toBeVisible({ timeout: 30_000 });
    const downloading = page.waitForEvent("download");
    await press(link);
    const archive = await readFile(await (await downloading).path());

    await page.goto("/app/projects");
    await expect(
      page.getByRole("heading", { level: 1, name: en["projects.title"] }),
    ).toBeVisible();
    await press(page.getByRole("button", { name: en["project_import.open"] }));
    const dialog = page.getByRole("dialog", {
      name: en["project_import.title"],
    });
    const field = dialog.getByLabel(en["project_import.file_label"]);
    await expect(field).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    await field.setInputFiles({
      name: "lighthouse.zip",
      mimeType: "application/zip",
      buffer: archive,
    });
    await press(
      dialog.getByRole("button", { name: en["project_import.submit"] }),
    );
    const opened = dialog.getByRole("link", {
      name: en["project_import.open_project"],
    });
    await expect(opened).toBeVisible({ timeout: 60_000 });
    // The archive's IDs are the project's that it was exported from, which
    // is on this instance, so the import gives everything fresh ones.
    await expect(
      dialog.getByText(en["project_import.policy_remap_auto"]),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    await press(opened);
    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(/\/app\/projects\/prj_/);
    expect(page.url()).not.toContain(story.projectId);
  });

  test("does not publish a draft with an error", async ({ page }) => {
    const story = await makeStory(page, "Blocked", "gold >");
    await openReleases(page, story);
    await expect(page.getByText(en["releases.none"])).toBeVisible();
    await press(
      page.getByRole("button", { name: en["releases.publish.open"] }),
    );
    const dialog = page.getByRole("dialog", {
      name: en["releases.publish.title"],
    });
    await expect(dialog.getByText(/^1 error, \d+ warnings?$/)).toBeVisible();
    await expect(
      dialog.getByText(en["releases.publish.blocked"]),
    ).toBeVisible();
    await expect(dialog.getByRole("checkbox")).toHaveCount(0);
    await expect(
      dialog.getByRole("button", { name: en["releases.publish.submit"] }),
    ).toBeDisabled();
    expect(await violations(page)).toEqual([]);
  });
});
