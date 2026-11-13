// The list of what a field offers to complete, below it, by a combobox's
// pattern, as the rich text's { and @ (plan note N73) and the expression
// editor (N74) show one: the field keeps the focus, and names the list
// (aria-controls) and the offer highlighted (aria-activedescendant), whose
// keys move through it and choose one; the offer highlighted is scrolled
// into view; a click or a tap chooses one without taking the focus, so that
// the on-screen keyboard stays; and the offers are 8 px from the field, and
// from each other, at least 44 px high, as a finger's targets are (PRD
// NFR-TOUCH-01). What each field offers, and how its keys work the list, is
// its own.

import { type ReactNode, useEffect, useRef } from "react";

/**
 * The frame of a list of offers, under the field: a press anywhere on it,
 * on an offer or between two, or on what it says, leaves the focus in the
 * field, so that one that misses an offer closes neither the list nor the
 * on-screen keyboard.
 */
export function OfferFrame({
  className,
  children,
}: {
  className: string;
  children: ReactNode;
}) {
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: a press on it does nothing but leave the focus in the field, whose keys work the list.
    <div className={className} onMouseDown={(event) => event.preventDefault()}>
      {children}
    </div>
  );
}

/** The listbox of the offers, of the ID that the field names (aria-controls), which scrolls, as high as maxHeight's class makes it at most. */
export function OfferList({
  id,
  label,
  maxHeight,
  children,
}: {
  id: string;
  label: string;
  /** The class of the list's most height, as max-h-60. */
  maxHeight: string;
  children: ReactNode;
}) {
  return (
    <div
      id={id}
      role="listbox"
      aria-label={label}
      className={`flex flex-col gap-2 overflow-y-auto ${maxHeight}`}
    >
      {children}
    </div>
  );
}

/**
 * An offer of the list, of the ID that the field names while it is
 * highlighted (aria-activedescendant, selected), which is scrolled into
 * view then, as the arrows move to one that the list's height hides, and
 * which a click or a tap chooses (onChoose). The field's keys move through
 * the offers and choose one, which keeps the focus in the field, so an
 * offer takes none itself, nor as it is pressed, as its frame has it; the
 * layout of what it shows is layout's classes.
 */
export function OfferOption({
  id,
  selected,
  onChoose,
  layout,
  children,
}: {
  id: string;
  selected: boolean;
  onChoose: () => void;
  layout: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // Once it is highlighted, and once the list has it elsewhere, of another
  // ID, as once more is typed.
  // biome-ignore lint/correctness/useExhaustiveDependencies: id is what tells its place in the list.
  useEffect(() => {
    if (selected) {
      ref.current?.scrollIntoView?.({ block: "nearest" });
    }
  }, [selected, id]);
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: the field's keys work the offers (aria-activedescendant), and the field keeps the focus.
    // biome-ignore lint/a11y/useKeyWithClickEvents: as above.
    <div
      ref={ref}
      id={id}
      role="option"
      aria-selected={selected}
      onClick={onChoose}
      className={`flex min-h-11 cursor-pointer rounded px-3 aria-selected:bg-surface-hover ${layout}`}
    >
      {children}
    </div>
  );
}
