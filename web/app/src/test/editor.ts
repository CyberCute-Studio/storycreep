// An editor of the app's rich text for a test, outside React, in jsdom,
// which lacks the layout that ProseMirror reads to scroll the selection into
// view: a range's rectangles, and the element at a point, which this gives
// it as empty.

import type { RichTextDoc } from "@storycreep/shared/api";
import { emptyRichText } from "@storycreep/shared/richtext";
import { Editor } from "@tiptap/core";
import { afterEach } from "vitest";
import { pasteProps, richTextExtensions } from "../richtext/extensions";

if (typeof Range.prototype.getClientRects !== "function") {
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList;
  Range.prototype.getBoundingClientRect = () => new DOMRect();
}
if (typeof document.elementFromPoint !== "function") {
  document.elementFromPoint = () => null;
}
// A paste of pasteHTML and pasteText, which jsdom has no event of: one
// without data, as the content is given to them.
if (typeof globalThis.ClipboardEvent !== "function") {
  globalThis.ClipboardEvent = class extends Event {
    readonly clipboardData = null;
  } as unknown as typeof ClipboardEvent;
}

const editors: Editor[] = [];

afterEach(() => {
  for (const editor of editors.splice(0)) {
    editor.destroy();
  }
});

/** Returns an editor of content, attached to the page, which the test's end destroys. */
export function testEditor(content: RichTextDoc = emptyRichText()): Editor {
  const element = document.createElement("div");
  document.body.append(element);
  const editor = new Editor({
    element,
    content,
    extensions: richTextExtensions({
      unknown: { mention: "component", value: "value" },
    }),
    injectCSS: false,
    editorProps: pasteProps,
  });
  editors.push(editor);
  return editor;
}
