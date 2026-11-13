// What the user types after { or @ in a rich text, which the editor offers
// the project's values or its components for (PRD FR-TEXT-02, FR-TEXT-03,
// plan note N73): a plugin that finds the trigger and what was typed after
// it before the caret, in the state of each transaction, and hands the keys
// that the list takes, while it is open, to the editor's component, which
// shows the list and inserts what is chosen (insertChosen).

import type { Node as PMNode } from "@tiptap/pm/model";
import {
  type EditorState,
  Plugin,
  PluginKey,
  TextSelection,
} from "@tiptap/pm/state";
import type { EditorView } from "@tiptap/pm/view";

/** What a suggestion is of: { of a value to show (FR-TEXT-02), and @ of a component to mention (FR-TEXT-03). */
export type Trigger = "{" | "@";

/** What the user is typing after a trigger, which the editor offers choices for. */
export interface Suggestion {
  trigger: Trigger;
  /** Where the trigger is in the document, which what is chosen replaces from. */
  from: number;
  /** Where the caret is, after what was typed after the trigger, which what is chosen replaces up to. */
  to: number;
  /** What was typed after the trigger. */
  query: string;
}

/** The longest that what is typed after a trigger is, in UTF-16 code units, as an attribute's name, its component's key, and a format, which no name is longer than. */
export const MAX_QUERY = 200;

/**
 * The pattern of a trigger and what was typed after it, at the end of the
 * text before the caret: { or @ at the start of the text, or after a
 * character that is not a letter, a digit, or _, so that the @ of an email
 * address, as keeper@example.com, offers nothing, then what was typed,
 * without white space, braces, @, or anything but text, which the object
 * replacement character stands for, as a mention, a value reference, or a
 * line break, so that a space ends the suggestion.
 */
const triggered = /(^|[^\p{L}\p{N}_])([{@])([^\s{}@\uFFFC]*)$/u;

/**
 * Returns what the user is typing after a trigger in state, or undefined
 * where they are not: where the selection is not a caret, where no trigger
 * is before it in its paragraph as triggered finds it, where what was typed
 * after it is longer than MAX_QUERY, and in code, which shows a brace or an
 * at sign as it is, where the trigger or what was typed after it has the
 * code mark, or what is typed at the caret would.
 */
export function suggestionAt(state: EditorState): Suggestion | undefined {
  const { selection, schema } = state;
  if (!(selection instanceof TextSelection) || !selection.empty) {
    return undefined;
  }
  const $caret = selection.$from;
  if (!$caret.parent.isTextblock) {
    return undefined;
  }
  // Each leaf, as a mention or a line break, is one position, as is each
  // character of text, so an index into what is before the caret is an
  // offset in the paragraph. What is read is what a trigger, what may be
  // typed after it, and the character before it fit in, rather than the
  // whole paragraph before the caret, as of 100,000 characters, at each
  // keystroke; a trigger at its start, but not the paragraph's, has a
  // character before it that is not read, and is not one that triggered
  // takes for the paragraph's start.
  const start = Math.max(0, $caret.parentOffset - MAX_QUERY - 2);
  const before = $caret.parent.textBetween(
    start,
    $caret.parentOffset,
    undefined,
    "\uFFFC",
  );
  const match = triggered.exec(before);
  if (
    match === null ||
    match[3].length > MAX_QUERY ||
    (start > 0 && match.index === 0 && match[1] === "")
  ) {
    return undefined;
  }
  const from = $caret.start() + start + match.index + match[1].length;
  const to = $caret.pos;
  const code = schema.marks.code;
  if (
    code !== undefined &&
    (state.doc.rangeHasMark(from, to, code) ||
      code.isInSet(state.storedMarks ?? $caret.marks()) !== undefined)
  ) {
    return undefined;
  }
  return { trigger: match[2] as Trigger, from, to, query: match[3] };
}

/** What the plugin keeps: the suggestion that is open, and where the one that the user dismissed starts, if they did. */
interface SuggestState {
  open: Suggestion | undefined;
  dismissed: number | undefined;
}

const suggestKey = new PluginKey<SuggestState>("richTextSuggest");

/** Returns the suggestion that is open in state, if one is. */
export function suggestionOf(state: EditorState): Suggestion | undefined {
  return suggestKey.getState(state)?.open;
}

/**
 * Closes the suggestion that is open in view, as Escape does: it stays
 * closed while the caret is after its trigger, as the user types on, and
 * opens again only once the caret has left it, as after a space, or for
 * another trigger, so that what they type after a brace that they meant as
 * one is not offered anything.
 */
export function dismissSuggestion(view: EditorView): void {
  view.dispatch(
    view.state.tr.setMeta(suggestKey, "dismiss").setMeta("addToHistory", false),
  );
}

/**
 * Returns the plugin of suggestions, which hands each key that goes down
 * while one is open to onKey, which reports whether it took it, as an arrow
 * that moves through the list, Enter that chooses, or Escape, which closes
 * it (dismissSuggestion): a key that an input method takes while it
 * composes, as Enter, which ends what was typed in Japanese, is the input
 * method's, as Safari tells of it by its keyCode alone, 229.
 */
export function suggester(
  onKey: (event: KeyboardEvent, open: Suggestion, view: EditorView) => boolean,
): Plugin<SuggestState> {
  return new Plugin<SuggestState>({
    key: suggestKey,
    state: {
      init: (_, state) => ({ open: suggestionAt(state), dismissed: undefined }),
      apply: (tr, value, _, state) => {
        let dismissed = value.dismissed;
        if (dismissed !== undefined && tr.docChanged) {
          // A dismissed trigger that was deleted is no longer dismissed.
          const mapped = tr.mapping.mapResult(dismissed, 1);
          dismissed = mapped.deletedAfter ? undefined : mapped.pos;
        }
        if (tr.getMeta(suggestKey) === "dismiss" && value.open !== undefined) {
          dismissed = tr.mapping.map(value.open.from, 1);
        }
        const found = suggestionAt(state);
        if (dismissed !== undefined && found?.from !== dismissed) {
          dismissed = undefined;
        }
        return {
          open:
            found !== undefined && found.from !== dismissed ? found : undefined,
          dismissed,
        };
      },
    },
    props: {
      handleKeyDown: (view, event) => {
        const open = suggestionOf(view.state);
        if (
          open === undefined ||
          !view.editable ||
          event.isComposing ||
          event.keyCode === 229
        ) {
          return false;
        }
        return onKey(event, open, view);
      },
    },
  });
}

/**
 * Puts node, a value reference or a mention, in place of the trigger of
 * open and what was typed after it, with the marks of the trigger, as bold,
 * and the caret after it, as one step of the editor's undo.
 */
export function insertChosen(
  view: EditorView,
  open: Suggestion,
  node: PMNode,
): void {
  const { state } = view;
  const marks = state.doc.nodeAt(open.from)?.marks ?? [];
  const tr = state.tr.replaceWith(open.from, open.to, node.mark(marks));
  tr.setSelection(TextSelection.create(tr.doc, open.from + node.nodeSize));
  view.dispatch(tr.scrollIntoView());
}
