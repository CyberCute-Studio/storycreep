// What a form of an entity shows while the user edits it (PRD FR-RT-02 AC3):
// the entity as the page has it, which the project's document, or a read of
// the page's, changes as it changes elsewhere, until the user is in the
// form, with the focus, or has changed something in it, and then as it was
// once they came in, so that such a change does not take away the focus, a
// selection, or what the user typed, until the focus leaves the form with
// nothing changed, or the form lets go of it, as once it is saved. The form
// is shown anew, with controls of its own, once what it shows changes (key),
// and the focus, if it was in the form, is on the control in its place that
// had it, so that the form holds what it shows for as long as the focus is
// in it, rather than lose the focus to the page, and hold what it showed
// once the user has left it (FR-RT-02 AC3).

import {
  type FocusEvent,
  type RefObject,
  useCallback,
  useRef,
  useState,
} from "react";

/** What a form shows of an entity, and how it holds it. */
export interface Held<T> {
  /** What the form shows. */
  shown: T;
  /** The key of the form, which shows it anew once what it shows changes. */
  key: string;
  /** The form, as useFocusRefusal finds its fields. */
  form: RefObject<HTMLFormElement | null>;
  /** The props of the form, which hold what it shows as the user comes in, and let go of it as they leave. */
  props: {
    ref: (form: HTMLFormElement | null) => void;
    onFocus: (event: FocusEvent<HTMLFormElement>) => void;
    onChange: () => void;
    onBlur: (event: FocusEvent<HTMLFormElement>) => void;
  };
  /**
   * Holds value, what the user changed over the entity as it is now, as
   * once a save was refused for a change made elsewhere since, which the
   * form shows anew, and which the focus leaving it does not let go.
   */
  hold: (value: T) => void;
  /**
   * Holds the entity as the page has it now while the focus is in the form,
   * and lets go of it otherwise, as once a save found nothing changed.
   */
  holdNow: () => void;
  /** Lets go of what the form holds, which it shows anew as the page has it. */
  release: () => void;
}

/**
 * Returns what of a form the focus may be in, in an order that the form
 * shown anew has it in too: its controls, and then its editable texts, as a
 * rich text's, which are no form control: the elements that are editable,
 * or say that they are not, of which none that holds them does (the editing
 * hosts, rather than the atoms within them that ProseMirror marks).
 */
function controlsOf(form: HTMLFormElement): Element[] {
  return [
    ...form.elements,
    ...Array.from(form.querySelectorAll("[contenteditable]")).filter(
      (element) => !element.parentElement?.closest("[contenteditable]"),
    ),
  ];
}

/** Returns what a form of entity shows, and how it holds it. */
export function useHeld<T extends { rev: number }>(entity: T): Held<T> {
  const form = useRef<HTMLFormElement>(null);
  // What the form holds, and whether it is what the user changed over the
  // entity (hold), which the form shows anew, as a key of its own, once it
  // lets go of it, though the entity is of the same revision.
  const [held, setHeld] = useState<{ value: T; typed: boolean }>();
  // Whether the user has changed something in the form since it held the
  // entity, which the focus leaving it then does not let go.
  const changed = useRef(false);
  // The place, among the form's controls (controlsOf), of the one that has
  // the focus, while one has.
  const focused = useRef<number>(undefined);
  // The letting go of the form that the focus leaving it waits with
  // (props.onBlur).
  const leaving = useRef<ReturnType<typeof setTimeout>>(undefined);
  const shown = held?.value ?? entity;
  // The form shown anew has the focus where it had it: its controls before
  // went with it, without the form being told that the focus left them, as
  // React tells of no blur while it changes the page.
  const ref = useCallback((node: HTMLFormElement | null) => {
    form.current = node;
    const at = focused.current;
    if (
      node === null ||
      at === undefined ||
      at < 0 ||
      node.contains(node.ownerDocument.activeElement)
    ) {
      return;
    }
    const control = controlsOf(node)[at];
    if (control instanceof HTMLElement) {
      control.focus();
    }
  }, []);
  const holdEntity = () =>
    setHeld((before) => before ?? { value: entity, typed: false });
  return {
    shown,
    key: `${shown.rev}${held?.typed ? "+" : ""}`,
    form,
    props: {
      ref,
      onFocus: (event) => {
        clearTimeout(leaving.current);
        leaving.current = undefined;
        focused.current = controlsOf(event.currentTarget).indexOf(
          event.target as Element,
        );
        holdEntity();
      },
      onChange: () => {
        changed.current = true;
        holdEntity();
      },
      onBlur: (event) => {
        if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
          return;
        }
        // The page itself lost the focus, as to another tab or window, and
        // the control that had it has it again once the user is back, as
        // the form held what it showed meanwhile.
        if (event.target === event.currentTarget.ownerDocument.activeElement) {
          return;
        }
        // The form shown anew meanwhile, as once a save ends, leaves the
        // focus where the user took it.
        focused.current = undefined;
        // What a control of the form opened in a portal, as a rich text's
        // link dialog or a menu, is outside the form's elements, but React
        // tells the form of the focus in it (onFocus), right after this, as
        // the focus moves: the form lets go only once it has not, rather
        // than let go and hold again, which would show it anew, without
        // what was opened, where what it shows changed meanwhile.
        clearTimeout(leaving.current);
        leaving.current = setTimeout(() => {
          leaving.current = undefined;
          if (!changed.current) {
            setHeld(undefined);
          }
        });
      },
    },
    hold: (value) => {
      changed.current = true;
      setHeld({ value, typed: true });
    },
    holdNow: () => {
      changed.current = false;
      setHeld(
        form.current?.contains(document.activeElement)
          ? { value: entity, typed: false }
          : undefined,
      );
    },
    release: () => {
      changed.current = false;
      setHeld(undefined);
    },
  };
}
