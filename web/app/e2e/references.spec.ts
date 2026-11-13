import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { newId } from "@storycreep/shared/ids";
import {
  canvasOf,
  drawn,
  makeBoard,
  makeProject,
  pointOf,
  test,
} from "./boards.ts";
import { apply, signedIn } from "./content.ts";
import {
  en,
  expect,
  message,
  press,
  smallTargets,
  violations,
} from "./fixtures.ts";

// The value references and the mentions of a project's rich text (PRD
// FR-TEXT-02, FR-TEXT-03, plan note N73), on the desktop with a mouse and
// on the tablet with fingers: { and @, typed in an element's text, offer
// the project's values and components, and insert what is chosen as a
// reference to its ID; a mention is in its component's list of mentions;
// and a rename of what a text refers to shows in every text, at once in a
// page open on them, without a change of the texts.

/** A project of its own, with a board of two elements, a global variable gold, and a component Elara of a type with a runtime attribute trust. */
async function makeContent(page: Page) {
  const session = await signedIn(page, "Rhea References");
  const projectId = await makeProject(page.request, session, "Lighthouse");
  const board = await makeBoard(
    page.request,
    session,
    projectId,
    "Harbour",
    2,
    { cols: 2 },
  );
  const variableId = newId("variable");
  const typeId = newId("component_type");
  const attributeId = newId("attribute_def");
  const componentId = newId("component");
  await apply(page.request, session, projectId, [
    {
      type: "variable.create",
      payload: {
        id: variableId,
        key: "gold",
        type: "int",
        default: 0,
        scope: "global",
      },
    },
    {
      type: "component_type.create",
      payload: { id: typeId, name: "Character" },
    },
    {
      type: "attribute.create",
      payload: {
        id: attributeId,
        component_type_id: typeId,
        key: "trust",
        name: "Trust",
        type: "int",
        runtime: true,
      },
    },
    {
      type: "component.create",
      payload: {
        id: componentId,
        component_type_id: typeId,
        key: "elara",
        name: "Elara",
      },
    },
  ]);
  return {
    session,
    projectId,
    board,
    variableId,
    attributeId,
    componentId,
  };
}

/** Opens the editor of the text of the first node of the board, Scene 1.1, by two presses on it, and returns the editor's form and its text. */
async function openText(page: Page, board: string) {
  const canvas = canvasOf(page, board);
  await drawn(canvas);
  const at = await pointOf(canvas, { x: 120, y: 64 });
  if (test.info().project.use.hasTouch) {
    await page.touchscreen.tap(at.x, at.y);
    await page.touchscreen.tap(at.x, at.y);
  } else {
    await page.mouse.click(at.x, at.y);
    await page.mouse.click(at.x, at.y);
  }
  const editor = page.getByRole("form", {
    name: message("canvas.text_editor", { node: "Scene 1.1" }),
  });
  const text = editor.getByRole("textbox", {
    name: message("canvas.text_of", { node: "Scene 1.1" }),
  });
  await expect(text).toBeVisible();
  return { editor, text };
}

/** Returns what the API has of the node or the connection at path, of projectId. */
async function read(
  request: APIRequestContext,
  projectId: string,
  path: string,
): Promise<{ rev: number; text?: unknown; label?: unknown }> {
  const res = await request.get(`/api/v1/projects/${projectId}/${path}`);
  expect(res.status(), await res.text()).toBe(200);
  return res.json();
}

/** Returns the item of the canvas's tree whose name starts with start. */
function treeItem(page: Page, board: string, start: string): Locator {
  return page
    .getByRole("tree", { name: message("canvas.tree.label", { board }) })
    .getByRole("treeitem", { name: new RegExp(`^${start}`) });
}

