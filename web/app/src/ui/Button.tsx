// The app's buttons. Each is at least 44 pixels square, the size of a
// fingertip (PRD FR-EDIT-09), and its hover state changes only its colours
// (web/app/lint/hover.ts).

import type { ComponentProps } from "react";

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
      className={`inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center gap-2 rounded-md px-4 font-medium disabled:cursor-not-allowed disabled:opacity-60 ${variants[variant]} ${className}`}
      {...props}
    />
  );
}
