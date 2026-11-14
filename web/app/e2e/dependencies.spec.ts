import type { Page } from "@playwright/test";
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

// A project's dependency view (PRD FR-DEP-02, plan note N77), on the desktop
// and the tablet: the journey of J3, whose board header comes to say that
// Harbourmaster depends on another board once a choice of it reads what
// Elara's conversation writes, a cycle, drawn without an error (AC4), and a
// dependency inspected down to its sites (AC2).

interface Story {
  session: ApiSession;
  projectId: string;
  elara: string;
  harbourmaster: string;
  meet: string;
  keeper: string;
  sent: string;
}

/**
 * Makes a project of the boards Elara — First Meeting, whose node Meet
 * assigns met_elara as the flow leaves it, and Harbourmaster, whose node
 * Keeper leads to Sent by the choice The keeper sent me, and whose jumper
 * Back leads to Meet: Elara depends on Harbourmaster, by the jumper.
 */
async function makeStory(page: Page, name: string): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Harbour");
  const elara = newId("board");
  const harbourmaster = newId("board");
  const meet = newId("node");
  const keeper = newId("node");
  const sent = newId("node");
  const back = newId("node");
  const node = (
    id: string,
    board: string,
    title: string,
    x: number,
    more: Record<string, unknown> = {},
  ) => ({
    type: "node.create",
    payload: {
      id,
      board_id: board,
      type: "element",
      kind: "narration",
      title,
      position: { x, y: 0 },
      ...more,
    },
  });
  await apply(page.request, session, projectId, [
    {
      type: "board.create",
      payload: { id: elara, name: "Elara — First Meeting" },
    },
    {
      type: "board.create",
      payload: { id: harbourmaster, name: "Harbourmaster" },
    },
    {
      type: "variable.create",
      payload: {
        id: newId("variable"),
        key: "met_elara",
        type: "bool",
        scope: "global",
      },
    },
    node(meet, elara, "Meet", 0, { on_exit: "met_elara = true" }),
    node(keeper, harbourmaster, "Keeper", 0),
    node(sent, harbourmaster, "Sent", 400, { is_ending: true }),
    node(back, harbourmaster, "Back", 0, {
      type: "jumper",
      kind: undefined,
      position: { x: 0, y: 300 },
    }),
    {
      type: "node.update",
      payload: { id: back, target: { board_id: elara, node_id: meet } },
    },
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: keeper,
        to_node_id: back,
      },
    },
  ]);
  return { session, projectId, elara, harbourmaster, meet, keeper, sent };
}

test.describe("a project's dependency view", () => {
  // Verifies: FR-DEP-02/AC4
  test("badges a board that comes to depend on another, and draws their cycle without an error", async ({
    page,
  }) => {
    const story = await makeStory(page, "Dependencies");
    await page.goto(
      `/app/projects/${story.projectId}/boards/${story.harbourmaster}`,
    );
    await drawn(canvasOf(page, "Harbourmaster"));
    const badge = page.getByRole("link", { name: /^Depends on/ });
    await expect(badge).toHaveText(
      "Depends on no other board · 1 board depends on it",
    );
    // J3: a choice of Harbourmaster gated on met_elara, which Elara's
    // conversation writes, saved in another session, makes the board
    // depend on Elara, which the header tells without a reload.
    await apply(page.request, story.session, story.projectId, [
      {
        type: "connection.create",
        payload: {
          id: newId("connection"),
          from_node_id: story.keeper,
          to_node_id: story.sent,
          label: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: "The keeper sent me." }],
              },
            ],
          },
          condition: "met_elara == true",
        },
      },
    ]);
    await expect(badge).toHaveText(
      "Depends on 1 other board · 1 board depends on it",
    );
    await press(badge);
    await expect(page).toHaveURL(
      new RegExp(`/dependencies\\?board=${story.harbourmaster}$`),
    );
    await expect(
      page.getByRole("heading", { level: 1, name: en["dependencies.title"] }),
    ).toBeVisible();
    // Each depends on the other: a cycle, which is drawn, and is no error.
    const drawing = page.locator("[data-graph-pairs]");
    await expect(drawing).toHaveAttribute("data-graph-boards", "2");
    await expect(drawing).toHaveAttribute("data-graph-pairs", "2");
    await expect(
      page.getByText("2 boards, 2 dependencies between them."),
    ).toBeVisible();
    await expect(page.getByRole("alert").filter({ hasText: /\S/ })).toHaveCount(
      0,
    );
    for (const name of ["Harbourmaster", "Elara — First Meeting"]) {
      await expect(
        page.getByRole("button", {
          name: `${name} Depends on 1 · Needed by 1`,
          exact: true,
        }),
      ).toBeVisible();
    }
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);
  });

  // Verifies: FR-DEP-02/AC2
  test("inspects a dependency down to the sites that make it, each a link to its node", async ({
    page,
  }) => {
    const story = await makeStory(page, "Inspection");
    await page.goto(`/app/projects/${story.projectId}/dependencies`);
    await press(
      page.getByRole("button", {
        name: "Inspect what Elara — First Meeting depends on in Harbourmaster",
      }),
    );
    const inspected = page.getByRole("complementary", {
      name: en["dependencies.inspector"],
    });
    await expect(
      inspected.getByRole("heading", { name: "Jumpers (1)" }),
    ).toBeVisible();
    await expect(inspected.getByRole("link", { name: "Back" })).toHaveAttribute(
      "href",
      new RegExp(`/boards/${story.harbourmaster}\\?node=`),
    );
    await press(inspected.getByRole("link", { name: "Meet" }));
    const canvas = canvasOf(page, "Elara — First Meeting");
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    // The filter of a kind keeps its edges alone, which the address holds.
    await page.goBack();
    await page
      .getByRole("combobox", { name: en["dependencies.kind"] })
      .selectOption("variable");
    await expect(page).toHaveURL(/kind=variable/);
    await expect(
      page.getByText(en["dependencies.none_filtered"]),
    ).toBeVisible();
  });
});
