import type { RichTextDoc } from "@storycreep/shared/api";
import { Slice } from "@tiptap/pm/model";
import { describe, expect, test } from "vitest";
import { testEditor } from "../test/editor";
import { storedOf } from "./convert";
import { pasteProps } from "./extensions";

const p = (value: string) => ({
  content: [{ text: value, type: "text" }],
  type: "paragraph",
});

describe("the editor's extensions", () => {
  test("link an address typed without a scheme, and an email address, as the link's field does", () => {
    const editor = testEditor();
    editor.commands.insertContent("see example.com ");
    editor.commands.insertContent("or keeper@example.com ");
    const linked = (value: string, href: string) => ({
      marks: [{ attrs: { href }, type: "link" }],
      text: value,
      type: "text",
    });
    expect(storedOf(editor.state.doc).doc).toEqual({
      content: [
        {
          content: [
            { text: "see ", type: "text" },
            linked("example.com", "https://example.com"),
            { text: " or ", type: "text" },
            linked("keeper@example.com", "mailto:keeper@example.com"),
            { text: " ", type: "text" },
          ],
          type: "paragraph",
        },
      ],
      type: "doc",
    });
  });

  test("a URL pasted over the selection links it where a link may lead to the URL, and is pasted as text over it otherwise", () => {
    const editor = testEditor({
      content: [p("harbour")],
      type: "doc",
    } as RichTextDoc);
    editor.commands.selectAll();
    editor.view.pasteText("ftp://files.example.com");
    expect(storedOf(editor.state.doc).doc?.content).toEqual([
      p("ftp://files.example.com"),
    ]);
    editor.commands.setContent(p("harbour"));
    editor.commands.selectAll();
    editor.view.pasteText("example.com");
    expect(storedOf(editor.state.doc).doc?.content).toEqual([
      {
        content: [
          {
            marks: [{ attrs: { href: "https://example.com" }, type: "link" }],
            text: "harbour",
            type: "text",
          },
        ],
        type: "paragraph",
      },
    ]);
  });

  test("keep a change within the schema wherever it is in a long text, though it sets only a mark", () => {
    const paragraphs = Array.from({ length: 50 }, (_, i) => p(`line ${i}`));
    const editor = testEditor({
      content: paragraphs,
      type: "doc",
    } as RichTextDoc);
    const last = editor.state.doc.content.size - 1;
    editor.view.dispatch(editor.state.tr.insertText("‏!", last));
    const link = editor.schema.marks.link.create({ href: "javascript:x()" });
    editor.view.dispatch(editor.state.tr.addMark(200, 204, link));
    const stored = storedOf(editor.state.doc).doc;
    expect(stored?.content.at(-1)).toEqual(p("line 49!"));
    expect(JSON.stringify(stored)).not.toContain("link");
  });

  test("keep a pair of surrogates, and a carriage return and a line feed, that a mark's boundary splits one character", () => {
    const editor = testEditor();
    const { schema } = editor;
    const bold = schema.marks.bold.create();
    // x, the two halves of 😀, and y; bold up to the first half.
    editor.commands.insertContent("x😀y");
    editor.view.dispatch(editor.state.tr.addMark(1, 3, bold));
    expect(storedOf(editor.state.doc).doc?.content).toEqual([
      {
        content: [
          { marks: [{ type: "bold" }], text: "x😀", type: "text" },
          { text: "y", type: "text" },
        ],
        type: "paragraph",
      },
    ]);
    editor.commands.clearContent();
    editor.view.dispatch(
      editor.state.tr.insert(1, [
        schema.text("a\r"),
        schema.text("\nb", [bold]),
      ]),
    );
    expect(storedOf(editor.state.doc).doc?.content).toEqual([
      {
        content: [
          { text: "a", type: "text" },
          { type: "hardBreak" },
          { marks: [{ type: "bold" }], text: "b", type: "text" },
        ],
        type: "paragraph",
      },
    ]);
  });

  test("Tab nests the selected list items only where none of their lists would nest deeper than lists nest", () => {
    const item = (text: string, ...lists: object[]) => ({
      content: [p(text), ...lists],
      type: "listItem",
    });
    const bullets = (...items: object[]) => ({
      content: items,
      type: "bulletList",
    });
    // a, b, and c, whose list x is in, in a list at depth 4.
    let list = bullets(item("a"), item("b"), item("c", bullets(item("x"))));
    for (let depth = 3; depth >= 1; depth--) {
      list = bullets(item(`level ${depth}`, list));
    }
    const content = { content: [list], type: "doc" } as RichTextDoc;
    const editor = testEditor(content);
    const at = (text: string) => {
      let found = 0;
      editor.state.doc.descendants((node, pos) => {
        if (node.text === text) {
          found = pos;
        }
      });
      return found;
    };
    // From b into c: c's list would be at depth 6.
    editor.commands.setTextSelection({ from: at("b"), to: at("c") + 1 });
    editor.commands.keyboardShortcut("Tab");
    expect(storedOf(editor.state.doc).doc).toEqual(content);
    // b alone moves into a list of a, at depth 5.
    editor.commands.setTextSelection(at("b"));
    editor.commands.keyboardShortcut("Tab");
    expect(storedOf(editor.state.doc).doc).not.toEqual(content);
  });

  test("Shift-Tab moves the selected list items up only into the list that holds their list, and leaves the focus to move back otherwise", () => {
    const item = (text: string, ...lists: object[]) => ({
      content: [p(text), ...lists],
      type: "listItem",
    });
    const content = {
      content: [
        {
          content: [item("a", { content: [item("b")], type: "bulletList" })],
          type: "bulletList",
        },
      ],
      type: "doc",
    } as RichTextDoc;
    const editor = testEditor(content);
    const at = (text: string) => {
      let found = 0;
      editor.state.doc.descendants((node, pos) => {
        if (node.text === text) {
          found = pos;
        }
      });
      return found;
    };
    // Whether the editor takes Shift-Tab, which the browser otherwise takes
    // to move the focus back.
    const shiftTab = () =>
      editor.view.someProp("handleKeyDown", (handle) =>
        handle(
          editor.view,
          new KeyboardEvent("keydown", { key: "Tab", shiftKey: true }),
        ),
      ) === true;
    // a is in no list within a list item.
    editor.commands.setTextSelection(at("a"));
    expect(shiftTab()).toBe(false);
    expect(storedOf(editor.state.doc).doc).toEqual(content);
    // b moves up into a's list.
    editor.commands.setTextSelection(at("b"));
    expect(shiftTab()).toBe(true);
    expect(storedOf(editor.state.doc).doc).toEqual({
      content: [{ content: [item("a"), item("b")], type: "bulletList" }],
      type: "doc",
    });
  });

  test("setLink sets a URL as the link's field takes it, and refuses one that no link may lead to", () => {
    const editor = testEditor({
      content: [p("harbour")],
      type: "doc",
    } as RichTextDoc);
    editor.commands.selectAll();
    expect(editor.commands.setLink({ href: "javascript:alert(1)" })).toBe(
      false,
    );
    expect(editor.commands.setLink({ href: "example.com" })).toBe(true);
    expect(storedOf(editor.state.doc).doc?.content).toEqual([
      {
        content: [
          {
            marks: [{ attrs: { href: "https://example.com" }, type: "link" }],
            text: "harbour",
            type: "text",
          },
        ],
        type: "paragraph",
      },
    ]);
  });

  test("draw a link's element with its URL only where it is one that a link stores, as not of a link without a URL or with a relative one", () => {
    const linked = (attrs: object | undefined) => ({
      content: [
        {
          marks: [{ ...(attrs === undefined ? {} : { attrs }), type: "link" }],
          text: "harbour",
          type: "text",
        },
      ],
      type: "paragraph",
    });
    const editor = testEditor({
      content: [
        linked(undefined),
        linked({ href: 5 }),
        linked({ href: "example.com" }),
        linked({ href: "https://example.com/tide" }),
      ],
      type: "doc",
    } as unknown as RichTextDoc);
    expect(
      Array.from(editor.view.dom.querySelectorAll("a"), (a) =>
        a.getAttribute("href"),
      ),
    ).toEqual([null, null, null, "https://example.com/tide"]);
  });

  test("drop the plain text of what is dropped from outside the editor at the place that it is dropped at, where the cleaning keeps nothing of its HTML", () => {
    const editor = testEditor({
      content: [p("harbour")],
      type: "doc",
    } as RichTextDoc);
    const drop = (text: string) =>
      ({
        clientX: 0,
        clientY: 0,
        dataTransfer: {
          getData: (type: string) => (type === "text/plain" ? text : ""),
        },
      }) as unknown as DragEvent;
    // The place before "bour", which jsdom, which lays nothing out, cannot
    // find from the drop's coordinates.
    editor.view.posAtCoords = () => ({ pos: 4, inside: -1 });
    const { handleDrop } = pasteProps;
    expect(handleDrop(editor.view, drop(""), Slice.empty, false)).toBe(false);
    expect(handleDrop(editor.view, drop("x²"), Slice.empty, true)).toBe(false);
    expect(handleDrop(editor.view, drop("x²"), Slice.empty, false)).toBe(true);
    expect(storedOf(editor.state.doc).doc?.content).toEqual([p("harx²bour")]);
  });
});
