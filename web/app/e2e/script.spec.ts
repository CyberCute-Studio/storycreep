import type { Page } from "@playwright/test";
import {
  canvasOf,
  drawn,
  makeBoard,
  makeProject,
  pointOf,
  sentBatches,
  type TestBoard,
  test,
} from "./boards.ts";
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

// A board's script view (PRD FR-TEXT-04, NFR-A11Y-02, plan note N75): the
// board as a linear document, whose edits are the canvas's commands, which
// the canvas shows without a reload, which a keyboard works alone, whose
// targets a finger finds, and which shows a board of 500 nodes within a
// second.

/** Makes a board of count elements in a row, connected one to the next, whose entry node is the first, of a project of its own. */
async function scriptBoard(
  page: Page,
  name: string,
  count: number,
  cols = count,
): Promise<TestBoard & { projectId: string; session: ApiSession }> {
  const session = await signedIn(page, name);
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const board = await makeBoard(
    page.request,
    session,
    projectId,
    "Harbour",
    count,
    { cols },
  );
  await apply(page.request, session, projectId, [
    {
      type: "board.set_entry_node",
      payload: { id: board.boardId, node_id: board.nodeIds[0] },
    },
  ]);
  return { ...board, projectId, session };
}

/** Returns the script view's region. */
function scriptOf(page: Page) {
  return page.getByRole("region", { name: en["script.heading"] });
}

/** Returns the entry of the node of title. */
function entryOf(page: Page, title: string) {
  return scriptOf(page).getByRole("article", { name: title, exact: true });
}

/** Returns the text of the node of id, as the server has it, as plain words. */
async function textOf(page: Page, projectId: string, id: string) {
  const res = await page.request.get(
    `/api/v1/projects/${projectId}/nodes/${id}`,
  );
  expect(res.status()).toBe(200);
  const node = (await res.json()) as {
    title: string;
    text: { content: { content?: { text?: string }[] }[] };
  };
  return {
    title: node.title,
    text: node.text.content
      .map((p) => (p.content ?? []).map((i) => i.text ?? "").join(""))
      .join("\n"),
  };
}

