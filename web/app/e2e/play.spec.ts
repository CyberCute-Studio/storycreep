import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, makeProject, test } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import { en, expect, press, smallTargets, violations } from "./fixtures.ts";

// Play mode (PRD FR-PLAY-01, plan note N78), in the browser, against the
// server: started from a board's canvas, its panel beside it on the
// desktop, or a sheet over it on the tablet, a choice that a jumper takes
// to another board's page, with the state that the flow had, and its stop.

/** A rich text of one paragraph of inline nodes. */
function paragraph(...content: unknown[]) {
  return { type: "doc", content: [{ type: "paragraph", content }] };
}

const text = (t: string) => ({ type: "text", text: t });

interface Story {
  projectId: string;
  harbour: string;
  elara: string;
}

/**
 * Makes a project of the boards Harbour and Elara: Harbour's entry, Quay,
 * whose text shows gold and whose on-exit raises it, leads by the choice
 * Visit Elara to a jumper to Elara's entry, Meeting, an ending, whose text
 * shows gold too.
 */
async function makeStory(page: Page, name: string): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const harbour = newId("board");
  const elara = newId("board");
  const quay = newId("node");
  const jumper = newId("node");
  const meeting = newId("node");
  const gold = newId("variable");
  const value = { type: "valueRef", attrs: { ref: gold } };
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: harbour, name: "Harbour" } },
    { type: "board.create", payload: { id: elara, name: "Elara" } },
    {
      type: "variable.create",
      payload: { id: gold, key: "gold", type: "int", scope: "global" },
    },
    {
      type: "node.create",
      payload: {
        id: quay,
        board_id: harbour,
        type: "element",
        kind: "narration",
        title: "Quay",
        position: { x: 0, y: 0 },
        text: paragraph(text("Gold "), value, text(".")),
        on_exit: "gold += 1",
      },
    },
    {
      type: "node.create",
      payload: {
        id: meeting,
        board_id: elara,
        type: "element",
        kind: "narration",
        title: "Meeting",
        position: { x: 0, y: 0 },
        is_ending: true,
        text: paragraph(text("You bring "), value, text(" gold.")),
      },
    },
    {
      type: "node.create",
      payload: {
        id: jumper,
        board_id: harbour,
        type: "jumper",
        title: "To Elara",
        position: { x: 400, y: 0 },
        target: { board_id: elara, node_id: meeting },
      },
    },
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: quay,
        to_node_id: jumper,
        label: paragraph(text("Visit Elara")),
      },
    },
    { type: "board.set_entry_node", payload: { id: harbour, node_id: quay } },
    { type: "board.set_entry_node", payload: { id: elara, node_id: meeting } },
  ]);
  return { projectId, harbour, elara };
}

/** Play's panel: a region beside the canvas on the desktop, and a sheet over it on the tablet. */
function playOf(page: Page): Locator {
  return page.getByRole(
    test.info().project.use.hasTouch === true ? "dialog" : "region",
    { name: en["play.title"] },
  );
}

test.describe("play mode", () => {
  // Verifies: FR-PLAY-01/AC3, FR-EDIT-05/AC3
  test("plays a board from its entry node, follows a jumper to the other board's page with the state, and stops", async ({
    page,
  }) => {
    const story = await makeStory(page, "Player");
    await page.goto(`/app/projects/${story.projectId}/boards/${story.harbour}`);
    const canvas = canvasOf(page, "Harbour");
    await drawn(canvas);
    // From the palette, which the header's Commands opens to a finger, and
    // Ctrl or Cmd with K to the keys.
    await press(page.getByRole("button", { name: en["shell.commands"] }));
    const palette = page.getByRole("dialog", { name: en["palette.title"] });
    await expect(palette).toBeVisible();
    await press(
      palette.getByRole("option", { name: en["command.play.start"] }),
    );
    const play = playOf(page);
    await expect(play.getByText("Gold 0.")).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);

    await press(play.getByRole("button", { name: "Visit Elara" }));
    await expect(page).toHaveURL(
      new RegExp(`/projects/${story.projectId}/boards/${story.elara}$`),
    );
    await drawn(canvasOf(page, "Elara"));
    const there = playOf(page);
    await expect(there.getByText("You bring 1 gold.")).toBeVisible();
    await expect(there.getByText(en["play.ended.ending"])).toBeVisible();
    expect(await violations(page)).toEqual([]);

    await press(there.getByRole("button", { name: en["play.stop"] }));
    await expect(there).toBeHidden();
  });
});
