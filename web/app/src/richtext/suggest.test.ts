import { TextSelection } from "@tiptap/pm/state";
import { describe, expect, test } from "vitest";
import { testEditor } from "../test/editor";
import {
  dismissSuggestion,
  MAX_QUERY,
  suggestionAt,
  suggestionOf,
} from "./suggest";

/** Returns an editor of one paragraph of text, with the caret at its end. */
function editorOf(text: string) {
  const editor = testEditor({
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }],
  });
  editor.view.dispatch(
    editor.state.tr.setSelection(
      TextSelection.create(editor.state.doc, text.length + 1),
    ),
  );
  return editor;
}

describe("what the user types after { or @", () => {
  test("is found at the end of a long paragraph by what is before the caret alone", () => {
    const long = `${"x".repeat(99_990)} {go`;
    expect(suggestionAt(editorOf(long).state)).toEqual({
      trigger: "{",
      from: long.length - 2,
      to: long.length + 1,
      query: "go",
    });
    const most = `${"x ".repeat(200)}@${"b".repeat(MAX_QUERY)}`;
    expect(suggestionAt(editorOf(most).state)?.query).toHaveLength(MAX_QUERY);
    expect(suggestionAt(editorOf(`${most}b`).state)).toBeUndefined();
    // Not after a letter, however far the caret is from the paragraph's
    // start.
    expect(
      suggestionAt(editorOf(`${"x".repeat(300)}{${"b".repeat(150)}`).state),
    ).toBeUndefined();
    expect(
      suggestionAt(editorOf(`{${"b".repeat(MAX_QUERY)}`).state)?.from,
    ).toBe(1);
  });

  test("stays closed once dismissed while the caret stays after its trigger, and offers again once the trigger is typed anew", () => {
    const editor = editorOf("Pay {go");
    expect(suggestionOf(editor.state)?.query).toBe("go");
    dismissSuggestion(editor.view);
    expect(suggestionOf(editor.state)).toBeUndefined();
    editor.commands.insertContent("ld");
    expect(suggestionOf(editor.state)).toBeUndefined();
    // The brace and what follows it deleted, and a brace typed in its place.
    editor.commands.deleteRange({ from: 5, to: 10 });
    expect(editor.getText()).toBe("Pay ");
    editor.commands.insertContent("{");
    expect(suggestionOf(editor.state)).toMatchObject({ from: 5, query: "" });
  });
});
