import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, makeProject, test } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import { type ApiSession, en, expect, press, violations } from "./fixtures.ts";

// A project's writing statistics (PRD FR-TEXT-06, plan note N76): the words
// and the characters of its texts, and its nodes, choices, and conditions,
// of the project, of each of its boards, and of a node, in its inspector,
// which follow an edit within a second, without a reload.

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
  greet: string;
  reply: string;
}

/**
 * Makes a project of the board Harbour, of the element Greet, whose text is
 * "Hello there." (2 words, 12 characters), and the element Reply, "Fine."
 * (1 word, 5 characters), and the choice Ask (1 word, 3 characters) from
 * Greet to Reply, whose condition is true: 4 words, 20 characters, 2 nodes,
 * 1 choice, and 1 condition.
 */
async function makeStory(page: Page, name: string): Promise<Story> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const boardId = newId("board");
  const greet = newId("node");
  const reply = newId("node");
  await apply(page.request, session, projectId, [
    { type: "board.create", payload: { id: boardId, name: "Harbour" } },
    ...[
      { id: greet, title: "Greet", text: "Hello there.", x: 0 },
      { id: reply, title: "Reply", text: "Fine.", x: 400 },
    ].map((n) => ({
      type: "node.create",
      payload: {
        id: n.id,
        board_id: boardId,
        type: "element",
        title: n.title,
        position: { x: n.x, y: 0 },
        text: paragraph(n.text),
      },
    })),
    {
      type: "connection.create",
      payload: {
        id: newId("connection"),
        from_node_id: greet,
        to_node_id: reply,
        label: paragraph("Ask"),
        condition: "true",
      },
    },
  ]);
  return { session, projectId, boardId, greet, reply };
}

/** Returns the value of the term of a list of counts. */
function countOf(list: Locator, term: string): Locator {
  return list.locator(`dt:text-is("${term}") + dd`);
}

/** Sets the text of the node of story to text through the API, as another session of the account does. */
async function edit(page: Page, story: Story, id: string, text: string) {
  await apply(page.request, story.session, story.projectId, [
    { type: "node.update", payload: { id, text: paragraph(text) } },
  ]);
}

test.describe("a project's writing statistics", () => {
  // Verifies: FR-TEXT-06/AC1
  test("shows the project's counts and each board's, and follows an edit within a second", async ({
    page,
  }) => {
    const story = await makeStory(page, "Statistics");
    await page.goto(`/app/projects/${story.projectId}/statistics`);
    await expect(
      page.getByRole("heading", { level: 1, name: en["statistics.title"] }),
    ).toBeVisible();
    const project = page.getByRole("region", {
      name: en["statistics.project"],
    });
    const want = async (counts: Record<string, string>) => {
      for (const [term, value] of Object.entries(counts)) {
        await expect(countOf(project, term)).toHaveText(value, {
          timeout: 1000,
        });
      }
    };
    await want({
      [en["statistics.nodes"]]: "2",
      [en["statistics.words"]]: "4",
      [en["statistics.characters"]]: "20",
      [en["statistics.choices"]]: "1",
      [en["statistics.conditions"]]: "1",
    });
    const row = page.getByRole("row", { name: /Harbour/ });
    await expect(row.getByRole("cell")).toHaveText(["2", "4", "20", "1", "1"]);
    await expect(row.getByRole("link", { name: "Harbour" })).toHaveAttribute(
      "href",
      `/app/projects/${story.projectId}/boards/${story.boardId}`,
    );
    expect(await violations(page)).toEqual([]);

    // "Hello there, weary traveller.": 4 words, 29 characters, which the
    // page shows within a second of the batch's answer.
    await edit(page, story, story.greet, "Hello there, weary traveller.");
    await want({
      [en["statistics.words"]]: "6",
      [en["statistics.characters"]]: "37",
    });
    await expect(row.getByRole("cell")).toHaveText(["2", "6", "37", "1", "1"], {
      timeout: 1000,
    });
  });

  // Verifies: FR-TEXT-06/AC1
  test("shows a node's counts in its inspector, which follow an edit of its text within a second", async ({
    page,
  }) => {
    const story = await makeStory(page, "Inspected");
    await page.goto(
      `/app/projects/${story.projectId}/boards/${story.boardId}?node=${story.greet}`,
    );
    const canvas = canvasOf(page, "Harbour");
    await drawn(canvas);
    await expect(canvas).toHaveAttribute("data-selected", "1");
    const inspect = page.getByRole("button", {
      name: en["canvas.inspector"],
      exact: true,
    });
    if ((await inspect.getAttribute("aria-pressed")) !== "true") {
      await press(inspect);
    }
    const panel = page.getByRole(
      test.info().project.use.hasTouch ? "dialog" : "region",
      { name: "Greet" },
    );
    const stats = panel.getByRole("region", {
      name: en["inspector.statistics"],
    });
    await stats.scrollIntoViewIfNeeded();
    // Its text and the label of its choice: 3 words, 15 characters.
    await expect(countOf(stats, en["statistics.words"])).toHaveText("3");
    await expect(countOf(stats, en["statistics.characters"])).toHaveText("15");
    await expect(countOf(stats, en["statistics.choices"])).toHaveText("1");
    await expect(countOf(stats, en["statistics.conditions"])).toHaveText("1");

    await edit(page, story, story.greet, "Hello there, weary traveller.");
    await expect(countOf(stats, en["statistics.words"])).toHaveText("5", {
      timeout: 1000,
    });
    await expect(countOf(stats, en["statistics.characters"])).toHaveText("32", {
      timeout: 1000,
    });
  });
});
