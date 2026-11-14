// The app's buttons. Each is at least 44 pixels square, the size of a
// fingertip (PRD FR-EDIT-09), and its hover state changes only its colours
// (web/app/lint/hover.ts).

import type { ComponentProps, ReactNode } from "react";

const variants = {
  primary: "bg-accent text-accent-fg hover:bg-accent-hover",
  secondary: "border border-border bg-surface text-fg hover:bg-surface-hover",
  // A destructive action, as revoking a session. The theme has no darker
  // danger, so hover underlines its label instead.
  danger: "bg-danger text-danger-fg hover:underline",
} as const;

export interface ButtonProps extends ComponentProps<"button"> {
  variant?: keyof typeof variants;
}

/**
 * Returns the classes of a button of variant, which a link that looks like
 * one, as among a toolbar's buttons, has too, of the same size.
 */
export function buttonClass(variant: keyof typeof variants = "secondary") {
  return `inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center gap-2 rounded-md px-4 font-medium disabled:cursor-not-allowed disabled:opacity-60 ${variants[variant]}`;
}

/**
 * The classes of a toggle button that is pressed, as a toolbar's, with
 * aria-pressed: filled with the accent, rather than ringed with it, which
 * the focus ring, of the same colour and width, would only move 2 px out on
 * the button pressed (NFR-A11Y-04); and, in a system's forced colours,
 * which replace the page's own and drop the fill, filled with the system's
 * colours of a selection. The rich-text editor's toolbar and the canvas's
 * share them, and a link of the page shown has the same of aria-current
 * (currentClass), which a change of these changes too.
 */
export const pressedClass =
  "aria-pressed:border-accent aria-pressed:bg-accent aria-pressed:text-accent-fg aria-pressed:hover:bg-accent-hover forced-colors:aria-pressed:bg-[Highlight] forced-colors:aria-pressed:hover:bg-[Highlight] forced-colors:aria-pressed:text-[HighlightText]";

/**
 * The classes of a link, as a button, of the page that is shown, which the
 * router marks aria-current, as the board's page's links of its views:
 * those of a toggle button that is pressed (pressedClass), of aria-current
 * in place of aria-pressed.
 */
export const currentClass =
  "aria-[current=page]:border-accent aria-[current=page]:bg-accent aria-[current=page]:text-accent-fg aria-[current=page]:hover:bg-accent-hover forced-colors:aria-[current=page]:bg-[Highlight] forced-colors:aria-[current=page]:hover:bg-[Highlight] forced-colors:aria-[current=page]:text-[HighlightText]";

/** A button, which does not submit a form unless its type says so. */
export function Button({
  variant = "secondary",
  className = "",
  type = "button",
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={`${buttonClass(variant)} ${className}`}
      {...props}
    />
  );
}

/**
 * A button that its own press may leave with nothing to do, as the
 * inspector's that moves a choice up once the choice is the first, or with
 * nothing to do for a while, as the canvas's Paste while the paste that it
 * asked for is under way: where it is unavailable, it says so
 * (aria-disabled), looks so, and does nothing, rather than be disabled,
 * which would drop the keyboard's focus that it has, to the page's content
 * (WCAG 2.4.3), and which a dialog that it opened could not give back to it
 * as it closes, as the error console's Clear keeps its own
 * (errors/ErrorConsole.tsx).
 */
export function LastingButton({
  unavailable,
  onClick,
  className = "",
  ...props
}: ButtonProps & { unavailable: boolean }) {
  return (
    <Button
      {...props}
      aria-disabled={unavailable || undefined}
      className={`aria-disabled:cursor-not-allowed aria-disabled:opacity-60 ${className}`}
      onClick={(event) => {
        if (unavailable) {
          // A button that submits a form submits none.
          event.preventDefault();
        } else {
          onClick?.(event);
        }
      }}
    />
  );
}

/** What MoreButton reads of a list's query, a page at a time (pagedQuery). */
interface MoreOf {
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
}

/**
 * The button that shows the next page of list, while it has one, which is
 * disabled while that page is read.
 */
export function MoreButton({
  list,
  children,
}: {
  list: MoreOf;
  children: ReactNode;
}) {
  if (!list.hasNextPage) {
    return null;
  }
  return (
    <Button
      disabled={list.isFetchingNextPage}
      onClick={() => void list.fetchNextPage()}
    >
      {children}
    </Button>
  );
}
