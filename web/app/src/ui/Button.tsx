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
 * share them.
 */
export const pressedClass =
  "aria-pressed:border-accent aria-pressed:bg-accent aria-pressed:text-accent-fg aria-pressed:hover:bg-accent-hover forced-colors:aria-pressed:bg-[Highlight] forced-colors:aria-pressed:hover:bg-[Highlight] forced-colors:aria-pressed:text-[HighlightText]";

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