test.describe("a board's script view", () => {
  // Verifies: FR-TEXT-04/AC1
  test("sends the canvas's own commands for a node's text and a choice's label, which the canvas shows without a reload, and shows the canvas's edits", async ({
    page,
  }) => {
    const board = await scriptBoard(page, "Sam Script", 4);
    await page.goto(
      `/app/projects/${board.projectId}/boards/${board.boardId}?view=script`,
    );
    await expect(entryOf(page, "Scene 1.1")).toBeVisible();
    // A mark of the page that a reload would take away.
    await page.evaluate(() => {
      (window as unknown as { kept: boolean }).kept = true;
    });
    const sent = sentBatches(page);
    // The second node's text, in its entry's editor.
    await press(
      entryOf(page, "Scene 1.2").getByRole("button", {
        name: message("script.edit_named", { node: "Scene 1.2" }),
      }),
    );
    const editor = page.getByRole("form", {
      name: message("script.node_editor", { node: "Scene 1.2" }),
    });
    const text = editor.getByRole("textbox", {
      name: message("canvas.text_of", { node: "Scene 1.2" }),
    });
    await text.focus();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("The gate opens.");
    await press(editor.getByRole("button", { name: en["script.save"] }));
    await expect(editor).toBeHidden();
    await expect(entryOf(page, "Scene 1.2")).toContainText("The gate opens.");
    // The first node's one choice, to the second, gets a label.
    await press(
      entryOf(page, "Scene 1.1").getByRole("button", {
        name: message("script.edit_label_named", {
          choice: message("script.choice_to", { node: "Scene 1.2" }),
        }),
      }),
    );
    const labelEditor = page.getByRole("form", {
      name: en["script.choice_editor"],
    });
    await labelEditor
      .getByRole("textbox", { name: en["inspector.label"] })
      .focus();
    await page.keyboard.type("Open the gate");
    await press(labelEditor.getByRole("button", { name: en["script.save"] }));
    await expect(labelEditor).toBeHidden();
    await expect.poll(() => sent.length).toBe(2);
    const scriptTypes = sent.map((b) => b.map((c) => c.type));
    expect(scriptTypes).toEqual([["node.update"], ["connection.update"]]);
    // The canvas shows both, without a reload: its tree names the node by
    // its new text, and the connection by its label.
    await press(
      page.getByRole("link", { name: en["board.view_canvas"], exact: true }),
    );
    const canvas = canvasOf(page, board.name);
    await drawn(canvas);
    const tree = page.getByRole("tree", {
      name: message("canvas.tree.label", { board: board.name }),
    });
    await expect(
      tree.getByRole("treeitem", {
        name: /^Scene 1\.2, Dialogue: The gate opens\./,
      }),
    ).toHaveCount(1);
    // The canvas's own edit of a text is the same command.
    const at = await pointOf(canvas, { x: 2 * 320 + 120, y: 64 });
    if (test.info().project.use.hasTouch) {
      await page.touchscreen.tap(at.x, at.y);
      await page.touchscreen.tap(at.x, at.y);
    } else {
      await page.mouse.click(at.x, at.y);
      await page.mouse.click(at.x, at.y);
    }
    const canvasEditor = page.getByRole("form", {
      name: message("canvas.text_editor", { node: "Scene 1.3" }),
    });
    const canvasText = canvasEditor.getByRole("textbox", {
      name: message("canvas.text_of", { node: "Scene 1.3" }),
    });
    await canvasText.focus();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("The keeper waits.");
    await press(
      canvasEditor.getByRole("button", { name: en["canvas.text_save"] }),
    );
    await expect(canvasEditor).toBeHidden();
    await expect.poll(() => sent.length).toBe(3);
    expect(sent[2].map((c) => c.type)).toEqual(scriptTypes[0]);
    // The script view shows the canvas's edit.
    await press(
      page.getByRole("link", { name: en["board.view_script"], exact: true }),
    );
    await expect(entryOf(page, "Scene 1.3")).toContainText("The keeper waits.");
    expect(
      await page.evaluate(() => (window as unknown as { kept?: boolean }).kept),
    ).toBe(true);
    expect(await textOf(page, board.projectId, board.nodeIds[1])).toMatchObject(
      { text: "The gate opens." },
    );
  });

  // Verifies: FR-TEXT-04/AC3, NFR-A11Y-02, NFR-A11Y-08
  test("is worked with the keyboard alone: a title edited, a node added after another and its text written, and a choice added, each saved from the keyboard", async ({
    page,
  }) => {
    test.skip(test.info().project.use.hasTouch === true, "a keyboard");
    const board = await scriptBoard(page, "Kit Keyboard", 3);
    await page.goto(
      `/app/projects/${board.projectId}/boards/${board.boardId}?view=script`,
    );
    await expect(entryOf(page, "Scene 1.1")).toBeVisible();
    expect(await violations(page)).toEqual([]);
    const sent = sentBatches(page);
    // From the view's switch, Tab reaches the first entry's Edit.
    await page
      .getByRole("link", { name: en["board.view_script"], exact: true })
      .focus();
    const edit = entryOf(page, "Scene 1.1").getByRole("button", {
      name: message("script.edit_named", { node: "Scene 1.1" }),
    });
    for (let i = 0; i < 40; i++) {
      if (await edit.evaluate((el) => el === document.activeElement)) {
        break;
      }
      await page.keyboard.press("Tab");
    }
    await expect(edit).toBeFocused();
    await page.keyboard.press("Enter");
    const title = page.getByRole("textbox", {
      name: en["inspector.node_title"],
    });
    await expect(title).toBeFocused();
    expect(await violations(page)).toEqual([]);
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("The quay");
    await page.keyboard.press("Enter");
    const renamed = entryOf(page, "The quay").getByRole("button", {
      name: message("script.edit_named", { node: "The quay" }),
    });
    await expect(renamed).toBeFocused();
    // The next control adds a node after it, whose text has the focus.
    await page.keyboard.press("Tab");
    await expect(
      entryOf(page, "The quay").getByRole("button", {
        name: message("script.add_after_named", { node: "The quay" }),
      }),
    ).toBeFocused();
    await page.keyboard.press("Enter");
    const added = page.getByRole("form", {
      name: message("script.node_editor", { node: en["canvas.node.untitled"] }),
    });
    await expect(
      added.getByRole("textbox", {
        name: message("canvas.text_of", { node: en["canvas.node.untitled"] }),
      }),
    ).toBeFocused();
    await page.keyboard.type("A gull lands on the rail.");
    const save = added.getByRole("button", { name: en["script.save"] });
    for (let i = 0; i < 20; i++) {
      if (await save.evaluate((el) => el === document.activeElement)) {
        break;
      }
      await page.keyboard.press("Tab");
    }
    await expect(save).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(added).toBeHidden();
    await expect.poll(() => sent.length).toBe(3);
    expect(sent.map((b) => b.map((c) => c.type))).toEqual([
      ["node.update"],
      ["node.create", "connection.reconnect", "connection.create"],
      ["node.update"],
    ]);
    // The new node is between the first and the second, as the server has
    // it, and the view lists it so.
    const reached = scriptOf(page).getByRole("region", {
      name: /^Reached from the entry node/,
    });
    await expect(reached.getByRole("heading", { level: 4 })).toHaveText([
      "The quay",
      en["canvas.node.untitled"],
      "Scene 1.2",
      "Scene 1.3",
    ]);
    const created = (sent[1][0].payload as { id: string }).id;
    expect(await textOf(page, board.projectId, created)).toEqual({
      title: "",
      text: "A gull lands on the rail.",
    });
    // A choice, added from the keyboard, opens its label with the focus.
    const addChoice = entryOf(page, "Scene 1.3").getByRole("button", {
      name: message("script.add_choice_named", { node: "Scene 1.3" }),
    });
    await addChoice.focus();
    await page.keyboard.press("Enter");
    const label = page
      .getByRole("form", { name: en["script.choice_editor"] })
      .getByRole("textbox", { name: en["inspector.label"] });
    await expect(label).toBeFocused();
    expect(await violations(page)).toEqual([]);
    // Escape leaves the label, which it holds no change of, and the focus
    // goes back to the choice's own button.
    await page.keyboard.press("Escape");
    await expect(label).toBeHidden();
    await expect(page.locator(":focus")).toHaveAttribute(
      "aria-label",
      message("script.edit_label_named", {
        choice: message("script.choice_to", {
          node: en["canvas.node.untitled"],
        }),
      }),
    );
  });

  // Verifies: NFR-TOUCH-01, NFR-A11Y-08
  test("has targets that a finger finds, with an editor open too", async ({
    page,
  }) => {
    test.skip(!test.info().project.use.hasTouch, "a touch screen");
    const board = await scriptBoard(page, "Tia Touch", 3);
    await page.goto(
      `/app/projects/${board.projectId}/boards/${board.boardId}?view=script`,
    );
    await expect(entryOf(page, "Scene 1.1")).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    expect(await violations(page)).toEqual([]);
    await press(
      entryOf(page, "Scene 1.2").getByRole("button", {
        name: message("script.edit_named", { node: "Scene 1.2" }),
      }),
    );
    await expect(
      page.getByRole("form", {
        name: message("script.node_editor", { node: "Scene 1.2" }),
      }),
    ).toBeVisible();
    expect(await smallTargets(page)).toEqual([]);
    expect(await violations(page)).toEqual([]);
  });

  // Verifies: FR-TEXT-04/AC4
  test("shows a board of 500 nodes within a second", async ({
    page,
  }, testInfo) => {
    test.setTimeout(180_000);
    const count = 500;
    const board = await scriptBoard(page, "Pia Pace", count, 25);
    // The page reads the board once, as it opens; the view is timed as it
    // shows what the document holds, from the canvas's switch to it.
    await page.goto(`/app/projects/${board.projectId}/boards/${board.boardId}`);
    await drawn(canvasOf(page, board.name));
    const runs = 3;
    const shown: number[] = [];
    for (let run = 0; run < runs; run++) {
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
                if (document.querySelectorAll("article").length === count) {
                  requestAnimationFrame(() =>
                    resolve(performance.now() - start),
                  );
                } else {
                  requestAnimationFrame(check);
                }
              };
              check();
            }),
          { count, name: en["board.view_script"] },
        ),
      );
      await press(
        page.getByRole("link", { name: en["board.view_canvas"], exact: true }),
      );
      await drawn(canvasOf(page, board.name));
    }
    const report = `shown in ${shown.map(Math.round).join(", ")} ms`;
    testInfo.annotations.push({
      type: "FR-TEXT-04/AC4",
      description: `${count} nodes: ${report}`,
    });
    console.log(`FR-TEXT-04 AC4, ${count} nodes: ${report}`);
    expect(
      Math.round(Math.max(...shown)),
      "the slowest showing of the view",
    ).toBeLessThan(1000);
  });
});