test.describe("a project's rich text, its value references and its mentions", () => {
  // Verifies: FR-TEXT-03/AC1, NFR-A11Y-08, NFR-TOUCH-01
  test("inserts a mention with @ and a value reference with { in an element's text, and lists the mention in its component's usages", async ({
    page,
  }) => {
    const content = await makeContent(page);
    await page.goto(
      `/app/projects/${content.projectId}/boards/${content.board.boardId}`,
    );
    const { editor, text } = await openText(page, content.board.name);
    await text.focus();
    await page.keyboard.press("Control+End");
    await page.keyboard.type(" Ask @Ela");
    const mentions = editor.getByRole("listbox", {
      name: en["richtext.suggest.mentions"],
    });
    await expect(mentions.getByRole("option")).toHaveText([/^@Elara/]);
    // The list of what @ offers, open, is accessible, and each of its
    // offers is a finger's target.
    expect(await violations(page)).toEqual([]);
    if (test.info().project.use.hasTouch) {
      expect(await smallTargets(page)).toEqual([]);
    }
    await press(mentions.getByRole("option", { name: /^@Elara/ }));
    await expect(mentions).toBeHidden();
    await expect(text).toBeFocused();
    await page.keyboard.type(" for {gol:2");
    const values = editor.getByRole("listbox", {
      name: en["richtext.suggest.values"],
    });
    await expect(values.getByRole("option")).toHaveText([/^\{gold:2\}/]);
    await page.keyboard.press("Enter");
    await expect(values).toBeHidden();
    await expect(text).toContainText("Ask @Elara for {gold:2}");
    await press(editor.getByRole("button", { name: en["canvas.text_save"] }));
    await expect(editor).toBeHidden();
    // The text stores the IDs of what it refers to.
    const node = await read(
      page.request,
      content.projectId,
      `nodes/${content.board.nodeIds[0]}`,
    );
    const inline = JSON.stringify(node.text);
    expect(inline).toContain(
      JSON.stringify({ attrs: { id: content.componentId }, type: "mention" }),
    );
    expect(inline).toContain(
      JSON.stringify({
        attrs: { decimals: 2, ref: content.variableId },
        type: "valueRef",
      }),
    );
    // The component's list of mentions lists the node's text, whose link
    // opens it on the canvas.
    await page.goto(
      `/app/projects/${content.projectId}/components?component=${content.componentId}`,
    );
    await press(page.getByRole("button", { name: en["usage.show"] }));
    const mentioned = page.getByRole("list", { name: "Mentioned in" });
    await press(mentioned.getByRole("link", { name: "The text of Scene 1.1" }));
    await expect(page).toHaveURL(
      new RegExp(`\\?node=${content.board.nodeIds[0]}$`),
    );
  });

  // Verifies: FR-TEXT-02/AC1
  test("shows a variable's new key in every text that refers to it once another session renames it, without a change of the texts", async ({
    page,
  }) => {
    const content = await makeContent(page);
    const [first, second] = content.board.nodeIds;
    const connectionId = newId("connection");
    const reference = (ref: string, attribute?: string) => ({
      type: "valueRef",
      attrs: attribute === undefined ? { ref } : { ref, attribute },
    });
    await apply(page.request, content.session, content.projectId, [
      {
        type: "node.update",
        payload: {
          id: first,
          text: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [
                  { type: "text", text: "You owe " },
                  reference(content.variableId),
                  { type: "text", text: " to Elara, who trusts you " },
                  reference(content.componentId, content.attributeId),
                ],
              },
            ],
          },
        },
      },
      {
        type: "connection.create",
        payload: {
          id: connectionId,
          from_node_id: first,
          to_node_id: second,
          label: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [
                  { type: "text", text: "Pay " },
                  reference(content.variableId),
                ],
              },
            ],
          },
        },
      },
    ]);
    const nodeBefore = await read(
      page.request,
      content.projectId,
      `nodes/${first}`,
    );
    const connectionBefore = await read(
      page.request,
      content.projectId,
      `connections/${connectionId}`,
    );
    await page.goto(
      `/app/projects/${content.projectId}/boards/${content.board.boardId}`,
    );
    const board = content.board.name;
    await drawn(canvasOf(page, board));
    const node = treeItem(page, board, "Scene 1.1");
    await expect(node).toHaveAccessibleName(
      /You owe \{gold\} to Elara, who trusts you \{elara\.trust\}$/,
    );
    // Another session renames the variable, the component's key, and the
    // attribute's.
    await apply(page.request, content.session, content.projectId, [
      {
        type: "variable.rename",
        payload: { id: content.variableId, key: "debt" },
      },
      {
        type: "component.set_key",
        payload: { id: content.componentId, key: "vey" },
      },
      {
        type: "attribute.set_key",
        payload: { id: content.attributeId, key: "faith" },
      },
    ]);
    await expect(node).toHaveAccessibleName(
      /You owe \{debt\} to Elara, who trusts you \{vey\.faith\}$/,
    );
    // The connection's label, as the tree lists it under its node, beside
    // the connection without a label that the board was made with.
    await node.focus();
    await page.keyboard.press("ArrowRight");
    await expect(
      page
        .getByRole("tree", { name: message("canvas.tree.label", { board }) })
        .getByRole("treeitem", {
          name: message("canvas.tree.connection_labelled", {
            target: "Scene 1.2",
            label: "Pay {debt}",
          }),
          exact: true,
        }),
    ).toHaveCount(1);
    // The editor of the node's text shows the new names too.
    const { editor, text } = await openText(page, board);
    await expect(text).toHaveText(
      "You owe {debt} to Elara, who trusts you {vey.faith}",
    );
    await press(editor.getByRole("button", { name: en["canvas.text_cancel"] }));
    // The texts are as they were stored, at the revisions that they had.
    expect(
      await read(page.request, content.projectId, `nodes/${first}`),
    ).toMatchObject({ rev: nodeBefore.rev, text: nodeBefore.text });
    expect(
      await read(
        page.request,
        content.projectId,
        `connections/${connectionId}`,
      ),
    ).toMatchObject({
      rev: connectionBefore.rev,
      label: connectionBefore.label,
    });
    // A page that reads them anew shows the new names as well.
    await page.reload();
    await drawn(canvasOf(page, board));
    await expect(treeItem(page, board, "Scene 1.1")).toHaveAccessibleName(
      /You owe \{debt\} to Elara, who trusts you \{vey\.faith\}$/,
    );
  });
});
