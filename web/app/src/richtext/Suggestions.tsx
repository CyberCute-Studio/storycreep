// The list of what the { or @ typed in a rich text offers (PRD FR-TEXT-02,
// FR-TEXT-03, plan note N73): the values or the components that what was
// typed after it names, below the text, within the editor's frame, rather
// than over the text at the caret, so that it is wherever the editor is, as
// in a sheet that keeps the rest of the page inert, or the box over a node
// on the canvas, and is not cut off by what holds it. The text keeps the
// focus, as a combobox's pattern has it: its arrows move through the list
// (aria-activedescendant), Enter inserts what is highlighted, Escape closes
// the list, keeping what was typed, and a click or a tap on an offer
// inserts it, without taking the focus, so that the on-screen keyboard
// stays.

import type { EditorView } from "@tiptap/pm/view";
import { type Editor, useEditorState } from "@tiptap/react";
import { type RefObject, useEffect, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { type Offer, offersOf, type RichTextChoices } from "./choices";
import { valueChipText } from "./extensions";
import {
  dismissSuggestion,
  insertChosen,
  type Suggestion,
  suggestionOf,
} from "./suggest";

/** What takes the keys of a suggestion that is open, as the editor's extensions hand them on (onSuggestionKey). */
export type SuggestionKeys = (
  event: KeyboardEvent,
  open: Suggestion,
  view: EditorView,
) => boolean;

/** Returns what tells one suggestion from another, which the highlight is of. */
function suggestionKey(open: Suggestion): string {
  return `${open.from} ${open.trigger}${open.query}`;
}

/** Returns what an offer shows of what it inserts: a value as its reference shows it, with the format typed, and a component as its mention does. */
function offerText(offer: Offer): string {
  return offer.kind === "value"
    ? valueChipText(offer.choice.name, offer.decimals)
    : `@${offer.choice.name}`;
}

/**
 * The list of the suggestion that is open in editor, while the text has the
 * focus, of choices, whose keys it takes from keys.
 */
export function Suggestions({
  editor,
  choices,
  keys,
}: {
  editor: Editor;
  choices: RichTextChoices;
  keys: RefObject<SuggestionKeys | undefined>;
}) {
  const { t } = useTranslation();
  const listId = useId();
  const state = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      open: suggestionOf(e.state),
      focused: e.isFocused,
    }),
  });
  const open = state.focused ? state.open : undefined;
  const { offers, total } = useMemo(
    () =>
      open === undefined ? { offers: [], total: 0 } : offersOf(choices, open),
    [choices, open],
  );
  // The offer highlighted: the one moved to, of the suggestion as it is, or
  // the first, as once more was typed.
  const [moved, setMoved] = useState<{ at: string; index: number }>();
  const indexOf = (at: string, count: number) =>
    moved?.at === at ? Math.min(moved.index, count - 1) : 0;
  const index =
    open === undefined ? -1 : indexOf(suggestionKey(open), offers.length);
  const optionId = (i: number) => `${listId}-${i}`;
  const activeId =
    open !== undefined && offers.length > 0 ? optionId(index) : undefined;

  const choose = (offer: Offer, at: Suggestion) => {
    const { nodes } = editor.schema;
    insertChosen(
      editor.view,
      at,
      offer.kind === "mention"
        ? nodes.mention.create({ id: offer.choice.id })
        : nodes.valueRef.create({
            ref: offer.choice.ref,
            attribute: offer.choice.attribute ?? null,
            decimals: offer.decimals ?? null,
          }),
    );
  };

  // The keys of the suggestion, as the plugin gives it, which is as the
  // text is as the key goes down, though the list has not rendered it yet.
  // A key with Alt, Ctrl, or Cmd is the text's, and so is Shift and Enter,
  // which breaks the line.
  useEffect(() => {
    keys.current = (event, at, view) => {
      if (event.altKey || event.ctrlKey || event.metaKey) {
        return false;
      }
      if (event.key === "Escape") {
        dismissSuggestion(view);
        return true;
      }
      const now = offersOf(choices, at).offers;
      if (now.length === 0 || (event.key === "Enter" && event.shiftKey)) {
        return false;
      }
      const key = suggestionKey(at);
      const i = indexOf(key, now.length);
      switch (event.key) {
        case "ArrowDown":
          setMoved({ at: key, index: (i + 1) % now.length });
          return true;
        case "ArrowUp":
          setMoved({ at: key, index: (i - 1 + now.length) % now.length });
          return true;
        case "Enter":
          choose(now[i], at);
          return true;
      }
      return false;
    };
  });
  useEffect(
    () => () => {
      keys.current = undefined;
    },
    [keys],
  );

  // The text names the list and the offer highlighted while it is open, as
  // a combobox does; ProseMirror leaves alone the attributes of its element
  // that its props do not set.
  useEffect(() => {
    if (editor.isDestroyed) {
      return;
    }
    const dom = editor.view.dom;
    if (activeId === undefined) {
      dom.removeAttribute("aria-controls");
      dom.removeAttribute("aria-activedescendant");
      return;
    }
    dom.setAttribute("aria-controls", listId);
    dom.setAttribute("aria-activedescendant", activeId);
    document.getElementById(activeId)?.scrollIntoView?.({ block: "nearest" });
  }, [editor, listId, activeId]);
  useEffect(
    () => () => {
      if (!editor.isDestroyed) {
        editor.view.dom.removeAttribute("aria-controls");
        editor.view.dom.removeAttribute("aria-activedescendant");
      }
    },
    [editor],
  );

  const values = open?.trigger === "{";
  const status =
    open === undefined
      ? ""
      : values
        ? t("richtext.suggest.values_count", { count: total })
        : t("richtext.suggest.mentions_count", { count: total });
  return (
    <>
      <p role="status" className="sr-only">
        {status}
      </p>
      {open !== undefined && (
        <div className="space-y-1 border-t border-border p-1">
          {offers.length > 0 ? (
            <div
              id={listId}
              role="listbox"
              aria-label={
                values
                  ? t("richtext.suggest.values")
                  : t("richtext.suggest.mentions")
              }
              className="flex max-h-60 flex-col gap-2 overflow-y-auto"
            >
              {offers.map((offer, i) => (
                // The text's keys move through the offers and choose one
                // (aria-activedescendant), which keeps the focus in the
                // text, so an offer takes none itself, nor as it is
                // pressed.
                // biome-ignore lint/a11y/useFocusableInteractive: as above.
                // biome-ignore lint/a11y/useKeyWithClickEvents: as above.
                <div
                  key={
                    offer.kind === "value"
                      ? `${offer.choice.ref} ${offer.choice.attribute ?? ""}`
                      : offer.choice.id
                  }
                  id={optionId(i)}
                  role="option"
                  aria-selected={i === index}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => {
                    const at = suggestionOf(editor.state);
                    if (at !== undefined) {
                      choose(offer, at);
                    }
                  }}
                  className="flex min-h-11 cursor-pointer flex-wrap items-center gap-x-3 rounded px-3 aria-selected:bg-surface-hover"
                >
                  <span className="min-w-0 break-words">
                    {offerText(offer)}
                  </span>
                  <span className="min-w-0 break-words text-fg-muted">
                    {offer.choice.detail}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="px-3 py-2 text-fg-muted">
              {values
                ? t("richtext.suggest.no_values")
                : t("richtext.suggest.no_mentions")}
            </p>
          )}
          {total > offers.length && (
            <p className="px-3 text-fg-muted">
              {t("richtext.suggest.more", { count: total - offers.length })}
            </p>
          )}
          {values && (
            <p className="px-3 text-fg-muted">
              {t("richtext.suggest.format_hint")}
            </p>
          )}
        </div>
      )}
    </>
  );
}
