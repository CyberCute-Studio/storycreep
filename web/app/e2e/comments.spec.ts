import type { Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import { canvasOf, drawn, makeProject, test } from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import { en, expect, press, smallTargets, violations } from "./fixtures.ts";

// A project's threaded comments (PRD FR-COMMENT-01, plan item 74), in the
// browser, against the server, on the desktop and the tablet: a thread
// posted on a node in its inspector, a reply, the canvas's badge of the
// node's open threads, which its tree says too, the thread resolved, and
// the project's list of its threads, filtered by their status; the list's
// link to a node's thread, which opens the node's inspector with the focus
// on the heading of its threads, in view; and the list's link to a board's
// own thread, which opens the board's page with the focus on the heading of
// its threads, in view under the canvas.

/** The inspector: a region beside the canvas on the desktop, and a sheet over it on the tablet. */
function inspectorOf(page: Page, title: string): Locator {
  return page.getByRole(
    test.info().project.use.hasTouch === true ? "dialog" : "region",
    { name: title },
  );
}

/** Types text into the rich text of textbox, which takes the focus as a tap or a click would give it. */
async function typeInto(textbox: Locator, text: string): Promise<void> {
  await press(textbox);
  await expect(textbox).toBeFocused();
  await textbox.page().keyboard.type(text);
  await expect(textbox).toHaveText(text);
}

test.describe("threaded comments", () => {
  test("posts a thread on a node, answers it, shows its badge, resolves it, and lists it among the project's", async ({
    page,
  }) => {
    const session = await signedIn(page, "Critic");
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const boardId = newId("board");
    const quay = newId("node");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: boardId, name: "Harbour" } },
      {
        type: "node.create",
        payload: {
          id: quay,
          board_id: boardId,
          type: "element",
          kind: "narration",
          title: "Quay",
          position: { x: 0, y: 0 },
        },
      },
    ]);
    // A link to the node's comments opens its inspector at them.
    await page.goto(
      `/app/projects/${projectId}/boards/${boardId}?node=${quay}&comments=true`,
    );
    const canvas = canvasOf(page, "Harbour");
    await drawn(canvas);
    const inspector = inspectorOf(page, "Quay");
    const comments = inspector.getByRole("region", {
      name: en["comments.title"],
    });
    await expect(comments.getByText(en["comments.none"])).toBeVisible();
    await expect(canvas).toHaveAttribute("data-commented", "0");

    await typeInto(
      comments.getByRole("textbox", { name: en["comments.new"] }),
      "The lamp is out.",
    );
    await press(comments.getByRole("button", { name: en["comments.post"] }));
    await expect(comments.getByText(en["comments.posted"])).toBeVisible();
    const thread = comments.getByRole("listitem").filter({
      hasText: "The lamp is out.",
    });
    await expect(thread).toBeVisible();
    // The canvas counts the node's open thread on its card's badge, which
    // its tree says in the node's name, under the tablet's sheet too.
    await expect(canvas).toHaveAttribute("data-commented", "1");
    await expect(
      page
        .getByRole("tree", {
          name: "Nodes and connections of Harbour",
          includeHidden: true,
        })
        .getByRole("treeitem", {
          name: /; 1 open comment$/,
          includeHidden: true,
        }),
    ).toHaveCount(1);
    expect(await violations(page)).toEqual([]);
    // A finger's targets, of the tablet, where the canvas's handles of the
    // node selected are a finger's too, rather than a mouse's 24 px.
    const touch = test.info().project.use.hasTouch === true;
    if (touch) {
      expect(await smallTargets(page)).toEqual([]);
    }

    await press(thread.getByRole("button", { name: en["comments.reply"] }));
    await typeInto(
      thread.getByRole("textbox", { name: en["comments.reply_label"] }),
      "I will light it.",
    );
    await press(
      thread.getByRole("button", { name: en["comments.reply_post"] }),
    );
    await expect(thread.getByText("I will light it.")).toBeVisible();
    await expect(
      thread.getByRole("button", { name: en["comments.reply"] }),
    ).toBeFocused();
    expect(await violations(page)).toEqual([]);
    if (touch) {
      expect(await smallTargets(page)).toEqual([]);
    }

    await press(thread.getByRole("button", { name: en["comments.resolve"] }));
    await expect(comments.getByText(en["comments.resolved"])).toBeVisible();
    await expect(
      thread.getByRole("button", { name: en["comments.reopen"] }),
    ).toBeVisible();
    // A resolved thread is open no more, which the badge counts.
    await expect(canvas).toHaveAttribute("data-commented", "0");
    await press(inspector.getByRole("button", { name: en["panel.close"] }));

    // The project's list, open threads first, of which there are none now.
    await press(
      page
        .getByRole("navigation", { name: "Project Lighthouse" })
        .getByRole("link", { name: en["project.nav.comments"] }),
    );
    await expect(page.getByText(en["comments.page.none_open"])).toBeVisible();
    await page
      .getByRole("combobox", { name: en["comments.page.status"] })
      .selectOption("resolved");
    await expect(page).toHaveURL(/[?&]status=resolved/);
    const listed = page.getByRole("listitem").filter({
      hasText: "The lamp is out.",
    });
    await expect(listed.getByText("I will light it.")).toBeVisible();
    await expect(
      listed.getByRole("link", { name: "On “Quay”, Harbour" }),
    ).toBeVisible();
    expect(await violations(page)).toEqual([]);
    expect(await smallTargets(page)).toEqual([]);
  });

  test("opens a node's inspector with the focus on its threads' heading, in view, as a link to one of them asks", async ({
    page,
  }) => {
    const session = await signedIn(page, "Critic");
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const boardId = newId("board");
    const quay = newId("node");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: boardId, name: "Harbour" } },
      {
        type: "node.create",
        payload: {
          id: quay,
          board_id: boardId,
          type: "element",
          kind: "narration",
          title: "Quay",
          position: { x: 0, y: 0 },
        },
      },
      {
        type: "comment.create",
        payload: {
          id: newId("comment"),
          target: quay,
          body: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: "The lamp is out." }],
              },
            ],
          },
        },
      },
    ]);
    await page.goto(`/app/projects/${projectId}/comments`);
    await press(page.getByRole("link", { name: "On “Quay”, Harbour" }));
    await drawn(canvasOf(page, "Harbour"));
    // The inspector's comments are its last section, under the node's own,
    // whose heading takes the focus once they are read, which the browser
    // brings into view, wherever in the inspector it then is.
    const comments = inspectorOf(page, "Quay").getByRole("region", {
      name: en["comments.title"],
    });
    const heading = comments.getByRole("heading", {
      name: en["comments.title"],
    });
    await expect(heading).toBeFocused();
    await expect(heading).toBeInViewport();
    await expect(comments.getByText("The lamp is out.")).toBeVisible();
  });

  test("opens a board's page with the focus on its own threads' heading, in view under the canvas, as a link to one of them asks", async ({
    page,
  }) => {
    const session = await signedIn(page, "Critic");
    const projectId = await makeProject(page.request, session, "Lighthouse");
    const boardId = newId("board");
    await apply(page.request, session, projectId, [
      { type: "board.create", payload: { id: boardId, name: "Harbour" } },
      {
        type: "comment.create",
        payload: {
          id: newId("comment"),
          target: boardId,
          body: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: "Rename it." }],
              },
            ],
          },
        },
      },
    ]);
    await page.goto(`/app/projects/${projectId}/comments`);
    await press(page.getByRole("link", { name: "On the board Harbour" }));
    await drawn(canvasOf(page, "Harbour"));
    // The canvas takes the window's height below where it starts, under
    // which the board's own threads are, whose heading takes the focus once
    // they are read, which the browser brings into view.
    const heading = page.getByRole("heading", {
      name: en["comments.title"],
      level: 2,
    });
    await expect(heading).toBeFocused();
    await expect(heading).toBeInViewport();
    await expect(page.getByText("Rename it.")).toBeVisible();
  });
});
