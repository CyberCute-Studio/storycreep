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
import { OfferFrame, OfferList, OfferOption } from "../ui/Offers";
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

/** Returns what tells one offer from another, whichever place in the list it has: a value's component's or variable's ID and its attribute's, or a component's ID. */
function offerKey(offer: Offer): string {
  return offer.kind === "value"
    ? `${offer.choice.ref} ${offer.choice.attribute ?? ""}`
    : offer.choice.id;
}

/** The keys that the list takes while it is open: those that move through it, choose from it, and close it. */
const listKeys: ReadonlySet<string> = new Set([
  "ArrowDown",
  "ArrowUp",
  "Enter",
  "Escape",
]);

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
  // The offer highlighted: the one moved to, of the suggestion as it is,
  // wherever the list has it now, as once another session added a value
  // that sorts before it, or the first, as once more was typed, or once the
  // list no longer has it.
  const [moved, setMoved] = useState<{ at: string; offer: string }>();
  const indexOf = (at: string, now: readonly Offer[]) => {
    const i =
      moved?.at === at ? now.findIndex((o) => offerKey(o) === moved.offer) : 0;
    return Math.max(i, 0);
  };
  const index = open === undefined ? -1 : indexOf(suggestionKey(open), offers);
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
  // text is as the key goes down, though the list has not rendered it yet:
  // the list's own, of which one with Shift, Alt, Ctrl, or Cmd is the
  // text's, as Shift and an arrow, which selects, or Shift and Enter, which
  // breaks the line; what the list offers is found only for those, rather
  // than for each key that types.
  useEffect(() => {
    keys.current = (event, at, view) => {
      if (
        !listKeys.has(event.key) ||
        event.shiftKey ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
      ) {
        return false;
      }
      if (event.key === "Escape") {
        dismissSuggestion(view);
        return true;
      }
      const now = offersOf(choices, at).offers;
      if (now.length === 0) {
        return false;
      }
      const key = suggestionKey(at);
      const i = indexOf(key, now);
      const moveTo = (next: number) =>
        setMoved({ at: key, offer: offerKey(now[next]) });
      switch (event.key) {
        case "ArrowDown":
          moveTo((i + 1) % now.length);
          return true;
        case "ArrowUp":
          moveTo((i - 1 + now.length) % now.length);
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
  // a combobox does, which the offer scrolls into view (OfferOption);
  // ProseMirror leaves alone the attributes of its element that its props do
  // not set.
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
        // Below the text, as a field's offers are (OfferFrame).
        <OfferFrame className="space-y-1 border-t border-border p-2">
          {offers.length > 0 ? (
            <OfferList
              id={listId}
              label={
                values
                  ? t("richtext.suggest.values")
                  : t("richtext.suggest.mentions")
              }
              maxHeight="max-h-60"
            >
              {offers.map((offer, i) => (
                <OfferOption
                  key={offerKey(offer)}
                  id={optionId(i)}
                  selected={i === index}
                  onChoose={() => {
                    const at = suggestionOf(editor.state);
                    if (at !== undefined) {
                      choose(offer, at);
                    }
                  }}
                  layout="flex-wrap items-center gap-x-3"
                >
                  <span className="min-w-0 break-words">
                    {offerText(offer)}
                  </span>
                  <span className="min-w-0 break-words text-fg-muted">
                    {offer.choice.detail}
                  </span>
                </OfferOption>
              ))}
            </OfferList>
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
        </OfferFrame>
      )}
    </>
  );
}
