import { TextSelection } from "@tiptap/pm/state";
import { describe, expect, test } from "vitest";
import { testEditor } from "../test/editor";
import {
  dismissSuggestion,
  insertChosen,
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
    // Nor after a letter of two code units, as U+20000, whose first half
    // is just before what is read of the longest query.
    expect(
      suggestionAt(
        editorOf(`${"x".repeat(300)}\u{20000}{${"b".repeat(MAX_QUERY)}`).state,
      ),
    ).toBeUndefined();
    expect(
      suggestionAt(
        editorOf(`${"x ".repeat(150)}\u{20000} {${"b".repeat(MAX_QUERY)}`)
          .state,
      )?.query,
    ).toHaveLength(MAX_QUERY);
  });

  test("offers nothing after a letter whose accent or vowel sign is a mark of its own, as in a word", () => {
    // café written as e and U+0301, and नमस्ते, which ends in a vowel sign.
    expect(suggestionAt(editorOf("cafe\u0301@ex").state)).toBeUndefined();
    expect(
      suggestionAt(editorOf("\u0928\u092e\u0938\u094d\u0924\u0947{go").state),
    ).toBeUndefined();
    expect(suggestionAt(editorOf("cafe\u0301 @ex").state)?.query).toBe("ex");
  });

  test("puts what is chosen in place of the trigger as an undo step of its own, which gives back what was typed", () => {
    const editor = editorOf("Pay ");
    // Typed just before it is chosen, which the undo's history would join
    // to the choice.
    editor.commands.insertContent("{go");
    const open = suggestionOf(editor.state);
    expect(open?.query).toBe("go");
    if (open !== undefined) {
      insertChosen(
        editor.view,
        open,
        editor.schema.nodes.valueRef.create({ ref: "var_gold" }),
      );
    }
    expect(editor.state.doc.firstChild?.lastChild?.type.name).toBe("valueRef");
    editor.commands.undo();
    expect(editor.getText()).toBe("Pay {go");
    editor.commands.undo();
    expect(editor.getText()).toBe("Pay ");
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
