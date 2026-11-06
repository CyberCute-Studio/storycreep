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

const editors: { editor: Editor; element: HTMLElement }[] = [];

afterEach(() => {
  for (const { editor, element } of editors.splice(0)) {
    editor.destroy();
    element.remove();
  }
});

/**
 * Returns an editor of content, attached to the page, which the test's end
 * destroys and takes off the page, with the editor's props for what is
 * pasted, or with editorProps, as {} for ProseMirror's own paste.
 */
export function testEditor(
  content: RichTextDoc = emptyRichText(),
  editorProps: object = pasteProps,
): Editor {
  const element = document.createElement("div");
  document.body.append(element);
  const editor = new Editor({
    element,
    content,
    extensions: richTextExtensions({
      labels: { mention: () => "component", value: () => "value" },
    }),
    injectCSS: false,
    editorProps,
  });
  editors.push({ editor, element });
  return editor;
}
