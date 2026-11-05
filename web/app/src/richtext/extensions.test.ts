import type { RichTextDoc } from "@storycreep/shared/api";
import { describe, expect, test } from "vitest";
import { testEditor } from "../test/editor";
import { storedOf } from "./convert";

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
});
