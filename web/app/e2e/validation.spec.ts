import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, makeProject, test } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import {
  type ApiSession,
  en,
  expect,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// A project's validation (PRD FR-DEP-03, plan note N77): its findings, each
// a link that opens the editor of what it is about (AC3), and a warning
// muted with a reason, which the page then lists apart, never hidden, and
// unmutes (AC4), on the desktop and the tablet.

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
  dead: string;
  ask: string;
  gold: string;
}

/**
 * Makes a project of the board Harbour, whose entry Greet leads to Reply,
 * an ending, by the choice Ask, whose condition does not compile (an
 * error, expr_invalid), and to Dead, which nothing leaves and which is no
 * ending (a warning, dead_end); Greet's on-exit script assigns gold, which
 * nothing reads (a warning, var_written_never_read).
 */
async function makeStory(page: Page, name: string): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const boardId = newId("board");
  const greet = newId("node");
  const reply = newId("node");
  const dead = newId("node");
  const ask = newId("connection");
  const gold = newId("variable");
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
      text: paragraph(`${title}.`),
      ...more,
    },
  });
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: boardId, name: "Harbour" } },
    {
      type: "variable.create",
      payload: { id: gold, key: "gold", type: "int", scope: "global" },
    },
    element(greet, "Greet", 0, { on_exit: "gold = 1" }),
    element(reply, "Reply", 400, { is_ending: true }),
    element(dead, "Dead", 400, { position: { x: 400, y: 300 } }),
    {
      type: "connection.create",
      payload: {
        id: ask,
        from_node_id: greet,
        to_node_id: reply,
        label: paragraph("Ask"),
        condition: "gold >",
      },
    },
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: greet,
        to_node_id: dead,
        label: paragraph("Wander"),
      },
    },
    { type: "board.set_entry_node", payload: { id: boardId, node_id: greet } },
  ]);
  return { session, projectId, boardId, dead, ask, gold };
}

/** Returns the section of the page under the heading that matches name. */
function sectionOf(page: Page, name: RegExp): Locator {
  return page
    .locator("section")
    .filter({ has: page.getByRole("heading", { level: 2, name }) });
}

/** Opens the validation of story, and waits for its heading. */
async function openValidation(page: Page, story: Story): Promise<void> {
  await page.goto(`/app/projects/${story.projectId}/validation`);
  await expect(
    page.getByRole("heading", { level: 1, name: en["validation.title"] }),
  ).toBeVisible();
}

/** The inspector of the canvas, a region beside it on the desktop, which a link opens, or a sheet on the tablet, which its button opens. */
async function inspectorOf(page: Page, name: string): Promise<Locator> {
  if (test.info().project.use.hasTouch) {
    await press(
      page.getByRole("button", { name: en["canvas.inspector"], exact: true }),
    );
    const sheet = page.getByRole("dialog", { name });
    await expect(sheet).toBeVisible();
    return sheet;
  }
  const region = page.getByRole("region", { name });
  await expect(region).toBeVisible();
  return region;
}

test.describe("a project's validation", () => {
  // Verifies: FR-DEP-03/AC3
  test("opens the editor of what each finding is about", async ({ page }) => {
    const story = await makeStory(page, "Validation");
    await openValidation(page, story);
    await expect(sectionOf(page, /^Error \(1\)$/)).toBeVisible();
    await expect(sectionOf(page, /^Warnings \(2\)$/)).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    // The choice whose condition does not compile opens selected, in the
    // inspector of the connection, whose condition is the one at fault.
    await press(
      sectionOf(page, /^Error/).getByRole("link", {
        name: "The condition of a choice from Greet, on Harbour",
      }),
    );
    const canvas = canvasOf(page, "Harbour");
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-connection", story.ask);
    const connection = await inspectorOf(page, en["inspector.connection"]);
    await expect(
      connection.getByRole("textbox", { name: en["inspector.condition"] }),
    ).toHaveValue("gold >");

    // The dead end opens its element, selected, in its inspector.
    await openValidation(page, story);
    await press(
      sectionOf(page, /^Warnings/).getByRole("link", {
        name: "Dead, on Harbour",
      }),
    );
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    await expect(canvas).toHaveAttribute("data-connection", "");
    await inspectorOf(page, "Dead");

    // The variable that nothing reads opens its panel.
    await openValidation(page, story);
    await press(
      sectionOf(page, /^Warnings/).getByRole("link", {
        name: "The global variable gold",
      }),
    );
    await expect(page).toHaveURL(
      new RegExp(
        `/projects/${story.projectId}/variables\\?variable=${story.gold}$`,
      ),
    );
  });

  // Verifies: FR-DEP-03/AC4
  test("mutes a warning with a reason, lists it apart with its reason, and unmutes it", async ({
    page,
  }) => {
    const story = await makeStory(page, "Muting");
    await openValidation(page, story);
    await press(
      page.getByRole("button", {
        name: `${en["validation.mute"]}: Dead, on Harbour`,
      }),
    );
    const dialog = page.getByRole("dialog", { name: "Mute: Dead, on Harbour" });
    await expect(dialog).toBeVisible();
    await dialog
      .getByRole("textbox", { name: en["validation.reason"] })
      .fill("The demo ends here.");
    await press(
      dialog.getByRole("button", { name: en["validation.mute_submit"] }),
    );
    await expect(dialog).toBeHidden();
    const muted = sectionOf(page, /^Muted \(1\)$/);
    await expect(muted.getByText("Reason: The demo ends here.")).toBeVisible();
    await expect(muted.getByText(/^Muted by you/)).toBeVisible();
    await expect(sectionOf(page, /^Warning \(1\)$/)).toBeVisible();
    // The mute is the project's: the server lists it apart, and so does
    // the page once it is read anew.
    const report = await page.request.get(
      `/api/v1/projects/${story.projectId}/validation`,
    );
    const body = (await report.json()) as {
      muted: { code: string; entity_id: string; mute: { reason: string } }[];
    };
    expect(body.muted).toMatchObject([
      {
        code: "dead_end",
        entity_id: story.dead,
        mute: { reason: "The demo ends here." },
      },
    ]);
    await page.reload();
    await expect(
      sectionOf(page, /^Muted \(1\)$/).getByText("Reason: The demo ends here."),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);

    await press(
      sectionOf(page, /^Muted/).getByRole("button", {
        name: `${en["validation.unmute"]}: Dead, on Harbour`,
      }),
    );
    await expect(
      sectionOf(page, /^Muted \(0\)$/).getByText(en["validation.none_muted"]),
    ).toBeVisible();
    await expect(sectionOf(page, /^Warnings \(2\)$/)).toBeVisible();
  });
});
