// What a form of an entity shows while the user edits it (PRD FR-RT-02 AC3):
// the entity as the page has it, which the project's document, or a read of
// the page's, changes as it changes elsewhere, until the user is in the
// form, with the focus, or has changed something in it, and then as it was
// once they came in, so that such a change does not take away the focus, a
// selection, or what the user typed, until the focus leaves the form with
// nothing changed, or the form lets go of it, as once it is saved. The form
// is shown anew, with controls of its own, once what it shows changes (key),
// and the focus, if it was in the form, is on the control in its place that
// had it, or that opened what had it in a portal, which goes with the form
// shown anew, so that the form holds what it shows for as long as the focus is
// in it, rather than lose the focus to the page, and hold what it showed
// once the user has left it (FR-RT-02 AC3).

import {
  type FocusEvent,
  type KeyboardEvent,
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
 * shown anew has it in too: its controls, then its editable texts, as a
 * rich text's, which are no form control: the elements that are editable,
 * or say that they are not, of which none that holds them does (the editing
 * hosts, rather than the atoms within them that ProseMirror marks), and then
 * the controls of its toolbars, as a rich text's, which one that may not be
 * changed has none of, so that the other controls keep their places,
 * though the form shown anew has a toolbar more or fewer.
 */
function controlsOf(form: HTMLFormElement): Element[] {
  const controls = [...form.elements];
  const inToolbar = (element: Element) => {
    const toolbar = element.closest('[role="toolbar"]');
    return toolbar !== null && form.contains(toolbar);
  };
  return [
    ...controls.filter((element) => !inToolbar(element)),
    ...Array.from(form.querySelectorAll("[contenteditable]")).filter(
      (element) => !element.parentElement?.closest("[contenteditable]"),
    ),
    ...controls.filter(inToolbar),
  ];
}

/** The props of an editor's form that tell the keys that went down in it from one held as it opened (useOwnKeys). */
export interface OwnKeys {
  onKeyDownCapture: (event: KeyboardEvent) => void;
  onKeyUpCapture: (event: KeyboardEvent) => void;
}

/**
 * Returns the props of an editor's form that keep the keys that went down
 * in it, by their places, whose repeats, as the user holds one, are the
 * user's typing: a key held as the form opened, as the Enter on what opened
 * it, goes on repeating into what the form gave the focus, where its
 * repeats do nothing until it goes up, rather than submit the form, or add
 * a paragraph for each, which the user never typed. The canvas's editor of
 * a text and the script view's forms share it.
 */
export function useOwnKeys(): OwnKeys {
  const pressed = useRef(new Set<string>());
  return {
    onKeyDownCapture: (event) => {
      if (!event.repeat) {
        pressed.current.add(event.code);
      } else if (!pressed.current.has(event.code)) {
        event.preventDefault();
        event.stopPropagation();
      }
    },
    onKeyUpCapture: (event) => {
      pressed.current.delete(event.code);
    },
  };
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
  // the focus, while one has, or that opened what has it in a portal.
  const focused = useRef<number>(undefined);
  // The place of the control that the focus last left, until the form lets
  // go, which the focus in what that control opened in a portal, as a rich
  // text's link dialog, is at (props.onFocus).
  const left = useRef<number>(undefined);
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
        const at = controlsOf(event.currentTarget).indexOf(
          event.target as Element,
        );
        // The focus in what a control opened in a portal, outside the
        // form's elements, is at that control, which the form shown anew,
        // without what was opened, gives it back to, rather than to the page.
        focused.current =
          at < 0 && !event.currentTarget.contains(event.target as Node)
            ? (left.current ?? at)
            : at;
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
        left.current = focused.current;
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
          left.current = undefined;
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
